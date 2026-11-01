package mock

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/bilhasry/merapi/internal/spec"
)

// This file enforces documented security requirements in the mock: an
// operation with effective `security` answers missing credentials with its
// documented 401/403 (or a synthetic 401). Checks are *shape-only* — the mock
// verifies that a credential is present and roughly well-formed, never that
// its value is valid. That makes auth error paths testable without a backend
// while staying deterministic.

// effectiveSecurity returns the requirements applying to an operation: its
// own `security`, else the document-level default. An empty non-nil list
// means "explicitly public".
func (s *Server) effectiveSecurity(ep *spec.Endpoint) []map[string][]string {
	if ep.Security != nil {
		return ep.Security
	}
	return s.doc.Security()
}

// authError reports why a request fails the operation's security
// requirements, or "" when it passes (or the operation is public).
// Requirements are OR'd: satisfying any one grants access; within one
// requirement, every named scheme must be present (AND). An empty
// requirement object means "optional auth" and always passes.
func (s *Server) authError(ep *spec.Endpoint, r *http.Request) string {
	reqs := s.effectiveSecurity(ep)
	if len(reqs) == 0 {
		return ""
	}
	for _, req := range reqs {
		satisfied := true
		for name := range req {
			if !s.hasCredential(r, name) {
				satisfied = false
				break
			}
		}
		if satisfied {
			return ""
		}
	}
	return "missing or invalid credentials"
}

// hasCredential checks whether the request carries a credential matching the
// named scheme (shape only).
func (s *Server) hasCredential(r *http.Request, schemeName string) bool {
	scheme := s.doc.GetSecurityScheme(schemeName)
	if scheme == nil {
		return false // undefined scheme: never satisfied
	}
	switch scheme.Type {
	case "apiKey":
		var value string
		switch scheme.In {
		case "header":
			value = r.Header.Get(scheme.ParameterName)
		case "query":
			value = r.URL.Query().Get(scheme.ParameterName)
		case "cookie":
			if c, err := r.Cookie(scheme.ParameterName); err == nil {
				value = c.Value
			}
		}
		return strings.TrimSpace(value) != ""
	case "http":
		auth := r.Header.Get("Authorization")
		if strings.TrimSpace(auth) == "" {
			return false
		}
		switch scheme.Scheme {
		case "bearer":
			return hasAuthScheme(auth, "bearer")
		case "basic":
			return hasAuthScheme(auth, "basic")
		default:
			return true // any non-empty Authorization header
		}
	case "oauth2", "openIdConnect":
		// Typically used with bearer tokens; accept any Authorization value.
		return strings.TrimSpace(r.Header.Get("Authorization")) != ""
	case "mutualTLS":
		return r.TLS != nil && len(r.TLS.PeerCertificates) > 0
	}
	return false
}

// hasAuthScheme reports whether an Authorization header uses the given scheme
// (case-insensitive) with a non-empty value.
func hasAuthScheme(auth, scheme string) bool {
	parts := strings.SplitN(auth, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], scheme) {
		return false
	}
	return strings.TrimSpace(parts[1]) != ""
}

// pickAuthError finds a documented 401/403 response for auth failures (the
// lowest of the two). ok is false when neither is documented.
func pickAuthError(ep *spec.Endpoint) (int, spec.Response, bool) {
	var codes []int
	byCode := map[int]spec.Response{}
	for key, resp := range ep.Responses {
		code, err := strconv.Atoi(strings.TrimSpace(key))
		if err != nil || (code != http.StatusUnauthorized && code != http.StatusForbidden) {
			continue
		}
		codes = append(codes, code)
		byCode[code] = resp
	}
	if len(codes) == 0 {
		return 0, spec.Response{}, false
	}
	sort.Ints(codes)
	return codes[0], byCode[codes[0]], true
}
