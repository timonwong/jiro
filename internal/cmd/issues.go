package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/timonwong/jiro/internal/apperr"
	"github.com/timonwong/jiro/internal/jira"
	"github.com/timonwong/jiro/internal/output"
)

func (a *app) issueCommand() *cobra.Command {
	command := &cobra.Command{Use: "issue", Short: "Work with Jira issues"}
	comment := &cobra.Command{Use: "comment", Short: "Work with issue comments"}
	comment.AddCommand(a.issueCommentCommand(), a.issueCommentsCommand())
	link := &cobra.Command{Use: "link", Short: "Work with issue links"}
	link.AddCommand(a.issueLinkCommand(), a.issueLinksCommand(), a.issueUnlinkCommand(), a.issueLinkTypesCommand())
	bulk := &cobra.Command{Use: "bulk", Short: "Apply one action to issues selected by JQL"}
	bulk.AddCommand(a.issueBulkTransitionCommand(), a.issueBulkAssignCommand(), a.issueBulkUpdateCommand())
	command.AddCommand(
		a.issuesListCommand(),
		a.issueShowCommand(),
		a.issueCreateCommand(),
		a.issueUpdateCommand(),
		a.issueTypesCommand(),
		a.issueTransitionsCommand(),
		a.issueTransitionCommand(),
		a.issueAssignCommand(),
		comment,
		link,
		bulk,
	)
	return command
}

func (a *app) issuesListCommand() *cobra.Command {
	var project, status, assignee, issueType, rawJQL string
	var resolution, reporter, sprint, parent, created, updated string
	var limit, offset int
	var all bool
	var fields, labels, components, fixVersions []string
	command := &cobra.Command{
		Use:   "list",
		Short: "List issues",
		Args:  exactArgs(0),
		RunE: func(command *cobra.Command, _ []string) error {
			if err := validatePagination(offset, limit); err != nil {
				return err
			}
			client, settings, err := a.client()
			if err != nil {
				return err
			}
			jql, err := BuildIssueJQL(IssueListJQLOptions{
				RawJQL: rawJQL,
				Filters: IssueListJQLFilters{
					Project: project, Status: status, Assignee: assignee, IssueType: issueType,
					Resolution: resolution, Reporter: reporter, Labels: labels, Components: components,
					FixVersions: fixVersions, Sprint: sprint, Parent: parent, Created: created, Updated: updated,
				},
			})
			if err != nil {
				return err
			}
			fieldResolution, err := a.resolveReadFieldSelectors(command.Context(), client, settings, fields)
			if err != nil {
				return err
			}
			var result jira.SearchResult
			if all {
				result, err = searchAll(command.Context(), client, jql, offset, limit, fieldResolution.resolved)
			} else {
				result, err = client.ListIssues(command.Context(), jira.IssueListOptions{
					JQL: jql, Page: jira.Page{StartAt: offset, MaxResults: limit}, Fields: fieldResolution.resolved,
				})
			}
			if err != nil {
				return err
			}
			a.normalizeSearchSprintMemberships(&result, fieldResolution.sprintFieldID)
			return a.render(searchOutput{SearchResult: result, FieldSelection: fieldResolution.selection}, output.Table{
				Columns: []output.Column{output.Fixed("KEY"), output.Flexible("SUMMARY"), output.Flexible("STATUS"), output.Flexible("ASSIGNEE")},
				Rows:    issueRows(result.Issues),
			})
		},
	}
	flags := command.Flags()
	flags.StringVarP(&project, "project", "p", "", "filter by project key")
	flags.StringVar(&status, "status", "", "filter by status")
	flags.StringVar(&assignee, "assignee", "", "filter by assignee; use me for currentUser()")
	flags.StringVarP(&issueType, "type", "t", "", "filter by issue type")
	flags.StringVar(&resolution, "resolution", "", "filter by resolution; use unresolved for empty resolution")
	flags.StringVar(&reporter, "reporter", "", "filter by reporter; use me for currentUser()")
	flags.StringSliceVar(&labels, "label", nil, "filter by label; repeatable")
	flags.StringSliceVar(&components, "component", nil, "filter by component name; repeatable")
	flags.StringSliceVar(&fixVersions, "fix-version", nil, "filter by fix version name; repeatable")
	flags.StringVar(&sprint, "sprint", "", "filter by sprint ID, name, active/open, closed, or future")
	flags.StringVar(&parent, "parent", "", "filter by parent issue key")
	flags.StringVar(&created, "created", "", "filter by Jira absolute or relative created date")
	flags.StringVar(&updated, "updated", "", "filter by Jira absolute or relative updated date")
	flags.StringVarP(&rawJQL, "jql", "q", "", "additional raw JQL expression")
	flags.IntVarP(&limit, "limit", "n", 50, "maximum issues per page")
	flags.IntVar(&offset, "offset", 0, "zero-based result offset")
	flags.BoolVar(&all, "all", false, "fetch all result pages")
	flags.StringSliceVar(&fields, "fields", nil, "comma-separated Field Selectors to resolve and request")
	return command
}

