package cmd

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/spf13/cobra"
	"github.com/timonwong/jiro/internal/apperr"
	"github.com/timonwong/jiro/internal/config"
	"github.com/timonwong/jiro/internal/jira"
	"github.com/timonwong/jiro/internal/output"
)

type issueBatchResult struct {
	Operation    string           `json:"operation"`
	DryRun       bool             `json:"dryRun"`
	JQL          string           `json:"jql"`
	Total        int              `json:"total"`
	Ready        int              `json:"ready"`
	Succeeded    int              `json:"succeeded"`
	Failed       int              `json:"failed"`
	Unchanged    int              `json:"unchanged"`
	Unknown      int              `json:"unknown"`
	NotAttempted int              `json:"notAttempted"`
	Items        []issueBatchItem `json:"items"`
}

type issueBatchItem struct {
	IssueKey        string            `json:"issueKey"`
	Outcome         string            `json:"outcome"`
	Current         issueBatchCurrent `json:"current"`
	Target          issueBatchTarget  `json:"target"`
	ActualIssueType *jira.IssueType   `json:"actualIssueType,omitempty"`
	Error           string            `json:"error,omitempty"`
}

type issueBatchCurrent struct {
	Status    *jira.Status    `json:"status,omitempty"`
	Assignee  *jira.User      `json:"assignee,omitempty"`
	IssueType *jira.IssueType `json:"issueType,omitempty"`
}

type issueBatchTarget struct {
	TransitionSpec string           `json:"transitionSpec,omitempty"`
	Transition     *jira.Transition `json:"transition,omitempty"`
	Resolution     string           `json:"resolution,omitempty"`
	Assignee       *string          `json:"assignee,omitempty"`
	Unassigned     bool             `json:"unassigned,omitempty"`
	IssueTypeInput string           `json:"issueTypeInput,omitempty"`
	IssueType      *jira.IssueType  `json:"issueType,omitempty"`
}

func (a *app) issueBulkTransitionCommand() *cobra.Command {
	var jql, target, resolution string
	var fields []string
	var dryRun, yes bool
	command := &cobra.Command{
		Use:   "move",
		Short: "Transition every issue selected by JQL",
		Args:  exactArgs(0),
		RunE: func(command *cobra.Command, _ []string) error {
			if strings.TrimSpace(jql) == "" || strings.TrimSpace(target) == "" {
				return apperr.New(apperr.KindInvalidInput, "--jql and --to are required")
			}
			if dryRun == yes {
				return apperr.New(apperr.KindInvalidInput, "exactly one of --dry-run or --yes is required")
			}
			resolutionValue, err := parseResolutionOption(resolution, command.Flags().Changed("resolution"))
			if err != nil {
				return err
			}
			var client *jira.Client
			var settings config.Settings
			if dryRun {
				client, settings, err = a.client()
			} else {
				client, settings, err = a.writableClient()
			}
			if err != nil {
				return err
			}
			issues, err := searchAll(command.Context(), client, jql, 0, 50, []string{"status", "assignee"})
			if err != nil {
				return err
			}
			result := issueBatchResult{
				Operation: "transition", DryRun: dryRun, JQL: jql, Total: len(issues.Issues),
				Items: make([]issueBatchItem, 0, len(issues.Issues)),
			}
			if len(issues.Issues) == 0 {
				return a.finishIssueBatch(result)
			}
			resolvedFields, err := a.resolveFields(command.Context(), client, settings, fields)
			if err != nil {
				return err
			}
			if resolutionValue != nil {
				resolvedFields["resolution"] = resolutionValue.fieldValue
			}
			batchTarget := issueBatchTarget{TransitionSpec: target}
			if resolutionValue != nil {
				batchTarget.Resolution = resolutionValue.output
			}
			for index, issue := range issues.Issues {
				item := issueBatchItem{
					IssueKey: issue.Key,
					Current:  issueBatchCurrent{Status: issue.Status},
					Target:   batchTarget,
				}
				transitions, transitionErr := client.ListTransitions(command.Context(), issue.Key)
				if transitionErr == nil {
					var transition jira.Transition
					transition, transitionErr = matchTransition(target, transitions)
					if transitionErr == nil {
						item.Target.Transition = &transition
						result.Ready++
						if dryRun {
							item.Outcome = "ready"
						} else {
							transitionErr = client.Transition(command.Context(), issue.Key, jira.TransitionInput{ID: transition.ID, Fields: resolvedFields})
							if transitionErr == nil {
								item.Outcome = "succeeded"
								result.Succeeded++
							}
						}
					}
				}
				if transitionErr != nil {
					item.Outcome = "failed"
					item.Error = transitionErr.Error()
					result.Failed++
					result.Items = append(result.Items, item)
					if isSystemicIssueError(transitionErr) {
						appendNotAttempted(&result, issues.Issues[index+1:], batchTarget, transitionErr.Error())
						break
					}
					continue
				}
				result.Items = append(result.Items, item)
			}
			return a.finishIssueBatch(result)
		},
	}
	flags := command.Flags()
	flags.StringVar(&jql, "jql", "", "JQL selecting every issue to process")
	flags.StringVar(&target, "to", "", "transition ID, name, or destination status")
	flags.StringVar(&resolution, "resolution", "", "resolution name or numeric ID; use none to clear")
	flags.StringArrayVar(&fields, "field", nil, "custom field as alias=value or customfield_N=value; repeatable")
	flags.BoolVar(&dryRun, "dry-run", false, "preflight transition availability without validating fields or changing Jira")
	flags.BoolVar(&yes, "yes", false, "confirm execution without prompting")
	return command
}

