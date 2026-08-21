package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/timonwong/jiro/internal/fieldcache"
)

func TestIssueCloneCreatesLinkAndSprintInOrder(t *testing.T) {
	clearCommandEnv(t)
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		paths = append(paths, request.Method+" "+request.URL.Path)
		switch request.URL.Path {
		case "/rest/api/2/myself":
			writeTestPrincipal(writer)
		case "/rest/api/2/field":
			_, _ = io.WriteString(writer, `[{"id":"customfield_12345","name":"Release Flag","custom":true,"schema":{"type":"string"}},{"id":"customfield_10001","name":"Sprint","custom":true,"schema":{"type":"array","custom":"com.pyxis.greenhopper.jira:gh-sprint"}}]`)
		case "/rest/api/2/issue/OPS-1":
			if request.Method == http.MethodGet {
				if request.URL.Query().Get("fields") == "project,issuetype,summary,description,priority,assignee,labels,components,fixVersions,parent" {
					_, _ = io.WriteString(writer, `{"id":"1","key":"OPS-1","fields":{"project":{"key":"OPS"},"issuetype":{"id":"10001","name":"Task"},"summary":"Source","description":"Context","priority":{"id":"3","name":"Major"},"assignee":{"name":"alice"},"labels":["one"],"components":[{"id":"10","name":"API"}],"fixVersions":[{"id":"20","name":"4.4"}],"parent":{"key":"OPS-0"}}}`)
					return
				}
				_, _ = io.WriteString(writer, `{"id":"1","key":"OPS-1","fields":{"project":{"key":"OPS"},"issuetype":{"id":"10001","name":"Task"},"summary":"Source","description":"Context","priority":{"id":"3","name":"Major"},"assignee":{"name":"alice"},"labels":["one"],"components":[{"id":"10","name":"API"}],"fixVersions":[{"id":"20","name":"4.4"}],"parent":{"key":"OPS-0"},"customfield_12345":"copy", "customfield_10001":["Sprint@[id=42, state=ACTIVE]"]}}`)
			}
		case "/rest/api/2/issue/createmeta/OPS/issuetypes/10001":
			_, _ = io.WriteString(writer, `{"fields":{"customfield_12345":{"name":"Release Flag","schema":{"type":"string"},"operations":["set"]},"customfield_10001":{"name":"Sprint","schema":{"type":"array","custom":"com.pyxis.greenhopper.jira:gh-sprint"},"operations":["set"]}}}`)
		case "/rest/api/2/issueLinkType":
			_, _ = io.WriteString(writer, `{"issueLinkTypes":[{"id":"10000","name":"Cloners","inward":"is cloned by","outward":"clones"}]}`)
		case "/rest/api/2/issue":
			var payload struct {
				Fields map[string]any `json:"fields"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload.Fields["summary"] != "CLONE - Source" || payload.Fields["project"].(map[string]any)["key"] != "OPS" || payload.Fields["issuetype"].(map[string]any)["id"] != "10001" {
				t.Fatalf("create fields = %#v", payload.Fields)
			}
			if payload.Fields["customfield_12345"] != "copy" || payload.Fields["customfield_10001"] != nil {
				t.Fatalf("custom fields = %#v", payload.Fields)
			}
			_, _ = io.WriteString(writer, `{"id":"2","key":"OPS-2"}`)
		case "/rest/api/2/issueLink":
			var payload struct {
				Type         map[string]string `json:"type"`
				OutwardIssue map[string]string `json:"outwardIssue"`
				InwardIssue  map[string]string `json:"inwardIssue"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload.Type["id"] != "10000" || payload.OutwardIssue["key"] != "OPS-1" || payload.InwardIssue["key"] != "OPS-2" {
				t.Fatalf("link payload = %#v", payload)
			}
			writer.WriteHeader(http.StatusCreated)
		case "/rest/agile/1.0/sprint/42/issue":
			writer.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected %s %s", request.Method, request.URL.Path)
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := newFieldCacheTestApp(stdout, stderr, fieldcache.New(t.TempDir(), nil))
	code := a.execute([]string{"--config", writeCLIConfig(t, server.URL, false), "issue", "clone", "OPS-1"})
	if code != 0 || stderr.Len() != 0 || stdout.String() != "Cloned OPS-1 to OPS-2\n" {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	wantPaths := []string{
		"GET /rest/api/2/issue/OPS-1", "GET /rest/api/2/issue/createmeta/OPS/issuetypes/10001",
		"GET /rest/api/2/myself", "GET /rest/api/2/field", "GET /rest/api/2/issue/OPS-1", "GET /rest/api/2/issueLinkType", "POST /rest/api/2/issue", "POST /rest/api/2/issueLink", "POST /rest/agile/1.0/sprint/42/issue",
	}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Fatalf("paths = %#v, want %#v", paths, wantPaths)
	}
}

func TestIssueCloneRejectsMultipleActiveSprintsBeforeCreate(t *testing.T) {
	clearCommandEnv(t)
	var creates int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/myself":
			writeTestPrincipal(writer)
		case "/rest/api/2/field":
			_, _ = io.WriteString(writer, `[{"id":"customfield_10001","name":"Sprint","custom":true,"schema":{"custom":"com.pyxis.greenhopper.jira:gh-sprint"}}]`)
		case "/rest/api/2/issue/OPS-1":
			_, _ = io.WriteString(writer, `{"id":"1","key":"OPS-1","fields":{"project":{"key":"OPS"},"issuetype":{"id":"10001","name":"Task"},"summary":"Source","customfield_10001":["Sprint@[id=41, state=ACTIVE]","Sprint@[id=42, state=ACTIVE]"]}}`)
		case "/rest/api/2/issue/createmeta/OPS/issuetypes/10001":
			_, _ = io.WriteString(writer, `{"fields":{"customfield_10001":{"name":"Sprint","schema":{"custom":"com.pyxis.greenhopper.jira:gh-sprint"}}}}`)
		case "/rest/api/2/issue":
			creates++
		default:
			t.Fatalf("unexpected %s %s", request.Method, request.URL.Path)
		}
	}))
	defer server.Close()
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := newFieldCacheTestApp(stdout, stderr, fieldcache.New(t.TempDir(), nil))
	code := a.execute([]string{"--config", writeCLIConfig(t, server.URL, false), "issue", "clone", "OPS-1"})
	if code != 2 || creates != 0 || !strings.Contains(stderr.String(), "multiple active Sprint") {
		t.Fatalf("code=%d creates=%d stdout=%s stderr=%s", code, creates, stdout, stderr)
	}
}

