// Package gitlab provides on-demand context fetching from GitLab via the glab CLI.
package gitlab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"strings"

	"github.com/iamaina/nexus/internal/live"
)

// builtinHosts are recognised GitLab hostnames without extra configuration.
var builtinHosts = map[string]bool{
	"gitlab.com":         true,
	"ops.gitlab.net":     true,
	"pre.gitlab.com":     true,
	"staging.gitlab.com": true,
	"dev.gitlab.org":     true,
}

// resource is a parsed GitLab resource URL.
type resource struct {
	Host    string
	Path    string // namespace/project or group/path (no leading slash)
	Kind    string // issues | merge_requests | work_items | epics
	ID      string // numeric ID, or "" for a list
	IsGroup bool
}

// ExtractAndFetch scans text for GitLab URLs and returns a live.Output per URL
// that could be fetched. extraHosts extends the built-in set (e.g. private instances).
func ExtractAndFetch(ctx context.Context, text string, extraHosts []string) []live.Output {
	hosts := buildHostSet(extraHosts)
	var out []live.Output
	seen := make(map[string]bool)

	for word := range strings.FieldsSeq(text) {
		word = strings.Trim(word, ".,;:!?\"'()[]<>")
		if !strings.HasPrefix(word, "http") || !strings.Contains(word, "/-/") {
			continue
		}
		if seen[word] {
			continue
		}
		seen[word] = true

		r, err := parseURL(word, hosts)
		if err != nil {
			continue
		}
		name, text, err := fetchResource(ctx, r)
		out = append(out, live.Output{Name: name, Text: text, Err: err})
	}
	return out
}

// FetchTodos fetches the user's pending GitLab todos from the given host.
// Pass "gitlab.com" for the default.
func FetchTodos(ctx context.Context, host string) live.Output {
	data, err := callGlab(ctx, host, "/todos?state=pending&per_page=20")
	if err != nil {
		return live.Output{Name: "gl-todos", Err: err}
	}
	return live.Output{Name: "gl-todos", Text: formatTodos(data)}
}

// FetchGroupItems fetches open work items / issues from a GitLab group.
// groupPath is the namespace path, e.g. "gitlab-com/gl-infra/software-delivery".
func FetchGroupItems(ctx context.Context, host, groupPath string) live.Output {
	encoded := strings.ReplaceAll(groupPath, "/", "%2F")
	shortName := groupPath
	if parts := strings.Split(groupPath, "/"); len(parts) > 0 {
		shortName = parts[len(parts)-1]
	}

	// Try work_items API first (GitLab 15.1+), fall back to issues.
	data, err := callGlab(ctx, host,
		fmt.Sprintf("/groups/%s/work_items?state=opened&sort=updated_desc&per_page=20", encoded))
	if err != nil {
		data, err = callGlab(ctx, host,
			fmt.Sprintf("/groups/%s/issues?state=opened&sort=updated_desc&per_page=20", encoded))
		if err != nil {
			return live.Output{Name: "gl:" + shortName, Err: err}
		}
	}
	return live.Output{Name: "gl:" + shortName, Text: formatItemList(data, groupPath)}
}

// ParseGroupArg converts a /gl items argument (URL or path) to host + group path.
func ParseGroupArg(arg string) (host, groupPath string) {
	if strings.HasPrefix(arg, "http") {
		u, err := url.Parse(arg)
		if err == nil {
			p := u.Path
			// Strip "/groups/" prefix if present
			p = strings.TrimPrefix(p, "/groups/")
			// Strip "/-/..." suffix
			if idx := strings.Index(p, "/-/"); idx >= 0 {
				p = p[:idx]
			}
			return u.Host, strings.Trim(p, "/")
		}
	}
	return "gitlab.com", strings.Trim(arg, "/")
}

// ─── internal ────────────────────────────────────────────────────────────────

func buildHostSet(extra []string) map[string]bool {
	hosts := make(map[string]bool, len(builtinHosts)+len(extra))
	for h := range builtinHosts {
		hosts[h] = true
	}
	for _, h := range extra {
		if h != "" {
			hosts[h] = true
		}
	}
	return hosts
}

func parseURL(rawURL string, hosts map[string]bool) (*resource, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if !hosts[u.Host] {
		return nil, fmt.Errorf("unknown host: %s", u.Host)
	}

	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")

	// Locate the "-" segment that forms the "/-/" separator.
	dashIdx := -1
	for i, p := range parts {
		if p == "-" {
			dashIdx = i
			break
		}
	}
	if dashIdx < 1 {
		return nil, fmt.Errorf("not a resource URL")
	}

	var kind, id string
	if dashIdx+1 < len(parts) {
		kind = parts[dashIdx+1]
	}
	if dashIdx+2 < len(parts) {
		id = parts[dashIdx+2]
	}

	switch kind {
	case "issues", "merge_requests", "work_items", "epics":
	default:
		return nil, fmt.Errorf("unsupported kind: %s", kind)
	}

	isGroup := parts[0] == "groups"
	var entityPath string
	if isGroup {
		entityPath = strings.Join(parts[1:dashIdx], "/")
	} else {
		entityPath = strings.Join(parts[:dashIdx], "/")
	}

	return &resource{Host: u.Host, Path: entityPath, Kind: kind, ID: id, IsGroup: isGroup}, nil
}

