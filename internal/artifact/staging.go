package artifact

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/th1nk-er/ScreenLens/internal/config"
)

const temporaryDirectoryPattern = "screenlens-run-*"

// Image is a short-lived, private image artifact used by local backends.
// Cleanup is idempotent so callers can safely defer it and also clean up on
// early process failures.
type Image struct {
	Dir  string
	Path string

	mu      sync.Mutex
	cleaned bool
}

func Stage(data []byte, mimeType string) (*Image, error) {
	return StageInDir(data, mimeType, "")
}

// StageInDir stages an image below dir. An empty dir creates a private system
// temporary directory. A caller-supplied dir is useful for CLI sandboxes that
// only permit reads below their working directory.
func StageInDir(data []byte, mimeType, dir string) (*Image, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("cannot stage an empty image")
	}

	var runDir string
	var err error
	if strings.TrimSpace(dir) == "" {
		runDir, err = os.MkdirTemp("", temporaryDirectoryPattern)
	} else {
		base, absErr := filepath.Abs(dir)
		if absErr != nil {
			return nil, fmt.Errorf("resolve artifact directory: %w", absErr)
		}
		if err = os.MkdirAll(base, 0o700); err == nil {
			runDir, err = os.MkdirTemp(base, temporaryDirectoryPattern)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("create artifact directory: %w", err)
	}

	extension := extensionForMIME(mimeType)
	path := filepath.Join(runDir, "screenshot"+extension)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		_ = os.RemoveAll(runDir)
		return nil, fmt.Errorf("write screenshot artifact: %w", err)
	}
	return &Image{Dir: runDir, Path: path}, nil
}

func (i *Image) Cleanup() error {
	if i == nil {
		return nil
	}
	i.mu.Lock()
	if i.cleaned {
		i.mu.Unlock()
		return nil
	}
	i.cleaned = true
	i.mu.Unlock()
	if err := os.RemoveAll(i.Dir); err != nil {
		return fmt.Errorf("remove screenshot artifact: %w", err)
	}
	return nil
}

func extensionForMIME(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case config.MIMETypePNG:
		return ".png"
	default:
		return ".jpg"
	}
}
