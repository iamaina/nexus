// Package worktrack provides helpers for reading and updating work-tracking files.
package worktrack

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Dir returns the expanded work-tracking directory path.
func Dir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "ops-nexus", "intelligence", "work-tracking")
}

// List returns the base names of all .md files in the work-tracking directory,
// excluding TEMPLATE.md.
func List() ([]string, error) {
	entries, err := os.ReadDir(Dir())
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() && strings.HasSuffix(name, ".md") && name != "TEMPLATE.md" {
			names = append(names, strings.TrimSuffix(name, ".md"))
		}
	}
	return names, nil
}

// AppendNote appends a timestamped note to the "Where I left off" section of
// the named work-tracking file (matched case-insensitively by substring).
// If no file matches, it returns an error listing candidates.
func AppendNote(fileFragment, note string) (destPath string, err error) {
	match, err := resolve(fileFragment)
	if err != nil {
		return "", err
	}

	f, err := os.OpenFile(match, os.O_APPEND|os.O_WRONLY, 0o640) //nolint:gosec // path is from resolved file list
	if err != nil {
		return "", fmt.Errorf("open %s: %w", filepath.Base(match), err)
	}
	defer func() { _ = f.Close() }()

	stamp := time.Now().Format("2006-01-02 15:04")
	entry := fmt.Sprintf("\n### %s\n\n- %s\n", stamp, note)
	if _, wErr := f.WriteString(entry); wErr != nil {
		return "", fmt.Errorf("write: %w", wErr)
	}
	return match, nil
}

// CreateFromTemplate creates a new work-tracking file from TEMPLATE.md,
// substituting the given title. Returns the created file path.
func CreateFromTemplate(slug, title string) (string, error) {
	tmplPath := filepath.Join(Dir(), "TEMPLATE.md")
	tmpl, err := os.ReadFile(tmplPath) //nolint:gosec // fixed path under user's home
	if err != nil {
		return "", fmt.Errorf("read template: %w", err)
	}

	today := time.Now().Format("2006-01-02")
	content := strings.ReplaceAll(string(tmpl), "Issue XXXXX — Title", title)
	content = strings.ReplaceAll(content, "YYYY-MM-DD → ongoing", today+" → ongoing")
	content = strings.ReplaceAll(content, "YYYY-MM-DD\n\n-", today+"\n\n-")

	dest := filepath.Join(Dir(), slug+".md")
	if _, err := os.Stat(dest); err == nil {
		return "", fmt.Errorf("file already exists: %s", slug+".md")
	}
	if err := os.WriteFile(dest, []byte(content), 0o640); err != nil { //nolint:gosec // fixed path
		return "", fmt.Errorf("write: %w", err)
	}
	return dest, nil
}

// ReadTemplate returns the raw content of TEMPLATE.md.
func ReadTemplate() (string, error) {
	tmplPath := filepath.Join(Dir(), "TEMPLATE.md")
	data, err := os.ReadFile(tmplPath) //nolint:gosec // fixed path under user's home
	if err != nil {
		return "", fmt.Errorf("read template: %w", err)
	}
	return string(data), nil
}

// ReadFilePath returns the resolved absolute path and content of the named
// work-tracking file (matched case-insensitively by substring).
func ReadFilePath(fileFragment string) (path string, content string, err error) {
	match, err := resolve(fileFragment)
	if err != nil {
		return "", "", err
	}
	data, err := os.ReadFile(match) //nolint:gosec // path is from resolved file list
	if err != nil {
		return "", "", fmt.Errorf("read %s: %w", filepath.Base(match), err)
	}
	return match, string(data), nil
}

// WriteNew writes content to slug.md in the work-tracking directory.
// Returns an error if the file already exists.
func WriteNew(slug, content string) (string, error) {
	dest := filepath.Join(Dir(), slug+".md")
	if _, err := os.Stat(dest); err == nil {
		return "", fmt.Errorf("file already exists: %s.md", slug)
	}
	if err := os.WriteFile(dest, []byte(content), 0o640); err != nil { //nolint:gosec // fixed path
		return "", fmt.Errorf("write: %w", err)
	}
	return dest, nil
}

