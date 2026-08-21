// Package jira provides a normalized client for Jira Data Center and Server's
// REST API v2. It deliberately keeps Jira's wire payloads private.
package jira

// Page identifies a page in Jira list results.
type Page struct {
	StartAt    int `json:"startAt"`
	MaxResults int `json:"maxResults"`
}

// User is a Jira user in jiro's stable representation.
type User struct {
	AccountID    string `json:"accountId"`
	Username     string `json:"username"`
	DisplayName  string `json:"displayName"`
	EmailAddress string `json:"emailAddress"`
	Active       bool   `json:"active"`
}

// AssigneeTarget is a resolved Jira REST v2 assignment target. A nil Username
// deliberately clears the assignee.
type AssigneeTarget struct {
	Username *string `json:"username"`
}

// Project identifies a Jira project.
type Project struct {
	ID          string `json:"id"`
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	URL         string `json:"url,omitempty"`
	ProjectType string `json:"projectType,omitempty"`
	Lead        *User  `json:"lead,omitempty"`
}

// IssueType identifies an issue type.
type IssueType struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Subtask     bool   `json:"subtask"`
}

// Status identifies an issue status.
type Status struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category,omitempty"`
}

// Priority identifies an issue priority.
type Priority struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Component identifies an issue component.
type Component struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Version identifies a Jira project version.
type Version struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Archived bool   `json:"archived"`
	Released bool   `json:"released"`
}

// IssueLinkType identifies the relationship and direction of an issue link.
type IssueLinkType struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Inward  string `json:"inward"`
	Outward string `json:"outward"`
}

// IssueLink is a normalized link relative to the issue it was listed from.
type IssueLink struct {
	ID           string        `json:"id"`
	Direction    string        `json:"direction"`
	Relationship string        `json:"relationship"`
	Type         IssueLinkType `json:"type"`
	OtherIssue   Issue         `json:"otherIssue"`
}

// IssueLinkInput creates a link from From to To using a resolved Link Type ID.
// From is the outward issue and To is the inward issue in Jira's REST payload.
type IssueLinkInput struct {
	From   string `json:"from"`
	To     string `json:"to"`
	TypeID string `json:"typeId"`
}

// Board identifies a Jira Software board.
type Board struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// BoardPage is a normalized page of Jira Software boards.
type BoardPage struct {
	StartAt    int     `json:"startAt"`
	MaxResults int     `json:"maxResults"`
	Total      int     `json:"total"`
	IsLast     bool    `json:"isLast"`
	Boards     []Board `json:"boards"`
}

// Sprint identifies a Jira Software sprint.
type Sprint struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	State         string `json:"state"`
	BoardID       int    `json:"boardId,omitempty"`
	BoardName     string `json:"boardName,omitempty"`
	OriginBoardID int    `json:"originBoardId,omitempty"`
	Goal          string `json:"goal,omitempty"`
	StartDate     string `json:"startDate,omitempty"`
	EndDate       string `json:"endDate,omitempty"`
	CompleteDate  string `json:"completeDate,omitempty"`
}

// SprintState is a Jira Software Sprint list state filter.
type SprintState string

const (
	SprintStateActive SprintState = "active"
	SprintStateClosed SprintState = "closed"
	SprintStateFuture SprintState = "future"
	SprintStateAll    SprintState = "all"
)

// SprintPage is a normalized page of Jira Software sprints.
type SprintPage struct {
	StartAt    int      `json:"startAt"`
	MaxResults int      `json:"maxResults"`
	Total      int      `json:"total"`
	IsLast     bool     `json:"isLast"`
	Sprints    []Sprint `json:"sprints"`
}

// MoveIssueToSprintInput selects the target Sprint. Board optionally scopes
// name and active Sprint resolution to one Board selector.
type MoveIssueToSprintInput struct {
	Sprint string `json:"sprint"`
	Board  string `json:"board,omitempty"`
}

