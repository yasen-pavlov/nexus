package search

import (
	"encoding/json"
	"testing"

	"github.com/muty/nexus/internal/lang"
)

func TestIndexMappingJSON_ContainsLanguageSubFields(t *testing.T) {
	raw := indexMappingJSON(0, lang.Default())

	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("indexMappingJSON did not return valid JSON: %v\n%s", err, raw)
	}

	props := parsed["mappings"].(map[string]any)["properties"].(map[string]any)
	for _, field := range []string{"title", "content"} {
		m := props[field].(map[string]any)
		if m["analyzer"] != "standard" {
			t.Errorf("%s.analyzer = %v, want standard", field, m["analyzer"])
		}
		fields, ok := m["fields"].(map[string]any)
		if !ok {
			t.Fatalf("%s.fields missing or not an object", field)
		}
		for _, want := range []string{"english", "german", "bulgarian"} {
			sub, ok := fields[want].(map[string]any)
			if !ok {
				t.Errorf("%s.fields.%s missing", field, want)
				continue
			}
			if sub["analyzer"] != want {
				t.Errorf("%s.fields.%s.analyzer = %v, want %s", field, want, sub["analyzer"], want)
			}
		}
	}
}

func TestIndexMappingJSON_NoLanguages(t *testing.T) {
	raw := indexMappingJSON(0, nil)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("indexMappingJSON did not return valid JSON: %v", err)
	}
	props := parsed["mappings"].(map[string]any)["properties"].(map[string]any)
	m := props["title"].(map[string]any)
	if m["analyzer"] != "standard" {
		t.Errorf("title.analyzer = %v, want standard", m["analyzer"])
	}
	if _, has := m["fields"]; has {
		t.Errorf("title should have no fields sub-object when languages empty")
	}
}

func TestIndexMappingJSON_WithEmbedding(t *testing.T) {
	raw := indexMappingJSON(1024, lang.Default())
	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("indexMappingJSON did not return valid JSON: %v", err)
	}
	props := parsed["mappings"].(map[string]any)["properties"].(map[string]any)
	emb, ok := props["embedding"].(map[string]any)
	if !ok {
		t.Fatal("embedding field missing when dimension > 0")
	}
	if emb["type"] != "knn_vector" {
		t.Errorf("embedding.type = %v, want knn_vector", emb["type"])
	}
	if emb["dimension"].(float64) != 1024 {
		t.Errorf("embedding.dimension = %v, want 1024", emb["dimension"])
	}
}
