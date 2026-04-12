package model

import "time"

// BinaryStoreEntry is a row in the binary_store_entries table tracking one
// cached binary blob. The (SourceType, SourceName, SourceID) triple matches
// the document identity used everywhere else in the system.
type BinaryStoreEntry struct {
	SourceType     string    `json:"source_type"`
	SourceName     string    `json:"source_name"`
	SourceID       string    `json:"source_id"`
	FilePath       string    `json:"file_path"`
	Size           int64     `json:"size"`
	StoredAt       time.Time `json:"stored_at"`
	LastAccessedAt time.Time `json:"last_accessed_at"`
}

// BinaryStoreStats aggregates the cache size and item count for one
// source type + source name combination.
type BinaryStoreStats struct {
	SourceType string `json:"source_type"`
	SourceName string `json:"source_name"`
	Count      int64  `json:"count"`
	TotalSize  int64  `json:"total_size"`
}
