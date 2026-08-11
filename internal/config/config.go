package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	ProtocolOpenAIChat        = "openai-chat-completions"
	ProtocolOpenAIResponses   = "openai-responses"
	ProtocolAnthropicMessages = "anthropic-messages"
)

// Config is the complete runtime configuration for ScreenLens.
type Config struct {
	App      AppConfig      `yaml:"app"`
	Hotkey   HotkeyConfig   `yaml:"hotkey"`
	Capture  CaptureConfig  `yaml:"capture"`
	Vision   VisionConfig   `yaml:"vision"`
	Telegram TelegramConfig `yaml:"telegram"`
	Tray     TrayConfig     `yaml:"tray"`
}

type AppConfig struct {
	Name    string `yaml:"name"`
	LogFile string `yaml:"log_file"`
}

type HotkeyConfig struct {
	Enabled bool   `yaml:"enabled"`
	Capture string `yaml:"capture"`
}

type CaptureConfig struct {
	Monitor   string `yaml:"monitor"`
	Format    string `yaml:"format"`
	Quality   int    `yaml:"quality"`
	MaxWidth  int    `yaml:"max_width"`
	MaxHeight int    `yaml:"max_height"`
	MaxBytes  int    `yaml:"max_bytes"`
}

// VisionConfig describes a protocol adapter rather than a particular vendor.
// This is what allows OpenAI-compatible third-party services to be configured
// without adding a new package for every vendor.
type VisionConfig struct {
	Protocol       string            `yaml:"protocol"`
	Provider       string            `yaml:"provider"` // Deprecated alias for protocol/vendor labels.
	Endpoint       string            `yaml:"endpoint"`
	Model          string            `yaml:"model"`
	APIKey         string            `yaml:"api_key"`
	APIKeyHeader   string            `yaml:"api_key_header"`
	APIKeyPrefix   string            `yaml:"api_key_prefix"`
	Headers        map[string]string `yaml:"headers"`
	Prompt         string            `yaml:"prompt"`
	Timeout        string            `yaml:"timeout"`
	MaxTokens      int               `yaml:"max_tokens"`
	MaxTokensField string            `yaml:"max_tokens_field"`
	Proxy          ProxyConfig       `yaml:"proxy"`
}

type TelegramConfig struct {
	Token          string      `yaml:"token"`
	ChatID         string      `yaml:"chat_id"`
	AllowedUserIDs []int64     `yaml:"allowed_user_ids"`
	ParseMode      string      `yaml:"parse_mode"` // Legacy setting; text uses RichMessage Markdown.
	PollTimeout    int         `yaml:"poll_timeout"`
	Timeout        string      `yaml:"timeout"`
	SendImage      bool        `yaml:"send_image"`
	Proxy          ProxyConfig `yaml:"proxy"`
}

type ProxyConfig struct {
	Type    string `yaml:"type"`
	Address string `yaml:"address"`
}

type TrayConfig struct {
	Enabled bool `yaml:"enabled"`
}

func Defaults() Config {
	return Config{
		App: AppConfig{Name: "ScreenLens"},
		Hotkey: HotkeyConfig{
			Enabled: true,
			Capture: "CTRL+SHIFT+S",
		},
		Capture: CaptureConfig{
			Monitor:   "primary",
			Format:    "jpeg",
			Quality:   85,
			MaxWidth:  2560,
			MaxHeight: 1440,
			MaxBytes:  7 * 1024 * 1024,
		},
		Vision: VisionConfig{
			Protocol:  ProtocolOpenAIChat,
			Endpoint:  "https://api.openai.com/v1/chat/completions",
			Model:     "gpt-4.1-mini",
			Prompt:    defaultPrompt,
			Timeout:   "2m",
			MaxTokens: 2048,
		},
		Telegram: TelegramConfig{
			ParseMode:   "MarkdownV2",
			PollTimeout: 10,
			Timeout:     "30s",
			SendImage:   true,
		},
	}
}

