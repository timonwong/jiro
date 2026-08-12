package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/timonwong/jiro/internal/markup"
)

func TestJFMToJiraReadsStdinAndWritesExactText(t *testing.T) {
	clearCommandEnv(t)
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)

	code := Execute([]string{"jfm", "to-jira"}, strings.NewReader("**done**"), stdout, stderr)

	if code != 0 || stdout.String() != "*done*" || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestJFMFromJiraReadsStdinAndWritesExactText(t *testing.T) {
	clearCommandEnv(t)
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)

	code := Execute([]string{"jfm", "from-jira"}, strings.NewReader("h2. Release"), stdout, stderr)

	if code != 0 || stdout.String() != "## Release" || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestJFMCommandsAcceptAFileOrStdinMarker(t *testing.T) {
	clearCommandEnv(t)
	path := filepath.Join(t.TempDir(), "input.jfm")
	if err := os.WriteFile(path, []byte("# Release"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{"jfm", "to-jira", path}, strings.NewReader("ignored"), stdout, stderr)
	if code != 0 || stdout.String() != "h1. Release" || stderr.Len() != 0 {
		t.Fatalf("file code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"jfm", "from-jira", "-"}, strings.NewReader("h1. Release"), stdout, stderr)
	if code != 0 || stdout.String() != "# Release" || stderr.Len() != 0 {
		t.Fatalf("stdin marker code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestJFMConversionEmptyResultWritesZeroBytes(t *testing.T) {
	clearCommandEnv(t)
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)

	code := Execute([]string{"jfm", "to-jira"}, strings.NewReader(""), stdout, stderr)

	if code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestJFMConversionHasNoJiroInputSizeLimit(t *testing.T) {
	clearCommandEnv(t)
	input := strings.Repeat("x", (16<<20)+1)
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)

	code := Execute([]string{"jfm", "to-jira"}, strings.NewReader(input), stdout, stderr)

	if code != 0 || stderr.Len() != 0 || stdout.Len() != len(input) || !bytes.Equal(stdout.Bytes(), []byte(input)) {
		t.Fatalf("code=%d stdout=%d stderr=%q", code, stdout.Len(), stderr.String())
	}
}

func TestJFMCommandHelpDocumentsTheOfflineInputContract(t *testing.T) {
	clearCommandEnv(t)
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{"jfm", "to-jira", "--help"}, strings.NewReader(""), stdout, stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	for _, value := range []string{"FILE|-", "standard input", "Jiro Flavored Markdown", "Jira Markup"} {
		if !strings.Contains(stdout.String(), value) {
			t.Fatalf("help is missing %q: %s", value, stdout.String())
		}
	}
	for _, value := range []string{"--input", "--output-file", "--inline"} {
		if strings.Contains(stdout.String(), value) {
			t.Fatalf("help unexpectedly contains %q: %s", value, stdout.String())
		}
	}
}

func TestJFMConversionJSONMapsWarningsInSourceOrder(t *testing.T) {
	clearCommandEnv(t)
	input := "[one](https://one.example \"One\")\n\n[two](https://two.example \"Two\")"
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)

	code := Execute([]string{"--output=json", "jfm", "to-jira"}, strings.NewReader(input), stdout, stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	var response struct {
		SchemaVersion string `json:"schemaVersion"`
		Data          struct {
			JiraMarkup string `json:"jiraMarkup"`
		} `json:"data"`
		Warnings []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Details struct {
				Direction string `json:"direction"`
				Line      int    `json:"line"`
				Column    int    `json:"column"`
				Construct string `json:"construct"`
				Reason    string `json:"reason"`
			} `json:"details"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.SchemaVersion != "1" || response.Data.JiraMarkup != "[one|https://one.example]\n\n[two|https://two.example]" {
		t.Fatalf("response = %#v", response)
	}
	if len(response.Warnings) != 2 {
		t.Fatalf("warnings = %#v", response.Warnings)
	}
	for index, warning := range response.Warnings {
		line := 1
		if index == 1 {
			line = 3
		}
		wantMessage := fmt.Sprintf("JFM to Jira conversion at line %d, column 1 (link): Markdown link title was discarded because Jira Markup has no equivalent", line)
		if warning.Code != "jfm_conversion" || warning.Message != wantMessage ||
			warning.Details.Direction != "jfm_to_jira" || warning.Details.Line != line || warning.Details.Column != 1 ||
			warning.Details.Construct != "link" || warning.Details.Reason != "Markdown link title was discarded because Jira Markup has no equivalent" {
			t.Fatalf("warning[%d] = %#v", index, warning)
		}
	}
}

func TestJFMConversionJSONOmitsWarningsWhenConversionIsWarningFree(t *testing.T) {
	clearCommandEnv(t)
	tests := []struct {
		name, command, input, field, want string
	}{
		{"to Jira", "to-jira", "**done**", "jiraMarkup", "*done*"},
		{"from Jira", "from-jira", "h1. Release", "jfm", "# Release"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
			code := Execute([]string{"--output=json", "jfm", test.command}, strings.NewReader(test.input), stdout, stderr)
			if code != 0 || stderr.Len() != 0 {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			var response struct {
				SchemaVersion string            `json:"schemaVersion"`
				Data          map[string]string `json:"data"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
				t.Fatal(err)
			}
			if response.SchemaVersion != "1" || response.Data[test.field] != test.want {
				t.Fatalf("response=%s", stdout.String())
			}
			if _, found := raw["warnings"]; found {
				t.Fatalf("warning-free response contains warnings: %s", stdout.String())
			}
		})
	}
}

func TestJFMFromJiraWritesConversionWarningsToStderr(t *testing.T) {
	clearCommandEnv(t)
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)

	code := Execute([]string{"jfm", "from-jira"}, strings.NewReader("{expand:title=More}\n*bold*\n{expand}"), stdout, stderr)

	if code != 0 || stdout.String() != "{expand:title=More}\n**bold**\n{expand}" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	want := "warning: Jira to JFM conversion at line 1, column 1 (jira-macro): unsupported Jira macro delimiters remain literal while its body is converted best-effort\n"
	if stderr.String() != want {
		t.Fatalf("stderr=%q, want %q", stderr.String(), want)
	}
}

