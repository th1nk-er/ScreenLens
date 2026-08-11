package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
)

var ErrOutputLimit = errors.New("local agent output exceeded the configured limit")

type ProcessSpec struct {
	Command        string
	Args           []string
	Dir            string
	Env            []string
	MaxOutputBytes int
}

type ProcessResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

type ProcessRunner interface {
	Run(context.Context, ProcessSpec) (ProcessResult, error)
}

type OSProcessRunner struct{}

func (OSProcessRunner) Run(ctx context.Context, spec ProcessSpec) (ProcessResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if spec.Command == "" {
		return ProcessResult{}, errors.New("local agent command is empty")
	}
	if spec.MaxOutputBytes < 1 {
		return ProcessResult{}, errors.New("local agent output limit must be positive")
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.CommandContext(runCtx, spec.Command, spec.Args...)
	command.Dir = spec.Dir
	command.Env = spec.Env
	configureCommand(command)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return ProcessResult{}, fmt.Errorf("create local agent stdout pipe: %w", err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return ProcessResult{}, fmt.Errorf("create local agent stderr pipe: %w", err)
	}
	if err := command.Start(); err != nil {
		return ProcessResult{}, fmt.Errorf("start local agent %q: %w", spec.Command, err)
	}

	var outputMu sync.Mutex
	var stdoutBuffer, stderrBuffer limitedBuffer
	stdoutBuffer.limit = spec.MaxOutputBytes
	stderrBuffer.limit = spec.MaxOutputBytes
	var exceeded bool
	var readers sync.WaitGroup
	readOutput := func(reader io.ReadCloser, target *limitedBuffer) {
		defer readers.Done()
		_, readErr := io.Copy(target, reader)
		if errors.Is(readErr, ErrOutputLimit) {
			outputMu.Lock()
			exceeded = true
			outputMu.Unlock()
			cancel()
		}
	}
	readers.Add(2)
	go readOutput(stdout, &stdoutBuffer)
	go readOutput(stderr, &stderrBuffer)

	processDone := make(chan struct{})
	var processFinished atomic.Bool
	go func() {
		select {
		case <-runCtx.Done():
			if !processFinished.Load() {
				killProcessTree(command)
			}
		case <-processDone:
		}
	}()
	waitErr := command.Wait()
	processFinished.Store(true)
	close(processDone)
	readers.Wait()
	result := ProcessResult{
		Stdout:   stdoutBuffer.Bytes(),
		Stderr:   stderrBuffer.Bytes(),
		ExitCode: exitCode(command),
	}
	outputMu.Lock()
	wasExceeded := exceeded
	outputMu.Unlock()
	if wasExceeded {
		return result, ErrOutputLimit
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if waitErr != nil {
		return result, fmt.Errorf("local agent exited with code %d: %w", result.ExitCode, waitErr)
	}
	return result, nil
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	remaining := b.limit - b.Len()
	if remaining <= 0 {
		return 0, ErrOutputLimit
	}
	if len(data) > remaining {
		_, _ = b.Buffer.Write(data[:remaining])
		return remaining, ErrOutputLimit
	}
	return b.Buffer.Write(data)
}

// ReadFrom overrides bytes.Buffer.ReadFrom. Without this method io.Copy can
// bypass Write through the promoted bytes.Buffer implementation, defeating
// the output limit for pipe readers.
func (b *limitedBuffer) ReadFrom(reader io.Reader) (int64, error) {
	buffer := make([]byte, 32*1024)
	var total int64
	for {
		count, readErr := reader.Read(buffer)
		if count > 0 {
			written, writeErr := b.Write(buffer[:count])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return total, nil
			}
			return total, readErr
		}
	}
}

func exitCode(command *exec.Cmd) int {
	if command.ProcessState == nil {
		return -1
	}
	return command.ProcessState.ExitCode()
}
