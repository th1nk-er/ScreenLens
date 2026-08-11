package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

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

	capture, visionClient, err := buildComponents(cfg)
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
	var currentConfigMu = make(chan struct{}, 1)
	currentConfigMu <- struct{}{}
	hotkeyManager := hotkey.NewManager(ctx, func(err error) {
		logger.Error("hotkey listener stopped", "error", err)
		cancel()
	})
	reload := func(context.Context) error {
		logger.Info("configuration reload started", "path", *configPath)
		next, err := config.Load(*configPath)
		if err != nil {
			logger.Error("configuration reload failed", "stage", "load", "error", err)
			return err
		}
		<-currentConfigMu
		defer func() { currentConfigMu <- struct{}{} }()
		if telegramRestartRequired(currentConfig.Telegram, next.Telegram) {
			err := fmt.Errorf("Telegram connection or authorization settings changed; restart is required")
			logger.Error("configuration reload failed", "stage", "validate", "error", err)
			return err
		}
		nextCapture, nextVision, err := buildComponents(next)
		if err != nil {
			logger.Error("configuration reload failed", "stage", "initialize", "error", err)
			return err
		}
		hotkeyChanged := next.Hotkey.Enabled != currentConfig.Hotkey.Enabled || next.Hotkey.Capture != currentConfig.Hotkey.Capture
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
		engine.Replace(nextCapture, nextVision, next.Vision.Prompt, next.Telegram.SendImage)
		currentConfig = next
		logger.Info("configuration reload completed", "protocol", next.Vision.Protocol, "model", next.Vision.Model)
		return nil
	}

	telegramBot, err := telegram.New(cfg.Telegram, telegram.Handlers{
		Context: ctx,
		OnScreen: func(ctx context.Context, target string) error {
			return engine.CaptureFrom(ctx, target, workflow.CaptureSourceTelegram)
		},
		OnReload: reload,
		Status: func() string {
			<-currentConfigMu
			defer func() { currentConfigMu <- struct{}{} }()
			return formatStatus(engine.Status(), currentConfig)
		},
	})
	if err != nil {
		logger.Error("initialize Telegram", "error", err)
		os.Exit(1)
	}

	engine = workflow.New(capture, visionClient, telegramBot, cfg.Vision.Prompt, cfg.Telegram.SendImage)
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

func buildComponents(cfg config.Config) (*screenshot.Capturer, vision.Vision, error) {
	capture, err := screenshot.New(cfg.Capture)
	if err != nil {
		return nil, nil, fmt.Errorf("create screenshot capturer: %w", err)
	}
	visionClient, err := vision.New(cfg.Vision, capture.MIMEType())
	if err != nil {
		return nil, nil, fmt.Errorf("create vision client: %w", err)
	}
	return capture, visionClient, nil
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
		"- **Vision protocol:** `" + config.NormalizeProtocol(cfg.Vision.Protocol, cfg.Vision.Provider) + "`",
		"- **Model:** `" + cfg.Vision.Model + "`",
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
	return strings.Join(parts, "\n")
}

func telegramRestartRequired(current, next config.TelegramConfig) bool {
	if current.Token != next.Token || current.ChatID != next.ChatID ||
		current.PollTimeout != next.PollTimeout || current.Timeout != next.Timeout ||
		current.Proxy != next.Proxy {
		return true
	}
	if len(current.AllowedUserIDs) != len(next.AllowedUserIDs) {
		return true
	}
	for index := range current.AllowedUserIDs {
		if current.AllowedUserIDs[index] != next.AllowedUserIDs[index] {
			return true
		}
	}
	return false
}
