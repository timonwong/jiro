package cmd

import (
	"github.com/timonwong/jiro/internal/jira"
	"github.com/timonwong/jiro/internal/output"
)

func (a *app) normalizeSearchSprintMemberships(result *jira.SearchResult, fieldID string) {
	if fieldID == "" {
		return
	}
	for index := range result.Issues {
		a.normalizeIssueSprintMemberships(&result.Issues[index], fieldID)
	}
}

func (a *app) normalizeIssueSprintMemberships(issue *jira.Issue, fieldID string) {
	if fieldID == "" {
		return
	}
	memberships, failures := jira.NormalizeSprintMemberships(issue.Fields[fieldID])
	issue.Sprints = &memberships
	if len(failures) == 0 {
		return
	}
	a.addWarning(output.Warning{
		Code:    "sprint_membership_normalization",
		Message: "some Sprint memberships could not be fully normalized",
		Details: map[string]any{
			"issueKey": issue.Key,
			"fieldId":  fieldID,
			"failures": failures,
		},
	})
}
