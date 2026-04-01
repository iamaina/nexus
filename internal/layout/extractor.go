package layout

import (
	"fmt"
	"os"
	"os/exec"
)

// ExtractPDF runs the Python extractor on path and returns raw span JSON.
func ExtractPDF(path string) ([]byte, error) {
	if _, err := os.Stat(".venv/bin/python"); os.IsNotExist(err) {
		return nil, fmt.Errorf("python environment not set up — run: make setup-python")
	}
	cmd := exec.Command(".venv/bin/python", "scripts/extract_pdf.py", path) //nolint:gosec
	return cmd.Output()
}
