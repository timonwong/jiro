package cmd

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/spf13/cobra"
	"github.com/timonwong/jiro/internal/config"
)

func (a *app) configureCompletions(root *cobra.Command) {
	root.CompletionOptions.SetDefaultShellCompDirective(cobra.ShellCompDirectiveNoFileComp)
	must(root.MarkPersistentFlagFilename("config"))
	mustRegisterFlagCompletion(root, "profile", func(_ *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		names, err := config.ProfileNames(a.configOptions())
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	})
	mustRegisterFlagCompletion(root, "output", fixedCompletions(
		cobra.CompletionWithDesc("text", "Human-readable output"),
		cobra.CompletionWithDesc("json", "Versioned machine-readable output"),
	))

	api := mustFindCommand(root, "api")
	descriptions := map[string]string{
		http.MethodGet: "Retrieve a resource", http.MethodHead: "Retrieve response headers", http.MethodOptions: "Retrieve endpoint options",
		http.MethodPost: "Create or invoke a resource", http.MethodPut: "Replace a resource", http.MethodPatch: "Update a resource",
		http.MethodDelete: "Delete a resource",
	}
	methodCompletions := make([]cobra.Completion, 0, len(apiMethodSuggestions))
	for _, method := range apiMethodSuggestions {
		methodCompletions = append(methodCompletions, cobra.CompletionWithDesc(method, descriptions[method]))
	}
	mustRegisterFlagCompletion(api, "method", fixedCompletions(methodCompletions...))
	mustRegisterFlagCompletion(api, "input", fileOrStdinCompletions)
	for _, path := range [][]string{{"jfm", "to-jira"}, {"jfm", "from-jira"}} {
		mustFindCommand(root, path...).ValidArgsFunction = fileOrStdinCompletions
	}

	issuesList := mustFindCommand(root, "issue", "list")
	mustRegisterFlagCompletion(issuesList, "assignee", fixedCompletions(
		cobra.CompletionWithDesc("me", "Use Jira currentUser()"),
	))
	mustRegisterFlagCompletion(issuesList, "reporter", fixedCompletions(
		cobra.CompletionWithDesc("me", "Use Jira currentUser()"),
	))
	mustRegisterFlagCompletion(issuesList, "resolution", fixedCompletions(
		cobra.CompletionWithDesc("unresolved", "Issues without a resolution"),
	))
	mustRegisterFlagCompletion(issuesList, "sprint", fixedCompletions(
		cobra.CompletionWithDesc("active", "Open sprints"),
		cobra.CompletionWithDesc("open", "Open sprints"),
		cobra.CompletionWithDesc("closed", "Closed sprints"),
		cobra.CompletionWithDesc("future", "Future sprints"),
	))

	mustRegisterFlagCompletion(mustFindCommand(root, "sprint", "list"), "state", fixedCompletions(
		cobra.CompletionWithDesc("active", "Active Sprints"),
		cobra.CompletionWithDesc("closed", "Closed Sprints"),
		cobra.CompletionWithDesc("future", "Future Sprints"),
		cobra.CompletionWithDesc("all", "All Sprints"),
	))

	for _, path := range [][]string{{"issue", "add"}, {"issue", "update"}} {
		command := mustFindCommand(root, path...)
		mustRegisterFlagCompletion(command, "input-format", inputFormatCompletions())
		mustRegisterFlagCompletion(command, "assignee", fixedCompletions(
			cobra.CompletionWithDesc("none", "Clear the assignee"),
		))
		mustRegisterFlagCompletion(command, "description-file", fileOrStdinCompletions)
	}

	issueCreate := mustFindCommand(root, "issue", "add")
	mustRegisterFlagCompletion(issueCreate, "sprint", fixedCompletions(
		cobra.CompletionWithDesc("active", "Open sprint"),
	))
	issueUpdate := mustFindCommand(root, "issue", "update")
	mustRegisterFlagCompletion(issueUpdate, "sprint", fixedCompletions(
		cobra.CompletionWithDesc("active", "Open sprint"),
	))
	for _, completion := range []struct {
		field       string
		description string
	}{
		{"component", "Clear all components"},
		{"fix-version", "Clear all fix versions"},
	} {
		mustRegisterFlagCompletion(issueUpdate, completion.field, fixedCompletions(
			cobra.CompletionWithDesc("none", completion.description),
		))
	}

	assigneeCompletions := fixedCompletions(
		cobra.CompletionWithDesc("me", "Assign to the current Jira user"),
		cobra.CompletionWithDesc("none", "Clear the assignee"),
	)
	mustRegisterFlagCompletion(mustFindCommand(root, "issue", "assign"), "assignee", assigneeCompletions)
	mustRegisterFlagCompletion(mustFindCommand(root, "issue", "bulk", "assign"), "assignee", assigneeCompletions)

	for _, path := range [][]string{{"issue", "comment", "add"}, {"issue", "comment", "edit"}} {
		issueComment := mustFindCommand(root, path...)
		mustRegisterFlagCompletion(issueComment, "input-format", inputFormatCompletions())
		mustRegisterFlagCompletion(issueComment, "body-file", fileOrStdinCompletions)
	}
}

func inputFormatCompletions() cobra.CompletionFunc {
	return fixedCompletions(
		cobra.CompletionWithDesc("jira", "Jira Markup"),
		cobra.CompletionWithDesc("jfm", "Jiro Flavored Markdown"),
		cobra.CompletionWithDesc("markdown", "Alias for Jiro Flavored Markdown"),
	)
}

func fixedCompletions(values ...cobra.Completion) cobra.CompletionFunc {
	return cobra.FixedCompletions(values, cobra.ShellCompDirectiveNoFileComp)
}

func fileOrStdinCompletions(_ *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return []cobra.Completion{cobra.CompletionWithDesc("-", "Read from stdin")}, cobra.ShellCompDirectiveDefault
}

func mustFindCommand(root *cobra.Command, path ...string) *cobra.Command {
	command, remaining, err := root.Find(path)
	if err != nil || len(remaining) != 0 || command == root {
		panic(fmt.Sprintf("completion command path %q is invalid", strings.Join(path, " ")))
	}
	return command
}

func mustRegisterFlagCompletion(command *cobra.Command, name string, completion cobra.CompletionFunc) {
	must(command.RegisterFlagCompletionFunc(name, completion))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