// UpdateByPath updates the "Next action" blockquote and prepends a new dated
// journal entry to the "Where I left off" section of the file at path.
func UpdateByPath(path, nextAction string, journalLines []string) error {
	data, err := os.ReadFile(path) //nolint:gosec // path is from resolved file list
	if err != nil {
		return fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	updated := string(data)
	if nextAction != "" {
		updated = updateNextAction(updated, nextAction)
	}
	if len(journalLines) > 0 {
		updated = prependJournalEntry(updated, journalLines)
	}
	if err := os.WriteFile(path, []byte(updated), 0o640); err != nil { //nolint:gosec // path is from resolved file list
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

// updateNextAction replaces the blockquote line immediately under
// "## Next action" with the given action text, but only when the text
// has actually changed. This prevents nexus from silently overwriting a
// next action that Claude (or the user) already refined.
func updateNextAction(content, action string) string {
	action = strings.TrimSpace(action)
	lines := strings.Split(content, "\n")
	inSection := false
	for i, line := range lines {
		if line == "## Next action" {
			inSection = true
			continue
		}
		if inSection && strings.HasPrefix(line, ">") {
			existing := strings.TrimSpace(strings.TrimPrefix(line, ">"))
			if existing == action {
				return content // no change — preserve whatever Claude wrote
			}
			lines[i] = "> " + action
			return strings.Join(lines, "\n")
		}
		if inSection && strings.HasPrefix(line, "##") {
			break
		}
	}
	return content
}

// prependJournalEntry inserts a new dated entry at the top of the
// "## Where I left off" section.
func prependJournalEntry(content string, lines []string) string {
	stamp := time.Now().Format("2006-01-02 15:04")
	var entry strings.Builder
	fmt.Fprintf(&entry, "### %s\n\n", stamp)
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if !strings.HasPrefix(l, "-") {
			l = "- " + l
		}
		fmt.Fprintf(&entry, "%s\n", l)
	}
	entry.WriteString("\n")

	const sectionHeader = "## Where I left off"
	idx := strings.Index(content, sectionHeader)
	if idx < 0 {
		return content + "\n" + entry.String()
	}

	// Find the first existing ### entry after the section header.
	after := idx + len(sectionHeader)
	subIdx := strings.Index(content[after:], "\n### ")
	if subIdx < 0 {
		return content + entry.String()
	}
	insertAt := after + subIdx + 1 // keep the \n before ###
	return content[:insertAt] + entry.String() + "\n" + content[insertAt:]
}

// UpdateResumeSession rewrites the `nexus --resume <session-id>` line in the
// Resume section with the real session name. If the line is not found or the
// value is already correct, the file is left unchanged.
func UpdateResumeSession(path, sessionName string) error {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	updated := updateNexusResumeLine(string(data), sessionName)
	if updated == string(data) {
		return nil
	}
	return os.WriteFile(path, []byte(updated), 0o640) //nolint:gosec
}

func updateNexusResumeLine(content, sessionName string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "nexus --resume ") {
			if trimmed == "nexus --resume "+sessionName {
				return content // already up to date
			}
			lines[i] = "nexus --resume " + sessionName
			return strings.Join(lines, "\n")
		}
	}
	return content
}

// resolve finds a work-tracking file whose name contains fileFragment
// (case-insensitive). Returns an error if no match or multiple matches.
func resolve(fileFragment string) (string, error) {
	entries, err := os.ReadDir(Dir())
	if err != nil {
		return "", err
	}

	lower := strings.ToLower(fileFragment)
	var matches []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() && strings.HasSuffix(name, ".md") && name != "TEMPLATE.md" {
			if strings.Contains(strings.ToLower(name), lower) {
				matches = append(matches, filepath.Join(Dir(), name))
			}
		}
	}

	switch len(matches) {
	case 0:
		names, _ := List()
		return "", fmt.Errorf("no work-tracking file matches %q\n  Available: %s", fileFragment, strings.Join(names, ", "))
	case 1:
		return matches[0], nil
	default:
		short := make([]string, len(matches))
		for i, m := range matches {
			short[i] = filepath.Base(m)
		}
		return "", fmt.Errorf("ambiguous: %q matches %s\n  Be more specific", fileFragment, strings.Join(short, ", "))
	}
}