func TestIssueCloneLinkFailurePreservesCreatedIssueAndRendersPartialResult(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/issue/OPS-1":
			_, _ = io.WriteString(writer, `{"id":"1","key":"OPS-1","fields":{"project":{"key":"OPS"},"issuetype":{"id":"10001","name":"Task"},"summary":"Source"}}`)
		case "/rest/api/2/issue/createmeta/OPS/issuetypes/10001":
			_, _ = io.WriteString(writer, `{"fields":{}}`)
		case "/rest/api/2/myself":
			writeTestPrincipal(writer)
		case "/rest/api/2/field":
			_, _ = io.WriteString(writer, `[]`)
		case "/rest/api/2/issueLinkType":
			_, _ = io.WriteString(writer, `{"issueLinkTypes":[{"id":"10000","name":"Cloners","inward":"is cloned by","outward":"clones"}]}`)
		case "/rest/api/2/issue":
			_, _ = io.WriteString(writer, `{"id":"2","key":"OPS-2"}`)
		case "/rest/api/2/issueLink":
			writer.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(writer, `{"errorMessages":["link unavailable"]}`)
		default:
			t.Fatalf("unexpected %s %s", request.Method, request.URL.Path)
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := newFieldCacheTestApp(stdout, stderr, fieldcache.New(t.TempDir(), nil))
	code := a.execute([]string{"--config", writeCLIConfig(t, server.URL, false), "--quiet", "-ojson", "issue", "clone", "OPS-1"})
	if code != 7 || !strings.Contains(stdout.String(), `"key":"OPS-2"`) || !strings.Contains(stdout.String(), `"created":false`) || !strings.Contains(stderr.String(), `"kind":"partial_failure"`) {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestIssueCloneNoLinkAppliesExplicitOverrides(t *testing.T) {
	clearCommandEnv(t)
	var linkTypeCalls int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/issue/OPS-1":
			_, _ = io.WriteString(writer, `{"id":"1","key":"OPS-1","fields":{"project":{"key":"OPS"},"issuetype":{"id":"10001","name":"Task"},"summary":"Source","description":"old","assignee":{"name":"alice"},"components":[{"name":"API"}],"customfield_12345":"old"}}`)
		case "/rest/api/2/issue/createmeta/OPS/issuetypes/10001":
			_, _ = io.WriteString(writer, `{"fields":{"customfield_12345":{"name":"Flag","schema":{"type":"string"}}}}`)
		case "/rest/api/2/myself":
			writeTestPrincipal(writer)
		case "/rest/api/2/field":
			_, _ = io.WriteString(writer, `[{"id":"customfield_12345","name":"Flag","custom":true,"schema":{"type":"string"}}]`)
		case "/rest/api/2/issueLinkType":
			linkTypeCalls++
		case "/rest/api/2/issue":
			var payload struct {
				Fields map[string]any `json:"fields"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload.Fields["summary"] != "Replacement" || payload.Fields["description"] != "new" || payload.Fields["assignee"] != nil || payload.Fields["customfield_12345"] != "explicit" {
				t.Fatalf("create fields = %#v", payload.Fields)
			}
			if got, ok := payload.Fields["components"].([]any); !ok || len(got) != 0 {
				t.Fatalf("components = %#v", payload.Fields["components"])
			}
			_, _ = io.WriteString(writer, `{"id":"2","key":"OPS-2"}`)
		default:
			t.Fatalf("unexpected %s %s", request.Method, request.URL.Path)
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := newFieldCacheTestApp(stdout, stderr, fieldcache.New(t.TempDir(), nil))
	code := a.execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "-ojson", "issue", "clone", "OPS-1", "--no-link",
		"--summary", "Replacement", "--description", "new", "--assignee", "none", "--component", "none", "--field", "customfield_12345=explicit",
	})
	if code != 0 || stderr.Len() != 0 || linkTypeCalls != 0 || !strings.Contains(stdout.String(), `"enabled":false`) {
		t.Fatalf("code=%d linkTypes=%d stdout=%s stderr=%s", code, linkTypeCalls, stdout, stderr)
	}
}

func TestIssueCloneCopiesExplicitlyUnassignedSource(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/issue/OPS-1":
			_, _ = io.WriteString(writer, `{"id":"1","key":"OPS-1","fields":{"project":{"key":"OPS"},"issuetype":{"id":"10001","name":"Task"},"summary":"Source","assignee":null}}`)
		case "/rest/api/2/issue/createmeta/OPS/issuetypes/10001":
			_, _ = io.WriteString(writer, `{"fields":{}}`)
		case "/rest/api/2/myself":
			writeTestPrincipal(writer)
		case "/rest/api/2/field":
			_, _ = io.WriteString(writer, `[]`)
		case "/rest/api/2/issue":
			var payload struct {
				Fields map[string]any `json:"fields"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			assignee, found := payload.Fields["assignee"]
			if !found || assignee != nil {
				t.Fatalf("assignee = %#v, found=%t; want explicit null", assignee, found)
			}
			_, _ = io.WriteString(writer, `{"id":"2","key":"OPS-2"}`)
		default:
			t.Fatalf("unexpected %s %s", request.Method, request.URL.Path)
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := newFieldCacheTestApp(stdout, stderr, fieldcache.New(t.TempDir(), nil))
	code := a.execute([]string{"--config", writeCLIConfig(t, server.URL, false), "-ojson", "issue", "clone", "OPS-1", "--no-link"})
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestIssueCloneClassifiesPreflightAndSourceErrors(t *testing.T) {
	validIssue := `{"id":"1","key":"OPS-1","fields":{"project":{"key":"OPS"},"issuetype":{"id":"10001","name":"Task"},"summary":"Source"}}`
	subtaskWithoutParent := `{"id":"1","key":"OPS-1","fields":{"project":{"key":"OPS"},"issuetype":{"id":"10001","name":"Sub-task","subtask":true},"summary":"Source"}}`
	tests := []struct {
		name        string
		firstStatus int
		firstBody   string
		secondBody  string
		linkTypes   string
		wantCode    int
		wantMessage string
		noLink      bool
		wantCreates int
	}{
		{name: "invalid source", firstStatus: http.StatusNotFound, wantCode: 4, wantMessage: "HTTP 404", wantCreates: 0},
		{name: "source API failure", firstStatus: http.StatusBadGateway, wantCode: 5, wantMessage: "HTTP 502", wantCreates: 0},
		{name: "missing project", firstBody: `{"id":"1","key":"OPS-1","fields":{"issuetype":{"id":"10001"}}}`, wantCode: 5, wantMessage: "has no Project", wantCreates: 0},
		{name: "missing issue type", firstBody: `{"id":"1","key":"OPS-1","fields":{"project":{"key":"OPS"}}}`, wantCode: 5, wantMessage: "has no Issue Type ID", wantCreates: 0},
		{name: "missing parent", firstBody: subtaskWithoutParent, secondBody: subtaskWithoutParent, wantCode: 5, wantMessage: "has no Parent", noLink: true, wantCreates: 0},
		{name: "missing Cloners link type", firstBody: validIssue, secondBody: validIssue, linkTypes: `{"issueLinkTypes":[]}`, wantCode: 2, wantMessage: "was not found", wantCreates: 0},
		{name: "ambiguous Cloners link type", firstBody: validIssue, secondBody: validIssue, linkTypes: `{"issueLinkTypes":[{"id":"10000","name":"Cloners"},{"id":"10001","name":"Cloners"}]}`, wantCode: 2, wantMessage: "is ambiguous", wantCreates: 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearCommandEnv(t)
			var sourceCalls, creates int
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				switch request.URL.Path {
				case "/rest/api/2/issue/OPS-1":
					sourceCalls++
					if sourceCalls == 1 && test.firstStatus != 0 {
						writer.WriteHeader(test.firstStatus)
						return
					}
					body := test.firstBody
					if sourceCalls > 1 {
						body = test.secondBody
					}
					if body == "" {
						body = validIssue
					}
					_, _ = io.WriteString(writer, body)
				case "/rest/api/2/issue/createmeta/OPS/issuetypes/10001":
					_, _ = io.WriteString(writer, `{"fields":{}}`)
				case "/rest/api/2/myself":
					writeTestPrincipal(writer)
				case "/rest/api/2/field":
					_, _ = io.WriteString(writer, `[]`)
				case "/rest/api/2/issueLinkType":
					if test.linkTypes == "" {
						t.Fatalf("unexpected link type lookup")
					}
					_, _ = io.WriteString(writer, test.linkTypes)
				case "/rest/api/2/issue":
					creates++
					_, _ = io.WriteString(writer, `{"id":"2","key":"OPS-2"}`)
				default:
					t.Fatalf("unexpected %s %s", request.Method, request.URL.Path)
				}
			}))

			stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
			a := newFieldCacheTestApp(stdout, stderr, fieldcache.New(t.TempDir(), nil))
			args := []string{"--config", writeCLIConfig(t, server.URL, false), "-ojson", "issue", "clone", "OPS-1"}
			if test.noLink {
				args = append(args, "--no-link")
			}
			code := a.execute(args)
			server.Close()
			if code != test.wantCode || creates != test.wantCreates || !strings.Contains(stderr.String(), test.wantMessage) {
				t.Fatalf("code=%d creates=%d stdout=%s stderr=%s", code, creates, stdout, stderr)
			}
		})
	}
}

