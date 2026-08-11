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
	if _, ok := cfg.Analysis.Profiles["claude"]; !ok {
		t.Fatal("built-in Claude profile disappeared during YAML merge")
	}
	if maxTokens := cfg.Analysis.Profiles["claude"].LocalAgent.MaxOutputTokens; maxTokens != DefaultAgentMaxOutputTokens {
		t.Fatalf("Claude max_output_tokens = %d, want %d", maxTokens, DefaultAgentMaxOutputTokens)
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
