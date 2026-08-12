package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/th1nk-er/ScreenLens/internal/analyzer"
	"github.com/th1nk-er/ScreenLens/internal/config"
	"github.com/th1nk-er/ScreenLens/internal/workflow"
)

func TestSameUserIDsIgnoresOrder(t *testing.T) {
	if !sameUserIDs([]int64{3, 1, 2}, []int64{2, 3, 1}) {
		t.Fatal("sameUserIDs() = false, want true")
	}
	if sameUserIDs([]int64{1, 2}, []int64{1, 3}) {
		t.Fatal("sameUserIDs() = true, want false")
	}
}

func TestFormatAgentsIsStableAndSeparatesProfileTypes(t *testing.T) {
	cfg := config.Defaults()
	got := formatAgents(cfg)
	if got != "# Local agent profiles\n\nUse `/screen [profile]` to select one for a single capture.\n\n- **Active:** `auto`\n- `vision` (hosted vision API)\n- `claude`\n- `codex`\n- `opencode`" {
		t.Fatalf("formatAgents() = %q", got)
	}
}

func TestFormatStatusShowsManualProfileWhenWorkflowsAreConfigured(t *testing.T) {
	cfg := config.Defaults()
	cfg.Analysis.Workflows = map[string]config.AnalysisWorkflow{
		"review": {Steps: []config.AnalysisStep{{Name: "inspect", Profile: "codex", Prompt: "inspect"}}},
	}
	cfg.Analysis.Workflow = "review"
	status := workflow.Status{Profile: "codex", Backend: "codex"}
	formatted := formatStatus(status, cfg)
	if !strings.Contains(formatted, "**Analysis mode:** `local-agent`") || strings.Contains(formatted, "**Analysis workflow:**") {
		t.Fatalf("status = %q", formatted)
	}
}

func TestBuildWorkflowAnalyzerChainsVisionProfiles(t *testing.T) {
	var prompts []string
	responses := []string{"codex result", "claude result"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		messages := payload["messages"].([]any)
		content := messages[0].(map[string]any)["content"].([]any)
		prompts = append(prompts, content[0].(map[string]any)["text"].(string))
		index := len(prompts) - 1
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"` + responses[index] + `"}}]}`))
	}))
	defer server.Close()

	cfg := config.Defaults()
	cfg.Analysis.Prompt = "fallback"
	cfg.Analysis.Workflow = "review"
	cfg.Analysis.Workflows = map[string]config.AnalysisWorkflow{
		"review": {Steps: []config.AnalysisStep{
			{Name: "inspect", Profile: "first", Prompt: "Inspect the screenshot."},
			{Name: "summarize", Profile: "second", Prompt: "Summarize the previous output."},
		}},
	}
	first := cfg.Analysis.Profiles["vision"]
	first.Vision.Endpoint = server.URL
	first.Vision.Model = "first-model"
	second := first
	second.Vision.Model = "second-model"
	cfg.Analysis.Profiles = map[string]config.AnalysisProfile{
		"first":  first,
		"second": second,
	}
	cfg.Telegram.Token = "token"
	cfg.Telegram.ChatID = "123"
	if err := cfg.NormalizeAndValidate(); err != nil {
		t.Fatal(err)
	}

	backend, name, err := buildWorkflowAnalyzer(cfg, config.MIMETypeJPEG)
	if err != nil {
		t.Fatal(err)
	}
	result, err := backend.Analyze(context.Background(), analyzer.Request{Image: []byte("screen"), MIMEType: config.MIMETypeJPEG})
	if err != nil {
		t.Fatal(err)
	}
	if name != "workflow:review" || result.Text != "claude result" || result.Workflow != "review" {
		t.Fatalf("name = %q, result = %+v", name, result)
	}
	if len(prompts) != 2 || !strings.Contains(prompts[1], "codex result") {
		t.Fatalf("prompts = %q", prompts)
	}
}
