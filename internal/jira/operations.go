package jira

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/timonwong/jiro/internal/apperr"
)

const agileDiscoveryPageSize = 50

// Myself returns the authenticated Jira user.
func (c *Client) Myself(ctx context.Context) (User, error) {
	var wire wireUser
	if err := c.do(ctx, http.MethodGet, "rest/api/2/myself", nil, nil, &wire); err != nil {
		return User{}, err
	}
	return normalizeUser(wire), nil
}

// Search searches issues using JQL, pagination, and an optional field list.
func (c *Client) Search(ctx context.Context, opts IssueListOptions) (SearchResult, error) {
	if strings.TrimSpace(opts.JQL) == "" {
		return SearchResult{}, apperr.New(apperr.KindInvalidInput, "JQL is required")
	}
	payload := map[string]any{"jql": opts.JQL}
	if opts.Page.StartAt > 0 {
		payload["startAt"] = opts.Page.StartAt
	}
	if opts.Page.MaxResults > 0 {
		payload["maxResults"] = opts.Page.MaxResults
	}
	if len(opts.Fields) > 0 {
		payload["fields"] = opts.Fields
	}
	var wire wireSearch
	if err := c.do(ctx, http.MethodPost, "rest/api/2/search", nil, payload, &wire); err != nil {
		return SearchResult{}, err
	}
	return normalizeSearch(wire), nil
}

// ListIssues is an explicit alias for Search.
func (c *Client) ListIssues(ctx context.Context, opts IssueListOptions) (SearchResult, error) {
	return c.Search(ctx, opts)
}

// ShowIssue returns one issue. fields selects Jira fields when non-empty.
func (c *Client) ShowIssue(ctx context.Context, key string, fields []string) (Issue, error) {
	if strings.TrimSpace(key) == "" {
		return Issue{}, apperr.New(apperr.KindInvalidInput, "issue key is required")
	}
	query := url.Values{}
	if len(fields) > 0 {
		query.Set("fields", strings.Join(fields, ","))
	}
	var wire wireIssue
	if err := c.do(ctx, http.MethodGet, "rest/api/2/issue/"+url.PathEscape(key), query, nil, &wire); err != nil {
		return Issue{}, err
	}
	return normalizeIssue(wire), nil
}

// CreateIssue creates and returns an issue.
func (c *Client) CreateIssue(ctx context.Context, input CreateIssueInput) (Issue, error) {
	if strings.TrimSpace(input.ProjectKey) == "" || (strings.TrimSpace(input.IssueType) == "" && strings.TrimSpace(input.IssueTypeID) == "") || strings.TrimSpace(input.Summary) == "" {
		return Issue{}, apperr.New(apperr.KindInvalidInput, "project key, issue type, and summary are required")
	}
	fields := cloneFields(input.Fields)
	fields["project"] = map[string]string{"key": input.ProjectKey}
	if strings.TrimSpace(input.IssueTypeID) != "" {
		fields["issuetype"] = map[string]string{"id": input.IssueTypeID}
	} else {
		fields["issuetype"] = map[string]string{"name": input.IssueType}
	}
	fields["summary"] = input.Summary
	if input.Description != nil {
		fields["description"] = *input.Description
	}
	var wire wireIssue
	if err := c.do(ctx, http.MethodPost, "rest/api/2/issue", nil, map[string]any{"fields": fields}, &wire); err != nil {
		return Issue{}, err
	}
	return normalizeIssue(wire), nil
}

// UpdateIssue updates fields on an issue.
func (c *Client) UpdateIssue(ctx context.Context, key string, input UpdateIssueInput) error {
	if strings.TrimSpace(key) == "" {
		return apperr.New(apperr.KindInvalidInput, "issue key is required")
	}
	fields := cloneFields(input.Fields)
	if input.Summary != nil {
		fields["summary"] = *input.Summary
	}
	if input.Description != nil {
		fields["description"] = *input.Description
	}
	if len(fields) == 0 {
		return apperr.New(apperr.KindInvalidInput, "at least one issue field is required")
	}
	return c.do(ctx, http.MethodPut, "rest/api/2/issue/"+url.PathEscape(key), nil, map[string]any{"fields": fields}, nil)
}

