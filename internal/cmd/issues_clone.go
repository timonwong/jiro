package cmd

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/timonwong/jiro/internal/apperr"
	"github.com/timonwong/jiro/internal/config"
	"github.com/timonwong/jiro/internal/jira"
	"github.com/timonwong/jiro/internal/output"
)

var issueCloneSourceFields = []string{
	"project", "issuetype", "summary", "description", "priority", "assignee",
	"labels", "components", "fixVersions", "parent",
}

// issueCloneCommand creates a new Issue from one source Issue. Jira has no
// public REST clone operation, so the command keeps each post-create stage
// visible and never attempts to roll back an already-created Issue.
func (a *app) issueCloneCommand() *cobra.Command {
	var summary, description, descriptionFile, inputFormat, priority, assignee string
	var labels, components, fixVersions, fields []string
	var noLink bool
	command := &cobra.Command{
		Use:   "clone SOURCE",
		Short: "Clone an issue",
		Args:  exactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			client, settings, err := a.writableClient()
			if err != nil {
				return err
			}
			return a.runIssueClone(command, client, settings, args[0], cloneOptions{
				Summary: summary, SummarySet: command.Flags().Changed("summary"),
				Description: description, DescriptionSet: command.Flags().Changed("description"),
				DescriptionFile: descriptionFile, InputFormat: inputFormat,
				Priority: priority, PrioritySet: command.Flags().Changed("priority"),
				Assignee: assignee, AssigneeSet: command.Flags().Changed("assignee"),
				Labels: labels, LabelsSet: command.Flags().Changed("label"),
				Components: components, ComponentsSet: command.Flags().Changed("component"),
				FixVersions: fixVersions, FixVersionsSet: command.Flags().Changed("fix-version"),
				Fields: fields, NoLink: noLink,
			})
		},
	}
	flags := command.Flags()
	flags.StringVarP(&summary, "summary", "s", "", "replacement issue summary")
	flags.StringVar(&description, "description", "", "replacement description text")
	flags.StringVar(&descriptionFile, "description-file", "", "replacement description file path, or - for stdin")
	flags.StringVar(&inputFormat, "input-format", "jira", "text input format: jira, jfm, or markdown")
	flags.StringVar(&priority, "priority", "", "replacement priority name")
	flags.StringVar(&assignee, "assignee", "", "replacement assignee username; use none to clear")
	flags.StringSliceVar(&labels, "label", nil, "replacement label; repeat or pass comma-separated values")
	flags.StringSliceVar(&components, "component", nil, "replacement component name; use none to clear")
	flags.StringSliceVar(&fixVersions, "fix-version", nil, "replacement fix version name; use none to clear")
	flags.StringArrayVar(&fields, "field", nil, "custom field as alias=value or customfield_N=value; repeatable")
	flags.BoolVar(&noLink, "no-link", false, "do not create the default Cloners link")
	return command
}

type cloneOptions struct {
	Summary, Description, DescriptionFile, InputFormat string
	SummarySet, DescriptionSet                         bool
	Priority, Assignee                                 string
	PrioritySet, AssigneeSet                           bool
	Labels, Components, FixVersions, Fields            []string
	LabelsSet, ComponentsSet, FixVersionsSet           bool
	NoLink                                             bool
}

