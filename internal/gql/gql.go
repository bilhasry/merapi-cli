// Package gql loads and validates GraphQL SDL schemas, summarizes their
// operations, and builds deterministic example responses by walking a query's
// selection set against the schema. It backs `merapi gql ...` and the
// GraphQL endpoint of `merapi mock` (see docs/design/graphql.md).
package gql

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// Schema is a parsed and validated GraphQL SDL document.
type Schema struct {
	ast *ast.Schema
}

// Load reads and validates an SDL file.
func Load(path string) (*Schema, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s, err := Parse(string(data))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// Parse validates SDL source text.
func Parse(source string) (*Schema, error) {
	sch, err := gqlparser.LoadSchema(&ast.Source{Name: "schema.graphql", Input: source})
	if err != nil {
		return nil, err
	}
	return &Schema{ast: sch}, nil
}

// Operation is one field of the Query/Mutation/Subscription root.
type Operation struct {
	Name        string
	Description string
	Arguments   []Argument
	ReturnType  string
}

// Argument is an operation argument.
type Argument struct {
	Name     string
	Type     string
	Required bool
}

// TypeInfo describes one named type.
type TypeInfo struct {
	Name        string
	Kind        string // OBJECT, INTERFACE, UNION, ENUM, INPUT_OBJECT, SCALAR
	Description string
	FieldCount  int
}

// Summary is the introspection-free overview used by `gql show` and MCP.
type Summary struct {
	Types         []TypeInfo
	Queries       []Operation
	Mutations     []Operation
	Subscriptions []Operation
}

// Summarize lists types and root operations (sorted by name).
func (s *Schema) Summarize() Summary {
	out := Summary{
		Queries:       rootOps(s.ast.Query),
		Mutations:     rootOps(s.ast.Mutation),
		Subscriptions: rootOps(s.ast.Subscription),
	}
	for _, t := range s.ast.Types {
		if strings.HasPrefix(t.Name, "__") {
			continue // skip introspection types
		}
		out.Types = append(out.Types, TypeInfo{
			Name:        t.Name,
			Kind:        string(t.Kind),
			Description: firstLine(t.Description),
			FieldCount:  len(t.Fields) + len(t.EnumValues),
		})
	}
	sortInfos(out.Types)
	return out
}

func rootOps(def *ast.Definition) []Operation {
	if def == nil {
		return nil
	}
	var out []Operation
	for _, f := range def.Fields {
		if strings.HasPrefix(f.Name, "__") {
			continue // skip introspection fields (__schema, __type)
		}
		op := Operation{
			Name:        f.Name,
			Description: firstLine(f.Description),
			ReturnType:  f.Type.String(),
		}
		for _, a := range f.Arguments {
			op.Arguments = append(op.Arguments, Argument{
				Name:     a.Name,
				Type:     a.Type.String(),
				Required: a.Type.NonNull,
			})
		}
		out = append(out, op)
	}
	sortOps(out)
	return out
}

// Example builds a deterministic example response body for a query document.
// operationName selects one operation when the document defines several.
// The result is the standard GraphQL envelope: {"data": …} or, when the query
// is invalid, {"errors": [{message, path?}]}.
func (s *Schema) Example(query, operationName string) (any, int) {
	// LoadQuery parses and validates in one step; errors cover both bad
	// syntax and spec violations (unknown fields, bad types, ...).
	doc, gqlErrs := gqlparser.LoadQuery(s.ast, query)
	if len(gqlErrs) > 0 {
		return errEnvelope(gqlErrs), 200
	}
	op := pickOperation(doc, operationName)
	if op == nil {
		return errEnvelope(fmt.Errorf("operation %q not found in the document", operationName)), 200
	}
	if op.Operation == ast.Subscription {
		return errEnvelope(fmt.Errorf("subscriptions are not served by the mock")), 200
	}

	data := map[string]any{}
	var errs []error
	selectionExample(s.ast, rootDefinition(s.ast, op.Operation), op.SelectionSet, data, &errs)
	envelope := map[string]any{"data": data}
	if len(errs) > 0 {
		envelope["errors"] = errorList(errs)
	}
	return envelope, 200
}

func rootDefinition(sch *ast.Schema, kind ast.Operation) *ast.Definition {
	switch kind {
	case ast.Mutation:
		return sch.Mutation
	case ast.Subscription:
		return sch.Subscription
	default:
		return sch.Query
	}
}

func pickOperation(doc *ast.QueryDocument, name string) *ast.OperationDefinition {
	for _, op := range doc.Operations {
		if name == "" || op.Name == name {
			return op
		}
	}
	return nil
}

// selectionExample fills out with example values for a selection set against
// an OBJECT/INTERFACE definition. Unknown fields append to errs.
func selectionExample(sch *ast.Schema, def *ast.Definition, sel ast.SelectionSet, out map[string]any, errs *[]error) {
	if def == nil {
		return
	}
	for _, selection := range sel {
		switch field := selection.(type) {
		case *ast.Field:
			if field.Name == "__typename" {
				out[field.Alias] = def.Name
				continue
			}
			fd := def.Fields.ForName(field.Name)
			if fd == nil {
				*errs = append(*errs, fmt.Errorf("unknown field %q on type %q", field.Name, def.Name))
				continue
			}
			out[field.Alias] = valueExample(sch, fd.Type, field.SelectionSet, field.Name+"."+fd.Type.String(), errs)
		case *ast.InlineFragment:
			// Merged into the parent shape: recurse on the matching type.
			if t := sch.Types[field.TypeCondition]; t != nil {
				selectionExample(sch, t, field.SelectionSet, out, errs)
			}
		case *ast.FragmentSpread:
			*errs = append(*errs, fmt.Errorf("fragment spreads are not supported by the mock (inline the fragment): %q", field.Name))
		}
	}
}

// valueExample generates one example value for a field type: scalars get
// deterministic values, enums their first value, objects recurse, and lists
// contain a single item.
func valueExample(sch *ast.Schema, t *ast.Type, sel ast.SelectionSet, path string, errs *[]error) any {
	if t.Elem != nil { // list type
		return []any{valueExample(sch, t.Elem, sel, path, errs)}
	}
	named := sch.Types[t.NamedType]
	if named == nil {
		return nil
	}
	switch named.Kind {
	case ast.Scalar:
		return scalarExample(named.Name)
	case ast.Enum:
		if len(named.EnumValues) > 0 {
			return named.EnumValues[0].Name
		}
		return nil
	case ast.Object, ast.Interface:
		out := map[string]any{}
		selectionExample(sch, named, sel, out, errs)
		return out
	case ast.Union:
		// Serve the first member's shape when the query inlines it; without
		// an inline fragment there is nothing to select.
		if len(named.Types) > 0 {
			if member := sch.Types[named.Types[0]]; member != nil {
				out := map[string]any{"__typename": member.Name}
				selectionExample(sch, member, sel, out, errs)
				return out
			}
		}
		return map[string]any{"__typename": named.Name}
	default: // INPUT_OBJECT cannot appear in outputs
		return nil
	}
}

// scalarExample returns a deterministic value per built-in scalar.
func scalarExample(name string) any {
	switch name {
	case "Int":
		return 0
	case "Float":
		return 0.0
	case "Boolean":
		return true
	case "ID":
		return "1"
	default: // String and custom scalars
		return "string"
	}
}

func errEnvelope(err error) any {
	return map[string]any{"data": nil, "errors": errorList([]error{err})}
}

func errorList(errs []error) []any {
	out := make([]any, 0, len(errs))
	for _, err := range errs {
		item := map[string]any{"message": strings.TrimSpace(err.Error())}
		var gerr *gqlerror.Error
		if errors.As(err, &gerr) && gerr.Path != nil {
			item["path"] = fmt.Sprint(gerr.Path)
		}
		out = append(out, item)
	}
	return out
}

// --- small helpers -----------------------------------------------------------

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

func sortOps(ops []Operation) {
	for i := 1; i < len(ops); i++ {
		for j := i; j > 0 && ops[j].Name < ops[j-1].Name; j-- {
			ops[j], ops[j-1] = ops[j-1], ops[j]
		}
	}
}

func sortInfos(infos []TypeInfo) {
	for i := 1; i < len(infos); i++ {
		for j := i; j > 0 && infos[j].Name < infos[j-1].Name; j-- {
			infos[j], infos[j-1] = infos[j-1], infos[j]
		}
	}
}
