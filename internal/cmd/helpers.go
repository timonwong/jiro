package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/timonwong/jiro/internal/apperr"
	"github.com/timonwong/jiro/internal/config"
	"github.com/timonwong/jiro/internal/jira"
	"github.com/timonwong/jiro/internal/markup"
)

var directCustomFieldID = regexp.MustCompile(`^customfield_[0-9]+$`)
var jqlRelativeDate = regexp.MustCompile(`^[+-][0-9]+[wdhm](?:\s+[0-9]+[wdhm])*$`)
var numericJiraID = regexp.MustCompile(`^[0-9]+$`)

type resolutionOption struct {
	fieldValue any
	output     string
}

type readFieldResolution struct {
	selection     []jira.FieldSelection
	resolved      []string
	sprintFieldID string
}

// IssueListJQLFilters contains the typed filters supported by issues list.
// Values in Labels, Components, and FixVersions are combined with IN; every
// populated field is combined with AND.
type IssueListJQLFilters struct {
	Project     string
	Status      string
	Assignee    string
	IssueType   string
	Resolution  string
	Reporter    string
	Labels      []string
	Components  []string
	FixVersions []string
	Sprint      string
	Parent      string
	Created     string
	Updated     string
}

// IssueListJQLOptions configures the Issue list JQL query. RawJQL is an
// explicitly supplied JQL expression, which is combined with Filters.
type IssueListJQLOptions struct {
	RawJQL  string
	Filters IssueListJQLFilters
}

func readText(ctx context.Context, reader io.Reader, inline string, inlineSet bool, file string) (*string, error) {
	if inlineSet && file != "" {
		return nil, apperr.New(apperr.KindInvalidInput, "inline text and file input are mutually exclusive")
	}
	if inlineSet {
		value := inline
		return &value, nil
	}
	if file == "" {
		return nil, nil
	}
	var data []byte
	var err error
	if file == "-" {
		closer, _ := reader.(io.Closer)
		data, err = readAllWithContext(ctx, reader, closer)
	} else {
		data, err = readFileWithContext(ctx, file)
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, err
		}
		return nil, apperr.Wrap(apperr.KindInvalidInput, err, "read text input")
	}
	value := string(data)
	return &value, nil
}

func (a *app) resolveCommentBody(command *cobra.Command, body, bodyFile, inputFormat string) (string, error) {
	format, err := parseInputFormat(inputFormat)
	if err != nil {
		return "", err
	}
	bodyValue, err := readText(command.Context(), a.stdin, body, command.Flags().Changed("body"), bodyFile)
	if err != nil {
		return "", err
	}
	if bodyValue == nil || *bodyValue == "" {
		return "", apperr.New(apperr.KindInvalidInput, "comment body is required")
	}
	bodyValue, err = a.convertToJiraMarkup(command.Context(), bodyValue, format, "body")
	if err != nil {
		return "", err
	}
	return *bodyValue, nil
}

type textInputFormat string

const (
	inputFormatJira textInputFormat = "jira"
	inputFormatJFM  textInputFormat = "jfm"
)

func parseInputFormat(value string) (textInputFormat, error) {
	switch strings.ToLower(value) {
	case "", "jira":
		return inputFormatJira, nil
	case "jfm", "markdown":
		return inputFormatJFM, nil
	default:
		return "", apperr.New(apperr.KindInvalidInput, "input format must be jira, jfm, or markdown")
	}
}

func (a *app) convertToJiraMarkup(ctx context.Context, input *string, format textInputFormat, field string) (*string, error) {
	if input == nil {
		return nil, nil
	}
	if format == inputFormatJira {
		return input, nil
	}
	converted, err := markup.FromJFM(ctx, *input)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, apperr.Wrap(apperr.KindUnexpected, err, "convert JFM input: %v", err)
	}
	for _, warning := range jfmConversionWarnings(jfmToJiraDirection, converted.Warnings, map[string]any{"field": field}) {
		a.addWarning(warning)
	}
	return &converted.Markup, nil
}