func (a *app) issueBulkAssignCommand() *cobra.Command {
	var jql, assignee string
	var dryRun, yes bool
	command := &cobra.Command{
		Use:   "assign",
		Short: "Assign every issue selected by JQL",
		Args:  exactArgs(0),
		RunE: func(command *cobra.Command, _ []string) error {
			if strings.TrimSpace(jql) == "" || strings.TrimSpace(assignee) == "" {
				return apperr.New(apperr.KindInvalidInput, "--jql and --assignee are required")
			}
			if dryRun == yes {
				return apperr.New(apperr.KindInvalidInput, "exactly one of --dry-run or --yes is required")
			}
			var client *jira.Client
			var err error
			if dryRun {
				client, _, err = a.client()
			} else {
				client, _, err = a.writableClient()
			}
			if err != nil {
				return err
			}
			issues, err := searchAll(command.Context(), client, jql, 0, 50, []string{"status", "assignee"})
			if err != nil {
				return err
			}
			result := issueBatchResult{
				Operation: "assign", DryRun: dryRun, JQL: jql, Total: len(issues.Issues),
				Items: make([]issueBatchItem, 0, len(issues.Issues)),
			}
			if len(issues.Issues) == 0 {
				return a.finishIssueBatch(result)
			}
			resolved, _, err := resolveAssignee(command.Context(), client, assignee)
			if err != nil {
				return err
			}
			for index, issue := range issues.Issues {
				item := issueBatchItem{
					IssueKey: issue.Key,
					Current:  issueBatchCurrent{Assignee: issue.Assignee},
					Target:   assigneeBatchTarget(resolved),
				}
				result.Ready++
				if dryRun {
					item.Outcome = "ready"
					result.Items = append(result.Items, item)
					continue
				}
				assignErr := client.AssignIssue(command.Context(), issue.Key, resolved)
				if assignErr == nil {
					item.Outcome = "succeeded"
					result.Succeeded++
					result.Items = append(result.Items, item)
					continue
				}
				item.Outcome = "failed"
				item.Error = assignErr.Error()
				result.Failed++
				result.Items = append(result.Items, item)
				if isSystemicIssueError(assignErr) {
					appendNotAttempted(&result, issues.Issues[index+1:], assigneeBatchTarget(resolved), assignErr.Error())
					break
				}
			}
			return a.finishIssueBatch(result)
		},
	}
	flags := command.Flags()
	flags.StringVar(&jql, "jql", "", "JQL selecting every issue to process")
	flags.StringVar(&assignee, "assignee", "", "assignee username; use me or none")
	flags.BoolVar(&dryRun, "dry-run", false, "preflight every matching issue without changing Jira")
	flags.BoolVar(&yes, "yes", false, "confirm execution without prompting")
	return command
}

