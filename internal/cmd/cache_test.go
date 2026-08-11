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
	"time"

	"github.com/timonwong/jiro/internal/fieldcache"
	"github.com/timonwong/jiro/internal/output"
)

var testFieldPrincipal = fieldcache.Principal{AccountID: "account-alice", Username: "alice"}

func TestDirectCustomFieldIDBypassesMetadata(t *testing.T) {
	clearCommandEnv(t)
	var myselfCalls, fieldCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/myself":
			myselfCalls.Add(1)
			t.Fatal("direct custom field ID called /myself")
		case "/rest/api/2/field":
			fieldCalls.Add(1)
			t.Fatal("direct custom field ID called /field")
		case "/rest/api/2/issue":
			_, _ = io.WriteString(writer, `{"id":"1","key":"OPS-1"}`)
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := newFieldCacheTestApp(stdout, stderr, fieldcache.New(t.TempDir(), nil))
	code := a.execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "-ojson", "issue", "add",
		"--project", "OPS", "--type", "Task", "--summary", "Direct", "--field", "customfield_10006=5",
	})
	if code != 0 || stderr.Len() != 0 || myselfCalls.Load() != 0 || fieldCalls.Load() != 0 {
		t.Fatalf("code=%d myself=%d fields=%d stdout=%s stderr=%s", code, myselfCalls.Load(), fieldCalls.Load(), stdout.String(), stderr.String())
	}
}

func TestFreshCustomFieldCacheAvoidsFieldLookup(t *testing.T) {
	clearCommandEnv(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	store := fieldcache.New(t.TempDir(), func() time.Time { return now })
	var fieldCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/myself":
			writeTestPrincipal(writer)
		case "/rest/api/2/field":
			fieldCalls.Add(1)
			t.Fatal("fresh cache called /field")
		case "/rest/api/2/issue":
			_, _ = io.WriteString(writer, `{"id":"1","key":"OPS-1"}`)
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()
	writeTestSnapshot(t, store, server.URL, "customfield_10006", "Story Points")

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := newFieldCacheTestApp(stdout, stderr, store)
	code := a.execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "-ojson", "issue", "add",
		"--project", "OPS", "--type", "Task", "--summary", "Cached", "--field", "story-points=5",
	})
	if code != 0 || stderr.Len() != 0 || fieldCalls.Load() != 0 {
		t.Fatalf("code=%d fields=%d stdout=%s stderr=%s", code, fieldCalls.Load(), stdout.String(), stderr.String())
	}
}

func TestStaleCustomFieldCacheAllowsMutationWithWarning(t *testing.T) {
	clearCommandEnv(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	store := fieldcache.New(t.TempDir(), func() time.Time { return now })
	var fieldCalls, issueCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/myself":
			writeTestPrincipal(writer)
		case "/rest/api/2/field":
			fieldCalls.Add(1)
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(writer, `{"errorMessages":["metadata unavailable"]}`)
		case "/rest/api/2/issue":
			issueCalls.Add(1)
			_, _ = io.WriteString(writer, `{"id":"1","key":"OPS-1"}`)
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()
	writeTestSnapshot(t, store, server.URL, "customfield_10006", "Story Points")
	now = now.Add(25 * time.Hour)

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := newFieldCacheTestApp(stdout, stderr, store)
	code := a.execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "-ojson", "issue", "add",
		"--project", "OPS", "--type", "Task", "--summary", "Stale", "--field", "story-points=5",
	})
	if code != 0 || stderr.Len() != 0 || fieldCalls.Load() != 1 || issueCalls.Load() != 1 {
		t.Fatalf("code=%d fields=%d issues=%d stdout=%s stderr=%s", code, fieldCalls.Load(), issueCalls.Load(), stdout.String(), stderr.String())
	}
	var envelope struct {
		Warnings []output.Warning `json:"warnings"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Warnings) != 1 || envelope.Warnings[0].Code != "stale_field_cache" {
		t.Fatalf("warnings = %#v", envelope.Warnings)
	}
}

func TestAliasMissForcesOneLiveRefresh(t *testing.T) {
	clearCommandEnv(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	store := fieldcache.New(t.TempDir(), func() time.Time { return now })
	var fieldCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/myself":
			writeTestPrincipal(writer)
		case "/rest/api/2/field":
			fieldCalls.Add(1)
			_, _ = io.WriteString(writer, `[{"id":"customfield_10006","name":"Story Points","custom":true,"schema":{"type":"number"}}]`)
		case "/rest/api/2/issue":
			_, _ = io.WriteString(writer, `{"id":"1","key":"OPS-1"}`)
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()
	writeTestSnapshot(t, store, server.URL, "customfield_99999", "Old Field")

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := newFieldCacheTestApp(stdout, stderr, store)
	code := a.execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "-ojson", "issue", "add",
		"--project", "OPS", "--type", "Task", "--summary", "Refresh", "--field", "story-points=5",
	})
	if code != 0 || stderr.Len() != 0 || fieldCalls.Load() != 1 {
		t.Fatalf("code=%d fields=%d stdout=%s stderr=%s", code, fieldCalls.Load(), stdout.String(), stderr.String())
	}
}

func TestInvalidCustomFieldCacheDoesNotFallback(t *testing.T) {
	clearCommandEnv(t)
	store := fieldcache.New(t.TempDir(), nil)
	var issueCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/myself":
			writeTestPrincipal(writer)
		case "/rest/api/2/field":
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(writer, `{"errorMessages":["metadata unavailable"]}`)
		case "/rest/api/2/issue":
			issueCalls.Add(1)
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()
	path, err := store.Path(server.URL, testFieldPrincipal)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"schemaVersion":99}`), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := newFieldCacheTestApp(stdout, stderr, store)
	code := a.execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "-ojson", "issue", "add",
		"--project", "OPS", "--type", "Task", "--summary", "Invalid", "--field", "story-points=5",
	})
	if code != 5 || stdout.Len() != 0 || issueCalls.Load() != 0 || !strings.Contains(stderr.String(), "metadata unavailable") {
		t.Fatalf("code=%d issues=%d stdout=%s stderr=%s", code, issueCalls.Load(), stdout.String(), stderr.String())
	}
}