// ListIssueTypesForIssue returns the Issue Types Jira exposes as compatible on
// the Issue's Edit screen. Jira Server may leave the operations metadata empty
// for this system field, so allowedValues is the authoritative list here.
func (c *Client) ListIssueTypesForIssue(ctx context.Context, key string) ([]IssueType, error) {
	if strings.TrimSpace(key) == "" {
		return nil, apperr.New(apperr.KindInvalidInput, "issue key is required")
	}
	var wire wireEditMeta
	if err := c.do(ctx, http.MethodGet, "rest/api/2/issue/"+url.PathEscape(key)+"/editmeta", nil, nil, &wire); err != nil {
		return nil, err
	}
	field, ok := wire.Fields["issuetype"]
	if !ok {
		return []IssueType{}, nil
	}
	result := make([]IssueType, len(field.AllowedValues))
	for i, issueType := range field.AllowedValues {
		result[i] = IssueType{
			ID: issueType.ID, Name: issueType.Name,
			Description: issueType.Description, Subtask: issueType.Subtask,
		}
	}
	return result, nil
}

// ListCreateFields returns the fields Jira exposes on the Create screen for a
// Project and Issue Type. The endpoint is intentionally scoped to the target
// pair because instance-wide field metadata includes read-only fields.
func (c *Client) ListCreateFields(ctx context.Context, projectKey, issueTypeID string) ([]CreateField, error) {
	projectKey = strings.TrimSpace(projectKey)
	issueTypeID = strings.TrimSpace(issueTypeID)
	if projectKey == "" || issueTypeID == "" {
		return nil, apperr.New(apperr.KindInvalidInput, "project key and issue type ID are required")
	}
	var wire wireCreateMeta
	path := "rest/api/2/issue/createmeta/" + url.PathEscape(projectKey) + "/issuetypes/" + url.PathEscape(issueTypeID)
	if err := c.do(ctx, http.MethodGet, path, nil, nil, &wire); err != nil {
		return nil, err
	}
	if wire.Fields == nil {
		return nil, apperr.New(apperr.KindAPI, "Jira Create metadata response is missing fields")
	}
	return normalizeCreateFields(wire), nil
}

// UpdateIssueType changes one Issue's type through Jira's generic Edit Issue
// endpoint. Callers must resolve the target from ListIssueTypesForIssue first;
// affected older Jira Server releases can otherwise accept incompatible types.
func (c *Client) UpdateIssueType(ctx context.Context, key, issueTypeID string) error {
	if strings.TrimSpace(key) == "" || strings.TrimSpace(issueTypeID) == "" {
		return apperr.New(apperr.KindInvalidInput, "issue key and issue type ID are required")
	}
	payload := map[string]any{
		"fields": map[string]any{"issuetype": map[string]string{"id": issueTypeID}},
	}
	return c.do(ctx, http.MethodPut, "rest/api/2/issue/"+url.PathEscape(key), nil, payload, nil)
}

// ResolveAssignee resolves a username, me, or none once for one operation.
func (c *Client) ResolveAssignee(ctx context.Context, assignee string) (AssigneeTarget, error) {
	assignee = strings.TrimSpace(assignee)
	if assignee == "" {
		return AssigneeTarget{}, apperr.New(apperr.KindInvalidInput, "assignee is required")
	}
	if strings.EqualFold(assignee, "me") {
		user, err := c.Myself(ctx)
		if err != nil {
			return AssigneeTarget{}, err
		}
		if strings.TrimSpace(user.Username) == "" {
			return AssigneeTarget{}, apperr.New(apperr.KindAPI, "authenticated Jira user has no username")
		}
		assignee = user.Username
	} else if strings.EqualFold(assignee, "none") {
		return AssigneeTarget{}, nil
	}
	return AssigneeTarget{Username: &assignee}, nil
}

