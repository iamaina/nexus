// ExtractPDF extracts text and layout information from a PDF file.
package layout

import (
	"fmt"
	"os"
	"os/exec"
)

func ExtractPDF(path string) ([]byte, error) {
	if _, err := os.Stat(".venv/bin/python"); os.IsNotExist(err) {
		fmt.Println("❌ Python environment not set up. Run: make setup-python")
		return nil, fmt.Errorf("Python environment not set up")
	}
	cmd := exec.Command(".venv/bin/python", "scripts/extract_pdf.py", path)
	return cmd.Output()
}
