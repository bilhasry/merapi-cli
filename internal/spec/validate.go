package spec

import (
	"fmt"
	"os"
	"strings"

	"github.com/pb33f/libopenapi"
	validator "github.com/pb33f/libopenapi-validator"
)

// Issue is one validation finding, positioned in the source file when possible.
type Issue struct {
	Severity string `json:"severity"` // "error" or "warning"
	Message  string `json:"message"`
	Line     int    `json:"line,omitempty"`
	Col      int    `json:"col,omitempty"`
	Type     string `json:"type,omitempty"`
	HowToFix string `json:"howToFix,omitempty"`
}

func (i Issue) String() string {
	loc := ""
	if i.Line > 0 {
		loc = fmt.Sprintf(" (line %d)", i.Line)
	}
	s := fmt.Sprintf("[%s]%s %s", i.Severity, loc, i.Message)
	if i.HowToFix != "" {
		s += "\n    fix: " + i.HowToFix
	}
	return s
}

// Validate parses the document with libOpenAPI and validates it against the
// OpenAPI 3.x meta-schema, then appends documentation-quality warnings.
func (d *Document) Validate() ([]Issue, error) {
	data, err := os.ReadFile(d.Path)
	if err != nil {
		return nil, err
	}
	var issues []Issue

	doc, err := libopenapi.NewDocument(data)
	if err != nil {
		return append(issues, Issue{
			Severity: "error",
			Message:  fmt.Sprintf("document could not be parsed: %v", err),
		}), nil
	}
	hl, setupErr := validator.NewValidator(doc)
	if setupErr != nil {
		return append(issues, Issue{
			Severity: "error",
			Message:  fmt.Sprintf("validator setup failed: %v", setupErr),
		}), nil
	}
	valid, verrs := hl.ValidateDocument()
	if !valid {
		for _, e := range verrs {
			issues = append(issues, Issue{
				Severity: "error",
				Message:  firstNonEmpty(e.Message, e.Reason, "document does not match the OpenAPI schema"),
				Line:     e.SpecLine,
				Col:      e.SpecCol,
				Type:     e.ValidationType,
				HowToFix: e.HowToFix,
			})
			for _, sf := range e.SchemaValidationErrors {
				where := sf.FieldPath
				if where == "" {
					where = "schema"
				}
				issues = append(issues, Issue{
					Severity: "error",
					Message:  fmt.Sprintf("%s: %s", where, sf.Reason),
					Line:     e.SpecLine,
					Col:      e.SpecCol,
					Type:     e.ValidationType,
				})
			}
		}
	}

	issues = append(issues, d.qualityWarnings()...)
	return issues, nil
}

// qualityWarnings surfaces documentation gaps that do not invalidate the
// document but hurt its usefulness for humans and agents.
func (d *Document) qualityWarnings() []Issue {
	var issues []Issue
	lineOf := d.lineIndex()
	for _, ep := range d.ListEndpoints(EndpointFilter{}) {
		where := fmt.Sprintf("%s %s", ep.Method, ep.Path)
		if ep.Summary == "" {
			issues = append(issues, Issue{
				Severity: "warning",
				Message:  where + " has no summary",
				Line:     lineOf(opKey(ep.Method, ep.Path)),
				HowToFix: "run `merapi endpoint update " + ep.Method + " " + ep.Path + " --summary \"...\"`",
			})
		}
		if ep.Description == "" {
			issues = append(issues, Issue{
				Severity: "warning",
				Message:  where + " has no description",
				Line:     lineOf(opKey(ep.Method, ep.Path)),
				HowToFix: "run `merapi endpoint update " + ep.Method + " " + ep.Path + " --description \"...\"`",
			})
		}
	}
	// Operations referencing a security scheme that is not defined in
	// components/securitySchemes would break generated docs, samples and
	// mock enforcement — surface it as an error.
	for _, msg := range d.UndefinedSecuritySchemes() {
		issues = append(issues, Issue{
			Severity: "error",
			Message:  msg,
			HowToFix: "define it with `merapi security add <name> ...` or remove it from the operation's security list",
		})
	}
	return issues
}

func opKey(method, path string) string { return strings.ToUpper(method) + " " + path }

// lineIndex maps "METHOD /path" keys to 1-based source lines for warnings.
func (d *Document) lineIndex() func(string) int {
	// Collect operation positions from the node tree.
	type pos struct{ line, col int }
	ops := map[string]pos{}
	paths := d.PathsNode()
	for _, path := range mapKeys(paths) {
		item := mapGet(paths, path)
		if item == nil {
			continue
		}
		for _, m := range Methods {
			if op := mapGet(item, m); op != nil {
				ops[opKey(m, path)] = pos{line: op.Line, col: op.Column}
			}
		}
	}
	return func(key string) int {
		if p, ok := ops[key]; ok {
			return p.line
		}
		return 0
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// HasErrors reports whether the issue list contains blocking errors.
func HasErrors(issues []Issue) bool {
	for _, i := range issues {
		if i.Severity == "error" {
			return true
		}
	}
	return false
}