// AssignIssue assigns or unassigns an issue using a resolved REST v2 target.
func (c *Client) AssignIssue(ctx context.Context, key string, target AssigneeTarget) error {
	if strings.TrimSpace(key) == "" {
		return apperr.New(apperr.KindInvalidInput, "issue key is required")
	}
	var username any
	if target.Username != nil {
		username = *target.Username
	}
	return c.do(ctx, http.MethodPut, "rest/api/2/issue/"+url.PathEscape(key)+"/assignee", nil, map[string]any{"name": username}, nil)
}

// ListIssueLinks returns links relative to the supplied issue.
func (c *Client) ListIssueLinks(ctx context.Context, key string) ([]IssueLink, error) {
	if strings.TrimSpace(key) == "" {
		return nil, apperr.New(apperr.KindInvalidInput, "issue key is required")
	}
	query := url.Values{"fields": {"issuelinks"}}
	var wire wireIssue
	if err := c.do(ctx, http.MethodGet, "rest/api/2/issue/"+url.PathEscape(key), query, nil, &wire); err != nil {
		return nil, err
	}
	return normalizeIssueLinks(wire.Fields["issuelinks"]), nil
}

// ListIssueLinkTypes returns the link types configured on this Jira Instance.
func (c *Client) ListIssueLinkTypes(ctx context.Context) ([]IssueLinkType, error) {
	var wire struct {
		IssueLinkTypes []wireIssueLinkType `json:"issueLinkTypes"`
	}
	if err := c.do(ctx, http.MethodGet, "rest/api/2/issueLinkType", nil, nil, &wire); err != nil {
		return nil, err
	}
	result := make([]IssueLinkType, len(wire.IssueLinkTypes))
	for i, linkType := range wire.IssueLinkTypes {
		result[i] = normalizeIssueLinkType(linkType)
	}
	return result, nil
}

// CreateIssueLink creates an outward link from input.From to input.To.
func (c *Client) CreateIssueLink(ctx context.Context, input IssueLinkInput) error {
	if strings.TrimSpace(input.From) == "" || strings.TrimSpace(input.To) == "" || strings.TrimSpace(input.TypeID) == "" {
		return apperr.New(apperr.KindInvalidInput, "from issue, to issue, and link type are required")
	}
	if err := ValidateIssueLinkTypeID(input.TypeID); err != nil {
		return err
	}
	typeID := strings.TrimSpace(input.TypeID)
	payload := map[string]any{
		"type":         map[string]string{"id": typeID},
		"outwardIssue": map[string]string{"key": input.From},
		"inwardIssue":  map[string]string{"key": input.To},
	}
	return c.do(ctx, http.MethodPost, "rest/api/2/issueLink", nil, payload, nil)
}

// ValidateIssueLinkTypeID rejects values Jira cannot accept as link type IDs.
func ValidateIssueLinkTypeID(value string) error {
	if id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err != nil || id <= 0 {
		return apperr.New(apperr.KindInvalidInput, "link type ID must be positive")
	}
	return nil
}

// DeleteIssueLink deletes one issue link by Jira link ID.
func (c *Client) DeleteIssueLink(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if numericID, err := strconv.ParseInt(id, 10, 64); err != nil || numericID <= 0 {
		return apperr.New(apperr.KindInvalidInput, "issue link ID is required")
	}
	return c.do(ctx, http.MethodDelete, "rest/api/2/issueLink/"+url.PathEscape(id), nil, nil, nil)
}

// ListBoards returns a page of Jira Software boards in Jira's source order.
func (c *Client) ListBoards(ctx context.Context, page Page) (BoardPage, error) {
	var wire wireBoardPage
	if err := c.do(ctx, http.MethodGet, "rest/agile/1.0/board", pageQuery(page), nil, &wire); err != nil {
		return BoardPage{}, err
	}
	if wire.Values == nil {
		return BoardPage{}, apperr.New(apperr.KindAPI, "Jira Board response is missing values")
	}
	result := normalizeBoardPage(wire)
	if err := validateAgilePage("Board", page.StartAt, result.StartAt, len(result.Boards), wire.Total, wire.IsLast, wire.IsLastPage); err != nil {
		return BoardPage{}, err
	}
	for _, board := range result.Boards {
		if board.ID <= 0 {
			return BoardPage{}, apperr.New(apperr.KindAPI, "Jira Board response contains a non-positive ID")
		}
	}
	return result, nil
}

