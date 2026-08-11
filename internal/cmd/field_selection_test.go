package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/timonwong/jiro/internal/fieldcache"
	"github.com/timonwong/jiro/internal/jira"
)

func TestSearchResolvesFieldSelectorsAndReportsFieldSelection(t *testing.T) {
	clearCommandEnv(t)
	var myselfCalls, fieldCalls, searchCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/myself":
			myselfCalls.Add(1)
			writeTestPrincipal(writer)
		case "/rest/api/2/field":
			fieldCalls.Add(1)
			_, _ = io.WriteString(writer, `[
				{"id":"summary","name":"Summary","custom":false,"schema":{"type":"string"}},
				{"id":"customfield_10001","name":"Sprint","custom":true,"schema":{"type":"array"}}
			]`)
		case "/rest/api/2/search":
			searchCalls.Add(1)
			var payload struct {
				Fields []string `json:"fields"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if want := []string{"summary", "customfield_10001"}; !reflect.DeepEqual(payload.Fields, want) {
				t.Fatalf("fields = %#v, want %#v", payload.Fields, want)
			}
			_, _ = io.WriteString(writer, `{"startAt":0,"maxResults":50,"total":1,"issues":[{"id":"1","key":"AIT-1","fields":{"summary":"One","customfield_10001":[]}}]}`)
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := newFieldCacheTestApp(stdout, stderr, fieldcache.New(t.TempDir(), nil))
	code := a.execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "-ojson", "search", "project = AIT",
		"--fields", "summary,Sprint",
	})
	if code != 0 || stderr.Len() != 0 || myselfCalls.Load() != 1 || fieldCalls.Load() != 1 || searchCalls.Load() != 1 {
		t.Fatalf("code=%d myself=%d fields=%d search=%d stdout=%s stderr=%s", code, myselfCalls.Load(), fieldCalls.Load(), searchCalls.Load(), stdout.String(), stderr.String())
	}
	var envelope struct {
		Data struct {
			FieldSelection []jira.FieldSelection `json:"fieldSelection"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	want := []jira.FieldSelection{
		{Selector: "summary", Resolved: "summary"},
		{Selector: "Sprint", Resolved: "customfield_10001"},
	}
	if !reflect.DeepEqual(envelope.Data.FieldSelection, want) {
		t.Fatalf("fieldSelection = %#v, want %#v; stdout=%s", envelope.Data.FieldSelection, want, strings.TrimSpace(stdout.String()))
	}
}

func TestStaleFieldMetadataMissPreservesRefreshError(t *testing.T) {
	clearCommandEnv(t)
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	store := fieldcache.New(t.TempDir(), func() time.Time { return now })

	var searchCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/myself":
			writeTestPrincipal(writer)
		case "/rest/api/2/field":
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(writer, `{"errorMessages":["metadata unavailable"]}`)
		case "/rest/api/2/search":
			searchCalls.Add(1)
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	writeTestSnapshot(t, store, server.URL, "summary", "Summary")
	now = now.Add(25 * time.Hour)

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := newFieldCacheTestApp(stdout, stderr, store)
	code := a.execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "-ojson", "search", "project = AIT",
		"--fields", "Sprint",
	})
	if code != 5 || stdout.Len() != 0 || searchCalls.Load() != 0 || !strings.Contains(stderr.String(), "metadata unavailable") {
		t.Fatalf("code=%d search=%d stdout=%s stderr=%s", code, searchCalls.Load(), stdout.String(), stderr.String())
	}
}

func TestFreshFieldMetadataMissDoesNotRefresh(t *testing.T) {
	clearCommandEnv(t)
	store := fieldcache.New(t.TempDir(), nil)
	var fieldCalls, searchCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/myself":
			writeTestPrincipal(writer)
		case "/rest/api/2/field":
			fieldCalls.Add(1)
		case "/rest/api/2/search":
			searchCalls.Add(1)
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()
	writeTestSnapshot(t, store, server.URL, "summary", "Summary")

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := newFieldCacheTestApp(stdout, stderr, store)
	code := a.execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "-ojson", "search", "project = AIT",
		"--fields", "Sprint",
	})
	if code != 2 || stdout.Len() != 0 || fieldCalls.Load() != 0 || searchCalls.Load() != 0 || !strings.Contains(stderr.String(), `field selector \"Sprint\" was not found`) {
		t.Fatalf("code=%d fields=%d search=%d stdout=%s stderr=%s", code, fieldCalls.Load(), searchCalls.Load(), stdout.String(), stderr.String())
	}
}

