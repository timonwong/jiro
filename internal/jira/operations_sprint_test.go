package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/timonwong/jiro/internal/apperr"
)

func TestListAllBoardsFollowsEveryPageInJiraOrder(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/agile/1.0/board" {
			t.Fatalf("request = %s %s", r.Method, r.URL.String())
		}
		if got := r.URL.Query().Get("maxResults"); got != "50" {
			t.Fatalf("maxResults = %q, want 50", got)
		}
		switch r.URL.Query().Get("startAt") {
		case "":
			_, _ = w.Write([]byte(`{"startAt":0,"maxResults":2,"total":3,"values":[{"id":7,"name":"Platform","type":"scrum"},{"id":3,"name":"Operations","type":"kanban"}]}`))
		case "2":
			_, _ = w.Write([]byte(`{"startAt":2,"maxResults":2,"total":3,"values":[{"id":11,"name":"Release","type":"scrum"}]}`))
		default:
			t.Fatalf("unexpected board page = %s", r.URL.String())
		}
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	boards, err := client.ListAllBoards(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []Board{
		{ID: 7, Name: "Platform", Type: "scrum"},
		{ID: 3, Name: "Operations", Type: "kanban"},
		{ID: 11, Name: "Release", Type: "scrum"},
	}
	if !reflect.DeepEqual(boards, want) {
		t.Fatalf("ListAllBoards() = %#v, want %#v", boards, want)
	}
}

func TestListAllBoardsRejectsNonAdvancingPagination(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			_, _ = w.Write([]byte(`{"startAt":0,"maxResults":1,"total":2,"values":[{"id":7,"name":"Platform","type":"scrum"}]}`))
		case 2:
			_, _ = w.Write([]byte(`{"startAt":0,"maxResults":1,"total":2,"values":[{"id":7,"name":"Platform","type":"scrum"}]}`))
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	boards, err := client.ListAllBoards(context.Background())
	if err == nil || !strings.Contains(err.Error(), "pagination did not advance") {
		t.Fatalf("ListAllBoards() = %#v, %v", boards, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
}

func TestListAllBoardsRejectsEmptyPageBeforeTotal(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("startAt") {
		case "":
			_, _ = w.Write([]byte(`{"startAt":0,"maxResults":1,"total":3,"values":[{"id":7,"name":"Platform","type":"scrum"}]}`))
		case "1":
			_, _ = w.Write([]byte(`{"startAt":1,"maxResults":1,"total":3,"isLast":false,"values":[]}`))
		default:
			t.Fatalf("unexpected board page = %s", r.URL.String())
		}
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	boards, err := client.ListAllBoards(context.Background())
	if err == nil || !strings.Contains(err.Error(), "ended before completion") || boards != nil {
		t.Fatalf("ListAllBoards() = %#v, %v", boards, err)
	}
}

func TestListBoardsRejectsMalformedSuccessfulPage(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"empty body":           "",
		"null body":            "null",
		"missing values":       `{}`,
		"null values":          `{"values":null}`,
		"invalid ID":           `{"values":[{"id":0,"name":"Broken"}]}`,
		"non-final empty page": `{"isLast":false,"values":[]}`,
		"premature final page": `{"total":2,"isLast":true,"values":[{"id":7,"name":"Platform"}]}`,
		"non-final at total":   `{"total":1,"isLast":false,"values":[{"id":7,"name":"Platform"}]}`,
		"final markers differ": `{"total":1,"isLast":true,"isLastPage":false,"values":[{"id":7,"name":"Platform"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()

			client, err := NewClient(Config{BaseURL: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.ListBoards(context.Background(), Page{}); err == nil {
				t.Fatal("ListBoards() error = nil")
			}
		})
	}
}

func TestListAllSprintsFiltersStateAndPreservesQueriedAndOriginBoardIdentity(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/agile/1.0/board/12/sprint" {
			t.Fatalf("request = %s %s", r.Method, r.URL.String())
		}
		if got := r.URL.Query().Get("maxResults"); got != "50" {
			t.Fatalf("maxResults = %q, want 50", got)
		}
		if got := r.URL.Query().Get("state"); got != "active" {
			t.Fatalf("state = %q, want active", got)
		}
		switch r.URL.Query().Get("startAt") {
		case "":
			_, _ = w.Write([]byte(`{"startAt":0,"maxResults":1,"total":2,"values":[{"id":41,"name":"Sprint 13","state":"active","originBoardId":7,"goal":"stabilize","startDate":"2026-08-01","endDate":"2026-08-14"}]}`))
		case "1":
			_, _ = w.Write([]byte(`{"startAt":1,"maxResults":1,"total":2,"values":[{"id":42,"name":"Sprint 14","state":"active","originBoardId":7,"goal":"ship","startDate":"2026-08-15","endDate":"2026-08-28","completeDate":"2026-08-29"}]}`))
		default:
			t.Fatalf("unexpected sprint page = %s", r.URL.String())
		}
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	sprints, err := client.ListAllSprints(context.Background(), 12, SprintStateActive)
	if err != nil {
		t.Fatal(err)
	}
	want := []Sprint{
		{ID: 41, Name: "Sprint 13", State: "active", BoardID: 12, OriginBoardID: 7, Goal: "stabilize", StartDate: "2026-08-01", EndDate: "2026-08-14"},
		{ID: 42, Name: "Sprint 14", State: "active", BoardID: 12, OriginBoardID: 7, Goal: "ship", StartDate: "2026-08-15", EndDate: "2026-08-28", CompleteDate: "2026-08-29"},
	}
	if !reflect.DeepEqual(sprints, want) {
		t.Fatalf("ListAllSprints() = %#v, want %#v", sprints, want)
	}
}

func TestListAllSprintsReturnsRowsFetchedBeforePageFailure(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("startAt") {
		case "":
			_, _ = w.Write([]byte(`{"startAt":0,"maxResults":1,"total":2,"values":[{"id":41,"name":"Sprint 13","state":"active","originBoardId":7}]}`))
		case "1":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"errorMessages":["agile service unavailable"]}`))
		default:
			t.Fatalf("unexpected sprint page = %s", r.URL.String())
		}
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	sprints, err := client.ListAllSprints(context.Background(), 12, SprintStateActive)
	if err == nil || !strings.Contains(err.Error(), "agile service unavailable") {
		t.Fatalf("ListAllSprints() = %#v, %v", sprints, err)
	}
	want := []Sprint{{ID: 41, Name: "Sprint 13", State: "active", BoardID: 12, OriginBoardID: 7}}
	if !reflect.DeepEqual(sprints, want) {
		t.Fatalf("ListAllSprints() = %#v, want %#v", sprints, want)
	}
}