func (a *app) resolveFields(ctx context.Context, client *jira.Client, settings config.Settings, values []string) (map[string]any, error) {
	parsed, err := jira.ParseFieldValues(values)
	if err != nil {
		return nil, err
	}
	if len(parsed) == 0 {
		return map[string]any{}, nil
	}
	needsMetadata := false
	for key := range parsed {
		if !directCustomFieldID.MatchString(key) {
			needsMetadata = true
			break
		}
	}
	var metadata fieldMetadata
	if needsMetadata {
		metadata, err = a.loadFieldMetadata(ctx, client, settings)
		if err != nil {
			return nil, err
		}
	}
	resolve := func(fields []jira.Field) (map[string]any, error) {
		resolved := make(map[string]any, len(parsed))
		for key, value := range parsed {
			id, err := jira.ResolveCustomField(key, fields)
			if err != nil {
				return nil, err
			}
			resolved[id] = value
		}
		return resolved, nil
	}
	resolved, resolutionErr := resolve(metadata.fields)
	if resolutionErr == nil || !needsMetadata || metadata.refreshAttempted {
		return resolved, resolutionErr
	}
	refreshed, cacheErr, refreshErr := a.fetchFieldMetadata(ctx, client, settings, metadata.principal)
	if refreshErr != nil {
		return nil, apperr.New(apperr.KindInvalidInput, fmt.Sprintf("%v; live custom field refresh failed: %v", resolutionErr, refreshErr))
	}
	if cacheErr != nil {
		a.addFieldCacheWriteWarning(refreshed.path, cacheErr)
	}
	return resolve(refreshed.fields)
}

func (a *app) resolveReadFieldSelectors(
	ctx context.Context,
	client *jira.Client,
	settings config.Settings,
	selectors []string,
) (readFieldResolution, error) {
	if len(selectors) == 0 {
		return readFieldResolution{}, nil
	}
	needsMetadata, err := jira.FieldSelectorsNeedMetadata(selectors)
	if err != nil {
		return readFieldResolution{}, err
	}
	var fields []jira.Field
	var metadata fieldMetadata
	if needsMetadata {
		metadata, err = a.loadFieldMetadata(ctx, client, settings)
		if err != nil {
			return readFieldResolution{}, err
		}
		fields = metadata.fields
	}
	selection, err := jira.ResolveFieldSelectors(selectors, fields)
	if err != nil {
		if metadata.refreshErr != nil {
			return readFieldResolution{}, metadata.refreshErr
		}
		return readFieldResolution{}, err
	}
	resolved := make([]string, len(selection))
	for i, item := range selection {
		resolved[i] = item.Resolved
	}
	sprintFieldID, err := selectedSprintFieldID(selection, fields)
	if err != nil {
		return readFieldResolution{}, err
	}
	return readFieldResolution{selection: selection, resolved: resolved, sprintFieldID: sprintFieldID}, nil
}

func selectedSprintFieldID(selection []jira.FieldSelection, fields []jira.Field) (string, error) {
	const sprintSchemaCustom = "com.pyxis.greenhopper.jira:gh-sprint"
	byID := make(map[string]jira.Field, len(fields))
	for _, field := range fields {
		byID[field.ID] = field
	}
	for _, item := range selection {
		selectsMetadataField, err := jira.FieldSelectorSelectsMetadataField(item.Selector)
		if err != nil {
			return "", err
		}
		if !selectsMetadataField {
			continue
		}
		field, found := byID[item.Resolved]
		if found && field.SchemaCustom == sprintSchemaCustom {
			return field.ID, nil
		}
	}
	return "", nil
}

func parseResolutionOption(raw string, changed bool) (*resolutionOption, error) {
	if !changed {
		return nil, nil
	}
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, apperr.New(apperr.KindInvalidInput, "--resolution must not be empty")
	}
	if strings.EqualFold(value, "none") {
		return &resolutionOption{fieldValue: nil, output: "none"}, nil
	}
	if numericJiraID.MatchString(value) {
		return &resolutionOption{fieldValue: map[string]string{"id": value}, output: value}, nil
	}
	return &resolutionOption{fieldValue: map[string]string{"name": value}, output: value}, nil
}

func jqlLiteral(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}

