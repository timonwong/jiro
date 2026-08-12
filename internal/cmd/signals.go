package cmd

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"

	"github.com/spf13/cobra"
)

// executionNotifier owns the process-wide signal handling installed by Execute.
// Commands observe an interrupt as context cancellation, and a repeated signal
// escapes work that cannot observe it.
type executionNotifier struct {
	ctx    context.Context
	cancel context.CancelFunc
	native chan os.Signal
	done   chan struct{}
	code   atomic.Int32

	mutex       sync.Mutex
	stopped     bool
	suspended   bool
	restorePipe func()
}

func installExecutionSignals() *executionNotifier {
	ctx, cancel := context.WithCancel(context.Background())
	notifier := &executionNotifier{
		ctx:    ctx,
		cancel: cancel,
		native: make(chan os.Signal, 1),
		done:   make(chan struct{}),
	}
	signal.Notify(notifier.native, executionSignals()...)
	go notifier.watch()
	return notifier
}

func (n *executionNotifier) watch() {
	select {
	case value := <-n.native:
		n.code.Store(int32(executionSignalExitCode(value)))
		n.cancel()
	case <-n.done:
		return
	}
	// A repeated signal ends work that cannot observe cancellation, such as a
	// blocking terminal read, keeping the exit code the contract defines.
	select {
	case value := <-n.native:
		os.Exit(executionSignalExitCode(value))
	case <-n.done:
	}
}

func (n *executionNotifier) exitCode() int { return int(n.code.Load()) }

func (n *executionNotifier) stop() {
	n.mutex.Lock()
	defer n.mutex.Unlock()
	if n.stopped {
		return
	}
	n.stopped = true
	signal.Stop(n.native)
	if n.restorePipe != nil {
		n.restorePipe()
	}
	close(n.done)
	n.cancel()
}

// suspend restores the default disposition of the execution signals until the
// returned function resumes delivery. A blocking terminal read never observes
// cancellation, so an interactive prompt must let a signal end the process.
func (n *executionNotifier) suspend() func() {
	n.mutex.Lock()
	defer n.mutex.Unlock()
	if n.stopped || n.suspended {
		return func() {}
	}
	n.suspended = true
	signal.Stop(n.native)
	return n.resume
}

func (n *executionNotifier) resume() {
	n.mutex.Lock()
	defer n.mutex.Unlock()
	if n.stopped || !n.suspended {
		return
	}
	n.suspended = false
	signal.Notify(n.native, executionSignals()...)
}

func (n *executionNotifier) ignoreBrokenPipe() {
	n.mutex.Lock()
	defer n.mutex.Unlock()
	if n.stopped || n.restorePipe != nil {
		return
	}
	n.restorePipe = ignoreBrokenPipeSignal()
}

// handlesBrokenPipe reports whether the command treats a closed output pipe as
// a normal end of output. Every other command keeps the default death by
// SIGPIPE.
func handlesBrokenPipe(command *cobra.Command) bool {
	return isRawHTTPCommand(command) || isJFMConversionCommand(command)
}

// signalSuspendingTerminal restores the default signal disposition around an
// interactive prompt, so Ctrl-C ends the process instead of queueing a
// cancellation the blocked prompt cannot act on.
type signalSuspendingTerminal struct {
	terminal loginTerminal
	notifier *executionNotifier
}

func (t signalSuspendingTerminal) IsTerminal() bool { return t.terminal.IsTerminal() }

func (t signalSuspendingTerminal) Prompt(label, defaultValue string) (string, error) {
	resume := t.notifier.suspend()
	defer resume()
	return t.terminal.Prompt(label, defaultValue)
}

func (t signalSuspendingTerminal) PromptSecret(label string) (string, error) {
	resume := t.notifier.suspend()
	defer resume()
	return t.terminal.PromptSecret(label)
}
