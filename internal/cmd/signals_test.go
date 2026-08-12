package cmd

import (
	"errors"
	"io"
	"testing"
)

type readerFunc func([]byte) (int, error)

func (r readerFunc) Read(value []byte) (int, error) { return r(value) }

type recordingLoginTerminal struct {
	observe     func()
	promptCalls int
	secretCalls int
}

func (t *recordingLoginTerminal) IsTerminal() bool { return true }

func (t *recordingLoginTerminal) Prompt(label, defaultValue string) (string, error) {
	t.promptCalls++
	t.observe()
	return label + "=" + defaultValue, nil
}

func (t *recordingLoginTerminal) PromptSecret(label string) (string, error) {
	t.secretCalls++
	t.observe()
	return "", errors.New(label)
}

func TestInteractivePromptsSuspendSignalDelivery(t *testing.T) {
	notifier := installExecutionSignals()
	defer notifier.stop()
	suspended := func() bool {
		notifier.mutex.Lock()
		defer notifier.mutex.Unlock()
		return notifier.suspended
	}

	var inside []bool
	terminal := &recordingLoginTerminal{observe: func() { inside = append(inside, suspended()) }}
	prompting := signalSuspendingTerminal{terminal: terminal, notifier: notifier}

	if !prompting.IsTerminal() || suspended() {
		t.Fatalf("IsTerminal must not suspend delivery")
	}
	value, err := prompting.Prompt("Host", "https://jira.example")
	if value != "Host=https://jira.example" || err != nil {
		t.Fatalf("Prompt() = %q, %v", value, err)
	}
	if suspended() {
		t.Fatal("Prompt left signal delivery suspended")
	}
	if _, err := prompting.PromptSecret("Token"); err == nil || err.Error() != "Token" {
		t.Fatalf("PromptSecret() error = %v", err)
	}
	if suspended() {
		t.Fatal("PromptSecret left signal delivery suspended")
	}
	if terminal.promptCalls != 1 || terminal.secretCalls != 1 {
		t.Fatalf("prompts=%d secrets=%d", terminal.promptCalls, terminal.secretCalls)
	}
	if len(inside) != 2 || !inside[0] || !inside[1] {
		t.Fatalf("suspended during prompts = %v", inside)
	}
}

func TestStdinCredentialReadSuspendsSignalDelivery(t *testing.T) {
	notifier := installExecutionSignals()
	defer notifier.stop()
	suspended := func() bool {
		notifier.mutex.Lock()
		defer notifier.mutex.Unlock()
		return notifier.suspended
	}

	inside := false
	a := &app{signals: notifier, stdin: readerFunc(func(value []byte) (int, error) {
		inside = suspended()
		return copy(value, "token\n"), io.EOF
	})}
	secret, err := a.readStdinCredential()
	if secret != "token" || err != nil {
		t.Fatalf("readStdinCredential() = %q, %v", secret, err)
	}
	if !inside {
		t.Fatal("credential read ran with signal delivery installed")
	}
	if suspended() {
		t.Fatal("credential read left signal delivery suspended")
	}
}

func TestStoppedNotifierKeepsPromptsWorking(t *testing.T) {
	notifier := installExecutionSignals()
	notifier.stop()
	notifier.stop()

	terminal := &recordingLoginTerminal{observe: func() {}}
	prompting := signalSuspendingTerminal{terminal: terminal, notifier: notifier}
	if _, err := prompting.Prompt("Host", ""); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	notifier.mutex.Lock()
	defer notifier.mutex.Unlock()
	if notifier.suspended {
		t.Fatal("a stopped notifier must not record a suspension")
	}
}