func (a *app) issueShowCommand() *cobra.Command {
	var fields []string
	command := &cobra.Command{
		Use:   "show ISSUE-KEY",
		Short: "Show an issue",
		Args:  exactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			client, settings, err := a.client()
			if err != nil {
				return err
			}
			fieldResolution, err := a.resolveReadFieldSelectors(command.Context(), client, settings, fields)
			if err != nil {
				return err
			}
			issue, err := client.ShowIssue(command.Context(), args[0], fieldResolution.resolved)
			if err != nil {
				return err
			}
			a.normalizeIssueSprintMemberships(&issue, fieldResolution.sprintFieldID)
			return a.renderIssueShow(issue, fieldResolution.selection)
		},
	}
	command.Flags().StringSliceVar(&fields, "fields", nil, "comma-separated Field Selectors to resolve and request")
	return command
}

func (a *app) issueCreateCommand() *cobra.Command {
	var project, issueType, summary, description, descriptionFile, inputFormat, parent, sprint string
	var priority, assignee string
	var labels, components, fixVersions, fields []string
	command := &cobra.Command{
		Use:   "add",
		Short: "Create an issue",
		Args:  exactArgs(0),
		RunE: func(command *cobra.Command, _ []string) error {
			if strings.TrimSpace(project) == "" || strings.TrimSpace(issueType) == "" || strings.TrimSpace(summary) == "" {
				return apperr.New(apperr.KindInvalidInput, "project, type, and summary are required")
			}
			client, settings, err := a.writableClient()
			if err != nil {
				return err
			}
			format, err := parseInputFormat(inputFormat)
			if err != nil {
				return err
			}
			descriptionValue, err := readText(a.stdin, description, command.Flags().Changed("description"), descriptionFile)
			if err != nil {
				return err
			}
			descriptionValue, err = a.convertToJiraMarkup(command.Context(), descriptionValue, format, "description")
			if err != nil {
				return err
			}
			resolvedFields, err := a.resolveFields(command.Context(), client, settings, fields)
			if err != nil {
				return err
			}
			applyStandardFields(resolvedFields, priority, assignee, labels, command.Flags().Changed("label"))
			if strings.TrimSpace(parent) != "" {
				resolvedFields["parent"] = map[string]string{"key": parent}
			}
			applyNamedIssueField(resolvedFields, "components", components, len(components) > 0, false)
			applyNamedIssueField(resolvedFields, "fixVersions", fixVersions, len(fixVersions) > 0, false)
			input := jira.CreateIssueInput{
				ProjectKey: project, IssueType: issueType, Summary: summary,
				Description: descriptionValue, Fields: resolvedFields,
			}
			issue, err := client.CreateIssue(command.Context(), input)
			if err != nil {
				return err
			}
			result := map[string]any{"id": issue.ID, "key": issue.Key}
			if strings.TrimSpace(sprint) != "" {
				result["sprint"] = sprint
				if err := client.MoveIssueToSprint(command.Context(), issue.Key, jira.MoveIssueToSprintInput{Sprint: sprint}); err != nil {
					result["sprintMoved"] = false
					if renderErr := a.renderPartial(result, "Created "+issue.Key); renderErr != nil {
						return renderErr
					}
					return apperr.Wrap(apperr.KindPartialFailure, err, "created %s but failed to move it to sprint %q: %v", issue.Key, sprint, err)
				}
				result["sprintMoved"] = true
			}
			return a.renderMessage(result, "Created "+issue.Key)
		},
	}
	flags := command.Flags()
	flags.StringVarP(&project, "project", "p", "", "project key")
	flags.StringVarP(&issueType, "type", "t", "", "issue type")
	flags.StringVarP(&summary, "summary", "s", "", "issue summary")
	flags.StringVar(&description, "description", "", "description text")
	flags.StringVar(&descriptionFile, "description-file", "", "description file path, or - for stdin")
	flags.StringVar(&inputFormat, "input-format", "jira", "text input format: jira, jfm, or markdown")
	flags.StringVar(&priority, "priority", "", "priority name")
	flags.StringVar(&assignee, "assignee", "", "assignee username; use none to clear")
	flags.StringSliceVar(&labels, "label", nil, "label; repeat or pass comma-separated values")
	flags.StringVar(&parent, "parent", "", "parent issue key")
	flags.StringSliceVar(&components, "component", nil, "component name; repeatable")
	flags.StringSliceVar(&fixVersions, "fix-version", nil, "fix version name; repeatable")
	flags.StringVar(&sprint, "sprint", "", "sprint ID, name substring, or active")
	flags.StringArrayVar(&fields, "field", nil, "custom field as alias=value or customfield_N=value; repeatable")
	return command
}

