package templ

import (
	"reflect"
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	ctx := NewContext("http://api.example.com/", map[string]string{
		"tenant": "acme",
		"pet_id": "42",
	}, map[string]string{
		"API_TOKEN": "tok-123",
	})

	cases := map[string]string{
		"plain":                       "plain",
		"{{base_url}}/pets":           "http://api.example.com/pets",
		"Bearer {{secret:API_TOKEN}}": "Bearer tok-123",
		"pets/{{pet_id}}":             "pets/42",
		"{{ tenant }}":                "acme",
		"{{secret:API_TOKEN}}":        "tok-123", // bare secret names also resolve
	}
	for in, want := range cases {
		got, err := ctx.Resolve(in)
		if err != nil {
			t.Errorf("Resolve(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("Resolve(%q) = %q, want %q", in, got, want)
		}
	}

	// Unknown references are hard errors.
	if _, err := ctx.Resolve("{{nope}}"); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("expected undefined-reference error, got %v", err)
	}
	if _, err := ctx.Resolve("{{secret:MISSING}}"); err == nil {
		t.Error("missing secret should error")
	}
}

func TestResolveAny(t *testing.T) {
	ctx := NewContext("http://x", map[string]string{"name": "Rex"}, nil)
	in := map[string]any{
		"name":  "{{name}}",
		"meta":  map[string]any{"tag": "pet-{{name}}"},
		"list":  []any{"a", "{{name}}", 7},
		"count": 3,
	}
	out, err := ctx.ResolveAny(in)
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["name"] != "Rex" {
		t.Errorf("name = %v", m["name"])
	}
	if m["meta"].(map[string]any)["tag"] != "pet-Rex" {
		t.Errorf("tag = %v", m["meta"])
	}
	if !reflect.DeepEqual(m["list"], []any{"a", "Rex", 7}) {
		t.Errorf("list = %v", m["list"])
	}
	if m["count"] != 3 {
		t.Errorf("count = %v", m["count"])
	}
}

func TestCapturesOverrideVariables(t *testing.T) {
	ctx := NewContext("", map[string]string{"pet_id": "1"}, nil)
	ctx = ctx.WithCaptures(map[string]string{"pet_id": "99"})
	got, err := ctx.Resolve("{{pet_id}}")
	if err != nil {
		t.Fatal(err)
	}
	if got != "99" {
		t.Errorf("capture should win over variable, got %q", got)
	}
}

func TestReferences(t *testing.T) {
	refs := References("x {{a}} y {{secret:B}} {{ c }}")
	want := []string{"a", "secret:B", "c"}
	if !reflect.DeepEqual(refs, want) {
		t.Errorf("References = %v, want %v", refs, want)
	}
}
