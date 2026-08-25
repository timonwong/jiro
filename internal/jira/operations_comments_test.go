package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/timonwong/jiro/internal/apperr"
)

func TestUpdateCommentReplacesBody(t *testing.T) {
	t.Parallel()
	requests := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Method + " " + r.URL.Path
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/rest/api/2/issue/OPS-1/comment/7":
			var payload CommentInput
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload.Body != "updated" {
				t.Fatalf("PUT body = %#v", payload)
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/2/issue/OPS-1/comment/7":
			_, _ = w.Write([]byte(`{"id":"7","body":"updated","author":{"name":"tpwang","displayName":"Timon"},"created":"2026-08-25T01:00:00.000+0000","updated":"2026-08-25T02:00:00.000+0000"}`))
		default:
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.UpdateComment(context.Background(), "OPS-1", "7", CommentInput{Body: "updated"}); err != nil {
		t.Fatal(err)
	}
	comment, err := client.ShowComment(context.Background(), "OPS-1", "7")
	if err != nil {
		t.Fatal(err)
	}
	if request := <-requests; request != "PUT /rest/api/2/issue/OPS-1/comment/7" {
		t.Fatalf("first request = %q", request)
	}
	if request := <-requests; request != "GET /rest/api/2/issue/OPS-1/comment/7" {
		t.Fatalf("second request = %q", request)
	}
	if comment.ID != "7" || comment.Body != "updated" || comment.Author == nil || comment.Author.Username != "tpwang" || comment.Updated == "" {
		t.Fatalf("UpdateComment() = %#v", comment)
	}
}

func TestUpdateCommentRejectsMissingInput(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("invalid comment edit must fail before network access")
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []struct {
		key, id, body string
	}{
		{"", "7", "body"}, {"OPS-1", "", "body"}, {"OPS-1", "7", ""},
	} {
		if err := client.UpdateComment(context.Background(), input.key, input.id, CommentInput{Body: input.body}); apperr.As(err).Kind != apperr.KindInvalidInput {
			t.Fatalf("UpdateComment(%q, %q, %q) error = %v", input.key, input.id, input.body, err)
		}
	}
}
