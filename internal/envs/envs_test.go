package envs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bilhasry/merapi/internal/project"
)

func newTestProject(t *testing.T) *project.Project {
	t.Helper()
	dir := t.TempDir()
	p, err := project.Init(dir, "test-api", false)
	if err != nil {
		t.Fatalf("project.Init: %v", err)
	}
	return p
}

func TestEnvironmentCRUD(t *testing.T) {
	p := newTestProject(t)

	if _, err := Create(p, "dev", "http://localhost:1", false); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := Create(p, "dev", "http://localhost:1", false); err == nil {
		t.Error("duplicate Create should fail without force")
	}
	if _, err := Create(p, "staging", "https://staging.example.com", false); err != nil {
		t.Fatalf("Create(staging): %v", err)
	}

	names, err := List(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "dev" || names[1] != "staging" {
		t.Errorf("List = %v", names)
	}

	env, err := Load(p, "staging")
	if err != nil {
		t.Fatal(err)
	}
	if env.BaseURL != "https://staging.example.com" {
		t.Errorf("BaseURL = %q", env.BaseURL)
	}

	// Variables round-trip.
	if err := SetVariable(p, "dev", "tenant", "acme"); err != nil {
		t.Fatal(err)
	}
	dev, _ := Load(p, "dev")
	if dev.Variables["tenant"] != "acme" {
		t.Errorf("variable not persisted: %v", dev.Variables)
	}
	if err := UnsetVariable(p, "dev", "tenant"); err != nil {
		t.Fatal(err)
	}
	dev, _ = Load(p, "dev")
	if _, ok := dev.Variables["tenant"]; ok {
		t.Error("variable still present after unset")
	}

	// Invalid names rejected.
	if err := ValidateName("../evil"); err == nil {
		t.Error("path traversal name should be rejected")
	}
	if err := SetVariable(p, "dev", "bad-key", "x"); err == nil {
		t.Error("invalid variable key should be rejected")
	}

	// Delete removes env + secrets file.
	if err := Delete(p, "staging"); err != nil {
		t.Fatal(err)
	}
	names, _ = List(p)
	if len(names) != 1 {
		t.Errorf("List after delete = %v", names)
	}
}

func TestSecretsRoundTrip(t *testing.T) {
	p := newTestProject(t)
	if _, err := Create(p, "dev", "http://localhost:1", false); err != nil {
		t.Fatal(err)
	}
	t.Setenv(SecretsKeyEnvVar, "unit-test-passphrase-123")

	// Without a passphrase, set fails with guidance.
	t.Setenv(SecretsKeyEnvVar, "")
	if err := SetSecret(p, "dev", "TOKEN", "abc"); err == nil || !strings.Contains(err.Error(), SecretsKeyEnvVar) {
		t.Errorf("expected ErrNoPassphrase-style error, got %v", err)
	}

	t.Setenv(SecretsKeyEnvVar, "unit-test-passphrase-123")
	if err := SetSecret(p, "dev", "TOKEN", "super-secret-value"); err != nil {
		t.Fatalf("SetSecret: %v", err)
	}
	if err := SetSecret(p, "dev", "PASSWORD", "hunter2"); err != nil {
		t.Fatalf("SetSecret(2): %v", err)
	}

	// The store must not contain plaintext.
	blob, err := os.ReadFile(p.SecretsPath("dev"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), "super-secret-value") {
		t.Error("secret stored in plaintext!")
	}
	info, err := os.Stat(p.SecretsPath("dev"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("secret file mode = %v, want 0600", info.Mode().Perm())
	}

	secrets, err := LoadSecrets(p, "dev")
	if err != nil {
		t.Fatalf("LoadSecrets: %v", err)
	}
	if secrets["TOKEN"] != "super-secret-value" || secrets["PASSWORD"] != "hunter2" {
		t.Errorf("decrypted secrets = %v", secrets)
	}

	// Wrong passphrase fails cleanly.
	t.Setenv(SecretsKeyEnvVar, "wrong-passphrase-xyz")
	if _, err := LoadSecrets(p, "dev"); err == nil {
		t.Error("wrong passphrase should fail decryption")
	}

	// Unset removes the key.
	t.Setenv(SecretsKeyEnvVar, "unit-test-passphrase-123")
	if err := UnsetSecret(p, "dev", "TOKEN"); err != nil {
		t.Fatal(err)
	}
	secrets, _ = LoadSecrets(p, "dev")
	if _, ok := secrets["TOKEN"]; ok {
		t.Error("secret still present after unset")
	}
	if err := UnsetSecret(p, "dev", "MISSING"); err == nil {
		t.Error("unsetting missing secret should fail")
	}
}

func TestActiveEnv(t *testing.T) {
	p := newTestProject(t)
	if got := p.ActiveEnv(); got != "dev" {
		t.Errorf("default active env = %q, want dev", got)
	}
	if _, err := Create(p, "prod", "https://api.example.com", false); err != nil {
		t.Fatal(err)
	}
	if err := p.SetActiveEnv("prod"); err != nil {
		t.Fatal(err)
	}
	if got := p.ActiveEnv(); got != "prod" {
		t.Errorf("active env = %q, want prod", got)
	}
	// State is local to .merapi (gitignored).
	if _, err := os.Stat(filepath.Join(p.LocalPath(), "state.yaml")); err != nil {
		t.Errorf("state file missing: %v", err)
	}
}