func (a *app) runIssueClone(command *cobra.Command, client *jira.Client, settings config.Settings, sourceKey string, options cloneOptions) error {
	ctx := command.Context()
	source, err := client.ShowIssue(ctx, sourceKey, issueCloneSourceFields)
	if err != nil {
		return err
	}
	if source.Project == nil || strings.TrimSpace(source.Project.Key) == "" {
		return apperr.New(apperr.KindAPI, fmt.Sprintf("Jira issue %s has no Project", sourceKey))
	}
	if source.IssueType == nil || strings.TrimSpace(source.IssueType.ID) == "" {
		return apperr.New(apperr.KindAPI, fmt.Sprintf("Jira issue %s has no Issue Type ID", sourceKey))
	}
	createFields, err := client.ListCreateFields(ctx, source.Project.Key, source.IssueType.ID)
	if err != nil {
		return err
	}
	metadata, err := a.loadFieldMetadata(ctx, client, settings)
	if err != nil {
		return err
	}
	fieldIDs := make([]string, 0, len(issueCloneSourceFields)+len(createFields)+1)
	fieldIDs = append(fieldIDs, issueCloneSourceFields...)
	sprintFieldIDs := make(map[string]struct{})
	for _, field := range createFields {
		if field.ID == "" {
			continue
		}
		if field.Custom {
			fieldIDs = append(fieldIDs, field.ID)
		}
		if field.SchemaCustom == "com.pyxis.greenhopper.jira:gh-sprint" {
			sprintFieldIDs[field.ID] = struct{}{}
		}
	}
	for _, field := range customFieldsOnly(metadata.fields) {
		if field.SchemaCustom == "com.pyxis.greenhopper.jira:gh-sprint" {
			fieldIDs = append(fieldIDs, field.ID)
			sprintFieldIDs[field.ID] = struct{}{}
		}
	}
	fieldIDs = uniqueStrings(fieldIDs)
	if len(sprintFieldIDs) > 1 {
		return apperr.New(apperr.KindAPI, fmt.Sprintf("Jira exposes multiple Sprint Custom Fields for issue %s", sourceKey))
	}
	source, err = client.ShowIssue(ctx, sourceKey, fieldIDs)
	if err != nil {
		return err
	}
	if source.Project == nil || strings.TrimSpace(source.Project.Key) == "" {
		return apperr.New(apperr.KindAPI, fmt.Sprintf("Jira issue %s has no Project", sourceKey))
	}
	if source.IssueType == nil || strings.TrimSpace(source.IssueType.ID) == "" {
		return apperr.New(apperr.KindAPI, fmt.Sprintf("Jira issue %s has no Issue Type ID", sourceKey))
	}
	if source.IssueType.Subtask && (source.Parent == nil || strings.TrimSpace(source.Parent.Key) == "") {
		return apperr.New(apperr.KindAPI, fmt.Sprintf("Jira sub-task %s has no Parent", sourceKey))
	}
	if rawParent, found := source.Fields["parent"]; found && rawParent != nil && (source.Parent == nil || strings.TrimSpace(source.Parent.Key) == "") {
		return apperr.New(apperr.KindAPI, fmt.Sprintf("Jira issue %s has invalid Parent data", sourceKey))
	}

	activeSprint, err := a.cloneActiveSprint(source, sprintFieldIDs)
	if err != nil {
		return err
	}
	var linkType jira.IssueLinkType
	if !options.NoLink {
		types, err := client.ListIssueLinkTypes(ctx)
		if err != nil {
			return err
		}
		linkType, err = matchIssueLinkType("Cloners", types)
		if err != nil {
			return err
		}
		linkID, parseErr := strconv.ParseInt(strings.TrimSpace(linkType.ID), 10, 64)
		if parseErr != nil || linkID <= 0 {
			return apperr.New(apperr.KindAPI, "Jira Cloners Link Type has an invalid ID")
		}
		if !strings.EqualFold(strings.TrimSpace(linkType.Outward), "clones") || !strings.EqualFold(strings.TrimSpace(linkType.Inward), "is cloned by") {
			return apperr.New(apperr.KindAPI, "Jira Cloners Link Type has unexpected direction")
		}
	}

	description, err := readText(ctx, a.stdin, options.Description, options.DescriptionSet, options.DescriptionFile)
	if err != nil {
		return err
	}
	format, err := parseInputFormat(options.InputFormat)
	if err != nil {
		return err
	}
	description, err = a.convertToJiraMarkup(ctx, description, format, "description")
	if err != nil {
		return err
	}

	resolvedFields := cloneCopiedFields(source, createFields, sprintFieldIDs, a)
	resolvedExplicit, err := a.resolveFields(ctx, client, settings, options.Fields)
	if err != nil {
		return err
	}
	for sprintFieldID := range sprintFieldIDs {
		if _, found := resolvedExplicit[sprintFieldID]; found {
			return apperr.New(apperr.KindInvalidInput, "Sprint Custom Fields cannot be set with --field during issue clone")
		}
	}
	for key, value := range resolvedExplicit {
		resolvedFields[key] = value
	}
	applyCloneStandardOverrides(resolvedFields, options)
	if source.Parent != nil && strings.TrimSpace(source.Parent.Key) != "" {
		resolvedFields["parent"] = map[string]string{"key": source.Parent.Key}
	}

	input := jira.CreateIssueInput{
		ProjectKey: source.Project.Key, IssueTypeID: source.IssueType.ID,
		Summary: cloneSummary(source.Summary, options), Description: description,
		Fields: resolvedFields,
	}
	clone, err := client.CreateIssue(ctx, input)
	if err != nil {
		return err
	}
	result := map[string]any{
		"sourceIssueKey": sourceKey,
		"id":             clone.ID,
		"key":            clone.Key,
		"linked":         false,
		"sprintMoved":    false,
	}
	if options.NoLink {
		result["link"] = map[string]any{"enabled": false, "created": false}
	} else {
		result["link"] = map[string]any{"enabled": true, "type": linkType, "created": false}
	}
	if activeSprint == nil {
		result["sprint"] = map[string]any{"assigned": false}
	} else {
		result["sprint"] = map[string]any{"id": activeSprint.ID, "assigned": false}
	}
	if !options.NoLink {
		if err := client.CreateIssueLink(ctx, jira.IssueLinkInput{From: sourceKey, To: clone.Key, TypeID: linkType.ID}); err != nil {
			if renderErr := a.renderPartial(result, fmt.Sprintf("Cloned %s to %s", sourceKey, clone.Key)); renderErr != nil {
				return renderErr
			}
			return apperr.Wrap(apperr.KindPartialFailure, err, "created %s but failed to create the Cloners link: %v", clone.Key, err)
		}
		result["linked"] = true
		result["link"].(map[string]any)["created"] = true
	}
	if activeSprint != nil {
		if err := client.AddIssuesToSprint(ctx, activeSprint.ID, []string{clone.Key}); err != nil {
			if renderErr := a.renderPartial(result, fmt.Sprintf("Cloned %s to %s", sourceKey, clone.Key)); renderErr != nil {
				return renderErr
			}
			return apperr.Wrap(apperr.KindPartialFailure, err, "created %s but failed to add it to Sprint %d: %v", clone.Key, activeSprint.ID, err)
		}
		result["sprintMoved"] = true
		result["sprint"].(map[string]any)["assigned"] = true
	}
	return a.renderMessage(result, fmt.Sprintf("Cloned %s to %s", sourceKey, clone.Key))
}

