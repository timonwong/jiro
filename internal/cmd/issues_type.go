package cmd

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/spf13/cobra"
	"github.com/timonwong/jiro/internal/apperr"
	"github.com/timonwong/jiro/internal/jira"
	"github.com/timonwong/jiro/internal/output"
)

type issueTypesResult struct {
	IssueKey         string           `json:"issueKey"`
	CurrentIssueType *jira.IssueType  `json:"currentIssueType"`
	IssueTypes       []jira.IssueType `json:"issueTypes"`
}

type issueTypeUpdateResult struct {
	Key       string          `json:"key"`
	Updated   bool            `json:"updated"`
	Unchanged bool            `json:"unchanged"`
	Unknown   bool            `json:"unknown"`
	IssueType *jira.IssueType `json:"issueType,omitempty"`
}

var issueTypeUpdateConflictingFlags = []string{
	"summary", "description", "description-file", "input-format", "priority", "assignee",
	"label", "component", "fix-version", "sprint", "field",
}

func (a *app) issueTypesCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list-types ISSUE-KEY",
		Short: "List Issue Types compatible with an issue",
		Args:  exactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			issue, err := client.ShowIssue(command.Context(), args[0], []string{"issuetype"})
			if err != nil {
				return err
			}
			if issue.IssueType == nil || strings.TrimSpace(issue.IssueType.ID) == "" {
				return apperr.New(apperr.KindAPI, fmt.Sprintf("Jira issue %s has no Issue Type", args[0]))
			}
			issueTypes, err := client.ListIssueTypesForIssue(command.Context(), args[0])
			if err != nil {
				return err
			}
			rows := make([][]string, 0, len(issueTypes))
			for _, issueType := range issueTypes {
				current := ""
				if issueType.ID == issue.IssueType.ID {
					current = "yes"
				}
				rows = append(rows, []string{issueType.ID, issueType.Name, fmt.Sprint(issueType.Subtask), current, issueType.Description})
			}
			result := issueTypesResult{IssueKey: issue.Key, CurrentIssueType: issue.IssueType, IssueTypes: issueTypes}
			return a.render(result, output.Table{
				Columns: []output.Column{
					output.Fixed("ID"), output.Flexible("NAME"), output.Fixed("SUBTASK"),
					output.Fixed("CURRENT"), output.Flexible("DESCRIPTION"),
				},
				Rows: rows,
			})
		},
	}
}

func issueTypeFlagConflicts(command *cobra.Command) []string {
	conflicts := make([]string, 0)
	for _, name := range issueTypeUpdateConflictingFlags {
		if command.Flags().Changed(name) {
			conflicts = append(conflicts, "--"+name)
		}
	}
	return conflicts
}

func (a *app) runIssueTypeUpdate(ctx context.Context, client *jira.Client, key, target string) error {
	issue, err := client.ShowIssue(ctx, key, []string{"issuetype"})
	if err != nil {
		return err
	}
	resolved, unchanged, err := preflightIssueTypeUpdate(ctx, client, issue, target)
	if err != nil {
		return err
	}
	if unchanged {
		result := issueTypeUpdateResult{Key: issue.Key, Unchanged: true, IssueType: issue.IssueType}
		return a.renderMessage(result, fmt.Sprintf("Issue type for %s is already %s", issue.Key, issue.IssueType.Name))
	}
	actual, unknown, updateErr := applyIssueTypeUpdate(ctx, client, issue.Key, resolved)
	result := issueTypeUpdateResult{Key: issue.Key, Unknown: unknown, IssueType: actual}
	if updateErr == nil {
		result.Updated = true
		return a.renderMessage(result, fmt.Sprintf("Updated %s issue type to %s", issue.Key, actual.Name))
	}
	if unknown {
		if err := a.renderPartial(result, fmt.Sprintf("Issue type update state is unknown for %s", issue.Key)); err != nil {
			return err
		}
		return apperr.Wrap(apperr.KindPartialFailure, updateErr,
			"issue type update state is unknown for %s; do not retry without checking Jira", issue.Key)
	}
	if actual != nil {
		if err := a.renderPartial(result, fmt.Sprintf("Issue type verification failed for %s", issue.Key)); err != nil {
			return err
		}
		return apperr.Wrap(apperr.KindPartialFailure, updateErr,
			"issue type update for %s did not verify; readback returned %s (%s)", issue.Key, actual.Name, actual.ID)
	}
	return updateErr
}

