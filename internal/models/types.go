// Package models defines the data types and database access layer for nexus.
package models

// Document represents metadata for an ingested file stored in the database.
type Document struct {
	ID          int64
	SourceName  string
	FilePath    string
	FileHash    string
	CharCount   int
	ChunkCount  int
	IngestTime  string
	DocType     string
	Language    string
	Institution string
	DocDate     string
}

// SourceSummary holds aggregate counts for one ingested source.
type SourceSummary struct {
	SourceName string
	DocCount   int
	ChunkCount int
}

// DocMeta carries optional classification metadata written to the documents table.
// It is nil for batch ingestion (nexus ingest) and populated by nexus file.
type DocMeta struct {
	DocType     string // e.g. "bank_statement", "id_document"
	Language    string // BCP-47 code: "en", "nl"
	Institution string // issuing organisation, empty if unknown
	DocDate     string // YYYY-MM-DD or YYYY-MM, empty if unknown
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

// SearchFilter controls which sources are included or excluded from a vector
// similarity search. It is constructed by the command layer from config defaults
// and the user's explicit --source / --category flags.
type SearchFilter struct {
	// Source restricts results to documents whose source_name or file_path contains
	// this string (case-insensitive). An explicit source bypasses ExcludeNames.
	Source string

	// ExcludeNames is a list of exact source names to skip.
	// Populated from sources with search_by_default: false when Source is empty.
	ExcludeNames []string

	// IncludeNames restricts results to documents whose source_name is in this list.
	// Used for --category filtering. Takes precedence over ExcludeNames.
	IncludeNames []string
}

// ContextSource is a registered live context source — a shell command whose
// output is injected into the query prompt at query time.
type ContextSource struct {
	ID          int64
	Name        string
	Command     string
	Description string
	CreatedAt   string
}
