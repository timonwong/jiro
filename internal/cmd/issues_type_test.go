package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/timonwong/jiro/internal/jira"
)

func TestIssueListTypesReturnsCurrentAndCompatibleTypes(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/api/2/issue/OPS-1":
			if r.URL.Query().Get("fields") != "issuetype" {
				t.Fatalf("fields = %q", r.URL.Query().Get("fields"))
			}
			_, _ = io.WriteString(w, `{"id":"1","key":"OPS-1","fields":{"issuetype":{"id":"10000","name":"Task","subtask":false}}}`)
		case "/rest/api/2/issue/OPS-1/editmeta":
			_, _ = io.WriteString(w, `{"fields":{"issuetype":{"operations":[],"allowedValues":[{"id":"10000","name":"Task","subtask":false},{"id":"10001","name":"Sub-task","description":"Child work","subtask":true}]}}}`)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, true), "-ojson", "issue", "list-types", "OPS-1",
	}, strings.NewReader(""), stdout, stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var envelope struct {
		Data struct {
			IssueKey         string `json:"issueKey"`
			CurrentIssueType struct {
				ID string `json:"id"`
			} `json:"currentIssueType"`
			IssueTypes []struct {
				ID      string `json:"id"`
				Subtask bool   `json:"subtask"`
			} `json:"issueTypes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.IssueKey != "OPS-1" || envelope.Data.CurrentIssueType.ID != "10000" ||
		len(envelope.Data.IssueTypes) != 2 || envelope.Data.IssueTypes[1].ID != "10001" || !envelope.Data.IssueTypes[1].Subtask {
		t.Fatalf("data = %+v", envelope.Data)
	}
}

func TestIssueListTypesNormalizesMissingEditFieldToEmptyList(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/2/issue/OPS-1":
			_, _ = io.WriteString(w, `{"id":"1","key":"OPS-1","fields":{"issuetype":{"id":"10000","name":"Task","subtask":false}}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/2/issue/OPS-1/editmeta":
			_, _ = io.WriteString(w, `{"fields":{"summary":{"operations":["set"]}}}`)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, true), "-ojson", "issue", "list-types", "OPS-1",
	}, strings.NewReader(""), stdout, stderr)
	if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"issueTypes":[]`) {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestIssueUpdateTypePreflightsWritesResolvedIDAndReadsBack(t *testing.T) {
	clearCommandEnv(t)
	issueReads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/2/issue/OPS-1":
			issueReads++
			issueType := `{"id":"10000","name":"Task","subtask":false}`
			if issueReads == 2 {
				issueType = `{"id":"10001","name":"Story","subtask":false}`
			}
			_, _ = io.WriteString(w, `{"id":"1","key":"OPS-1","fields":{"issuetype":`+issueType+`}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/2/issue/OPS-1/editmeta":
			_, _ = io.WriteString(w, `{"fields":{"issuetype":{"operations":[],"allowedValues":[{"id":"10000","name":"Task","subtask":false},{"id":"10001","name":"Story","subtask":false}]}}}`)
		case r.Method == http.MethodPut && r.URL.Path == "/rest/api/2/issue/OPS-1":
			var payload struct {
				Fields map[string]map[string]string `json:"fields"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if got := payload.Fields["issuetype"]["id"]; got != "10001" {
				t.Fatalf("issue type ID = %q", got)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "-ojson", "issue", "update", "OPS-1", "-t", "story",
	}, strings.NewReader(""), stdout, stderr)
	if code != 0 || stderr.Len() != 0 || issueReads != 2 {
		t.Fatalf("code=%d reads=%d stdout=%s stderr=%s", code, issueReads, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `"updated":true`) || !strings.Contains(stdout.String(), `"unchanged":false`) ||
		!strings.Contains(stdout.String(), `"unknown":false`) || !strings.Contains(stdout.String(), `"name":"Story"`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestIssueUpdateTypeNoOpSkipsEditMetaAndPut(t *testing.T) {
	clearCommandEnv(t)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || r.URL.Path != "/rest/api/2/issue/OPS-1" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		_, _ = io.WriteString(w, `{"id":"1","key":"OPS-1","fields":{"issuetype":{"id":"10001","name":"Story","subtask":false}}}`)
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "-ojson", "issue", "update", "OPS-1", "--type", "10001",
	}, strings.NewReader(""), stdout, stderr)
	if code != 0 || stderr.Len() != 0 || requests != 1 || !strings.Contains(stdout.String(), `"unchanged":true`) ||
		!strings.Contains(stdout.String(), `"updated":false`) {
		t.Fatalf("code=%d requests=%d stdout=%s stderr=%s", code, requests, stdout.String(), stderr.String())
	}
}

func TestIssueUpdateTypeIsExclusiveAndFailsClosed(t *testing.T) {
	clearCommandEnv(t)
	t.Run("exclusive", func(t *testing.T) {
		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		code := Execute([]string{"issue", "update", "OPS-1", "--type", "Story", "--priority", "High"}, strings.NewReader(""), stdout, stderr)
		if code != 2 || !strings.Contains(stderr.String(), "--type cannot be combined") {
			t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
		}
	})

	t.Run("incompatible", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/rest/api/2/issue/OPS-1":
				_, _ = io.WriteString(w, `{"id":"1","key":"OPS-1","fields":{"issuetype":{"id":"10000","name":"Task","subtask":false}}}`)
			case r.Method == http.MethodGet && r.URL.Path == "/rest/api/2/issue/OPS-1/editmeta":
				_, _ = io.WriteString(w, `{"fields":{"issuetype":{"allowedValues":[{"id":"10000","name":"Task","subtask":false}]}}}`)
			default:
				t.Fatalf("fail-closed update made unexpected request %s %s", r.Method, r.URL.String())
			}
		}))
		defer server.Close()
		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		code := Execute([]string{
			"--config", writeCLIConfig(t, server.URL, false), "-ojson", "issue", "update", "OPS-1", "--type", "Story",
		}, strings.NewReader(""), stdout, stderr)
		if code != 2 || !strings.Contains(stderr.String(), "not compatible") || !strings.Contains(stderr.String(), "Task (10000)") {
			t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
		}
	})
}

func TestIssueUpdateTypeRejectsAmbiguousCurrentName(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/2/issue/OPS-1":
			_, _ = io.WriteString(w, `{"id":"1","key":"OPS-1","fields":{"issuetype":{"id":"10001","name":"Story","subtask":false}}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/2/issue/OPS-1/editmeta":
			_, _ = io.WriteString(w, `{"fields":{"issuetype":{"allowedValues":[{"id":"10001","name":"Story","subtask":false},{"id":"10002","name":"story","subtask":false}]}}}`)
		default:
			t.Fatalf("ambiguous selector made unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "-ojson", "issue", "update", "OPS-1", "--type", "STORY",
	}, strings.NewReader(""), stdout, stderr)
	if code != 2 || !strings.Contains(stderr.String(), "ambiguous") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestIssueUpdateTypeReadbackFailureIsUnknown(t *testing.T) {
	clearCommandEnv(t)
	issueReads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/2/issue/OPS-1":
			issueReads++
			if issueReads == 1 {
				_, _ = io.WriteString(w, `{"id":"1","key":"OPS-1","fields":{"issuetype":{"id":"10000","name":"Task","subtask":false}}}`)
				return
			}
			http.Error(w, `{"errorMessages":["readback unavailable"]}`, http.StatusInternalServerError)
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/2/issue/OPS-1/editmeta":
			_, _ = io.WriteString(w, `{"fields":{"issuetype":{"allowedValues":[{"id":"10001","name":"Story","subtask":false}]}}}`)
		case r.Method == http.MethodPut && r.URL.Path == "/rest/api/2/issue/OPS-1":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "-ojson", "issue", "update", "OPS-1", "--type", "Story",
	}, strings.NewReader(""), stdout, stderr)
	if code != 7 || !strings.Contains(stdout.String(), `"unknown":true`) || !strings.Contains(stderr.String(), `"kind":"partial_failure"`) ||
		!strings.Contains(stderr.String(), "do not retry") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestIssueUpdateTypeReadbackMismatchReturnsActualType(t *testing.T) {
	clearCommandEnv(t)
	issueReads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/2/issue/OPS-1":
			issueReads++
			issueType := `{"id":"10000","name":"Task","subtask":false}`
			if issueReads == 2 {
				issueType = `{"id":"10002","name":"Bug","subtask":false}`
			}
			_, _ = io.WriteString(w, `{"id":"1","key":"OPS-1","fields":{"issuetype":`+issueType+`}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/2/issue/OPS-1/editmeta":
			_, _ = io.WriteString(w, `{"fields":{"issuetype":{"allowedValues":[{"id":"10001","name":"Story","subtask":false}]}}}`)
		case r.Method == http.MethodPut && r.URL.Path == "/rest/api/2/issue/OPS-1":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "-ojson", "issue", "update", "OPS-1", "--type", "Story",
	}, strings.NewReader(""), stdout, stderr)
	if code != 7 || !strings.Contains(stdout.String(), `"unknown":false`) || !strings.Contains(stdout.String(), `"name":"Bug"`) ||
		!strings.Contains(stderr.String(), `"kind":"partial_failure"`) || !strings.Contains(stderr.String(), "readback returned Bug (10002)") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestIssueUpdateTypeIndeterminateWriteUsesReadback(t *testing.T) {
	clearCommandEnv(t)
	issueReads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/2/issue/OPS-1":
			issueReads++
			issueType := `{"id":"10000","name":"Task","subtask":false}`
			if issueReads == 2 {
				issueType = `{"id":"10001","name":"Story","subtask":false}`
			}
			_, _ = io.WriteString(w, `{"id":"1","key":"OPS-1","fields":{"issuetype":`+issueType+`}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/2/issue/OPS-1/editmeta":
			_, _ = io.WriteString(w, `{"fields":{"issuetype":{"allowedValues":[{"id":"10001","name":"Story","subtask":false}]}}}`)
		case r.Method == http.MethodPut && r.URL.Path == "/rest/api/2/issue/OPS-1":
			http.Error(w, `{"errorMessages":["gateway lost the response"]}`, http.StatusBadGateway)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "-ojson", "issue", "update", "OPS-1", "--type", "Story",
	}, strings.NewReader(""), stdout, stderr)
	if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"updated":true`) || !strings.Contains(stdout.String(), `"name":"Story"`) {
		t.Fatalf("code=%d reads=%d stdout=%s stderr=%s", code, issueReads, stdout.String(), stderr.String())
	}
}

func TestIssueUpdateTypeMalformedReadbackIsUnknown(t *testing.T) {
	clearCommandEnv(t)
	issueReads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/2/issue/OPS-1":
			issueReads++
			if issueReads == 1 {
				_, _ = io.WriteString(w, `{"id":"1","key":"OPS-1","fields":{"issuetype":{"id":"10000","name":"Task","subtask":false}}}`)
				return
			}
			_, _ = io.WriteString(w, `{"id":"1","key":"OPS-1","fields":{}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/2/issue/OPS-1/editmeta":
			_, _ = io.WriteString(w, `{"fields":{"issuetype":{"allowedValues":[{"id":"10001","name":"Story","subtask":false}]}}}`)
		case r.Method == http.MethodPut && r.URL.Path == "/rest/api/2/issue/OPS-1":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "-ojson", "issue", "update", "OPS-1", "--type", "Story",
	}, strings.NewReader(""), stdout, stderr)
	if code != 7 || !strings.Contains(stdout.String(), `"unknown":true`) || !strings.Contains(stderr.String(), "do not retry") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestMatchIssueTypePrefersIDAndRejectsAmbiguousName(t *testing.T) {
	types := []jira.IssueType{
		{ID: "10001", Name: "Story"},
		{ID: "10002", Name: "story"},
	}
	resolved, err := matchIssueType("10002", types)
	if err != nil || resolved.ID != "10002" {
		t.Fatalf("matchIssueType(ID) = %#v, %v", resolved, err)
	}
	if _, err := matchIssueType("STORY", types); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("matchIssueType(name) error = %v", err)
	}
}

