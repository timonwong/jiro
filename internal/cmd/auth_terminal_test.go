package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/timonwong/jiro/internal/apperr"
)

var authenticationOptions = []loginOption{
	{Label: "Personal Access Token", Value: "pat"},
	{Label: "Username and password", Value: "basic"},
}

func TestStreamLoginTerminalForms(t *testing.T) {
	t.Run("select moves with arrow keys", func(t *testing.T) {
		output := new(bytes.Buffer)
		terminal := &streamLoginTerminal{input: strings.NewReader("\x1b[B\r"), output: output}
		value, err := terminal.Select("Authentication", authenticationOptions, "pat")
		if value != "basic" || err != nil {
			t.Fatalf("Select() = %q, %v", value, err)
		}
		if !strings.HasSuffix(output.String(), "Authentication: Username and password\n") {
			t.Fatalf("answer not kept on screen: %q", output.String())
		}
	})

	t.Run("select starts on the default", func(t *testing.T) {
		terminal := &streamLoginTerminal{input: strings.NewReader("\r"), output: new(bytes.Buffer)}
		value, err := terminal.Select("Authentication", authenticationOptions, "basic")
		if value != "basic" || err != nil {
			t.Fatalf("Select() = %q, %v", value, err)
		}
	})

	t.Run("prompt accepts the prefilled default", func(t *testing.T) {
		terminal := &streamLoginTerminal{input: strings.NewReader("\r"), output: new(bytes.Buffer)}
		value, err := terminal.Prompt("Host", "https://jira.example", nil)
		if value != "https://jira.example" || err != nil {
			t.Fatalf("Prompt() = %q, %v", value, err)
		}
		if !strings.HasSuffix(terminal.output.(*bytes.Buffer).String(), "Host: https://jira.example\n") {
			t.Fatalf("answer not kept on screen: %q", terminal.output.(*bytes.Buffer).String())
		}
	})

	t.Run("prompt trims typed input", func(t *testing.T) {
		terminal := &streamLoginTerminal{input: strings.NewReader(" alice \r"), output: new(bytes.Buffer)}
		value, err := terminal.Prompt("Username", "", nil)
		if value != "alice" || err != nil {
			t.Fatalf("Prompt() = %q, %v", value, err)
		}
	})

	t.Run("secret is not echoed", func(t *testing.T) {
		output := new(bytes.Buffer)
		terminal := &streamLoginTerminal{input: strings.NewReader("s3cret\r"), output: output}
		value, err := terminal.PromptSecret("Token")
		if value != "s3cret" || err != nil {
			t.Fatalf("PromptSecret() = %q, %v", value, err)
		}
		if strings.Contains(output.String(), "s3cret") {
			t.Fatal("secret was echoed")
		}
	})

	t.Run("confirm accepts the default", func(t *testing.T) {
		terminal := &streamLoginTerminal{input: strings.NewReader("\r"), output: new(bytes.Buffer)}
		confirmed, err := terminal.Confirm("Store?", true)
		if !confirmed || err != nil {
			t.Fatalf("Confirm() = %t, %v", confirmed, err)
		}
		if !strings.HasSuffix(terminal.output.(*bytes.Buffer).String(), "Store? Yes\n") {
			t.Fatalf("answer not kept on screen: %q", terminal.output.(*bytes.Buffer).String())
		}
	})

	t.Run("confirm accepts n", func(t *testing.T) {
		terminal := &streamLoginTerminal{input: strings.NewReader("n"), output: new(bytes.Buffer)}
		confirmed, err := terminal.Confirm("Store?", true)
		if confirmed || err != nil {
			t.Fatalf("Confirm() = %t, %v", confirmed, err)
		}
	})

	t.Run("ctrl-c cancels login", func(t *testing.T) {
		terminal := &streamLoginTerminal{input: strings.NewReader("\x03"), output: new(bytes.Buffer)}
		_, err := terminal.Select("Authentication", authenticationOptions, "pat")
		if err == nil || apperr.As(err).Kind != apperr.KindInvalidInput || !strings.Contains(err.Error(), "login canceled") {
			t.Fatalf("Select() error = %v", err)
		}
	})
}
