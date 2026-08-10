package jira

import (
	"encoding/json"

	"github.com/timonwong/jiro/internal/apperr"
)

type wireUser struct {
	AccountID    string `json:"accountId"`
	Name         string `json:"name"`
	Key          string `json:"key"`
	DisplayName  string `json:"displayName"`
	EmailAddress string `json:"emailAddress"`
	Active       bool   `json:"active"`
}

type wireProject struct {
	ID             string   `json:"id"`
	Key            string   `json:"key"`
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	URL            string   `json:"url"`
	ProjectTypeKey string   `json:"projectTypeKey"`
	Lead           wireUser `json:"lead"`
}

type wireIssueType struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Subtask     bool   `json:"subtask"`
}

type wireEditMeta struct {
	Fields map[string]wireEditMetaField `json:"fields"`
}

type wireEditMetaField struct {
	AllowedValues []wireIssueType `json:"allowedValues"`
}

type wireStatus struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	StatusCategory struct {
		Name string `json:"name"`
	} `json:"statusCategory"`
}

type wirePriority struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type wireComponent struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type wireIssueLinkType struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Inward  string `json:"inward"`
	Outward string `json:"outward"`
}

type wireBoard struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

type wireBoardPage struct {
	StartAt    int         `json:"startAt"`
	MaxResults int         `json:"maxResults"`
	Total      *int        `json:"total"`
	IsLast     *bool       `json:"isLast"`
	IsLastPage *bool       `json:"isLastPage"`
	Values     []wireBoard `json:"values"`
}

type wireSprint struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	State         string `json:"state"`
	OriginBoardID int    `json:"originBoardId"`
	Goal          string `json:"goal"`
	StartDate     string `json:"startDate"`
	EndDate       string `json:"endDate"`
	CompleteDate  string `json:"completeDate"`
}

type wireSprintPage struct {
	StartAt    int          `json:"startAt"`
	MaxResults int          `json:"maxResults"`
	Total      *int         `json:"total"`
	IsLast     *bool        `json:"isLast"`
	IsLastPage *bool        `json:"isLastPage"`
	Values     []wireSprint `json:"values"`
}

type wireIssue struct {
	ID     string         `json:"id"`
	Key    string         `json:"key"`
	Fields map[string]any `json:"fields"`
}

type wireSearch struct {
	StartAt    int         `json:"startAt"`
	MaxResults int         `json:"maxResults"`
	Total      int         `json:"total"`
	Issues     []wireIssue `json:"issues"`
}

type wireComment struct {
	ID      string   `json:"id"`
	Body    string   `json:"body"`
	Author  wireUser `json:"author"`
	Created string   `json:"created"`
	Updated string   `json:"updated"`
}

type wireComments struct {
	Comments []wireComment `json:"comments"`
}

type wireField struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Custom bool   `json:"custom"`
	Schema struct {
		Type string `json:"type"`
	} `json:"schema"`
}

type wireTransition struct {
	ID     string      `json:"id"`
	Name   string      `json:"name"`
	To     wireStatus  `json:"to"`
	Fields []wireField `json:"fields"`
}

type rawJSON struct{ target *[]byte }

func (r *rawJSON) UnmarshalJSON(value []byte) error {
	*r.target = append((*r.target)[:0], value...)
	return nil
}

func normalizeUser(w wireUser) User {
	username := w.Name
	if username == "" {
		username = w.Key
	}
	return User{AccountID: w.AccountID, Username: username, DisplayName: w.DisplayName, EmailAddress: w.EmailAddress, Active: w.Active}
}

func normalizeProject(w wireProject) Project {
	project := Project{ID: w.ID, Key: w.Key, Name: w.Name, Description: w.Description, URL: w.URL, ProjectType: w.ProjectTypeKey}
	if w.Lead.Name != "" || w.Lead.Key != "" || w.Lead.AccountID != "" {
		lead := normalizeUser(w.Lead)
		project.Lead = &lead
	}
	return project
}

