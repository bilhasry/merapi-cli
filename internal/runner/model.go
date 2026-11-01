// Package runner executes API test scenarios stored as YAML files in the
// project's tests directory.
//
// A scenario is an ordered list of steps; values captured from responses are
// available to later steps via {{name}} templates.
package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Scenario is one test file.
type Scenario struct {
	File        string `yaml:"-" json:"file,omitempty"`
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	Skip        bool   `yaml:"skip,omitempty" json:"skip,omitempty"`
	Steps       []Step `yaml:"steps" json:"steps"`
}

// Step is one request/assertion pair within a scenario.
type Step struct {
	Name    string            `yaml:"name,omitempty" json:"name,omitempty"`
	Skip    bool              `yaml:"skip,omitempty" json:"skip,omitempty"`
	Request Request           `yaml:"request" json:"request"`
	Expect  Expect            `yaml:"expect,omitempty" json:"expect,omitempty"`
	Capture map[string]string `yaml:"capture,omitempty" json:"capture,omitempty"`
}

// Request describes the HTTP call. Templates ({{var}}, {{secret:NAME}},
// {{base_url}}) are expanded before sending.
type Request struct {
	Method    string            `yaml:"method,omitempty" json:"method,omitempty"`
	Path      string            `yaml:"path" json:"path"`
	Headers   map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	Query     map[string]string `yaml:"query,omitempty" json:"query,omitempty"`
	Body      string            `yaml:"body,omitempty" json:"body,omitempty"`
	Json      any               `yaml:"json,omitempty" json:"json,omitempty"`
	TimeoutMS int               `yaml:"timeout_ms,omitempty" json:"timeoutMs,omitempty"`
}

// Expect holds assertions for the response. Zero values skip the check.
type Expect struct {
	Status        int               `yaml:"status,omitempty" json:"status,omitempty"`
	StatusIn      []int             `yaml:"status_in,omitempty" json:"statusIn,omitempty"`
	Headers       map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	BodyContains  []string          `yaml:"body_contains,omitempty" json:"bodyContains,omitempty"`
	Json          map[string]any    `yaml:"json,omitempty" json:"json,omitempty"`
	MaxDurationMS int               `yaml:"max_duration_ms,omitempty" json:"maxDurationMs,omitempty"`
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// Slug turns a scenario name into a file-safe slug.
func Slug(name string) string {
	s := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if s == "" {
		s = "scenario"
	}
	return s
}

// Discover lists scenario files (*.yaml / *.yml) sorted by path.
func Discover(testsDir string) ([]string, error) {
	entries, err := os.ReadDir(testsDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if ext == ".yaml" || ext == ".yml" {
			files = append(files, filepath.Join(testsDir, name))
		}
	}
	sort.Strings(files)
	return files, nil
}

// LoadScenario reads and validates one scenario file.
func LoadScenario(path string) (*Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var sc Scenario
	if err := yaml.Unmarshal(data, &sc); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	sc.File = path
	if sc.Name == "" {
		sc.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	if len(sc.Steps) == 0 {
		return nil, fmt.Errorf("%s: scenario has no steps", path)
	}
	for i, step := range sc.Steps {
		if step.Request.Path == "" {
			return nil, fmt.Errorf("%s: step %d has no request.path", path, i+1)
		}
	}
	return &sc, nil
}

// yamlMarshal marshals a scenario (or any value) to YAML bytes.
func yamlMarshal(v any) ([]byte, error) {
	return yaml.Marshal(v)
}

// SaveScenario writes a scenario back to disk.
func SaveScenario(sc *Scenario, path string) error {
	if sc.Name == "" {
		return fmt.Errorf("scenario name is required")
	}
	data, err := yamlMarshal(sc)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
