package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeProtocolAndDefaults(t *testing.T) {
	cfg := Defaults()
	if cfg.Analysis.Mode != AnalysisModeLocalAgent || cfg.Analysis.Profile != LocalAgentProviderAuto {
		t.Fatalf("analysis defaults = %+v, want local-agent/auto", cfg.Analysis)
	}
	vision := cfg.Analysis.Profiles["vision"].Vision
	if vision.RetryCount != 3 {
		t.Fatalf("retry_count = %d, want 3", vision.RetryCount)
	}
	vision.Protocol = "responses"
	vision.Model = "vision-model"
	vision.APIKey = "key"
	cfg.Analysis.Profiles["vision"] = AnalysisProfile{Type: AnalysisProfileTypeVisionAPI, Vision: vision}
	cfg.Analysis.Mode = AnalysisModeVision
	cfg.Analysis.Profile = "vision"
	cfg.Analysis.Prompt = "Analyze the screenshot."
	cfg.Telegram.Token = "token"
	cfg.Telegram.ChatID = "123"
	if err := cfg.NormalizeAndValidate(); err != nil {
		t.Fatalf("NormalizeAndValidate() error = %v", err)
	}
	vision = cfg.Analysis.Profiles["vision"].Vision
	if vision.Protocol != ProtocolOpenAIResponses {
		t.Fatalf("protocol = %q, want %q", vision.Protocol, ProtocolOpenAIResponses)
	}
	if vision.MaxTokensField != "max_output_tokens" {
		t.Fatalf("max_tokens_field = %q, want max_output_tokens", vision.MaxTokensField)
	}
	if got := cfg.Analysis.AutoProfiles; len(got) != 3 || got[0] != "codex" || got[1] != "claude" || got[2] != "opencode" {
		t.Fatalf("auto_profiles = %q, want default discovery order", got)
	}
}

func TestAutoProfilesCanBeConfigured(t *testing.T) {
	cfg := Defaults()
	cfg.Analysis.AutoProfiles = []string{" custom-agent ", "codex"}
	cfg.Analysis.Prompt = "Analyze the screenshot."
	cfg.Telegram.Token = "token"
	cfg.Telegram.ChatID = "123"
	if err := cfg.NormalizeAndValidate(); err != nil {
		t.Fatal(err)
	}
	if got := cfg.Analysis.AutoProfiles; len(got) != 2 || got[0] != "custom-agent" || got[1] != "codex" {
		t.Fatalf("auto_profiles = %q, want normalized configured order", got)
	}
}

func TestAutoProfilesRejectDuplicates(t *testing.T) {
	cfg := Defaults()
	cfg.Analysis.AutoProfiles = []string{"codex", "CODEX"}
	cfg.Analysis.Prompt = "Analyze the screenshot."
	cfg.Telegram.Token = "token"
	cfg.Telegram.ChatID = "123"
	if err := cfg.NormalizeAndValidate(); err == nil {
		t.Fatal("duplicate auto profile was accepted")
	}
}

func TestNormalizeAndValidateRejectsUnknownProtocol(t *testing.T) {
	cfg := Defaults()
	vision := cfg.Analysis.Profiles["vision"].Vision
	vision.Protocol = "some-vendor-native-api"
	vision.Model = "model"
	cfg.Analysis.Profiles["vision"] = AnalysisProfile{Type: AnalysisProfileTypeVisionAPI, Vision: vision}
	cfg.Analysis.Mode = AnalysisModeVision
	cfg.Analysis.Profile = "vision"
	cfg.Analysis.Prompt = "Analyze the screenshot."
	cfg.Telegram.Token = "token"
	cfg.Telegram.ChatID = "123"
	if err := cfg.NormalizeAndValidate(); err == nil {
		t.Fatal("NormalizeAndValidate() error = nil, want unsupported protocol error")
	}
}

