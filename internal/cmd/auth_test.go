package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/timonwong/jiro/internal/config"
)

type terminalStep struct {
	kind         string // "prompt", "select", "confirm", or "secret"
	label        string
	defaultValue string
	value        string
	// rejected is offered to the prompt validator first and must be refused.
	rejected string
	options  []string
}

type fakeLoginTerminal struct {
	steps    []terminalStep
	index    int
	rejected []string
}

func (f *fakeLoginTerminal) IsTerminal() bool { return true }

func (f *fakeLoginTerminal) next(kind, label, defaultValue string) (terminalStep, error) {
	if f.index >= len(f.steps) {
		return terminalStep{}, fmt.Errorf("unexpected %s %q", kind, label)
	}
	want := f.steps[f.index]
	f.index++
	if kind != want.kind || label != want.label || defaultValue != want.defaultValue {
		return terminalStep{}, fmt.Errorf("%s %q default %q, want %s %q default %q", kind, label, defaultValue, want.kind, want.label, want.defaultValue)
	}
	return want, nil
}

func (f *fakeLoginTerminal) Prompt(label, defaultValue string, validate func(string) error) (string, error) {
	want, err := f.next("prompt", label, defaultValue)
	if err != nil {
		return "", err
	}
	if want.rejected != "" {
		if validate == nil || validate(want.rejected) == nil {
			return "", fmt.Errorf("prompt %q accepted %q", label, want.rejected)
		}
		f.rejected = append(f.rejected, want.rejected)
	}
	value := want.value
	if value == "" {
		value = defaultValue
	}
	if validate != nil {
		if err := validate(value); err != nil {
			return "", err
		}
	}
	return value, nil
}

func (f *fakeLoginTerminal) PromptSecret(label string) (string, error) {
	want, err := f.next("secret", label, "")
	if err != nil {
		return "", err
	}
	return want.value, nil
}

func (f *fakeLoginTerminal) Select(label string, options []loginOption, defaultValue string) (string, error) {
	want, err := f.next("select", label, defaultValue)
	if err != nil {
		return "", err
	}
	values := make([]string, 0, len(options))
	for _, option := range options {
		values = append(values, option.Value)
	}
	if want.options != nil && strings.Join(values, ",") != strings.Join(want.options, ",") {
		return "", fmt.Errorf("select %q options = %v, want %v", label, values, want.options)
	}
	if want.value == "" {
		return defaultValue, nil
	}
	return want.value, nil
}

func (f *fakeLoginTerminal) Confirm(label string, defaultValue bool) (bool, error) {
	want, err := f.next("confirm", label, strconv.FormatBool(defaultValue))
	if err != nil {
		return false, err
	}
	if want.value == "" {
		return defaultValue, nil
	}
	return strconv.ParseBool(want.value)
}

type memorySecretStore struct {
	secrets     map[string]string
	setCalls    int
	deleteCalls int
}

func newMemorySecretStore() *memorySecretStore {
	return &memorySecretStore{secrets: map[string]string{}}
}

func (m *memorySecretStore) Get(service, account string) (string, error) {
	value, ok := m.secrets[service+"/"+account]
	if !ok {
		return "", config.ErrSecretNotFound
	}
	return value, nil
}

func (m *memorySecretStore) Set(service, account, secret string) error {
	m.setCalls++
	m.secrets[service+"/"+account] = secret
	return nil
}

func (m *memorySecretStore) Delete(service, account string) error {
	m.deleteCalls++
	key := service + "/" + account
	if _, ok := m.secrets[key]; !ok {
		return config.ErrSecretNotFound
	}
	delete(m.secrets, key)
	return nil
}

