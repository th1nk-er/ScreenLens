package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
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
	DefaultAgentTimeout         = "5m"
	DefaultAgentMaxOutputBytes  = 2 * 1024 * 1024
	DefaultAgentMaxOutputTokens = 8192
	DefaultAgentRetryCount      = 1

	DefaultTelegramParseMode      = "MarkdownV2"
	DefaultTelegramPollTimeout    = 10
	DefaultTelegramTimeout        = "30s"
	DefaultTelegramRequestTimeout = 30 * time.Second

	DefaultAPIKeyPrefix        = "Bearer"
	DefaultAppName             = "ScreenLens"
	DefaultHotkey              = "CTRL+SHIFT+S"
	DefaultRegionStartHotkey   = "ALT+SHIFT+S"
	DefaultRegionEndHotkey     = "ALT+SHIFT+E"
	DefaultRegionCaptureHotkey = "ALT+SHIFT+C"
)

const (
	AnalysisModeVision     = "vision"
	AnalysisModeLocalAgent = "local-agent"

	AnalysisProfileTypeVisionAPI  = "vision-api"
	AnalysisProfileTypeLocalAgent = "local-agent"

	LocalAgentProviderAuto = "auto"
	LocalAgentTransportCLI = "cli"
	LocalAgentWorkDirTemp  = "temp"
	LocalAgentImageAuto    = "auto"
	LocalAgentImagePath    = "path"
	LocalAgentImageNative  = "native"

	LocalAgentSessionEphemeral  = "ephemeral"
	LocalAgentSessionPersistent = "persistent"

	WorkflowInputBoth       = "both"
	WorkflowInputScreenshot = "screenshot"
	WorkflowInputPrevious   = "previous"
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

// Config is the complete runtime configuration for ScreenLens. All analysis
// backends are configured below Analysis; there is deliberately no separate
// top-level vision or local-agent configuration tree.
type Config struct {
	App      AppConfig      `yaml:"app"`
	Hotkey   HotkeyConfig   `yaml:"hotkey"`
	Capture  CaptureConfig  `yaml:"capture"`
	Analysis AnalysisConfig `yaml:"analysis"`
	Telegram TelegramConfig `yaml:"telegram"`
	Tray     TrayConfig     `yaml:"tray"`
}

type AppConfig struct {
	Name    string `yaml:"name"`
	LogFile string `yaml:"log_file"`
}

type HotkeyConfig struct {
	Enabled       bool   `yaml:"enabled"`
	Capture       string `yaml:"capture"`
	RegionStart   string `yaml:"region_start"`
	RegionEnd     string `yaml:"region_end"`
	RegionCapture string `yaml:"region_capture"`
}

type CaptureConfig struct {
	Monitor   string `yaml:"monitor"`
	Format    string `yaml:"format"`
	Quality   int    `yaml:"quality"`
	MaxWidth  int    `yaml:"max_width"`
	MaxHeight int    `yaml:"max_height"`
	MaxBytes  int    `yaml:"max_bytes"`
}

// AnalysisConfig owns the common analysis contract: the active mode/profile,
// optional legacy prompt, execution limits, workflow registry, and backend
// profiles.
type AnalysisConfig struct {
	Mode           string                      `yaml:"mode"`
	Profile        string                      `yaml:"profile"`
	AutoProfiles   []string                    `yaml:"auto_profiles"`
	Prompt         string                      `yaml:"prompt"`
	Workflow       string                      `yaml:"workflow"`
	Workflows      map[string]AnalysisWorkflow `yaml:"workflows"`
	Timeout        string                      `yaml:"timeout"`
	MaxOutputBytes int                         `yaml:"max_output_bytes"`
	Session        string                      `yaml:"session"`
	Profiles       map[string]AnalysisProfile  `yaml:"profiles"`
}

// AnalysisWorkflow is an ordered chain of analysis steps. Each step can use a
// named profile from AnalysisConfig.Profiles or define an isolated inline
// profile through ProfileConfig.
type AnalysisWorkflow struct {
	Steps   []AnalysisStep `yaml:"steps"`
	Timeout string         `yaml:"timeout"`
}

// AnalysisStep is deliberately small: orchestration concerns live here while
// provider-specific settings stay in AnalysisProfile. A step must set exactly
// one of Profile and ProfileConfig.
type AnalysisStep struct {
	Name          string           `yaml:"name"`
	Profile       string           `yaml:"profile"`
	ProfileConfig *AnalysisProfile `yaml:"profile_config"`
	Prompt        string           `yaml:"prompt"`
	Input         string           `yaml:"input"`
}

// AnalysisProfile keeps provider-specific fields isolated while sharing the
// common analysis settings above. Exactly one backend section is used based on
// Type.
type AnalysisProfile struct {
	Type       string            `yaml:"type"`
	Vision     VisionConfig      `yaml:"vision"`
	LocalAgent LocalAgentProfile `yaml:"local_agent"`
}

// VisionConfig describes a protocol adapter rather than a particular vendor.
type VisionConfig struct {
	Protocol       string            `yaml:"protocol"`
	Provider       string            `yaml:"provider"`
	Endpoint       string            `yaml:"endpoint"`
	Model          string            `yaml:"model"`
	APIKey         string            `yaml:"api_key"`
	APIKeyHeader   string            `yaml:"api_key_header"`
	APIKeyPrefix   string            `yaml:"api_key_prefix"`
	Headers        map[string]string `yaml:"headers"`
	Timeout        string            `yaml:"timeout"`
	RetryCount     int               `yaml:"retry_count"`
	MaxTokens      int               `yaml:"max_tokens"`
	MaxTokensField string            `yaml:"max_tokens_field"`
	Proxy          ProxyConfig       `yaml:"proxy"`
}

// LocalAgentProfile contains provider-neutral CLI settings. Empty Command and
// Args select the built-in defaults for the named Provider.
type LocalAgentProfile struct {
	Provider        string            `yaml:"provider"`
	Transport       string            `yaml:"transport"`
	ImageTransport  string            `yaml:"image_transport"`
	Command         string            `yaml:"command"`
	Args            []string          `yaml:"args"`
	WorkDir         string            `yaml:"work_dir"`
	Timeout         string            `yaml:"timeout"`
	MaxOutputBytes  int               `yaml:"max_output_bytes"`
	MaxOutputTokens int               `yaml:"max_output_tokens"`
	RetryCount      *int              `yaml:"retry_count"`
	Env             map[string]string `yaml:"env"`
	Session         string            `yaml:"session"`
}

type TelegramConfig struct {
	Token          string      `yaml:"token"`
	ChatID         string      `yaml:"chat_id"`
	AllowedUserIDs []int64     `yaml:"allowed_user_ids"`
	ParseMode      string      `yaml:"parse_mode"`
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
	vision := VisionConfig{
		Protocol:   ProtocolOpenAIChat,
		Endpoint:   endpointOpenAIChat,
		Model:      DefaultVisionModel,
		Timeout:    DefaultVisionTimeout,
		RetryCount: DefaultVisionRetryCount,
		MaxTokens:  DefaultVisionMaxTokens,
	}
	return Config{
		App: AppConfig{Name: DefaultAppName},
		Hotkey: HotkeyConfig{
			Enabled:       true,
			Capture:       DefaultHotkey,
			RegionStart:   DefaultRegionStartHotkey,
			RegionEnd:     DefaultRegionEndHotkey,
			RegionCapture: DefaultRegionCaptureHotkey,
		},
		Capture: CaptureConfig{
			Monitor:   MonitorPrimary,
			Format:    FormatJPEG,
			Quality:   DefaultCaptureQuality,
			MaxWidth:  DefaultCaptureMaxWidth,
			MaxHeight: DefaultCaptureMaxHeight,
			MaxBytes:  DefaultCaptureMaxBytes,
		},
		Analysis: AnalysisConfig{
			Mode:           AnalysisModeLocalAgent,
			Profile:        LocalAgentProviderAuto,
			AutoProfiles:   DefaultAutoProfiles(),
			Timeout:        DefaultAgentTimeout,
			MaxOutputBytes: DefaultAgentMaxOutputBytes,
			Session:        LocalAgentSessionEphemeral,
			Profiles: map[string]AnalysisProfile{
				"vision": {
					Type:   AnalysisProfileTypeVisionAPI,
					Vision: vision,
				},
				"codex": {
					Type:       AnalysisProfileTypeLocalAgent,
					LocalAgent: LocalAgentProfile{Provider: "codex", Transport: LocalAgentTransportCLI, RetryCount: intPointer(DefaultAgentRetryCount)},
				},
				"claude": {
					Type:       AnalysisProfileTypeLocalAgent,
					LocalAgent: LocalAgentProfile{Provider: "claude", Transport: LocalAgentTransportCLI, RetryCount: intPointer(DefaultAgentRetryCount)},
				},
				"opencode": {
					Type:       AnalysisProfileTypeLocalAgent,
					LocalAgent: LocalAgentProfile{Provider: "opencode", Transport: LocalAgentTransportCLI, RetryCount: intPointer(DefaultAgentRetryCount)},
				},
			},
		},
		Telegram: TelegramConfig{
			ParseMode:   DefaultTelegramParseMode,
			PollTimeout: DefaultTelegramPollTimeout,
			Timeout:     DefaultTelegramTimeout,
			SendImage:   true,
		},
	}
}

// DefaultAutoProfiles is the backwards-compatible provider discovery order.
// Users can override it with analysis.auto_profiles without changing code.
func DefaultAutoProfiles() []string {
	return []string{"codex", "claude", "opencode"}
}

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
	var extraDocument any
	if err := decoder.Decode(&extraDocument); err != io.EOF {
		if err == nil {
			return Config{}, fmt.Errorf("parse config %q: multiple YAML documents are not supported", path)
		}
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
	c.Hotkey.Capture = strings.TrimSpace(c.Hotkey.Capture)
	if c.Hotkey.Capture == "" {
		c.Hotkey.Capture = DefaultHotkey
	}
	c.Hotkey.RegionStart = strings.TrimSpace(c.Hotkey.RegionStart)
	if c.Hotkey.RegionStart == "" {
		c.Hotkey.RegionStart = DefaultRegionStartHotkey
	}
	c.Hotkey.RegionEnd = strings.TrimSpace(c.Hotkey.RegionEnd)
	if c.Hotkey.RegionEnd == "" {
		c.Hotkey.RegionEnd = DefaultRegionEndHotkey
	}
	c.Hotkey.RegionCapture = strings.TrimSpace(c.Hotkey.RegionCapture)
	if c.Hotkey.RegionCapture == "" {
		c.Hotkey.RegionCapture = DefaultRegionCaptureHotkey
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
		return errors.New("capture.quality must be between 1 and 100")
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

	if err := c.normalizeAnalysis(); err != nil {
		return err
	}
	if err := c.normalizeAnalysisProfiles(); err != nil {
		return err
	}
	if err := c.normalizeAnalysisWorkflows(); err != nil {
		return err
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
		return errors.New("telegram.parse_mode must be MarkdownV2, Markdown, HTML, or plain")
	}
	if c.Telegram.PollTimeout <= 0 {
		c.Telegram.PollTimeout = DefaultTelegramPollTimeout
	}
	if c.Telegram.Timeout != "" {
		if timeout, err := time.ParseDuration(c.Telegram.Timeout); err != nil || timeout <= 0 {
			return errors.New("telegram.timeout must be a positive duration")
		}
	}
	for _, userID := range c.Telegram.AllowedUserIDs {
		if userID <= 0 {
			return errors.New("telegram.allowed_user_ids must contain positive user IDs")
		}
	}
	return nil
}

func (c *Config) normalizeAnalysis() error {
	c.Analysis.Mode = strings.ToLower(strings.TrimSpace(c.Analysis.Mode))
	switch c.Analysis.Mode {
	case "", "agent", "local", AnalysisModeLocalAgent:
		c.Analysis.Mode = AnalysisModeLocalAgent
	case "api", "direct", AnalysisModeVision:
		c.Analysis.Mode = AnalysisModeVision
	default:
		return fmt.Errorf("analysis.mode must be %s or %s", AnalysisModeVision, AnalysisModeLocalAgent)
	}
	c.Analysis.Profile = strings.ToLower(strings.TrimSpace(c.Analysis.Profile))
	if c.Analysis.Mode == AnalysisModeLocalAgent && c.Analysis.Profile == "" {
		c.Analysis.Profile = LocalAgentProviderAuto
	}
	if c.Analysis.Mode == AnalysisModeVision && c.Analysis.Profile == "" {
		c.Analysis.Profile = "vision"
	}
	if len(c.Analysis.AutoProfiles) == 0 {
		c.Analysis.AutoProfiles = DefaultAutoProfiles()
	}
	seenAutoProfiles := make(map[string]struct{}, len(c.Analysis.AutoProfiles))
	for index, profile := range c.Analysis.AutoProfiles {
		profile = strings.ToLower(strings.TrimSpace(profile))
		if profile == "" {
			return fmt.Errorf("analysis.auto_profiles[%d] must not be empty", index)
		}
		if _, exists := seenAutoProfiles[profile]; exists {
			return fmt.Errorf("analysis.auto_profiles contains duplicate profile %q", profile)
		}
		seenAutoProfiles[profile] = struct{}{}
		c.Analysis.AutoProfiles[index] = profile
	}
	c.Analysis.Prompt = strings.TrimSpace(c.Analysis.Prompt)
	if c.Analysis.Prompt == "" && len(c.Analysis.Workflows) == 0 {
		return errors.New("analysis.prompt is required; configure it in the YAML file")
	}
	if c.Analysis.Timeout == "" {
		c.Analysis.Timeout = DefaultAgentTimeout
	}
	if timeout, err := time.ParseDuration(c.Analysis.Timeout); err != nil || timeout <= 0 {
		return errors.New("analysis.timeout must be a positive duration")
	}
	if c.Analysis.MaxOutputBytes == 0 {
		c.Analysis.MaxOutputBytes = DefaultAgentMaxOutputBytes
	}
	if c.Analysis.MaxOutputBytes < 1 {
		return errors.New("analysis.max_output_bytes must be positive")
	}
	c.Analysis.Session = strings.ToLower(strings.TrimSpace(c.Analysis.Session))
	if c.Analysis.Session == "" {
		c.Analysis.Session = LocalAgentSessionEphemeral
	}
	if c.Analysis.Session != LocalAgentSessionEphemeral && c.Analysis.Session != LocalAgentSessionPersistent {
		return errors.New("analysis.session must be ephemeral or persistent")
	}
	return nil
}

func (c *Config) normalizeAnalysisProfiles() error {
	if len(c.Analysis.Profiles) == 0 {
		if len(c.Analysis.Workflows) == 0 {
			return errors.New("analysis.profiles must contain at least one profile")
		}
		// A workflow may define every backend through step.profile_config.
		// normalizeAnalysisWorkflows validates named references after this
		// function returns, so an empty registry is valid in that case.
		return nil
	}
	normalized := make(map[string]AnalysisProfile, len(c.Analysis.Profiles))
	for rawName, profile := range c.Analysis.Profiles {
		name := strings.ToLower(strings.TrimSpace(rawName))
		if name == "" {
			return errors.New("analysis.profiles must not contain an empty profile name")
		}
		if _, exists := normalized[name]; exists {
			return fmt.Errorf("analysis.profiles contains duplicate profile name %q", name)
		}
		profile.Type = strings.ToLower(strings.TrimSpace(profile.Type))
		switch profile.Type {
		case AnalysisProfileTypeVisionAPI:
			if err := normalizeVisionProfile(&profile.Vision); err != nil {
				return fmt.Errorf("analysis.profiles.%s.vision: %w", name, err)
			}
		case AnalysisProfileTypeLocalAgent:
			if err := normalizeLocalAgentProfile(&profile.LocalAgent, name, c.Analysis); err != nil {
				return err
			}
		default:
			return fmt.Errorf("analysis.profiles.%s.type must be %s or %s", name, AnalysisProfileTypeVisionAPI, AnalysisProfileTypeLocalAgent)
		}
		normalized[name] = profile
	}
	c.Analysis.Profiles = normalized
	if len(c.Analysis.Workflows) == 0 && c.Analysis.Mode == AnalysisModeVision {
		profile, ok := c.Analysis.Profiles[c.Analysis.Profile]
		if !ok || profile.Type != AnalysisProfileTypeVisionAPI {
			return fmt.Errorf("analysis.profile %q must reference a vision-api profile", c.Analysis.Profile)
		}
	}
	if len(c.Analysis.Workflows) == 0 && c.Analysis.Mode == AnalysisModeLocalAgent && c.Analysis.Profile != LocalAgentProviderAuto {
		profile, ok := c.Analysis.Profiles[c.Analysis.Profile]
		if !ok || profile.Type != AnalysisProfileTypeLocalAgent {
			return fmt.Errorf("analysis.profile %q must reference a local-agent profile", c.Analysis.Profile)
		}
	}
	return nil
}

func (c *Config) normalizeAnalysisWorkflows() error {
	if len(c.Analysis.Workflows) == 0 {
		c.Analysis.Workflow = strings.ToLower(strings.TrimSpace(c.Analysis.Workflow))
		return nil
	}

	normalized := make(map[string]AnalysisWorkflow, len(c.Analysis.Workflows))
	for rawName, workflow := range c.Analysis.Workflows {
		name := strings.ToLower(strings.TrimSpace(rawName))
		if name == "" {
			return errors.New("analysis.workflows must not contain an empty workflow name")
		}
		if _, exists := normalized[name]; exists {
			return fmt.Errorf("analysis.workflows contains duplicate workflow name %q", name)
		}
		if len(workflow.Steps) == 0 {
			return fmt.Errorf("analysis.workflows.%s.steps must contain at least one step", name)
		}
		stepNames := make(map[string]struct{}, len(workflow.Steps))
		if workflow.Timeout != "" {
			timeout, err := time.ParseDuration(workflow.Timeout)
			if err != nil || timeout <= 0 {
				return fmt.Errorf("analysis.workflows.%s.timeout must be a positive duration", name)
			}
		}
		for index := range workflow.Steps {
			if err := normalizeAnalysisStep(&workflow.Steps[index], name, index, c.Analysis); err != nil {
				return err
			}
			if _, exists := stepNames[workflow.Steps[index].Name]; exists {
				return fmt.Errorf("analysis.workflows.%s contains duplicate step name %q", name, workflow.Steps[index].Name)
			}
			stepNames[workflow.Steps[index].Name] = struct{}{}
		}
		normalized[name] = workflow
	}
	c.Analysis.Workflows = normalized

	c.Analysis.Workflow = strings.ToLower(strings.TrimSpace(c.Analysis.Workflow))
	if c.Analysis.Workflow == "" {
		if _, ok := normalized["default"]; ok {
			c.Analysis.Workflow = "default"
		} else if len(normalized) == 1 {
			for name := range normalized {
				c.Analysis.Workflow = name
			}
		} else {
			return errors.New("analysis.workflow is required when multiple workflows are configured")
		}
	}
	if _, ok := normalized[c.Analysis.Workflow]; !ok {
		return fmt.Errorf("analysis.workflow %q is not configured", c.Analysis.Workflow)
	}
	return nil
}

func normalizeAnalysisStep(step *AnalysisStep, workflowName string, index int, analysis AnalysisConfig) error {
	path := fmt.Sprintf("analysis.workflows.%s.steps[%d]", workflowName, index)
	step.Name = strings.TrimSpace(step.Name)
	if step.Name == "" {
		step.Name = fmt.Sprintf("step-%d", index+1)
	}
	step.Prompt = strings.TrimSpace(step.Prompt)
	if step.Prompt == "" {
		return fmt.Errorf("%s.prompt is required", path)
	}
	step.Input = strings.ToLower(strings.TrimSpace(step.Input))
	if step.Input == "" {
		step.Input = WorkflowInputBoth
	}
	switch step.Input {
	case WorkflowInputBoth, WorkflowInputScreenshot:
	case WorkflowInputPrevious:
		if index == 0 {
			return fmt.Errorf("%s.input cannot be previous on the first step", path)
		}
	default:
		return fmt.Errorf("%s.input must be %s, %s, or %s", path, WorkflowInputBoth, WorkflowInputScreenshot, WorkflowInputPrevious)
	}
	step.Profile = strings.ToLower(strings.TrimSpace(step.Profile))
	if step.Profile != "" && step.ProfileConfig != nil {
		return fmt.Errorf("%s must set only one of profile or profile_config", path)
	}
	if step.Profile == "" && step.ProfileConfig == nil {
		return fmt.Errorf("%s must set profile or profile_config", path)
	}
	if step.ProfileConfig == nil {
		if step.Profile == LocalAgentProviderAuto {
			return nil
		}
		profile, ok := analysis.Profiles[step.Profile]
		if !ok {
			return fmt.Errorf("%s.profile %q is not configured", path, step.Profile)
		}
		if profile.Type != AnalysisProfileTypeLocalAgent && profile.Type != AnalysisProfileTypeVisionAPI {
			return fmt.Errorf("%s.profile %q has unsupported type %q", path, step.Profile, profile.Type)
		}
		return nil
	}

	profile := step.ProfileConfig
	profile.Type = strings.ToLower(strings.TrimSpace(profile.Type))
	switch profile.Type {
	case AnalysisProfileTypeVisionAPI:
		if err := normalizeVisionProfile(&profile.Vision); err != nil {
			return fmt.Errorf("%s.profile_config.vision: %w", path, err)
		}
	case AnalysisProfileTypeLocalAgent:
		if err := normalizeLocalAgentProfile(&profile.LocalAgent, path+".profile_config", analysis); err != nil {
			return err
		}
	default:
		return fmt.Errorf("%s.profile_config.type must be %s or %s", path, AnalysisProfileTypeVisionAPI, AnalysisProfileTypeLocalAgent)
	}
	return nil
}

func normalizeVisionProfile(c *VisionConfig) error {
	c.Protocol = NormalizeProtocol(c.Protocol, c.Provider)
	if c.Protocol == "" {
		return errors.New("protocol is required")
	}
	if !IsSupportedProtocol(c.Protocol) {
		return fmt.Errorf("unsupported protocol %q", c.Protocol)
	}
	c.Model = strings.TrimSpace(c.Model)
	if c.Model == "" {
		return errors.New("model is required")
	}
	if c.Endpoint == "" {
		c.Endpoint = DefaultEndpoint(c.Protocol)
	}
	if c.MaxTokens == 0 {
		c.MaxTokens = DefaultVisionMaxTokens
	}
	if c.MaxTokens < 1 {
		return errors.New("max_tokens must be positive")
	}
	if c.RetryCount < 0 {
		return errors.New("retry_count must not be negative")
	}
	if c.APIKeyHeader == "" {
		c.APIKeyHeader = DefaultAPIKeyHeader(c.Protocol)
	}
	if c.APIKeyPrefix == "" && c.Protocol != ProtocolAnthropicMessages {
		c.APIKeyPrefix = DefaultAPIKeyPrefix
	}
	if c.MaxTokensField == "" {
		c.MaxTokensField = DefaultMaxTokensField(c.Protocol)
	}
	if c.Timeout != "" {
		if timeout, err := time.ParseDuration(c.Timeout); err != nil || timeout <= 0 {
			return errors.New("timeout must be a positive duration")
		}
	}
	return nil
}

func normalizeLocalAgentProfile(profile *LocalAgentProfile, name string, analysis AnalysisConfig) error {
	profile.Provider = strings.ToLower(strings.TrimSpace(profile.Provider))
	if profile.Provider == "" {
		return fmt.Errorf("analysis.profiles.%s.local_agent.provider is required", name)
	}
	profile.Transport = strings.ToLower(strings.TrimSpace(profile.Transport))
	if profile.Transport == "" {
		profile.Transport = LocalAgentTransportCLI
	}
	if profile.Transport != LocalAgentTransportCLI {
		return fmt.Errorf("analysis.profiles.%s.local_agent.transport must be %s", name, LocalAgentTransportCLI)
	}
	profile.ImageTransport = strings.ToLower(strings.TrimSpace(profile.ImageTransport))
	if profile.ImageTransport == "" {
		profile.ImageTransport = LocalAgentImageAuto
	}
	if profile.ImageTransport != LocalAgentImageAuto && profile.ImageTransport != LocalAgentImagePath && profile.ImageTransport != LocalAgentImageNative {
		return fmt.Errorf("analysis.profiles.%s.local_agent.image_transport must be %s, %s, or %s", name, LocalAgentImageAuto, LocalAgentImagePath, LocalAgentImageNative)
	}
	profile.Session = strings.ToLower(strings.TrimSpace(profile.Session))
	if profile.Session == "" {
		profile.Session = analysis.Session
	}
	if profile.Session != LocalAgentSessionEphemeral && profile.Session != LocalAgentSessionPersistent {
		return fmt.Errorf("analysis.profiles.%s.local_agent.session must be ephemeral or persistent", name)
	}
	if profile.MaxOutputBytes < 0 {
		return fmt.Errorf("analysis.profiles.%s.local_agent.max_output_bytes must not be negative", name)
	}
	if profile.MaxOutputTokens == 0 {
		profile.MaxOutputTokens = DefaultAgentMaxOutputTokens
	}
	if profile.MaxOutputTokens < 1 {
		return fmt.Errorf("analysis.profiles.%s.local_agent.max_output_tokens must be positive", name)
	}
	if profile.RetryCount != nil && *profile.RetryCount < 0 {
		return fmt.Errorf("analysis.profiles.%s.local_agent.retry_count must not be negative", name)
	}
	if profile.RetryCount == nil {
		profile.RetryCount = intPointer(DefaultAgentRetryCount)
	}
	return nil
}

func intPointer(value int) *int {
	return &value
}

func (c Config) ActiveVisionConfig() (VisionConfig, error) {
	profile, ok := c.Analysis.Profiles[c.Analysis.Profile]
	if !ok || profile.Type != AnalysisProfileTypeVisionAPI {
		return VisionConfig{}, fmt.Errorf("analysis profile %q is not a vision-api profile", c.Analysis.Profile)
	}
	return profile.Vision, nil
}

func (c Config) LocalAgentProfiles() map[string]LocalAgentProfile {
	profiles := make(map[string]LocalAgentProfile)
	for name, profile := range c.Analysis.Profiles {
		if profile.Type == AnalysisProfileTypeLocalAgent {
			profiles[name] = profile.LocalAgent
		}
	}
	return profiles
}

func (a AnalysisConfig) HasWorkflows() bool {
	return len(a.Workflows) > 0
}

func (a AnalysisConfig) ActiveWorkflow() (AnalysisWorkflow, error) {
	if len(a.Workflows) == 0 {
		return AnalysisWorkflow{}, errors.New("analysis workflows are not configured")
	}
	name := strings.ToLower(strings.TrimSpace(a.Workflow))
	workflow, ok := a.Workflows[name]
	if !ok {
		return AnalysisWorkflow{}, fmt.Errorf("analysis workflow %q is not configured", a.Workflow)
	}
	return workflow, nil
}

func (a AnalysisConfig) WorkflowNames() []string {
	names := make([]string, 0, len(a.Workflows))
	for name := range a.Workflows {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (a AnalysisConfig) WorkflowTimeout(workflow AnalysisWorkflow) time.Duration {
	if workflow.Timeout != "" {
		return parseTimeout(workflow.Timeout, parseTimeout(a.Timeout, 5*time.Minute))
	}
	return parseTimeout(a.Timeout, 5*time.Minute)
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