func (a *app) issueUpdateCommand() *cobra.Command {
	var summary, description, descriptionFile, inputFormat, issueType string
	var priority, assignee, sprint string
	var labels, components, fixVersions, fields []string
	command := &cobra.Command{
		Use:   "update ISSUE-KEY",
		Short: "Update an issue",
		Args:  exactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			if command.Flags().Changed("type") {
				if strings.TrimSpace(issueType) == "" {
					return apperr.New(apperr.KindInvalidInput, "--type must not be empty")
				}
				if conflicts := issueTypeFlagConflicts(command); len(conflicts) > 0 {
					return apperr.New(apperr.KindInvalidInput,
						fmt.Sprintf("--type cannot be combined with other issue update flags: %s", strings.Join(conflicts, ", ")))
				}
				client, _, err := a.writableClient()
				if err != nil {
					return err
				}
				return a.runIssueTypeUpdate(command.Context(), client, args[0], issueType)
			}
			client, settings, err := a.writableClient()
			if err != nil {
				return err
			}
			format, err := parseInputFormat(inputFormat)
			if err != nil {
				return err
			}
			descriptionValue, err := readText(a.stdin, description, command.Flags().Changed("description"), descriptionFile)
			if err != nil {
				return err
			}
			descriptionValue, err = a.convertToJiraMarkup(command.Context(), descriptionValue, format, "description")
			if err != nil {
				return err
			}
			var resolvedSprint *jira.Sprint
			if strings.TrimSpace(sprint) != "" {
				value, err := client.ResolveSprint(command.Context(), sprint)
				if err != nil {
					return err
				}
				resolvedSprint = &value
			}
			resolvedFields, err := a.resolveFields(command.Context(), client, settings, fields)
			if err != nil {
				return err
			}
			applyStandardFields(resolvedFields, priority, assignee, labels, command.Flags().Changed("label"))
			applyNamedIssueField(resolvedFields, "components", components, command.Flags().Changed("component"), true)
			applyNamedIssueField(resolvedFields, "fixVersions", fixVersions, command.Flags().Changed("fix-version"), true)
			input := jira.UpdateIssueInput{Description: descriptionValue, Fields: resolvedFields}
			if command.Flags().Changed("summary") {
				input.Summary = &summary
			}
			hasFieldUpdate := input.Summary != nil || input.Description != nil || len(input.Fields) > 0
			if !hasFieldUpdate && resolvedSprint == nil {
				return apperr.New(apperr.KindInvalidInput, "at least one issue field is required")
			}
			if hasFieldUpdate {
				if err := client.UpdateIssue(command.Context(), args[0], input); err != nil {
					return err
				}
			}
			result := map[string]any{"key": args[0], "updated": true}
			if resolvedSprint != nil {
				result["sprint"] = sprint
				if err := client.AddIssuesToSprint(command.Context(), resolvedSprint.ID, []string{args[0]}); err != nil {
					if !hasFieldUpdate {
						return err
					}
					result["sprintMoved"] = false
					if renderErr := a.renderPartial(result, "Updated "+args[0]); renderErr != nil {
						return renderErr
					}
					return apperr.Wrap(apperr.KindPartialFailure, err, "updated %s but failed to move it to sprint %q: %v", args[0], sprint, err)
				}
				result["sprintMoved"] = true
			}
			return a.renderMessage(result, "Updated "+args[0])
		},
	}
	flags := command.Flags()
	flags.StringVarP(&summary, "summary", "s", "", "new issue summary")
	flags.StringVarP(&issueType, "type", "t", "", "compatible issue type ID or unique name; cannot be combined with other update flags")
	flags.StringVar(&description, "description", "", "description text")
	flags.StringVar(&descriptionFile, "description-file", "", "description file path, or - for stdin")
	flags.StringVar(&inputFormat, "input-format", "jira", "text input format: jira, jfm, or markdown")
	flags.StringVar(&priority, "priority", "", "priority name")
	flags.StringVar(&assignee, "assignee", "", "assignee username; use none to clear")
	flags.StringSliceVar(&labels, "label", nil, "replacement labels; repeat or pass comma-separated values")
	flags.StringSliceVar(&components, "component", nil, "replacement component names; use a single none to clear")
	flags.StringSliceVar(&fixVersions, "fix-version", nil, "replacement fix version names; use a single none to clear")
	flags.StringVar(&sprint, "sprint", "", "sprint ID, name substring, or active")
	flags.StringArrayVar(&fields, "field", nil, "custom field as alias=value or customfield_N=value; repeatable")
	return command
}

