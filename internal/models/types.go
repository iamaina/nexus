// Package models defines the data types and database access layer for nexus.
package models

// Document represents metadata for an ingested file stored in the database.
type Document struct {
	ID         int64
	SourceName string
	FilePath   string
	FileHash   string
	CharCount  int
	ChunkCount int
}

// EnrichedChunk is a rendered chunk ready for storage — text, heading context, and level.
type EnrichedChunk struct {
	Text    string
	Chapter string
	Level   int
}

// Result is a retrieved chunk returned by a vector similarity search.
// DocumentID, ChunkIndex, and Level are navigation fields used for child
// chunk expansion and are not displayed to the end user.
type Result struct {
	DocumentID int64
	ChunkIndex int
	Level      int
	File       string
	Chapter    string
	Text       string
	Score      float64
}
