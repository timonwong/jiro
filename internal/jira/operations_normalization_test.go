package jira

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestShowIssueNormalizesParentAndFixVersions(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/rest/api/2/issue/DEMO-2" {
			t.Fatalf("request = %s %s", r.Method, r.URL.String())
		}
		_, _ = w.Write([]byte(`{"id":"2","key":"DEMO-2","fields":{"summary":"Child","parent":{"id":"1","key":"DEMO-1","fields":{"summary":"Parent"}},"fixVersions":[{"id":"10","name":"4.4.0","archived":false,"released":true}]}}`))
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	issue, err := client.ShowIssue(context.Background(), "DEMO-2", nil)
	if err != nil {
		t.Fatal(err)
	}
	if issue.Parent == nil || issue.Parent.Key != "DEMO-1" || issue.Parent.Summary != "Parent" || len(issue.FixVersions) != 1 || issue.FixVersions[0] != (Version{ID: "10", Name: "4.4.0", Released: true}) {
		t.Fatalf("ShowIssue() = %#v", issue)
	}
}

func TestResolveSprintAcceptsActive(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/agile/1.0/board":
			_, _ = w.Write([]byte(`{"startAt":0,"maxResults":50,"total":1,"values":[{"id":3,"name":"Platform","type":"scrum"}]}`))
		case "/rest/agile/1.0/board/3/sprint":
			_, _ = w.Write([]byte(`{"startAt":0,"maxResults":50,"total":2,"values":[{"id":22,"name":"Future","state":"future"},{"id":23,"name":"Current","state":"active"}]}`))
		default:
			t.Fatalf("request = %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	sprint, err := client.ResolveSprint(context.Background(), "ACTIVE", "")
	if err != nil || sprint.ID != 23 || sprint.Name != "Current" {
		t.Fatalf("ResolveSprint() = %#v, %v", sprint, err)
	}
}

func TestResolveSprintRejectsEmptyAndNonPositiveIDs(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("invalid sprint specs must fail before network access")
	}))
	defer server.Close()
	client, err := NewClient(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range []string{"", "0", "-1"} {
		if _, err := client.ResolveSprint(context.Background(), spec, ""); err == nil {
			t.Fatalf("ResolveSprint(%q) error = nil", spec)
		}
	}
}
