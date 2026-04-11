package search

import (
	"encoding/json"
	"fmt"

	"github.com/muty/nexus/internal/lang"
)

// indexMappingJSON returns the OpenSearch index mapping as a JSON string.
//
// Text fields (title, content) use the standard analyzer as a base and
// expose one sub-field per configured language (e.g. title.english,
// content.german) using the corresponding built-in language analyzer.
// Queries against title/content via multi_match most_fields accumulate
// matches across every analyzer that recognizes the query terms, giving
// both exact-token and stemmed/morphological recall in one pass.
func indexMappingJSON(embeddingDimension int, languages []lang.Language) string {
	embeddingField := ""
	knnSetting := ""

	if embeddingDimension > 0 {
		knnSetting = `"knn": true,`
		embeddingField = fmt.Sprintf(`,
      "embedding": {
        "type": "knn_vector",
        "dimension": %d,
        "method": {
          "name": "hnsw",
          "space_type": "cosinesimil",
          "engine": "lucene"
        }
      }`, embeddingDimension)
	}

	textField := buildTextFieldMapping(languages)

	return fmt.Sprintf(`{
  "settings": {
    "index": {
      %s
      "number_of_shards": 1,
      "number_of_replicas": 0
    }
  },
  "mappings": {
    "properties": {
      "id":           { "type": "keyword" },
      "parent_id":    { "type": "keyword" },
      "doc_id":       { "type": "keyword" },
      "chunk_index":  { "type": "integer" },
      "source_type":  { "type": "keyword" },
      "source_name":  { "type": "keyword" },
      "source_id":    { "type": "keyword" },
      "title":        %s,
      "content":      %s,
      "full_content": { "type": "text", "index": false },
      "metadata":     { "type": "object", "enabled": false },
      "url":          { "type": "keyword" },
      "visibility":   { "type": "keyword" },
      "created_at":   { "type": "date" },
      "indexed_at":   { "type": "date" },
      "owner_id":     { "type": "keyword" },
      "shared":       { "type": "boolean" },
      "mime_type":    { "type": "keyword" },
      "size":         { "type": "long" }%s
    }
  }
}`, knnSetting, textField, textField, embeddingField)
}

// buildTextFieldMapping returns a JSON text-field mapping fragment with
// one language sub-field per entry in languages. The base analyzer stays
// "standard" (catch-all for unknown languages + exact-token matching)
// and each language adds a `fields.<name>` sub-field analyzed with the
// corresponding built-in language analyzer.
func buildTextFieldMapping(languages []lang.Language) string {
	if len(languages) == 0 {
		return `{ "type": "text", "analyzer": "standard" }`
	}

	subFields := make(map[string]map[string]string, len(languages))
	for _, l := range languages {
		subFields[l.Name] = map[string]string{
			"type":     "text",
			"analyzer": l.OpenSearchAnalyzer,
		}
	}

	payload := map[string]any{
		"type":     "text",
		"analyzer": "standard",
		"fields":   subFields,
	}

	// json.Marshal never fails on a value composed of strings and maps.
	b, _ := json.Marshal(payload)
	return string(b)
}
