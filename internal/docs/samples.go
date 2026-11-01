package docs

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/bilhasry/merapi/internal/spec"
)

// Sample is one ready-to-paste request snippet for an endpoint.
type Sample struct {
	Language string `json:"language"` // slug: curl, go, python, javascript
	Code     string `json:"code"`
}

// sampleLanguages maps slug -> markdown fence language, in display order.
var sampleLanguages = []struct {
	slug  string
	title string
	fence string
}{
	{"curl", "cURL", "bash"},
	{"go", "Go", "go"},
	{"python", "Python", "python"},
	{"javascript", "JavaScript", "javascript"},
}

// SampleLanguages lists the supported sample language slugs.
func SampleLanguages() []string {
	out := make([]string, 0, len(sampleLanguages))
	for _, l := range sampleLanguages {
		out = append(out, l.slug)
	}
	return out
}

// SampleDisplayName returns the human title of a sample language slug.
func SampleDisplayName(slug string) string {
	for _, l := range sampleLanguages {
		if l.slug == slug {
			return l.title
		}
	}
	return slug
}

// SampleFence returns the markdown code-fence language for a slug.
func SampleFence(slug string) string {
	for _, l := range sampleLanguages {
		if l.slug == slug {
			return l.fence
		}
	}
	return slug
}

// BaseURL resolves the base URL used in samples: the given override, else the
// active environment's base URL is expected to be passed in, else the first
// server of the document, else a placeholder.
func BaseURL(doc *spec.Document, override string) string {
	if override != "" {
		return strings.TrimRight(override, "/")
	}
	if servers := doc.Servers(); len(servers) > 0 {
		return strings.TrimRight(servers[0], "/")
	}
	return "https://api.example.com"
}

// Samples generates ready-to-paste request samples for ep. baseURL may be
// empty (a placeholder is used). langs selects slugs from SampleLanguages;
// empty means all. Unknown slugs return an error.
func Samples(doc *spec.Document, ep *spec.Endpoint, baseURL string, langs []string) ([]Sample, error) {
	wanted := map[string]bool{}
	for _, l := range langs {
		wanted[l] = true
	}
	for _, l := range langs {
		if !containsSlug(l) {
			return nil, fmt.Errorf("unknown language %q (supported: %s)", l, strings.Join(SampleLanguages(), ", "))
		}
	}

	req := buildSampleRequest(doc, ep, BaseURL(doc, baseURL))
	var out []Sample
	for _, l := range sampleLanguages {
		if len(wanted) > 0 && !wanted[l.slug] {
			continue
		}
		var code string
		switch l.slug {
		case "curl":
			code = curlSample(req)
		case "go":
			code = goSample(req)
		case "python":
			code = pythonSample(req)
		case "javascript":
			code = jsSample(req)
		}
		out = append(out, Sample{Language: l.slug, Code: strings.TrimRight(code, "\n") + "\n"})
	}
	return out, nil
}

func containsSlug(slug string) bool {
	for _, l := range sampleLanguages {
		if l.slug == slug {
			return true
		}
	}
	return false
}

type kv struct{ Key, Value string }

// sampleRequest is the language-neutral model of one example request.
type sampleRequest struct {
	Method  string
	URL     string
	Headers []kv // sorted, without Content-Type (renderers add it with the body)
	Body    any  // parsed JSON body, nil when the operation takes none
}