func normalizeIssue(w wireIssue) Issue {
	issue := Issue{ID: w.ID, Key: w.Key, Fields: cloneFields(w.Fields)}
	issue.Summary, _ = w.Fields["summary"].(string)
	issue.Description, _ = w.Fields["description"].(string)
	issue.Created, _ = w.Fields["created"].(string)
	issue.Updated, _ = w.Fields["updated"].(string)
	if labels, ok := w.Fields["labels"].([]any); ok {
		issue.Labels = make([]string, 0, len(labels))
		for _, label := range labels {
			if name, ok := label.(string); ok {
				issue.Labels = append(issue.Labels, name)
			}
		}
	}
	issue.Project = projectFromValue(w.Fields["project"])
	issue.IssueType = issueTypeFromValue(w.Fields["issuetype"])
	issue.Status = statusFromValue(w.Fields["status"])
	issue.Priority = priorityFromValue(w.Fields["priority"])
	issue.Assignee = userFromValue(w.Fields["assignee"])
	issue.Reporter = userFromValue(w.Fields["reporter"])
	issue.Parent = issueFromValue(w.Fields["parent"])
	if components, ok := w.Fields["components"].([]any); ok {
		issue.Components = make([]Component, 0, len(components))
		for _, component := range components {
			if raw, ok := component.(map[string]any); ok {
				issue.Components = append(issue.Components, Component{ID: stringValue(raw["id"]), Name: stringValue(raw["name"])})
			}
		}
	}
	if versions, ok := w.Fields["fixVersions"].([]any); ok {
		issue.FixVersions = make([]Version, 0, len(versions))
		for _, version := range versions {
			if raw, ok := version.(map[string]any); ok {
				issue.FixVersions = append(issue.FixVersions, Version{
					ID: stringValue(raw["id"]), Name: stringValue(raw["name"]),
					Archived: boolValue(raw["archived"]), Released: boolValue(raw["released"]),
				})
			}
		}
	}
	return issue
}

func normalizeSearch(w wireSearch) SearchResult {
	issues := make([]Issue, len(w.Issues))
	for i, issue := range w.Issues {
		issues[i] = normalizeIssue(issue)
	}
	return SearchResult{StartAt: w.StartAt, MaxResults: w.MaxResults, Total: w.Total, Issues: issues}
}

func normalizeComment(w wireComment) Comment {
	comment := Comment{ID: w.ID, Body: w.Body, Created: w.Created, Updated: w.Updated}
	if w.Author.Name != "" || w.Author.Key != "" || w.Author.AccountID != "" {
		author := normalizeUser(w.Author)
		comment.Author = &author
	}
	return comment
}

func normalizeField(w wireField) Field {
	return Field{ID: w.ID, Name: w.Name, Custom: w.Custom, Type: w.Schema.Type}
}

func normalizeTransition(w wireTransition) Transition {
	transition := Transition{ID: w.ID, Name: w.Name}
	if w.To.ID != "" || w.To.Name != "" {
		to := Status{ID: w.To.ID, Name: w.To.Name, Category: w.To.StatusCategory.Name}
		transition.To = &to
	}
	transition.Fields = make([]Field, len(w.Fields))
	for i, field := range w.Fields {
		transition.Fields[i] = normalizeField(field)
	}
	return transition
}

func normalizeIssueLinkType(w wireIssueLinkType) IssueLinkType {
	return IssueLinkType{ID: w.ID, Name: w.Name, Inward: w.Inward, Outward: w.Outward}
}

func normalizeBoardPage(w wireBoardPage) BoardPage {
	boards := make([]Board, len(w.Values))
	for i, board := range w.Values {
		boards[i] = Board{ID: board.ID, Name: board.Name, Type: board.Type}
	}
	return BoardPage{StartAt: w.StartAt, MaxResults: w.MaxResults, Total: intValue(w.Total), IsLast: boolPointerValue(w.IsLast) || boolPointerValue(w.IsLastPage), Boards: boards}
}