func cloneSummary(source string, options cloneOptions) string {
	if options.SummarySet {
		return options.Summary
	}
	return "CLONE - " + source
}

func applyCloneStandardOverrides(fields map[string]any, options cloneOptions) {
	if options.PrioritySet && options.Priority != "" {
		fields["priority"] = map[string]string{"name": options.Priority}
	}
	if options.AssigneeSet && options.Assignee != "" {
		if strings.EqualFold(options.Assignee, "none") {
			fields["assignee"] = nil
		} else {
			fields["assignee"] = map[string]string{"name": options.Assignee}
		}
	}
	if options.LabelsSet {
		fields["labels"] = options.Labels
	}
	applyNamedIssueField(fields, "components", options.Components, options.ComponentsSet, true)
	applyNamedIssueField(fields, "fixVersions", options.FixVersions, options.FixVersionsSet, true)
}

func cloneCopiedFields(source jira.Issue, createFields []jira.CreateField, sprintFieldIDs map[string]struct{}, app *app) map[string]any {
	fields := make(map[string]any)
	if source.Description != "" {
		fields["description"] = source.Description
	} else if _, found := source.Fields["description"]; found {
		fields["description"] = source.Description
	}
	if source.Priority != nil {
		fields["priority"] = namedOrID(source.Priority.ID, source.Priority.Name)
	}
	if source.Assignee != nil {
		if source.Assignee.Username != "" {
			fields["assignee"] = map[string]string{"name": source.Assignee.Username}
		} else if source.Assignee.AccountID != "" {
			fields["assignee"] = map[string]string{"accountId": source.Assignee.AccountID}
		}
	} else if _, found := source.Fields["assignee"]; found {
		fields["assignee"] = nil
	}
	if source.Labels != nil {
		fields["labels"] = append([]string(nil), source.Labels...)
	}
	if source.Components != nil {
		fields["components"] = cloneNamedValues(source.Components)
	}
	if source.FixVersions != nil {
		fields["fixVersions"] = cloneVersionValues(source.FixVersions)
	}
	if source.Parent != nil && source.Parent.Key != "" {
		fields["parent"] = map[string]string{"key": source.Parent.Key}
	}

	allowed := make(map[string]jira.CreateField, len(createFields))
	for _, field := range createFields {
		if field.Custom && createFieldWritable(field) {
			allowed[field.ID] = field
		}
	}
	customIDs := make([]string, 0)
	for id := range source.Fields {
		if directCustomFieldID.MatchString(id) {
			customIDs = append(customIDs, id)
		}
	}
	sort.Strings(customIDs)
	for _, id := range customIDs {
		field := source.Fields[id]
		if _, isSprint := sprintFieldIDs[id]; isSprint {
			continue
		}
		_, found := allowed[id]
		if found {
			if !cloneValueEmpty(field) {
				fields[id] = field
			}
			continue
		}
		if !cloneValueEmpty(field) {
			app.addWarning(cloneSkippedFieldWarning(id))
		}
	}
	return fields
}

