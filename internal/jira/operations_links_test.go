package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIssueLinksAreNormalizedAndManageable(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/2/issue/DEMO-1":
			if got := r.URL.Query().Get("fields"); got != "issuelinks" {
				t.Fatalf("fields = %q", got)
			}
			_, _ = w.Write([]byte(`{"id":"1","key":"DEMO-1","fields":{"issuelinks":[{"id":"7","type":{"id":"10000","name":"Blocks","inward":"is blocked by","outward":"blocks"},"outwardIssue":{"id":"2","key":"DEMO-2","fields":{"summary":"Blocked work"}}},{"id":"8","type":{"id":"10001","name":"Relates","inward":"relates to","outward":"relates to"},"inwardIssue":{"id":"3","key":"DEMO-3","fields":{"summary":"Related work"}}}]}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/2/issueLinkType":
			_, _ = w.Write([]byte(`{"issueLinkTypes":[{"id":"10000","name":"Blocks","inward":"is blocked by","outward":"blocks"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/rest/api/2/issueLink":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if got := payload["type"].(map[string]any)["id"]; got != "10000" {
				t.Fatalf("link type id = %#v", got)
			}
			if got := payload["outwardIssue"].(map[string]any)["key"]; got != "DEMO-1" {
				t.Fatalf("outward issue = %#v", got)
			}
			if got := payload["inwardIssue"].(map[string]any)["key"]; got != "DEMO-2" {
				t.Fatalf("inward issue = %#v", got)
			}
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodDelete && r.URL.Path == "/rest/api/2/issueLink/7":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	links, err := client.ListIssueLinks(context.Background(), "DEMO-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 2 || links[0].Direction != "outward" || links[0].Relationship != "blocks" || links[0].OtherIssue.Key != "DEMO-2" || links[1].Direction != "inward" || links[1].Relationship != "relates to" {
		t.Fatalf("ListIssueLinks() = %#v", links)
	}
	linkTypes, err := client.ListIssueLinkTypes(context.Background())
	if err != nil || len(linkTypes) != 1 || linkTypes[0].Outward != "blocks" {
		t.Fatalf("ListIssueLinkTypes() = %#v, %v", linkTypes, err)
	}
	if err := client.CreateIssueLink(context.Background(), IssueLinkInput{From: "DEMO-1", To: "DEMO-2", TypeID: linkTypes[0].ID}); err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteIssueLink(context.Background(), "7"); err != nil {
		t.Fatal(err)
	}
}

func TestCreateIssueLinkUsesResolvedNumericTypeWithoutLookup(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/rest/api/2/issueLink" {
			t.Fatalf("request = %s %s", r.Method, r.URL.String())
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if got := payload["type"].(map[string]any)["id"]; got != "10000" {
			t.Fatalf("link type id = %#v", got)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.CreateIssueLink(context.Background(), IssueLinkInput{From: "DEMO-1", To: "DEMO-2", TypeID: "10000"}); err != nil {
		t.Fatal(err)
	}
}

func TestCreateIssueLinkRejectsInvalidTypeIDBeforeRequest(t *testing.T) {
	t.Parallel()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests++
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.CreateIssueLink(context.Background(), IssueLinkInput{From: "DEMO-1", To: "DEMO-2", TypeID: "invalid"}); err == nil {
		t.Fatal("CreateIssueLink() error = nil, want invalid link type ID")
	}
	if requests != 0 {
		t.Fatalf("requests = %d, want 0", requests)
	}
}

func TestDeleteIssueLinkRejectsNonNumericID(t *testing.T) {
	t.Parallel()
	client, err := NewClient(Config{BaseURL: "https://jira.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteIssueLink(context.Background(), "DEMO-1"); err == nil {
		t.Fatal("DeleteIssueLink() error = nil, want invalid link ID")
	}
}
