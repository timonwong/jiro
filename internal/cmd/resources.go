package cmd

import (
	"github.com/spf13/cobra"
	"github.com/timonwong/jiro/internal/jira"
	"github.com/timonwong/jiro/internal/output"
)

func (a *app) searchCommand() *cobra.Command {
	var limit, offset int
	var all bool
	var fields []string
	command := &cobra.Command{
		Use:   "search JQL",
		Short: "Search issues with JQL",
		Args:  exactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			if err := validatePagination(offset, limit); err != nil {
				return err
			}
			client, settings, err := a.client()
			if err != nil {
				return err
			}
			fieldResolution, err := a.resolveReadFieldSelectors(command.Context(), client, settings, fields)
			if err != nil {
				return err
			}
			var result jira.SearchResult
			if all {
				result, err = searchAll(command.Context(), client, args[0], offset, limit, fieldResolution.resolved)
			} else {
				result, err = client.Search(command.Context(), jira.IssueListOptions{
					JQL: args[0], Page: jira.Page{StartAt: offset, MaxResults: limit}, Fields: fieldResolution.resolved,
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
	command.Flags().IntVarP(&limit, "limit", "n", 50, "maximum issues per page")
	command.Flags().IntVar(&offset, "offset", 0, "zero-based result offset")
	command.Flags().BoolVar(&all, "all", false, "fetch all result pages")
	command.Flags().StringSliceVar(&fields, "fields", nil, "comma-separated Field Selectors to resolve and request")
	return command
}

type searchOutput struct {
	jira.SearchResult
	FieldSelection []jira.FieldSelection `json:"fieldSelection,omitempty"`
}

func (a *app) projectCommand() *cobra.Command {
	command := &cobra.Command{Use: "project", Short: "Work with Jira projects"}
	command.AddCommand(a.projectsListCommand(), a.projectShowCommand())
	return command
}

func (a *app) projectsListCommand() *cobra.Command {
	var limit, offset int
	command := &cobra.Command{
		Use:   "list",
		Short: "List projects",
		Args:  exactArgs(0),
		RunE: func(command *cobra.Command, _ []string) error {
			if err := validatePagination(offset, limit); err != nil {
				return err
			}
			client, _, err := a.client()
			if err != nil {
				return err
			}
			projects, err := client.ListProjects(command.Context(), jira.Page{StartAt: offset, MaxResults: limit})
			if err != nil {
				return err
			}
			rows := make([][]string, 0, len(projects))
			for _, project := range projects {
				rows = append(rows, []string{project.Key, project.Name, project.ProjectType})
			}
			return a.render(map[string]any{"projects": projects}, output.Table{
				Columns: []output.Column{output.Fixed("KEY"), output.Flexible("NAME"), output.Flexible("TYPE")}, Rows: rows,
			})
		},
	}
	command.Flags().IntVarP(&limit, "limit", "n", 50, "maximum projects")
	command.Flags().IntVar(&offset, "offset", 0, "zero-based result offset")
	return command
}

func (a *app) projectShowCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "show PROJECT-KEY",
		Short: "Show a project",
		Args:  exactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			project, err := client.ShowProject(command.Context(), args[0])
			if err != nil {
				return err
			}
			lead := ""
			if project.Lead != nil {
				lead = project.Lead.DisplayName
			}
			return a.render(project, output.Table{
				Columns: []output.Column{output.Fixed("FIELD"), output.Flexible("VALUE")},
				Rows: [][]string{
					{"Key", project.Key}, {"Name", project.Name}, {"Type", project.ProjectType},
					{"Lead", lead}, {"Description", project.Description},
				},
			})
		},
	}
}

func (a *app) fieldCommand() *cobra.Command {
	command := &cobra.Command{Use: "field", Short: "Inspect Jira fields"}
	command.AddCommand(a.fieldsListCommand())
	return command
}

func (a *app) fieldsListCommand() *cobra.Command {
	var customOnly bool
	command := &cobra.Command{
		Use:   "list",
		Short: "List Jira fields",
		Args:  exactArgs(0),
		RunE: func(command *cobra.Command, _ []string) error {
			client, settings, err := a.client()
			if err != nil {
				return err
			}
			var fields []jira.Field
			if customOnly {
				metadata, err := a.loadFieldMetadata(command.Context(), client, settings)
				if err != nil {
					return err
				}
				fields = customFieldsOnly(metadata.fields)
			} else {
				fields, err = client.ListFields(command.Context())
				if err != nil {
					return err
				}
			}
			type fieldView struct {
				ID     string `json:"id"`
				Name   string `json:"name"`
				Alias  string `json:"alias,omitempty"`
				Custom bool   `json:"custom"`
				Type   string `json:"type,omitempty"`
			}
			views := make([]fieldView, 0, len(fields))
			rows := make([][]string, 0, len(fields))
			for _, field := range fields {
				alias := ""
				if field.Custom {
					alias = jira.Slug(field.Name)
				}
				views = append(views, fieldView{ID: field.ID, Name: field.Name, Alias: alias, Custom: field.Custom, Type: field.Type})
				rows = append(rows, []string{field.ID, field.Name, alias, field.Type})
			}
			return a.render(map[string]any{"fields": views}, output.Table{
				Columns: []output.Column{output.Fixed("ID"), output.Flexible("NAME"), output.Fixed("ALIAS"), output.Flexible("TYPE")}, Rows: rows,
			})
		},
	}
	command.Flags().BoolVar(&customOnly, "custom", false, "show custom fields only")
	return command
}
