package layout

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// codeExtensions lists source/config file types that are readable as plain text.
// These are routed through ExtractPlainText rather than the PDF extractor.
var codeExtensions = map[string]bool{
	".tf": true, ".hcl": true,
	".go": true, ".rb": true, ".py": true, ".rs": true,
	".js": true, ".ts": true,
	".sh": true, ".bash": true,
	".yaml": true, ".yml": true, ".json": true, ".toml": true,
	".sql":     true,
	".jsonnet": true, ".libsonnet": true,
}

// Extract dispatches to the correct extractor based on file extension.
// Markdown → ExtractMarkdown (heading-aware, section tree).
// HTML → ExtractHTMLFile (heading-aware, strips nav/footer noise).
// Plain text and code/config files → ExtractPlainText.
// Everything else → ExtractPDF (Python/PyMuPDF).
func Extract(path string) ([]Span, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch {
	case ext == ".md":
		return ExtractMarkdown(path)
	case ext == ".html" || ext == ".htm":
		return ExtractHTMLFile(path)
	case ext == ".txt" || codeExtensions[ext]:
		return ExtractPlainText(path)
	default:
		return extractPDFSpans(path)
	}
}

// ExtractHTMLFile opens a local HTML file and delegates to ExtractHTML.
func ExtractHTMLFile(path string) ([]Span, error) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return ExtractHTML(f)
}

// ExtractMarkdown converts a Markdown file into []Span, assigning synthetic
// font sizes so the downstream layout pipeline can detect headings and build
// a proper section tree.
//
// Font size mapping (body = 12pt):
//
//	#  → 24pt  (ratio 2.0  → H1)
//	## → 16pt  (ratio 1.33 → H2)
//	###→ 13.2pt(ratio 1.1  → H3)
//	other → 12pt body text or Courier for fenced code
func ExtractMarkdown(path string) ([]Span, error) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	const (
		bodySize = 12.0
		h1Size   = 24.0
		h2Size   = 16.0
		h3Size   = 13.2
		lineH    = 14.0 // body line height — keeps consecutive lines < 20pt apart (merge threshold)
		codeH    = 12.0 // code line height — keeps consecutive lines < 14pt apart (code merge threshold)
		paraGap  = 30.0 // blank-line gap — forces a new paragraph block (> 20pt threshold)
	)

	var spans []Span
	y := 50.0
	page := 1
	inCode := false

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		// fenced code block toggle (``` or ~~~)
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			inCode = !inCode
			y += paraGap
			continue
		}

		if inCode {
			if strings.TrimSpace(line) == "" {
				y += codeH
				continue
			}
			spans = append(spans, Span{
				Type:     "text",
				Text:     line,
				FontSize: bodySize,
				FontName: "Courier",
				Page:     page,
				Y:        y,
			})
			y += codeH
			continue
		}

		// blank line → paragraph break
		if strings.TrimSpace(line) == "" {
			y += paraGap
			continue
		}

		// ATX headings (#, ##, ###; treat #### and deeper as body)
		if h, rest, ok := parseATXHeading(line); ok {
			var size float64
			switch h {
			case 1:
				size = h1Size
			case 2:
				size = h2Size
			case 3:
				size = h3Size
			default:
				// H4+ treated as body text — not detected as headings
				size = bodySize
			}
			spans = append(spans, Span{
				Type:     "text",
				Text:     rest,
				FontSize: size,
				FontName: "Helvetica-Bold",
				Page:     page,
				Y:        y,
			})
			y += paraGap
			continue
		}

		// regular body text
		text := stripInlineMarkdown(line)
		if text == "" {
			y += paraGap
			continue
		}
		spans = append(spans, Span{
			Type:     "text",
			Text:     text,
			FontSize: bodySize,
			FontName: "Helvetica",
			Page:     page,
			Y:        y,
		})
		y += lineH
	}

	if err := scanner.Err(); err != nil && err != bufio.ErrTooLong {
		return nil, err
	}
	return spans, nil
}

// ExtractPlainText converts a plain text file into []Span.
// All lines are emitted as body text; no heading detection is attempted.
func ExtractPlainText(path string) ([]Span, error) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	const (
		bodySize = 12.0
		lineH    = 14.0
		paraGap  = 30.0
	)

	var spans []Span
	y := 50.0

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			y += paraGap
			continue
		}
		spans = append(spans, Span{
			Type:     "text",
			Text:     line,
			FontSize: bodySize,
			FontName: "Helvetica",
			Page:     1,
			Y:        y,
		})
		y += lineH
	}

	if err := scanner.Err(); err != nil && err != bufio.ErrTooLong {
		return nil, err
	}
	return spans, nil
}

// parseATXHeading detects "# Title", "## Title", etc.
// Returns (level, title, true) on match.
func parseATXHeading(line string) (int, string, bool) {
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level == 0 || level > 6 {
		return 0, "", false
	}
	rest := line[level:]
	if rest == "" || rest[0] != ' ' {
		return 0, "", false
	}
	return level, strings.TrimSpace(rest), true
}

var (
	reBoldItalic    = regexp.MustCompile(`\*{1,3}([^*]+)\*{1,3}`)
	reInlineCode    = regexp.MustCompile("`([^`]+)`")
	reLink          = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	reStrikethrough = regexp.MustCompile(`~~([^~]+)~~`)
	reHTMLTag       = regexp.MustCompile(`<[^>]+>`)
)

// stripInlineMarkdown removes inline formatting markers, returning plain text.
func stripInlineMarkdown(s string) string {
	s = reStrikethrough.ReplaceAllString(s, "$1")
	s = reBoldItalic.ReplaceAllString(s, "$1")
	s = reInlineCode.ReplaceAllString(s, "$1")
	s = reLink.ReplaceAllString(s, "$1")
	s = reHTMLTag.ReplaceAllString(s, "")
	// strip leading markdown list/blockquote markers
	s = strings.TrimLeft(s, ">-*+")
	return strings.TrimSpace(s)
}