func TestNormalizeAndValidateRejectsInvalidTimeoutAndMonitor(t *testing.T) {
	base := Defaults()
	base.Analysis.Prompt = "Analyze the screenshot."
	base.Telegram.Token = "token"
	base.Telegram.ChatID = "123"

	badTimeout := base
	vision := badTimeout.Analysis.Profiles["vision"].Vision
	vision.Timeout = "not-a-duration"
	badTimeout.Analysis.Profiles["vision"] = AnalysisProfile{Type: AnalysisProfileTypeVisionAPI, Vision: vision}
	if err := badTimeout.NormalizeAndValidate(); err == nil {
		t.Fatal("invalid vision timeout was accepted")
	}

	badMonitor := base
	badMonitor.Capture.Monitor = "2screens"
	if err := badMonitor.NormalizeAndValidate(); err == nil {
		t.Fatal("invalid monitor was accepted")
	}
}

func TestNormalizeAndValidateRejectsNegativeRetryCount(t *testing.T) {
	cfg := Defaults()
	vision := cfg.Analysis.Profiles["vision"].Vision
	vision.RetryCount = -1
	cfg.Analysis.Profiles["vision"] = AnalysisProfile{Type: AnalysisProfileTypeVisionAPI, Vision: vision}
	cfg.Analysis.Prompt = "Analyze the screenshot."
	cfg.Telegram.Token = "token"
	cfg.Telegram.ChatID = "123"

	if err := cfg.NormalizeAndValidate(); err == nil {
		t.Fatal("negative vision retry count was accepted")
	}
}

func TestPromptIsRequiredAndStoredOnlyOnAnalysis(t *testing.T) {
	cfg := Defaults()
	cfg.Telegram.Token = "token"
	cfg.Telegram.ChatID = "123"
	if err := cfg.NormalizeAndValidate(); err == nil {
		t.Fatal("missing analysis.prompt was accepted")
	}
	cfg.Analysis.Prompt = "Configured prompt"
	if err := cfg.NormalizeAndValidate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Analysis.Prompt != "Configured prompt" {
		t.Fatalf("prompt = %q", cfg.Analysis.Prompt)
	}
}

func TestLoadUsesUnifiedAnalysisProfiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `analysis:
  mode: local-agent
  profile: codex
  prompt: |
    Read the screenshot and summarize it.
  profiles:
    codex:
      type: local-agent
      local_agent:
        provider: codex
telegram:
  token: token
  chat_id: "123"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Analysis.Profiles["codex"].LocalAgent.Provider != "codex" {
		t.Fatalf("codex profile = %+v", cfg.Analysis.Profiles["codex"])
	}
	if cfg.Analysis.Profiles["codex"].LocalAgent.RetryCount == nil || *cfg.Analysis.Profiles["codex"].LocalAgent.RetryCount != DefaultAgentRetryCount {
		t.Fatalf("codex retry_count = %v, want %d", cfg.Analysis.Profiles["codex"].LocalAgent.RetryCount, DefaultAgentRetryCount)
	}
	if got := cfg.Analysis.Profiles["codex"].LocalAgent.ImageTransport; got != LocalAgentImageAuto {
		t.Fatalf("codex image_transport = %q, want %q", got, LocalAgentImageAuto)
	}
	if _, ok := cfg.Analysis.Profiles["claude"]; !ok {
		t.Fatal("built-in Claude profile disappeared during YAML merge")
	}
	if maxTokens := cfg.Analysis.Profiles["claude"].LocalAgent.MaxOutputTokens; maxTokens != DefaultAgentMaxOutputTokens {
		t.Fatalf("Claude max_output_tokens = %d, want %d", maxTokens, DefaultAgentMaxOutputTokens)
	}
}

func TestExampleConfigLoads(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "example-key")
	t.Setenv("TELEGRAM_BOT_TOKEN", "token")
	t.Setenv("TELEGRAM_CHAT_ID", "123")
	cfg, err := Load(filepath.Join("..", "..", "config.example.yaml"))
	if err != nil {
		t.Fatalf("config.example.yaml failed to load: %v", err)
	}
	for _, name := range []string{"codex", "claude", "opencode", "vision"} {
		if _, ok := cfg.Analysis.Profiles[name]; !ok {
			t.Fatalf("example config is missing profile %q", name)
		}
	}
}

func TestLocalAgentImageTransportIsValidated(t *testing.T) {
	cfg := Defaults()
	cfg.Analysis.Prompt = "Analyze the screenshot."
	profile := cfg.Analysis.Profiles["codex"]
	profile.LocalAgent.ImageTransport = "unsupported"
	cfg.Analysis.Profiles["codex"] = profile
	cfg.Telegram.Token = "token"
	cfg.Telegram.ChatID = "123"
	if err := cfg.NormalizeAndValidate(); err == nil {
		t.Fatal("unsupported image_transport was accepted")
	}
}

func TestLocalAgentRetryCountCanBeDisabled(t *testing.T) {
	cfg := Defaults()
	cfg.Analysis.Prompt = "Analyze the screenshot."
	cfg.Analysis.Profiles["codex"] = AnalysisProfile{
		Type: AnalysisProfileTypeLocalAgent,
		LocalAgent: LocalAgentProfile{
			Provider:   "codex",
			RetryCount: intPointer(0),
		},
	}
	cfg.Telegram.Token = "token"
	cfg.Telegram.ChatID = "123"
	if err := cfg.NormalizeAndValidate(); err != nil {
		t.Fatal(err)
	}
	if retryCount := cfg.Analysis.Profiles["codex"].LocalAgent.RetryCount; retryCount == nil || *retryCount != 0 {
		t.Fatalf("retry_count = %v, want 0", retryCount)
	}
}

func TestLoadRejectsMultipleYAMLDocuments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `analysis:
  prompt: Analyze the screenshot.