func preflightIssueTypeUpdate(ctx context.Context, client *jira.Client, issue jira.Issue, target string) (jira.IssueType, bool, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return jira.IssueType{}, false, apperr.New(apperr.KindInvalidInput, "issue type is required")
	}
	if issue.IssueType == nil || strings.TrimSpace(issue.IssueType.ID) == "" {
		return jira.IssueType{}, false, apperr.New(apperr.KindInvalidInput, fmt.Sprintf("Jira issue %s has no Issue Type", issue.Key))
	}
	if target == issue.IssueType.ID {
		return *issue.IssueType, true, nil
	}
	allowed, err := client.ListIssueTypesForIssue(ctx, issue.Key)
	if err != nil {
		return jira.IssueType{}, false, err
	}
	resolved, err := matchIssueType(target, allowed)
	if err != nil {
		return jira.IssueType{}, false, apperr.New(apperr.KindInvalidInput,
			fmt.Sprintf("issue type %q is not compatible with %s: %v; allowed Issue Types: %s",
				target, issue.Key, err, formatIssueTypes(allowed)))
	}
	return resolved, resolved.ID == issue.IssueType.ID, nil
}

func applyIssueTypeUpdate(ctx context.Context, client *jira.Client, key string, target jira.IssueType) (*jira.IssueType, bool, error) {
	writeErr := client.UpdateIssueType(ctx, key, target.ID)
	if writeErr != nil && !issueTypeWriteMayHaveSucceeded(writeErr) {
		return nil, false, writeErr
	}
	issue, err := client.ShowIssue(ctx, key, []string{"issuetype"})
	if err != nil {
		if writeErr != nil {
			err = apperr.Wrap(apperr.KindAPI, err,
				"issue type write for %s returned an indeterminate error and readback failed: write: %v; readback: %v",
				key, writeErr, err)
		}
		return nil, true, err
	}
	if issue.IssueType == nil || strings.TrimSpace(issue.IssueType.ID) == "" {
		return nil, true, apperr.New(apperr.KindAPI,
			fmt.Sprintf("issue type readback for %s did not contain a valid Issue Type", key))
	}
	if issue.IssueType.ID != target.ID {
		actual := issue.IssueType
		message := fmt.Sprintf("issue type readback mismatch for %s: wanted %s (%s), got %s (%s)",
			key, target.Name, target.ID, actual.Name, actual.ID)
		if writeErr != nil {
			message += "; write returned an indeterminate error: " + writeErr.Error()
		}
		return actual, false, apperr.New(apperr.KindAPI, message)
	}
	return issue.IssueType, false, nil
}

func issueTypeWriteMayHaveSucceeded(err error) bool {
	appErr := apperr.As(err)
	return appErr.Kind == apperr.KindAPI &&
		(appErr.StatusCode == 0 || appErr.StatusCode == http.StatusRequestTimeout || appErr.StatusCode >= http.StatusInternalServerError)
}

func matchIssueType(target string, issueTypes []jira.IssueType) (jira.IssueType, error) {
	target = strings.TrimSpace(target)
	for _, issueType := range issueTypes {
		if issueType.ID == target {
			return issueType, nil
		}
	}
	matches := make([]jira.IssueType, 0, 1)
	for _, issueType := range issueTypes {
		if strings.EqualFold(issueType.Name, target) {
			matches = append(matches, issueType)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return jira.IssueType{}, apperr.New(apperr.KindInvalidInput, fmt.Sprintf("issue type %q was not found", target))
	default:
		return jira.IssueType{}, apperr.New(apperr.KindInvalidInput, fmt.Sprintf("issue type %q is ambiguous", target))
	}
}

func formatIssueTypes(issueTypes []jira.IssueType) string {
	if len(issueTypes) == 0 {
		return "none"
	}
	values := make([]string, len(issueTypes))
	for i, issueType := range issueTypes {
		values[i] = fmt.Sprintf("%s (%s)", issueType.Name, issueType.ID)
	}
	return strings.Join(values, ", ")
}
