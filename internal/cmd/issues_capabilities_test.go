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
	"testing"
)

func TestIssuesAssignResolvesMeAndSupportsNone(t *testing.T) {
	clearCommandEnv(t)
	tests := []struct {
		name           string
		value          string
		wantUsername   any
		wantMyselfCall bool
	}{
		{name: "me", value: "me", wantUsername: "alice", wantMyselfCall: true},
		{name: "none", value: "none", wantUsername: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			myselfCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				switch request.URL.Path {
				case "/rest/api/2/myself":
					myselfCalls++
					_, _ = io.WriteString(writer, `{"name":"alice","displayName":"Alice","active":true}`)
				case "/rest/api/2/issue/OPS-1/assignee":
					if request.Method != http.MethodPut {
						t.Fatalf("method = %s", request.Method)
					}
					var payload map[string]any
					if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
						t.Fatal(err)
					}
					if got := payload["name"]; got != test.wantUsername {
						t.Fatalf("username = %#v, want %#v", got, test.wantUsername)
					}
					writer.WriteHeader(http.StatusNoContent)
				default:
					t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
				}
			}))
			defer server.Close()

			stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
			code := Execute([]string{
				"--config", writeCLIConfig(t, server.URL, false), "-ojson",
				"issue", "assign", "OPS-1", "--assignee", test.value,
			}, strings.NewReader(""), stdout, stderr)
			if code != 0 || stderr.Len() != 0 {
				t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			if (myselfCalls == 1) != test.wantMyselfCall {
				t.Fatalf("myself calls = %d", myselfCalls)
			}
			var envelope struct {
				Data struct {
					Key      string `json:"key"`
					Assignee any    `json:"assignee"`
				} `json:"data"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Data.Key != "OPS-1" {
				t.Fatalf("result = %+v", envelope.Data)
			}
		})
	}
}

func TestIssuesLinkResolvesTypeAndPreservesOutwardDirection(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/issueLinkType":
			_, _ = io.WriteString(writer, `{"issueLinkTypes":[{"id":"10000","name":"Depends","inward":"is depended on by","outward":"depends on"}]}`)
		case "/rest/api/2/issueLink":
			if request.Method != http.MethodPost {
				t.Fatalf("method = %s", request.Method)
			}
			var payload struct {
				Type         map[string]any `json:"type"`
				OutwardIssue map[string]any `json:"outwardIssue"`
				InwardIssue  map[string]any `json:"inwardIssue"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload.Type["id"] != "10000" || payload.OutwardIssue["key"] != "OPS-1" || payload.InwardIssue["key"] != "OPS-2" {
				t.Fatalf("payload = %+v", payload)
			}
			writer.WriteHeader(http.StatusCreated)
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "-ojson",
		"issue", "link", "add", "OPS-1", "--to", "OPS-2", "--type", "depends",
	}, strings.NewReader(""), stdout, stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `"from":"OPS-1"`) || !strings.Contains(stdout.String(), `"to":"OPS-2"`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestIssuesLinkRejectsNumericIDNameAmbiguity(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/rest/api/2/issueLinkType" {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		_, _ = io.WriteString(writer, `{"issueLinkTypes":[{"id":"10000","name":"Blocks","inward":"is blocked by","outward":"blocks"},{"id":"10001","name":"10000","inward":"inward","outward":"outward"}]}`)
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "-ojson", "issue", "link", "add", "OPS-1", "--to", "OPS-2", "--type", "10000",
	}, strings.NewReader(""), stdout, stderr)
	if code != 2 || !strings.Contains(stderr.String(), "ambiguous") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestIssueLinkDiscoveryAndDeletion(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/issue/OPS-1":
			if request.URL.Query().Get("fields") != "issuelinks" {
				t.Fatalf("query = %s", request.URL.RawQuery)
			}
			_, _ = io.WriteString(writer, `{"id":"1","key":"OPS-1","fields":{"issuelinks":[{"id":"55","type":{"id":"10000","name":"Depends","inward":"is depended on by","outward":"depends on"},"outwardIssue":{"id":"2","key":"OPS-2","fields":{"summary":"Target"}}}]}}`)
		case "/rest/api/2/issueLinkType":
			_, _ = io.WriteString(writer, `{"issueLinkTypes":[{"id":"10000","name":"Depends","inward":"is depended on by","outward":"depends on"}]}`)
		case "/rest/api/2/issueLink/55":
			if request.Method != http.MethodDelete {
				t.Fatalf("method = %s", request.Method)
			}
			writer.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
	}))
	defer server.Close()
	configPath := writeCLIConfig(t, server.URL, false)

	t.Run("links expose the Jira link ID and direction", func(t *testing.T) {
		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		code := Execute([]string{"--config", configPath, "-ojson", "issue", "link", "list", "OPS-1"}, strings.NewReader(""), stdout, stderr)
		if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"id":"55"`) || !strings.Contains(stdout.String(), `"direction":"outward"`) {
			t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
		}
	})

	t.Run("link types expose both relationships", func(t *testing.T) {
		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		code := Execute([]string{"--config", configPath, "-ojson", "issue", "link", "types"}, strings.NewReader(""), stdout, stderr)
		if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"inward":"is depended on by"`) || !strings.Contains(stdout.String(), `"outward":"depends on"`) {
			t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
		}
	})

	t.Run("unlink deletes only by Jira link ID", func(t *testing.T) {
		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		code := Execute([]string{"--config", configPath, "-ojson", "issue", "link", "delete", "55"}, strings.NewReader(""), stdout, stderr)
		if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"linkId":"55"`) {
			t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
		}
	})
}

func TestIssuesUnlinkRejectsIssueKeysBeforeNetwork(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("network must not be called for a non-numeric Jira link ID")
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "-ojson", "issue", "link", "delete", "OPS-1",
	}, strings.NewReader(""), stdout, stderr)
	if code != 2 || !strings.Contains(stderr.String(), `"kind":"invalid_input"`) {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestIssueCreateAndUpdateStandardRelationshipFields(t *testing.T) {
	clearCommandEnv(t)
	requestNumber := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestNumber++
		var payload struct {
			Fields map[string]any `json:"fields"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		switch request.URL.Path {
		case "/rest/api/2/issue":
			parent := payload.Fields["parent"].(map[string]any)
			components := payload.Fields["components"].([]any)
			versions := payload.Fields["fixVersions"].([]any)
			if parent["key"] != "OPS-1" || components[0].(map[string]any)["name"] != "API" || components[1].(map[string]any)["name"] != "UI" || versions[0].(map[string]any)["name"] != "4.4" {
				t.Fatalf("create fields = %#v", payload.Fields)
			}
			_, _ = io.WriteString(writer, `{"id":"2","key":"OPS-2"}`)
		case "/rest/api/2/issue/OPS-2":
			if components, ok := payload.Fields["components"].([]any); !ok || len(components) != 0 {
				t.Fatalf("components = %#v", payload.Fields["components"])
			}
			versions := payload.Fields["fixVersions"].([]any)
			if len(versions) != 2 || versions[0].(map[string]any)["name"] != "4.5" || versions[1].(map[string]any)["name"] != "4.6" {
				t.Fatalf("fixVersions = %#v", versions)
			}
			writer.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
	}))
	defer server.Close()
	configPath := writeCLIConfig(t, server.URL, false)

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", configPath, "issue", "add", "--project", "OPS", "--type", "Task", "--summary", "Child",
		"--parent", "OPS-1", "--component", "API", "--component", "UI", "--fix-version", "4.4",
	}, strings.NewReader(""), stdout, stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("create code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{
		"--config", configPath, "issue", "update", "OPS-2", "--component", "none", "--fix-version", "4.5", "--fix-version", "4.6",
	}, strings.NewReader(""), stdout, stderr)
	if code != 0 || stderr.Len() != 0 || requestNumber != 2 {
		t.Fatalf("update code=%d requests=%d stdout=%s stderr=%s", code, requestNumber, stdout.String(), stderr.String())
	}
}

