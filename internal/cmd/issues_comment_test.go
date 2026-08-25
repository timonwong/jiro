package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIssueCommentEditConvertsJFMAndReadsBack(t *testing.T) {
	clearCommandEnv(t)
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/rest/api/2/issue/OPS-1/comment/7":
			var payload map[string]string
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload["body"] != "h1. Updated" {
				t.Fatalf("PUT payload = %#v", payload)
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/2/issue/OPS-1/comment/7":
			_, _ = io.WriteString(w, `{"id":"7","body":"h1. Updated","author":{"name":"tpwang","displayName":"Timon"},"created":"2026-08-25T01:00:00.000+0000","updated":"2026-08-25T02:00:00.000+0000"}`)
		default:
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "--output=json",
		"issue", "comment", "edit", "OPS-1", "7", "--body", "# Updated", "--input-format=jfm",
	}, strings.NewReader(""), stdout, stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if len(requests) != 2 || requests[0] != "PUT /rest/api/2/issue/OPS-1/comment/7" || requests[1] != "GET /rest/api/2/issue/OPS-1/comment/7" {
		t.Fatalf("requests = %#v", requests)
	}
	if !strings.Contains(stdout.String(), `"body":"h1. Updated"`) || !strings.Contains(stdout.String(), `"updated":"2026-08-25T02:00:00.000+0000"`) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestIssueCommentEditReadbackFailureIsPartial(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, `{"errorMessages":["readback unavailable"]}`, http.StatusInternalServerError)
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, false), "--output=json",
		"issue", "comment", "edit", "OPS-1", "7", "--body", "updated",
	}, strings.NewReader(""), stdout, stderr)
	if code != 7 || !strings.Contains(stdout.String(), `"issueKey":"OPS-1"`) || !strings.Contains(stdout.String(), `"commentID":"7"`) || !strings.Contains(stdout.String(), `"updated":true`) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), `"kind":"partial_failure"`) || !strings.Contains(stderr.String(), "read-back failed") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestIssueCommentEditRequiresBody(t *testing.T) {
	clearCommandEnv(t)
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{
		"--config", writeCLIConfig(t, server.URL, false),
		"issue", "comment", "edit", "OPS-1", "7",
	}, strings.NewReader(""), stdout, stderr)
	if code != 2 || requests != 0 || stdout.Len() != 0 || stderr.String() != "comment body is required\n" {
		t.Fatalf("code=%d requests=%d stdout=%q stderr=%q", code, requests, stdout.String(), stderr.String())
	}
}