func (a *app) issueCommentsCommand() *cobra.Command {
	var limit, offset int
	command := &cobra.Command{
		Use:   "list ISSUE-KEY",
		Short: "List issue comments",
		Args:  exactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			if err := validatePagination(offset, limit); err != nil {
				return err
			}
			client, _, err := a.client()
			if err != nil {
				return err
			}
			comments, err := client.Comments(command.Context(), args[0], jira.Page{StartAt: offset, MaxResults: limit})
			if err != nil {
				return err
			}
			rows := make([][]string, 0, len(comments))
			for _, comment := range comments {
				author := ""
				if comment.Author != nil {
					author = comment.Author.DisplayName
				}
				rows = append(rows, []string{comment.ID, author, comment.Created, comment.Body})
			}
			data := map[string]any{"issueKey": args[0], "comments": comments}
			return a.render(data, output.Table{
				Columns: []output.Column{output.Fixed("ID"), output.Flexible("AUTHOR"), output.Flexible("CREATED"), output.Flexible("BODY")},
				Rows:    rows,
			})
		},
	}
	command.Flags().IntVarP(&limit, "limit", "n", 50, "maximum comments")
	command.Flags().IntVar(&offset, "offset", 0, "zero-based result offset")
	return command
}

func (a *app) issueCommentCommand() *cobra.Command {
	var body, bodyFile, inputFormat string
	command := &cobra.Command{
		Use:   "add ISSUE-KEY",
		Short: "Add an issue comment",
		Args:  exactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			client, _, err := a.writableClient()
			if err != nil {
				return err
			}
			format, err := parseInputFormat(inputFormat)
			if err != nil {
				return err
			}
			bodyValue, err := readText(a.stdin, body, command.Flags().Changed("body"), bodyFile)
			if err != nil {
				return err
			}
			if bodyValue == nil || *bodyValue == "" {
				return apperr.New(apperr.KindInvalidInput, "comment body is required")
			}
			bodyValue, err = a.convertToJiraMarkup(command.Context(), bodyValue, format, "body")
			if err != nil {
				return err
			}
			input := jira.CommentInput{Body: *bodyValue}
			comment, err := client.Comment(command.Context(), args[0], input)
			if err != nil {
				return err
			}
			return a.renderMessage(comment, "Commented on "+args[0])
		},
	}
	flags := command.Flags()
	flags.StringVar(&body, "body", "", "comment body")
	flags.StringVar(&bodyFile, "body-file", "", "comment body file path, or - for stdin")
	flags.StringVar(&inputFormat, "input-format", "jira", "text input format: jira, jfm, or markdown")
	return command
}

