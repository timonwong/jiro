package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/timonwong/jiro/internal/apperr"
)

func TestUpdateCommentReplacesBodyAndReadsBack(t *testing.T) {
	t.Parallel()
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
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
	comment, err := client.UpdateComment(context.Background(), "OPS-1", "7", CommentInput{Body: "updated"})
	if err != nil {
		t.Fatal(err)
	}
	if comment.ID != "7" || comment.Body != "updated" || comment.Author == nil || comment.Author.Username != "tpwang" || comment.Updated == "" {
		t.Fatalf("UpdateComment() = %#v", comment)
	}
	if len(requests) != 2 || requests[0] != "PUT /rest/api/2/issue/OPS-1/comment/7" || requests[1] != "GET /rest/api/2/issue/OPS-1/comment/7" {
		t.Fatalf("requests = %#v", requests)
	}
}

func TestUpdateCommentReadbackFailureIsPartial(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, `{"errorMessages":["readback unavailable"]}`, http.StatusInternalServerError)
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.UpdateComment(context.Background(), "OPS-1", "7", CommentInput{Body: "updated"})
	if apperr.As(err).Kind != apperr.KindPartialFailure {
		t.Fatalf("UpdateComment() error = %v, kind = %s", err, apperr.As(err).Kind)
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
		if _, err := client.UpdateComment(context.Background(), input.key, input.id, CommentInput{Body: input.body}); apperr.As(err).Kind != apperr.KindInvalidInput {
			t.Fatalf("UpdateComment(%q, %q, %q) error = %v", input.key, input.id, input.body, err)
		}
	}
}
