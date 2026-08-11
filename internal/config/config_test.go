package config

import "testing"

func TestNormalizeProtocolAndDefaults(t *testing.T) {
	cfg := Defaults()
	if cfg.Vision.RetryCount != 3 {
		t.Fatalf("retry_count = %d, want 3", cfg.Vision.RetryCount)
	}
	cfg.Vision.Protocol = "responses"
	cfg.Vision.Model = "vision-model"
	cfg.Vision.APIKey = "key"
	cfg.Telegram.Token = "token"
	cfg.Telegram.ChatID = "123"
	if err := cfg.NormalizeAndValidate(); err != nil {
		t.Fatalf("NormalizeAndValidate() error = %v", err)
	}
	if cfg.Vision.Protocol != ProtocolOpenAIResponses {
		t.Fatalf("protocol = %q, want %q", cfg.Vision.Protocol, ProtocolOpenAIResponses)
	}
	if cfg.Vision.MaxTokensField != "max_output_tokens" {
		t.Fatalf("max_tokens_field = %q, want max_output_tokens", cfg.Vision.MaxTokensField)
	}
}

func TestNormalizeAndValidateRejectsUnknownProtocol(t *testing.T) {
	cfg := Defaults()
	cfg.Vision.Protocol = "some-vendor-native-api"
	cfg.Vision.Model = "model"
	cfg.Telegram.Token = "token"
	cfg.Telegram.ChatID = "123"
	if err := cfg.NormalizeAndValidate(); err == nil {
		t.Fatal("NormalizeAndValidate() error = nil, want unsupported protocol error")
	}
}

func TestNormalizeAndValidateRejectsInvalidTimeoutAndMonitor(t *testing.T) {
	base := Defaults()
	base.Vision.Model = "model"
	base.Telegram.Token = "token"
	base.Telegram.ChatID = "123"

	badTimeout := base
	badTimeout.Vision.Timeout = "not-a-duration"
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
	cfg.Vision.Model = "model"
	cfg.Vision.RetryCount = -1
	cfg.Telegram.Token = "token"
	cfg.Telegram.ChatID = "123"

	if err := cfg.NormalizeAndValidate(); err == nil {
		t.Fatal("negative vision retry count was accepted")
	}
}