func TestStaleFieldMetadataResolvesSelectorWithWarning(t *testing.T) {
	clearCommandEnv(t)
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	store := fieldcache.New(t.TempDir(), func() time.Time { return now })
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/myself":
			writeTestPrincipal(writer)
		case "/rest/api/2/field":
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(writer, `{"errorMessages":["metadata unavailable"]}`)
		case "/rest/api/2/search":
			var payload struct {
				Fields []string `json:"fields"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if want := []string{"customfield_10001"}; !reflect.DeepEqual(payload.Fields, want) {
				t.Fatalf("fields = %#v, want %#v", payload.Fields, want)
			}
			_, _ = io.WriteString(writer, `{"startAt":0,"maxResults":50,"total":0,"issues":[]}`)
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()
	writeTestSnapshot(t, store, server.URL, "customfield_10001", "Sprint")
	now = now.Add(25 * time.Hour)

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := newFieldCacheTestApp(stdout, stderr, store)
	code := a.execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "-ojson", "search", "project = AIT",
		"--fields", "Sprint",
	})
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var envelope struct {
		Warnings []struct {
			Code string `json:"code"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Warnings) != 1 || envelope.Warnings[0].Code != "stale_field_cache" {
		t.Fatalf("warnings = %#v", envelope.Warnings)
	}
}

func TestSearchAllResolvesFieldSelectorsOnce(t *testing.T) {
	clearCommandEnv(t)
	var myselfCalls, fieldCalls, searchCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/myself":
			myselfCalls.Add(1)
			writeTestPrincipal(writer)
		case "/rest/api/2/field":
			fieldCalls.Add(1)
			_, _ = io.WriteString(writer, `[{"id":"customfield_10001","name":"Sprint","custom":true}]`)
		case "/rest/api/2/search":
			searchCalls.Add(1)
			var payload struct {
				StartAt int      `json:"startAt"`
				Fields  []string `json:"fields"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if want := []string{"customfield_10001"}; !reflect.DeepEqual(payload.Fields, want) {
				t.Fatalf("fields = %#v, want %#v", payload.Fields, want)
			}
			issueNumber := payload.StartAt + 1
			_, _ = fmt.Fprintf(writer, `{"startAt":%d,"maxResults":1,"total":2,"issues":[{"id":"%d","key":"AIT-%d","fields":{"customfield_10001":[]}}]}`, payload.StartAt, issueNumber, issueNumber)
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := newFieldCacheTestApp(stdout, stderr, fieldcache.New(t.TempDir(), nil))
	code := a.execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "-ojson", "search", "project = AIT",
		"--all", "--limit", "1", "--fields", "Sprint",
	})
	if code != 0 || stderr.Len() != 0 || myselfCalls.Load() != 1 || fieldCalls.Load() != 1 || searchCalls.Load() != 2 {
		t.Fatalf("code=%d myself=%d fields=%d search=%d stdout=%s stderr=%s", code, myselfCalls.Load(), fieldCalls.Load(), searchCalls.Load(), stdout.String(), stderr.String())
	}
}

func TestDirectAndSpecialFieldSelectorsBypassMetadata(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/myself", "/rest/api/2/field":
			t.Fatalf("direct selectors called metadata endpoint %q", request.URL.Path)
		case "/rest/api/2/search":
			var payload struct {
				Fields []string `json:"fields"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			want := []string{"customfield_99999", "*navigable", "-customfield_10001"}
			if !reflect.DeepEqual(payload.Fields, want) {
				t.Fatalf("fields = %#v, want %#v", payload.Fields, want)
			}
			_, _ = io.WriteString(writer, `{"startAt":0,"maxResults":50,"total":0,"issues":[]}`)
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := newFieldCacheTestApp(stdout, stderr, fieldcache.New(t.TempDir(), nil))
	code := a.execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "-ojson", "search", "project = AIT",
		"--fields", "customfield_99999,*navigable,-customfield_10001",
	})
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestSearchWithoutExplicitFieldsOmitsFieldSelectionAndMetadataLookup(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/myself", "/rest/api/2/field":
			t.Fatalf("search without --fields called metadata endpoint %q", request.URL.Path)
		case "/rest/api/2/search":
			_, _ = io.WriteString(writer, `{"startAt":0,"maxResults":50,"total":0,"issues":[]}`)
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := newFieldCacheTestApp(stdout, stderr, fieldcache.New(t.TempDir(), nil))
	code := a.execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "-ojson", "search", "project = AIT",
	})
	if code != 0 || stderr.Len() != 0 || strings.Contains(stdout.String(), "fieldSelection") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestReadFieldHelpUsesFieldSelectorTerminology(t *testing.T) {
	clearCommandEnv(t)
	for _, args := range [][]string{
		{"search", "--help"},
		{"issue", "list", "--help"},
		{"issue", "show", "--help"},
	} {
		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		a := newFieldCacheTestApp(stdout, stderr, fieldcache.New(t.TempDir(), nil))
		if code := a.execute(args); code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "Field Selectors") {
			t.Fatalf("args=%v code=%d stdout=%s stderr=%s", args, code, stdout.String(), stderr.String())
		}
	}
}

