package cmd

import (
	"bytes"
	"encoding/json"
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

func TestIssueShowNormalizesExplicitSprintMembership(t *testing.T) {
	clearCommandEnv(t)
	legacy := "com.atlassian.greenhopper.service.sprint.Sprint@abc[id=1839,rapidViewId=12,state=ACTIVE,name=AIT-4.5-S2]"
	malformed := "com.atlassian.greenhopper.service.sprint.Sprint@broken[name=Missing ID]"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/myself":
			writeTestPrincipal(writer)
		case "/rest/api/2/field":
			_, _ = io.WriteString(writer, `[{"id":"customfield_10001","name":"Iteration","custom":true,"schema":{"type":"array","custom":"com.pyxis.greenhopper.jira:gh-sprint"}}]`)
		case "/rest/api/2/issue/AIT-1":
			if got := request.URL.Query().Get("fields"); got != "customfield_10001" {
				t.Fatalf("fields = %q", got)
			}
			response, _ := json.Marshal(map[string]any{
				"id": "1", "key": "AIT-1", "fields": map[string]any{"customfield_10001": []any{legacy, malformed}},
			})
			_, _ = writer.Write(response)
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := newFieldCacheTestApp(stdout, stderr, fieldcache.New(t.TempDir(), nil))
	code := a.execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "-ojson", "issue", "show", "AIT-1", "--fields", "Iteration",
	})
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var envelope struct {
		Data struct {
			Key            string                  `json:"key"`
			Fields         map[string]any          `json:"fields"`
			Sprints        []jira.SprintMembership `json:"sprints"`
			FieldSelection []jira.FieldSelection   `json:"fieldSelection"`
		} `json:"data"`
		Warnings []struct {
			Code    string `json:"code"`
			Details struct {
				IssueKey string                         `json:"issueKey"`
				FieldID  string                         `json:"fieldId"`
				Failures []jira.SprintMembershipFailure `json:"failures"`
			} `json:"details"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Key != "AIT-1" || !reflect.DeepEqual(envelope.Data.Sprints, []jira.SprintMembership{{ID: 1839, Name: "AIT-4.5-S2", State: "active", BoardID: 12}}) {
		t.Fatalf("data = %#v; stdout=%s", envelope.Data, stdout.String())
	}
	raw := envelope.Data.Fields["customfield_10001"].([]any)
	if !reflect.DeepEqual(raw, []any{legacy, malformed}) {
		t.Fatalf("raw = %#v", raw)
	}
	if len(envelope.Warnings) != 1 || envelope.Warnings[0].Code != "sprint_membership_normalization" ||
		envelope.Warnings[0].Details.IssueKey != "AIT-1" || envelope.Warnings[0].Details.FieldID != "customfield_10001" ||
		!reflect.DeepEqual(envelope.Warnings[0].Details.Failures, []jira.SprintMembershipFailure{{Index: 1, Property: "id", Reason: "positive integer id is required"}}) {
		t.Fatalf("warnings = %#v", envelope.Warnings)
	}
}

func TestStaleSprintMetadataRetainsSemanticIdentity(t *testing.T) {
	clearCommandEnv(t)
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	store := fieldcache.New(t.TempDir(), func() time.Time { return now })
	var fieldCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/myself":
			writeTestPrincipal(writer)
		case "/rest/api/2/field":
			if fieldCalls.Add(1) == 1 {
				_, _ = io.WriteString(writer, `[{"id":"customfield_10001","name":"Iteration","custom":true,"schema":{"type":"array","custom":"com.pyxis.greenhopper.jira:gh-sprint"}}]`)
				return
			}
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(writer, `{"errorMessages":["metadata unavailable"]}`)
		case "/rest/api/2/issue/AIT-1":
			_, _ = io.WriteString(writer, `{"id":"1","key":"AIT-1","fields":{"customfield_10001":["Sprint@[id=1839,state=ACTIVE,name=S1]"]}}`)
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()
	config := writeCLIConfig(t, server.URL, false)

	for run := 0; run < 2; run++ {
		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		a := newFieldCacheTestApp(stdout, stderr, store)
		code := a.execute([]string{"--config", config, "-ojson", "issue", "show", "AIT-1", "--fields", "Iteration"})
		if code != 0 || stderr.Len() != 0 {
			t.Fatalf("run=%d code=%d stdout=%s stderr=%s", run, code, stdout.String(), stderr.String())
		}
		var envelope struct {
			Data struct {
				Sprints []jira.SprintMembership `json:"sprints"`
			} `json:"data"`
			Warnings []struct {
				Code string `json:"code"`
			} `json:"warnings"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(envelope.Data.Sprints, []jira.SprintMembership{{ID: 1839, Name: "S1", State: "active"}}) {
			t.Fatalf("run=%d sprints=%#v", run, envelope.Data.Sprints)
		}
		if run == 0 && len(envelope.Warnings) != 0 {
			t.Fatalf("initial warnings = %#v", envelope.Warnings)
		}
		if run == 1 && (len(envelope.Warnings) != 1 || envelope.Warnings[0].Code != "stale_field_cache") {
			t.Fatalf("stale warnings = %#v", envelope.Warnings)
		}
		now = now.Add(25 * time.Hour)
	}
}

