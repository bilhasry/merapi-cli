package gql

import (
	"encoding/json"
	"strings"
	"testing"
)

const wilayahSDL = `
scalar DateTime

type Province {
  id: ID!
  code: String!
  name: String!
  cities(type: CityType): [City!]!
}

type City {
  id: ID!
  name: String!
  type: CityType!
  province: Province!
}

enum CityType { KABUPATEN KOTA }

type ProvinceList {
  count: Int!
  data: [Province!]!
}

type Query {
  "All provinces"
  provinces: ProvinceList!
  province(id: ID!): Province
  "All cities, optionally filtered by type"
  cities(type: CityType): [City!]!
  serverTime: DateTime!
}

input CreateProvinceInput {
  code: String!
  name: String!
}

type Mutation {
  createProvince(input: CreateProvinceInput!): Province!
}
`

func mustParse(t *testing.T) *Schema {
	t.Helper()
	s, err := Parse(wilayahSDL)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestParseAndSummarize(t *testing.T) {
	s := mustParse(t)
	sum := s.Summarize()

	if len(sum.Queries) != 4 {
		t.Fatalf("queries = %d, want 4", len(sum.Queries))
	}
	// Sorted by name: cities, province, provinces, serverTime.
	if sum.Queries[0].Name != "cities" || sum.Queries[3].Name != "serverTime" {
		t.Errorf("query order = %v", sum.Queries)
	}
	prov := sum.Queries[1] // province(id: ID!)
	if len(prov.Arguments) != 1 || prov.Arguments[0].Name != "id" || !prov.Arguments[0].Required {
		t.Errorf("province args = %+v", prov.Arguments)
	}
	if !strings.Contains(prov.ReturnType, "Province") {
		t.Errorf("province return type = %q", prov.ReturnType)
	}

	if len(sum.Mutations) != 1 || sum.Mutations[0].Name != "createProvince" {
		t.Errorf("mutations = %+v", sum.Mutations)
	}
	if len(sum.Subscriptions) != 0 {
		t.Errorf("subscriptions = %+v", sum.Subscriptions)
	}

	// Types: Province, City, CityType, ProvinceList, Query, Mutation, CreateProvinceInput, DateTime.
	kinds := map[string]string{}
	for _, ti := range sum.Types {
		kinds[ti.Name] = ti.Kind
	}
	for name, want := range map[string]string{
		"Province": "OBJECT", "City": "OBJECT", "CityType": "ENUM",
		"ProvinceList": "OBJECT", "CreateProvinceInput": "INPUT_OBJECT", "DateTime": "SCALAR",
	} {
		if kinds[name] != want {
			t.Errorf("type %s kind = %q, want %q", name, kinds[name], want)
		}
	}
	if _, ok := kinds["__Schema"]; ok {
		t.Error("introspection types should be skipped")
	}
}

func TestParseInvalidSDL(t *testing.T) {
	if _, err := Parse("type Query { broken"); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestExampleQuery(t *testing.T) {
	s := mustParse(t)
	body, status := s.Example(`{ provinces { count data { id code name } } }`, "")
	if status != 200 {
		t.Fatalf("status = %d", status)
	}
	raw, _ := json.Marshal(body)
	var got struct {
		Data struct {
			Provinces struct {
				Count int `json:"count"`
				Data  []struct {
					ID   string `json:"id"`
					Code string `json:"code"`
					Name string `json:"name"`
				} `json:"data"`
			} `json:"provinces"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
	if got.Data.Provinces.Count != 0 {
		t.Errorf("count = %d, want 0 (Int)", got.Data.Provinces.Count)
	}
	if len(got.Data.Provinces.Data) != 1 {
		t.Fatalf("list should contain one example item, got %d", len(got.Data.Provinces.Data))
	}
	item := got.Data.Provinces.Data[0]
	if item.ID != "1" || item.Name != "string" || item.Code != "string" {
		t.Errorf("item = %+v", item)
	}
}

func TestExampleEnumTypenameAndArgs(t *testing.T) {
	s := mustParse(t)
	body, _ := s.Example(`{ cities(type: KOTA) { type province { __typename id } } }`, "")
	raw, _ := json.Marshal(body)
	out := string(raw)
	// Enum -> first value; __typename -> object name.
	if !strings.Contains(out, `"type":"KABUPATEN"`) {
		t.Errorf("enum example missing first value: %s", out)
	}
	if !strings.Contains(out, `"__typename":"Province"`) {
		t.Errorf("__typename missing: %s", out)
	}
}

func TestExampleMutation(t *testing.T) {
	s := mustParse(t)
	body, _ := s.Example(
		`mutation Create($input: CreateProvinceInput!) { createProvince(input: $input) { id name } }`,
		"")
	raw, _ := json.Marshal(body)
	if !strings.Contains(string(raw), "createProvince") {
		t.Errorf("mutation envelope missing: %s", raw)
	}
}

func TestExampleErrors(t *testing.T) {
	s := mustParse(t)

	// Unknown field -> GraphQL error envelope, HTTP 200.
	body, status := s.Example(`{ nope }`, "")
	if status != 200 {
		t.Errorf("status = %d, want 200 (GraphQL convention)", status)
	}
	raw, _ := json.Marshal(body)
	// Note: search quote-free fragments — json.Marshal escapes inner quotes.
	if !strings.Contains(string(raw), `Cannot query field`) || !strings.Contains(string(raw), `nope`) || !strings.Contains(string(raw), `"errors"`) {
		t.Errorf("want unknown-field error envelope, got %s", raw)
	}

	// Operation name that doesn't exist.
	body, _ = s.Example(`query a { provinces { count } }`, "b")
	raw, _ = json.Marshal(body)
	if !strings.Contains(string(raw), `operation`) || !strings.Contains(string(raw), `not found in the document`) {
		t.Errorf("want operation-not-found error, got %s", raw)
	}

	// Selecting a scalar without sub-selections is a validation error.
	body, _ = s.Example(`{ provinces { count count { x } } }`, "")
	raw, _ = json.Marshal(body)
	if !strings.Contains(string(raw), `"errors"`) {
		t.Errorf("invalid query should produce errors: %s", raw)
	}
}

func TestExampleFragmentSpreadUnsupported(t *testing.T) {
	s := mustParse(t)
	body, _ := s.Example(`
		{ provinces { count ...Fields } }
		fragment Fields on ProvinceList { data { id } }
	`, "")
	raw, _ := json.Marshal(body)
	if !strings.Contains(string(raw), "fragment spreads are not supported") {
		t.Errorf("want fragment-spread error, got %s", raw)
	}
}

func TestExampleInlineFragment(t *testing.T) {
	// Union with an inline fragment should merge the member's fields.
	s2, err := Parse(`
		type Province { id: ID! name: String! }
		union Thing = Province
		type Query { thing: Thing }
	`)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := s2.Example(`{ thing { __typename ... on Province { name } } }`, "")
	raw, _ := json.Marshal(body)
	if !strings.Contains(string(raw), `"name":"string"`) {
		t.Errorf("inline fragment fields missing: %s", raw)
	}
}

func TestExampleDeterministic(t *testing.T) {
	s := mustParse(t)
	a, _ := s.Example(`{ provinces { count data { id name } } }`, "")
	b, _ := s.Example(`{ provinces { count data { id name } } }`, "")
	ra, _ := json.Marshal(a)
	rb, _ := json.Marshal(b)
	if string(ra) != string(rb) {
		t.Errorf("examples must be deterministic:\n%s\n%s", ra, rb)
	}
}
