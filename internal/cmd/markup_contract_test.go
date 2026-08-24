package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

type textMutationCommand struct {
	name        string
	method      string
	path        string
	args        []string
	inlineFlag  string
	fileFlag    string
	payloadPath []string
	statusCode  int
	response    string
}

type capturedMutationRequest struct {
	method  string
	path    string
	payload map[string]any
	err     error
}

type textMutationExecution struct {
	source       string
	input        string
	format       string
	outputFormat string
}

type textMutationResult struct {
	requestText string
	stdout      string
	stderr      string
}

func TestJFMCapableMutationsShareTheTextInputContract(t *testing.T) {
	formats := []struct {
		name   string
		input  string
		format string
		want   string
	}{
		{
			name:   "canonical JFM input is converted",
			input:  "# Release\n\n- **done**\n- [docs](https://example.com)",
			format: "jfm",
			want:   "h1. Release\n\n* *done*\n* [docs|https://example.com]",
		},
		{
			name:   "markdown is a warning-free JFM alias",
			input:  "# Release\n\n- **done**\n- [docs](https://example.com)",
			format: "markdown",
			want:   "h1. Release\n\n* *done*\n* [docs|https://example.com]",
		},
		{
			name:  "Jira Markup is the byte-preserving default",
			input: "{panel:title=Keep}\r\n* raw _Jira_ markup *\n{panel}\n",
			want:  "{panel:title=Keep}\r\n* raw _Jira_ markup *\n{panel}\n",
		},
	}
	sources := []string{"inline", "file", "stdin"}
	commands := textMutationCommands()

	for _, format := range formats {
		for _, command := range commands {
			for _, source := range sources {
				t.Run(format.name+"/"+command.name+"/"+source, func(t *testing.T) {
					result := executeTextMutation(t, command, textMutationExecution{
						source: source, input: format.input, format: format.format, outputFormat: "json",
					})
					if result.requestText != format.want || result.stderr != "" {
						t.Fatalf("request text = %q, stderr=%q, want %q", result.requestText, result.stderr, format.want)
					}
				})
			}
		}
	}
}

func textMutationCommands() []textMutationCommand {
	return []textMutationCommand{
		{
			name:        "issue create",
			method:      http.MethodPost,
			path:        "/rest/api/2/issue",
			args:        []string{"issue", "add", "--project", "OPS", "--type", "Story", "--summary", "Markup contract"},
			inlineFlag:  "--description",
			fileFlag:    "--description-file",
			payloadPath: []string{"fields", "description"},
			statusCode:  http.StatusOK,
			response:    `{"id":"1","key":"OPS-1"}`,
		},
		{
			name:        "issue update",
			method:      http.MethodPut,
			path:        "/rest/api/2/issue/OPS-1",
			args:        []string{"issue", "update", "OPS-1"},
			inlineFlag:  "--description",
			fileFlag:    "--description-file",
			payloadPath: []string{"fields", "description"},
			statusCode:  http.StatusNoContent,
		},
		{
			name:        "issue comment",
			method:      http.MethodPost,
			path:        "/rest/api/2/issue/OPS-1/comment",
			args:        []string{"issue", "comment", "add", "OPS-1"},
			inlineFlag:  "--body",
			fileFlag:    "--body-file",
			payloadPath: []string{"body"},
			statusCode:  http.StatusOK,
			response:    `{"id":"7","body":"accepted"}`,
		},
	}
}

