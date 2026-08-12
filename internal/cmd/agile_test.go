package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/timonwong/jiro/internal/jira"
)

func TestBoardListFetchesEveryPageInJiraOrder(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/agile/1.0/board" {
			t.Fatalf("request = %s %s", r.Method, r.URL.String())
		}
		switch r.URL.Query().Get("startAt") {
		case "":
			_, _ = io.WriteString(w, `{"startAt":0,"maxResults":1,"total":2,"values":[{"id":12,"name":"Platform","type":"scrum"}]}`)
		case "1":
			_, _ = io.WriteString(w, `{"startAt":1,"maxResults":1,"total":2,"values":[{"id":3,"name":"Operations","type":"kanban"}]}`)
		default:
			t.Fatalf("unexpected board page = %s", r.URL.String())
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{"--config", writeCLIConfig(t, server.URL, true), "--output=json", "board", "list"}, strings.NewReader(""), stdout, stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var envelope struct {
		SchemaVersion string `json:"schemaVersion"`
		Data          struct {
			Total  int `json:"total"`
			Boards []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"boards"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.SchemaVersion != "1" || envelope.Data.Total != 2 || len(envelope.Data.Boards) != 2 ||
		envelope.Data.Boards[0].ID != 12 || envelope.Data.Boards[1].ID != 3 {
		t.Fatalf("envelope = %+v", envelope)
	}
}

func TestBoardListTextColumnsAndEmptyOutput(t *testing.T) {
	clearCommandEnv(t)
	t.Run("rows are headerless TSV outside a TTY", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"isLast":true,"values":[{"id":12,"name":"Platform","type":"scrum"}]}`)
		}))
		defer server.Close()

		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		code := Execute([]string{"--config", writeCLIConfig(t, server.URL, true), "board", "list"}, strings.NewReader(""), stdout, stderr)
		if code != 0 || stderr.Len() != 0 || stdout.String() != "12\tPlatform\tscrum\n" {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	})
	t.Run("empty result is silent even in a TTY", func(t *testing.T) {
		t.Setenv("JIRO_FORCE_TTY", "1")
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"isLast":true,"values":[]}`)
		}))
		defer server.Close()

		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		code := Execute([]string{"--config", writeCLIConfig(t, server.URL, true), "board", "list"}, strings.NewReader(""), stdout, stderr)
		if code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	})
}

func TestSprintListDefaultsActiveAndPreservesBoardRelationships(t *testing.T) {
	clearCommandEnv(t)
	var mu sync.Mutex
	requests := make([]string, 0, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.URL.String())
		mu.Unlock()
		switch r.URL.Path {
		case "/rest/agile/1.0/board":
			_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"isLast":true,"values":[{"id":12,"name":"Platform","type":"scrum"},{"id":18,"name":"Delivery","type":"scrum"}]}`)
		case "/rest/agile/1.0/board/12/sprint":
			if r.URL.Query().Get("state") != "active" {
				t.Fatalf("state = %q", r.URL.Query().Get("state"))
			}
			_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"isLast":true,"values":[{"id":42,"name":"Sprint 14","state":"active","originBoardId":7,"goal":"ship","startDate":"2026-08-01","endDate":"2026-08-14"}]}`)
		case "/rest/agile/1.0/board/18/sprint":
			if r.URL.Query().Get("state") != "active" {
				t.Fatalf("state = %q", r.URL.Query().Get("state"))
			}
			_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"isLast":true,"values":[{"id":42,"name":"Sprint 14","state":"active","originBoardId":7,"goal":"ship","startDate":"2026-08-01","endDate":"2026-08-14"}]}`)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{"--config", writeCLIConfig(t, server.URL, true), "--output=json", "sprint", "list"}, strings.NewReader(""), stdout, stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var envelope struct {
		Data struct {
			Total   int `json:"total"`
			Sprints []struct {
				ID            int    `json:"id"`
				BoardID       int    `json:"boardId"`
				BoardName     string `json:"boardName"`
				OriginBoardID int    `json:"originBoardId"`
				Goal          string `json:"goal"`
				StartDate     string `json:"startDate"`
				EndDate       string `json:"endDate"`
			} `json:"sprints"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Total != 2 || len(envelope.Data.Sprints) != 2 ||
		envelope.Data.Sprints[0].ID != 42 || envelope.Data.Sprints[0].BoardID != 12 || envelope.Data.Sprints[0].BoardName != "Platform" || envelope.Data.Sprints[0].OriginBoardID != 7 ||
		envelope.Data.Sprints[1].ID != 42 || envelope.Data.Sprints[1].BoardID != 18 || envelope.Data.Sprints[1].BoardName != "Delivery" || envelope.Data.Sprints[1].Goal != "ship" {
		t.Fatalf("data = %+v", envelope.Data)
	}
	// Boards are always listed before any sprint request; per-Board sprint
	// requests run concurrently, so their arrival order is not asserted.
	sort.Strings(requests[1:])
	wantRequests := []string{
		"/rest/agile/1.0/board?maxResults=50",
		"/rest/agile/1.0/board/12/sprint?maxResults=50&state=active",
		"/rest/agile/1.0/board/18/sprint?maxResults=50&state=active",
	}
	if strings.Join(requests, "\n") != strings.Join(wantRequests, "\n") {
		t.Fatalf("requests = %v, want %v", requests, wantRequests)
	}
}