func TestIssueCloneAcceptsCreateFieldWhenOperationsAreOmitted(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/issue/OPS-1":
			if request.Method == http.MethodGet && request.URL.Query().Get("fields") != "project,issuetype,summary,description,priority,assignee,labels,components,fixVersions,parent" {
				_, _ = io.WriteString(writer, `{"id":"1","key":"OPS-1","fields":{"project":{"key":"OPS"},"issuetype":{"id":"10001","name":"Task"},"summary":"Source","customfield_12345":"copied"}}`)
				return
			}
			_, _ = io.WriteString(writer, `{"id":"1","key":"OPS-1","fields":{"project":{"key":"OPS"},"issuetype":{"id":"10001","name":"Task"},"summary":"Source"}}`)
		case "/rest/api/2/issue/createmeta/OPS/issuetypes/10001":
			_, _ = io.WriteString(writer, `{"fields":{"customfield_12345":{"name":"Flag","schema":{"type":"string"}}}}`)
		case "/rest/api/2/myself":
			writeTestPrincipal(writer)
		case "/rest/api/2/field":
			_, _ = io.WriteString(writer, `[]`)
		case "/rest/api/2/issue":
			var payload struct {
				Fields map[string]any `json:"fields"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload.Fields["customfield_12345"] != "copied" {
				t.Fatalf("custom field = %#v", payload.Fields["customfield_12345"])
			}
			_, _ = io.WriteString(writer, `{"id":"2","key":"OPS-2"}`)
		default:
			t.Fatalf("unexpected %s %s", request.Method, request.URL.Path)
		}
	}))
	defer server.Close()
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := newFieldCacheTestApp(stdout, stderr, fieldcache.New(t.TempDir(), nil))
	code := a.execute([]string{"--config", writeCLIConfig(t, server.URL, false), "issue", "clone", "OPS-1", "--no-link"})
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestIssueCloneSprintFailurePreservesSuccessfulLink(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/issue/OPS-1":
			_, _ = io.WriteString(writer, `{"id":"1","key":"OPS-1","fields":{"project":{"key":"OPS"},"issuetype":{"id":"10001","name":"Task"},"summary":"Source","customfield_10001":["Sprint@[id=42, state=ACTIVE]"]}}`)
		case "/rest/api/2/issue/createmeta/OPS/issuetypes/10001":
			_, _ = io.WriteString(writer, `{"fields":{}}`)
		case "/rest/api/2/myself":
			writeTestPrincipal(writer)
		case "/rest/api/2/field":
			_, _ = io.WriteString(writer, `[{"id":"customfield_10001","name":"Sprint","custom":true,"schema":{"custom":"com.pyxis.greenhopper.jira:gh-sprint"}}]`)
		case "/rest/api/2/issueLinkType":
			_, _ = io.WriteString(writer, `{"issueLinkTypes":[{"id":"10000","name":"Cloners","inward":"is cloned by","outward":"clones"}]}`)
		case "/rest/api/2/issue":
			_, _ = io.WriteString(writer, `{"id":"2","key":"OPS-2"}`)
		case "/rest/api/2/issueLink":
			writer.WriteHeader(http.StatusCreated)
		case "/rest/agile/1.0/sprint/42/issue":
			writer.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(writer, `{"errorMessages":["agile unavailable"]}`)
		default:
			t.Fatalf("unexpected %s %s", request.Method, request.URL.Path)
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := newFieldCacheTestApp(stdout, stderr, fieldcache.New(t.TempDir(), nil))
	code := a.execute([]string{"--config", writeCLIConfig(t, server.URL, false), "-ojson", "issue", "clone", "OPS-1"})
	if code != 7 || !strings.Contains(stdout.String(), `"linked":true`) || !strings.Contains(stdout.String(), `"sprint":{"assigned":false,"id":42}`) || !strings.Contains(stderr.String(), `"kind":"partial_failure"`) {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestIssueCloneWarnsForNonCreatableCustomField(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/issue/OPS-1":
			fields := request.URL.Query().Get("fields")
			if fields == "project,issuetype,summary,description,priority,assignee,labels,components,fixVersions,parent" {
				_, _ = io.WriteString(writer, `{"id":"1","key":"OPS-1","fields":{"project":{"key":"OPS"},"issuetype":{"id":"10001","name":"Task"},"summary":"Source"}}`)
				return
			}
			wantFields := "project,issuetype,summary,description,priority,assignee,labels,components,fixVersions,parent,customfield_12345"
			if fields != wantFields {
				t.Fatalf("second source GET fields = %q, want %q", fields, wantFields)
			}
			_, _ = io.WriteString(writer, `{"id":"1","key":"OPS-1","fields":{"project":{"key":"OPS"},"issuetype":{"id":"10001","name":"Task"},"summary":"Source","customfield_12345":"legacy"}}`)
		case "/rest/api/2/issue/createmeta/OPS/issuetypes/10001":
			_, _ = io.WriteString(writer, `{"fields":{"customfield_12345":{"name":"Legacy Flag","schema":{"type":"string"},"operations":[]}}}`)
		case "/rest/api/2/myself":
			writeTestPrincipal(writer)
		case "/rest/api/2/field":
			_, _ = io.WriteString(writer, `[]`)
		case "/rest/api/2/issue":
			var payload struct {
				Fields map[string]any `json:"fields"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if _, found := payload.Fields["customfield_12345"]; found {
				t.Fatalf("non-creatable Custom Field was sent: %#v", payload.Fields)
			}
			_, _ = io.WriteString(writer, `{"id":"2","key":"OPS-2"}`)
		default:
			t.Fatalf("unexpected %s %s", request.Method, request.URL.Path)
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := newFieldCacheTestApp(stdout, stderr, fieldcache.New(t.TempDir(), nil))
	code := a.execute([]string{"--config", writeCLIConfig(t, server.URL, false), "-ojson", "issue", "clone", "OPS-1", "--no-link"})
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var envelope struct {
		Warnings []struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Warnings) != 1 || envelope.Warnings[0].Code != "issue_clone_custom_field_skipped" || envelope.Warnings[0].Details["fieldId"] != "customfield_12345" {
		t.Fatalf("warnings = %#v, stdout=%s", envelope.Warnings, stdout)
	}
}

func TestIssueCloneWarnsForCustomFieldAbsentFromCreateScreen(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/issue/OPS-1":
			fields := request.URL.Query().Get("fields")
			if fields == "project,issuetype,summary,description,priority,assignee,labels,components,fixVersions,parent" {
				_, _ = io.WriteString(writer, `{"id":"1","key":"OPS-1","fields":{"project":{"key":"OPS"},"issuetype":{"id":"10001","name":"Task"},"summary":"Source"}}`)
				return
			}
			wantFields := "project,issuetype,summary,description,priority,assignee,labels,components,fixVersions,parent,customfield_12345"
			if fields != wantFields {
				t.Fatalf("second source GET fields = %q, want %q", fields, wantFields)
			}
			_, _ = io.WriteString(writer, `{"id":"1","key":"OPS-1","fields":{"project":{"key":"OPS"},"issuetype":{"id":"10001","name":"Task"},"summary":"Source","customfield_12345":"legacy"}}`)
		case "/rest/api/2/issue/createmeta/OPS/issuetypes/10001":
			_, _ = io.WriteString(writer, `{"fields":{}}`)
		case "/rest/api/2/myself":
			writeTestPrincipal(writer)
		case "/rest/api/2/field":
			_, _ = io.WriteString(writer, `[{"id":"customfield_12345","name":"Legacy Flag","custom":true,"schema":{"type":"string"}}]`)
		case "/rest/api/2/issue":
			var payload struct {
				Fields map[string]any `json:"fields"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if _, found := payload.Fields["customfield_12345"]; found {
				t.Fatalf("Custom Field absent from Create screen was sent: %#v", payload.Fields)
			}
			_, _ = io.WriteString(writer, `{"id":"2","key":"OPS-2"}`)
		default:
			t.Fatalf("unexpected %s %s", request.Method, request.URL.Path)
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := newFieldCacheTestApp(stdout, stderr, fieldcache.New(t.TempDir(), nil))
	code := a.execute([]string{"--config", writeCLIConfig(t, server.URL, false), "-ojson", "issue", "clone", "OPS-1", "--no-link"})
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var envelope struct {
		Warnings []struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Warnings) != 1 || envelope.Warnings[0].Code != "issue_clone_custom_field_skipped" || envelope.Warnings[0].Details["fieldId"] != "customfield_12345" {
		t.Fatalf("warnings = %#v, stdout=%s", envelope.Warnings, stdout)
	}
}

func TestIssueCloneMalformedSprintMembershipWarnsWithoutSprintMove(t *testing.T) {
	clearCommandEnv(t)
	var sprintCalls int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/issue/OPS-1":
			if request.URL.Query().Get("fields") == "project,issuetype,summary,description,priority,assignee,labels,components,fixVersions,parent" {
				_, _ = io.WriteString(writer, `{"id":"1","key":"OPS-1","fields":{"project":{"key":"OPS"},"issuetype":{"id":"10001","name":"Task"},"summary":"Source"}}`)
				return
			}
			_, _ = io.WriteString(writer, `{"id":"1","key":"OPS-1","fields":{"project":{"key":"OPS"},"issuetype":{"id":"10001","name":"Task"},"summary":"Source","customfield_10001":["Sprint@[id=0, state=ACTIVE]","malformed"]}}`)
		case "/rest/api/2/issue/createmeta/OPS/issuetypes/10001":
			_, _ = io.WriteString(writer, `{"fields":{"customfield_10001":{"name":"Sprint","schema":{"type":"array","custom":"com.pyxis.greenhopper.jira:gh-sprint"},"operations":["set"]}}}`)
		case "/rest/api/2/myself":
			writeTestPrincipal(writer)
		case "/rest/api/2/field":
			_, _ = io.WriteString(writer, `[{"id":"customfield_10001","name":"Sprint","custom":true,"schema":{"type":"array","custom":"com.pyxis.greenhopper.jira:gh-sprint"}}]`)
		case "/rest/api/2/issue":
			_, _ = io.WriteString(writer, `{"id":"2","key":"OPS-2"}`)
		case "/rest/agile/1.0/sprint/42/issue":
			sprintCalls++
		default:
			t.Fatalf("unexpected %s %s", request.Method, request.URL.Path)
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := newFieldCacheTestApp(stdout, stderr, fieldcache.New(t.TempDir(), nil))
	code := a.execute([]string{"--config", writeCLIConfig(t, server.URL, false), "-ojson", "issue", "clone", "OPS-1", "--no-link"})
	if code != 0 || stderr.Len() != 0 || sprintCalls != 0 {
		t.Fatalf("code=%d sprintCalls=%d stdout=%s stderr=%s", code, sprintCalls, stdout, stderr)
	}
	var envelope struct {
		Data struct {
			Sprint map[string]any `json:"sprint"`
		} `json:"data"`
		Warnings []struct {
			Code string `json:"code"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Sprint["assigned"] != false || len(envelope.Warnings) != 1 || envelope.Warnings[0].Code != "sprint_membership_normalization" {
		t.Fatalf("data=%#v warnings=%#v stdout=%s", envelope.Data, envelope.Warnings, stdout)
	}
}