func TestJFMConversionWarningsRemainStructuredAndDoNotStopMutations(t *testing.T) {
	const input = `[Title](https://example.com "Read]")`
	const converted = `[Title|https://example.com]`
	const message = "JFM to Jira conversion at line 1, column 1 (link): link title was dropped; a Jira link title cannot carry a closing bracket"

	for _, format := range []string{"jfm", "markdown"} {
		for _, command := range textMutationCommands() {
			for _, outputFormat := range []string{"text", "json"} {
				t.Run(format+"/"+command.name+"/"+outputFormat, func(t *testing.T) {
					result := executeTextMutation(t, command, textMutationExecution{
						source: "inline", input: input, format: format, outputFormat: outputFormat,
					})
					if result.requestText != converted {
						t.Fatalf("request text = %q, want %q", result.requestText, converted)
					}
					if outputFormat == "text" {
						if result.stderr != "warning: "+message+"\n" || result.stdout == "" {
							t.Fatalf("stdout=%q stderr=%q", result.stdout, result.stderr)
						}
						return
					}
					if result.stderr != "" {
						t.Fatalf("stderr = %q, want empty", result.stderr)
					}
					var envelope struct {
						Warnings []struct {
							Code    string `json:"code"`
							Message string `json:"message"`
							Details struct {
								Direction string `json:"direction"`
								Field     string `json:"field"`
								Line      int    `json:"line"`
								Column    int    `json:"column"`
								Construct string `json:"construct"`
								Reason    string `json:"reason"`
							} `json:"details"`
						} `json:"warnings"`
					}
					if err := json.Unmarshal([]byte(result.stdout), &envelope); err != nil {
						t.Fatal(err)
					}
					if len(envelope.Warnings) != 1 {
						t.Fatalf("warnings = %+v", envelope.Warnings)
					}
					warning := envelope.Warnings[0]
					wantField := "description"
					if command.name == "issue comment" {
						wantField = "body"
					}
					if warning.Code != "jfm_conversion" || warning.Message != message || warning.Details.Direction != "jfm_to_jira" ||
						warning.Details.Field != wantField || warning.Details.Line != 1 || warning.Details.Column != 1 ||
						warning.Details.Construct != "link" || warning.Details.Reason != "link title was dropped; a Jira link title cannot carry a closing bracket" {
						t.Fatalf("warning = %+v", warning)
					}
				})
			}
		}
	}
}

func TestInvalidTextInputFormatStopsMutationBeforeJiraRequest(t *testing.T) {
	clearCommandEnv(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	defer server.Close()
	configPath := writeCLIConfig(t, server.URL, false)

	for _, command := range textMutationCommands() {
		t.Run(command.name, func(t *testing.T) {
			calls.Store(0)
			args := append([]string{"--config", configPath}, command.args...)
			args = append(args, command.inlineFlag, "text", "--input-format=html")
			stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
			code := Execute(args, strings.NewReader(""), stdout, stderr)
			if code != 2 || calls.Load() != 0 || stdout.Len() != 0 ||
				stderr.String() != "input format must be jira, jfm, or markdown\n" {
				t.Fatalf("code=%d calls=%d stdout=%q stderr=%q", code, calls.Load(), stdout.String(), stderr.String())
			}
		})
	}
}

func executeTextMutation(t *testing.T, command textMutationCommand, execution textMutationExecution) textMutationResult {
	t.Helper()
	clearCommandEnv(t)

	var calls atomic.Int32
	captured := make(chan capturedMutationRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		var payload map[string]any
		err := json.NewDecoder(request.Body).Decode(&payload)
		captured <- capturedMutationRequest{method: request.Method, path: request.URL.Path, payload: payload, err: err}
		writer.WriteHeader(command.statusCode)
		if command.response != "" {
			_, _ = io.WriteString(writer, command.response)
		}
	}))
	defer server.Close()

	args := []string{"--config", writeCLIConfig(t, server.URL, false)}
	if execution.outputFormat == "json" {
		args = append(args, "--output=json")
	}
	args = append(args, command.args...)
	stdin := strings.NewReader("")
	switch execution.source {
	case "inline":
		args = append(args, command.inlineFlag, execution.input)
	case "file":
		path := filepath.Join(t.TempDir(), "input.txt")
		if err := os.WriteFile(path, []byte(execution.input), 0o600); err != nil {
			t.Fatal(err)
		}
		args = append(args, command.fileFlag, path)
	case "stdin":
		stdin = strings.NewReader(execution.input)
		args = append(args, command.fileFlag, "-")
	default:
		t.Fatalf("unknown source %q", execution.source)
	}
	if execution.format != "" {
		args = append(args, "--input-format="+execution.format)
	}

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute(args, stdin, stdout, stderr)
	if code != 0 || calls.Load() != 1 {
		t.Fatalf("code=%d calls=%d stdout=%s stderr=%s", code, calls.Load(), stdout.String(), stderr.String())
	}
	request := <-captured
	if request.err != nil {
		t.Fatal(request.err)
	}
	if request.method != command.method || request.path != command.path {
		t.Fatalf("request = %s %s, want %s %s", request.method, request.path, command.method, command.path)
	}
	value, ok := stringAtJSONPath(request.payload, command.payloadPath...)
	if !ok {
		t.Fatalf("payload path %v is not a string: %#v", command.payloadPath, request.payload)
	}
	return textMutationResult{requestText: value, stdout: stdout.String(), stderr: stderr.String()}
}

func stringAtJSONPath(value map[string]any, path ...string) (string, bool) {
	var current any = value
	for _, element := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return "", false
		}
		current, ok = object[element]
		if !ok {
			return "", false
		}
	}
	result, ok := current.(string)
	return result, ok
}
