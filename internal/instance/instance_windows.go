//go:build windows

package instance

import (
	"fmt"

	"golang.org/x/sys/windows"
)

type Lock struct {
	handle windows.Handle
}

func Acquire(name string) (*Lock, error) {
	mutexName, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, fmt.Errorf("encode instance name: %w", err)
	}
	handle, err := windows.CreateMutex(nil, false, mutexName)
	if err == windows.ERROR_ALREADY_EXISTS {
		_ = windows.CloseHandle(handle)
		return nil, ErrAlreadyRunning
	}
	if err != nil {
		return nil, fmt.Errorf("create instance mutex: %w", err)
	}
	return &Lock{handle: handle}, nil
}

func (l *Lock) Close() error {
	if l == nil || l.handle == 0 {
		return nil
	}
	err := windows.CloseHandle(l.handle)
	l.handle = 0
	return err
}
