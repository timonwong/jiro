//go:build !windows

package cmd

import (
	"bytes"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"testing"
	"time"
)

// guardInterrupts keeps a test-sent interrupt from killing the test binary when
// the code under test fails to install its own handler.
func guardInterrupts(t *testing.T) {
	t.Helper()
	guard := make(chan os.Signal, 1)
	signal.Notify(guard, syscall.SIGINT)
	t.Cleanup(func() { signal.Stop(guard) })
}

type blockingSignalReader struct {
	once    sync.Once
	started chan struct{}
	release chan struct{}
}

func (r *blockingSignalReader) Read([]byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	<-r.release
	return 0, io.EOF
}

func TestExecuteInterruptsOrdinaryCommands(t *testing.T) {
	clearCommandEnv(t)
	guardInterrupts(t)
	args := []string{
		"--config", writeCLIConfig(t, "https://jira.invalid", false),
		"issue", "add", "--project", "AIT", "--type", "Task", "--summary", "Example", "--description-file", "-",
	}
	stdin := &blockingSignalReader{started: make(chan struct{}), release: make(chan struct{})}
	defer close(stdin.release)
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	done := make(chan int, 1)
	go func() { done <- Execute(args, stdin, stdout, stderr) }()

	select {
	case <-stdin.started:
	case <-time.After(10 * time.Second):
		t.Fatal("command never reached the description input")
	}
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 130 || stdout.Len() != 0 || stderr.String() != "canceled\n" {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("interrupt did not cancel the command")
	}
}

func TestPromptSuspensionRestoresSignalDelivery(t *testing.T) {
	guardInterrupts(t)
	notifier := installExecutionSignals()
	defer notifier.stop()

	prompting := signalSuspendingTerminal{terminal: &recordingLoginTerminal{observe: func() {}}, notifier: notifier}
	if _, err := prompting.Prompt("Host", ""); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case <-notifier.ctx.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("interrupt was not delivered after the prompt")
	}
	if code := notifier.exitCode(); code != 130 {
		t.Fatalf("exit code = %d, want 130", code)
	}
}