func (a *app) issueTransitionsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list-transitions ISSUE-KEY",
		Short: "List available issue transitions",
		Args:  exactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			transitions, err := client.ListTransitions(command.Context(), args[0])
			if err != nil {
				return err
			}
			rows := make([][]string, 0, len(transitions))
			for _, transition := range transitions {
				to := ""
				if transition.To != nil {
					to = transition.To.Name
				}
				rows = append(rows, []string{transition.ID, transition.Name, to})
			}
			return a.render(map[string]any{"issueKey": args[0], "transitions": transitions}, output.Table{
				Columns: []output.Column{output.Fixed("ID"), output.Flexible("NAME"), output.Flexible("TO")}, Rows: rows,
			})
		},
	}
}

func (a *app) issueTransitionCommand() *cobra.Command {
	var target, comment, inputFormat, resolution string
	var fields []string
	command := &cobra.Command{
		Use:   "move ISSUE-KEY",
		Short: "Transition an issue",
		Args:  exactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			if strings.TrimSpace(target) == "" {
				return apperr.New(apperr.KindInvalidInput, "--to is required")
			}
			commentChanged := command.Flags().Changed("comment")
			if command.Flags().Changed("input-format") && !commentChanged {
				return apperr.New(apperr.KindInvalidInput, "--input-format requires --comment")
			}
			var commentValue *string
			if commentChanged {
				if strings.TrimSpace(comment) == "" {
					return apperr.New(apperr.KindInvalidInput, "--comment must not be empty")
				}
				format, err := parseInputFormat(inputFormat)
				if err != nil {
					return err
				}
				commentValue = &comment
				commentValue, err = a.convertToJiraMarkup(command.Context(), commentValue, format, "comment")
				if err != nil {
					return err
				}
			}
			resolutionValue, err := parseResolutionOption(resolution, command.Flags().Changed("resolution"))
			if err != nil {
				return err
			}
			client, settings, err := a.writableClient()
			if err != nil {
				return err
			}
			transitions, err := client.ListTransitions(command.Context(), args[0])
			if err != nil {
				return err
			}
			transition, err := matchTransition(target, transitions)
			if err != nil {
				return err
			}
			resolvedFields, err := a.resolveFields(command.Context(), client, settings, fields)
			if err != nil {
				return err
			}
			if resolutionValue != nil {
				resolvedFields["resolution"] = resolutionValue.fieldValue
			}
			input := jira.TransitionInput{ID: transition.ID, Fields: resolvedFields, Comment: commentValue}
			if err := client.Transition(command.Context(), args[0], input); err != nil {
				return err
			}
			result := map[string]any{
				"key": args[0], "transition": transition, "transitioned": true, "commented": commentChanged,
			}
			if resolutionValue != nil {
				result["resolution"] = resolutionValue.output
			}
			return a.renderMessage(result, fmt.Sprintf("Transitioned %s to %s", args[0], transition.Name))
		},
	}
	flags := command.Flags()
	flags.StringVar(&target, "to", "", "transition ID or name")
	flags.StringVar(&comment, "comment", "", "comment added by the transition")
	flags.StringVar(&inputFormat, "input-format", "jira", "comment input format: jira, jfm, or markdown")
	flags.StringVar(&resolution, "resolution", "", "resolution name or numeric ID; use none to clear")
	flags.StringArrayVar(&fields, "field", nil, "custom field as alias=value or customfield_N=value; repeatable")
	return command
}