func TestLoginInteractiveBasicAndJSON(t *testing.T) {
	clearCommandEnv(t)
	server := authenticatedServer(t, config.AuthBasic, "alice", "fresh-password", http.StatusOK)
	defer server.Close()

	path := filepath.Join(t.TempDir(), "config.toml")
	terminal := &fakeLoginTerminal{steps: []terminalStep{
		{kind: "prompt", label: "Host", value: server.URL},
		{kind: "select", label: "Authentication", defaultValue: "pat", value: "basic", options: []string{"pat", "basic"}},
		{kind: "prompt", label: "Username", value: "alice"},
		{kind: "secret", label: "Password", value: "fresh-password"},
	}}
	store := newMemorySecretStore()
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := &app{stdin: strings.NewReader(""), stdout: stdout, stderr: stderr, terminal: terminal, secretStore: store}
	if code := a.execute([]string{"--config", path, "-ojson", "auth", "login"}); code != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var envelope struct {
		SchemaVersion string      `json:"schemaVersion"`
		Data          loginResult `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.SchemaVersion != "1" || envelope.Data.Profile != "default" || envelope.Data.CredentialStore != "keyring" || envelope.Data.User.Username != "alice" {
		t.Fatalf("result = %+v", envelope)
	}
	if store.setCalls != 1 || !storeContains(store, "fresh-password") {
		t.Fatalf("secret store = %+v", store)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("fresh-password")) {
		t.Fatalf("keyring login wrote plaintext credential: %s", data)
	}
}

func TestLoginInteractivePATUsesExistingDefaultsAndFreshSecret(t *testing.T) {
	clearCommandEnv(t)
	server := authenticatedServer(t, config.AuthPAT, "", "fresh-token", http.StatusOK)
	defer server.Close()
	path := writeAuthConfig(t, fmt.Sprintf("[default]\nhost = %q\nauth_type = \"pat\"\nuse_keyring = false\ntoken = \"old-token\"\n", server.URL))
	terminal := &fakeLoginTerminal{steps: []terminalStep{
		{kind: "prompt", label: "Host", defaultValue: server.URL},
		{kind: "select", label: "Authentication", defaultValue: "pat"},
		{kind: "secret", label: "Token", value: "fresh-token"},
	}}
	store := newMemorySecretStore()
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := &app{stdin: strings.NewReader(""), stdout: stdout, stderr: stderr, terminal: terminal, secretStore: store}
	if code := a.execute([]string{"--config", path, "--quiet", "auth", "login"}); code != 0 || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !storeContains(store, "fresh-token") || storeContains(store, "old-token") {
		t.Fatalf("fresh credential not stored: %+v", store)
	}
}

func TestLoginAuthenticationMenuDefaults(t *testing.T) {
	t.Run("new profile preselects PAT", func(t *testing.T) {
		clearCommandEnv(t)
		server := authenticatedServer(t, config.AuthPAT, "", "fresh-token", http.StatusOK)
		defer server.Close()
		path := writeAuthConfig(t, fmt.Sprintf("[default]\nhost = %q\nauth_type = \"basic\"\nusername = \"alice\"\n", server.URL))
		terminal := &fakeLoginTerminal{steps: []terminalStep{
			{kind: "prompt", label: "Host", defaultValue: server.URL},
			{kind: "select", label: "Authentication", defaultValue: "pat", options: []string{"pat", "basic"}},
			{kind: "secret", label: "Token", value: "fresh-token"},
		}}
		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		a := &app{stdin: strings.NewReader(""), stdout: stdout, stderr: stderr, terminal: terminal, secretStore: newMemorySecretStore()}
		if code := a.execute([]string{"--config", path, "--profile", "new", "auth", "login"}); code != 0 || terminal.index != len(terminal.steps) {
			t.Fatalf("code=%d steps=%d stdout=%s stderr=%s", code, terminal.index, stdout.String(), stderr.String())
		}
	})

	t.Run("existing basic profile preselects basic and its username", func(t *testing.T) {
		clearCommandEnv(t)
		server := authenticatedServer(t, config.AuthBasic, "alice", "fresh-password", http.StatusOK)
		defer server.Close()
		path := writeAuthConfig(t, fmt.Sprintf("[default]\nhost = %q\nauth_type = \"basic\"\nusername = \"alice\"\n", server.URL))
		terminal := &fakeLoginTerminal{steps: []terminalStep{
			{kind: "prompt", label: "Host", defaultValue: server.URL},
			{kind: "select", label: "Authentication", defaultValue: "basic"},
			{kind: "prompt", label: "Username", defaultValue: "alice"},
			{kind: "secret", label: "Password", value: "fresh-password"},
		}}
		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		a := &app{stdin: strings.NewReader(""), stdout: stdout, stderr: stderr, terminal: terminal, secretStore: newMemorySecretStore()}
		if code := a.execute([]string{"--config", path, "auth", "login"}); code != 0 || terminal.index != len(terminal.steps) {
			t.Fatalf("code=%d steps=%d stdout=%s stderr=%s", code, terminal.index, stdout.String(), stderr.String())
		}
	})
}

func TestLoginPromptsRejectInvalidInputInPlace(t *testing.T) {
	clearCommandEnv(t)
	server := authenticatedServer(t, config.AuthBasic, "alice", "fresh-password", http.StatusOK)
	defer server.Close()
	path := filepath.Join(t.TempDir(), "config.toml")
	terminal := &fakeLoginTerminal{steps: []terminalStep{
		{kind: "prompt", label: "Host", rejected: "ftp://jira.example", value: server.URL},
		{kind: "select", label: "Authentication", defaultValue: "pat", value: "basic"},
		{kind: "prompt", label: "Username", rejected: "   ", value: "alice"},
		{kind: "secret", label: "Password", value: "fresh-password"},
	}}
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := &app{stdin: strings.NewReader(""), stdout: stdout, stderr: stderr, terminal: terminal, secretStore: newMemorySecretStore()}
	if code := a.execute([]string{"--config", path, "auth", "login"}); code != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if len(terminal.rejected) != 2 {
		t.Fatalf("rejected = %q", terminal.rejected)
	}
}

func TestLoginEmptySecretIsRejected(t *testing.T) {
	clearCommandEnv(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	terminal := &fakeLoginTerminal{steps: []terminalStep{
		{kind: "prompt", label: "Host", value: "https://jira.example"},
		{kind: "select", label: "Authentication", defaultValue: "pat"},
		{kind: "secret", label: "Token"},
	}}
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := &app{stdin: strings.NewReader(""), stdout: stdout, stderr: stderr, terminal: terminal, secretStore: newMemorySecretStore()}
	if code := a.execute([]string{"--config", path, "auth", "login"}); code != 2 || !strings.Contains(stderr.String(), "token must not be empty") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("failed login wrote config: %v", err)
	}
}

// unavailableSecretStore mimics go-keyring on a Linux host without a Secret
// Service provider.
type unavailableSecretStore struct{ calls int }

func (s *unavailableSecretStore) Get(string, string) (string, error) {
	s.calls++
	return "", fmt.Errorf(`exec: "dbus-launch": executable file not found in $PATH`)
}

func (s *unavailableSecretStore) Set(string, string, string) error {
	s.calls++
	return fmt.Errorf("keyring written")
}

func (s *unavailableSecretStore) Delete(string, string) error {
	s.calls++
	return fmt.Errorf("keyring deleted")
}

func TestLoginKeyringUnavailable(t *testing.T) {
	t.Run("interactive confirmation stores the credential in config", func(t *testing.T) {
		clearCommandEnv(t)
		server := authenticatedServer(t, config.AuthPAT, "", "fresh-token", http.StatusOK)
		defer server.Close()
		path := filepath.Join(t.TempDir(), "config.toml")
		terminal := &fakeLoginTerminal{steps: []terminalStep{
			{kind: "confirm", label: "Store the credential in the config file (mode 0600) instead?", defaultValue: "true"},
			{kind: "prompt", label: "Host", value: server.URL},
			{kind: "select", label: "Authentication", defaultValue: "pat"},
			{kind: "secret", label: "Token", value: "fresh-token"},
		}}
		store := &unavailableSecretStore{}
		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		a := &app{stdin: strings.NewReader(""), stdout: stdout, stderr: stderr, terminal: terminal, secretStore: store}
		if code := a.execute([]string{"--config", path, "-ojson", "auth", "login"}); code != 0 {
			t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
		}
		if !strings.Contains(stderr.String(), "OS keyring is unavailable") || !strings.Contains(stderr.String(), "dbus-launch") {
			t.Fatalf("stderr=%s", stderr.String())
		}
		if !strings.Contains(stdout.String(), `"credentialStore":"config"`) {
			t.Fatalf("stdout=%s", stdout.String())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(data, []byte(`token = "fresh-token"`)) || !bytes.Contains(data, []byte("use_keyring = false")) {
			t.Fatalf("config = %s", data)
		}
		assertConfigMode0600(t, path)
		if store.calls != 1 {
			t.Fatalf("keyring calls = %d, want the single probe", store.calls)
		}
	})

	t.Run("declining the fallback stops before any other prompt", func(t *testing.T) {
		clearCommandEnv(t)
		path := filepath.Join(t.TempDir(), "config.toml")
		terminal := &fakeLoginTerminal{steps: []terminalStep{
			{kind: "confirm", label: "Store the credential in the config file (mode 0600) instead?", defaultValue: "true", value: "false"},
		}}
		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		a := &app{stdin: strings.NewReader(""), stdout: stdout, stderr: stderr, terminal: terminal, secretStore: &unavailableSecretStore{}}
		if code := a.execute([]string{"--config", path, "auth", "login"}); code != 2 || !strings.Contains(stderr.String(), "--use-keyring=false") {
			t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("declined login wrote config: %v", err)
		}
	})

	t.Run("explicit keyring request fails without prompting", func(t *testing.T) {
		clearCommandEnv(t)
		path := filepath.Join(t.TempDir(), "config.toml")
		terminal := &fakeLoginTerminal{}
		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		a := &app{stdin: strings.NewReader(""), stdout: stdout, stderr: stderr, terminal: terminal, secretStore: &unavailableSecretStore{}}
		if code := a.execute([]string{"--config", path, "auth", "login", "--use-keyring"}); code != 2 || !strings.Contains(stderr.String(), "OS keyring is unavailable") {
			t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
		}
		if terminal.index != 0 {
			t.Fatalf("prompted %d times", terminal.index)
		}
	})

	t.Run("non-TTY login fails before contacting Jira", func(t *testing.T) {
		clearCommandEnv(t)
		var calls int
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
		defer server.Close()
		t.Setenv("JIRA_HOST", server.URL)
		t.Setenv("JIRA_TOKEN", "env-token")
		path := filepath.Join(t.TempDir(), "config.toml")
		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		a := &app{stdin: strings.NewReader(""), stdout: stdout, stderr: stderr, secretStore: &unavailableSecretStore{}}
		if code := a.execute([]string{"--config", path, "auth", "login"}); code != 2 || !strings.Contains(stderr.String(), "--use-keyring=false") || calls != 0 {
			t.Fatalf("code=%d calls=%d stdout=%s stderr=%s", code, calls, stdout.String(), stderr.String())
		}
	})

	t.Run("config storage never probes the keyring", func(t *testing.T) {
		clearCommandEnv(t)
		server := authenticatedServer(t, config.AuthPAT, "", "env-token", http.StatusOK)
		defer server.Close()
		t.Setenv("JIRA_HOST", server.URL)
		t.Setenv("JIRA_TOKEN", "env-token")
		path := filepath.Join(t.TempDir(), "config.toml")
		store := &unavailableSecretStore{}
		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		a := &app{stdin: strings.NewReader(""), stdout: stdout, stderr: stderr, secretStore: store}
		if code := a.execute([]string{"--config", path, "auth", "login", "--use-keyring=false"}); code != 0 || store.calls != 0 {
			t.Fatalf("code=%d calls=%d stdout=%s stderr=%s", code, store.calls, stdout.String(), stderr.String())
		}
	})
}

func assertConfigMode0600(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("config mode = %v", mode)
	}
}

func TestLoginNonTTYEnvironmentAndStdin(t *testing.T) {
	t.Run("environment password wins", func(t *testing.T) {
		clearCommandEnv(t)
		server := authenticatedServer(t, config.AuthBasic, "alice", "env-password", http.StatusOK)
		defer server.Close()
		t.Setenv("JIRA_HOST", server.URL)
		t.Setenv("JIRA_USERNAME", "alice")
		t.Setenv("JIRA_PASSWORD", "env-password")
		path := filepath.Join(t.TempDir(), "config.toml")
		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		code := Execute([]string{"--config", path, "-ojson", "auth", "login", "--use-keyring=false"}, strings.NewReader("stdin-password"), stdout, stderr)
		if code != 0 || stderr.Len() != 0 {
			t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
		}
		if !strings.Contains(stdout.String(), `"credentialStore":"config"`) {
			t.Fatalf("stdout=%s", stdout.String())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(data, []byte("env-password")) || bytes.Contains(data, []byte("stdin-password")) {
			t.Fatalf("config secret = %s", data)
		}
	})

	t.Run("PAT token from stdin", func(t *testing.T) {
		clearCommandEnv(t)
		server := authenticatedServer(t, config.AuthPAT, "", "stdin-token", http.StatusOK)
		defer server.Close()
		t.Setenv("JIRA_HOST", server.URL)
		path := filepath.Join(t.TempDir(), "config.toml")
		store := newMemorySecretStore()
		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		a := &app{stdin: strings.NewReader("stdin-token\r\n"), stdout: stdout, stderr: stderr, secretStore: store}
		code := a.execute([]string{"--config", path, "auth", "login", "--token-stdin"})
		if code != 0 || stderr.Len() != 0 || !storeContains(store, "stdin-token") {
			t.Fatalf("code=%d store=%+v stdout=%s stderr=%s", code, store, stdout.String(), stderr.String())
		}
	})

	t.Run("password from stdin requires environment username", func(t *testing.T) {
		clearCommandEnv(t)
		server := authenticatedServer(t, config.AuthBasic, "alice", "stdin-password", http.StatusOK)
		defer server.Close()
		t.Setenv("JIRA_HOST", server.URL)
		t.Setenv("JIRA_USERNAME", "alice")
		store := newMemorySecretStore()
		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		a := &app{stdin: strings.NewReader("stdin-password\n"), stdout: stdout, stderr: stderr, secretStore: store}
		code := a.execute([]string{"--config", filepath.Join(t.TempDir(), "config.toml"), "auth", "login", "--password-stdin"})
		if code != 0 || stderr.Len() != 0 || !storeContains(store, "stdin-password") {
			t.Fatalf("code=%d store=%+v stdout=%s stderr=%s", code, store, stdout.String(), stderr.String())
		}
	})

	t.Run("stdin is not consumed without an explicit stdin flag", func(t *testing.T) {
		clearCommandEnv(t)
		path := writeAuthConfig(t, "[default]\nhost = \"https://jira.example\"\nauth_type = \"pat\"\n")
		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		code := Execute([]string{"--config", path, "auth", "login"}, strings.NewReader("implicit-token"), stdout, stderr)
		if code != 2 || !strings.Contains(stderr.String(), "non-TTY login requires") {
			t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
		}
	})

	for _, test := range []struct {
		name string
		args []string
		env  map[string]string
		want string
	}{
		{name: "stdin modes are mutually exclusive", args: []string{"auth", "login", "--password-stdin", "--token-stdin"}, env: map[string]string{"JIRA_USERNAME": "alice"}, want: "mutually exclusive"},
		{name: "stdin mode conflicts with environment token", args: []string{"auth", "login", "--password-stdin"}, env: map[string]string{"JIRA_USERNAME": "alice", "JIRA_TOKEN": "environment-token"}, want: "conflicts"},
		{name: "stdin mode conflicts with environment password", args: []string{"auth", "login", "--token-stdin"}, env: map[string]string{"JIRA_PASSWORD": "environment-password"}, want: "conflicts"},
	} {
		t.Run(test.name, func(t *testing.T) {
			clearCommandEnv(t)
			for name, value := range test.env {
				t.Setenv(name, value)
			}
			stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
			code := Execute(test.args, strings.NewReader("secret"), stdout, stderr)
			if code != 2 || !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestLoginTTYEnvironmentCredentialBypassesCredentialPromptsAndUsesUserAgent(t *testing.T) {
	clearCommandEnv(t)
	var gotUserAgent string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		gotUserAgent = request.Header.Get("User-Agent")
		username, password, ok := request.BasicAuth()
		if !ok || username != "alice" || password != "environment-password" {
			t.Errorf("BasicAuth = %q/%q/%t", username, password, ok)
		}
		_, _ = io.WriteString(writer, `{"name":"alice","displayName":"Alice","active":true}`)
	}))
	defer server.Close()
	t.Setenv("JIRA_HOST", server.URL)
	t.Setenv("JIRA_USERNAME", "alice")
	t.Setenv("JIRA_PASSWORD", "environment-password")
	t.Setenv("JIRA_USER_AGENT", "runtime-agent")

	path := filepath.Join(t.TempDir(), "config.toml")
	terminal := &fakeLoginTerminal{steps: []terminalStep{{kind: "prompt", label: "Host", defaultValue: server.URL}}}
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := &app{stdin: strings.NewReader(""), stdout: stdout, stderr: stderr, terminal: terminal, secretStore: newMemorySecretStore()}
	if code := a.execute([]string{"--config", path, "auth", "login", "--use-keyring=false"}); code != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if terminal.index != 1 || gotUserAgent != "runtime-agent" {
		t.Fatalf("prompts=%d User-Agent=%q", terminal.index, gotUserAgent)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("runtime-agent")) {
		t.Fatalf("runtime User-Agent was persisted: %s", data)
	}
}

func TestLoginMissingInputAndUnauthorizedDoNotCommit(t *testing.T) {
	clearCommandEnv(t)
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls++
		writer.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	t.Run("missing username", func(t *testing.T) {
		clearCommandEnv(t)
		t.Setenv("JIRA_HOST", server.URL)
		path := filepath.Join(t.TempDir(), "config.toml")
		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		code := Execute([]string{"--config", path, "auth", "login", "--password-stdin"}, strings.NewReader("secret"), stdout, stderr)
		if code != 2 || !strings.Contains(stderr.String(), "username is required") || calls != 0 {
			t.Fatalf("code=%d calls=%d stdout=%s stderr=%s", code, calls, stdout.String(), stderr.String())
		}
	})

	t.Run("new profile missing explicit auth", func(t *testing.T) {
		clearCommandEnv(t)
		path := filepath.Join(t.TempDir(), "config.toml")
		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		code := Execute([]string{"--config", path, "auth", "login"}, strings.NewReader("secret"), stdout, stderr)
		if code != 2 || !strings.Contains(stderr.String(), "non-TTY login requires") || calls != 0 {
			t.Fatalf("code=%d calls=%d stdout=%s stderr=%s", code, calls, stdout.String(), stderr.String())
		}
	})

	t.Run("unsupported API version", func(t *testing.T) {
		clearCommandEnv(t)
		path := writeAuthConfig(t, fmt.Sprintf("[default]\nhost = %q\nauth_type = \"pat\"\napi_version = 3\n", server.URL))
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		code := Execute([]string{"--config", path, "auth", "login", "--token-stdin"}, strings.NewReader("secret"), stdout, stderr)
		if code != 2 || !strings.Contains(stderr.String(), "REST API version 2") || calls != 0 {
			t.Fatalf("code=%d calls=%d stdout=%s stderr=%s", code, calls, stdout.String(), stderr.String())
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Fatal("unsupported API version changed config")
		}
	})

	t.Run("401", func(t *testing.T) {
		clearCommandEnv(t)
		path := writeAuthConfig(t, fmt.Sprintf("[default]\nhost = %q\nauth_type = \"pat\"\nuse_keyring = true\n", server.URL))
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		store := newMemorySecretStore()
		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		a := &app{stdin: strings.NewReader("bad-token"), stdout: stdout, stderr: stderr, secretStore: store}
		code := a.execute([]string{"--config", path, "-ojson", "auth", "login", "--token-stdin"})
		if code != 3 || !strings.Contains(stderr.String(), `"kind":"auth"`) || store.setCalls != 0 {
			t.Fatalf("code=%d store=%+v stdout=%s stderr=%s", code, store, stdout.String(), stderr.String())
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Fatal("unauthorized login changed config")
		}
	})
}

func TestLoginLogoutAndLogoutIdempotence(t *testing.T) {
	clearCommandEnv(t)
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	server := authenticatedServer(t, config.AuthPAT, "", "token", http.StatusOK)
	defer server.Close()
	path := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("JIRA_HOST", server.URL)
	store := newMemorySecretStore()
	stdout.Reset()
	stderr.Reset()
	a := &app{stdin: strings.NewReader("token"), stdout: stdout, stderr: stderr, secretStore: store}
	if code := a.execute([]string{"--config", path, "auth", "login", "--token-stdin"}); code != 0 {
		t.Fatalf("login code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	t.Setenv("JIRA_TOKEN", "environment-token")

	for index, wantRemoved := range []bool{true, false} {
		stdout.Reset()
		stderr.Reset()
		a = &app{stdin: strings.NewReader(""), stdout: stdout, stderr: stderr, secretStore: store}
		code := a.execute([]string{"--config", path, "-ojson", "auth", "logout"})
		if code != 0 || stderr.Len() != 0 {
			t.Fatalf("logout %d code=%d stdout=%s stderr=%s", index, code, stdout.String(), stderr.String())
		}
		var envelope struct {
			Data logoutResult `json:"data"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Data.Profile != "default" || envelope.Data.CredentialStore != "keyring" || envelope.Data.CredentialRemoved != wantRemoved || !envelope.Data.EnvironmentCredentialActive {
			t.Fatalf("logout %d = %+v", index, envelope.Data)
		}
	}

}

