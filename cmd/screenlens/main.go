package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/th1nk-er/ScreenLens/internal/agent"
	"github.com/th1nk-er/ScreenLens/internal/analyzer"
	"github.com/th1nk-er/ScreenLens/internal/config"
	"github.com/th1nk-er/ScreenLens/internal/hotkey"
	"github.com/th1nk-er/ScreenLens/internal/instance"
	"github.com/th1nk-er/ScreenLens/internal/logging"
	"github.com/th1nk-er/ScreenLens/internal/screenshot"
	"github.com/th1nk-er/ScreenLens/internal/telegram"
	"github.com/th1nk-er/ScreenLens/internal/tray"
	"github.com/th1nk-er/ScreenLens/internal/vision"
	"github.com/th1nk-er/ScreenLens/internal/workflow"
)

const (
	defaultConfigPath = "config.yaml"
	instanceName      = "Local\\ScreenLens"
	buildModeConsole  = "console"
	buildModeGUI      = "gui"
	statusIdle        = "Idle"
	statusCapturing   = "Capturing"
)

func main() {
	configPath := flag.String("config", defaultConfigPath, "path to the YAML configuration file")
	flag.Parse()

	logHandle, err := logging.Open("", buildMode == buildModeConsole)
	if err != nil {
		fmt.Fprintf(os.Stderr, "initialize logging: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = logHandle.Close() }()
	logger := logHandle.Logger
	slog.SetDefault(logger)

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("load configuration", "error", err)
		os.Exit(1)
	}
	if cfg.App.LogFile != "" {
		configuredPath, pathErr := logging.ResolvePath(cfg.App.LogFile)
		if pathErr != nil {
			logger.Error("resolve configured log file", "error", pathErr)
			os.Exit(1)
		}
		if configuredPath != logHandle.Path {
			previousLogHandle := logHandle
			logHandle, err = logging.Open(cfg.App.LogFile, buildMode == buildModeConsole)
			if err != nil {
				logger.Error("initialize configured logging", "error", err)
				os.Exit(1)
			}
			_ = previousLogHandle.Close()
			logger = logHandle.Logger
			slog.SetDefault(logger)
		}
	}
	logger.Info("ScreenLens starting", "mode", buildMode, "log_file", logHandle.Path)

	instanceLock, err := instance.Acquire(instanceName)
	if errors.Is(err, instance.ErrAlreadyRunning) {
		logger.Warn("another ScreenLens instance is already running")
		return
	}
	if err != nil {
		logger.Error("initialize single-instance protection", "error", err)
		os.Exit(1)
	}
	defer instanceLock.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	capture, backend, backendName, err := buildComponents(cfg)
	if err != nil {
		logger.Error("initialize components", "error", err)
		os.Exit(1)
	}
	if cfg.Hotkey.Enabled {
		if _, err := hotkey.New(cfg.Hotkey.Capture); err != nil {
			logger.Error("configure hotkey", "error", err)
			os.Exit(1)
		}
	}

	var engine *workflow.Engine
	var currentConfig = cfg
	var currentConfigMu sync.RWMutex
	var reloadMu sync.Mutex
	resolveProfile := func(profile string) (analyzer.Analyzer, string, error) {
		currentConfigMu.RLock()
		snapshot := currentConfig
		currentConfigMu.RUnlock()
		profileName := strings.ToLower(strings.TrimSpace(profile))
		if profileName == "vision" || (snapshot.Analysis.Profiles[profileName].Type == config.AnalysisProfileTypeVisionAPI) {
			snapshot.Analysis.Profile = profileName
			resolved, err := buildVisionAnalyzer(snapshot, capture.MIMEType())
			return resolved, profileName, err
		}
		snapshot.Analysis.Mode = config.AnalysisModeLocalAgent
		snapshot.Analysis.Profile = profileName
		resolved, name, err := buildLocalAnalyzer(snapshot)
		return resolved, name, err
	}
	hotkeyManager := hotkey.NewManager(ctx, func(err error) {
		logger.Error("hotkey listener stopped", "error", err)
		cancel()
	})
	reload := func(context.Context) error {
		reloadMu.Lock()
		defer reloadMu.Unlock()
		logger.Info("configuration reload started", "path", *configPath)
		next, err := config.Load(*configPath)
		if err != nil {
			logger.Error("configuration reload failed", "stage", "load", "error", err)
			return err
		}
		currentConfigMu.RLock()
		previous := currentConfig
		currentConfigMu.RUnlock()
		if telegramRestartRequired(previous.Telegram, next.Telegram) {
			err := fmt.Errorf("Telegram connection or authorization settings changed; restart is required")
			logger.Error("configuration reload failed", "stage", "validate", "error", err)
			return err
		}
		nextCapture, nextBackend, nextBackendName, err := buildComponents(next)
		if err != nil {
			logger.Error("configuration reload failed", "stage", "initialize", "error", err)
			return err
		}
		hotkeyChanged := next.Hotkey.Enabled != previous.Hotkey.Enabled || next.Hotkey.Capture != previous.Hotkey.Capture
		if hotkeyChanged {
			if err := hotkeyManager.Start(next.Hotkey.Enabled, next.Hotkey.Capture, func() {
				logger.Info("capture hotkey pressed")
				if err := engine.CaptureFrom(ctx, "", workflow.CaptureSourceHotkey); err != nil {
					logger.Warn("queue hotkey capture", "error", err)
				}
			}); err != nil {
				logger.Error("configuration reload failed", "stage", "hotkey", "error", err)
				return err
			}
		}
		engine.ReplaceAnalyzer(nextCapture, nextBackend, next.Analysis.Prompt, next.Telegram.SendImage, nextBackendName, resolveProfile)
		currentConfigMu.Lock()
		currentConfig = next
		currentConfigMu.Unlock()
		logger.Info("configuration reload completed", "analysis_mode", next.Analysis.Mode, "backend", nextBackendName)
		return nil
	}

	telegramBot, err := telegram.New(cfg.Telegram, telegram.Handlers{
		Context: ctx,
		OnScreen: func(ctx context.Context, target string) error {
			return engine.CaptureFrom(ctx, target, workflow.CaptureSourceTelegram)
		},
		OnScreenProfile: func(ctx context.Context, target, profile string) error {
			return engine.CaptureFromProfile(ctx, target, workflow.CaptureSourceTelegram, profile)
		},
		OnReload: reload,
		OnCancel: func(context.Context) bool { return engine.Cancel() },
		Agents: func() string {
			currentConfigMu.RLock()
			snapshot := currentConfig
			currentConfigMu.RUnlock()
			return formatAgents(snapshot)
		},
		Status: func() string {
			currentConfigMu.RLock()
			snapshot := currentConfig
			currentConfigMu.RUnlock()
			return formatStatus(engine.Status(), snapshot)
		},
	})
	if err != nil {
		logger.Error("initialize Telegram", "error", err)
		os.Exit(1)
	}

	engine = workflow.NewAnalyzer(capture, backend, telegramBot, cfg.Analysis.Prompt, cfg.Telegram.SendImage, backendName, resolveProfile)
	go func() {
		if err := engine.Run(ctx); err != nil {
			logger.Error("workflow stopped", "error", err)
			cancel()
		}
	}()
	go func() {
		logger.Info("Telegram bot started")
		telegramBot.Start()
		cancel()
	}()

	if err := hotkeyManager.Start(cfg.Hotkey.Enabled, cfg.Hotkey.Capture, func() {
		logger.Info("capture hotkey pressed")
		if err := engine.CaptureFrom(ctx, "", workflow.CaptureSourceHotkey); err != nil {
			logger.Warn("queue hotkey capture", "error", err)
		}
	}); err != nil {
		logger.Error("configure hotkey", "error", err)
		cancel()
	}

	trayEnabled := buildMode == buildModeGUI || cfg.Tray.Enabled
	if buildMode == buildModeGUI && !cfg.Tray.Enabled {
		logger.Info("tray enabled by GUI build mode")
	}
	if trayEnabled {
		go tray.Run(ctx, cfg.App.Name, tray.Actions{
			Capture: func() {
				if err := engine.CaptureFrom(ctx, "", workflow.CaptureSourceTray); err != nil {
					logger.Warn("queue tray capture", "error", err)
				}
			},
			Reload: func() {
				if err := reload(ctx); err != nil {
					logger.Warn("reload config from tray", "error", err)
				}
			},
			Exit: cancel,
		})
	}
	logger.Info("ScreenLens ready", "hotkey_enabled", cfg.Hotkey.Enabled, "tray_enabled", trayEnabled)

	<-ctx.Done()
	hotkeyManager.Stop()
	telegramBot.Stop()
	logger.Info("ScreenLens stopped")
}

func buildComponents(cfg config.Config) (*screenshot.Capturer, analyzer.Analyzer, string, error) {
	capture, err := screenshot.New(cfg.Capture)
	if err != nil {
		return nil, nil, "", fmt.Errorf("create screenshot capturer: %w", err)
	}
	backend, backendName, err := buildAnalyzer(cfg, capture.MIMEType())
	if err != nil {
		return nil, nil, "", err
	}
	return capture, backend, backendName, nil
}

func buildAnalyzer(cfg config.Config, imageMIME string) (analyzer.Analyzer, string, error) {
	if cfg.Analysis.Mode == config.AnalysisModeLocalAgent {
		return buildLocalAnalyzer(cfg)
	}
	backend, err := buildVisionAnalyzer(cfg, imageMIME)
	if err != nil {
		return nil, "", err
	}
	return backend, "vision", nil
}

func buildVisionAnalyzer(cfg config.Config, imageMIME string) (analyzer.Analyzer, error) {
	visionConfig, err := cfg.ActiveVisionConfig()
	if err != nil {
		return nil, fmt.Errorf("resolve vision profile: %w", err)
	}
	visionClient, err := vision.New(visionConfig, imageMIME)
	if err != nil {
		return nil, fmt.Errorf("create vision client: %w", err)
	}
	return analyzer.NewVisionAdapter(visionClient, imageMIME), nil
}

func buildLocalAnalyzer(cfg config.Config) (analyzer.Analyzer, string, error) {
	backend, info, err := agent.Build(cfg.Analysis, cfg.LocalAgentProfiles())
	if err != nil {
		return nil, "", fmt.Errorf("create local agent: %w", err)
	}
	return backend, info.Provider, nil
}

func formatStatus(status workflow.Status, cfg config.Config) string {
	state := statusIdle
	if status.Busy {
		state = statusCapturing
	}
	parts := []string{
		"# ScreenLens status",
		"",
		"- **State:** `" + state + "`",
	}
	if cfg.Analysis.Mode == config.AnalysisModeLocalAgent {
		profile := status.Profile
		if profile == "" {
			profile = cfg.Analysis.Profile
		}
		if profile == "" {
			profile = status.Backend
		}
		parts = append(parts, "- **Analysis mode:** `local-agent`", "- **Agent:** `"+status.Backend+"`", "- **Profile:** `"+profile+"`")
	} else {
		visionConfig, err := cfg.ActiveVisionConfig()
		if err != nil {
			parts = append(parts, "- **Analysis mode:** `vision`", "- **Vision profile:** unavailable")
		} else {
			parts = append(parts, "- **Analysis mode:** `vision`", "- **Vision protocol:** `"+config.NormalizeProtocol(visionConfig.Protocol, visionConfig.Provider)+"`", "- **Model:** `"+visionConfig.Model+"`")
		}
	}
	if !status.LastFinished.IsZero() {
		parts = append(parts, "- **Last duration:** `"+status.LastDuration.Round(time.Millisecond).String()+"`")
	} else {
		parts = append(parts, "- **Last duration:** _not available yet_")
	}
	if strings.TrimSpace(status.LastError) != "" {
		parts = append(parts, "- **Last error:** "+strings.ReplaceAll(status.LastError, "\n", " "))
	}
	if strings.TrimSpace(status.LastWarning) != "" {
		parts = append(parts, "- **Last warning:** "+strings.ReplaceAll(status.LastWarning, "\n", " "))
	}
	if strings.TrimSpace(status.SessionID) != "" {
		parts = append(parts, "- **Session:** `"+status.SessionID+"`")
	}
	return strings.Join(parts, "\n")
}

func formatAgents(cfg config.Config) string {
	parts := []string{"# Local agent profiles", "", "Use `/screen [profile]` to select one for a single capture."}
	if cfg.Analysis.Mode == config.AnalysisModeLocalAgent {
		parts = append(parts, "", "- **Active:** `"+cfg.Analysis.Profile+"`")
	}
	names := make([]string, 0, len(cfg.Analysis.Profiles))
	visionNames := make([]string, 0, len(cfg.Analysis.Profiles))
	for name, profile := range cfg.Analysis.Profiles {
		if profile.Type == config.AnalysisProfileTypeLocalAgent {
			names = append(names, name)
		} else if profile.Type == config.AnalysisProfileTypeVisionAPI {
			visionNames = append(visionNames, name)
		}
	}
	sort.Strings(names)
	sort.Strings(visionNames)
	for _, name := range visionNames {
		parts = append(parts, "- `"+name+"` (hosted vision API)")
	}
	for _, name := range names {
		parts = append(parts, "- `"+name+"`")
	}
	return strings.Join(parts, "\n")
}

func telegramRestartRequired(current, next config.TelegramConfig) bool {
	if current.Token != next.Token || current.ChatID != next.ChatID ||
		current.PollTimeout != next.PollTimeout || current.Timeout != next.Timeout ||
		current.Proxy != next.Proxy {
		return true
	}
	return !sameUserIDs(current.AllowedUserIDs, next.AllowedUserIDs)
}

func sameUserIDs(left, right []int64) bool {
	if len(left) != len(right) {
		return false
	}
	seen := make(map[int64]struct{}, len(left))
	for _, userID := range left {
		seen[userID] = struct{}{}
	}
	for _, userID := range right {
		if _, ok := seen[userID]; !ok {
			return false
		}
	}
	return true
}