func cloneSkippedFieldWarning(id string) output.Warning {
	return output.Warning{Code: "issue_clone_custom_field_skipped", Message: fmt.Sprintf("skipped non-empty Custom Field %s because it is not on the target Create screen", id), Details: map[string]any{"fieldId": id}}
}

func namedOrID(id, name string) map[string]string {
	if strings.TrimSpace(id) != "" {
		return map[string]string{"id": id}
	}
	return map[string]string{"name": name}
}

func cloneNamedValues(values []jira.Component) []map[string]string {
	result := make([]map[string]string, 0, len(values))
	for _, value := range values {
		result = append(result, namedOrID(value.ID, value.Name))
	}
	return result
}

func cloneVersionValues(values []jira.Version) []map[string]string {
	result := make([]map[string]string, 0, len(values))
	for _, value := range values {
		result = append(result, namedOrID(value.ID, value.Name))
	}
	return result
}

func cloneValueEmpty(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice:
		return v.Len() == 0
	case reflect.String:
		return strings.TrimSpace(v.String()) == ""
	}
	return false
}

func createFieldWritable(field jira.CreateField) bool {
	if field.Operations == nil {
		return true
	}
	for _, operation := range field.Operations {
		if strings.EqualFold(operation, "set") {
			return true
		}
	}
	return false
}

func (a *app) cloneActiveSprint(source jira.Issue, fieldIDs map[string]struct{}) (*jira.SprintMembership, error) {
	if len(fieldIDs) == 0 {
		return nil, nil
	}
	allMemberships := make([]jira.SprintMembership, 0)
	orderedFieldIDs := make([]string, 0, len(fieldIDs))
	for fieldID := range fieldIDs {
		orderedFieldIDs = append(orderedFieldIDs, fieldID)
	}
	sort.Strings(orderedFieldIDs)
	for _, fieldID := range orderedFieldIDs {
		memberships, failures := jira.NormalizeSprintMemberships(source.Fields[fieldID])
		if len(failures) > 0 {
			a.addWarning(output.Warning{Code: "sprint_membership_normalization", Message: "some Sprint memberships could not be fully normalized", Details: map[string]any{"issueKey": source.Key, "fieldId": fieldID, "failures": failures}})
		}
		allMemberships = append(allMemberships, memberships...)
	}
	active := make([]jira.SprintMembership, 0, 1)
	for _, membership := range allMemberships {
		if strings.EqualFold(membership.State, string(jira.SprintStateActive)) {
			active = append(active, membership)
		}
	}
	if len(active) > 1 {
		return nil, apperr.New(apperr.KindInvalidInput, fmt.Sprintf("Jira issue %s has multiple active Sprint Memberships", source.Key))
	}
	if len(active) == 1 {
		return &active[0], nil
	}
	return nil, nil
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, found := seen[value]; found {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