telegram:
  token: token
  chat_id: "123"
---
extra: document
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load() error = nil, want multiple-document error")
	}
}

func TestLocalAgentSessionIsNormalized(t *testing.T) {
	cfg := Defaults()
	cfg.Analysis.Prompt = "Analyze the screenshot."
	profile := cfg.Analysis.Profiles["codex"]
	profile.LocalAgent.Session = " PERSISTENT "
	cfg.Analysis.Profiles["codex"] = profile
	cfg.Telegram.Token = "token"
	cfg.Telegram.ChatID = "123"
	if err := cfg.NormalizeAndValidate(); err != nil {
		t.Fatal(err)
	}
	if got := cfg.Analysis.Profiles["codex"].LocalAgent.Session; got != LocalAgentSessionPersistent {
		t.Fatalf("session = %q, want %q", got, LocalAgentSessionPersistent)
	}
}

func TestWorkflowConfigSupportsNamedAndInlineProfiles(t *testing.T) {
	cfg := Defaults()
	cfg.Analysis.Prompt = "fallback prompt"
	cfg.Analysis.Workflow = "review"
	cfg.Analysis.Workflows = map[string]AnalysisWorkflow{
		"review": {
			Timeout: "2m",
			Steps: []AnalysisStep{
				{Name: "inspect", Profile: "codex", Prompt: "Inspect the screenshot."},
				{
					Name:   "summarize",
					Prompt: "Summarize the previous result.",
					ProfileConfig: &AnalysisProfile{
						Type:       AnalysisProfileTypeLocalAgent,
						LocalAgent: LocalAgentProfile{Provider: "deepseek", Command: "deepseek"},
					},
				},
			},
		},
	}
	cfg.Telegram.Token = "token"
	cfg.Telegram.ChatID = "123"
	if err := cfg.NormalizeAndValidate(); err != nil {
		t.Fatal(err)
	}
	workflow, err := cfg.Analysis.ActiveWorkflow()
	if err != nil {
		t.Fatal(err)
	}
	if len(workflow.Steps) != 2 || workflow.Steps[0].Profile != "codex" {
		t.Fatalf("workflow = %+v", workflow)
	}
	if workflow.Steps[1].ProfileConfig == nil || workflow.Steps[1].ProfileConfig.LocalAgent.Session != LocalAgentSessionEphemeral {
		t.Fatalf("inline profile = %+v", workflow.Steps[1].ProfileConfig)
	}
	if workflow.Steps[0].Input != WorkflowInputBoth || workflow.Steps[1].Input != WorkflowInputBoth {
		t.Fatalf("default input modes = %q, %q", workflow.Steps[0].Input, workflow.Steps[1].Input)
	}
}

