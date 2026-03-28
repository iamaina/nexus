// ExtractPDF extracts text and layout information from a PDF file.
package layout

import (
	"os/exec"
)

func ExtractPDF(path string) ([]byte, error) {
	cmd := exec.Command("python3", "scripts/extract_pdf.py", path)
	return cmd.Output()
}