// BuildIssueJQL builds a Jira-native issue list query from typed filters.
func BuildIssueJQL(options IssueListJQLOptions) (string, error) {
	raw, order := splitJQLOrder(options.RawJQL)
	filters := options.Filters
	clauses := make([]string, 0, 15)
	appendLiteral := func(field, value string) error {
		value, err := validJQLFilterValue(field, value)
		if err != nil {
			return err
		}
		if value != "" {
			clauses = append(clauses, field+" = "+jqlLiteral(value))
		}
		return nil
	}
	for _, filter := range []struct {
		field string
		value string
	}{
		{"project", filters.Project},
		{"status", filters.Status},
	} {
		if err := appendLiteral(filter.field, filter.value); err != nil {
			return "", err
		}
	}
	if err := appendCurrentUserOrLiteral(&clauses, "assignee", filters.Assignee); err != nil {
		return "", err
	}
	if err := appendLiteral("issuetype", filters.IssueType); err != nil {
		return "", err
	}
	resolution, err := validJQLFilterValue("resolution", filters.Resolution)
	if err != nil {
		return "", err
	}
	if strings.EqualFold(resolution, "unresolved") {
		clauses = append(clauses, "resolution IS EMPTY")
	} else if resolution != "" {
		clauses = append(clauses, "resolution = "+jqlLiteral(resolution))
	}
	if err := appendCurrentUserOrLiteral(&clauses, "reporter", filters.Reporter); err != nil {
		return "", err
	}
	for _, filter := range []struct {
		field  string
		values []string
	}{
		{"labels", filters.Labels},
		{"component", filters.Components},
		{"fixVersion", filters.FixVersions},
	} {
		clause, err := jqlInClause(filter.field, filter.values)
		if err != nil {
			return "", err
		}
		if clause != "" {
			clauses = append(clauses, clause)
		}
	}
	if err := appendSprintClause(&clauses, filters.Sprint); err != nil {
		return "", err
	}
	if err := appendLiteral("parent", filters.Parent); err != nil {
		return "", err
	}
	for _, filter := range []struct {
		field string
		value string
	}{
		{"created", filters.Created},
		{"updated", filters.Updated},
	} {
		dateClauses, err := jqlDateClauses(filter.field, filter.value)
		if err != nil {
			return "", err
		}
		clauses = append(clauses, dateClauses...)
	}
	if raw != "" {
		if len(clauses) > 0 {
			clauses = append([]string{"(" + raw + ")"}, clauses...)
		} else {
			clauses = append(clauses, raw)
		}
	}
	query := strings.Join(clauses, " AND ")
	if order != "" {
		if query == "" {
			return order, nil
		}
		return query + " " + order, nil
	}
	if query == "" {
		return "ORDER BY updated DESC", nil
	}
	return query + " ORDER BY updated DESC", nil
}

func splitJQLOrder(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if index := strings.LastIndex(strings.ToLower(raw), " order by "); index >= 0 {
		return strings.TrimSpace(raw[:index]), strings.TrimSpace(raw[index:])
	}
	if strings.HasPrefix(strings.ToLower(raw), "order by ") {
		return "", raw
	}
	return raw, ""
}

func validJQLFilterValue(field, value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", nil
	}
	if strings.ContainsAny(trimmed, "\x00\r\n") {
		return "", fmt.Errorf("invalid %s filter value", field)
	}
	return trimmed, nil
}

func appendCurrentUserOrLiteral(clauses *[]string, field, value string) error {
	value, err := validJQLFilterValue(field, value)
	if err != nil || value == "" {
		return err
	}
	if strings.EqualFold(value, "me") {
		*clauses = append(*clauses, field+" = currentUser()")
		return nil
	}
	*clauses = append(*clauses, field+" = "+jqlLiteral(value))
	return nil
}

func jqlInClause(field string, values []string) (string, error) {
	if len(values) == 0 {
		return "", nil
	}
	literals := make([]string, 0, len(values))
	for _, value := range values {
		value, err := validJQLFilterValue(field, value)
		if err != nil {
			return "", err
		}
		if value == "" {
			return "", fmt.Errorf("invalid %s filter value", field)
		}
		literals = append(literals, jqlLiteral(value))
	}
	return field + " IN (" + strings.Join(literals, ", ") + ")", nil
}