func TestIssueCreateWithSprintPreservesCreatedResultOnMoveFailure(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/issue":
			_, _ = io.WriteString(writer, `{"id":"9","key":"OPS-9"}`)
		case "/rest/agile/1.0/board":
			_, _ = io.WriteString(writer, `{"startAt":0,"maxResults":50,"total":0,"values":[]}`)
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "-ojson", "issue", "add",
		"--project", "OPS", "--type", "Task", "--summary", "Scheduled", "--sprint", "missing",
	}, strings.NewReader(""), stdout, stderr)
	if code != 7 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `"id":"9"`) || !strings.Contains(stdout.String(), `"key":"OPS-9"`) {
		t.Fatalf("created result was not preserved: %s", stdout.String())
	}
	if !strings.Contains(stderr.String(), `"kind":"partial_failure"`) || !strings.Contains(stderr.String(), `sprint \"missing\" was not found`) {
		t.Fatalf("stderr = %s", stderr.String())
	}
}

func TestIssueCreateWithSprintAndMoveSucceed(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/issue":
			_, _ = io.WriteString(writer, `{"id":"10","key":"OPS-10"}`)
		case "/rest/agile/1.0/sprint/42/issue":
			var payload struct {
				Issues []string `json:"issues"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if len(payload.Issues) != 1 || payload.Issues[0] != "OPS-10" {
				t.Fatalf("payload = %+v", payload)
			}
			writer.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "-ojson", "issue", "add",
		"--project", "OPS", "--type", "Task", "--summary", "Scheduled", "--sprint", "42",
	}, strings.NewReader(""), stdout, stderr)
	if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"sprintMoved":true`) {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestIssueCreateWithSprintMoveFailureNeverRollsBack(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/issue":
			if request.Method != http.MethodPost {
				t.Fatalf("creation must not be rolled back: %s %s", request.Method, request.URL.Path)
			}
			_, _ = io.WriteString(writer, `{"id":"12","key":"OPS-12"}`)
		case "/rest/agile/1.0/sprint/42/issue":
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(writer, `{"errorMessages":["sprint is closed"]}`)
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "-ojson", "issue", "add",
		"--project", "OPS", "--type", "Task", "--summary", "Scheduled", "--sprint", "42",
	}, strings.NewReader(""), stdout, stderr)
	if code != 7 || !strings.Contains(stdout.String(), `"key":"OPS-12"`) || !strings.Contains(stderr.String(), `"kind":"partial_failure"`) {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestPartialCreateResultIsWrittenInQuietTextMode(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/issue":
			_, _ = io.WriteString(writer, `{"id":"13","key":"OPS-13"}`)
		case "/rest/agile/1.0/board":
			_, _ = io.WriteString(writer, `{"startAt":0,"maxResults":50,"total":0,"values":[]}`)
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "--quiet", "issue", "add",
		"--project", "OPS", "--type", "Task", "--summary", "Scheduled", "--sprint", "missing",
	}, strings.NewReader(""), stdout, stderr)
	if code != 7 || stdout.String() != "Created OPS-13\n" || !strings.Contains(stderr.String(), "failed to move") {
		t.Fatalf("code=%d stdout=%q stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestIssueUpdateSprintOnlyUsesAgileEndpoint(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/rest/agile/1.0/sprint/42/issue" || request.Method != http.MethodPost {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		var payload struct {
			Issues []string `json:"issues"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Issues) != 1 || payload.Issues[0] != "OPS-11" {
			t.Fatalf("payload = %+v", payload)
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "-ojson", "issue", "update", "OPS-11", "--sprint", "42",
	}, strings.NewReader(""), stdout, stderr)
	if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"sprint":"42"`) || !strings.Contains(stdout.String(), `"sprintMoved":true`) {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestIssueUpdateSprintPreflightsThenPreservesPartialResult(t *testing.T) {
	clearCommandEnv(t)

	t.Run("invalid selector performs no write", func(t *testing.T) {
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
		defer server.Close()
		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		code := Execute([]string{
			"--config", writeCLIConfig(t, server.URL, false), "-ojson", "issue", "update", "OPS-11",
			"--field", "story-points=5", "--sprint", "0",
		}, strings.NewReader(""), stdout, stderr)
		if code != 2 || calls != 0 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "sprint ID must be positive") {
			t.Fatalf("code=%d calls=%d stdout=%s stderr=%s", code, calls, stdout.String(), stderr.String())
		}
	})

	t.Run("field update precedes sprint and sprint failure is partial", func(t *testing.T) {
		var calls []string
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			switch {
			case request.Method == http.MethodPut && request.URL.Path == "/rest/api/2/issue/OPS-11":
				calls = append(calls, "fields")
				writer.WriteHeader(http.StatusNoContent)
			case request.Method == http.MethodPost && request.URL.Path == "/rest/agile/1.0/sprint/42/issue":
				calls = append(calls, "sprint")
				writer.WriteHeader(http.StatusInternalServerError)
				_, _ = io.WriteString(writer, `{"errorMessages":["sprint unavailable"]}`)
			default:
				t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
			}
		}))
		defer server.Close()
		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		code := Execute([]string{
			"--config", writeCLIConfig(t, server.URL, false), "-ojson", "issue", "update", "OPS-11",
			"--summary", "Updated", "--sprint", "42",
		}, strings.NewReader(""), stdout, stderr)
		if code != 7 || strings.Join(calls, ",") != "fields,sprint" ||
			!strings.Contains(stdout.String(), `"updated":true`) || !strings.Contains(stdout.String(), `"sprint":"42"`) ||
			!strings.Contains(stdout.String(), `"sprintMoved":false`) || !strings.Contains(stderr.String(), `"kind":"partial_failure"`) {
			t.Fatalf("code=%d calls=%v stdout=%s stderr=%s", code, calls, stdout.String(), stderr.String())
		}
	})

	t.Run("sprint-only failure is not partial", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.Method != http.MethodPost || request.URL.Path != "/rest/agile/1.0/sprint/42/issue" {
				t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
			}
			writer.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(writer, `{"errorMessages":["sprint unavailable"]}`)
		}))
		defer server.Close()
		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		code := Execute([]string{
			"--config", writeCLIConfig(t, server.URL, false), "-ojson", "issue", "update", "OPS-11", "--sprint", "42",
		}, strings.NewReader(""), stdout, stderr)
		if code != 5 || stdout.Len() != 0 || strings.Contains(stderr.String(), `"kind":"partial_failure"`) {
			t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
		}
	})
}

