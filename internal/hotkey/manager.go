package hotkey

import (
	"context"
	"sync"
)

// Manager owns the process-wide gohook listener and lets configuration reload
// replace it without overlapping two global hook loops.
type Manager struct {
	mu      sync.Mutex
	parent  context.Context
	onError func(error)
	cancel  context.CancelFunc
	done    chan struct{}
}

func NewManager(parent context.Context, onError func(error)) *Manager {
	if parent == nil {
		parent = context.Background()
	}
	return &Manager{parent: parent, onError: onError}
}

func (m *Manager) Start(enabled bool, combination string, onCapture func()) error {
	if !enabled {
		m.Stop()
		return nil
	}
	listener, err := New(combination)
	if err != nil {
		return err
	}
	return m.startListener(listener, onCapture)
}

// StartBindings starts one shared global listener for all configured
// shortcuts. Sharing the hook is important because gohook owns process-wide
// listener state.
func (m *Manager) StartBindings(enabled bool, bindings []Binding) error {
	if !enabled {
		m.Stop()
		return nil
	}
	listener, err := NewBindings(bindings)
	if err != nil {
		return err
	}
	return m.startListener(listener, nil)
}

func (m *Manager) startListener(listener *Listener, onCapture func()) error {
	m.Stop()

	ctx, cancel := context.WithCancel(m.parent)
	done := make(chan struct{})
	m.mu.Lock()
	m.cancel = cancel
	m.done = done
	m.mu.Unlock()
	go func() {
		defer close(done)
		if err := listener.Run(ctx, onCapture); err != nil && m.onError != nil {
			m.onError(err)
		}
	}()
	return nil
}

func (m *Manager) Stop() {
	m.mu.Lock()
	cancel, done := m.cancel, m.done
	m.cancel = nil
	m.done = nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}