func TestSearchTextOutputDoesNotExposeFieldSelection(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/myself":
			writeTestPrincipal(writer)
		case "/rest/api/2/field":
			_, _ = io.WriteString(writer, `[{"id":"customfield_10001","name":"Sprint","custom":true}]`)
		case "/rest/api/2/search":
			_, _ = io.WriteString(writer, `{"startAt":0,"maxResults":50,"total":1,"issues":[{"id":"1","key":"AIT-1","fields":{"summary":"One","status":{"name":"Open"},"customfield_10001":[]}}]}`)
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := newFieldCacheTestApp(stdout, stderr, fieldcache.New(t.TempDir(), nil))
	code := a.execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "search", "project = AIT", "--fields", "Sprint",
	})
	if code != 0 || stderr.Len() != 0 || strings.Contains(stdout.String(), "fieldSelection") || !strings.Contains(stdout.String(), "AIT-1") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestColdFieldMetadataFailureKeepsOperationalError(t *testing.T) {
	clearCommandEnv(t)
	var searchCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/myself":
			writeTestPrincipal(writer)
		case "/rest/api/2/field":
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(writer, `{"errorMessages":["metadata unavailable"]}`)
		case "/rest/api/2/search":
			searchCalls.Add(1)
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := newFieldCacheTestApp(stdout, stderr, fieldcache.New(t.TempDir(), nil))
	code := a.execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "-ojson", "search", "project = AIT", "--fields", "Sprint",
	})
	if code != 5 || stdout.Len() != 0 || searchCalls.Load() != 0 || !strings.Contains(stderr.String(), "metadata unavailable") {
		t.Fatalf("code=%d search=%d stdout=%s stderr=%s", code, searchCalls.Load(), stdout.String(), stderr.String())
	}
}

func TestIssueShowAddsFieldSelectionWithoutWrappingIssue(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/myself":
			writeTestPrincipal(writer)
		case "/rest/api/2/field":
			_, _ = io.WriteString(writer, `[
				{"id":"summary","name":"Summary","custom":false,"schema":{"type":"string"}},
				{"id":"customfield_10001","name":"Sprint","custom":true,"schema":{"type":"array"}}
			]`)
		case "/rest/api/2/issue/AIT-1":
			if got, want := request.URL.Query().Get("fields"), "summary,customfield_10001"; got != want {
				t.Fatalf("fields query = %q, want %q", got, want)
			}
			_, _ = io.WriteString(writer, `{"id":"1","key":"AIT-1","fields":{"summary":"One","customfield_10001":[]}}`)
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := newFieldCacheTestApp(stdout, stderr, fieldcache.New(t.TempDir(), nil))
	code := a.execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "-ojson", "issue", "show", "AIT-1",
		"--fields", "summary,Sprint",
	})
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var envelope struct {
		Data struct {
			Key            string                `json:"key"`
			Fields         map[string]any        `json:"fields"`
			FieldSelection []jira.FieldSelection `json:"fieldSelection"`
			Issue          json.RawMessage       `json:"issue"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Key != "AIT-1" || envelope.Data.Fields["customfield_10001"] == nil || envelope.Data.Issue != nil {
		t.Fatalf("data = %#v; stdout=%s", envelope.Data, stdout.String())
	}
	want := []jira.FieldSelection{
		{Selector: "summary", Resolved: "summary"},
		{Selector: "Sprint", Resolved: "customfield_10001"},
	}
	if !reflect.DeepEqual(envelope.Data.FieldSelection, want) {
		t.Fatalf("fieldSelection = %#v, want %#v", envelope.Data.FieldSelection, want)
	}
}

func TestIssueListUsesTheSharedFieldSelectorContract(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/myself":
			writeTestPrincipal(writer)
		case "/rest/api/2/field":
			_, _ = io.WriteString(writer, `[{"id":"customfield_10001","name":"Sprint","custom":true,"schema":{"type":"array"}}]`)
		case "/rest/api/2/search":
			var payload struct {
				Fields []string `json:"fields"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if want := []string{"customfield_10001"}; !reflect.DeepEqual(payload.Fields, want) {
				t.Fatalf("fields = %#v, want %#v", payload.Fields, want)
			}
			_, _ = io.WriteString(writer, `{"startAt":0,"maxResults":50,"total":0,"issues":[]}`)
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := newFieldCacheTestApp(stdout, stderr, fieldcache.New(t.TempDir(), nil))
	code := a.execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "-ojson", "issue", "list",
		"--project", "AIT", "--fields", "Sprint",
	})
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var envelope struct {
		Data struct {
			Issues         []jira.Issue          `json:"issues"`
			FieldSelection []jira.FieldSelection `json:"fieldSelection"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Issues == nil || len(envelope.Data.FieldSelection) != 1 || envelope.Data.FieldSelection[0].Resolved != "customfield_10001" {
		t.Fatalf("data = %#v", envelope.Data)
	}
}
