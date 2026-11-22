package models

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

const schemaPath = "../docs/schema/studio-file.schema.json"

type jsonSchema struct {
	Type                 string                 `json:"type"`
	Ref                  string                 `json:"$ref"`
	Required             []string               `json:"required"`
	Properties           map[string]*jsonSchema `json:"properties"`
	Items                *jsonSchema            `json:"items"`
	AdditionalProperties json.RawMessage        `json:"additionalProperties"`
	Defs                 map[string]*jsonSchema `json:"$defs"`
}

func loadSchema(t *testing.T) *jsonSchema {
	t.Helper()
	b, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	var s jsonSchema
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("%s: %v", schemaPath, err)
	}
	return &s
}

func (root *jsonSchema) resolve(s *jsonSchema) *jsonSchema {
	if s.Ref == "" {
		return s
	}
	return root.Defs[strings.TrimPrefix(s.Ref, "#/$defs/")]
}

func jsonTag(f reflect.StructField) (name string, optional bool) {
	parts := strings.Split(f.Tag.Get("json"), ",")
	for _, o := range parts[1:] {
		if o == "omitempty" || o == "omitzero" {
			optional = true
		}
	}
	return parts[0], optional
}

func wantType(t reflect.Type) string {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == reflect.TypeOf(time.Time{}) {
		return "string"
	}
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Map, reflect.Struct:
		return "object"
	}
	return "?" + t.Kind().String()
}

// checkStruct compares one Go struct against its schema object and recurses
// into nested struct, slice and pointer element types.
func checkStruct(t *testing.T, root *jsonSchema, path string, typ reflect.Type, s *jsonSchema) {
	t.Helper()
	if s == nil {
		t.Errorf("%s: schema object missing for Go type %s", path, typ)
		return
	}
	if s.Type != "object" {
		t.Errorf("%s: schema type is %q, Go type %s needs \"object\"", path, s.Type, typ)
	}
	// Additive fields never bump StoreSchemaVersion, so the schema must not
	// make an older reader reject a newer file.
	if string(s.AdditionalProperties) == "false" {
		t.Errorf("%s: remove \"additionalProperties\": false (newer files may add fields)", path)
	}

	wantReq := map[string]bool{}
	seen := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		name, optional := jsonTag(f)
		if name == "" || name == "-" {
			continue
		}
		seen[name] = true
		if !optional {
			wantReq[name] = true
		}
		prop, ok := s.Properties[name]
		if !ok {
			t.Errorf("%s: Go field %s.%s has json key %q but the schema has no such property; add it to properties in %s",
				path, typ.Name(), f.Name, name, schemaPath)
			continue
		}
		p := root.resolve(prop)
		if p == nil {
			t.Errorf("%s.%s: unresolvable $ref %q", path, name, prop.Ref)
			continue
		}
		if want := wantType(f.Type); p.Type != want {
			t.Errorf("%s.%s: schema type is %q but Go type %s needs %q", path, name, p.Type, f.Type, want)
		}
		ft := f.Type
		if ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Slice {
			el := ft.Elem()
			if p.Items == nil {
				t.Errorf("%s.%s: array schema needs \"items\"", path, name)
			} else if el.Kind() == reflect.Struct && el != reflect.TypeOf(time.Time{}) {
				checkStruct(t, root, path+"."+name+"[]", el, root.resolve(p.Items))
			} else if want := wantType(el); root.resolve(p.Items).Type != want {
				t.Errorf("%s.%s: items type is %q but Go element type %s needs %q", path, name, root.resolve(p.Items).Type, el, want)
			}
		}
		if ft.Kind() == reflect.Map {
			if string(p.AdditionalProperties) != `{ "type": "string" }` && !strings.Contains(string(p.AdditionalProperties), `"string"`) {
				t.Errorf("%s.%s: map schema needs additionalProperties {\"type\": \"string\"}", path, name)
			}
		}
		if ft.Kind() == reflect.Struct && ft != reflect.TypeOf(time.Time{}) {
			checkStruct(t, root, path+"."+name, ft, p)
		}
	}

	for name := range s.Properties {
		if !seen[name] {
			t.Errorf("%s: schema property %q has no json-tagged field in Go type %s; remove it from %s (or add the field)",
				path, name, typ.Name(), schemaPath)
		}
	}

	gotReq := map[string]bool{}
	for _, r := range s.Required {
		gotReq[r] = true
	}
	for _, n := range sortedKeys(wantReq) {
		if !gotReq[n] {
			t.Errorf("%s: %q has no omitempty/omitzero so it is always emitted; add it to \"required\"", path, n)
		}
	}
	for _, n := range sortedKeys(gotReq) {
		if !wantReq[n] {
			t.Errorf("%s: %q is in \"required\" but is omitempty/omitzero or unknown in Go; remove it from \"required\"", path, n)
		}
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestSchemaMatchesStructs(t *testing.T) {
	root := loadSchema(t)
	checkStruct(t, root, "StudioFile", reflect.TypeOf(StudioFile{}), root)
}

func fullScene() Scene {
	now := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	s := Scene{
		ID: "1", SiteID: "x", StudioURL: "https://x", Title: "t", URL: "https://x/1",
		ExternalIDs: map[string]string{ExternalStashDB: "abc"},
		Date:        now, Description: "d", Thumbnail: "th", Preview: "pv",
		Performers: []string{"a"}, Director: "d", Studio: "s",
		Tags: []string{"t"}, Categories: []string{"c"},
		Series: "s", SeriesPart: 1,
		Duration: 1, Resolution: "1080p", Width: 1, Height: 1, Format: "mp4",
		Views: 1, Likes: 1, Comments: 1,
		FirstSeenAt: now, ScrapedAt: now, DeletedAt: &now,
	}
	s.AddPrice(PriceSnapshot{Date: now, Regular: 10, Discounted: 5, IsOnSale: true, DiscountPercent: 50, Currency: "USD"})
	return s
}

func TestSchemaCoversEmittedKeys(t *testing.T) {
	root := loadSchema(t)
	scene := root.Defs["Scene"]
	price := root.Defs["PriceSnapshot"]

	for name, sc := range map[string]Scene{"full": fullScene(), "zero": {}} {
		b, err := json.Marshal(sc)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		for _, k := range emittedKeysOutsideSchema(m, scene, price) {
			t.Errorf("%s Scene emits key %s that the schema does not declare; add it to %s", name, k, schemaPath)
		}
	}
}

func emittedKeysOutsideSchema(m map[string]any, scene, price *jsonSchema) []string {
	var bad []string
	for k, v := range m {
		if _, ok := scene.Properties[k]; !ok {
			bad = append(bad, k)
		}
		if k == "priceHistory" {
			for i, e := range v.([]any) {
				for pk := range e.(map[string]any) {
					if _, ok := price.Properties[pk]; !ok {
						bad = append(bad, fmt.Sprintf("priceHistory[%d].%s", i, pk))
					}
				}
			}
		}
	}
	sort.Strings(bad)
	return bad
}

func TestSchemaRequiredAlwaysEmitted(t *testing.T) {
	root := loadSchema(t)
	b, _ := json.Marshal(Scene{})
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	for _, r := range root.Defs["Scene"].Required {
		if _, ok := m[r]; !ok {
			t.Errorf("zero Scene does not emit required key %q; remove it from \"required\" in %s", r, schemaPath)
		}
	}
}
