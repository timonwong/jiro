package cmd

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestAPICommandStreamsSuccessfulResponseAndReusesAuthentication(t *testing.T) {
	clearCommandEnv(t)
	t.Setenv("JIRO_FORCE_TTY", "invalid-for-raw-output")
	originalVersion := version
	version = "1.2.3-test"
	t.Cleanup(func() { version = originalVersion })

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Fatalf("method = %q", request.Method)
		}
		if request.URL.RequestURI() != "/jira/rest/api/2/myself?expand=groups%20and%20roles" {
			t.Fatalf("RequestURI = %q", request.URL.RequestURI())
		}
		username, password, ok := request.BasicAuth()
		if !ok || username != "alice" || password != "secret" {
			t.Fatalf("basic auth = %q/%q/%t", username, password, ok)
		}
		if got := request.Header.Get("User-Agent"); got != "jiro/1.2.3-test" {
			t.Fatalf("User-Agent = %q", got)
		}
		if got := request.Header.Get("Accept"); got != "application/json" {
			t.Fatalf("Accept = %q", got)
		}
		if got := request.Header.Get("Accept-Encoding"); got != "gzip" {
			t.Fatalf("Accept-Encoding = %q", got)
		}
		if got := request.Header.Get("Content-Type"); got != "" {
			t.Fatalf("Content-Type = %q", got)
		}
		_, _ = writer.Write([]byte{'a', 0, 'b'})
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL+"/jira", false),
		"api", "/rest/api/2/myself?expand=groups%20and%20roles", "--insecure",
	}, strings.NewReader(""), stdout, stderr)
	if code != 0 || !bytes.Equal(stdout.Bytes(), []byte{'a', 0, 'b'}) || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.Bytes(), stderr.Bytes())
	}
}

func TestAPICommandHelpDocumentsRawAndInsecureContract(t *testing.T) {
	clearCommandEnv(t)
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{"api", "--help"}, strings.NewReader(""), stdout, stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	for _, value := range []string{"raw bytes", "--output is not supported", "--insecure", "unsafe", "--string-field", "--form", "--include"} {
		if !strings.Contains(stdout.String(), value) {
			t.Fatalf("help is missing %q: %s", value, stdout.String())
		}
	}
	for _, value := range []string{"--silent", "--output-file", "--jq"} {
		if strings.Contains(stdout.String(), value) {
			t.Fatalf("help unexpectedly contains %q: %s", value, stdout.String())
		}
	}
}

func TestAPICommandWritesHTTPFailuresOnlyToStderr(t *testing.T) {
	clearCommandEnv(t)

	tests := []struct {
		status int
		body   string
		code   int
	}{
		{http.StatusUnauthorized, `{"error":"auth"}`, 3},
		{http.StatusForbidden, `{"error":"forbidden"}`, 3},
		{http.StatusNotFound, `missing`, 4},
		{http.StatusTooManyRequests, `slow down`, 6},
		{http.StatusInternalServerError, `broken`, 5},
		{http.StatusBadRequest, "", 5},
	}
	for _, test := range tests {
		t.Run(fmt.Sprint(test.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(test.status)
				_, _ = io.WriteString(writer, test.body)
			}))
			defer server.Close()

			stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
			code := Execute([]string{"--config", writeCLIConfig(t, server.URL, false), "api", "rest/api/2/example"}, strings.NewReader(""), stdout, stderr)
			if code != test.code || stdout.Len() != 0 {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			want := test.body
			if want == "" {
				want = fmt.Sprintf("Jira API returned HTTP %d %s\n", test.status, http.StatusText(test.status))
			}
			if stderr.String() != want {
				t.Fatalf("stderr = %q, want %q", stderr.String(), want)
			}
		})
	}
}

