package classifier

import (
	"strings"
	"testing"
)

// --- sanitise tests ---

func TestSanitise_UnknownInstitutionCleared(t *testing.T) {
	cl := &Classification{Institution: "Unknown"}
	cl.sanitise()
	if cl.Institution != "" {
		t.Errorf("expected empty institution, got %q", cl.Institution)
	}
}

func TestSanitise_UnknownInstitutionCaseInsensitive(t *testing.T) {
	for _, val := range []string{"UNKNOWN", "unknown", "Unknown"} {
		cl := &Classification{Institution: val}
		cl.sanitise()
		if cl.Institution != "" {
			t.Errorf("institution %q should be cleared, got %q", val, cl.Institution)
		}
	}
}

func TestSanitise_ExtensionStrippedFromFilename(t *testing.T) {
	cl := &Classification{Filename: "2024-03_ING_Bank_Statement.pdf", DestDir: "finance"}
	cl.sanitise()
	if cl.Filename != "2024-03_ING_Bank_Statement" {
		t.Errorf("expected extension stripped, got %q", cl.Filename)
	}
}

func TestSanitise_EmptyDestDirDefaultsToOther(t *testing.T) {
	cl := &Classification{Filename: "doc"}
	cl.sanitise()
	if cl.DestDir != "other" {
		t.Errorf("expected dest_dir 'other', got %q", cl.DestDir)
	}
}

func TestSanitise_EmptyFilenameDefaultsToDocument(t *testing.T) {
	cl := &Classification{DestDir: "finance"}
	cl.sanitise()
	if cl.Filename != "document" {
		t.Errorf("expected filename 'document', got %q", cl.Filename)
	}
}

func TestSanitise_DocTypeAndLanguageLowercased(t *testing.T) {
	cl := &Classification{DocType: "INVOICE", Language: "EN", DestDir: "finance", Filename: "doc"}
	cl.sanitise()
	if cl.DocType != "invoice" {
		t.Errorf("expected lowercase doc_type, got %q", cl.DocType)
	}
	if cl.Language != "en" {
		t.Errorf("expected lowercase language, got %q", cl.Language)
	}
}

func TestSanitise_WhitespaceStripped(t *testing.T) {
	cl := &Classification{
		DocType:     "  invoice  ",
		Language:    " nl ",
		Institution: " ING ",
		Filename:    " 2024-01_ING_statement ",
		DestDir:     " finance/bank-statements ",
	}
	cl.sanitise()
	if cl.DocType != "invoice" {
		t.Errorf("expected trimmed doc_type, got %q", cl.DocType)
	}
	if cl.Institution != "ING" {
		t.Errorf("expected trimmed institution, got %q", cl.Institution)
	}
	if cl.Filename != "2024-01_ING_statement" {
		t.Errorf("expected trimmed filename, got %q", cl.Filename)
	}
}

// --- buildPrompt tests ---

func TestBuildPrompt_ContainsFilename(t *testing.T) {
	prompt := buildPrompt("invoice-april-2026.pdf", "")
	if !strings.Contains(prompt, "invoice-april-2026.pdf") {
		t.Errorf("prompt should contain the filename, got:\n%s", prompt)
	}
}

func TestBuildPrompt_ContainsPreviewWhenProvided(t *testing.T) {
	preview := "INVOICE\nCanva BV\nAmount due: €29.00"
	prompt := buildPrompt("invoice.pdf", preview)
	if !strings.Contains(prompt, preview) {
		t.Errorf("prompt should contain the document preview, got:\n%s", prompt)
	}
}

func TestBuildPrompt_NoPreviewSectionWhenEmpty(t *testing.T) {
	prompt := buildPrompt("invoice.pdf", "")
	if strings.Contains(prompt, "Document text") {
		t.Errorf("prompt should not contain a Document text section when preview is empty, got:\n%s", prompt)
	}
}

func TestBuildPrompt_ReturnsJSONInstruction(t *testing.T) {
	prompt := buildPrompt("doc.pdf", "")
	if !strings.Contains(prompt, "doc_type") {
		t.Errorf("prompt should include JSON schema fields, got:\n%s", prompt)
	}
}
