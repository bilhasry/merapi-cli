// Package templ resolves {{variable}} references in requests and documents.
//
// Syntax:
//
//	{{base_url}}        the environment base URL
//	{{name}}            captured value, then environment variable "name"
//	{{secret:NAME}}     secret NAME from the encrypted store
//
// Resolution is strict: unknown references are errors, so misconfigured
// tests fail loudly instead of silently sending literal braces.
package templ

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var refRe = regexp.MustCompile(`\{\{\s*([^{}]+?)\s*\}\}`)

// Context carries everything resolvable by templates.
type Context struct {
	BaseURL string
	Vars    map[string]string // environment variables + captures (captures win)
	Secrets map[string]string
}

// NewContext builds a context from environment-style maps.
func NewContext(baseURL string, vars, secrets map[string]string) *Context {
	if vars == nil {
		vars = map[string]string{}
	}
	if secrets == nil {
		secrets = map[string]string{}
	}
	return &Context{BaseURL: baseURL, Vars: vars, Secrets: secrets}
}

// WithCaptures returns a copy with extra capture variables applied.
func (c *Context) WithCaptures(captures map[string]string) *Context {
	vars := make(map[string]string, len(c.Vars)+len(captures))
	for k, v := range c.Vars {
		vars[k] = v
	}
	for k, v := range captures {
		vars[k] = v
	}
	return &Context{BaseURL: c.BaseURL, Vars: vars, Secrets: c.Secrets}
}

// Resolve expands all references in s.
func (c *Context) Resolve(s string) (string, error) {
	var firstErr error
	out := refRe.ReplaceAllStringFunc(s, func(match string) string {
		groups := refRe.FindStringSubmatch(match)
		if len(groups) != 2 {
			return match
		}
		ref := strings.TrimSpace(groups[1])
		val, err := c.lookup(ref)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		return val
	})
	if firstErr != nil {
		return "", firstErr
	}
	return out, nil
}

// ResolveMap resolves every value of a string map.
func (c *Context) ResolveMap(m map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(m))
	for k, v := range m {
		rv, err := c.Resolve(v)
		if err != nil {
			return nil, fmt.Errorf("in %q: %w", k, err)
		}
		out[k] = rv
	}
	return out, nil
}

// ResolveAny walks arbitrary structures resolving string leaves.
func (c *Context) ResolveAny(v any) (any, error) {
	switch t := v.(type) {
	case nil:
		return nil, nil
	case string:
		return c.Resolve(t)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, item := range t {
			r, err := c.ResolveAny(item)
			if err != nil {
				return nil, err
			}
			out[k] = r
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			r, err := c.ResolveAny(item)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	default:
		return v, nil
	}
}

func (c *Context) lookup(ref string) (string, error) {
	if strings.HasPrefix(ref, "secret:") {
		name := strings.TrimSpace(strings.TrimPrefix(ref, "secret:"))
		v, ok := c.Secrets[name]
		if !ok {
			return "", fmt.Errorf("secret %q is not set for this environment", name)
		}
		return v, nil
	}
	if ref == "base_url" {
		if c.BaseURL == "" {
			return "", fmt.Errorf("the environment has no base_url configured")
		}
		return strings.TrimRight(c.BaseURL, "/"), nil
	}
	if v, ok := c.Vars[ref]; ok {
		return v, nil
	}
	if v, ok := c.Secrets[ref]; ok {
		return v, nil
	}
	return "", undefinedError(ref, c)
}

func undefinedError(ref string, c *Context) error {
	var names []string
	for k := range c.Vars {
		names = append(names, k)
	}
	for k := range c.Secrets {
		names = append(names, "secret:"+k)
	}
	sort.Strings(names)
	avail := "none"
	if len(names) > 0 {
		avail = strings.Join(names, ", ")
	}
	return fmt.Errorf("undefined reference %q (available: %s)", "{{"+ref+"}}", avail)
}

// References returns every reference found in s (useful for linting).
func References(s string) []string {
	var out []string
	for _, m := range refRe.FindAllStringSubmatch(s, -1) {
		if len(m) == 2 {
			out = append(out, strings.TrimSpace(m[1]))
		}
	}
	return out
}
