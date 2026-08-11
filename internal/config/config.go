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

	MonitorPrimary = "primary"
	FormatJPG      = "jpg"
	FormatJPEG     = "jpeg"
	FormatPNG      = "png"
	MIMETypeJPEG   = "image/jpeg"
	MIMETypePNG    = "image/png"

	TelegramParseModeMarkdown = "Markdown"
	TelegramParseModeHTML     = "HTML"

	DefaultCaptureQuality   = 85
	DefaultCaptureMaxWidth  = 2560
	DefaultCaptureMaxHeight = 1440
	DefaultCaptureMaxBytes  = 7 * 1024 * 1024

	DefaultVisionTimeout        = "2m"
	DefaultVisionModel          = "gpt-4.1-mini"
	DefaultVisionRetryCount     = 3
	DefaultVisionMaxTokens      = 2048
	DefaultVisionRequestTimeout = 2 * time.Minute

	DefaultTelegramParseMode      = "MarkdownV2"
	DefaultTelegramPollTimeout    = 10
	DefaultTelegramTimeout        = "30s"
	DefaultTelegramRequestTimeout = 30 * time.Second

	DefaultAPIKeyPrefix = "Bearer"
	DefaultAppName      = "ScreenLens"
	DefaultHotkey       = "CTRL+SHIFT+S"
)

const (
	apiKeyHeaderAuthorization = "Authorization"
	apiKeyHeaderAnthropic     = "x-api-key"

	maxTokensFieldCompletion = "max_completion_tokens"
	maxTokensFieldOutput     = "max_output_tokens"
	maxTokensFieldInput      = "max_tokens"

	endpointOpenAIChat      = "https://api.openai.com/v1/chat/completions"
	endpointOpenAIResponses = "https://api.openai.com/v1/responses"
	endpointAnthropic       = "https://api.anthropic.com/v1/messages"
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
	RetryCount     int               `yaml:"retry_count"`
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
		App: AppConfig{Name: DefaultAppName},
		Hotkey: HotkeyConfig{
			Enabled: true,
			Capture: DefaultHotkey,
		},
		Capture: CaptureConfig{
			Monitor:   MonitorPrimary,
			Format:    FormatJPEG,
			Quality:   DefaultCaptureQuality,
			MaxWidth:  DefaultCaptureMaxWidth,
			MaxHeight: DefaultCaptureMaxHeight,
			MaxBytes:  DefaultCaptureMaxBytes,
		},
		Vision: VisionConfig{
			Protocol:   ProtocolOpenAIChat,
			Endpoint:   endpointOpenAIChat,
			Model:      DefaultVisionModel,
			Prompt:     defaultPrompt,
			Timeout:    DefaultVisionTimeout,
			RetryCount: DefaultVisionRetryCount,
			MaxTokens:  DefaultVisionMaxTokens,
		},
		Telegram: TelegramConfig{
			ParseMode:   DefaultTelegramParseMode,
			PollTimeout: DefaultTelegramPollTimeout,
			Timeout:     DefaultTelegramTimeout,
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
		c.App.Name = DefaultAppName
	}

	c.Capture.Monitor = strings.TrimSpace(strings.ToLower(c.Capture.Monitor))
	if c.Capture.Monitor == "" {
		c.Capture.Monitor = MonitorPrimary
	}
	if c.Capture.Monitor != MonitorPrimary {
		if parsed, err := strconv.Atoi(c.Capture.Monitor); err != nil || parsed < 0 {
			return fmt.Errorf("capture.monitor must be %s or a display index", MonitorPrimary)
		}
	}
	c.Capture.Format = strings.ToLower(strings.TrimSpace(c.Capture.Format))
	if c.Capture.Format == "" {
		c.Capture.Format = FormatJPEG
	}
	if c.Capture.Format == FormatJPG {
		c.Capture.Format = FormatJPEG
	}
	if c.Capture.Format != FormatJPEG && c.Capture.Format != FormatPNG {
		return fmt.Errorf("capture.format must be %s or %s", FormatJPEG, FormatPNG)
	}
	if c.Capture.Quality == 0 {
		c.Capture.Quality = DefaultCaptureQuality
	}
	if c.Capture.Quality < 1 || c.Capture.Quality > 100 {
		return fmt.Errorf("capture.quality must be between 1 and 100")
	}
	if c.Capture.MaxWidth == 0 {
		c.Capture.MaxWidth = DefaultCaptureMaxWidth
	}
	if c.Capture.MaxHeight == 0 {
		c.Capture.MaxHeight = DefaultCaptureMaxHeight
	}
	if c.Capture.MaxBytes == 0 {
		c.Capture.MaxBytes = DefaultCaptureMaxBytes
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
		c.Vision.MaxTokens = DefaultVisionMaxTokens
	}
	if c.Vision.MaxTokens < 1 {
		return errors.New("vision.max_tokens must be positive")
	}
	if c.Vision.RetryCount < 0 {
		return errors.New("vision.retry_count must not be negative")
	}
	if c.Vision.APIKeyHeader == "" {
		c.Vision.APIKeyHeader = DefaultAPIKeyHeader(c.Vision.Protocol)
	}
	if c.Vision.APIKeyPrefix == "" && c.Vision.Protocol != ProtocolAnthropicMessages {
		c.Vision.APIKeyPrefix = DefaultAPIKeyPrefix
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
		c.Telegram.ParseMode = DefaultTelegramParseMode
	}
	switch strings.ToLower(c.Telegram.ParseMode) {
	case "markdownv2":
		c.Telegram.ParseMode = DefaultTelegramParseMode
	case "markdown":
		c.Telegram.ParseMode = TelegramParseModeMarkdown
	case "html":
		c.Telegram.ParseMode = TelegramParseModeHTML
	case "", "plain", "text":
		c.Telegram.ParseMode = ""
	default:
		return fmt.Errorf("telegram.parse_mode must be MarkdownV2, Markdown, HTML, or plain")
	}
	if c.Telegram.PollTimeout <= 0 {
		c.Telegram.PollTimeout = DefaultTelegramPollTimeout
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
		return endpointOpenAIResponses
	case ProtocolAnthropicMessages:
		return endpointAnthropic
	default:
		return endpointOpenAIChat
	}
}

func DefaultAPIKeyHeader(protocol string) string {
	if protocol == ProtocolAnthropicMessages {
		return apiKeyHeaderAnthropic
	}
	return apiKeyHeaderAuthorization
}

func DefaultMaxTokensField(protocol string) string {
	switch protocol {
	case ProtocolOpenAIResponses:
		return maxTokensFieldOutput
	case ProtocolAnthropicMessages:
		return maxTokensFieldInput
	default:
		return maxTokensFieldCompletion
	}
}

func (c VisionConfig) RequestTimeout() time.Duration {
	return parseTimeout(c.Timeout, DefaultVisionRequestTimeout)
}

func (c TelegramConfig) RequestTimeout() time.Duration {
	return parseTimeout(c.Timeout, DefaultTelegramRequestTimeout)
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