func (a *app) issueBulkUpdateCommand() *cobra.Command {
	var jql, target string
	var dryRun, yes bool
	command := &cobra.Command{
		Use:   "update",
		Short: "Update every issue selected by JQL",
		Args:  exactArgs(0),
		RunE: func(command *cobra.Command, _ []string) error {
			if strings.TrimSpace(jql) == "" || strings.TrimSpace(target) == "" {
				return apperr.New(apperr.KindInvalidInput, "--jql and --type are required")
			}
			if dryRun == yes {
				return apperr.New(apperr.KindInvalidInput, "exactly one of --dry-run or --yes is required")
			}
			var client *jira.Client
			var err error
			if dryRun {
				client, _, err = a.client()
			} else {
				client, _, err = a.writableClient()
			}
			if err != nil {
				return err
			}
			issues, err := searchAll(command.Context(), client, jql, 0, 50, []string{"issuetype"})
			if err != nil {
				return err
			}
			batchTarget := issueBatchTarget{IssueTypeInput: target}
			result := issueBatchResult{
				Operation: "update", DryRun: dryRun, JQL: jql, Total: len(issues.Issues),
				Items: make([]issueBatchItem, 0, len(issues.Issues)),
			}
			for index, issue := range issues.Issues {
				item := issueBatchItem{
					IssueKey: issue.Key,
					Current:  issueBatchCurrent{IssueType: issue.IssueType},
					Target:   batchTarget,
				}
				resolved, unchanged, preflightErr := preflightIssueTypeUpdate(command.Context(), client, issue, target)
				if preflightErr != nil {
					item.Outcome = "failed"
					item.Error = preflightErr.Error()
					result.Failed++
					result.Items = append(result.Items, item)
					if isSystemicIssueError(preflightErr) {
						appendNotAttempted(&result, issues.Issues[index+1:], batchTarget, preflightErr.Error())
						break
					}
					continue
				}
				item.Target.IssueType = &resolved
				if unchanged {
					item.Outcome = "unchanged"
					result.Unchanged++
					result.Items = append(result.Items, item)
					continue
				}
				result.Ready++
				if dryRun {
					item.Outcome = "ready"
					result.Items = append(result.Items, item)
					continue
				}

				actual, unknown, updateErr := applyIssueTypeUpdate(command.Context(), client, issue.Key, resolved)
				if updateErr == nil {
					item.Outcome = "succeeded"
					result.Succeeded++
					result.Items = append(result.Items, item)
					continue
				}
				item.Error = updateErr.Error()
				item.ActualIssueType = actual
				if unknown {
					item.Outcome = "unknown"
					result.Unknown++
					result.Items = append(result.Items, item)
					appendNotAttempted(&result, issues.Issues[index+1:], batchTarget,
						"a prior Issue Type update was accepted but could not be read back: "+updateErr.Error())
					break
				}
				item.Outcome = "failed"
				result.Failed++
				result.Items = append(result.Items, item)
				if actual == nil && isSystemicIssueError(updateErr) {
					appendNotAttempted(&result, issues.Issues[index+1:], batchTarget, updateErr.Error())
					break
				}
			}
			return a.finishIssueBatch(result)
		},
	}
	flags := command.Flags()
	flags.StringVar(&jql, "jql", "", "JQL selecting every issue to process")
	flags.StringVarP(&target, "type", "t", "", "compatible issue type ID or unique name")
	flags.BoolVar(&dryRun, "dry-run", false, "preflight compatible Issue Types without changing Jira")
	flags.BoolVar(&yes, "yes", false, "confirm execution without prompting")
	return command
}

func assigneeBatchTarget(target jira.AssigneeTarget) issueBatchTarget {
	if target.Username == nil {
		return issueBatchTarget{Unassigned: true}
	}
	return issueBatchTarget{Assignee: target.Username}
}

func appendNotAttempted(result *issueBatchResult, issues []jira.Issue, target issueBatchTarget, reason string) {
	for _, issue := range issues {
		result.Items = append(result.Items, issueBatchItem{
			IssueKey: issue.Key, Outcome: "not_attempted",
			Current: issueBatchCurrent{Status: issue.Status, Assignee: issue.Assignee, IssueType: issue.IssueType}, Target: target,
			Error: "not attempted after systemic failure: " + reason,
		})
		result.NotAttempted++
	}
}

func (a *app) finishIssueBatch(result issueBatchResult) error {
	if result.Failed > 0 || result.Unknown > 0 || result.NotAttempted > 0 {
		if err := a.renderPartial(result, issueBatchTable(result)); err != nil {
			return err
		}
		if result.Unknown > 0 {
			return apperr.New(apperr.KindPartialFailure, fmt.Sprintf(
				"bulk %s completed with %d failed, %d unknown, and %d not attempted",
				result.Operation, result.Failed, result.Unknown, result.NotAttempted,
			))
		}
		return apperr.New(apperr.KindPartialFailure, fmt.Sprintf(
			"bulk %s completed with %d failed and %d not attempted",
			result.Operation, result.Failed, result.NotAttempted,
		))
	}
	return a.render(result, issueBatchTable(result))
}

func issueBatchTable(result issueBatchResult) output.Table {
	rows := make([][]string, 0, len(result.Items))
	for _, item := range result.Items {
		rows = append(rows, []string{item.IssueKey, item.Outcome, item.Error})
	}
	return output.Table{
		Columns: []output.Column{output.Fixed("ISSUE"), output.Flexible("OUTCOME"), output.Flexible("ERROR")},
		Rows:    rows,
	}
}

func isSystemicIssueError(err error) bool {
	appErr := apperr.As(err)
	switch appErr.Kind {
	case apperr.KindAuth, apperr.KindRateLimit, apperr.KindUnexpected, apperr.KindConfig:
		return true
	case apperr.KindAPI:
		return appErr.StatusCode == 0 || appErr.StatusCode >= http.StatusInternalServerError
	default:
		return false
	}
}