func TestLogoutMissingProfileIsConfigError(t *testing.T) {
	clearCommandEnv(t)
	path := writeAuthConfig(t, "[default]\nhost = \"https://jira.example\"\nauth_type = \"pat\"\nuse_keyring = true\n")
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := &app{stdin: strings.NewReader(""), stdout: stdout, stderr: stderr, secretStore: newMemorySecretStore()}
	if code := a.execute([]string{"--config", path, "--profile", "missing", "-ojson", "auth", "logout"}); code != 2 || !strings.Contains(stderr.String(), `"kind":"config"`) {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestAuthStatusTextJSONQuietAndNamedProfile(t *testing.T) {
	clearCommandEnv(t)
	server := authenticatedServer(t, config.AuthBasic, "alice", "status-password", http.StatusOK)
	defer server.Close()

	path := writeAuthConfig(t, fmt.Sprintf(`[default]
host = "https://default.example"
username = "default-user"
auth_type = "basic"
use_keyring = false
password = "default-password"

[profiles.bot]
host = %q
username = "alice"
auth_type = "basic"
use_keyring = false
password = "status-password"
`, server.URL))
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	args := []string{"--config", path, "--profile", "bot", "auth", "status"}
	if code := Execute(args, strings.NewReader(""), stdout, stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("text code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{"Profile", "bot", "Jira Instance", server.URL, "basic", "Test User (alice)", "true"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("text stdout=%q; missing %q", stdout.String(), want)
		}
	}
	if strings.Contains(stdout.String(), "status-password") {
		t.Fatalf("text status exposed credential: %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	jsonArgs := []string{"--config", path, "--profile", "bot", "-ojson", "auth", "status"}
	if code := Execute(jsonArgs, strings.NewReader(""), stdout, stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("json code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var envelope struct {
		SchemaVersion string           `json:"schemaVersion"`
		Data          authStatusResult `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.SchemaVersion != "1" || envelope.Data.Profile != "bot" || envelope.Data.Instance != server.URL || envelope.Data.AuthType != config.AuthBasic || envelope.Data.User.Username != "alice" || !envelope.Data.User.Active {
		t.Fatalf("status = %+v", envelope)
	}
	if strings.Contains(stdout.String(), "status-password") {
		t.Fatalf("json status exposed credential: %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	quietArgs := []string{"--config", path, "--profile", "bot", "--quiet", "auth", "status"}
	if code := Execute(quietArgs, strings.NewReader(""), stdout, stderr); code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("quiet code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("auth status modified the config")
	}
}

func TestAuthStatusUsesInjectedKeyringStore(t *testing.T) {
	clearCommandEnv(t)
	server := authenticatedServer(t, config.AuthPAT, "", "keyring-token", http.StatusOK)
	defer server.Close()
	path := writeAuthConfig(t, fmt.Sprintf("[default]\nhost = %q\nauth_type = \"pat\"\nuse_keyring = true\n", server.URL))
	store := newMemorySecretStore()
	store.secrets[config.KeyringService+"/"+config.KeyringAccount("")] = "keyring-token"
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := &app{stdin: strings.NewReader(""), stdout: stdout, stderr: stderr, secretStore: store}
	if code := a.execute([]string{"--config", path, "-ojson", "auth", "status"}); code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `"profile":"default"`) || strings.Contains(stdout.String(), "keyring-token") {
		t.Fatalf("stdout=%s", stdout.String())
	}
}

func TestAuthStatusUsesResolvedUserAgent(t *testing.T) {
	clearCommandEnv(t)
	userAgents := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		userAgents <- request.Header.Get("User-Agent")
		_, _ = io.WriteString(writer, `{"name":"alice","displayName":"Alice","active":true}`)
	}))
	defer server.Close()
	path := writeAuthConfig(t, fmt.Sprintf(`[default]
host = %q
username = "alice"
auth_type = "basic"
use_keyring = false
password = "secret"
user_agent = "default-agent"

[profiles.bot]
host = %q
username = "alice"
password = "secret"
`, server.URL, server.URL))

	for _, test := range []struct {
		name, environment, want string
	}{
		{name: "named Profile inherits default", want: "default-agent"},
		{name: "environment overrides Profile", environment: "environment-agent", want: "environment-agent"},
	} {
		t.Run(test.name, func(t *testing.T) {
			clearCommandEnv(t)
			if test.environment != "" {
				t.Setenv("JIRA_USER_AGENT", test.environment)
			}
			stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
			if code := Execute([]string{"--config", path, "--profile", "bot", "auth", "status"}, strings.NewReader(""), stdout, stderr); code != 0 {
				t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			if got := <-userAgents; got != test.want {
				t.Fatalf("User-Agent = %q, want %q", got, test.want)
			}
		})
	}
}

func TestAuthStatusFailures(t *testing.T) {
	t.Run("missing credential", func(t *testing.T) {
		clearCommandEnv(t)
		path := writeAuthConfig(t, "[default]\nhost = \"https://jira.example\"\nauth_type = \"pat\"\nuse_keyring = false\n")
		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		code := Execute([]string{"--config", path, "-ojson", "auth", "status"}, strings.NewReader(""), stdout, stderr)
		if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), `"kind":"config"`) {
			t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
		}
	})

	t.Run("invalid credential", func(t *testing.T) {
		clearCommandEnv(t)
		server := authenticatedServer(t, config.AuthPAT, "", "bad-token", http.StatusUnauthorized)
		defer server.Close()
		path := writeAuthConfig(t, fmt.Sprintf("[default]\nhost = %q\nauth_type = \"pat\"\nuse_keyring = false\ntoken = \"bad-token\"\n", server.URL))
		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		code := Execute([]string{"--config", path, "-ojson", "auth", "status"}, strings.NewReader(""), stdout, stderr)
		if code != 3 || stdout.Len() != 0 || !strings.Contains(stderr.String(), `"kind":"auth"`) {
			t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
		}
	})

	t.Run("network failure", func(t *testing.T) {
		clearCommandEnv(t)
		server := authenticatedServer(t, config.AuthPAT, "", "token", http.StatusOK)
		instance := server.URL
		server.Close()
		path := writeAuthConfig(t, fmt.Sprintf("[default]\nhost = %q\nauth_type = \"pat\"\nuse_keyring = false\ntoken = \"token\"\n", instance))
		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		code := Execute([]string{"--config", path, "-ojson", "auth", "status"}, strings.NewReader(""), stdout, stderr)
		if code != 5 || stdout.Len() != 0 || !strings.Contains(stderr.String(), `"kind":"api_error"`) {
			t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
		}
	})

}

func TestAuthCommandsOnlyAvailableUnderAuth(t *testing.T) {
	clearCommandEnv(t)
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	if code := Execute([]string{"--help"}, strings.NewReader(""), stdout, stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("root help code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "  auth ") || strings.Contains(stdout.String(), "\n  login ") || strings.Contains(stdout.String(), "\n  logout ") || strings.Contains(stdout.String(), "\n  myself ") {
		t.Fatalf("root help exposes wrong auth commands:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Execute([]string{"auth", "--help"}, strings.NewReader(""), stdout, stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("auth help code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	for _, command := range []string{"login", "logout", "status"} {
		if !strings.Contains(stdout.String(), "  "+command+" ") {
			t.Fatalf("auth help missing %q:\n%s", command, stdout.String())
		}
	}

	for _, command := range []string{"login", "logout", "myself"} {
		stdout.Reset()
		stderr.Reset()
		if code := Execute([]string{command}, strings.NewReader(""), stdout, stderr); code != 1 || !strings.Contains(stderr.String(), "unknown command") {
			t.Fatalf("top-level %s code=%d stdout=%s stderr=%s", command, code, stdout.String(), stderr.String())
		}
	}
	for _, command := range schemaDocument().Commands {
		if command.Name == "myself" {
			t.Fatalf("removed myself command remains in schema: %+v", command)
		}
	}
}

func authenticatedServer(t *testing.T, auth config.AuthType, username, secret string, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/rest/api/2/myself" {
			t.Errorf("path = %q", request.URL.Path)
		}
		if auth == config.AuthPAT {
			if got := request.Header.Get("Authorization"); got != "Bearer "+secret {
				t.Errorf("Authorization = %q", got)
			}
		} else {
			gotUsername, gotPassword, ok := request.BasicAuth()
			if !ok || gotUsername != username || gotPassword != secret {
				t.Errorf("BasicAuth = %q/%q/%t", gotUsername, gotPassword, ok)
			}
		}
		writer.WriteHeader(status)
		if status >= 200 && status < 300 {
			_, _ = io.WriteString(writer, fmt.Sprintf(`{"name":%q,"displayName":"Test User","active":true}`, username))
		}
	}))
}

func writeAuthConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func storeContains(store *memorySecretStore, want string) bool {
	for _, value := range store.secrets {
		if value == want {
			return true
		}
	}
	return false
}

var _ config.SecretStore = (*memorySecretStore)(nil)