func TestAPICommandRejectsExplicitGlobalOutputBeforeNetwork(t *testing.T) {
	clearCommandEnv(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()

	for _, output := range [][]string{{"-o", "text"}, {"--output=json"}, {"-ojson"}} {
		args := []string{"--config", writeCLIConfig(t, server.URL, false)}
		args = append(args, output...)
		args = append(args, "api", "rest/api/2/myself")
		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		code := Execute(args, strings.NewReader(""), stdout, stderr)
		if code != 2 || stdout.Len() != 0 || calls.Load() != 0 || stderr.String() != "--output is not supported for api\n" {
			t.Fatalf("args=%v code=%d calls=%d stdout=%q stderr=%q", args, code, calls.Load(), stdout.String(), stderr.String())
		}
	}
}

func TestAPICommandHelpWinsOverTheGlobalOutputBan(t *testing.T) {
	clearCommandEnv(t)
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{"-o", "text", "api", "--help"}, strings.NewReader(""), stdout, stderr)
	if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "--output is not supported") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestAPICommandValidatesBeforeCredentialResolution(t *testing.T) {
	clearCommandEnv(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[default]\nhost = \"https://jira.invalid\"\nusername = \"alice\"\nuse_keyring = true\nread_only = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		args []string
	}{
		{"read only", []string{"api", "rest/api/2/issue", "--method", "POST"}},
		{"unsafe endpoint", []string{"api", "https://other.example/rest/api/2/myself"}},
		{"protected header", []string{"api", "rest/api/2/myself", "-H", "Authorization: stolen"}},
		{"invalid header value", []string{"api", "rest/api/2/myself", "-H", "X-Test: \x00"}},
		{"missing input file", []string{"api", "rest/api/2/issue", "--input", filepath.Join(t.TempDir(), "missing.json")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
			a := &app{stdin: strings.NewReader(""), stdout: stdout, stderr: stderr, secretStore: failingSecretStore{}}
			code := a.execute(append([]string{"--config", path}, test.args...))
			if code != 2 || stdout.Len() != 0 || strings.Contains(stderr.String(), "keyring accessed") {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestAPICommandInfersMethodsAndBuildsOrderedJSONFields(t *testing.T) {
	clearCommandEnv(t)

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			t.Fatalf("method = %q", request.Method)
		}
		if got := request.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("Content-Type = %q", got)
		}
		var body map[string]any
		decoder := json.NewDecoder(request.Body)
		decoder.UseNumber()
		if err := decoder.Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["name"] != "last" || body["literal"] != "@file" || fmt.Sprint(body["large"]) != "9007199254740993" {
			t.Fatalf("body = %#v", body)
		}
		_, _ = io.WriteString(writer, "ok")
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "api", "rest/api/2/issue",
		"-f", "name=first", "-F", "large=9007199254740993", "-F", "name=last", "-f", "literal=@file",
	}, strings.NewReader(""), stdout, stderr)
	if code != 0 || stdout.String() != "ok" || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestAPICommandTypedFieldsReadFilesAndStdin(t *testing.T) {
	clearCommandEnv(t)
	fieldPath := filepath.Join(t.TempDir(), "field.json")
	if err := os.WriteFile(fieldPath, []byte(`[1,2,3]`), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(body["fromFile"]) != "[1 2 3]" || body["fromStdin"] != "plain text" {
			t.Fatalf("body = %#v", body)
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "api", "rest/api/2/example",
		"-F", "fromFile=@" + fieldPath, "-F", "fromStdin=@-",
	}, strings.NewReader("plain text"), stdout, stderr)
	if code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestAPICommandReplacesInvalidUTF8FieldFileWhenEncodingJSON(t *testing.T) {
	clearCommandEnv(t)
	fieldPath := filepath.Join(t.TempDir(), "field.txt")
	if err := os.WriteFile(fieldPath, []byte{0xff}, 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["invalid"] != "�" {
			t.Fatalf("invalid field = %q, want replacement rune", body["invalid"])
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "api", "rest/api/2/example", "-F", "invalid=@" + fieldPath,
	}, strings.NewReader(""), stdout, stderr)
	if code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestAPICommandRoutesFieldsToQueryWithoutReorderingEndpointQuery(t *testing.T) {
	clearCommandEnv(t)

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Fatalf("method = %q", request.Method)
		}
		if request.URL.RawQuery != "existing=one%20two&repeat=old&repeat=first&typed=%7B%22id%22%3A2%7D&repeat=last" {
			t.Fatalf("RawQuery = %q", request.URL.RawQuery)
		}
		if request.Body != nil && request.ContentLength != 0 {
			t.Fatalf("unexpected request body")
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "api", "rest/api/2/search?existing=one%20two&repeat=old",
		"--method", "get", "-f", "repeat=first", "-F", `typed={"id":2}`, "-f", "repeat=last",
	}, strings.NewReader(""), stdout, stderr)
	if code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestAPICommandAllowsReadOnlyOptionsWithQueryFields(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodOptions || request.URL.RawQuery != "probe=true" || request.ContentLength != 0 {
			t.Fatalf("method=%q query=%q ContentLength=%d", request.Method, request.URL.RawQuery, request.ContentLength)
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, true), "api", "rest/api/2/example", "--method", "OPTIONS", "-F", "probe=true",
	}, strings.NewReader(""), stdout, stderr)
	if code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestAPICommandStreamsInputAndSendsKnownContentLength(t *testing.T) {
	clearCommandEnv(t)
	inputPath := filepath.Join(t.TempDir(), "payload.bin")
	input := []byte{'x', 0, 'y', '\n'}
	if err := os.WriteFile(inputPath, input, 0o600); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.ContentLength != int64(len(input)) {
			t.Fatalf("method=%q ContentLength=%d", request.Method, request.ContentLength)
		}
		if request.URL.RawQuery != "meta=%7B%22id%22%3A2%7D" {
			t.Fatalf("RawQuery = %q", request.URL.RawQuery)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(body, input) {
			t.Fatalf("body = %q", body)
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "api", "rest/api/2/import", "--input", inputPath, "-F", `meta={"id":2}`,
	}, strings.NewReader(""), stdout, stderr)
	if code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestAPICommandStreamsInputStdinWithChunkedTransfer(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.ContentLength != -1 || len(request.TransferEncoding) != 1 || request.TransferEncoding[0] != "chunked" {
			t.Fatalf("ContentLength=%d TransferEncoding=%v", request.ContentLength, request.TransferEncoding)
		}
		if got := request.Header.Get("Content-Type"); got != "" {
			t.Fatalf("Content-Type = %q", got)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil || string(body) != "streamed" {
			t.Fatalf("body=%q error=%v", body, err)
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "api", "rest/api/2/import", "--input", "-", "-H", "Content-Type:",
	}, strings.NewReader("streamed"), stdout, stderr)
	if code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestAPICommandStreamsMultipartFormWithKnownLength(t *testing.T) {
	clearCommandEnv(t)
	filePath := filepath.Join(t.TempDir(), "payload.json")
	if err := os.WriteFile(filePath, []byte(`{"ok":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.ContentLength <= 0 || len(request.TransferEncoding) != 0 {
			t.Fatalf("method=%q ContentLength=%d TransferEncoding=%v", request.Method, request.ContentLength, request.TransferEncoding)
		}
		if got := request.Header.Get("X-Atlassian-Token"); got != "" {
			t.Fatalf("unexpected X-Atlassian-Token = %q", got)
		}
		reader, err := request.MultipartReader()
		if err != nil {
			t.Fatal(err)
		}
		parts := map[string]struct {
			filename  string
			mediaType string
			body      string
		}{}
		for {
			part, nextErr := reader.NextPart()
			if errors.Is(nextErr, io.EOF) {
				break
			}
			if nextErr != nil {
				t.Fatal(nextErr)
			}
			body, readErr := io.ReadAll(part)
			if readErr != nil {
				t.Fatal(readErr)
			}
			parts[part.FormName()] = struct {
				filename  string
				mediaType string
				body      string
			}{part.FileName(), part.Header.Get("Content-Type"), string(body)}
		}
		if parts["text"].body != "value" || parts["literal"].body != "@text" {
			t.Fatalf("text parts = %#v", parts)
		}
		file := parts["upload"]
		if file.filename != "payload.json" || file.mediaType != "application/json" || file.body != `{"ok":true}` {
			t.Fatalf("file part = %#v", file)
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "api", "rest/api/2/attachments",
		"--form", "text=value", "--form", "literal=@@text", "--form", "upload=@" + filePath,
	}, strings.NewReader(""), stdout, stderr)
	if code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestAPICommandStreamsMultipartStdinWithChunkedTransfer(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.ContentLength != -1 || len(request.TransferEncoding) != 1 || request.TransferEncoding[0] != "chunked" {
			t.Fatalf("ContentLength=%d TransferEncoding=%v", request.ContentLength, request.TransferEncoding)
		}
		if err := request.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		file, header, err := request.FormFile("upload")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		body, err := io.ReadAll(file)
		if err != nil {
			t.Fatal(err)
		}
		if header.Filename != "stdin" || string(body) != "stdin body" {
			t.Fatalf("filename=%q body=%q", header.Filename, body)
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "api", "rest/api/2/attachments", "--form", "upload=@-",
	}, strings.NewReader("stdin body"), stdout, stderr)
	if code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestAPICommandHeaderDefaultsRemovalProtectionAndRepeatedValues(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Accept"); got != "" {
			t.Fatalf("Accept = %q", got)
		}
		if got := request.Header.Values("X-Value"); strings.Join(got, ",") != "first,last" {
			t.Fatalf("X-Value = %q", got)
		}
		if got := request.Header.Values("X-Reset"); strings.Join(got, ",") != "after" {
			t.Fatalf("X-Reset = %q", got)
		}
		if got := request.Header.Get("Accept-Encoding"); got != "gzip" {
			t.Fatalf("Accept-Encoding = %q", got)
		}
		writer.Header().Set("Content-Encoding", "gzip")
		_, _ = writer.Write([]byte{0x1f, 0x8b, 0x00, 0x01})
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "api", "rest/api/2/example",
		"-H", "Accept:", "-H", "x-value: first", "-H", "X-Value: last",
		"-H", "X-Reset: before", "-H", "x-reset:", "-H", "X-Reset: after", "-H", "Accept-Encoding: gzip",
	}, strings.NewReader(""), stdout, stderr)
	if code != 0 || !bytes.Equal(stdout.Bytes(), []byte{0x1f, 0x8b, 0x00, 0x01}) || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%v stderr=%q", code, stdout.Bytes(), stderr.String())
	}

	for _, header := range []string{"Authorization: x", "proxy-authorization: x", "Host: other", "Content-Length: 1", "user-agent: x"} {
		stdout.Reset()
		stderr.Reset()
		code = Execute([]string{"--config", writeCLIConfig(t, server.URL, false), "api", "rest/api/2/example", "-H", header}, strings.NewReader(""), stdout, stderr)
		if code != 2 || stdout.Len() != 0 {
			t.Fatalf("header=%q code=%d stdout=%q stderr=%q", header, code, stdout.String(), stderr.String())
		}
	}
	for _, header := range []string{"Content-Type:", "Content-Type: application/json"} {
		stdout.Reset()
		stderr.Reset()
		code = Execute([]string{
			"--config", writeCLIConfig(t, server.URL, false), "api", "rest/api/2/example", "--form", "a=b", "-H", header,
		}, strings.NewReader(""), stdout, stderr)
		if code != 2 || stdout.Len() != 0 {
			t.Fatalf("header=%q code=%d stdout=%q stderr=%q", header, code, stdout.String(), stderr.String())
		}
	}
}

func TestAPICommandIncludeQuietAndCookies(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Add("Z-Header", "second")
		writer.Header().Add("A-Header", "first")
		writer.Header().Add("Set-Cookie", "secret=one")
		writer.Header().Add("Set-Cookie", "secret=two")
		_, _ = io.WriteString(writer, "body")
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "--quiet", "api", "rest/api/2/example", "--include",
	}, strings.NewReader(""), stdout, stderr)
	if code != 0 || stderr.Len() != 0 || strings.Contains(stdout.String(), "body") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	output := stdout.String()
	if !strings.HasPrefix(output, "HTTP/1.1 200 OK\r\n") || strings.Index(output, "A-Header:") > strings.Index(output, "Z-Header:") {
		t.Fatalf("included headers = %q", output)
	}
	if strings.Count(output, "Set-Cookie: secret=") != 2 || !strings.Contains(output, "secret=one") || !strings.Contains(output, "secret=two") {
		t.Fatalf("cookie output = %q", output)
	}
}

func TestAPICommandIncludeWritesFailureMetadataAndBodyToStderr(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("X-Error", "value")
		writer.Header().Set("Set-Cookie", "secret=value")
		writer.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(writer, "missing")
	}))
	defer server.Close()
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "--quiet", "api", "rest/api/2/missing", "--include",
	}, strings.NewReader(""), stdout, stderr)
	if code != 4 || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.HasPrefix(stderr.String(), "HTTP/1.1 404 Not Found\r\n") ||
		!strings.Contains(stderr.String(), "Set-Cookie: secret=value\r\n") ||
		!strings.HasSuffix(stderr.String(), "\r\nmissing") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestAPICommandRedirectPolicy(t *testing.T) {
	clearCommandEnv(t)
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		targetCalls.Add(1)
		_, _ = io.WriteString(writer, "target")
	}))
	defer target.Close()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/same":
			http.Redirect(writer, request, "/final", http.StatusFound)
		case "/final":
			_, _ = io.WriteString(writer, "final")
		case "/cross":
			http.Redirect(writer, request, target.URL, http.StatusFound)
		case "/body":
			http.Redirect(writer, request, "/final", http.StatusTemporaryRedirect)
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	configPath := writeCLIConfig(t, server.URL, false)
	for _, test := range []struct {
		endpoint string
		args     []string
	}{
		{endpoint: "same"},
		{endpoint: "cross"},
		{endpoint: "body", args: []string{"-f", "a=b"}},
	} {
		stdout.Reset()
		stderr.Reset()
		args := []string{"--config", configPath, "api", test.endpoint}
		args = append(args, test.args...)
		code := Execute(args, strings.NewReader(""), stdout, stderr)
		if code != 5 || stdout.Len() != 0 || targetCalls.Load() != 0 ||
			(!strings.Contains(stderr.String(), "Found") && !strings.Contains(stderr.String(), "Temporary Redirect")) {
			t.Fatalf("endpoint=%s code=%d calls=%d stdout=%q stderr=%q", test.endpoint, code, targetCalls.Load(), stdout.String(), stderr.String())
		}
	}
}

func TestAPICommandUsesStandardAutomaticGzipDecompression(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Accept-Encoding") != "gzip" {
			t.Fatalf("Accept-Encoding = %q", request.Header.Get("Accept-Encoding"))
		}
		writer.Header().Set("Content-Encoding", "gzip")
		compressed := gzip.NewWriter(writer)
		_, _ = io.WriteString(compressed, "decompressed")
		_ = compressed.Close()
	}))
	defer server.Close()
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "api", "rest/api/2/example", "--include",
	}, strings.NewReader(""), stdout, stderr)
	if code != 0 || stderr.Len() != 0 || !strings.HasSuffix(stdout.String(), "\r\ndecompressed") || strings.Contains(stdout.String(), "Content-Encoding") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestAPICommandTimeoutZeroAndNegative(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		time.Sleep(50 * time.Millisecond)
		_, _ = io.WriteString(writer, "done")
	}))
	defer server.Close()
	configPath := writeCLIConfig(t, server.URL, false)

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{"--config", configPath, "api", "slow", "--timeout", "10ms"}, strings.NewReader(""), stdout, stderr)
	if code != 5 || !strings.Contains(stderr.String(), "deadline") {
		t.Fatalf("timeout code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"--config", configPath, "api", "slow", "--timeout", "0"}, strings.NewReader(""), stdout, stderr)
	if code != 0 || stdout.String() != "done" || stderr.Len() != 0 {
		t.Fatalf("unlimited code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"--config", configPath, "api", "slow", "--timeout", "-1s"}, strings.NewReader(""), stdout, stderr)
	if code != 2 || stdout.Len() != 0 {
		t.Fatalf("negative code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestAPICommandBrokenPipeIsSilentSuccess(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, "body")
	}))
	defer server.Close()

	stderr := new(bytes.Buffer)
	a := &app{stdin: strings.NewReader(""), stdout: brokenPipeWriter{}, stderr: stderr}
	code := a.execute([]string{"--config", writeCLIConfig(t, server.URL, false), "api", "rest/api/2/example"})
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestAPICommandSignalCancellationPreservesPartialOutput(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(writer, "partial")
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		<-request.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := &app{stdin: strings.NewReader(""), stdout: stdout, stderr: stderr}
	code := a.executeContext(ctx, []string{
		"--config", writeCLIConfig(t, server.URL, false), "api", "rest/api/2/stream", "--timeout", "0",
	}, func() int { return 143 })
	if code != 143 || stdout.String() != "partial" || stderr.String() != "request canceled\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestAPICommandSignalCancellationUnblocksTypedFieldStdin(t *testing.T) {
	clearCommandEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	stdin := &blockingReadCloser{closed: make(chan struct{})}
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := &app{stdin: stdin, stdout: stdout, stderr: stderr}
	code := a.executeContext(ctx, []string{
		"--config", writeCLIConfig(t, "https://jira.invalid", false), "api", "rest/api/2/example", "-F", "value=@-",
	}, func() int { return 130 })
	if code != 130 || stdout.Len() != 0 || stderr.String() != "request canceled\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestAPICommandSignalCancellationUnblocksInputStdin(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	stdin := &blockingReadCloser{closed: make(chan struct{})}
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := &app{stdin: stdin, stdout: stdout, stderr: stderr}
	code := a.executeContext(ctx, []string{
		"--config", writeCLIConfig(t, server.URL, false), "api", "rest/api/2/import", "--input", "-", "--timeout", "0",
	}, func() int { return 143 })
	if code != 143 || stdout.Len() != 0 || stderr.String() != "request canceled\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestAPICommandEmptyInputFileUsesKnownZeroLength(t *testing.T) {
	clearCommandEnv(t)
	inputPath := filepath.Join(t.TempDir(), "empty.json")
	if err := os.WriteFile(inputPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.ContentLength != 0 || len(request.TransferEncoding) != 0 {
			t.Fatalf("method=%q ContentLength=%d TransferEncoding=%v", request.Method, request.ContentLength, request.TransferEncoding)
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "api", "rest/api/2/import", "--input", inputPath,
	}, strings.NewReader(""), stdout, stderr)
	if code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestAPICommandFieldSourcesAndStdinConflicts(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("network must not be reached")
	}))
	defer server.Close()
	configPath := writeCLIConfig(t, server.URL, false)

	tests := [][]string{
		{"api", "rest/api/2/example", "--input", "-", "-F", "field=@-"},
		{"api", "rest/api/2/example", "--form", "first=@-", "--form", "second=@-"},
		{"api", "rest/api/2/example", "--form", "a=b", "-f", "c=d"},
		{"api", "rest/api/2/example", "--method="},
		{"api", "rest/api/2/example", "--input="},
	}
	for _, args := range tests {
		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		code := Execute(append([]string{"--config", configPath}, args...), strings.NewReader("input"), stdout, stderr)
		if code != 2 || stdout.Len() != 0 {
			t.Fatalf("args=%v code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
	}

	largePath := filepath.Join(t.TempDir(), "large.txt")
	large := bytes.Repeat([]byte("x"), (16<<20)+1)
	if err := os.WriteFile(largePath, large, 0o600); err != nil {
		t.Fatal(err)
	}
	largeServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body["large"]) != len(large) {
			t.Fatalf("large field length = %d, want %d", len(body["large"]), len(large))
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer largeServer.Close()
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{"--config", writeCLIConfig(t, largeServer.URL, false), "api", "rest/api/2/example", "-F", "large=@" + largePath}, strings.NewReader(""), stdout, stderr)
	if code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("large code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestAPICommandAllowsArbitraryMethodsAndBodiesOnReadMethods(t *testing.T) {
	clearCommandEnv(t)
	tests := []struct {
		method, body string
	}{
		{method: "propfind", body: "custom"},
		{method: "GET", body: "get-body"},
		{method: "HEAD", body: "head-body"},
		{method: "OPTIONS", body: "options-body"},
	}
	for _, test := range tests {
		t.Run(test.method, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Fatal(err)
				}
				if request.Method != strings.ToUpper(test.method) || string(body) != test.body {
					t.Fatalf("method=%q body=%q", request.Method, body)
				}
				writer.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
			code := Execute([]string{
				"--config", writeCLIConfig(t, server.URL, false), "api", "rest/api/2/example", "--method", test.method, "--input", "-",
			}, strings.NewReader(test.body), stdout, stderr)
			if code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
		})
	}
}

type brokenPipeWriter struct{}

func (brokenPipeWriter) Write([]byte) (int, error) { return 0, syscall.EPIPE }

type blockingReadCloser struct {
	closed chan struct{}
	once   sync.Once
}

func (r *blockingReadCloser) Read([]byte) (int, error) {
	<-r.closed
	return 0, io.ErrClosedPipe
}

func (r *blockingReadCloser) Close() error {
	r.once.Do(func() { close(r.closed) })
	return nil
}
