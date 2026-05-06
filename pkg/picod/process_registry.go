/*
Copyright The Volcano Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package picod

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"k8s.io/klog/v2"
)

const (
	maxProcesses      = 100
	reapInterval      = 30 * time.Second
	defaultProcessTTL = 900 * time.Second
)

// processEntry wraps a managed process with its runtime resources
type processEntry struct {
	process   *ManagedProcess
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    io.ReadCloser
	stderr    io.ReadCloser
	events    chan ProcessEvent
	listeners []chan ProcessEvent
	mu        sync.RWMutex
	startedAt time.Time
}

// ProcessRegistry manages a collection of running processes
type ProcessRegistry struct {
	mu        sync.RWMutex
	processes map[string]*processEntry
	wg        sync.WaitGroup
	stopCh    chan struct{}
	// procCtx is the registry-owned context used to bind spawned processes.
	// It is canceled in Stop() so all running processes are torn down on
	// registry shutdown. Crucially, this is independent from any caller's
	// context (e.g., the HTTP request context), which would otherwise kill
	// processes the moment the request completes.
	procCtx    context.Context
	procCancel context.CancelFunc
}

// NewProcessRegistry creates a new process registry with background reaper
func NewProcessRegistry() *ProcessRegistry {
	procCtx, procCancel := context.WithCancel(context.Background())
	r := &ProcessRegistry{
		processes:  make(map[string]*processEntry),
		stopCh:     make(chan struct{}),
		procCtx:    procCtx,
		procCancel: procCancel,
	}
	r.wg.Add(1)
	go r.reaper()
	return r
}

// Stop gracefully shuts down the registry and waits for background goroutines
func (r *ProcessRegistry) Stop() {
	close(r.stopCh)
	r.procCancel()
	r.wg.Wait()
}

// Start spawns a new process and returns its process ID.
//
// The caller's ctx is intentionally NOT used to bind the spawned process
// because envd handlers pass c.Request.Context(), which is canceled the
// moment the response is written. Processes must survive across multiple
// HTTP requests (e.g., subsequent input/signal/list calls), so they are
// bound to the registry-owned context instead.
func (r *ProcessRegistry) Start(_ context.Context, cmd []string, env map[string]string, cwd string, timeout int) (*ManagedProcess, error) {
	r.mu.Lock()
	if len(r.processes) >= maxProcesses {
		r.mu.Unlock()
		return nil, fmt.Errorf("process limit exceeded: max %d", maxProcesses)
	}
	r.mu.Unlock()

	if len(cmd) == 0 {
		return nil, fmt.Errorf("cmd is required")
	}

	processID := "proc_" + uuid.New().String()[:8]

	procCtx := r.procCtx
	if timeout > 0 {
		var cancel context.CancelFunc
		procCtx, cancel = context.WithTimeout(r.procCtx, time.Duration(timeout)*time.Second)
		_ = cancel
	}

	command := exec.CommandContext(procCtx, cmd[0], cmd[1:]...) //nolint:gosec // cmd is validated by caller

	// Set working directory
	if cwd != "" {
		command.Dir = cwd
	}

	// Merge environment variables
	mergedEnv := os.Environ()
	for k, v := range env {
		mergedEnv = append(mergedEnv, fmt.Sprintf("%s=%s", k, v))
	}
	command.Env = mergedEnv

	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := command.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("failed to start process: %w", err)
	}

	now := time.Now()
	mp := &ManagedProcess{
		ProcessID: processID,
		PID:       command.Process.Pid,
		Cmd:       cmd,
		Cwd:       cwd,
		Env:       command.Env,
		State:     ProcessStateRunning,
		StartedAt: now,
	}

	entry := &processEntry{
		process:   mp,
		cmd:       command,
		stdin:     stdin,
		stdout:    stdout,
		stderr:    stderr,
		events:    make(chan ProcessEvent, 64),
		listeners: make([]chan ProcessEvent, 0),
		startedAt: now,
	}

	r.mu.Lock()
	r.processes[processID] = entry
	r.mu.Unlock()

	klog.Infof("process started: id=%s pid=%d cmd=%v", processID, mp.PID, cmd)
	result := *mp

	// Start output goroutines
	go r.readOutput(entry, stdout, ProcessEventTypeStdout)
	go r.readOutput(entry, stderr, ProcessEventTypeStderr)
	go r.waitProcess(entry)

	return &result, nil
}

// Input writes data to a process's stdin
func (r *ProcessRegistry) Input(processID string, data string) error {
	r.mu.RLock()
	entry, ok := r.processes[processID]
	r.mu.RUnlock()
	if !ok {
		return fmt.Errorf("process not found: %s", processID)
	}

	entry.mu.RLock()
	stdin := entry.stdin
	entry.mu.RUnlock()

	if stdin == nil {
		return fmt.Errorf("stdin is closed")
	}

	_, err := io.WriteString(stdin, data)
	return err
}

// CloseStdin closes the stdin pipe of a process
func (r *ProcessRegistry) CloseStdin(processID string) error {
	r.mu.RLock()
	entry, ok := r.processes[processID]
	r.mu.RUnlock()
	if !ok {
		return fmt.Errorf("process not found: %s", processID)
	}

	entry.mu.Lock()
	if entry.stdin != nil {
		_ = entry.stdin.Close()
		entry.stdin = nil
	}
	entry.mu.Unlock()
	return nil
}

// Signal sends an OS signal to a process
func (r *ProcessRegistry) Signal(processID string, sig int) error {
	r.mu.RLock()
	entry, ok := r.processes[processID]
	r.mu.RUnlock()
	if !ok {
		return fmt.Errorf("process not found: %s", processID)
	}

	entry.mu.RLock()
	cmd := entry.cmd
	entry.mu.RUnlock()

	if cmd == nil || cmd.Process == nil {
		return fmt.Errorf("process is not running")
	}

	return cmd.Process.Signal(syscall.Signal(sig))
}

// List returns all managed processes
func (r *ProcessRegistry) List() []*ManagedProcess {
	r.mu.RLock()
	entries := make([]*processEntry, 0, len(r.processes))
	for _, entry := range r.processes {
		entries = append(entries, entry)
	}
	r.mu.RUnlock()

	result := make([]*ManagedProcess, 0, len(entries))
	for _, entry := range entries {
		entry.mu.RLock()
		p := *entry.process
		entry.mu.RUnlock()
		result = append(result, &p)
	}
	return result
}

// Get returns a single managed process by ID
func (r *ProcessRegistry) Get(processID string) (*ManagedProcess, error) {
	r.mu.RLock()
	entry, ok := r.processes[processID]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("process not found: %s", processID)
	}

	entry.mu.RLock()
	result := *entry.process
	entry.mu.RUnlock()
	return &result, nil
}

// Subscribe returns a channel that receives events for a process
func (r *ProcessRegistry) Subscribe(processID string) (<-chan ProcessEvent, error) {
	r.mu.RLock()
	entry, ok := r.processes[processID]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("process not found: %s", processID)
	}

	ch := make(chan ProcessEvent, 64)
	entry.mu.Lock()
	entry.listeners = append(entry.listeners, ch)
	entry.mu.Unlock()
	return ch, nil
}

// Unsubscribe removes a listener channel
func (r *ProcessRegistry) Unsubscribe(processID string, ch <-chan ProcessEvent) {
	r.mu.RLock()
	entry, ok := r.processes[processID]
	r.mu.RUnlock()
	if !ok {
		return
	}

	entry.mu.Lock()
	for i, listener := range entry.listeners {
		if listener == ch {
			entry.listeners = append(entry.listeners[:i], entry.listeners[i+1:]...)
			close(listener)
			break
		}
	}
	entry.mu.Unlock()
}

func (r *ProcessRegistry) readOutput(entry *processEntry, reader io.Reader, eventType ProcessEventType) {
	buf := make([]byte, 4096)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			event := ProcessEvent{
				Type: eventType,
				Data: string(buf[:n]),
				Time: time.Now(),
			}
			r.broadcastEvent(entry, event)
		}
		if err != nil {
			if err != io.EOF {
				klog.V(2).Infof("process %s %s reader error: %v", entry.process.ProcessID, eventType, err)
			}
			break
		}
	}
}

func (r *ProcessRegistry) waitProcess(entry *processEntry) {
	err := entry.cmd.Wait()

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	now := time.Now()
	entry.mu.Lock()
	entry.process.State = ProcessStateExited
	entry.process.ExitCode = &exitCode
	entry.process.ExitedAt = &now
	entry.stdin = nil
	entry.mu.Unlock()

	r.broadcastEvent(entry, ProcessEvent{
		Type:     ProcessEventTypeExit,
		ExitCode: &exitCode,
		Time:     now,
	})

	klog.Infof("process exited: id=%s pid=%d exit_code=%d", entry.process.ProcessID, entry.process.PID, exitCode)
}

func (r *ProcessRegistry) broadcastEvent(entry *processEntry, event ProcessEvent) {
	entry.mu.RLock()
	listeners := make([]chan ProcessEvent, len(entry.listeners))
	copy(listeners, entry.listeners)
	entry.mu.RUnlock()

	for _, ch := range listeners {
		select {
		case ch <- event:
		default:
			// Channel full, drop event
		}
	}
}

func (r *ProcessRegistry) reaper() {
	defer r.wg.Done()
	ticker := time.NewTicker(reapInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.reap()
		}
	}
}

func (r *ProcessRegistry) reap() {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	for id, entry := range r.processes {
		entry.mu.RLock()
		state := entry.process.State
		exitedAt := entry.process.ExitedAt
		listenerCount := len(entry.listeners)
		entry.mu.RUnlock()

		if state == ProcessStateExited && listenerCount == 0 {
			// Reap processes that have exited and have no active listeners
			// after a grace period
			if exitedAt != nil && now.Sub(*exitedAt) > 60*time.Second {
				delete(r.processes, id)
				klog.V(2).Infof("reaped process: %s", id)
			}
		}
	}
}