func TestListAllSprintsReturnsRowsBeforeEmptyPageAheadOfTotal(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("startAt") {
		case "":
			_, _ = w.Write([]byte(`{"startAt":0,"maxResults":1,"total":3,"values":[{"id":41,"name":"Sprint 13","state":"active","originBoardId":7}]}`))
		case "1":
			_, _ = w.Write([]byte(`{"startAt":1,"maxResults":1,"total":3,"isLast":false,"values":[]}`))
		default:
			t.Fatalf("unexpected sprint page = %s", r.URL.String())
		}
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	sprints, err := client.ListAllSprints(context.Background(), 12, SprintStateActive)
	if err == nil || !strings.Contains(err.Error(), "ended before completion") {
		t.Fatalf("ListAllSprints() = %#v, %v", sprints, err)
	}
	want := []Sprint{{ID: 41, Name: "Sprint 13", State: "active", BoardID: 12, OriginBoardID: 7}}
	if !reflect.DeepEqual(sprints, want) {
		t.Fatalf("ListAllSprints() = %#v, want %#v", sprints, want)
	}
}

func TestListAllSprintsRejectsPageWithoutNewRelationships(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("startAt") {
		case "":
			_, _ = w.Write([]byte(`{"startAt":0,"maxResults":1,"total":2,"values":[{"id":41,"name":"Sprint 13","state":"active","originBoardId":7}]}`))
		case "1":
			_, _ = w.Write([]byte(`{"startAt":1,"maxResults":1,"total":2,"values":[{"id":41,"name":"Sprint 13","state":"active","originBoardId":7}]}`))
		default:
			t.Fatalf("unexpected sprint page = %s", r.URL.String())
		}
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	sprints, err := client.ListAllSprints(context.Background(), 12, SprintStateActive)
	if err == nil || !strings.Contains(err.Error(), "returned no new Sprints") {
		t.Fatalf("ListAllSprints() = %#v, %v", sprints, err)
	}
	want := []Sprint{{ID: 41, Name: "Sprint 13", State: "active", BoardID: 12, OriginBoardID: 7}}
	if !reflect.DeepEqual(sprints, want) {
		t.Fatalf("ListAllSprints() = %#v, want %#v", sprints, want)
	}
}

