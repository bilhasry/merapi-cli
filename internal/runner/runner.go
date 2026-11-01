package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/bilhasry/merapi/internal/templ"
)

// Options configures a run.
type Options struct {
	EnvName    string
	BaseURL    string
	Variables  map[string]string
	Secrets    map[string]string
	Filter     string // substring match against file or scenario name
	FailFast   bool
	Timeout    time.Duration
	HTTPClient *http.Client
	Verbose    bool
}

// StepResult is the outcome of one step.
type StepResult struct {
	Name        string            `json:"name,omitempty"`
	Request     string            `json:"request"`
	Status      int               `json:"status,omitempty"`
	DurationMS  int64             `json:"durationMs"`
	Passed      bool              `json:"passed"`
	Skipped     bool              `json:"skipped,omitempty"`
	Failures    []string          `json:"failures,omitempty"`
	BodyExcerpt string            `json:"bodyExcerpt,omitempty"`
	captures    map[string]string // captured values for later steps (not serialized)
}

// ScenarioResult is the outcome of one scenario file.
type ScenarioResult struct {
	File       string       `json:"file"`
	Name       string       `json:"name"`
	Passed     bool         `json:"passed"`
	Skipped    bool         `json:"skipped,omitempty"`
	DurationMS int64        `json:"durationMs"`
	Steps      []StepResult `json:"steps"`
	Error      string       `json:"error,omitempty"`
}

// RunResult aggregates a full run.
type RunResult struct {
	Env        string           `json:"env"`
	Started    time.Time        `json:"started"`
	DurationMS int64            `json:"durationMs"`
	Passed     int              `json:"passed"`
	Failed     int              `json:"failed"`
	Skipped    int              `json:"skipped"`
	Scenarios  []ScenarioResult `json:"scenarios"`
}

// Success reports whether the run had no failures.
func (r *RunResult) Success() bool { return r.Failed == 0 }

// Run executes every scenario in testsDir that matches the filter.
func Run(testsDir string, opts Options) (*RunResult, error) {
	files, err := Discover(testsDir)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	result := &RunResult{Env: opts.EnvName, Started: started}

	for _, file := range files {
		sc, err := LoadScenario(file)
		if err != nil {
			result.Failed++
			result.Scenarios = append(result.Scenarios, ScenarioResult{
				File: file, Name: file, Error: err.Error(),
			})
			continue
		}
		if opts.Filter != "" && !matchesFilter(sc, opts.Filter) {
			continue
		}
		sr := runScenario(sc, opts)
		switch {
		case sr.Skipped:
			result.Skipped++
		case !sr.Passed:
			result.Failed++
		default:
			result.Passed++
		}
		result.Scenarios = append(result.Scenarios, sr)
		if opts.FailFast && !sr.Passed && !sr.Skipped {
			break
		}
	}
	result.DurationMS = time.Since(started).Milliseconds()
	return result, nil
}

func matchesFilter(sc *Scenario, filter string) bool {
	return strings.Contains(sc.File, filter) || strings.Contains(strings.ToLower(sc.Name), strings.ToLower(filter))
}

func runScenario(sc *Scenario, opts Options) ScenarioResult {
	sr := ScenarioResult{File: sc.File, Name: sc.Name, Passed: true}
	if sc.Skip {
		sr.Skipped = true
		sr.Passed = true
		return sr
	}
	vars := map[string]string{}
	for k, v := range opts.Variables {
		vars[k] = v
	}
	ctx := templ.NewContext(opts.BaseURL, vars, opts.Secrets)
	start := time.Now()
	for _, step := range sc.Steps {
		if step.Skip {
			sr.Steps = append(sr.Steps, StepResult{Name: step.Name, Skipped: true, Passed: true})
			continue
		}
		st := runStep(step, ctx, opts)
		sr.Steps = append(sr.Steps, st)
		if !st.Passed {
			sr.Passed = false
			break // skip remaining steps of a failing scenario
		}
		// Captures become available to later steps.
		for k, v := range st.captures {
			ctx.Vars[k] = v
		}
	}
	sr.DurationMS = time.Since(start).Milliseconds()
	return sr
}