// ListAllBoards returns every accessible Jira Software board in Jira's page
// order.
func (c *Client) ListAllBoards(ctx context.Context) ([]Board, error) {
	boards := make([]Board, 0)
	seen := make(map[int]struct{})
	for page := (Page{MaxResults: agileDiscoveryPageSize}); ; {
		result, err := c.ListBoards(ctx, page)
		if err != nil {
			return nil, err
		}
		added := 0
		for _, board := range result.Boards {
			if _, duplicate := seen[board.ID]; duplicate {
				continue
			}
			seen[board.ID] = struct{}{}
			boards = append(boards, board)
			added++
		}
		if len(result.Boards) > 0 && added == 0 {
			return nil, apperr.New(apperr.KindAPI, fmt.Sprintf("Jira Board pagination returned no new Boards at startAt %d", result.StartAt))
		}
		if !hasNextPage(result.StartAt, len(result.Boards), result.Total, result.IsLast) {
			return boards, nil
		}
		page.StartAt = result.StartAt + len(result.Boards)
	}
}

// ShowBoard returns one Jira Software board by its exact ID.
func (c *Client) ShowBoard(ctx context.Context, boardID int) (Board, error) {
	if boardID <= 0 {
		return Board{}, apperr.New(apperr.KindInvalidInput, "board ID is required")
	}
	var wire wireBoard
	if err := c.do(ctx, http.MethodGet, "rest/agile/1.0/board/"+strconv.Itoa(boardID), nil, nil, &wire); err != nil {
		return Board{}, err
	}
	if wire.ID != boardID {
		return Board{}, apperr.New(apperr.KindAPI, fmt.Sprintf("Jira Board response ID %d does not match requested Board %d", wire.ID, boardID))
	}
	return Board{ID: wire.ID, Name: wire.Name, Type: wire.Type}, nil
}

var numericBoardSelector = regexp.MustCompile(`^[+-]?[0-9]+$`)

// parseBoardSelector validates a Board selector and returns its exact Board
// ID, or 0 for a name-substring selector.
func parseBoardSelector(selector string) (int, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return 0, apperr.New(apperr.KindInvalidInput, "board selector must not be empty")
	}
	if !numericBoardSelector.MatchString(selector) {
		return 0, nil
	}
	id, err := strconv.ParseInt(selector, 10, 0)
	if err != nil || id <= 0 {
		return 0, apperr.New(apperr.KindInvalidInput, "board ID must be positive")
	}
	return int(id), nil
}

// ValidateBoardSelector reports whether a Board selector is well-formed
// without touching the network.
func ValidateBoardSelector(selector string) error {
	_, err := parseBoardSelector(selector)
	return err
}

// ResolveBoardSelector resolves one Board selector. A positive numeric
// selector fetches that exact Board ID directly; other selectors match a
// case-insensitive Board name substring across every accessible Board in
// Jira's board order.
func (c *Client) ResolveBoardSelector(ctx context.Context, selector string) ([]Board, error) {
	selector = strings.TrimSpace(selector)
	id, err := parseBoardSelector(selector)
	if err != nil {
		return nil, err
	}
	if id > 0 {
		board, err := c.ShowBoard(ctx, id)
		if err != nil {
			if apperr.As(err).Kind == apperr.KindNotFound {
				return nil, apperr.New(apperr.KindNotFound, fmt.Sprintf("board %q was not found", selector))
			}
			return nil, err
		}
		return []Board{board}, nil
	}
	boards, err := c.ListAllBoards(ctx)
	if err != nil {
		return nil, err
	}
	needle := strings.ToLower(selector)
	selected := make([]Board, 0)
	for _, board := range boards {
		if strings.Contains(strings.ToLower(board.Name), needle) {
			selected = append(selected, board)
		}
	}
	if len(selected) == 0 {
		return nil, apperr.New(apperr.KindNotFound, fmt.Sprintf("board %q was not found", selector))
	}
	return selected, nil
}

// ListSprints returns a page of a board's Jira Software sprints in Jira's
// source order.
func (c *Client) ListSprints(ctx context.Context, boardID int, page Page) (SprintPage, error) {
	return c.listSprints(ctx, boardID, page, SprintStateAll)
}