const defaultPrompt = `Analyze this screenshot and answer in concise Markdown.

Include:
- Current application or context
- Important information visible on screen
- Potential problems or recommended next actions

If something is uncertain, say so explicitly.`

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config %q: %w", path, err)
	}

	cfg := Defaults()
	data = []byte(os.ExpandEnv(string(data)))
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %q: %w", path, err)
	}
	if err := cfg.NormalizeAndValidate(); err != nil {
		return Config{}, fmt.Errorf("invalid config %q: %w", path, err)
	}
	return cfg, nil
}

func (c *Config) NormalizeAndValidate() error {
	c.App.Name = strings.TrimSpace(c.App.Name)
	c.App.LogFile = strings.TrimSpace(c.App.LogFile)
	if c.App.Name == "" {
		c.App.Name = "ScreenLens"
	}

	c.Capture.Monitor = strings.TrimSpace(strings.ToLower(c.Capture.Monitor))
	if c.Capture.Monitor == "" {
		c.Capture.Monitor = "primary"
	}
	if c.Capture.Monitor != "primary" {
		if parsed, err := strconv.Atoi(c.Capture.Monitor); err != nil || parsed < 0 {
			return fmt.Errorf("capture.monitor must be primary or a display index")
		}
	}
	c.Capture.Format = strings.ToLower(strings.TrimSpace(c.Capture.Format))
	if c.Capture.Format == "" {
		c.Capture.Format = "jpeg"
	}
	if c.Capture.Format == "jpg" {
		c.Capture.Format = "jpeg"
	}
	if c.Capture.Format != "jpeg" && c.Capture.Format != "png" {
		return fmt.Errorf("capture.format must be jpeg or png")
	}
	if c.Capture.Quality == 0 {
		c.Capture.Quality = 85
	}
	if c.Capture.Quality < 1 || c.Capture.Quality > 100 {
		return fmt.Errorf("capture.quality must be between 1 and 100")
	}
	if c.Capture.MaxWidth == 0 {
		c.Capture.MaxWidth = 2560
	}
	if c.Capture.MaxHeight == 0 {
		c.Capture.MaxHeight = 1440
	}
	if c.Capture.MaxBytes == 0 {
		c.Capture.MaxBytes = 7 * 1024 * 1024
	}
	if c.Capture.MaxWidth < 1 || c.Capture.MaxHeight < 1 {
		return errors.New("capture.max_width and capture.max_height must be positive")
	}
	if c.Capture.MaxBytes < 1 {
		return errors.New("capture.max_bytes must be positive")
	}

	c.Vision.Protocol = NormalizeProtocol(c.Vision.Protocol, c.Vision.Provider)
	if c.Vision.Protocol == "" {
		c.Vision.Protocol = ProtocolOpenAIChat
	}
	if !IsSupportedProtocol(c.Vision.Protocol) {
		return fmt.Errorf("unsupported vision.protocol %q", c.Vision.Protocol)
	}
	if c.Vision.Model = strings.TrimSpace(c.Vision.Model); c.Vision.Model == "" {
		return errors.New("vision.model is required")
	}
	if c.Vision.Endpoint == "" {
		c.Vision.Endpoint = DefaultEndpoint(c.Vision.Protocol)
	}
	if c.Vision.Prompt == "" {
		c.Vision.Prompt = defaultPrompt
	}
	if c.Vision.MaxTokens == 0 {
		c.Vision.MaxTokens = 2048
	}
	if c.Vision.MaxTokens < 1 {
		return errors.New("vision.max_tokens must be positive")
	}
	if c.Vision.APIKeyHeader == "" {
		c.Vision.APIKeyHeader = DefaultAPIKeyHeader(c.Vision.Protocol)
	}
	if c.Vision.APIKeyPrefix == "" && c.Vision.Protocol != ProtocolAnthropicMessages {
		c.Vision.APIKeyPrefix = "Bearer"
	}
	if c.Vision.MaxTokensField == "" {
		c.Vision.MaxTokensField = DefaultMaxTokensField(c.Vision.Protocol)
	}
	if c.Vision.Timeout != "" {
		if timeout, err := time.ParseDuration(c.Vision.Timeout); err != nil || timeout <= 0 {
			return fmt.Errorf("vision.timeout must be a positive duration")
		}
	}

	c.Telegram.Token = strings.TrimSpace(c.Telegram.Token)
	c.Telegram.ChatID = strings.TrimSpace(c.Telegram.ChatID)
	if c.Telegram.Token == "" {
		return errors.New("telegram.token is required")
	}
	if c.Telegram.ChatID == "" {
		return errors.New("telegram.chat_id is required")
	}
	if c.Telegram.ParseMode == "" {
		c.Telegram.ParseMode = "MarkdownV2"
	}
	switch strings.ToLower(c.Telegram.ParseMode) {
	case "markdownv2":
		c.Telegram.ParseMode = "MarkdownV2"
	case "markdown":
		c.Telegram.ParseMode = "Markdown"
	case "html":
		c.Telegram.ParseMode = "HTML"
	case "", "plain", "text":
		c.Telegram.ParseMode = ""
	default:
		return fmt.Errorf("telegram.parse_mode must be MarkdownV2, Markdown, HTML, or plain")
	}
	if c.Telegram.PollTimeout <= 0 {
		c.Telegram.PollTimeout = 10
	}
	if c.Telegram.Timeout != "" {
		if timeout, err := time.ParseDuration(c.Telegram.Timeout); err != nil || timeout <= 0 {
			return fmt.Errorf("telegram.timeout must be a positive duration")
		}
	}
	for _, userID := range c.Telegram.AllowedUserIDs {
		if userID <= 0 {
			return fmt.Errorf("telegram.allowed_user_ids must contain positive user IDs")
		}
	}
	return nil
}