func TestIssueCreateSprintBoardHintScopesResolutionWithoutEnumeration(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/issue":
			_, _ = io.WriteString(writer, `{"id":"9","key":"OPS-9"}`)
		case "/rest/agile/1.0/board/7":
			_, _ = io.WriteString(writer, `{"id":7,"name":"Platform","type":"scrum"}`)
		case "/rest/agile/1.0/board/7/sprint":
			_, _ = io.WriteString(writer, `{"startAt":0,"maxResults":50,"isLast":true,"values":[{"id":90,"name":"Sprint 9","state":"future","originBoardId":7}]}`)
		case "/rest/agile/1.0/sprint/90/issue":
			writer.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "-ojson", "issue", "add",
		"--project", "OPS", "--type", "Task", "--summary", "Scheduled", "--sprint", "sprint 9", "--sprint-board", "7",
	}, strings.NewReader(""), stdout, stderr)
	if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"sprintMoved":true`) {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestIssueUpdateSprintActiveWithBoardHintFiltersStateServerSide(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/agile/1.0/board/7":
			_, _ = io.WriteString(writer, `{"id":7,"name":"Platform","type":"scrum"}`)
		case "/rest/agile/1.0/board/7/sprint":
			if got := request.URL.Query().Get("state"); got != "active" {
				t.Fatalf("state = %q, want active", got)
			}
			_, _ = io.WriteString(writer, `{"startAt":0,"maxResults":50,"isLast":true,"values":[{"id":42,"name":"Sprint 14","state":"active","originBoardId":7}]}`)
		case "/rest/agile/1.0/sprint/42/issue":
			writer.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "-ojson", "issue", "update", "OPS-11",
		"--sprint", "active", "--sprint-board", "7",
	}, strings.NewReader(""), stdout, stderr)
	if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"sprintMoved":true`) {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestIssueSprintBoardFlagIsValidatedBeforeNetwork(t *testing.T) {
	clearCommandEnv(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer server.Close()
	configPath := writeCLIConfig(t, server.URL, false)

	tests := []struct {
		args []string
		want string
	}{
		{[]string{"issue", "add", "--project", "OPS", "--type", "Task", "--summary", "S", "--sprint-board", "7"}, "--sprint-board requires --sprint"},
		{[]string{"issue", "update", "OPS-1", "--summary", "S", "--sprint-board", "7"}, "--sprint-board requires --sprint"},
		{[]string{"issue", "add", "--project", "OPS", "--type", "Task", "--summary", "S", "--sprint", "x", "--sprint-board", "  "}, "board selector must not be empty"},
		{[]string{"issue", "update", "OPS-1", "--sprint", "x", "--sprint-board", "0"}, "board ID must be positive"},
	}
	for _, test := range tests {
		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		fullArgs := append([]string{"--config", configPath, "-ojson"}, test.args...)
		code := Execute(fullArgs, strings.NewReader(""), stdout, stderr)
		if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), test.want) {
			t.Fatalf("args=%v code=%d stdout=%s stderr=%s", test.args, code, stdout.String(), stderr.String())
		}
	}
	if calls != 0 {
		t.Fatalf("network calls = %d, want 0", calls)
	}
}