func buildSampleRequest(doc *spec.Document, ep *spec.Endpoint, baseURL string) sampleRequest {
	method := strings.ToUpper(strings.TrimSpace(ep.Method))
	if method == "" {
		method = "GET"
	}

	path := ep.Path
	var query, headers []kv
	for _, p := range ep.Parameters {
		value := p.Example
		placeholder := "<" + p.Name + ">"
		switch p.In {
		case "path":
			if value == "" {
				value = placeholder
			}
			path = strings.ReplaceAll(path, "{"+p.Name+"}", value)
		case "query":
			if value == "" {
				if !p.Required {
					continue
				}
				value = placeholder
			}
			query = append(query, kv{p.Name, value})
		case "header":
			if value == "" {
				if !p.Required {
					continue
				}
				value = placeholder
			}
			headers = append(headers, kv{p.Name, value})
		}
	}

	// Secured operations get their credential injected as {{secret:NAME}}
	// placeholders, so samples (and docs embedding them) are copy-paste
	// correct for both humans and test scenarios.
	authHeaders, authQuery := authSampleCredentials(doc, ep)
	headers = append(headers, authHeaders...)
	query = append(query, authQuery...)

	url := baseURL + path
	if len(query) > 0 {
		pairs := make([]string, 0, len(query))
		for _, q := range query {
			pairs = append(pairs, q.Key+"="+q.Value)
		}
		url += "?" + strings.Join(pairs, "&")
	}

	body := requestBodyExample(doc, ep)
	sortKV(headers)
	return sampleRequest{Method: method, URL: url, Headers: headers, Body: body}
}

// authSampleCredentials derives placeholder credentials for the operation's
// effective security requirements (its own, else the document-level default).
// Only the first requirement is shown — samples document one working way in.
func authSampleCredentials(doc *spec.Document, ep *spec.Endpoint) (headers, query []kv) {
	reqs := ep.Security
	if reqs == nil {
		reqs = doc.Security()
	}
	if len(reqs) == 0 {
		return nil, nil
	}
	req := reqs[0]
	names := make([]string, 0, len(req))
	for n := range req {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		scheme := doc.GetSecurityScheme(name)
		if scheme == nil {
			continue
		}
		placeholder := "{{secret:" + secretName(scheme) + "}}"
		switch scheme.Type {
		case "apiKey":
			switch scheme.In {
			case "header":
				headers = append(headers, kv{scheme.ParameterName, placeholder})
			case "query":
				query = append(query, kv{scheme.ParameterName, placeholder})
			case "cookie":
				headers = append(headers, kv{"Cookie", scheme.ParameterName + "=" + placeholder})
			}
		case "http":
			value := placeholder
			if s := strings.ToLower(scheme.Scheme); s == "bearer" || s == "basic" {
				value = strings.Title(s) + " " + placeholder //nolint:staticcheck // simple ASCII scheme names
			}
			headers = append(headers, kv{"Authorization", value})
		case "oauth2", "openIdConnect":
			headers = append(headers, kv{"Authorization", "Bearer " + placeholder})
		}
	}
	return headers, query
}

