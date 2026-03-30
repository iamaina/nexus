// This file defines the data structures used for representing the layout of a PDF document.
package layout

// The DocumentType is a simple string type that can be used to categorize
// documents based on their content or structure. This can be useful for
// downstream processing, such as applying different parsing or analysis
// strategies based on the document type.
type DocumentType string

// The BlockType is a simple string type that can be used to categorize
// different types of content blocks in the document, such as paragraphs,
// code snippets, and images. This allows us to apply different processing or
// formatting strategies based on the block type.
type BlockType string

// These constants represent the different types of blocks that can be
// identified in a document, such as paragraphs, code snippets, and images. This
// allows us to categorize content and apply different processing or formatting
// strategies based on the block type.
const (
	BlockParagraph  BlockType    = "paragraph"
	BlockCode       BlockType    = "code"
	BlockImage      BlockType    = "image"
	DocumentBook    DocumentType = "book"
	DocumentSlides  DocumentType = "slides"
	DocumentUnknown DocumentType = "unknown"
)

// The Block struct is a more general representation of a block of content in
// the document, which can be either a code, image or a paragraph. It includes the
// type of block, the text content, page number, and Y coordinate. This struct
// can be useful for intermediate processing steps where we want to treat
// images, code and paragraphs in a unified way before building the final
// hierarchical structure.
type Block struct {
	Type BlockType
	Text string
	Page int
	Y    float64
}

// The Heading struct represents a detected heading in the document, including
// its text content, heading level (e.g., H1, H2), font size, page number, and
// font name. This information is crucial for understanding the document's
// structure and hierarchy.
type Heading struct {
	ID       string
	Text     string
	Level    int
	Page     int
	Y        float64
	Children []*Heading
	Blocks   []Block
	FontSize float64
	FontName string
}

// The Span struct represents a single text span extracted from the PDF,
// including its text content, position (X, Y), font size, page number, font
// name, and any additional flags that may be relevant for layout analysis.
type Span struct {
	Type     string  `json:"type"`
	Text     string  `json:"text"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	FontSize float64 `json:"font_size"`
	Page     int     `json:"page"`
	FontName string  `json:"font_name"`
	Flags    int     `json:"flags"`
}

// The Line struct represents a line of text, which is a collection of spans
// that are close to each other in the Y coordinate and belong to the same page.
// It also includes features like the combined text, font information, and other
// derived features that can be used for heading detection and layout analysis.
type Line struct {
	Text     string
	Spans    []Span
	Y        float64
	Page     int
	XStart   float64
	XEnd     float64
	FontSize float64
	FontName string
	Flags    int
}

// The FontStats struct is used to store information about the font usage in the
// document, including the frequency of each font size, a sorted list of font
// sizes, the detected body font size, and any identified heading font sizes.
// This information is crucial for understanding the document's structure and
// for accurately detecting headings based on font size and usage patterns.
type FontStats struct {
	Frequency map[float64]int
	Sorted    []float64
	BodyFont  float64
	Headings  []float64
}

// The Node struct represents a node in the document's hierarchical structure,
// which is built based on the detected headings. Each node contains a Heading
// and a list of child nodes, allowing us to represent the document's structure
// as a tree.
type Node struct {
	Heading  Heading
	Children []*Node
	Blocks   []Block
}

// The Section struct represents a section of the document, which is defined by
// a heading and its associated content. Each section has a title, content,
// heading level, and a list of child sections, allowing us to represent the
// document's structure in a hierarchical manner.
type Section struct {
	Title    string
	Content  []Block
	Level    int
	Children []Section
	Page     int
}