func TestWorkflowConfigCanUseOnlyInlineProfiles(t *testing.T) {
	cfg := Defaults()
	cfg.Analysis.Profiles = nil
	cfg.Analysis.Prompt = "fallback prompt"
	cfg.Analysis.Workflow = "inline-only"
	cfg.Analysis.Workflows = map[string]AnalysisWorkflow{
		"inline-only": {Steps: []AnalysisStep{
			{
				Name:   "inspect",
				Prompt: "Inspect the screenshot.",
				ProfileConfig: &AnalysisProfile{
					Type:       AnalysisProfileTypeLocalAgent,
					LocalAgent: LocalAgentProfile{Provider: "codex"},
				},
			},
		}},
	}
	cfg.Telegram.Token = "token"
	cfg.Telegram.ChatID = "123"
	if err := cfg.NormalizeAndValidate(); err != nil {
		t.Fatalf("inline-only workflow was rejected: %v", err)
	}
	if len(cfg.Analysis.Profiles) != 0 {
		t.Fatalf("profiles = %v, want empty named profile registry", cfg.Analysis.Profiles)
	}
}

func TestWorkflowConfigRequiresExplicitSelectionWhenAmbiguous(t *testing.T) {
	cfg := Defaults()
	cfg.Analysis.Prompt = "fallback prompt"
	cfg.Analysis.Workflows = map[string]AnalysisWorkflow{
		"first":  {Steps: []AnalysisStep{{Profile: "codex", Prompt: "first"}}},
		"second": {Steps: []AnalysisStep{{Profile: "claude", Prompt: "second"}}},
	}
	cfg.Telegram.Token = "token"
	cfg.Telegram.ChatID = "123"
	if err := cfg.NormalizeAndValidate(); err == nil {
		t.Fatal("ambiguous workflow selection was accepted")
	}
}

func TestWorkflowConfigRejectsMissingProfileAndPrompt(t *testing.T) {
	cfg := Defaults()
	cfg.Analysis.Prompt = "fallback prompt"
	cfg.Analysis.Workflows = map[string]AnalysisWorkflow{
		"default": {Steps: []AnalysisStep{{Name: "broken", Profile: "codex"}}},
	}
	cfg.Telegram.Token = "token"
	cfg.Telegram.ChatID = "123"
	if err := cfg.NormalizeAndValidate(); err == nil {
		t.Fatal("missing step prompt was accepted")
	}

	cfg.Analysis.Workflows["default"] = AnalysisWorkflow{Steps: []AnalysisStep{{Name: "broken", Prompt: "prompt"}}}
	if err := cfg.NormalizeAndValidate(); err == nil {
		t.Fatal("missing step profile was accepted")
	}
}

func TestWorkflowConfigRejectsPreviousInputOnFirstStep(t *testing.T) {
	cfg := Defaults()
	cfg.Analysis.Prompt = "fallback prompt"
	cfg.Analysis.Workflows = map[string]AnalysisWorkflow{
		"default": {Steps: []AnalysisStep{{Profile: "codex", Input: WorkflowInputPrevious, Prompt: "prompt"}}},
	}
	cfg.Telegram.Token = "token"
	cfg.Telegram.ChatID = "123"
	if err := cfg.NormalizeAndValidate(); err == nil {
		t.Fatal("previous input was accepted on the first step")
	}
}