// Issue is a normalized issue. Fields holds Jira field values keyed by their
// Jira field IDs, without exposing the rest of Jira's wire document.
type Issue struct {
	ID          string              `json:"id"`
	Key         string              `json:"key"`
	Summary     string              `json:"summary"`
	Description string              `json:"description,omitempty"`
	Project     *Project            `json:"project,omitempty"`
	IssueType   *IssueType          `json:"issueType,omitempty"`
	Status      *Status             `json:"status,omitempty"`
	Priority    *Priority           `json:"priority,omitempty"`
	Assignee    *User               `json:"assignee,omitempty"`
	Reporter    *User               `json:"reporter,omitempty"`
	Parent      *Issue              `json:"parent,omitempty"`
	Labels      []string            `json:"labels,omitempty"`
	Components  []Component         `json:"components,omitempty"`
	FixVersions []Version           `json:"fixVersions,omitempty"`
	Created     string              `json:"created,omitempty"`
	Updated     string              `json:"updated,omitempty"`
	Fields      map[string]any      `json:"fields,omitempty"`
	Sprints     *[]SprintMembership `json:"sprints,omitempty"`
}

// IssueListOptions controls a JQL issue list query.
type IssueListOptions struct {
	JQL    string   `json:"jql"`
	Page   Page     `json:"page"`
	Fields []string `json:"fields,omitempty"`
}

// SearchResult is a normalized Jira search response.
type SearchResult struct {
	StartAt    int     `json:"startAt"`
	MaxResults int     `json:"maxResults"`
	Total      int     `json:"total"`
	Issues     []Issue `json:"issues"`
}

// CreateIssueInput is the writable normalized form for creating an issue.
type CreateIssueInput struct {
	ProjectKey  string         `json:"projectKey"`
	IssueType   string         `json:"issueType"`
	IssueTypeID string         `json:"issueTypeId,omitempty"`
	Summary     string         `json:"summary"`
	Description *string        `json:"description,omitempty"`
	Fields      map[string]any `json:"fields,omitempty"`
}

// UpdateIssueInput is the writable normalized form for updating an issue.
// Nil pointers leave standard fields unchanged; Fields are sent as supplied.
type UpdateIssueInput struct {
	Summary     *string        `json:"summary,omitempty"`
	Description *string        `json:"description,omitempty"`
	Fields      map[string]any `json:"fields,omitempty"`
}

// Comment is a normalized Jira issue comment.
type Comment struct {
	ID      string `json:"id"`
	Body    string `json:"body"`
	Author  *User  `json:"author,omitempty"`
	Created string `json:"created,omitempty"`
	Updated string `json:"updated,omitempty"`
}

// CommentInput describes a new or replacement comment body.
type CommentInput struct {
	Body string `json:"body"`
}

// Transition identifies a transition available for an issue.
type Transition struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	To     *Status `json:"to,omitempty"`
	Fields []Field `json:"fields,omitempty"`
}

// TransitionInput moves an issue using a Jira transition ID. Fields may
// contain values required by that transition screen. Comment, when present,
// is added atomically through the same transition request.
type TransitionInput struct {
	ID      string         `json:"id"`
	Fields  map[string]any `json:"fields,omitempty"`
	Comment *string        `json:"comment,omitempty"`
}

// Field is a normalized Jira field definition.
type Field struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Custom       bool   `json:"custom"`
	Type         string `json:"type,omitempty"`
	SchemaCustom string `json:"-"`
}

// CreateField describes one field Jira exposes on a target Create screen.
// Unlike the instance-wide Field metadata, this is scoped to one Project and
// Issue Type and is the boundary for copying source Custom Fields.
type CreateField struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Custom       bool     `json:"custom"`
	Required     bool     `json:"required"`
	Type         string   `json:"type,omitempty"`
	SchemaCustom string   `json:"-"`
	Operations   []string `json:"operations,omitempty"`
}

// FieldSelection records one requested Field Selector and the exact Jira
// selector produced from it.
type FieldSelection struct {
	Selector string `json:"selector"`
	Resolved string `json:"resolved"`
}