func TestJFMQuietSuppressesTextButNotWarnings(t *testing.T) {
	clearCommandEnv(t)
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)

	code := Execute([]string{"--quiet", "jfm", "to-jira"}, strings.NewReader("[Title](https://example.com \"Read\")"), stdout, stderr)

	if code != 0 || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	want := "warning: JFM to Jira conversion at line 1, column 1 (link): Markdown link title was discarded because Jira Markup has no equivalent\n"
	if stderr.String() != want {
		t.Fatalf("stderr=%q, want %q", stderr.String(), want)
	}
}

func TestJFMQuietDoesNotSuppressJSON(t *testing.T) {
	clearCommandEnv(t)
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)

	code := Execute([]string{"--quiet", "--output=json", "jfm", "to-jira"}, strings.NewReader("**done**"), stdout, stderr)

	if code != 0 || stderr.Len() != 0 || stdout.String() != "{\"schemaVersion\":\"1\",\"data\":{\"jiraMarkup\":\"*done*\"}}\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestJFMRejectsInvalidInputWithoutPartialOutput(t *testing.T) {
	clearCommandEnv(t)
	missing := filepath.Join(t.TempDir(), "missing.jfm")
	tests := []struct {
		name string
		args []string
	}{
		{"too many files", []string{"jfm", "to-jira", "one.jfm", "two.jfm"}},
		{"missing file", []string{"jfm", "to-jira", missing}},
		{"inline input flag", []string{"jfm", "to-jira", "--input", "# Release"}},
		{"output file flag", []string{"jfm", "from-jira", "--output-file", "result.md"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
			code := Execute(test.args, strings.NewReader("[Title](https://example.com \"Read\")"), stdout, stderr)
			if code != 2 || stdout.Len() != 0 || stderr.Len() == 0 {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestJFMFileFailureUsesTheJSONInvalidInputEnvelope(t *testing.T) {
	clearCommandEnv(t)
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute(
		[]string{"--output=json", "jfm", "to-jira", filepath.Join(t.TempDir(), "missing.jfm")},
		strings.NewReader(""), stdout, stderr,
	)
	if code != 2 || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var response struct {
		SchemaVersion string `json:"schemaVersion"`
		Error         struct {
			Kind string `json:"kind"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stderr.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.SchemaVersion != "1" || response.Error.Kind != "invalid_input" {
		t.Fatalf("response=%s", stderr.String())
	}
}

func TestJFMConversionDoesNotLoadConfigurationOrCredentials(t *testing.T) {
	clearCommandEnv(t)
	configPath := filepath.Join(t.TempDir(), "invalid-config.toml")
	if err := os.WriteFile(configPath, []byte("this is not valid = ["), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := &app{
		stdin:       strings.NewReader("h1. Release"),
		stdout:      stdout,
		stderr:      stderr,
		secretStore: jfmFailingSecretStore{},
	}

	code := a.execute([]string{"--config", configPath, "jfm", "from-jira"})

	if code != 0 || stdout.String() != "# Release" || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestJFMConversionBrokenPipeIsSilentSuccess(t *testing.T) {
	clearCommandEnv(t)
	for _, args := range [][]string{
		{"jfm", "to-jira"},
		{"--output=json", "jfm", "to-jira"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			stderr := new(bytes.Buffer)
			a := &app{stdin: strings.NewReader("**done**"), stdout: brokenPipeWriter{}, stderr: stderr}

			code := a.execute(args)

			if code != 0 || stderr.Len() != 0 {
				t.Fatalf("code=%d stderr=%q", code, stderr.String())
			}
		})
	}
}

func TestJFMConversionInternalFailureUsesUnexpectedErrorWithoutPartialOutput(t *testing.T) {
	clearCommandEnv(t)
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
			a := &app{stdin: strings.NewReader("input"), stdout: stdout, stderr: stderr}
			root := a.rootCommand()
			jfm := exactCommand(root, "jfm")
			jfm.RemoveCommand(exactCommand(root, "jfm to-jira"))
			jfm.AddCommand(a.jfmConversionCommand(jfmConversionSpec{
				name: "to-jira", short: "Convert Jiro Flavored Markdown to Jira Markup",
				direction: jfmToJiraDirection, resultField: "jiraMarkup",
				convert: func(context.Context, string) (string, []markup.ConversionWarning, error) {
					return "partial result", []markup.ConversionWarning{{
						Line: 1, Column: 1, Construct: "directive", Reason: "must not be emitted",
					}}, fmt.Errorf("%w: invariant failed", markup.ErrConversion)
				},
			}))
			args := []string{"jfm", "to-jira"}
			if format == "json" {
				args = append([]string{"--output=json"}, args...)
			}

			code := a.executeWithRoot(context.Background(), root, args, func() int { return 0 })

			if code != 1 || stdout.Len() != 0 || strings.Contains(stderr.String(), "must not be emitted") {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if format == "text" {
				if stderr.String() != "JFM conversion failed: invariant failed\n" {
					t.Fatalf("stderr=%q", stderr.String())
				}
				return
			}
			var response struct {
				SchemaVersion string `json:"schemaVersion"`
				Error         struct {
					Kind    string `json:"kind"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(stderr.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.SchemaVersion != "1" || response.Error.Kind != "unexpected" || response.Error.Message != "JFM conversion failed: invariant failed" {
				t.Fatalf("response=%s", stderr.String())
			}
		})
	}
}

func TestJFMConversionCancellationUnblocksStdin(t *testing.T) {
	clearCommandEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	stdin := &blockingJFMReadCloser{closed: make(chan struct{})}
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := &app{stdin: stdin, stdout: stdout, stderr: stderr}

	code := a.executeContext(ctx, []string{"jfm", "to-jira", "-"}, func() int { return 130 })

	if code != 130 || stdout.Len() != 0 || stderr.String() != "conversion canceled\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestJFMConversionCancellationDuringConversionHasNoPartialOutput(t *testing.T) {
	clearCommandEnv(t)
	ctx := &cancelJFMConversionContext{}
	ctx.remaining.Store(16)
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := &app{stdin: strings.NewReader(strings.Repeat("paragraph with **formatting** and [link](https://example.com)\n\n", 1000)), stdout: stdout, stderr: stderr}

	code := a.executeContext(ctx, []string{"jfm", "to-jira"}, func() int { return 143 })

	if code != 143 || stdout.Len() != 0 || stderr.String() != "conversion canceled\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

type blockingJFMReadCloser struct {
	closed chan struct{}
	once   sync.Once
}

func (r *blockingJFMReadCloser) Read([]byte) (int, error) {
	<-r.closed
	return 0, io.ErrClosedPipe
}

func (r *blockingJFMReadCloser) Close() error {
	r.once.Do(func() { close(r.closed) })
	return nil
}

type jfmFailingSecretStore struct{}

func (jfmFailingSecretStore) Get(string, string) (string, error) { panic("credential store accessed") }
func (jfmFailingSecretStore) Set(string, string, string) error   { panic("credential store accessed") }
func (jfmFailingSecretStore) Delete(string, string) error        { panic("credential store accessed") }

type cancelJFMConversionContext struct{ remaining atomic.Int32 }

func (ctx *cancelJFMConversionContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (ctx *cancelJFMConversionContext) Done() <-chan struct{}       { return nil }
func (ctx *cancelJFMConversionContext) Value(any) any               { return nil }
func (ctx *cancelJFMConversionContext) Err() error {
	if ctx.remaining.Add(-1) <= 0 {
		return context.Canceled
	}
	return nil
}