func TestImplicitCacheWriteFailureUsesLiveMetadataWithWarning(t *testing.T) {
	clearCommandEnv(t)
	root := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(root, []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := fieldcache.New(root, nil)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/myself":
			writeTestPrincipal(writer)
		case "/rest/api/2/field":
			_, _ = io.WriteString(writer, `[{"id":"customfield_10006","name":"Story Points","custom":true,"schema":{"type":"number"}}]`)
		case "/rest/api/2/issue":
			_, _ = io.WriteString(writer, `{"id":"1","key":"OPS-1"}`)
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := newFieldCacheTestApp(stdout, stderr, store)
	code := a.execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "-ojson", "issue", "add",
		"--project", "OPS", "--type", "Task", "--summary", "Live", "--field", "story-points=5",
	})
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var envelope struct {
		Warnings []output.Warning `json:"warnings"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Warnings) != 1 || envelope.Warnings[0].Code != "field_cache_write_failed" {
		t.Fatalf("warnings = %#v", envelope.Warnings)
	}
}

func TestCacheFieldsRefreshAndFieldListSources(t *testing.T) {
	clearCommandEnv(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	store := fieldcache.New(t.TempDir(), func() time.Time { return now })
	var myselfCalls, fieldCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/myself":
			myselfCalls.Add(1)
			writeTestPrincipal(writer)
		case "/rest/api/2/field":
			fieldCalls.Add(1)
			_, _ = io.WriteString(writer, `[{"id":"summary","name":"Summary","custom":false,"schema":{"type":"string"}},{"id":"customfield_10006","name":"Story Points","custom":true,"schema":{"type":"number"}}]`)
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()
	configPath := writeCLIConfig(t, server.URL, true)

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := newFieldCacheTestApp(stdout, stderr, store)
	code := a.execute([]string{"--config", configPath, "-ojson", "cache", "refresh"})
	if code != 0 || stderr.Len() != 0 || myselfCalls.Load() != 1 || fieldCalls.Load() != 1 {
		t.Fatalf("refresh code=%d myself=%d fields=%d stdout=%s stderr=%s", code, myselfCalls.Load(), fieldCalls.Load(), stdout.String(), stderr.String())
	}
	snapshot, _, err := store.Read(server.URL, testFieldPrincipal)
	if err != nil || len(snapshot.Fields) != 2 || snapshot.Fields[0].ID != "customfield_10006" || snapshot.Fields[1].ID != "summary" {
		t.Fatalf("snapshot=%#v err=%v", snapshot, err)
	}

	stdout.Reset()
	stderr.Reset()
	code = a.execute([]string{"--config", configPath, "-ojson", "field", "list", "--custom"})
	if code != 0 || stderr.Len() != 0 || myselfCalls.Load() != 2 || fieldCalls.Load() != 1 {
		t.Fatalf("custom list code=%d myself=%d fields=%d stdout=%s stderr=%s", code, myselfCalls.Load(), fieldCalls.Load(), stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = a.execute([]string{"--config", configPath, "-ojson", "field", "list"})
	if code != 0 || stderr.Len() != 0 || myselfCalls.Load() != 2 || fieldCalls.Load() != 2 {
		t.Fatalf("full list code=%d myself=%d fields=%d stdout=%s stderr=%s", code, myselfCalls.Load(), fieldCalls.Load(), stdout.String(), stderr.String())
	}
}

func TestRemovedRawFlagFailsBeforeNetwork(t *testing.T) {
	clearCommandEnv(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := newFieldCacheTestApp(stdout, stderr, fieldcache.New(t.TempDir(), nil))
	code := a.execute([]string{"--config", writeCLIConfig(t, server.URL, false), "cache", "refresh", "--raw"})
	if code != 2 || calls.Load() != 0 || !strings.Contains(stderr.String(), "invalid command flags") {
		t.Fatalf("code=%d calls=%d stdout=%s stderr=%s", code, calls.Load(), stdout.String(), stderr.String())
	}
}

func newFieldCacheTestApp(stdout, stderr *bytes.Buffer, store fieldcache.Store) *app {
	return &app{stdin: strings.NewReader(""), stdout: stdout, stderr: stderr, fieldStore: store}
}

func writeTestPrincipal(writer http.ResponseWriter) {
	_, _ = io.WriteString(writer, `{"accountId":"account-alice","name":"alice","displayName":"Alice","active":true}`)
}

func writeTestSnapshot(t *testing.T, store fieldcache.Store, instance, id, name string) {
	t.Helper()
	snapshot, err := store.NewSnapshot(instance, testFieldPrincipal, []fieldcache.Field{{ID: id, Name: name, Alias: strings.ToLower(strings.ReplaceAll(name, " ", "-")), Type: "number"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Write(snapshot); err != nil {
		t.Fatal(err)
	}
}