func TestSprintListStateIsNormalizedLocally(t *testing.T) {
	clearCommandEnv(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/rest/agile/1.0/board":
			_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"isLast":true,"values":[{"id":12,"name":"Platform","type":"scrum"}]}`)
		case "/rest/agile/1.0/board/12/sprint":
			if _, present := r.URL.Query()["state"]; present {
				t.Fatalf("state query must be omitted for all: %s", r.URL.String())
			}
			_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"isLast":true,"values":[]}`)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()
	configPath := writeCLIConfig(t, server.URL, true)

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{"--config", configPath, "--output=json", "sprint", "list", "--state", "ALL"}, strings.NewReader(""), stdout, stderr)
	if code != 0 || stderr.Len() != 0 || calls.Load() != 2 || !strings.Contains(stdout.String(), `"total":0`) {
		t.Fatalf("code=%d calls=%d stdout=%s stderr=%s", code, calls.Load(), stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"--config", configPath, "--output=json", "sprint", "list", "--state", "open"}, strings.NewReader(""), stdout, stderr)
	if code != 2 || calls.Load() != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), `"kind":"invalid_input"`) {
		t.Fatalf("code=%d calls=%d stdout=%s stderr=%s", code, calls.Load(), stdout.String(), stderr.String())
	}
}