func normalizeSprintPage(w wireSprintPage) SprintPage {
	sprints := make([]Sprint, len(w.Values))
	for i, sprint := range w.Values {
		sprints[i] = Sprint{
			ID: sprint.ID, Name: sprint.Name, State: sprint.State, OriginBoardID: sprint.OriginBoardID,
			Goal: sprint.Goal, StartDate: sprint.StartDate, EndDate: sprint.EndDate, CompleteDate: sprint.CompleteDate,
		}
	}
	return SprintPage{StartAt: w.StartAt, MaxResults: w.MaxResults, Total: intValue(w.Total), IsLast: boolPointerValue(w.IsLast) || boolPointerValue(w.IsLastPage), Sprints: sprints}
}

func intValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func boolPointerValue(value *bool) bool {
	return value != nil && *value
}

func normalizeIssueLinks(value any) []IssueLink {
	rawLinks, ok := value.([]any)
	if !ok {
		return nil
	}
	links := make([]IssueLink, 0, len(rawLinks))
	for _, value := range rawLinks {
		raw, ok := value.(map[string]any)
		if !ok {
			continue
		}
		typeRaw, _ := raw["type"].(map[string]any)
		linkType := IssueLinkType{ID: stringValue(typeRaw["id"]), Name: stringValue(typeRaw["name"]), Inward: stringValue(typeRaw["inward"]), Outward: stringValue(typeRaw["outward"])}
		link := IssueLink{ID: stringValue(raw["id"]), Type: linkType}
		if other := issueFromValue(raw["outwardIssue"]); other != nil {
			link.Direction, link.Relationship, link.OtherIssue = "outward", linkType.Outward, *other
		} else if other := issueFromValue(raw["inwardIssue"]); other != nil {
			link.Direction, link.Relationship, link.OtherIssue = "inward", linkType.Inward, *other
		}
		links = append(links, link)
	}
	return links
}

func projectFromValue(value any) *Project {
	raw, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	return &Project{ID: stringValue(raw["id"]), Key: stringValue(raw["key"]), Name: stringValue(raw["name"])}
}

func issueTypeFromValue(value any) *IssueType {
	raw, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	return &IssueType{
		ID: stringValue(raw["id"]), Name: stringValue(raw["name"]),
		Description: stringValue(raw["description"]), Subtask: boolValue(raw["subtask"]),
	}
}

func statusFromValue(value any) *Status {
	raw, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	category, _ := raw["statusCategory"].(map[string]any)
	return &Status{ID: stringValue(raw["id"]), Name: stringValue(raw["name"]), Category: stringValue(category["name"])}
}

func priorityFromValue(value any) *Priority {
	raw, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	return &Priority{ID: stringValue(raw["id"]), Name: stringValue(raw["name"])}
}

func issueFromValue(value any) *Issue {
	raw, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	issue := &Issue{ID: stringValue(raw["id"]), Key: stringValue(raw["key"])}
	if fields, ok := raw["fields"].(map[string]any); ok {
		issue.Fields = cloneFields(fields)
		issue.Summary = stringValue(fields["summary"])
	}
	return issue
}

func userFromValue(value any) *User {
	raw, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	user := User{AccountID: stringValue(raw["accountId"]), Username: stringValue(raw["name"]), DisplayName: stringValue(raw["displayName"]), EmailAddress: stringValue(raw["emailAddress"])}
	if user.Username == "" {
		user.Username = stringValue(raw["key"])
	}
	if active, ok := raw["active"].(bool); ok {
		user.Active = active
	}
	return &user
}

func stringValue(value any) string {
	stringValue, _ := value.(string)
	return stringValue
}

func boolValue(value any) bool {
	result, _ := value.(bool)
	return result
}

func decodeProjects(raw []byte) ([]Project, error) {
	var list []wireProject
	if err := json.Unmarshal(raw, &list); err == nil {
		return normalizeProjects(list), nil
	}
	var paged struct {
		Values []wireProject `json:"values"`
	}
	if err := json.Unmarshal(raw, &paged); err != nil {
		return nil, apperr.Wrap(apperr.KindAPI, err, "decode Jira project response")
	}
	return normalizeProjects(paged.Values), nil
}

func normalizeProjects(wire []wireProject) []Project {
	projects := make([]Project, len(wire))
	for i, project := range wire {
		projects[i] = normalizeProject(project)
	}
	return projects
}
