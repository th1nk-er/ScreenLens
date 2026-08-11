//go:build darwin || linux

package instance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"golang.org/x/sys/unix"
)

const (
	instanceLockFileMode = 0600
	defaultInstanceName  = "screenlens"
)

type Lock struct {
	file *os.File
}

func Acquire(name string) (*Lock, error) {
	lockPath, err := lockPath(name)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, instanceLockFileMode)
	if err != nil {
		return nil, fmt.Errorf("open instance lock %q: %w", lockPath, err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrAlreadyRunning
		}
		return nil, fmt.Errorf("lock instance file %q: %w", lockPath, err)
	}
	return &Lock{file: file}, nil
}

func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
	closeErr := l.file.Close()
	l.file = nil
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

func lockPath(name string) (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable path for instance lock: %w", err)
	}
	baseName := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			return unicode.ToLower(r)
		}
		return '_'
	}, strings.TrimSpace(name))
	baseName = strings.Trim(baseName, "_")
	if baseName == "" {
		baseName = defaultInstanceName
	}
	return filepath.Join(filepath.Dir(executable), "."+baseName+".lock"), nil
}