func (c *Client) listSprints(ctx context.Context, boardID int, page Page, state SprintState) (SprintPage, error) {
	if boardID <= 0 {
		return SprintPage{}, apperr.New(apperr.KindInvalidInput, "board ID is required")
	}
	query := pageQuery(page)
	if state != SprintStateAll {
		query.Set("state", string(state))
	}
	var wire wireSprintPage
	if err := c.do(ctx, http.MethodGet, "rest/agile/1.0/board/"+strconv.Itoa(boardID)+"/sprint", query, nil, &wire); err != nil {
		return SprintPage{}, err
	}
	if wire.Values == nil {
		return SprintPage{}, apperr.New(apperr.KindAPI, "Jira Sprint response is missing values")
	}
	result := normalizeSprintPage(wire)
	if err := validateAgilePage("Sprint", page.StartAt, result.StartAt, len(result.Sprints), wire.Total, wire.IsLast, wire.IsLastPage); err != nil {
		return SprintPage{}, err
	}
	for i := range result.Sprints {
		if result.Sprints[i].ID <= 0 {
			return SprintPage{}, apperr.New(apperr.KindAPI, "Jira Sprint response contains a non-positive ID")
		}
		result.Sprints[i].BoardID = boardID
	}
	return result, nil
}

// ParseSprintState validates and normalizes a Sprint list state.
func ParseSprintState(value string) (SprintState, error) {
	state := SprintState(strings.ToLower(strings.TrimSpace(value)))
	switch state {
	case SprintStateActive, SprintStateClosed, SprintStateFuture, SprintStateAll:
		return state, nil
	default:
		return "", apperr.New(apperr.KindInvalidInput, "sprint state must be active, closed, future, or all")
	}
}

// ListAllSprints returns every Sprint for one Board in Jira's page order.
func (c *Client) ListAllSprints(ctx context.Context, boardID int, state SprintState) ([]Sprint, error) {
	sprints := make([]Sprint, 0)
	seen := make(map[int]struct{})
	for page := (Page{MaxResults: agileDiscoveryPageSize}); ; {
		result, err := c.listSprints(ctx, boardID, page, state)
		if err != nil {
			return sprints, err
		}
		added := 0
		for _, sprint := range result.Sprints {
			if _, duplicate := seen[sprint.ID]; duplicate {
				continue
			}
			seen[sprint.ID] = struct{}{}
			sprints = append(sprints, sprint)
			added++
		}
		if len(result.Sprints) > 0 && added == 0 {
			return sprints, apperr.New(apperr.KindAPI, fmt.Sprintf("Jira Sprint pagination returned no new Sprints at startAt %d", result.StartAt))
		}
		if !hasNextPage(result.StartAt, len(result.Sprints), result.Total, result.IsLast) {
			return sprints, nil
		}
		page.StartAt = result.StartAt + len(result.Sprints)
	}
}

// ResolveSprint resolves a numeric ID, active, or case-insensitive name
// substring. Name and active matches preserve Jira's board and page order.
// A non-empty board selector scopes resolution to the Boards it matches.
func (c *Client) ResolveSprint(ctx context.Context, spec, board string) (Sprint, error) {
	return c.resolveSprint(ctx, spec, board)
}

// MoveIssueToSprint moves an issue to the Sprint selected by input.
func (c *Client) MoveIssueToSprint(ctx context.Context, key string, input MoveIssueToSprintInput) error {
	if strings.TrimSpace(key) == "" || strings.TrimSpace(input.Sprint) == "" {
		return apperr.New(apperr.KindInvalidInput, "issue key and sprint are required")
	}
	sprint, err := c.resolveSprint(ctx, input.Sprint, input.Board)
	if err != nil {
		return err
	}
	return c.AddIssuesToSprint(ctx, sprint.ID, []string{key})
}

