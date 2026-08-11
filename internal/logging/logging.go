package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"gopkg.in/natefinch/lumberjack.v2"
)

const defaultFileName = "screenlens.log"

type Handle struct {
	Logger *slog.Logger
	writer *lumberjack.Logger
	Path   string
}

func Open(configuredPath string, mirrorConsole bool) (*Handle, error) {
	path, err := ResolvePath(configuredPath)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	writer := &lumberjack.Logger{
		Filename:   path,
		MaxSize:    10,
		MaxBackups: 5,
		MaxAge:     30,
		Compress:   true,
	}
	var output io.Writer = writer
	if mirrorConsole {
		output = io.MultiWriter(writer, os.Stderr)
	}
	return &Handle{
		Logger: slog.New(slog.NewTextHandler(output, &slog.HandlerOptions{Level: slog.LevelInfo})),
		writer: writer,
		Path:   path,
	}, nil
}

func (h *Handle) Close() error {
	if h == nil || h.writer == nil {
		return nil
	}
	return h.writer.Close()
}

func ResolvePath(configuredPath string) (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}
	baseDir := filepath.Dir(executable)
	configuredPath = filepath.Clean(configuredPath)
	if configuredPath == "." || configuredPath == "" {
		return filepath.Join(baseDir, defaultFileName), nil
	}
	if filepath.IsAbs(configuredPath) {
		return configuredPath, nil
	}
	return filepath.Join(baseDir, configuredPath), nil
}