func TestSearchSprintMembershipWarningsFollowIssueOrder(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/myself":
			writeTestPrincipal(writer)
		case "/rest/api/2/field":
			_, _ = io.WriteString(writer, `[{"id":"customfield_10001","name":"Sprint","custom":true,"schema":{"type":"array","custom":"com.pyxis.greenhopper.jira:gh-sprint"}}]`)
		case "/rest/api/2/search":
			_, _ = io.WriteString(writer, `{
				"startAt":0,"maxResults":50,"total":2,"issues":[
					{"id":"2","key":"AIT-2","fields":{"customfield_10001":["Sprint@[name=missing]"]}},
					{"id":"1","key":"AIT-1","fields":{"customfield_10001":["Sprint@[id=7,name=S1]","Sprint@[id=7,name=S1]","broken"]}}
				]}`)
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := newFieldCacheTestApp(stdout, stderr, fieldcache.New(t.TempDir(), nil))
	code := a.execute([]string{"--config", writeCLIConfig(t, server.URL, false), "-ojson", "search", "project = AIT", "--fields", "Sprint"})
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var envelope struct {
		Data struct {
			Issues []jira.Issue `json:"issues"`
		} `json:"data"`
		Warnings []struct {
			Code    string `json:"code"`
			Details struct {
				IssueKey string `json:"issueKey"`
			} `json:"details"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.Issues) != 2 || envelope.Data.Issues[0].Sprints == nil || len(*envelope.Data.Issues[0].Sprints) != 0 ||
		envelope.Data.Issues[1].Sprints == nil || len(*envelope.Data.Issues[1].Sprints) != 2 || (*envelope.Data.Issues[1].Sprints)[0].ID != 7 || (*envelope.Data.Issues[1].Sprints)[1].ID != 7 {
		t.Fatalf("issues = %#v", envelope.Data.Issues)
	}
	if len(envelope.Warnings) != 2 || envelope.Warnings[0].Code != "sprint_membership_normalization" || envelope.Warnings[0].Details.IssueKey != "AIT-2" || envelope.Warnings[1].Details.IssueKey != "AIT-1" {
		t.Fatalf("warnings = %#v", envelope.Warnings)
	}
}

func TestSprintMembershipWarningUsesTextStderr(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/myself":
			writeTestPrincipal(writer)
		case "/rest/api/2/field":
			_, _ = io.WriteString(writer, `[{"id":"customfield_10001","name":"Sprint","custom":true,"schema":{"type":"array","custom":"com.pyxis.greenhopper.jira:gh-sprint"}}]`)
		case "/rest/api/2/issue/AIT-1":
			_, _ = io.WriteString(writer, `{"id":"1","key":"AIT-1","fields":{"customfield_10001":["broken"]}}`)
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := newFieldCacheTestApp(stdout, stderr, fieldcache.New(t.TempDir(), nil))
	code := a.execute([]string{"--config", writeCLIConfig(t, server.URL, false), "issue", "show", "AIT-1", "--fields", "Sprint"})
	if code != 0 || stderr.String() != "warning: some Sprint memberships could not be fully normalized\n" || stdout.Len() == 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestExcludedSprintSelectorDoesNotTriggerNormalization(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/myself":
			writeTestPrincipal(writer)
		case "/rest/api/2/field":
			_, _ = io.WriteString(writer, `[{"id":"customfield_10001","name":"Sprint","custom":true,"schema":{"type":"array","custom":"com.pyxis.greenhopper.jira:gh-sprint"}}]`)
		case "/rest/api/2/search":
			var payload struct {
				Fields []string `json:"fields"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(payload.Fields, []string{"-customfield_10001"}) {
				t.Fatalf("fields = %#v", payload.Fields)
			}
			_, _ = io.WriteString(writer, `{"startAt":0,"maxResults":50,"total":1,"issues":[{"id":"1","key":"AIT-1","fields":{"customfield_10001":["Sprint@[id=7]"]}}]}`)
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := newFieldCacheTestApp(stdout, stderr, fieldcache.New(t.TempDir(), nil))
	code := a.execute([]string{"--config", writeCLIConfig(t, server.URL, false), "-ojson", "search", "project = AIT", "--fields=-Sprint"})
	if code != 0 || stderr.Len() != 0 || strings.Contains(stdout.String(), `"sprints"`) {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestDirectSprintFieldIDRemainsRawWhenOtherSelectorLoadsMetadata(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/myself":
			writeTestPrincipal(writer)
		case "/rest/api/2/field":
			_, _ = io.WriteString(writer, `[
				{"id":"summary","name":"Summary","custom":false,"schema":{"type":"string"}},
				{"id":"customfield_10001","name":"Sprint","custom":true,"schema":{"type":"array","custom":"com.pyxis.greenhopper.jira:gh-sprint"}}
			]`)
		case "/rest/api/2/search":
			_, _ = io.WriteString(writer, `{"startAt":0,"maxResults":50,"total":1,"issues":[{"id":"1","key":"AIT-1","fields":{"summary":"One","customfield_10001":["Sprint@[id=7]"]}}]}`)
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := newFieldCacheTestApp(stdout, stderr, fieldcache.New(t.TempDir(), nil))
	code := a.execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "-ojson", "search", "project = AIT", "--fields", "summary,customfield_10001",
	})
	if code != 0 || stderr.Len() != 0 || strings.Contains(stdout.String(), `"sprints"`) {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestIssueShowWarnsForNonArraySprintValue(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/rest/api/2/myself":
			writeTestPrincipal(writer)
		case "/rest/api/2/field":
			_, _ = io.WriteString(writer, `[{"id":"customfield_10001","name":"Sprint","custom":true,"schema":{"type":"array","custom":"com.pyxis.greenhopper.jira:gh-sprint"}}]`)
		case "/rest/api/2/issue/AIT-1":
			_, _ = io.WriteString(writer, `{"id":"1","key":"AIT-1","fields":{"customfield_10001":9}}`)
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	a := newFieldCacheTestApp(stdout, stderr, fieldcache.New(t.TempDir(), nil))
	code := a.execute([]string{"--config", writeCLIConfig(t, server.URL, false), "-ojson", "issue", "show", "AIT-1", "--fields", "Sprint"})
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var envelope struct {
		Data struct {
			Sprints []jira.SprintMembership `json:"sprints"`
		} `json:"data"`
		Warnings []struct {
			Code    string `json:"code"`
			Details struct {
				Failures []jira.SprintMembershipFailure `json:"failures"`
			} `json:"details"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Sprints == nil || len(envelope.Data.Sprints) != 0 || len(envelope.Warnings) != 1 || envelope.Warnings[0].Code != "sprint_membership_normalization" ||
		!reflect.DeepEqual(envelope.Warnings[0].Details.Failures, []jira.SprintMembershipFailure{{Index: 0, Reason: "Sprint membership must be a legacy string or object"}}) {
		t.Fatalf("data=%#v warnings=%#v", envelope.Data, envelope.Warnings)
	}
}
