package search

import "fmt"

func indexMappingJSON(embeddingDimension int) string {
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
      "chunk_index":  { "type": "integer" },
      "source_type":  { "type": "keyword" },
      "source_name":  { "type": "keyword" },
      "source_id":    { "type": "keyword" },
      "title":        { "type": "text", "analyzer": "standard" },
      "content":      { "type": "text", "analyzer": "standard" },
      "full_content": { "type": "text", "index": false },
      "metadata":     { "type": "object", "enabled": false },
      "url":          { "type": "keyword" },
      "visibility":   { "type": "keyword" },
      "created_at":   { "type": "date" },
      "indexed_at":   { "type": "date" },
      "owner_id":     { "type": "keyword" },
      "shared":       { "type": "boolean" }%s
    }
  }
}`, knnSetting, embeddingField)
}
