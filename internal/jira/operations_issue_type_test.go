package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIssueTypeCompatibilityAndUpdateUseEditMetaAndResolvedID(t *testing.T) {
	t.Parallel()
	var updatePayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/2/issue/DEMO-1/editmeta":
			_, _ = w.Write([]byte(`{"fields":{"issuetype":{"operations":[],"allowedValues":[{"id":"10000","name":"Task","description":"Work","subtask":false},{"id":"10001","name":"Sub-task","subtask":true}]}}}`))
		case r.Method == http.MethodPut && r.URL.Path == "/rest/api/2/issue/DEMO-1":
			if err := json.NewDecoder(r.Body).Decode(&updatePayload); err != nil {
				t.Fatal(err)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request = %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	types, err := client.ListIssueTypesForIssue(context.Background(), "DEMO-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(types) != 2 || types[0] != (IssueType{ID: "10000", Name: "Task", Description: "Work"}) ||
		types[1] != (IssueType{ID: "10001", Name: "Sub-task", Subtask: true}) {
		t.Fatalf("ListIssueTypesForIssue() = %#v", types)
	}
	if err := client.UpdateIssueType(context.Background(), "DEMO-1", types[1].ID); err != nil {
		t.Fatal(err)
	}
	fields, ok := updatePayload["fields"].(map[string]any)
	if !ok {
		t.Fatalf("payload fields = %#v", updatePayload["fields"])
	}
	issueType, ok := fields["issuetype"].(map[string]any)
	if !ok || issueType["id"] != "10001" || len(issueType) != 1 {
		t.Fatalf("issuetype payload = %#v", fields["issuetype"])
	}
}

func TestListIssueTypesForIssueAllowsMissingEditField(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/rest/api/2/issue/DEMO-1/editmeta" {
			t.Fatalf("unexpected request = %s %s", r.Method, r.URL.String())
		}
		_, _ = w.Write([]byte(`{"fields":{"summary":{"operations":["set"]}}}`))
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	types, err := client.ListIssueTypesForIssue(context.Background(), "DEMO-1")
	if err != nil || types == nil || len(types) != 0 {
		t.Fatalf("ListIssueTypesForIssue() = %#v, %v", types, err)
	}
}