func TestSprintListBoardNameSelectorFansOutAcrossEverySubstringMatch(t *testing.T) {
	clearCommandEnv(t)
	var mu sync.Mutex
	requestedBoards := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/agile/1.0/board":
			_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"isLast":true,"values":[{"id":12,"name":"Platform","type":"scrum"},{"id":18,"name":"Operations","type":"scrum"},{"id":21,"name":"Data PLATFORM","type":"scrum"}]}`)
		case "/rest/agile/1.0/board/12/sprint", "/rest/agile/1.0/board/21/sprint":
			mu.Lock()
			requestedBoards = append(requestedBoards, r.URL.Path)
			mu.Unlock()
			_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"isLast":true,"values":[]}`)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{"--config", writeCLIConfig(t, server.URL, true), "--output=json", "sprint", "list", "--board", "plat"}, strings.NewReader(""), stdout, stderr)
	if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"total":0`) {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	sort.Strings(requestedBoards)
	if got := strings.Join(requestedBoards, ","); got != "/rest/agile/1.0/board/12/sprint,/rest/agile/1.0/board/21/sprint" {
		t.Fatalf("requested boards = %q", got)
	}
}

func TestSprintListBoardNameSelectorCanMatchOneBoard(t *testing.T) {
	clearCommandEnv(t)
	requestedSprintPaths := make([]string, 0, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/agile/1.0/board":
			_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"isLast":true,"values":[{"id":12,"name":"Platform","type":"scrum"},{"id":18,"name":"Operations","type":"scrum"},{"id":21,"name":"Data Platform","type":"scrum"}]}`)
		case "/rest/agile/1.0/board/18/sprint":
			requestedSprintPaths = append(requestedSprintPaths, r.URL.Path)
			_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"isLast":true,"values":[{"id":55,"name":"Operations 7","state":"active","originBoardId":18}]}`)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{"--config", writeCLIConfig(t, server.URL, true), "--output=json", "sprint", "list", "--board", "OPER"}, strings.NewReader(""), stdout, stderr)
	if code != 0 || stderr.Len() != 0 || len(requestedSprintPaths) != 1 || requestedSprintPaths[0] != "/rest/agile/1.0/board/18/sprint" ||
		!strings.Contains(stdout.String(), `"id":55`) || !strings.Contains(stdout.String(), `"boardName":"Operations"`) {
		t.Fatalf("code=%d paths=%v stdout=%s stderr=%s", code, requestedSprintPaths, stdout.String(), stderr.String())
	}
}

func TestSprintListRejectsEmptyAndNonPositiveBoardSelectorsBeforeNetwork(t *testing.T) {
	clearCommandEnv(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	defer server.Close()
	configPath := writeCLIConfig(t, server.URL, true)

	for _, selector := range []string{"", "   ", "0", "-4"} {
		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		code := Execute([]string{"--config", configPath, "--output=json", "sprint", "list", "--board", selector}, strings.NewReader(""), stdout, stderr)
		if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), `"kind":"invalid_input"`) {
			t.Fatalf("selector=%q code=%d stdout=%s stderr=%s", selector, code, stdout.String(), stderr.String())
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("network calls = %d, want 0", calls.Load())
	}
}

func TestSprintListBoardIDSelectorFetchesOneBoardWithoutEnumeration(t *testing.T) {
	clearCommandEnv(t)
	requested := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = append(requested, r.URL.Path)
		switch r.URL.Path {
		case "/rest/agile/1.0/board/18":
			_, _ = io.WriteString(w, `{"id":18,"name":"Platform","type":"scrum"}`)
		case "/rest/agile/1.0/board/18/sprint":
			_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"isLast":true,"values":[{"id":42,"name":"Sprint 14","state":"active","originBoardId":7}]}`)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{"--config", writeCLIConfig(t, server.URL, true), "--output=json", "sprint", "list", "--board", "18"}, strings.NewReader(""), stdout, stderr)
	if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"boardName":"Platform"`) {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if got := strings.Join(requested, ","); got != "/rest/agile/1.0/board/18,/rest/agile/1.0/board/18/sprint" {
		t.Fatalf("requested = %q", got)
	}
}

func TestSprintListDistinguishesEmptyBoardSetFromSelectorNotFound(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/agile/1.0/board":
			_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"isLast":true,"values":[]}`)
		case "/rest/agile/1.0/board/12":
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"errorMessages":["Board does not exist"]}`)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()
	configPath := writeCLIConfig(t, server.URL, true)

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{"--config", configPath, "--output=json", "sprint", "list"}, strings.NewReader(""), stdout, stderr)
	if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"total":0`) || !strings.Contains(stdout.String(), `"sprints":[]`) {
		t.Fatalf("bare code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	for _, selector := range []string{"12", "Platform"} {
		stdout.Reset()
		stderr.Reset()
		code = Execute([]string{"--config", configPath, "--output=json", "sprint", "list", "--board", selector}, strings.NewReader(""), stdout, stderr)
		if code != 4 || stdout.Len() != 0 || !strings.Contains(stderr.String(), `"kind":"not_found"`) ||
			!strings.Contains(stderr.String(), fmt.Sprintf(`board \"%s\" was not found`, selector)) {
			t.Fatalf("selector=%q code=%d stdout=%s stderr=%s", selector, code, stdout.String(), stderr.String())
		}
	}
}