func TestListSprintsRejectsMalformedSuccessfulPage(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"empty body":           "",
		"null body":            "null",
		"missing values":       `{}`,
		"null values":          `{"values":null}`,
		"invalid ID":           `{"values":[{"id":0,"name":"Broken"}]}`,
		"non-final empty page": `{"isLast":false,"values":[]}`,
		"premature final page": `{"total":2,"isLast":true,"values":[{"id":41,"name":"Sprint 13"}]}`,
		"non-final at total":   `{"total":1,"isLast":false,"values":[{"id":41,"name":"Sprint 13"}]}`,
		"final markers differ": `{"total":1,"isLast":true,"isLastPage":false,"values":[{"id":41,"name":"Sprint 13"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()

			client, err := NewClient(Config{BaseURL: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.ListSprints(context.Background(), 12, Page{}); err == nil {
				t.Fatal("ListSprints() error = nil")
			}
		})
	}
}

func TestListAllSprintsAllOmitsStateQuery(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, present := r.URL.Query()["state"]; present {
			t.Fatalf("state query must be omitted: %s", r.URL.String())
		}
		_, _ = w.Write([]byte(`{"startAt":0,"maxResults":50,"isLast":true,"values":[]}`))
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	sprints, err := client.ListAllSprints(context.Background(), 12, SprintStateAll)
	if err != nil || len(sprints) != 0 {
		t.Fatalf("ListAllSprints() = %#v, %v", sprints, err)
	}
}

func TestParseSprintStateNormalizesCaseAndRejectsInvalidValues(t *testing.T) {
	t.Parallel()
	for input, want := range map[string]SprintState{
		"ACTIVE":   SprintStateActive,
		" Closed ": SprintStateClosed,
		"Future":   SprintStateFuture,
		"all":      SprintStateAll,
	} {
		got, err := ParseSprintState(input)
		if err != nil || got != want {
			t.Fatalf("ParseSprintState(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"", "open", "active,future"} {
		if _, err := ParseSprintState(input); err == nil {
			t.Fatalf("ParseSprintState(%q) error = nil", input)
		}
	}
}

func TestMoveIssueToSprintUsesFirstNameMatchAcrossBoardAndSprintPages(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/agile/1.0/board":
			switch r.URL.Query().Get("startAt") {
			case "":
				_, _ = w.Write([]byte(`{"startAt":0,"maxResults":1,"total":2,"values":[{"id":1,"name":"Team A","type":"scrum"}]}`))
			case "1":
				_, _ = w.Write([]byte(`{"startAt":1,"maxResults":1,"total":2,"values":[{"id":2,"name":"Team B","type":"scrum"}]}`))
			default:
				t.Fatalf("unexpected board page = %s", r.URL.String())
			}
		case "/rest/agile/1.0/board/1/sprint":
			switch r.URL.Query().Get("startAt") {
			case "":
				_, _ = w.Write([]byte(`{"startAt":0,"maxResults":1,"total":2,"values":[{"id":10,"name":"Planning","state":"active","originBoardId":1}]}`))
			case "1":
				_, _ = w.Write([]byte(`{"startAt":1,"maxResults":1,"total":2,"values":[{"id":11,"name":"Release Candidate","state":"future","originBoardId":1}]}`))
			default:
				t.Fatalf("unexpected sprint page = %s", r.URL.String())
			}
		case "/rest/agile/1.0/sprint/11/issue":
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s", r.Method)
			}
			var payload struct {
				Issues []string `json:"issues"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if len(payload.Issues) != 1 || payload.Issues[0] != "DEMO-1" {
				t.Fatalf("payload = %#v", payload)
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
	if err := client.MoveIssueToSprint(context.Background(), "DEMO-1", MoveIssueToSprintInput{Sprint: "release"}); err != nil {
		t.Fatal(err)
	}
}

func TestResolveSprintFollowsIsLastWhenTotalIsOmitted(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/agile/1.0/board":
			switch r.URL.Query().Get("startAt") {
			case "":
				_, _ = w.Write([]byte(`{"startAt":0,"maxResults":1,"isLast":false,"values":[{"id":1,"name":"Team A","type":"scrum"}]}`))
			case "1":
				_, _ = w.Write([]byte(`{"startAt":1,"maxResults":1,"isLast":true,"values":[{"id":2,"name":"Team B","type":"scrum"}]}`))
			default:
				t.Fatalf("unexpected board page = %s", r.URL.String())
			}
		case "/rest/agile/1.0/board/1/sprint":
			_, _ = w.Write([]byte(`{"startAt":0,"maxResults":1,"isLast":true,"values":[{"id":10,"name":"Planning","state":"active","originBoardId":1}]}`))
		case "/rest/agile/1.0/board/2/sprint":
			switch r.URL.Query().Get("startAt") {
			case "":
				_, _ = w.Write([]byte(`{"startAt":0,"maxResults":1,"isLast":false,"values":[{"id":20,"name":"Hardening","state":"future","originBoardId":2}]}`))
			case "1":
				_, _ = w.Write([]byte(`{"startAt":1,"maxResults":1,"isLast":true,"values":[{"id":21,"name":"Release Train","state":"future","originBoardId":2}]}`))
			default:
				t.Fatalf("unexpected sprint page = %s", r.URL.String())
			}
		default:
			t.Fatalf("unexpected request = %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	sprint, err := client.ResolveSprint(context.Background(), "release", "")
	if err != nil || sprint.ID != 21 {
		t.Fatalf("ResolveSprint() = %#v, %v", sprint, err)
	}
}

func TestResolveSprintActiveFiltersStateServerSide(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/agile/1.0/board":
			_, _ = w.Write([]byte(`{"startAt":0,"maxResults":50,"total":2,"values":[{"id":1,"name":"Team A","type":"scrum"},{"id":2,"name":"Team B","type":"scrum"}]}`))
		case "/rest/agile/1.0/board/1/sprint":
			if got := r.URL.Query().Get("state"); got != "active" {
				t.Fatalf("state = %q, want active", got)
			}
			_, _ = w.Write([]byte(`{"startAt":0,"maxResults":50,"isLast":true,"values":[]}`))
		case "/rest/agile/1.0/board/2/sprint":
			if got := r.URL.Query().Get("state"); got != "active" {
				t.Fatalf("state = %q, want active", got)
			}
			_, _ = w.Write([]byte(`{"startAt":0,"maxResults":50,"isLast":true,"values":[{"id":23,"name":"Current","state":"active","originBoardId":2}]}`))
		default:
			t.Fatalf("unexpected request = %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	sprint, err := client.ResolveSprint(context.Background(), "active", "")
	if err != nil || sprint.ID != 23 {
		t.Fatalf("ResolveSprint() = %#v, %v", sprint, err)
	}
}

func TestResolveSprintNumericBoardHintSkipsBoardEnumeration(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/agile/1.0/board/2":
			_, _ = w.Write([]byte(`{"id":2,"name":"Team B","type":"scrum"}`))
		case "/rest/agile/1.0/board/2/sprint":
			_, _ = w.Write([]byte(`{"startAt":0,"maxResults":50,"isLast":true,"values":[{"id":21,"name":"Release Train","state":"future","originBoardId":2}]}`))
		case "/rest/agile/1.0/sprint/21/issue":
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
	if err := client.MoveIssueToSprint(context.Background(), "DEMO-1", MoveIssueToSprintInput{Sprint: "release", Board: "2"}); err != nil {
		t.Fatal(err)
	}
}

func TestResolveSprintBoardNameHintScopesResolutionToMatchingBoards(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/agile/1.0/board":
			_, _ = w.Write([]byte(`{"startAt":0,"maxResults":50,"total":3,"values":[{"id":1,"name":"Team A","type":"scrum"},{"id":2,"name":"Operations","type":"scrum"},{"id":3,"name":"Team B","type":"scrum"}]}`))
		case "/rest/agile/1.0/board/1/sprint":
			_, _ = w.Write([]byte(`{"startAt":0,"maxResults":50,"isLast":true,"values":[{"id":10,"name":"Planning","state":"future","originBoardId":1}]}`))
		case "/rest/agile/1.0/board/3/sprint":
			_, _ = w.Write([]byte(`{"startAt":0,"maxResults":50,"isLast":true,"values":[{"id":30,"name":"Release Train","state":"future","originBoardId":3}]}`))
		default:
			t.Fatalf("unexpected request = %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	sprint, err := client.ResolveSprint(context.Background(), "release", "team")
	if err != nil || sprint.ID != 30 || sprint.BoardID != 3 {
		t.Fatalf("ResolveSprint() = %#v, %v", sprint, err)
	}
}

func TestResolveSprintBoardHintNotFound(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/agile/1.0/board/9":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"errorMessages":["Board does not exist"]}`))
		case "/rest/agile/1.0/board":
			_, _ = w.Write([]byte(`{"startAt":0,"maxResults":50,"total":1,"values":[{"id":1,"name":"Team A","type":"scrum"}]}`))
		default:
			t.Fatalf("unexpected request = %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	for _, hint := range []string{"9", "missing"} {
		_, err := client.ResolveSprint(context.Background(), "release", hint)
		if got := apperr.As(err); err == nil || got.Kind != apperr.KindNotFound || got.Message != `board "`+hint+`" was not found` {
			t.Fatalf("ResolveSprint(hint=%q) error = %v", hint, err)
		}
	}
	if _, err := client.ResolveSprint(context.Background(), "release", "0"); apperr.As(err).Kind != apperr.KindInvalidInput {
		t.Fatalf("ResolveSprint(hint=0) error = %v", err)
	}
}

func TestResolveSprintNotFoundScansEveryBoardAndPageBeforeErroring(t *testing.T) {
	t.Parallel()
	var sprintPages atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/agile/1.0/board":
			_, _ = w.Write([]byte(`{"startAt":0,"maxResults":50,"total":2,"values":[{"id":1,"name":"Team A","type":"scrum"},{"id":2,"name":"Team B","type":"scrum"}]}`))
		case "/rest/agile/1.0/board/1/sprint", "/rest/agile/1.0/board/2/sprint":
			sprintPages.Add(1)
			switch r.URL.Query().Get("startAt") {
			case "":
				_, _ = w.Write([]byte(`{"startAt":0,"maxResults":1,"total":2,"values":[{"id":10,"name":"Planning","state":"future","originBoardId":1}]}`))
			case "1":
				_, _ = w.Write([]byte(`{"startAt":1,"maxResults":1,"total":2,"values":[{"id":11,"name":"Hardening","state":"future","originBoardId":1}]}`))
			default:
				t.Fatalf("unexpected sprint page = %s", r.URL.String())
			}
		default:
			t.Fatalf("unexpected request = %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ResolveSprint(context.Background(), "missing", "")
	if got := apperr.As(err); err == nil || got.Kind != apperr.KindInvalidInput || !strings.Contains(err.Error(), `sprint "missing" was not found`) {
		t.Fatalf("ResolveSprint() error = %v", err)
	}
	if sprintPages.Load() != 4 {
		t.Fatalf("sprint pages fetched = %d, want 4", sprintPages.Load())
	}
}

func TestShowBoardValidatesInputAndResponse(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/agile/1.0/board/7" {
			t.Fatalf("unexpected request = %s %s", r.Method, r.URL.String())
		}
		_, _ = w.Write([]byte(`{"id":8,"name":"Platform","type":"scrum"}`))
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ShowBoard(context.Background(), 0); apperr.As(err).Kind != apperr.KindInvalidInput {
		t.Fatalf("ShowBoard(0) error = %v", err)
	}
	if _, err := client.ShowBoard(context.Background(), 7); apperr.As(err).Kind != apperr.KindAPI {
		t.Fatalf("ShowBoard(7) mismatched ID error = %v", err)
	}
}