// AddIssuesToSprint moves issue keys to an already resolved Sprint ID.
func (c *Client) AddIssuesToSprint(ctx context.Context, sprintID int, keys []string) error {
	if sprintID <= 0 || len(keys) == 0 {
		return apperr.New(apperr.KindInvalidInput, "positive sprint ID and at least one issue key are required")
	}
	issues := make([]string, len(keys))
	for i, key := range keys {
		issues[i] = strings.TrimSpace(key)
		if issues[i] == "" {
			return apperr.New(apperr.KindInvalidInput, "issue key is required")
		}
	}
	return c.do(ctx, http.MethodPost, "rest/agile/1.0/sprint/"+strconv.Itoa(sprintID)+"/issue", nil, map[string][]string{"issues": issues}, nil)
}

func (c *Client) resolveSprint(ctx context.Context, spec, board string) (Sprint, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return Sprint{}, apperr.New(apperr.KindInvalidInput, "sprint is required")
	}
	if id, err := strconv.Atoi(spec); err == nil {
		if id <= 0 {
			return Sprint{}, apperr.New(apperr.KindInvalidInput, "sprint ID must be positive")
		}
		return Sprint{ID: id}, nil
	}
	active := strings.EqualFold(spec, "active")
	// An active spec filters server-side, so each board returns at most one
	// page of candidates instead of its full sprint history.
	state := SprintStateAll
	if active {
		state = SprintStateActive
	}
	needle := strings.ToLower(spec)
	if strings.TrimSpace(board) != "" {
		boards, err := c.ResolveBoardSelector(ctx, board)
		if err != nil {
			return Sprint{}, err
		}
		for _, board := range boards {
			sprint, found, err := c.findSprintOnBoard(ctx, board.ID, state, active, needle)
			if err != nil {
				return Sprint{}, err
			}
			if found {
				return sprint, nil
			}
		}
		return Sprint{}, apperr.New(apperr.KindInvalidInput, fmt.Sprintf("sprint %q was not found", spec))
	}
	for boardPage := (Page{MaxResults: agileDiscoveryPageSize}); ; {
		boards, err := c.ListBoards(ctx, boardPage)
		if err != nil {
			return Sprint{}, err
		}
		for _, board := range boards.Boards {
			sprint, found, err := c.findSprintOnBoard(ctx, board.ID, state, active, needle)
			if err != nil {
				return Sprint{}, err
			}
			if found {
				return sprint, nil
			}
		}
		if !hasNextPage(boards.StartAt, len(boards.Boards), boards.Total, boards.IsLast) {
			break
		}
		boardPage.StartAt = boards.StartAt + len(boards.Boards)
	}
	return Sprint{}, apperr.New(apperr.KindInvalidInput, fmt.Sprintf("sprint %q was not found", spec))
}

// findSprintOnBoard returns one board's first Sprint matching the resolution
// spec, in Jira's page order.
func (c *Client) findSprintOnBoard(ctx context.Context, boardID int, state SprintState, active bool, needle string) (Sprint, bool, error) {
	for sprintPage := (Page{MaxResults: agileDiscoveryPageSize}); ; {
		sprints, err := c.listSprints(ctx, boardID, sprintPage, state)
		if err != nil {
			return Sprint{}, false, err
		}
		for _, sprint := range sprints.Sprints {
			if (active && strings.EqualFold(sprint.State, "active")) || (!active && strings.Contains(strings.ToLower(sprint.Name), needle)) {
				return sprint, true, nil
			}
		}
		if !hasNextPage(sprints.StartAt, len(sprints.Sprints), sprints.Total, sprints.IsLast) {
			return Sprint{}, false, nil
		}
		sprintPage.StartAt = sprints.StartAt + len(sprints.Sprints)
	}
}

func hasNextPage(startAt, count, total int, isLast bool) bool {
	if isLast || count == 0 {
		return false
	}
	return total == 0 || startAt+count < total
}

