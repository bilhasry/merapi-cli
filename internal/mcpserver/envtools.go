package mcpserver

import (
	"context"
	"fmt"
	"sort"

	"github.com/bilhasry/merapi/internal/envs"
	"github.com/bilhasry/merapi/internal/project"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type EnvInfo struct {
	Name          string `json:"name"`
	BaseURL       string `json:"baseUrl,omitempty"`
	VariableCount int    `json:"variableCount"`
	SecretCount   int    `json:"secretCount"`
	Active        bool   `json:"active"`
}

type EnvListOut struct {
	Environments []EnvInfo `json:"environments"`
	Active       string    `json:"active"`
}

func envInfo(p *project.Project, name string) EnvInfo {
	info := EnvInfo{Name: name, Active: p.ActiveEnv() == name}
	if env, err := envs.Load(p, name); err == nil {
		info.BaseURL = env.BaseURL
		info.VariableCount = len(env.Variables)
	}
	if secrets, err := envs.LoadSecrets(p, name); err == nil {
		info.SecretCount = len(secrets)
	}
	return info
}

type EnvNameIn struct {
	Name string `json:"name" jsonschema:"environment name, e.g. dev, staging, prod"`
}

// EnvOptionalIn allows omitting the environment (active one is used).
type EnvOptionalIn struct {
	Name string `json:"name,omitempty" jsonschema:"environment name; defaults to the active environment"`
}

type OKOut struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

func registerEnvTools(s *mcp.Server, root string) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "env_list",
		Description: "List environments with base URL, variable/secret counts, and which one is active.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, EnvListOut, error) {
		p, err := openProject(root)
		if err != nil {
			return nil, EnvListOut{}, err
		}
		names, err := envs.List(p)
		if err != nil {
			return nil, EnvListOut{}, err
		}
		out := EnvListOut{Environments: []EnvInfo{}, Active: p.ActiveEnv()}
		for _, name := range names {
			out.Environments = append(out.Environments, envInfo(p, name))
		}
		return nil, out, nil
	})

	type EnvOut struct {
		Name        string            `json:"name"`
		BaseURL     string            `json:"baseUrl,omitempty"`
		Variables   map[string]string `json:"variables,omitempty"`
		SecretNames []string          `json:"secretNames,omitempty"`
		Active      bool              `json:"active"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "env_get",
		Description: "Get one environment: base URL, variables (values visible) and secret names (values never exposed).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in EnvNameIn) (*mcp.CallToolResult, EnvOut, error) {
		p, err := openProject(root)
		if err != nil {
			return nil, EnvOut{}, err
		}
		env, err := envs.Load(p, in.Name)
		if err != nil {
			return nil, EnvOut{}, err
		}
		out := EnvOut{Name: env.Name, BaseURL: env.BaseURL, Variables: env.Variables, Active: p.ActiveEnv() == env.Name}
		if secrets, err := envs.LoadSecrets(p, in.Name); err == nil {
			for k := range secrets {
				out.SecretNames = append(out.SecretNames, k)
			}
			sort.Strings(out.SecretNames)
		}
		return nil, out, nil
	})

	type EnvCreateIn struct {
		Name      string `json:"name" jsonschema:"environment name, e.g. staging"`
		BaseURL   string `json:"baseUrl,omitempty" jsonschema:"base URL for requests, e.g. https://api.staging.example.com"`
		Overwrite bool   `json:"overwrite,omitempty"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "env_create",
		Description: "Create an environment definition (base URL + variables live in environments/<name>.yaml).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in EnvCreateIn) (*mcp.CallToolResult, OKOut, error) {
		lock.Lock()
		defer lock.Unlock()
		p, err := openProject(root)
		if err != nil {
			return nil, OKOut{}, err
		}
		if _, err := envs.Create(p, in.Name, in.BaseURL, in.Overwrite); err != nil {
			return nil, OKOut{}, err
		}
		return nil, OKOut{OK: true, Message: fmt.Sprintf("environment %s created", in.Name)}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "env_use",
		Description: "Select the active environment for subsequent test runs (machine-local setting).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in EnvNameIn) (*mcp.CallToolResult, OKOut, error) {
		lock.Lock()
		defer lock.Unlock()
		p, err := openProject(root)
		if err != nil {
			return nil, OKOut{}, err
		}
		if _, err := envs.Load(p, in.Name); err != nil {
			return nil, OKOut{}, err
		}
		if err := p.SetActiveEnv(in.Name); err != nil {
			return nil, OKOut{}, err
		}
		return nil, OKOut{OK: true, Message: "active environment is now " + in.Name}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "env_delete",
		Description: "Delete an environment and its encrypted secrets.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in EnvNameIn) (*mcp.CallToolResult, OKOut, error) {
		lock.Lock()
		defer lock.Unlock()
		p, err := openProject(root)
		if err != nil {
			return nil, OKOut{}, err
		}
		if err := envs.Delete(p, in.Name); err != nil {
			return nil, OKOut{}, err
		}
		return nil, OKOut{OK: true, Message: fmt.Sprintf("environment %s deleted", in.Name)}, nil
	})

	// --- variables ---
	type VarIn struct {
		Name        string `json:"name" jsonschema:"variable name (letters, digits, _)"`
		Value       string `json:"value,omitempty" jsonschema:"value to set"`
		Environment string `json:"environment,omitempty" jsonschema:"defaults to the active environment"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "var_list",
		Description: "List the variables of an environment (plus its base URL). Use secret_list for secret names.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in EnvOptionalIn) (*mcp.CallToolResult, EnvOut, error) {
		p, err := openProject(root)
		if err != nil {
			return nil, EnvOut{}, err
		}
		name := envs.Resolve(p, in.Name)
		env, err := envs.Load(p, name)
		if err != nil {
			return nil, EnvOut{}, err
		}
		return nil, EnvOut{Name: env.Name, BaseURL: env.BaseURL, Variables: env.Variables}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "var_set",
		Description: "Set a plain (non-secret) environment variable used by test templates like {{name}}.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in VarIn) (*mcp.CallToolResult, OKOut, error) {
		lock.Lock()
		defer lock.Unlock()
		p, err := openProject(root)
		if err != nil {
			return nil, OKOut{}, err
		}
		name := envs.Resolve(p, in.Environment)
		if err := envs.SetVariable(p, name, in.Name, in.Value); err != nil {
			return nil, OKOut{}, err
		}
		return nil, OKOut{OK: true, Message: fmt.Sprintf("%s set in environment %s", in.Name, name)}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "var_unset",
		Description: "Remove a plain environment variable.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in VarIn) (*mcp.CallToolResult, OKOut, error) {
		lock.Lock()
		defer lock.Unlock()
		p, err := openProject(root)
		if err != nil {
			return nil, OKOut{}, err
		}
		name := envs.Resolve(p, in.Environment)
		if err := envs.UnsetVariable(p, name, in.Name); err != nil {
			return nil, OKOut{}, err
		}
		return nil, OKOut{OK: true, Message: fmt.Sprintf("%s removed from environment %s", in.Name, name)}, nil
	})

	// --- secrets ---
	mcp.AddTool(s, &mcp.Tool{
		Name:        "secret_list",
		Description: "List secret NAMES for an environment. Values are encrypted at rest and never returned.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in EnvOptionalIn) (*mcp.CallToolResult, struct {
		Environment string   `json:"environment"`
		SecretNames []string `json:"secretNames"`
	}, error) {
		type outT = struct {
			Environment string   `json:"environment"`
			SecretNames []string `json:"secretNames"`
		}
		p, err := openProject(root)
		if err != nil {
			return nil, outT{}, err
		}
		name := envs.Resolve(p, in.Name)
		out := outT{Environment: name, SecretNames: []string{}}
		secrets, err := envs.LoadSecrets(p, name)
		if err != nil {
			// Missing store or missing key both degrade to an empty list.
			return nil, out, nil
		}
		for k := range secrets {
			out.SecretNames = append(out.SecretNames, k)
		}
		sort.Strings(out.SecretNames)
		return nil, out, nil
	})

	type SecretSetIn struct {
		Name        string `json:"name" jsonschema:"secret name, e.g. API_TOKEN"`
		Value       string `json:"value" jsonschema:"secret value; stored encrypted (AES-256-GCM) and referenced as {{secret:NAME}}"`
		Environment string `json:"environment,omitempty" jsonschema:"defaults to the active environment"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "secret_set",
		Description: "Set an encrypted secret. Requires MERAPI_SECRETS_KEY in the server environment; values are never echoed back.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SecretSetIn) (*mcp.CallToolResult, OKOut, error) {
		lock.Lock()
		defer lock.Unlock()
		p, err := openProject(root)
		if err != nil {
			return nil, OKOut{}, err
		}
		name := envs.Resolve(p, in.Environment)
		if err := envs.SetSecret(p, name, in.Name, in.Value); err != nil {
			return nil, OKOut{}, err
		}
		return nil, OKOut{OK: true, Message: fmt.Sprintf("secret %s stored encrypted in environment %s", in.Name, name)}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "secret_unset",
		Description: "Delete an encrypted secret.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in VarIn) (*mcp.CallToolResult, OKOut, error) {
		lock.Lock()
		defer lock.Unlock()
		p, err := openProject(root)
		if err != nil {
			return nil, OKOut{}, err
		}
		name := envs.Resolve(p, in.Environment)
		if err := envs.UnsetSecret(p, name, in.Name); err != nil {
			return nil, OKOut{}, err
		}
		return nil, OKOut{OK: true, Message: fmt.Sprintf("secret %s removed from environment %s", in.Name, name)}, nil
	})
}
