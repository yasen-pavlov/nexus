package search

const indexMapping = `{
  "settings": {
    "number_of_shards": 1,
    "number_of_replicas": 0
  },
  "mappings": {
    "properties": {
      "id":          { "type": "keyword" },
      "source_type": { "type": "keyword" },
      "source_name": { "type": "keyword" },
      "source_id":   { "type": "keyword" },
      "title":       { "type": "text", "analyzer": "standard" },
      "content":     { "type": "text", "analyzer": "standard" },
      "metadata":    { "type": "object", "enabled": false },
      "url":         { "type": "keyword" },
      "visibility":  { "type": "keyword" },
      "created_at":  { "type": "date" },
      "indexed_at":  { "type": "date" }
    }
  }
}`
