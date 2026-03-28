// This file defines the data structures used for representing the layout of a PDF document.
package layout

// The Span struct represents a single text span extracted from the PDF,
// including its text content, position (X, Y), font size, page number, font
// name, and any additional flags that may be relevant for layout analysis.
type Span struct {
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

// The Heading struct represents a detected heading in the document, including
// its text content, heading level (e.g., H1, H2), font size, page number, and
// font name. This information is crucial for understanding the document's
// structure and hierarchy.
type Heading struct {
	Text     string
	Level    int
	FontSize float64
	Page     int
	FontName string
	Y        float64
}

// The Node struct represents a node in the document's hierarchical structure,
// which is built based on the detected headings. Each node contains a Heading
// and a list of child nodes, allowing us to represent the document's structure
// as a tree.
type Node struct {
	Heading    Heading
	Children   []*Node
	Paragraphs []string
}