// secretName derives the placeholder secret name for a scheme: the apiKey
// parameter name (X-API-Key → X_API_KEY), else the scheme name
// (BearerAuth → BEARER_AUTH). CamelCase boundaries become underscores.
func secretName(s *spec.SecurityScheme) string {
	base := s.Name
	if s.Type == "apiKey" && s.ParameterName != "" {
		base = s.ParameterName
	}
	runes := []rune(base)
	var b strings.Builder
	for i, r := range runes {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			// Underscore at camelCase boundaries: aB -> a_B, ABCd -> AB_Cd.
			if i > 0 {
				prevLower := runes[i-1] >= 'a' && runes[i-1] <= 'z'
				prevDigit := runes[i-1] >= '0' && runes[i-1] <= '9'
				nextLower := i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z'
				prevUpper := runes[i-1] >= 'A' && runes[i-1] <= 'Z'
				if prevLower || prevDigit || (prevUpper && nextLower) {
					b.WriteByte('_')
				}
			}
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return strings.ToUpper(b.String())
}

func sortKV(kvs []kv) {
	for i := 1; i < len(kvs); i++ {
		for j := i; j > 0 && kvs[j].Key < kvs[j-1].Key; j-- {
			kvs[j], kvs[j-1] = kvs[j-1], kvs[j]
		}
	}
}

// requestBodyExample returns the example body for an operation: the explicit
// example when set, else one derived from the request schema.
func requestBodyExample(doc *spec.Document, ep *spec.Endpoint) any {
	rb := ep.RequestBody
	if rb == nil {
		return nil
	}
	if rb.Example != nil {
		return rb.Example
	}
	var schema map[string]any
	switch {
	case rb.SchemaRef != "":
		schema = doc.GetSchema(refName(rb.SchemaRef))
	case rb.Schema != nil:
		schema = rb.Schema
	}
	if schema == nil {
		return nil
	}
	return exampleFromSchema(doc, schema, 0)
}

// ExampleFromSchema derives a minimal example value from a schema ($refs
// resolved through the document). It is the same derivation used by Samples
// and by the mock server.
func ExampleFromSchema(doc *spec.Document, schema map[string]any) any {
	return exampleFromSchema(doc, schema, 0)
}

// exampleFromSchema derives a minimal example value from a schema ($refs
// resolved through the document, depth-guarded).
func exampleFromSchema(doc *spec.Document, schema map[string]any, depth int) any {
	if schema == nil || depth > 6 {
		return nil
	}
	if ex, ok := schema["example"]; ok {
		return ex
	}
	if ref, ok := schema["$ref"].(string); ok {
		return exampleFromSchema(doc, doc.GetSchema(refName(ref)), depth+1)
	}
	switch t, _ := schema["type"].(string); t {
	case "object":
		props, _ := schema["properties"].(map[string]any)
		out := map[string]any{}
		for _, name := range keysOfAny(props) {
			sub, _ := props[name].(map[string]any)
			out[name] = exampleFromSchema(doc, sub, depth+1)
		}
		return out
	case "array":
		items, _ := schema["items"].(map[string]any)
		return []any{exampleFromSchema(doc, items, depth+1)}
	case "integer":
		return 0
	case "number":
		return 0
	case "boolean":
		return true
	default:
		if en, ok := schema["enum"].([]any); ok && len(en) > 0 {
			return en[0]
		}
		return "string"
	}
}

// --- per-language renderers -------------------------------------------------

func curlSample(r sampleRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "curl -X %s '%s'", r.Method, r.URL)
	for _, h := range r.Headers {
		fmt.Fprintf(&b, " \\\n  -H '%s: %s'", h.Key, h.Value)
	}
	if r.Body != nil {
		fmt.Fprintf(&b, " \\\n  -H 'Content-Type: application/json'")
		fmt.Fprintf(&b, " \\\n  -d '%s'", jsonBlock(r.Body, "  "))
	}
	return b.String()
}

func goSample(r sampleRequest) string {
	var b strings.Builder
	b.WriteString("package main\n\nimport (\n\t\"fmt\"\n\t\"io\"\n\t\"net/http\"\n")
	if r.Body != nil {
		b.WriteString("\t\"strings\"\n")
	}
	b.WriteString(")\n\nfunc main() {\n")
	if r.Body != nil {
		bodyLit := "`" + strings.ReplaceAll(jsonCompact(r.Body), "`", "'") + "`"
		fmt.Fprintf(&b, "\tbody := strings.NewReader(%s)\n", bodyLit)
		fmt.Fprintf(&b, "\treq, err := http.NewRequest(%q, %s, body)\n", r.Method, strconv.Quote(r.URL))
	} else {
		fmt.Fprintf(&b, "\treq, err := http.NewRequest(%q, %s, nil)\n", r.Method, strconv.Quote(r.URL))
	}
	b.WriteString("\tif err != nil {\n\t\tpanic(err)\n\t}\n")
	if r.Body != nil {
		b.WriteString("\treq.Header.Set(\"Content-Type\", \"application/json\")\n")
	}
	for _, h := range r.Headers {
		fmt.Fprintf(&b, "\treq.Header.Set(%s, %s)\n", strconv.Quote(h.Key), strconv.Quote(h.Value))
	}
	b.WriteString("\tresp, err := http.DefaultClient.Do(req)\n")
	b.WriteString("\tif err != nil {\n\t\tpanic(err)\n\t}\n")
	b.WriteString("\tdefer resp.Body.Close()\n")
	b.WriteString("\tdata, _ := io.ReadAll(resp.Body)\n")
	b.WriteString("\tfmt.Println(resp.Status, string(data))\n}")
	return b.String()
}