func NormalizeProtocol(protocol, provider string) string {
	value := strings.ToLower(strings.TrimSpace(protocol))
	if value == "" {
		value = strings.ToLower(strings.TrimSpace(provider))
	}
	switch value {
	case "openai", "openai-chat", "chat", "chat-completions", "openai-chat-completion", ProtocolOpenAIChat:
		return ProtocolOpenAIChat
	case "responses", "openai-response", ProtocolOpenAIResponses:
		return ProtocolOpenAIResponses
	case "anthropic", "claude", "messages", "anthropic-message", ProtocolAnthropicMessages:
		return ProtocolAnthropicMessages
	default:
		return value
	}
}

func IsSupportedProtocol(protocol string) bool {
	switch protocol {
	case ProtocolOpenAIChat, ProtocolOpenAIResponses, ProtocolAnthropicMessages:
		return true
	default:
		return false
	}
}

func DefaultEndpoint(protocol string) string {
	switch protocol {
	case ProtocolOpenAIResponses:
		return "https://api.openai.com/v1/responses"
	case ProtocolAnthropicMessages:
		return "https://api.anthropic.com/v1/messages"
	default:
		return "https://api.openai.com/v1/chat/completions"
	}
}

func DefaultAPIKeyHeader(protocol string) string {
	if protocol == ProtocolAnthropicMessages {
		return "x-api-key"
	}
	return "Authorization"
}

func DefaultMaxTokensField(protocol string) string {
	switch protocol {
	case ProtocolOpenAIResponses:
		return "max_output_tokens"
	case ProtocolAnthropicMessages:
		return "max_tokens"
	default:
		return "max_completion_tokens"
	}
}

func (c VisionConfig) RequestTimeout() time.Duration {
	return parseTimeout(c.Timeout, 2*time.Minute)
}

func (c TelegramConfig) RequestTimeout() time.Duration {
	return parseTimeout(c.Timeout, 30*time.Second)
}

func parseTimeout(value string, fallback time.Duration) time.Duration {
	if value == "" {
		return fallback
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

func ResolvePath(base, value string) string {
	if value == "" || filepath.IsAbs(value) {
		return value
	}
	return filepath.Join(base, value)
}