func (r *resource) apiPath() string {
	encoded := strings.ReplaceAll(r.Path, "/", "%2F")
	prefix := "projects"
	if r.IsGroup {
		prefix = "groups"
	}
	if r.ID != "" {
		return fmt.Sprintf("/%s/%s/%s/%s", prefix, encoded, r.Kind, r.ID)
	}
	return fmt.Sprintf("/%s/%s/%s?state=opened&sort=updated_desc&per_page=20", prefix, encoded, r.Kind)
}

func (r *resource) label() string {
	parts := strings.Split(r.Path, "/")
	shortName := parts[len(parts)-1]
	kindMap := map[string]string{
		"issues":         "issue",
		"merge_requests": "MR",
		"work_items":     "work item",
		"epics":          "epic",
	}
	k := kindMap[r.Kind]
	if k == "" {
		k = r.Kind
	}
	if r.ID != "" {
		return fmt.Sprintf("gl:%s %s #%s", shortName, k, r.ID)
	}
	return fmt.Sprintf("gl:%s %ss", shortName, k)
}

func fetchResource(ctx context.Context, r *resource) (name, text string, err error) {
	data, err := callGlab(ctx, r.Host, r.apiPath())
	if err != nil && r.Kind == "work_items" {
		// Fall back: work_items API may not be available on older instances.
		fallback := *r
		fallback.Kind = "issues"
		data, err = callGlab(ctx, fallback.Host, fallback.apiPath())
	}
	if err != nil {
		return r.label(), "", err
	}

	name = r.label()
	if r.ID != "" {
		switch r.Kind {
		case "merge_requests":
			text = formatSingleMR(data)
		default:
			text = formatSingleIssue(data)
		}
	} else {
		text = formatItemList(data, r.Path)
	}
	return name, text, nil
}

func callGlab(ctx context.Context, host, apiPath string) ([]byte, error) {
	var args []string
	if host != "gitlab.com" {
		args = []string{"--hostname", host, "api", apiPath}
	} else {
		args = []string{"api", apiPath}
	}
	cmd := exec.CommandContext(ctx, "glab", args...) //nolint:gosec // glab is a trusted CLI
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("glab %s: %w — %s", apiPath, err, strings.TrimSpace(errBuf.String()))
	}
	return out.Bytes(), nil
}

// ─── formatters ──────────────────────────────────────────────────────────────

type issueJSON struct {
	IID         int             `json:"iid"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	State       string          `json:"state"`
	Labels      json.RawMessage `json:"labels"`
	Assignees   []struct {
		Username string `json:"username"`
	} `json:"assignees"`
	Author struct {
		Username string `json:"username"`
	} `json:"author"`
	WebURL       string `json:"web_url"`
	WorkItemType struct {
		Name string `json:"name"`
	} `json:"work_item_type"`
}

type mrJSON struct {
	IID         int             `json:"iid"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	State       string          `json:"state"`
	Labels      json.RawMessage `json:"labels"`
	Author      struct {
		Username string `json:"username"`
	} `json:"author"`
	Assignees []struct {
		Username string `json:"username"`
	} `json:"assignees"`
	WebURL       string `json:"web_url"`
	SourceBranch string `json:"source_branch"`
	TargetBranch string `json:"target_branch"`
	Draft        bool   `json:"draft"`
}

type todoJSON struct {
	ActionName string `json:"action_name"`
	TargetType string `json:"target_type"`
	Target     struct {
		IID    int    `json:"iid"`
		Title  string `json:"title"`
		WebURL string `json:"web_url"`
		State  string `json:"state"`
	} `json:"target"`
	Project struct {
		NameWithNamespace string `json:"name_with_namespace"`
	} `json:"project"`
}

func parseLabels(raw json.RawMessage) []string {
	if raw == nil {
		return nil
	}
	// GitLab >= 14 returns label objects; older versions return plain strings.
	var objs []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &objs); err == nil && len(objs) > 0 {
		out := make([]string, len(objs))
		for i, o := range objs {
			out[i] = o.Name
		}
		return out
	}
	var strs []string
	_ = json.Unmarshal(raw, &strs)
	return strs
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