// runStep executes a single step and records assertions and captures.
func runStep(step Step, ctx *templ.Context, opts Options) StepResult {
	label := step.Name
	if label == "" {
		label = step.Request.Method + " " + step.Request.Path
	}
	st := StepResult{Name: step.Name}
	reqSpec := step.Request

	method := strings.ToUpper(strings.TrimSpace(reqSpec.Method))
	if method == "" {
		method = http.MethodGet
	}

	// Resolve templates.
	path, err := ctx.Resolve(reqSpec.Path)
	if err != nil {
		return failStep(st, method, reqSpec.Path, err)
	}
	headerVals, err := ctx.ResolveMap(reqSpec.Headers)
	if err != nil {
		return failStep(st, method, path, err)
	}
	queryVals, err := ctx.ResolveMap(reqSpec.Query)
	if err != nil {
		return failStep(st, method, path, err)
	}

	var bodyReader io.Reader
	contentType := ""
	if reqSpec.Json != nil {
		resolved, err := ctx.ResolveAny(reqSpec.Json)
		if err != nil {
			return failStep(st, method, path, err)
		}
		buf, err := json.Marshal(resolved)
		if err != nil {
			return failStep(st, method, path, fmt.Errorf("encoding json body: %w", err))
		}
		bodyReader = bytes.NewReader(buf)
		contentType = "application/json"
	} else if reqSpec.Body != "" {
		body, err := ctx.Resolve(reqSpec.Body)
		if err != nil {
			return failStep(st, method, path, err)
		}
		bodyReader = strings.NewReader(body)
		contentType = "application/json"
	}

	fullURL, err := buildURL(opts.BaseURL, path, queryVals)
	if err != nil {
		return failStep(st, method, path, err)
	}
	st.Request = method + " " + fullURL

	timeout := opts.Timeout
	if reqSpec.TimeoutMS > 0 {
		timeout = time.Duration(reqSpec.TimeoutMS) * time.Millisecond
	}
	httpReq, err := http.NewRequestWithContext(context.Background(), method, fullURL, bodyReader)
	if err != nil {
		return failStep(st, method, path, err)
	}
	for k, v := range headerVals {
		httpReq.Header.Set(k, v)
	}
	if contentType != "" && httpReq.Header.Get("Content-Type") == "" {
		httpReq.Header.Set("Content-Type", contentType)
	}

	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{
			Timeout: timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}

	callStart := time.Now()
	resp, err := client.Do(httpReq)
	elapsed := time.Since(callStart)
	st.DurationMS = elapsed.Milliseconds()
	if err != nil {
		return failStep(st, method, path, fmt.Errorf("request failed: %w", err))
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if readErr != nil {
		return failStep(st, method, path, fmt.Errorf("reading response: %w", readErr))
	}
	st.Status = resp.StatusCode

	var parsed any
	if err := json.Unmarshal(body, &parsed); err != nil {
		parsed = nil
	}

	// Resolve templates inside the expectations (captures, variables, secrets).
	resolvedExpect, expErr := resolveExpect(ctx, step.Expect)
	if expErr != nil {
		return failStep(st, method, path, expErr)
	}

	failures := assertResponse(resolvedExpect, resp, body, parsed, elapsed)

	if len(failures) == 0 && len(step.Capture) > 0 {
		if parsed == nil {
			failures = append(failures, "cannot capture from a non-JSON response body")
		} else {
			captures := map[string]string{}
			var capErr error
			names := make([]string, 0, len(step.Capture))
			for name := range step.Capture {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				expr := step.Capture[name]
				val, err := jsonPath(parsed, expr)
				if err != nil {
					capErr = err
					break
				}
				if val == nil {
					capErr = fmt.Errorf("capture %s: %s not found in response", name, expr)
					break
				}
				captures[name] = stringify(val)
			}
			if capErr != nil {
				failures = append(failures, capErr.Error())
			} else {
				st.captures = captures
			}
		}
	}

	if len(failures) > 0 {
		st.Passed = false
		st.Failures = failures
		st.BodyExcerpt = excerpt(string(body), 400)
	} else {
		st.Passed = true
	}
	return st
}

// resolveExpect resolves template expressions inside an Expect struct so that
// assertions can reference captured variables and secrets, e.g.
//
//	expect:
//	  json:
//	    "$.id": "{{pet_id}}"
func resolveExpect(ctx *templ.Context, exp Expect) (Expect, error) {
	var err error
	resolved := exp
	if exp.Json != nil {
		v, err := ctx.ResolveAny(exp.Json)
		if err != nil {
			return Expect{}, fmt.Errorf("resolving json assertions: %w", err)
		}
		if m, ok := v.(map[string]any); ok {
			resolved.Json = m
		}
	}
	resolved.BodyContains = make([]string, len(exp.BodyContains))
	for i, sub := range exp.BodyContains {
		if resolved.BodyContains[i], err = ctx.Resolve(sub); err != nil {
			return Expect{}, fmt.Errorf("resolving body_contains assertion: %w", err)
		}
	}
	if resolved.Headers, err = ctx.ResolveMap(exp.Headers); err != nil {
		return Expect{}, fmt.Errorf("resolving header assertions: %w", err)
	}
	return resolved, nil
}

// failStep records an error that occurred before the request was sent.
func failStep(st StepResult, method, path string, err error) StepResult {
	st.Request = method + " " + path
	st.Passed = false
	st.Failures = []string{err.Error()}
	return st
}

func buildURL(baseURL, path string, query map[string]string) (string, error) {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return appendQuery(path, query)
	}
	if baseURL == "" {
		return "", fmt.Errorf("no base URL: set one in the environment (merapi env add --base-url) or use an absolute {{base_url}}/path")
	}
	return appendQuery(strings.TrimRight(baseURL, "/")+path, query)
}