func TestSprintListTextColumnsAndEmptyOutput(t *testing.T) {
	clearCommandEnv(t)
	t.Run("rows keep raw dates in headerless TSV columns", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/rest/agile/1.0/board":
				_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"isLast":true,"values":[{"id":12,"name":"Platform","type":"scrum"}]}`)
			case "/rest/agile/1.0/board/12/sprint":
				_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"isLast":true,"values":[{"id":42,"name":"Sprint 14","state":"active","originBoardId":7,"startDate":"2026-08-01"}]}`)
			default:
				t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
			}
		}))
		defer server.Close()

		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		code := Execute([]string{"--config", writeCLIConfig(t, server.URL, true), "sprint", "list"}, strings.NewReader(""), stdout, stderr)
		const want = "42\tSprint 14\tactive\t12\tPlatform\t2026-08-01\t\t\n"
		if code != 0 || stderr.Len() != 0 || stdout.String() != want {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	})
	t.Run("empty result is silent even in a TTY", func(t *testing.T) {
		t.Setenv("JIRO_FORCE_TTY", "1")
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/rest/agile/1.0/board":
				_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"isLast":true,"values":[]}`)
			default:
				t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
			}
		}))
		defer server.Close()

		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		code := Execute([]string{"--config", writeCLIConfig(t, server.URL, true), "sprint", "list"}, strings.NewReader(""), stdout, stderr)
		if code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	})
}

func TestSprintListContinuesAndPreservesPartialResults(t *testing.T) {
	clearCommandEnv(t)
	var mu sync.Mutex
	requested := make([]string, 0, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requested = append(requested, r.URL.Path)
		mu.Unlock()
		switch r.URL.Path {
		case "/rest/agile/1.0/board":
			_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"isLast":true,"values":[{"id":12,"name":"Platform","type":"scrum"},{"id":18,"name":"Delivery","type":"scrum"},{"id":21,"name":"Operations","type":"scrum"}]}`)
		case "/rest/agile/1.0/board/12/sprint":
			_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"isLast":true,"values":[{"id":42,"name":"Sprint 14","state":"active","originBoardId":7}]}`)
		case "/rest/agile/1.0/board/18/sprint":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"errorMessages":["agile service unavailable"]}`)
		case "/rest/agile/1.0/board/21/sprint":
			_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"isLast":true,"values":[]}`)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{"--config", writeCLIConfig(t, server.URL, true), "--output=json", "sprint", "list"}, strings.NewReader(""), stdout, stderr)
	if code != 7 || !strings.Contains(stderr.String(), `"kind":"partial_failure"`) {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var envelope struct {
		Data struct {
			Total        int `json:"total"`
			Sprints      []jira.Sprint
			FailedBoards []struct {
				BoardID   int    `json:"boardId"`
				BoardName string `json:"boardName"`
				Error     string `json:"error"`
			} `json:"failedBoards"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Total != 1 || len(envelope.Data.Sprints) != 1 || len(envelope.Data.FailedBoards) != 1 ||
		envelope.Data.FailedBoards[0].BoardID != 18 || envelope.Data.FailedBoards[0].BoardName != "Delivery" ||
		!strings.Contains(envelope.Data.FailedBoards[0].Error, "agile service unavailable") {
		t.Fatalf("data = %+v", envelope.Data)
	}
	// Sprint requests fan out concurrently after the board listing, so only
	// the request set is asserted, not the arrival order.
	sort.Strings(requested[1:])
	wantRequested := "/rest/agile/1.0/board,/rest/agile/1.0/board/12/sprint,/rest/agile/1.0/board/18/sprint,/rest/agile/1.0/board/21/sprint"
	if strings.Join(requested, ",") != wantRequested {
		t.Fatalf("requested = %v", requested)
	}
}