func TestIssuesListWiresExtendedFilters(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/rest/api/2/search" || request.Method != http.MethodPost {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		jql := payload["jql"].(string)
		for _, fragment := range []string{
			`resolution IS EMPTY`, `reporter = currentUser()`, `labels IN ("agent", "cli")`,
			`component IN ("API")`, `fixVersion IN ("4.4")`, `sprint IN openSprints()`,
			`parent = "OPS-1"`, `created >= "2026-07-31"`, `created < "2026-08-01"`, `updated >= "-7d"`,
		} {
			if !strings.Contains(jql, fragment) {
				t.Fatalf("JQL %q does not contain %q", jql, fragment)
			}
		}
		_, _ = io.WriteString(writer, `{"startAt":0,"maxResults":50,"total":1,"issues":[{"id":"2","key":"OPS-2","fields":{"summary":"Filtered","status":{"name":"Open"}}}]}`)
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, true), "issue", "list",
		"--resolution", "unresolved", "--reporter", "me", "--label", "agent", "--label", "cli",
		"--component", "API", "--fix-version", "4.4", "--sprint", "active", "--parent", "OPS-1",
		"--created", "2026-07-31", "--updated=-7d",
	}, strings.NewReader(""), stdout, stderr)
	if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "OPS-2") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestNewIssueCommandsRenderTextAndJSON(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/search":
			_, _ = io.WriteString(writer, `{"startAt":0,"maxResults":50,"total":0,"issues":[]}`)
		case "/rest/api/2/issue":
			_, _ = io.WriteString(writer, `{"id":"20","key":"OPS-20"}`)
		case "/rest/api/2/issue/OPS-1":
			if request.Method == http.MethodGet {
				_, _ = io.WriteString(writer, `{"id":"1","key":"OPS-1","fields":{"issuelinks":[]}}`)
			} else {
				writer.WriteHeader(http.StatusNoContent)
			}
		case "/rest/api/2/issue/OPS-1/assignee", "/rest/agile/1.0/sprint/42/issue":
			writer.WriteHeader(http.StatusNoContent)
		case "/rest/api/2/issueLinkType":
			_, _ = io.WriteString(writer, `{"issueLinkTypes":[{"id":"10000","name":"Blocks","inward":"is blocked by","outward":"blocks"}]}`)
		case "/rest/api/2/issueLink":
			writer.WriteHeader(http.StatusCreated)
		case "/rest/api/2/issueLink/55":
			writer.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
	}))
	defer server.Close()
	configPath := writeCLIConfig(t, server.URL, false)
	commands := []struct {
		name      string
		args      []string
		emptyText bool
	}{
		{name: "list filters", args: []string{"issue", "list", "--resolution", "unresolved", "--sprint", "active"}, emptyText: true},
		{name: "create fields and sprint", args: []string{"issue", "add", "--project", "OPS", "--type", "Task", "--summary", "Create", "--parent", "OPS-1", "--component", "API", "--fix-version", "4.4", "--sprint", "42"}},
		{name: "update fields", args: []string{"issue", "update", "OPS-1", "--component", "none", "--fix-version", "4.5"}},
		{name: "assign", args: []string{"issue", "assign", "OPS-1", "--assignee", "alice"}},
		{name: "move", args: []string{"issue", "update", "OPS-1", "--sprint", "42"}},
		{name: "links", args: []string{"issue", "link", "list", "OPS-1"}, emptyText: true},
		{name: "link", args: []string{"issue", "link", "add", "OPS-1", "--to", "OPS-2", "--type", "Blocks"}},
		{name: "unlink", args: []string{"issue", "link", "delete", "55"}},
		{name: "link types", args: []string{"issue", "link", "types"}},
		{name: "bulk transition", args: []string{"issue", "bulk", "move", "--jql", "project = EMPTY", "--to", "Done", "--dry-run"}, emptyText: true},
		{name: "bulk assign", args: []string{"issue", "bulk", "assign", "--jql", "project = EMPTY", "--assignee", "alice", "--dry-run"}, emptyText: true},
	}
	for _, command := range commands {
		for _, format := range []string{"text", "json"} {
			t.Run(command.name+"/"+format, func(t *testing.T) {
				args := []string{"--config", configPath}
				if format == "json" {
					args = append(args, "-ojson")
				}
				args = append(args, command.args...)
				stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
				code := Execute(args, strings.NewReader(""), stdout, stderr)
				if code != 0 || stderr.Len() != 0 {
					t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
				}
				if format == "json" {
					if stdout.Len() == 0 {
						t.Fatal("JSON output is empty")
					}
					var envelope struct {
						SchemaVersion string `json:"schemaVersion"`
						Data          any    `json:"data"`
					}
					if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil || envelope.SchemaVersion != "1" || envelope.Data == nil {
						t.Fatalf("JSON output = %s, err=%v", stdout.String(), err)
					}
				} else if command.emptyText {
					if stdout.Len() != 0 {
						t.Fatalf("text output = %q, want empty", stdout.String())
					}
				} else if stdout.Len() == 0 {
					t.Fatal("text output is empty")
				}
			})
		}
	}
}

