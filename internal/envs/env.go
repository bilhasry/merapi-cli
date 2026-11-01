// Package envs manages environments: base URLs, plain variables, and an
// encrypted secret store per environment.
package envs

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/bilhasry/merapi/internal/project"
	"gopkg.in/yaml.v3"
)

// Environment is one environment definition (environments/<name>.yaml).
// Variables are plain values safe to commit; secrets live in the encrypted
// store instead (see secrets.go).
type Environment struct {
	Name      string            `yaml:"name" json:"name"`
	BaseURL   string            `yaml:"base_url" json:"base_url"`
	Variables map[string]string `yaml:"variables,omitempty" json:"variables,omitempty"`
}

var nameRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*$`)

// ValidateName checks an environment name is filename- and template-safe.
func ValidateName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("invalid environment name %q (use letters, digits, '-' or '_', starting with a letter)", name)
	}
	return nil
}

// List returns environment names sorted alphabetically.
func List(p *project.Project) ([]string, error) {
	entries, err := os.ReadDir(p.EnvironmentsPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		names = append(names, strings.TrimSuffix(e.Name(), ".yaml"))
	}
	sort.Strings(names)
	return names, nil
}

// Load reads one environment definition.
func Load(p *project.Project, name string) (*Environment, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p.EnvPath(name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("environment %q not found (create it with `merapi env add %s`)", name, name)
		}
		return nil, err
	}
	var env Environment
	if err := yaml.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", p.EnvPath(name), err)
	}
	env.Name = name
	if env.Variables == nil {
		env.Variables = map[string]string{}
	}
	return &env, nil
}

// Save writes an environment definition.
func Save(p *project.Project, env *Environment) error {
	if err := ValidateName(env.Name); err != nil {
		return err
	}
	if err := os.MkdirAll(p.EnvironmentsPath(), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(env)
	if err != nil {
		return err
	}
	return os.WriteFile(p.EnvPath(env.Name), data, 0o644)
}

// Create scaffolds a new environment.
func Create(p *project.Project, name, baseURL string, force bool) (*Environment, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	if _, err := os.Stat(p.EnvPath(name)); err == nil && !force {
		return nil, fmt.Errorf("environment %q already exists (use --force to overwrite)", name)
	}
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	env := &Environment{Name: name, BaseURL: baseURL, Variables: map[string]string{}}
	if err := Save(p, env); err != nil {
		return nil, err
	}
	return env, nil
}

// Delete removes an environment and its encrypted secrets.
func Delete(p *project.Project, name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	path := p.EnvPath(name)
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("environment %q not found", name)
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	_ = os.Remove(p.SecretsPath(name))
	if p.ActiveEnv() == name {
		_ = p.SetActiveEnv("")
	}
	return nil
}

// SetVariable sets one variable on an environment (loading and saving it).
func SetVariable(p *project.Project, envName, key, value string) error {
	if err := ValidateVariableKey(key); err != nil {
		return err
	}
	env, err := Load(p, envName)
	if err != nil {
		return err
	}
	env.Variables[key] = value
	return Save(p, env)
}

// UnsetVariable removes one variable from an environment.
func UnsetVariable(p *project.Project, envName, key string) error {
	env, err := Load(p, envName)
	if err != nil {
		return err
	}
	if _, ok := env.Variables[key]; !ok {
		return fmt.Errorf("variable %q not set in environment %q", key, envName)
	}
	delete(env.Variables, key)
	return Save(p, env)
}

var varKeyRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// ValidateVariableKey checks a variable name is template-safe.
func ValidateVariableKey(key string) error {
	if !varKeyRe.MatchString(key) {
		return fmt.Errorf("invalid variable name %q (use letters, digits and '_', starting with a letter or '_')", key)
	}
	return nil
}

// Resolve picks the effective environment name from: explicit flag, active
// environment, then the configured default.
func Resolve(p *project.Project, flagEnv string) string {
	if flagEnv != "" {
		return flagEnv
	}
	return p.ActiveEnv()
}

// DirIsEmpty reports whether a directory has no entries.
func DirIsEmpty(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return true
	}
	return len(entries) == 0
}