func TestSprintListPreservesRowsFetchedBeforeBoardPageFailure(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/agile/1.0/board":
			_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"isLast":true,"values":[{"id":12,"name":"Platform","type":"scrum"},{"id":18,"name":"Delivery","type":"scrum"}]}`)
		case "/rest/agile/1.0/board/12/sprint":
			switch r.URL.Query().Get("startAt") {
			case "":
				_, _ = io.WriteString(w, `{"startAt":0,"maxResults":1,"total":2,"values":[{"id":41,"name":"Sprint 13","state":"active","originBoardId":7}]}`)
			case "1":
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = io.WriteString(w, `{"errorMessages":["second page unavailable"]}`)
			default:
				t.Fatalf("unexpected request %s", r.URL.String())
			}
		case "/rest/agile/1.0/board/18/sprint":
			_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"isLast":true,"values":[{"id":42,"name":"Sprint 14","state":"active","originBoardId":18}]}`)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{"--config", writeCLIConfig(t, server.URL, true), "--output=json", "sprint", "list"}, strings.NewReader(""), stdout, stderr)
	if code != 7 || !strings.Contains(stderr.String(), `"kind":"partial_failure"`) {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var envelope struct {
		Data struct {
			Total        int `json:"total"`
			Sprints      []jira.Sprint
			FailedBoards []struct {
				BoardID int `json:"boardId"`
			} `json:"failedBoards"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Total != 2 || len(envelope.Data.Sprints) != 2 || len(envelope.Data.FailedBoards) != 1 || envelope.Data.FailedBoards[0].BoardID != 12 {
		t.Fatalf("data = %+v", envelope.Data)
	}
	if envelope.Data.Sprints[0].ID != 41 || envelope.Data.Sprints[0].BoardID != 12 || envelope.Data.Sprints[0].BoardName != "Platform" ||
		envelope.Data.Sprints[1].ID != 42 || envelope.Data.Sprints[1].BoardID != 18 || envelope.Data.Sprints[1].BoardName != "Delivery" {
		t.Fatalf("sprints = %+v", envelope.Data.Sprints)
	}
}

func TestSprintListTotalFailureReturnsOrdinaryAPIError(t *testing.T) {
	clearCommandEnv(t)
	t.Run("single selected Board", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/rest/agile/1.0/board/12":
				_, _ = io.WriteString(w, `{"id":12,"name":"Platform","type":"scrum"}`)
			case "/rest/agile/1.0/board/12/sprint":
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = io.WriteString(w, `{"errorMessages":["single failure"]}`)
			default:
				t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
			}
		}))
		defer server.Close()

		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		code := Execute([]string{"--config", writeCLIConfig(t, server.URL, true), "--output=json", "sprint", "list", "--board", "12"}, strings.NewReader(""), stdout, stderr)
		if code != 5 || stdout.Len() != 0 || !strings.Contains(stderr.String(), `"kind":"api_error"`) || strings.Contains(stderr.String(), "partial_failure") {
			t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
		}
	})
	t.Run("all selected Boards fail after every attempt", func(t *testing.T) {
		var sprintCalls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/rest/agile/1.0/board":
				_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"isLast":true,"values":[{"id":12,"name":"Platform","type":"scrum"},{"id":18,"name":"Delivery","type":"scrum"}]}`)
			case "/rest/agile/1.0/board/12/sprint", "/rest/agile/1.0/board/18/sprint":
				sprintCalls.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = io.WriteString(w, `{"errorMessages":["total failure"]}`)
			default:
				t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
			}
		}))
		defer server.Close()

		stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
		code := Execute([]string{"--config", writeCLIConfig(t, server.URL, true), "--output=json", "sprint", "list"}, strings.NewReader(""), stdout, stderr)
		if code != 5 || sprintCalls.Load() != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), `"kind":"api_error"`) || strings.Contains(stderr.String(), "partial_failure") {
			t.Fatalf("code=%d calls=%d stdout=%s stderr=%s", code, sprintCalls.Load(), stdout.String(), stderr.String())
		}
	})
}