func applyStandardFields(fields map[string]any, priority, assignee string, labels []string, labelsSet bool) {
	if priority != "" {
		fields["priority"] = map[string]string{"name": priority}
	}
	if assignee != "" {
		if strings.EqualFold(assignee, "none") {
			fields["assignee"] = nil
		} else {
			fields["assignee"] = map[string]string{"name": assignee}
		}
	}
	if labelsSet {
		fields["labels"] = labels
	}
}

func applyNamedIssueField(fields map[string]any, key string, values []string, changed, allowClear bool) {
	if !changed {
		return
	}
	if allowClear && len(values) == 1 && strings.EqualFold(strings.TrimSpace(values[0]), "none") {
		fields[key] = []map[string]string{}
		return
	}
	named := make([]map[string]string, 0, len(values))
	for _, value := range values {
		named = append(named, map[string]string{"name": value})
	}
	fields[key] = named
}

func issueShowSections(issue jira.Issue) []issueShowSection {
	status, issueType, priority, assignee, reporter, parent := "", "", "", "", "", ""
	if issue.Status != nil {
		status = issue.Status.Name
	}
	if issue.IssueType != nil {
		issueType = issue.IssueType.Name
	}
	if issue.Priority != nil {
		priority = issue.Priority.Name
	}
	if issue.Assignee != nil {
		assignee = issue.Assignee.DisplayName
	}
	if issue.Reporter != nil {
		reporter = issue.Reporter.DisplayName
	}
	if issue.Parent != nil {
		parent = issue.Parent.Key
	}
	components := make([]string, 0, len(issue.Components))
	for _, component := range issue.Components {
		components = append(components, component.Name)
	}
	fixVersions := make([]string, 0, len(issue.FixVersions))
	for _, version := range issue.FixVersions {
		fixVersions = append(fixVersions, version.Name)
	}
	return []issueShowSection{
		{header: "KEY", value: issue.Key},
		{header: "SUMMARY", value: issue.Summary},
		{header: "STATUS", value: status},
		{header: "TYPE", value: issueType},
		{header: "PRIORITY", value: priority},
		{header: "ASSIGNEE", value: assignee},
		{header: "REPORTER", value: reporter},
		{header: "LABELS", value: joinNames(issue.Labels)},
		{header: "PARENT", value: parent},
		{header: "COMPONENTS", value: joinNames(components)},
		{header: "FIX VERSIONS", value: joinNames(fixVersions)},
		{header: "DESCRIPTION", value: issue.Description},
	}
}

func matchTransition(target string, transitions []jira.Transition) (jira.Transition, error) {
	matches := make([]jira.Transition, 0, 1)
	for _, transition := range transitions {
		matchesTarget := transition.ID == target || strings.EqualFold(transition.Name, target)
		if transition.To != nil && strings.EqualFold(transition.To.Name, target) {
			matchesTarget = true
		}
		if matchesTarget {
			matches = append(matches, transition)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) == 0 {
		return jira.Transition{}, apperr.New(apperr.KindInvalidInput, fmt.Sprintf("transition %q was not found", target))
	}
	return jira.Transition{}, apperr.New(apperr.KindInvalidInput, fmt.Sprintf("transition %q is ambiguous", target))
}
