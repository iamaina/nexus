package summarizer

import (
	"errors"
	"strings"
	"testing"

	"github.com/iamaina/nexus/internal/live"
	"github.com/iamaina/nexus/internal/models"
)

// result is a helper to build a models.Result without repeating field names.
func result(file, chapter, text string) models.Result {
	return models.Result{File: file, Chapter: chapter, Text: text}
}

func TestBuildPrompt_CitationUsesDocumentName(t *testing.T) {
	results := []models.Result{
		result("/docs/progit.pdf", "The Three States", "The staging area stores what goes into your next commit."),
	}
	prompt := buildPrompt("What is the staging area?", results, nil)

	if !strings.Contains(prompt, "[progit — The Three States]") {
		t.Errorf("expected citation [progit — The Three States] in prompt, got:\n%s", prompt)
	}
}

func TestBuildPrompt_NoNumberedCitations(t *testing.T) {
	results := []models.Result{
		result("/docs/progit.pdf", "Git Basics", "some text"),
		result("/docs/progit.pdf", "Branching", "more text"),
	}
	prompt := buildPrompt("question", results, nil)

	if strings.Contains(prompt, "[1]") || strings.Contains(prompt, "[2]") {
		t.Errorf("prompt must not contain numbered citations [1]/[2], got:\n%s", prompt)
	}
}

func TestBuildPrompt_ChapterOptional(t *testing.T) {
	results := []models.Result{
		result("/docs/kubernetes-guide.pdf", "", "Pods are the smallest deployable units."),
	}
	prompt := buildPrompt("What is a pod?", results, nil)

	if !strings.Contains(prompt, "[kubernetes-guide]") {
		t.Errorf("expected citation [kubernetes-guide] when no chapter, got:\n%s", prompt)
	}
	if strings.Contains(prompt, "kubernetes-guide —") {
		t.Errorf("citation should not include em-dash when chapter is empty, got:\n%s", prompt)
	}
}

func TestBuildPrompt_LiveSectionIncluded(t *testing.T) {
	outputs := []live.Output{
		{Name: "kubectl", Command: "kubectl get pods", Text: "NAME   READY   STATUS\napi    1/1     Running"},
	}
	prompt := buildPrompt("What pods are running?", nil, outputs)

	if !strings.Contains(prompt, "[live:kubectl]") {
		t.Errorf("expected [live:kubectl] in prompt, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Live Context") {
		t.Errorf("expected Live Context section header in prompt, got:\n%s", prompt)
	}
}

func TestBuildPrompt_FailedLiveOutputsExcluded(t *testing.T) {
	outputs := []live.Output{
		{Name: "kubectl", Command: "kubectl get pods", Err: errors.New("connection refused")},
		{Name: "terraform", Command: "terraform show", Text: "resource \"aws_instance\" ..."},
	}
	prompt := buildPrompt("question", nil, outputs)

	if strings.Contains(prompt, "[live:kubectl]") {
		t.Errorf("failed live output should be excluded from prompt")
	}
	if !strings.Contains(prompt, "[live:terraform]") {
		t.Errorf("successful live output should be included in prompt")
	}
}

func TestBuildPrompt_EmptyLiveOutputTextExcluded(t *testing.T) {
	outputs := []live.Output{
		{Name: "kubectl", Command: "kubectl get pods", Text: ""},
	}
	prompt := buildPrompt("question", nil, outputs)

	if strings.Contains(prompt, "Live Context") {
		t.Errorf("Live Context section should be absent when all outputs are empty")
	}
}

func TestBuildPrompt_KnowledgeBaseSectionAbsentWhenNoResults(t *testing.T) {
	outputs := []live.Output{
		{Name: "kubectl", Command: "kubectl get pods", Text: "api 1/1 Running"},
	}
	prompt := buildPrompt("question", nil, outputs)

	if strings.Contains(prompt, "Knowledge Base:") {
		t.Errorf("Knowledge Base section should be absent when results is empty")
	}
}

func TestSummarizeWithLive_EmptyInputsReturnsNoInfoMessage(t *testing.T) {
	s := &OllamaSummarizer{model: "test"}
	msg, err := s.SummarizeWithLive(t.Context(), "question", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(msg, "couldn't find any relevant information") {
		t.Errorf("expected no-info message, got: %q", msg)
	}
}