func formatSingleIssue(data []byte) string {
	var issue issueJSON
	if err := json.Unmarshal(data, &issue); err != nil {
		return string(data)
	}
	var sb strings.Builder
	kind := "Issue"
	if issue.WorkItemType.Name != "" {
		kind = issue.WorkItemType.Name
	}
	// Use markdown link so the title is clickable in the rendered output.
	if issue.WebURL != "" {
		fmt.Fprintf(&sb, "**%s #%d:** [%s](%s)\n", kind, issue.IID, issue.Title, issue.WebURL)
	} else {
		fmt.Fprintf(&sb, "**%s #%d:** %s\n", kind, issue.IID, issue.Title)
	}
	fmt.Fprintf(&sb, "State: %s  Author: @%s\n", issue.State, issue.Author.Username)
	if len(issue.Assignees) > 0 {
		names := make([]string, len(issue.Assignees))
		for i, a := range issue.Assignees {
			names[i] = "@" + a.Username
		}
		fmt.Fprintf(&sb, "Assignees: %s\n", strings.Join(names, ", "))
	} else {
		sb.WriteString("Assignees: none\n")
	}
	if labels := parseLabels(issue.Labels); len(labels) > 0 {
		fmt.Fprintf(&sb, "Labels: %s\n", strings.Join(labels, ", "))
	}
	if issue.Description != "" {
		fmt.Fprintf(&sb, "\n%s\n", truncate(issue.Description, 800))
	}
	return sb.String()
}

func formatSingleMR(data []byte) string {
	var mr mrJSON
	if err := json.Unmarshal(data, &mr); err != nil {
		return string(data)
	}
	var sb strings.Builder
	title := mr.Title
	if mr.Draft {
		title = "[Draft] " + title
	}
	if mr.WebURL != "" {
		fmt.Fprintf(&sb, "**MR !%d:** [%s](%s)\n", mr.IID, title, mr.WebURL)
	} else {
		fmt.Fprintf(&sb, "**MR !%d:** %s\n", mr.IID, title)
	}
	fmt.Fprintf(&sb, "State: %s  `%s` → `%s`\n", mr.State, mr.SourceBranch, mr.TargetBranch)
	fmt.Fprintf(&sb, "Author: @%s\n", mr.Author.Username)
	if len(mr.Assignees) > 0 {
		names := make([]string, len(mr.Assignees))
		for i, a := range mr.Assignees {
			names[i] = "@" + a.Username
		}
		fmt.Fprintf(&sb, "Assignees: %s\n", strings.Join(names, ", "))
	}
	if labels := parseLabels(mr.Labels); len(labels) > 0 {
		fmt.Fprintf(&sb, "Labels: %s\n", strings.Join(labels, ", "))
	}
	if mr.Description != "" {
		fmt.Fprintf(&sb, "\n%s\n", truncate(mr.Description, 800))
	}
	return sb.String()
}

func formatTodos(data []byte) string {
	var todos []todoJSON
	if err := json.Unmarshal(data, &todos); err != nil {
		return string(data)
	}
	if len(todos) == 0 {
		return "No pending GitLab todos."
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "**Pending GitLab todos (%d):**\n\n", len(todos))
	for i, t := range todos {
		if i >= 15 {
			fmt.Fprintf(&sb, "\n… and %d more\n", len(todos)-i)
			break
		}
		// Render title as a markdown link so it's clickable after glamour renders it.
		if t.Target.WebURL != "" {
			fmt.Fprintf(&sb, "- `%s` **%s** [%s](%s)\n",
				t.ActionName, t.TargetType, t.Target.Title, t.Target.WebURL)
		} else {
			fmt.Fprintf(&sb, "- `%s` **%s** #%d: %s\n",
				t.ActionName, t.TargetType, t.Target.IID, t.Target.Title)
		}
		if t.Project.NameWithNamespace != "" {
			fmt.Fprintf(&sb, "  *%s*\n", t.Project.NameWithNamespace)
		}
	}
	return sb.String()
}

func formatItemList(data []byte, groupPath string) string {
	var items []issueJSON
	if err := json.Unmarshal(data, &items); err != nil {
		return string(data)
	}
	shortName := groupPath
	if parts := strings.Split(groupPath, "/"); len(parts) > 0 {
		shortName = parts[len(parts)-1]
	}
	if len(items) == 0 {
		return fmt.Sprintf("No open items in %s.", shortName)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "**Open items in %s (%d):**\n\n", shortName, len(items))
	for i, item := range items {
		if i >= 15 {
			fmt.Fprintf(&sb, "\n… and %d more\n", len(items)-i)
			break
		}
		assignee := "unassigned"
		if len(item.Assignees) > 0 {
			assignee = "@" + item.Assignees[0].Username
		}
		if item.WebURL != "" {
			fmt.Fprintf(&sb, "- [%s](%s) `%s`\n", item.Title, item.WebURL, assignee)
		} else {
			fmt.Fprintf(&sb, "- #%d [%s]: %s\n", item.IID, assignee, item.Title)
		}
	}
	return sb.String()
}