func TestNewIssueCommandRequiredFlagsFailBeforeNetwork(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("missing required flags must fail before network access")
	}))
	defer server.Close()
	configPath := writeCLIConfig(t, server.URL, false)
	commands := [][]string{
		{"issue", "assign", "OPS-1"},
		{"issue", "update", "OPS-1"},
		{"issue", "link", "add", "OPS-1", "--to", "OPS-2"},
		{"issue", "link", "add", "OPS-1", "--type", "Blocks"},
	}
	for _, command := range commands {
		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		args := append([]string{"--config", configPath, "-ojson"}, command...)
		if code := Execute(args, strings.NewReader(""), stdout, stderr); code != 2 {
			t.Fatalf("args=%v code=%d stdout=%s stderr=%s", args, code, stdout.String(), stderr.String())
		}
	}
}

func TestNewIssueMutationsRespectReadOnlyBeforeCredentialResolution(t *testing.T) {
	clearCommandEnv(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[default]\nhost = \"https://jira.invalid\"\nusername = \"alice\"\nread_only = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	commands := [][]string{
		{"issue", "assign", "OPS-1", "--assignee", "alice"},
		{"issue", "update", "OPS-1", "--sprint", "42"},
		{"issue", "link", "add", "OPS-1", "--to", "OPS-2", "--type", "10000"},
		{"issue", "link", "delete", "55"},
	}
	for _, command := range commands {
		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		args := append([]string{"--config", path, "-ojson"}, command...)
		if code := Execute(args, strings.NewReader(""), stdout, stderr); code != 2 || !strings.Contains(stderr.String(), "read_only") {
			t.Fatalf("args=%v code=%d stdout=%s stderr=%s", args, code, stdout.String(), stderr.String())
		}
	}
}