func pythonSample(r sampleRequest) string {
	var b strings.Builder
	b.WriteString("import requests\n\nresp = requests.request(\n")
	fmt.Fprintf(&b, "    %q,\n", r.Method)
	fmt.Fprintf(&b, "    %q,\n", r.URL)
	if len(r.Headers) > 0 || r.Body != nil {
		b.WriteString("    headers={\n")
		if r.Body != nil {
			b.WriteString("        \"Content-Type\": \"application/json\",\n")
		}
		for _, h := range r.Headers {
			fmt.Fprintf(&b, "        %q: %q,\n", h.Key, h.Value)
		}
		b.WriteString("    },\n")
	}
	if r.Body != nil {
		fmt.Fprintf(&b, "    json=%s,\n", pyLiteral(r.Body, 1))
	}
	b.WriteString(")\nprint(resp.status_code, resp.text)")
	return b.String()
}

func jsSample(r sampleRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "const resp = await fetch(%s", strconv.Quote(r.URL))
	if r.Method != "GET" || len(r.Headers) > 0 || r.Body != nil {
		b.WriteString(", {\n")
		fmt.Fprintf(&b, "  method: %q,\n", r.Method)
		if len(r.Headers) > 0 || r.Body != nil {
			b.WriteString("  headers: {\n")
			if r.Body != nil {
				b.WriteString("    \"Content-Type\": \"application/json\",\n")
			}
			for _, h := range r.Headers {
				fmt.Fprintf(&b, "    %s: %s,\n", strconv.Quote(h.Key), strconv.Quote(h.Value))
			}
			b.WriteString("  },\n")
		}
		if r.Body != nil {
			fmt.Fprintf(&b, "  body: JSON.stringify(%s),\n", jsonCompact(r.Body))
		}
		b.WriteString("}")
	}
	b.WriteString(");\n\nconsole.log(resp.status, await resp.text())")
	return b.String()
}

// --- helpers ----------------------------------------------------------------

func jsonCompact(v any) string {
	buf, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(buf)
}

// jsonBlock renders an indented JSON body; prefix goes on continuation lines.
func jsonBlock(v any, prefix string) string {
	buf, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "{}"
	}
	lines := strings.Split(string(buf), "\n")
	for i, l := range lines[1:] {
		lines[i+1] = prefix + l
	}
	return strings.Join(lines, "\n")
}

// pyLiteral renders a JSON-shaped value as a Python literal.
func pyLiteral(v any, indent int) string {
	pad := strings.Repeat("    ", indent)
	switch t := v.(type) {
	case map[string]any:
		if len(t) == 0 {
			return "{}"
		}
		var parts []string
		for _, k := range keysOfAny(t) {
			parts = append(parts, fmt.Sprintf("%s%s: %s", pad, strconv.Quote(k), pyLiteral(t[k], indent+1)))
		}
		return "{\n" + strings.Join(parts, ",\n") + "\n" + strings.Repeat("    ", indent-1) + "}"
	case []any:
		if len(t) == 0 {
			return "[]"
		}
		var parts []string
		for _, item := range t {
			parts = append(parts, pad+pyLiteral(item, indent+1))
		}
		return "[\n" + strings.Join(parts, ",\n") + "\n" + strings.Repeat("    ", indent-1) + "]"
	case bool:
		if t {
			return "True"
		}
		return "False"
	case nil:
		return "None"
	case string:
		return strconv.Quote(t)
	default:
		return fmt.Sprint(t)
	}
}
