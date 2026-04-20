package search

import (
	"encoding/json"

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
//
// The mapping is assembled as a Go map and emitted via json.Marshal so
// every value is guaranteed to be properly JSON-escaped — avoids the
// unsafe-quoting hazard of interpolating values into a JSON template.
func indexMappingJSON(embeddingDimension int, languages []lang.Language) string {
	keyword := jsonObj{"type": "keyword"}
	date := jsonObj{"type": "date"}
	boolean := jsonObj{"type": "boolean"}

	indexSettings := jsonObj{
		"number_of_shards":   1,
		"number_of_replicas": 0,
	}
	if embeddingDimension > 0 {
		indexSettings["knn"] = true
	}

	textField := buildTextField(languages)

	properties := jsonObj{
		"id":              keyword,
		"parent_id":       keyword,
		"doc_id":          keyword,
		"chunk_index":     jsonObj{"type": "integer"},
		"source_type":     keyword,
		"source_name":     keyword,
		"source_id":       keyword,
		"title":           textField,
		"content":         textField,
		"full_content":    jsonObj{"type": "text", "index": false},
		"metadata":        jsonObj{"type": "object", "enabled": false},
		"url":             keyword,
		"visibility":      keyword,
		"created_at":      date,
		"indexed_at":      date,
		"owner_id":        keyword,
		"shared":          boolean,
		"mime_type":       keyword,
		"size":            jsonObj{"type": "long"},
		"hidden":          boolean,
		"conversation_id": keyword,
		"imap_message_id": keyword,
		"relations": jsonObj{
			"type": "nested",
			"properties": jsonObj{
				"type":             keyword,
				"target_source_id": keyword,
				"target_id":        keyword,
			},
		},
	}
	if embeddingDimension > 0 {
		properties["embedding"] = jsonObj{
			"type":      "knn_vector",
			"dimension": embeddingDimension,
			"method": jsonObj{
				"name":       "hnsw",
				"space_type": "cosinesimil",
				"engine":     "lucene",
			},
		}
	}

	mapping := jsonObj{
		"settings": jsonObj{"index": indexSettings},
		"mappings": jsonObj{"properties": properties},
	}

	// json.Marshal never fails on a value composed of strings/maps/ints/bools.
	b, _ := json.Marshal(mapping)
	return string(b)
}

// buildTextField returns a JSON text-field mapping fragment with one
// language sub-field per entry in languages. The base analyzer stays
// "standard" (catch-all for unknown languages + exact-token matching)
// and each language adds a `fields.<name>` sub-field analyzed with the
// corresponding built-in language analyzer.
func buildTextField(languages []lang.Language) jsonObj {
	if len(languages) == 0 {
		return jsonObj{"type": "text", "analyzer": "standard"}
	}

	subFields := make(jsonObj, len(languages))
	for _, l := range languages {
		subFields[l.Name] = jsonObj{
			"type":     "text",
			"analyzer": l.OpenSearchAnalyzer,
		}
	}
	return jsonObj{
		"type":     "text",
		"analyzer": "standard",
		"fields":   subFields,
	}
}

// jsonObj is a readability alias — spelling out map[string]any on every
// nested mapping level makes the structure hard to scan.
type jsonObj = map[string]any