func validateAgilePage(resource string, requestedStartAt, receivedStartAt, count int, total *int, isLast, isLastPage *bool) error {
	if receivedStartAt != requestedStartAt {
		return apperr.New(apperr.KindAPI, fmt.Sprintf(
			"Jira %s pagination did not advance: requested startAt %d, received %d",
			resource, requestedStartAt, receivedStartAt,
		))
	}
	if receivedStartAt < 0 || count < 0 || (total != nil && *total < 0) {
		return apperr.New(apperr.KindAPI, fmt.Sprintf("Jira %s pagination contains a negative value", resource))
	}
	if isLast != nil && isLastPage != nil && *isLast != *isLastPage {
		return apperr.New(apperr.KindAPI, fmt.Sprintf("Jira %s pagination final-page markers disagree", resource))
	}
	explicitlyNonFinal := (isLast != nil && !*isLast) || (isLastPage != nil && !*isLastPage)
	explicitlyFinal := boolPointerValue(isLast) || boolPointerValue(isLastPage)
	end := receivedStartAt + count
	if total != nil {
		if end > *total || (explicitlyFinal && end < *total) || (explicitlyNonFinal && end >= *total) {
			return apperr.New(apperr.KindAPI, fmt.Sprintf(
				"Jira %s pagination metadata is inconsistent: startAt %d, count %d, total %d",
				resource, receivedStartAt, count, *total,
			))
		}
	}
	completeByTotal := total != nil && *total <= receivedStartAt
	if count == 0 && (explicitlyNonFinal || (!explicitlyFinal && !completeByTotal)) {
		totalValue := "omitted"
		if total != nil {
			totalValue = strconv.Itoa(*total)
		}
		return apperr.New(apperr.KindAPI, fmt.Sprintf(
			"Jira %s pagination ended before completion: startAt %d, total %s",
			resource, receivedStartAt, totalValue,
		))
	}
	return nil
}

// ListComments returns comments on an issue.
func (c *Client) ListComments(ctx context.Context, key string, page Page) ([]Comment, error) {
	if strings.TrimSpace(key) == "" {
		return nil, apperr.New(apperr.KindInvalidInput, "issue key is required")
	}
	query := pageQuery(page)
	var wire wireComments
	if err := c.do(ctx, http.MethodGet, "rest/api/2/issue/"+url.PathEscape(key)+"/comment", query, nil, &wire); err != nil {
		return nil, err
	}
	comments := make([]Comment, len(wire.Comments))
	for i, comment := range wire.Comments {
		comments[i] = normalizeComment(comment)
	}
	return comments, nil
}

// Comments is an explicit alias for ListComments.
func (c *Client) Comments(ctx context.Context, key string, page Page) ([]Comment, error) {
	return c.ListComments(ctx, key, page)
}

// ShowComment returns a single issue comment.
func (c *Client) ShowComment(ctx context.Context, key, id string) (Comment, error) {
	if strings.TrimSpace(key) == "" || strings.TrimSpace(id) == "" {
		return Comment{}, apperr.New(apperr.KindInvalidInput, "issue key and comment ID are required")
	}
	var wire wireComment
	if err := c.do(ctx, http.MethodGet, "rest/api/2/issue/"+url.PathEscape(key)+"/comment/"+url.PathEscape(id), nil, nil, &wire); err != nil {
		return Comment{}, err
	}
	return normalizeComment(wire), nil
}

// Comment creates an issue comment.
func (c *Client) Comment(ctx context.Context, key string, input CommentInput) (Comment, error) {
	if strings.TrimSpace(key) == "" || input.Body == "" {
		return Comment{}, apperr.New(apperr.KindInvalidInput, "issue key and comment body are required")
	}
	var wire wireComment
	if err := c.do(ctx, http.MethodPost, "rest/api/2/issue/"+url.PathEscape(key)+"/comment", nil, input, &wire); err != nil {
		return Comment{}, err
	}
	return normalizeComment(wire), nil
}

// UpdateComment replaces an issue comment body and reads the final Comment
// back. Jira's edit endpoint may return no representation, so read-back is
// required before reporting the normalized result as verified.
func (c *Client) UpdateComment(ctx context.Context, key, id string, input CommentInput) (Comment, error) {
	if strings.TrimSpace(key) == "" || strings.TrimSpace(id) == "" || input.Body == "" {
		return Comment{}, apperr.New(apperr.KindInvalidInput, "issue key, comment ID, and comment body are required")
	}
	path := "rest/api/2/issue/" + url.PathEscape(key) + "/comment/" + url.PathEscape(id)
	if err := c.do(ctx, http.MethodPut, path, nil, input, nil); err != nil {
		return Comment{}, err
	}
	comment, err := c.ShowComment(ctx, key, id)
	if err != nil {
		return Comment{}, apperr.Wrap(apperr.KindPartialFailure, err,
			"comment %s/%s was updated but read-back failed: %v", key, id, err)
	}
	return comment, nil
}