func appendSprintClause(clauses *[]string, sprint string) error {
	sprint, err := validJQLFilterValue("sprint", sprint)
	if err != nil || sprint == "" {
		return err
	}
	switch strings.ToLower(sprint) {
	case "active", "open":
		*clauses = append(*clauses, "sprint IN openSprints()")
	case "closed":
		*clauses = append(*clauses, "sprint IN closedSprints()")
	case "future":
		*clauses = append(*clauses, "sprint IN futureSprints()")
	default:
		if numericJiraID.MatchString(sprint) {
			*clauses = append(*clauses, "sprint = "+sprint)
			return nil
		}
		*clauses = append(*clauses, "sprint = "+jqlLiteral(sprint))
	}
	return nil
}

func jqlDateClauses(field, value string) ([]string, error) {
	value, err := validJQLFilterValue(field, value)
	if err != nil || value == "" {
		return nil, err
	}
	for _, layout := range []string{"2006-01-02", "2006/01/02"} {
		date, parseErr := time.Parse(layout, value)
		if parseErr == nil {
			return []string{
				field + " >= " + jqlLiteral(date.Format("2006-01-02")),
				field + " < " + jqlLiteral(date.AddDate(0, 0, 1).Format("2006-01-02")),
			}, nil
		}
	}
	if jqlRelativeDate.MatchString(value) {
		return []string{field + " >= " + jqlLiteral(value)}, nil
	}
	if function, ok := allowedJQLDateFunctions[strings.ToLower(value)]; ok {
		return []string{field + " >= " + function}, nil
	}
	return nil, fmt.Errorf("invalid %s date filter", field)
}

var allowedJQLDateFunctions = map[string]string{
	"now()":          "now()",
	"startofday()":   "startOfDay()",
	"endofday()":     "endOfDay()",
	"startofweek()":  "startOfWeek()",
	"endofweek()":    "endOfWeek()",
	"startofmonth()": "startOfMonth()",
	"endofmonth()":   "endOfMonth()",
	"startofyear()":  "startOfYear()",
	"endofyear()":    "endOfYear()",
}

func searchAll(ctx context.Context, client *jira.Client, jql string, startAt, maxResults int, fields []string) (jira.SearchResult, error) {
	if maxResults <= 0 {
		maxResults = 50
	}
	combined := jira.SearchResult{StartAt: startAt, MaxResults: maxResults}
	next := startAt
	seen := make(map[string]struct{})
	for {
		page, err := client.Search(ctx, jira.IssueListOptions{
			JQL:    jql,
			Page:   jira.Page{StartAt: next, MaxResults: maxResults},
			Fields: fields,
		})
		if err != nil {
			return jira.SearchResult{}, err
		}
		combined.Total = page.Total
		if len(page.Issues) == 0 {
			break
		}
		if page.StartAt != next {
			return jira.SearchResult{}, apperr.New(apperr.KindAPI, fmt.Sprintf("Jira search pagination did not advance: requested startAt %d, received %d", next, page.StartAt))
		}
		added := 0
		for _, issue := range page.Issues {
			_, duplicateKey := seen["key:"+issue.Key]
			_, duplicateID := seen["id:"+issue.ID]
			if (issue.Key != "" && duplicateKey) || (issue.ID != "" && duplicateID) {
				continue
			}
			if issue.Key != "" {
				seen["key:"+issue.Key] = struct{}{}
			}
			if issue.ID != "" {
				seen["id:"+issue.ID] = struct{}{}
			}
			combined.Issues = append(combined.Issues, issue)
			added++
		}
		if added == 0 {
			return jira.SearchResult{}, apperr.New(apperr.KindAPI, fmt.Sprintf("Jira search pagination returned no new issues at startAt %d", next))
		}
		if page.Total > 0 && next+len(page.Issues) >= page.Total {
			break
		}
		next += len(page.Issues)
	}
	return combined, nil
}

func issueRows(issues []jira.Issue) [][]string {
	rows := make([][]string, 0, len(issues))
	for _, issue := range issues {
		status := ""
		if issue.Status != nil {
			status = issue.Status.Name
		}
		assignee := ""
		if issue.Assignee != nil {
			assignee = issue.Assignee.DisplayName
			if assignee == "" {
				assignee = issue.Assignee.Username
			}
		}
		rows = append(rows, []string{issue.Key, issue.Summary, status, assignee})
	}
	return rows
}