func appendQuery(raw string, query map[string]string) (string, error) {
	if len(query) == 0 {
		return raw, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid URL %q: %w", raw, err)
	}
	q := u.Query()
	for k, v := range query {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func assertResponse(exp Expect, resp *http.Response, body []byte, parsed any, elapsed time.Duration) []string {
	var failures []string
	if exp.Status != 0 && resp.StatusCode != exp.Status {
		failures = append(failures, fmt.Sprintf("expected status %d, got %d", exp.Status, resp.StatusCode))
	}
	if len(exp.StatusIn) > 0 {
		ok := false
		for _, s := range exp.StatusIn {
			if resp.StatusCode == s {
				ok = true
				break
			}
		}
		if !ok {
			failures = append(failures, fmt.Sprintf("expected status in %v, got %d", exp.StatusIn, resp.StatusCode))
		}
	}
	for name, want := range exp.Headers {
		got := resp.Header.Get(name)
		if !strings.EqualFold(got, want) {
			failures = append(failures, fmt.Sprintf("expected header %s=%q, got %q", name, want, got))
		}
	}
	text := string(body)
	for _, sub := range exp.BodyContains {
		if !strings.Contains(text, sub) {
			failures = append(failures, fmt.Sprintf("response body does not contain %q", sub))
		}
	}
	if len(exp.Json) > 0 {
		if parsed == nil {
			failures = append(failures, "expected a JSON response body for json assertions, got non-JSON")
		} else {
			names := make([]string, 0, len(exp.Json))
			for name := range exp.Json {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, expr := range names {
				if strings.Contains(expr, "[*]") {
					failures = append(failures, fmt.Sprintf("assertion %s: wildcards are only supported in captures", expr))
					continue
				}
				want := exp.Json[expr]
				got, err := jsonPath(parsed, expr)
				if err != nil {
					failures = append(failures, fmt.Sprintf("assertion %s: %v", expr, err))
					continue
				}
				if msg := compareJSON(expr, want, got); msg != "" {
					failures = append(failures, msg)
				}
			}
		}
	}
	if exp.MaxDurationMS > 0 && elapsed > time.Duration(exp.MaxDurationMS)*time.Millisecond {
		failures = append(failures, fmt.Sprintf("expected response within %dms, took %dms", exp.MaxDurationMS, elapsed.Milliseconds()))
	}
	return failures
}

// compareJSON checks an expectation against an actual value. Expected values:
//
//	"@exists"      value must be present (may be null only if... no: any value)
//	"@not_exists"  value must be absent
//	"@null"        value must be present and null
//	other scalars  loosely compared (numbers numerically)
//	mappings       recursively subset-matched against the actual object
func compareJSON(expr string, want, got any) string {
	switch w := want.(type) {
	case string:
		switch w {
		case "@exists":
			if got == nil {
				return fmt.Sprintf("assertion %s: value does not exist", expr)
			}
			return ""
		case "@not_exists":
			if got != nil {
				return fmt.Sprintf("assertion %s: value exists but should not (got %s)", expr, stringify(got))
			}
			return ""
		case "@null":
			if got != nil {
				return fmt.Sprintf("assertion %s: expected null, got %s", expr, stringify(got))
			}
			return ""
		}
	}
	if want == nil {
		if got != nil {
			return fmt.Sprintf("assertion %s: expected null, got %s", expr, stringify(got))
		}
		return ""
	}
	if wf, wok := toFloat(want); wok {
		gf, gok := toFloat(got)
		if !gok {
			return fmt.Sprintf("assertion %s: expected number %v, got %s", expr, want, stringify(got))
		}
		if wf != gf {
			return fmt.Sprintf("assertion %s: expected %v, got %v", expr, want, got)
		}
		return ""
	}
	switch w := want.(type) {
	case map[string]any:
		gm, ok := got.(map[string]any)
		if !ok {
			return fmt.Sprintf("assertion %s: expected an object, got %s", expr, stringify(got))
		}
		for k, sub := range w {
			subGot, exists := gm[k]
			msg := compareJSON(expr+"."+k, sub, subGot)
			if msg != "" {
				if strings.Contains(msg, "does not exist") && !exists {
					return fmt.Sprintf("assertion %s.%s: property does not exist", expr, k)
				}
				return msg
			}
		}
		return ""
	case []any:
		ga, ok := got.([]any)
		if !ok {
			return fmt.Sprintf("assertion %s: expected an array, got %s", expr, stringify(got))
		}
		if len(w) != len(ga) {
			return fmt.Sprintf("assertion %s: expected %d items, got %d", expr, len(w), len(ga))
		}
		for i, sub := range w {
			if msg := compareJSON(fmt.Sprintf("%s[%d]", expr, i), sub, ga[i]); msg != "" {
				return msg
			}
		}
		return ""
	default:
		if fmt.Sprint(w) != fmt.Sprint(got) {
			return fmt.Sprintf("assertion %s: expected %v, got %s", expr, want, stringify(got))
		}
		return ""
	}
}

func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func stringify(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return "null"
	default:
		buf, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprint(t)
		}
		return string(buf)
	}
}

func excerpt(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