// AddComment is an explicit alias for Comment.
func (c *Client) AddComment(ctx context.Context, key string, input CommentInput) (Comment, error) {
	return c.Comment(ctx, key, input)
}

// ListTransitions returns transitions currently available for an issue.
func (c *Client) ListTransitions(ctx context.Context, key string) ([]Transition, error) {
	if strings.TrimSpace(key) == "" {
		return nil, apperr.New(apperr.KindInvalidInput, "issue key is required")
	}
	var wire struct {
		Transitions []wireTransition `json:"transitions"`
	}
	if err := c.do(ctx, http.MethodGet, "rest/api/2/issue/"+url.PathEscape(key)+"/transitions", nil, nil, &wire); err != nil {
		return nil, err
	}
	result := make([]Transition, len(wire.Transitions))
	for i, transition := range wire.Transitions {
		result[i] = normalizeTransition(transition)
	}
	return result, nil
}

// Transition moves an issue through a transition.
func (c *Client) Transition(ctx context.Context, key string, input TransitionInput) error {
	if strings.TrimSpace(key) == "" || strings.TrimSpace(input.ID) == "" {
		return apperr.New(apperr.KindInvalidInput, "issue key and transition ID are required")
	}
	if input.Comment != nil && strings.TrimSpace(*input.Comment) == "" {
		return apperr.New(apperr.KindInvalidInput, "transition comment must not be empty")
	}
	payload := map[string]any{"transition": map[string]string{"id": input.ID}}
	if len(input.Fields) > 0 {
		payload["fields"] = input.Fields
	}
	if input.Comment != nil {
		payload["update"] = map[string]any{
			"comment": []map[string]any{{"add": map[string]string{"body": *input.Comment}}},
		}
	}
	return c.do(ctx, http.MethodPost, "rest/api/2/issue/"+url.PathEscape(key)+"/transitions", nil, payload, nil)
}

// ListProjects returns Jira projects. Some Jira Server versions return an
// array while newer Data Center versions return a paged object; both are
// normalized here.
func (c *Client) ListProjects(ctx context.Context, page Page) ([]Project, error) {
	query := pageQuery(page)
	var raw []byte
	if err := c.do(ctx, http.MethodGet, "rest/api/2/project", query, nil, &rawJSON{target: &raw}); err != nil {
		return nil, err
	}
	return decodeProjects(raw)
}

// ShowProject returns a project by key or ID.
func (c *Client) ShowProject(ctx context.Context, key string) (Project, error) {
	if strings.TrimSpace(key) == "" {
		return Project{}, apperr.New(apperr.KindInvalidInput, "project key is required")
	}
	var wire wireProject
	if err := c.do(ctx, http.MethodGet, "rest/api/2/project/"+url.PathEscape(key), nil, nil, &wire); err != nil {
		return Project{}, err
	}
	return normalizeProject(wire), nil
}

// ListFields returns live Jira field definitions.
func (c *Client) ListFields(ctx context.Context) ([]Field, error) {
	var wire []wireField
	if err := c.do(ctx, http.MethodGet, "rest/api/2/field", nil, nil, &wire); err != nil {
		return nil, err
	}
	fields := make([]Field, len(wire))
	for i, field := range wire {
		fields[i] = normalizeField(field)
	}
	return fields, nil
}

func pageQuery(page Page) url.Values {
	query := url.Values{}
	if page.StartAt > 0 {
		query.Set("startAt", strconv.Itoa(page.StartAt))
	}
	if page.MaxResults > 0 {
		query.Set("maxResults", strconv.Itoa(page.MaxResults))
	}
	return query
}

func cloneFields(fields map[string]any) map[string]any {
	copy := make(map[string]any, len(fields)+3)
	for key, value := range fields {
		copy[key] = value
	}
	return copy
}