func TestBulkIssueTypeDryRunSeparatesReadyUnchangedAndFailed(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/api/2/search":
			_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"total":3,"issues":[{"id":"1","key":"OPS-1","fields":{"issuetype":{"id":"10000","name":"Task","subtask":false}}},{"id":"2","key":"OPS-2","fields":{"issuetype":{"id":"10001","name":"Story","subtask":false}}},{"id":"3","key":"OPS-3","fields":{"issuetype":{"id":"10002","name":"Bug","subtask":false}}}]}`)
		case "/rest/api/2/issue/OPS-1/editmeta":
			_, _ = io.WriteString(w, `{"fields":{"issuetype":{"allowedValues":[{"id":"10001","name":"Story","subtask":false}]}}}`)
		case "/rest/api/2/issue/OPS-2/editmeta":
			_, _ = io.WriteString(w, `{"fields":{"issuetype":{"allowedValues":[{"id":"10001","name":"Story","subtask":false}]}}}`)
		case "/rest/api/2/issue/OPS-3/editmeta":
			_, _ = io.WriteString(w, `{"fields":{"issuetype":{"allowedValues":[{"id":"10002","name":"Bug","subtask":false}]}}}`)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, true), "-ojson", "issue", "bulk", "update",
		"--jql", "project = OPS", "--type", "Story", "--dry-run",
	}, strings.NewReader(""), stdout, stderr)
	if code != 7 || !strings.Contains(stderr.String(), `"kind":"partial_failure"`) {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var envelope struct {
		Data struct {
			Total, Ready, Failed, Unchanged, Unknown int
			Items                                    []issueBatchItem
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Total != 3 || envelope.Data.Ready != 1 || envelope.Data.Failed != 1 || envelope.Data.Unchanged != 1 || envelope.Data.Unknown != 0 {
		t.Fatalf("data = %+v", envelope.Data)
	}
	if len(envelope.Data.Items) != 3 || envelope.Data.Items[0].Outcome != "ready" || envelope.Data.Items[0].Target.IssueTypeInput != "Story" ||
		envelope.Data.Items[0].Target.IssueType == nil || envelope.Data.Items[1].Outcome != "unchanged" ||
		envelope.Data.Items[2].Outcome != "failed" || envelope.Data.Items[2].Target.IssueType != nil {
		t.Fatalf("items = %+v", envelope.Data.Items)
	}
}

func TestBulkIssueTypeStopsAfterUnknownReadback(t *testing.T) {
	clearCommandEnv(t)
	issueReads := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/rest/api/2/search":
			_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"total":3,"issues":[{"id":"1","key":"OPS-1","fields":{"issuetype":{"id":"10000","name":"Task","subtask":false}}},{"id":"2","key":"OPS-2","fields":{"issuetype":{"id":"10000","name":"Task","subtask":false}}},{"id":"3","key":"OPS-3","fields":{"issuetype":{"id":"10000","name":"Task","subtask":false}}}]}`)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/editmeta"):
			if strings.Contains(r.URL.Path, "OPS-3") {
				t.Fatal("OPS-3 must be not_attempted after unknown readback")
			}
			_, _ = io.WriteString(w, `{"fields":{"issuetype":{"allowedValues":[{"id":"10001","name":"Story","subtask":false}]}}}`)
		case r.Method == http.MethodPut && (r.URL.Path == "/rest/api/2/issue/OPS-1" || r.URL.Path == "/rest/api/2/issue/OPS-2"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && (r.URL.Path == "/rest/api/2/issue/OPS-1" || r.URL.Path == "/rest/api/2/issue/OPS-2"):
			issueReads[r.URL.Path]++
			if r.URL.Path == "/rest/api/2/issue/OPS-2" {
				http.Error(w, `{"errorMessages":["readback unavailable"]}`, http.StatusInternalServerError)
				return
			}
			_, _ = io.WriteString(w, `{"id":"1","key":"OPS-1","fields":{"issuetype":{"id":"10001","name":"Story","subtask":false}}}`)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "-ojson", "issue", "bulk", "update",
		"--jql", "project = OPS", "--type", "Story", "--yes",
	}, strings.NewReader(""), stdout, stderr)
	if code != 7 || !strings.Contains(stderr.String(), `"kind":"partial_failure"`) {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var envelope struct {
		Data struct {
			Ready, Succeeded, Unknown, NotAttempted int
			Items                                   []issueBatchItem
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Ready != 2 || envelope.Data.Succeeded != 1 || envelope.Data.Unknown != 1 || envelope.Data.NotAttempted != 1 ||
		len(envelope.Data.Items) != 3 || envelope.Data.Items[0].Outcome != "succeeded" ||
		envelope.Data.Items[1].Outcome != "unknown" || envelope.Data.Items[2].Outcome != "not_attempted" {
		t.Fatalf("data = %+v", envelope.Data)
	}
}
