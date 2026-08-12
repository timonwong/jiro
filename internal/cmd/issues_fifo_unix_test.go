//go:build !windows

package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestIssueTextInputCancellationUnblocksFIFOOpen(t *testing.T) {
	clearCommandEnv(t)
	path := filepath.Join(t.TempDir(), "description.fifo")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := &app{stdin: strings.NewReader(""), stdout: stdout, stderr: stderr}
	code := a.executeContext(ctx, []string{
		"--config", writeCLIConfig(t, "https://jira.invalid", false),
		"issue", "add", "--project", "AIT", "--type", "Task", "--summary", "Example", "--description-file", path,
	}, func() int { return 130 })
	if code != 130 || stdout.Len() != 0 || stderr.String() != "canceled\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	unblocked := make(chan error, 1)
	go func() {
		writer, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err == nil {
			err = writer.Close()
		}
		unblocked <- err
	}()
	select {
	case err := <-unblocked:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("FIFO open did not unblock after cancellation")
	}
}
