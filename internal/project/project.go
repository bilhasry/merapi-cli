// Package project handles merapi project discovery, configuration and layout.
//
// A project directory looks like:
//
//	merapi.yaml            project configuration
//	openapi.yaml           the OpenAPI 3.1 document
//	tests/                 test scenario files (YAML)
//	environments/          environment definitions (variables, base URLs)
//	.merapi/               local state + encrypted secrets (gitignored)
package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const (
	// ConfigFileName is the marker file identifying an merapi project.
	ConfigFileName = "merapi.yaml"
	// LocalDir holds local state (active environment, encrypted secrets).
	LocalDir = ".merapi"
	// DefaultGraphQLFile is the conventional SDL location at the project root.
	DefaultGraphQLFile = "schema.graphql"
)

// Config mirrors merapi.yaml.
type Config struct {
	Name               string `yaml:"name" json:"name"`
	Version            int    `yaml:"version" json:"version"`
	Spec               string `yaml:"spec" json:"spec"`
	GraphQL            string `yaml:"graphql,omitempty" json:"graphql,omitempty"`
	TestsDir           string `yaml:"tests_dir" json:"tests_dir"`
	EnvironmentsDir    string `yaml:"environments_dir" json:"environments_dir"`
	DefaultEnvironment string `yaml:"default_environment" json:"default_environment"`
}

// state mirrors .merapi/state.yaml (machine-local, not committed).
type state struct {
	ActiveEnvironment string `yaml:"active_environment,omitempty"`
}

// Project is an opened merapi project.
type Project struct {
	Root   string
	Config Config
}

// ErrNotFound is returned when no merapi.yaml is found walking up the tree.
var ErrNotFound = errors.New("not an merapi project (no merapi.yaml found); run `merapi init` to create one")

// Find walks up from dir looking for an merapi project root.
func Find(dir string) (*Project, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	for {
		cfgPath := filepath.Join(abs, ConfigFileName)
		if info, err := os.Stat(cfgPath); err == nil && !info.IsDir() {
			cfg, err := readConfig(cfgPath)
			if err != nil {
				return nil, fmt.Errorf("reading %s: %w", cfgPath, err)
			}
			return &Project{Root: abs, Config: cfg}, nil
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return nil, ErrNotFound
		}
		abs = parent
	}
}

// Init creates a new project skeleton in dir.
func Init(dir, name string, force bool) (*Project, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	cfgPath := filepath.Join(abs, ConfigFileName)
	if _, err := os.Stat(cfgPath); err == nil && !force {
		return nil, fmt.Errorf("%s already exists (use --force to overwrite)", cfgPath)
	}
	if name == "" {
		name = filepath.Base(abs)
	}
	cfg := Config{
		Name:               name,
		Version:            1,
		Spec:               "openapi.yaml",
		TestsDir:           "tests",
		EnvironmentsDir:    "environments",
		DefaultEnvironment: "dev",
	}
	p := &Project{Root: abs, Config: cfg}
	for _, dir := range []string{abs, filepath.Join(abs, cfg.TestsDir), filepath.Join(abs, cfg.EnvironmentsDir), filepath.Join(abs, LocalDir)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	if err := p.SaveConfig(); err != nil {
		return nil, err
	}
	if err := updateGitIgnore(abs); err != nil {
		return nil, err
	}
	return p, nil
}

func readConfig(path string) (Config, error) {
	var cfg Config
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	cfg.withDefaults()
	return cfg, nil
}

func (c *Config) withDefaults() {
	if c.Spec == "" {
		c.Spec = "openapi.yaml"
	}
	if c.TestsDir == "" {
		c.TestsDir = "tests"
	}
	if c.EnvironmentsDir == "" {
		c.EnvironmentsDir = "environments"
	}
	if c.DefaultEnvironment == "" {
		c.DefaultEnvironment = "dev"
	}
}

// SaveConfig writes merapi.yaml.
func (p *Project) SaveConfig() error {
	p.Config.withDefaults()
	data, err := yaml.Marshal(p.Config)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(p.Root, ConfigFileName), data, 0o644)
}

// SpecPath returns the absolute path to the OpenAPI document.
func (p *Project) SpecPath() string { return filepath.Join(p.Root, p.Config.Spec) }

// GraphQLPath returns the absolute path to the GraphQL SDL file the project
// configures, or "" when the project has no graphql key and no schema.graphql
// at its root (i.e. REST-only projects).
func (p *Project) GraphQLPath() string {
	if p.Config.GraphQL != "" {
		return filepath.Join(p.Root, p.Config.GraphQL)
	}
	candidate := filepath.Join(p.Root, DefaultGraphQLFile)
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return candidate
	}
	return ""
}

// TestsPath returns the absolute path to the tests directory.
func (p *Project) TestsPath() string { return filepath.Join(p.Root, p.Config.TestsDir) }

// EnvironmentsPath returns the absolute path to the environments directory.
func (p *Project) EnvironmentsPath() string { return filepath.Join(p.Root, p.Config.EnvironmentsDir) }

// LocalPath returns the absolute path to the .merapi local directory.
func (p *Project) LocalPath() string { return filepath.Join(p.Root, LocalDir) }

// EnvPath returns the path of an environment definition file.
func (p *Project) EnvPath(name string) string {
	return filepath.Join(p.EnvironmentsPath(), name+".yaml")
}

// SecretsPath returns the path of the encrypted secret store for an environment.
func (p *Project) SecretsPath(env string) string {
	return filepath.Join(p.LocalPath(), "secrets."+env+".enc")
}

func (p *Project) readState() state {
	var st state
	if data, err := os.ReadFile(filepath.Join(p.LocalPath(), "state.yaml")); err == nil {
		_ = yaml.Unmarshal(data, &st)
	}
	return st
}

func (p *Project) writeState(st state) error {
	if err := os.MkdirAll(p.LocalPath(), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(st)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(p.LocalPath(), "state.yaml"), data, 0o644)
}

// ActiveEnv returns the environment selected by `merapi env use`, falling back
// to the configured default environment.
func (p *Project) ActiveEnv() string {
	if st := p.readState(); st.ActiveEnvironment != "" {
		return st.ActiveEnvironment
	}
	return p.Config.DefaultEnvironment
}

// SetActiveEnv records the active environment for this machine.
func (p *Project) SetActiveEnv(name string) error {
	return p.writeState(state{ActiveEnvironment: name})
}

// updateGitIgnore ensures local artifacts are ignored by git.
func updateGitIgnore(root string) error {
	const entries = LocalDir + "/\ndist/\n"
	path := filepath.Join(root, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	content := string(data)
	changed := false
	for _, line := range []string{LocalDir + "/", "dist/"} {
		if !containsLine(content, line) {
			if content != "" && content[len(content)-1] != '\n' {
				content += "\n"
			}
			content += line + "\n"
			changed = true
		}
	}
	if !changed && os.IsNotExist(err) {
		return os.WriteFile(path, []byte(entries), 0o644)
	}
	if changed {
		return os.WriteFile(path, []byte(content), 0o644)
	}
	return nil
}

func containsLine(content, line string) bool {
	start := 0
	for i := 0; i <= len(content); i++ {
		if i == len(content) || content[i] == '\n' {
			if content[start:i] == line {
				return true
			}
			start = i + 1
		}
	}
	return false
}