func TestSprintListPartialTextPreservesRowsInQuietMode(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/agile/1.0/board":
			_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"isLast":true,"values":[{"id":12,"name":"Platform","type":"scrum"},{"id":18,"name":"Delivery","type":"scrum"}]}`)
		case "/rest/agile/1.0/board/12/sprint":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"errorMessages":["first Board failed"]}`)
		case "/rest/agile/1.0/board/18/sprint":
			_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"isLast":true,"values":[{"id":42,"name":"Sprint 14","state":"active","originBoardId":7}]}`)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{"--config", writeCLIConfig(t, server.URL, true), "--quiet", "sprint", "list"}, strings.NewReader(""), stdout, stderr)
	if code != 7 || stdout.String() != "42\tSprint 14\tactive\t18\tDelivery\t\t\t\n" || !strings.Contains(stderr.String(), "failed Board") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestSprintListWorkerPoolEmitsBoardOrderDespiteCompletionOrder(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/agile/1.0/board":
			_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"isLast":true,"values":[`+
				`{"id":1,"name":"One","type":"scrum"},{"id":2,"name":"Two","type":"scrum"},{"id":3,"name":"Three","type":"scrum"},`+
				`{"id":4,"name":"Four","type":"scrum"},{"id":5,"name":"Five","type":"scrum"},{"id":6,"name":"Six","type":"scrum"}]}`)
		case "/rest/agile/1.0/board/1/sprint":
			time.Sleep(60 * time.Millisecond)
			_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"isLast":true,"values":[{"id":101,"name":"Sprint 101","state":"active","originBoardId":1}]}`)
		case "/rest/agile/1.0/board/2/sprint":
			time.Sleep(40 * time.Millisecond)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"errorMessages":["board two unavailable"]}`)
		case "/rest/agile/1.0/board/3/sprint":
			_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"isLast":true,"values":[{"id":103,"name":"Sprint 103","state":"active","originBoardId":3}]}`)
		case "/rest/agile/1.0/board/4/sprint":
			time.Sleep(20 * time.Millisecond)
			_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"isLast":true,"values":[{"id":104,"name":"Sprint 104","state":"active","originBoardId":4}]}`)
		case "/rest/agile/1.0/board/5/sprint":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"errorMessages":["board five unavailable"]}`)
		case "/rest/agile/1.0/board/6/sprint":
			_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"isLast":true,"values":[{"id":106,"name":"Sprint 106","state":"active","originBoardId":6}]}`)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{"--config", writeCLIConfig(t, server.URL, true), "--output=json", "sprint", "list"}, strings.NewReader(""), stdout, stderr)
	if code != 7 || !strings.Contains(stderr.String(), `"kind":"partial_failure"`) {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var envelope struct {
		Data struct {
			Total        int           `json:"total"`
			Sprints      []jira.Sprint `json:"sprints"`
			FailedBoards []struct {
				BoardID   int    `json:"boardId"`
				BoardName string `json:"boardName"`
				Error     string `json:"error"`
			} `json:"failedBoards"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	gotBoards := make([]int, 0, len(envelope.Data.Sprints))
	for _, sprint := range envelope.Data.Sprints {
		gotBoards = append(gotBoards, sprint.BoardID)
	}
	if envelope.Data.Total != 4 || fmt.Sprint(gotBoards) != "[1 3 4 6]" {
		t.Fatalf("sprint board order = %v, data = %+v", gotBoards, envelope.Data)
	}
	if len(envelope.Data.FailedBoards) != 2 ||
		envelope.Data.FailedBoards[0].BoardID != 2 || !strings.Contains(envelope.Data.FailedBoards[0].Error, "board two unavailable") ||
		envelope.Data.FailedBoards[1].BoardID != 5 || !strings.Contains(envelope.Data.FailedBoards[1].Error, "board five unavailable") {
		t.Fatalf("failedBoards = %+v", envelope.Data.FailedBoards)
	}
}

func TestSprintListWorkerPoolKeepsFirstBoardOrderErrorWhenAllFail(t *testing.T) {
	clearCommandEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/agile/1.0/board":
			_, _ = io.WriteString(w, `{"startAt":0,"maxResults":50,"isLast":true,"values":[{"id":1,"name":"One","type":"scrum"},{"id":2,"name":"Two","type":"scrum"},{"id":3,"name":"Three","type":"scrum"}]}`)
		case "/rest/agile/1.0/board/1/sprint":
			// The first Board in Jira order finishes last; its error must
			// still be the one reported when every Board fails.
			time.Sleep(80 * time.Millisecond)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"errorMessages":["first board failure"]}`)
		case "/rest/agile/1.0/board/2/sprint", "/rest/agile/1.0/board/3/sprint":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"errorMessages":["later board failure"]}`)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := Execute([]string{"--config", writeCLIConfig(t, server.URL, true), "--output=json", "sprint", "list"}, strings.NewReader(""), stdout, stderr)
	if code != 5 || stdout.Len() != 0 || !strings.Contains(stderr.String(), `"kind":"api_error"`) ||
		!strings.Contains(stderr.String(), "first board failure") || strings.Contains(stderr.String(), "partial_failure") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}
