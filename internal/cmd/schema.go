package cmd

import (
	"github.com/spf13/cobra"
	"github.com/timonwong/jiro/internal/output"
)

type cliSchema struct {
	ContractVersion string            `json:"contractVersion"`
	Program         string            `json:"program"`
	Platform        string            `json:"platform"`
	GlobalFlags     []flagSchema      `json:"globalFlags"`
	Commands        []commandSchema   `json:"commands"`
	Types           map[string]any    `json:"types"`
	Output          outputSchema      `json:"output"`
	ExitCodes       map[string]string `json:"exitCodes"`
}

type commandSchema struct {
	Name                 string         `json:"name"`
	Aliases              []string       `json:"aliases,omitempty"`
	Auth                 bool           `json:"auth"`
	Mutating             bool           `json:"mutating"`
	MutationMode         string         `json:"mutationMode,omitempty"`
	ReadOnlyMethods      []string       `json:"readOnlyMethods,omitempty"`
	OutputMode           string         `json:"outputMode"`
	SupportsGlobalOutput bool           `json:"supportsGlobalOutput"`
	Args                 string         `json:"args,omitempty"`
	Flags                []flagSchema   `json:"flags,omitempty"`
	JSONData             map[string]any `json:"jsonData"`
	Warnings             []string       `json:"warnings,omitempty"`
}

type flagSchema struct {
	Name          string   `json:"name"`
	Short         string   `json:"short,omitempty"`
	Type          string   `json:"type"`
	Required      bool     `json:"required,omitempty"`
	Repeatable    bool     `json:"repeatable,omitempty"`
	Default       any      `json:"default,omitempty"`
	ConflictsWith []string `json:"conflictsWith,omitempty"`
}

type outputSchema struct {
	Default        string `json:"default"`
	JSONEnvelope   string `json:"jsonEnvelope"`
	ErrorEnvelope  string `json:"errorEnvelope"`
	SuccessStream  string `json:"successStream"`
	ErrorStream    string `json:"errorStream"`
	PartialFailure string `json:"partialFailure"`
	SchemaVersion  string `json:"schemaVersion"`
	Warnings       string `json:"warnings"`
}

func (a *app) schemaCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "schema",
		Short: "Describe jiro's machine-readable CLI contract",
		Args:  exactArgs(0),
		RunE: func(_ *cobra.Command, _ []string) error {
			renderer := output.New(a.stdout, a.stderr, output.FormatJSON, false)
			return renderer.Success(schemaDocument())
		},
	}
}

func schemaDocument() cliSchema {
	return cliSchema{
		ContractVersion: "3",
		Program:         "jiro",
		Platform:        "Jira Data Center/Server REST API v2",
		GlobalFlags: []flagSchema{
			flag("config", "c", "path"), flag("profile", "", "string"),
			flagDefault("output", "o", "enum:text|json", "text"),
			flag("quiet", "", "boolean"),
		},
		Commands: []commandSchema{
			commandWithFlags("board list", nil, false, "", nil, map[string]any{
				"total": "number of Boards", "boards": []any{"Board"},
			}),
			commandWithFlags("cache refresh", nil, true, "", nil, object("refreshed", "path", "fieldCount", "fetchedAt", "expiresAt", "instance", "principal")),
			normalizedCommand(commandSchema{Name: "jfm to-jira", Auth: false, Mutating: false, Args: "[FILE|-]", JSONData: object("jiraMarkup")}),
			normalizedCommand(commandSchema{Name: "jfm from-jira", Auth: false, Mutating: false, Args: "[FILE|-]", JSONData: object("jfm")}),
			normalizedCommand(commandSchema{Name: "auth login", Auth: false, Mutating: true, Flags: []flagSchema{
				flagDefault("use-keyring", "", "boolean", true), flag("password-stdin", "", "boolean"), flag("token-stdin", "", "boolean"),
			}, JSONData: object("profile", "host", "authType", "credentialStore", "user")}),
			normalizedCommand(commandSchema{Name: "auth logout", Auth: false, Mutating: true, JSONData: object("profile", "credentialStore", "credentialRemoved", "environmentCredentialActive")}),
			normalizedCommand(commandSchema{Name: "auth status", Auth: true, Mutating: false, JSONData: object("profile", "instance", "authType", "user")}),
			{
				Name: "api", Auth: true, Mutating: true, MutationMode: "http_method",
				ReadOnlyMethods: append([]string(nil), apiReadOnlyMethodNames...), OutputMode: "raw_http", SupportsGlobalOutput: false,
				Args: "ENDPOINT", Flags: []flagSchema{
					flagDefault("method", "X", "string", "GET"),
					flag("input", "", "path-or-stdin"), repeatableFlag("string-field", "f", "key=value"), repeatableFlag("field", "F", "key=value"),
					repeatableFlag("form", "", "key=value"), repeatableFlag("header", "H", "header"),
					flagDefault("timeout", "", "duration", "30s"), flag("insecure", "", "boolean"), flag("include", "i", "boolean"),
				}, JSONData: nil,
			},
			commandWithFlags("issue list", nil, false, "", []flagSchema{
				flag("project", "p", "string"), flag("status", "", "string"), flag("assignee", "", "string"),
				flag("type", "t", "string"), flag("resolution", "", "string"), flag("reporter", "", "string"),
				repeatableFlag("label", "", "string"), repeatableFlag("component", "", "string"), repeatableFlag("fix-version", "", "string"),
				flag("sprint", "", "string"), flag("parent", "", "issue-key"), flag("created", "", "jira-date"), flag("updated", "", "jira-date"),
				flag("jql", "q", "string"), flagDefault("limit", "n", "integer", 50),
				flagDefault("offset", "", "integer", 0), flag("all", "", "boolean"), flag("fields", "", "field-selector-list"),
			}, issueCollectionWithFieldSelection("startAt", "maxResults", "total")),
			commandWithFlags("issue show", nil, false, "ISSUE-KEY", []flagSchema{flag("fields", "", "field-selector-list")}, issueWithFieldSelection()),
			commandWithFlags("issue add", nil, true, "", []flagSchema{
				requiredFlag("project", "p", "string"), requiredFlag("type", "t", "string"), requiredFlag("summary", "s", "string"),
				flag("description", "", "string"), flag("description-file", "", "path-or-stdin"), flagDefault("input-format", "", "enum:jira|jfm|markdown", "jira"),
				flag("priority", "", "string"), flag("assignee", "", "string"), repeatableFlag("label", "", "string"), flag("parent", "", "issue-key"),
				repeatableFlag("component", "", "string"), repeatableFlag("fix-version", "", "string"), flag("sprint", "", "string"),
				flag("sprint-board", "", "string"), repeatableFlag("field", "", "custom-field-alias-or-id=value"),
			}, object("id", "key", "sprint", "sprintMoved")),
			normalizedCommand(commandSchema{Name: "issue clone", Auth: true, Mutating: true, Args: "SOURCE", Flags: []flagSchema{
				flag("summary", "s", "string"), flag("description", "", "string"), flag("description-file", "", "path-or-stdin"),
				flagDefault("input-format", "", "enum:jira|jfm|markdown", "jira"), flag("priority", "", "string"), flag("assignee", "", "string"),
				repeatableFlag("label", "", "string"), repeatableFlag("component", "", "string"), repeatableFlag("fix-version", "", "string"),
				repeatableFlag("field", "", "custom-field-alias-or-id=value"), flag("no-link", "", "boolean"),
			}, JSONData: object("sourceIssueKey", "id", "key", "linked", "sprintMoved", "link", "sprint"), Warnings: []string{"issue_clone_custom_field_skipped", "sprint_membership_normalization"}}),
			commandWithFlags("issue update", nil, true, "ISSUE-KEY", []flagSchema{
				flag("summary", "s", "string"), conflictingFlag("type", "t", "string", issueTypeUpdateConflictingFlags...), flag("description", "", "string"), flag("description-file", "", "path-or-stdin"),
				flagDefault("input-format", "", "enum:jira|jfm|markdown", "jira"), flag("priority", "", "string"), flag("assignee", "", "string"),
				repeatableFlag("label", "", "string"), repeatableFlag("component", "", "string"), repeatableFlag("fix-version", "", "string"), flag("sprint", "", "string"),
				flag("sprint-board", "", "string"), repeatableFlag("field", "", "custom-field-alias-or-id=value"),
			}, object("key", "updated", "unchanged", "unknown", "issueType", "sprint", "sprintMoved")),
			command("issue list-types", false, "ISSUE-KEY", object("issueKey", "currentIssueType", "issueTypes")),
			commandWithFlags("issue comment list", nil, false, "ISSUE-KEY", []flagSchema{
				flagDefault("limit", "n", "integer", 50), flagDefault("offset", "", "integer", 0),
			}, object("issueKey", "comments")),
			commandWithFlags("issue comment add", nil, true, "ISSUE-KEY", []flagSchema{
				flag("body", "", "string"), flag("body-file", "", "path-or-stdin"), flagDefault("input-format", "", "enum:jira|jfm|markdown", "jira"),
			}, object("id", "body", "author", "created")),
			commandWithFlags("issue comment edit", nil, true, "ISSUE-KEY COMMENT-ID", []flagSchema{
				flag("body", "", "string"), flag("body-file", "", "path-or-stdin"), flagDefault("input-format", "", "enum:jira|jfm|markdown", "jira"),
			}, object("id", "body", "author", "created", "updated")),
			command("issue list-transitions", false, "ISSUE-KEY", object("issueKey", "transitions")),
			commandWithFlags("issue move", nil, true, "ISSUE-KEY", []flagSchema{
				requiredFlag("to", "", "string"), flag("comment", "", "string"),
				flagDefault("input-format", "", "enum:jira|jfm|markdown", "jira"), flag("resolution", "", "string"),
				repeatableFlag("field", "", "custom-field-alias-or-id=value"),
			}, object("key", "transition", "transitioned", "commented", "resolution")),
			commandWithFlags("issue assign", nil, true, "ISSUE-KEY", []flagSchema{
				requiredFlag("assignee", "", "string"),
			}, object("key", "assignee", "assigned")),
			command("issue link list", false, "ISSUE-KEY", object("issueKey", "links")),
			commandWithFlags("issue link add", nil, true, "FROM", []flagSchema{
				requiredFlag("to", "", "issue-key"), requiredFlag("type", "", "string"),
			}, object("from", "to", "type", "linked")),
			command("issue link delete", true, "LINK-ID", object("linkId", "unlinked")),
			command("issue link types", false, "", object("linkTypes")),
			commandWithFlags("issue bulk move", nil, true, "", []flagSchema{
				requiredFlag("jql", "", "string"), requiredFlag("to", "", "string"), flag("resolution", "", "string"),
				repeatableFlag("field", "", "custom-field-alias-or-id=value"), flag("dry-run", "", "boolean"), flag("yes", "", "boolean"),
			}, batchObject()),
			commandWithFlags("issue bulk assign", nil, true, "", []flagSchema{
				requiredFlag("jql", "", "string"), requiredFlag("assignee", "", "string"), flag("dry-run", "", "boolean"), flag("yes", "", "boolean"),
			}, batchObject()),
			commandWithFlags("issue bulk update", nil, true, "", []flagSchema{
				requiredFlag("jql", "", "string"), requiredFlag("type", "t", "string"), flag("dry-run", "", "boolean"), flag("yes", "", "boolean"),
			}, batchObject()),
			commandWithFlags("search", nil, false, "JQL", []flagSchema{
				flagDefault("limit", "n", "integer", 50), flagDefault("offset", "", "integer", 0), flag("all", "", "boolean"), flag("fields", "", "field-selector-list"),
			}, issueCollectionWithFieldSelection("startAt", "maxResults", "total")),
			commandWithFlags("project list", nil, false, "", []flagSchema{
				flagDefault("limit", "n", "integer", 50), flagDefault("offset", "", "integer", 0),
			}, object("projects")),
			commandWithFlags("project show", nil, false, "PROJECT-KEY", nil, object("id", "key", "name", "description", "projectType", "lead")),
			commandWithFlags("field list", nil, false, "", []flagSchema{flag("custom", "", "boolean")}, object("fields")),
			commandWithFlags("sprint list", nil, false, "", []flagSchema{
				flag("board", "", "string"), flagDefault("state", "", "enum:active|closed|future|all", "active"),
			}, map[string]any{
				"total": "number of Sprint relationships", "sprints": []any{"Sprint"}, "failedBoards": []any{"FailedBoard"},
			}),
			normalizedCommand(commandSchema{Name: "schema", Auth: false, Mutating: false, JSONData: object("contractVersion", "program", "platform", "globalFlags", "commands", "types", "output", "exitCodes")}),
		},
		Types: typeDefinitions(),
		Output: outputSchema{
			Default: "text", JSONEnvelope: `{"schemaVersion":"1","data":...,"warnings":[...]?}`,
			ErrorEnvelope:  `{"schemaVersion":"1","error":{"kind":...,"message":...}}`,
			SuccessStream:  "stdout",
			ErrorStream:    "stderr",
			PartialFailure: "on partial_failure, complete normalized result data is written to stdout before the structured error is written to stderr; exit code 7",
			SchemaVersion:  output.SchemaVersion, Warnings: "non-fatal success conditions; JSON envelope or text stderr",
		},
		ExitCodes: map[string]string{
			"0": "success", "1": "unexpected error", "2": "invalid input or config", "3": "authentication failed",
			"4": "resource not found", "5": "Jira API error", "6": "rate limited", "7": "partial failure",
		},
	}
}

func command(name string, mutating bool, args string, data map[string]any) commandSchema {
	return commandWithFlags(name, nil, mutating, args, nil, data)
}

func commandWithFlags(name string, aliases []string, mutating bool, args string, flags []flagSchema, data map[string]any) commandSchema {
	return normalizedCommand(commandSchema{Name: name, Aliases: aliases, Auth: true, Mutating: mutating, Args: args, Flags: flags, JSONData: data})
}

func normalizedCommand(command commandSchema) commandSchema {
	command.OutputMode = "normalized"
	command.SupportsGlobalOutput = true
	return command
}

func object(fields ...string) map[string]any {
	result := make(map[string]any, len(fields))
	for _, field := range fields {
		result[field] = "see command schema"
	}
	return result
}

func objectWithFieldSelection(fields ...string) map[string]any {
	result := object(fields...)
	result["fieldSelection"] = []any{"FieldSelection"}
	return result
}

func issueCollectionWithFieldSelection(fields ...string) map[string]any {
	result := objectWithFieldSelection(fields...)
	result["issues"] = []any{"Issue"}
	return result
}

func issueWithFieldSelection() map[string]any {
	result := issueTypeDefinition()
	result["fieldSelection"] = []any{"FieldSelection"}
	return result
}

func batchObject() map[string]any {
	return batchResultDefinition()
}

func typeDefinitions() map[string]any {
	return map[string]any{
		"Board":            object("id", "name", "type"),
		"Issue":            issueTypeDefinition(),
		"IssueType":        object("id", "name", "description", "subtask"),
		"Version":          object("id", "name", "archived", "released"),
		"Sprint":           object("id", "name", "state", "boardId", "boardName", "originBoardId", "goal", "startDate", "endDate", "completeDate"),
		"SprintMembership": object("id", "name", "state", "boardId", "goal", "startDate", "endDate", "completeDate"),
		"FieldSelector": map[string]any{
			"forms":      []any{"Jira field ID", "customfield_N", "Jira display name", "*all", "*navigable", "-FieldSelector"},
			"resolution": "exact Jira field ID, then locally normalized display name",
		},
		"FieldSelection": map[string]any{
			"selector": "original Field Selector spelling", "resolved": "exact selector sent to Jira",
		},
		"FailedBoard":   object("boardId", "boardName", "error"),
		"IssueLinkType": object("id", "name", "inward", "outward"),
		"IssueLink": map[string]any{
			"id": "Jira Link ID", "direction": "inward|outward", "relationship": "direction-relative description",
			"type": "IssueLinkType", "otherIssue": object("id", "key", "summary"),
		},
		"BatchCurrent": object("status", "assignee", "issueType"),
		"BatchTarget":  object("transitionSpec", "transition", "resolution", "assignee", "unassigned", "issueTypeInput", "issueType"),
		"BatchItem":    batchItemDefinition(),
		"BatchResult":  batchResultDefinition(),
	}
}

func issueTypeDefinition() map[string]any {
	result := object(
		"id", "key", "summary", "description", "project", "issueType", "status", "priority", "assignee", "reporter",
		"parent", "labels", "components", "fixVersions", "created", "updated", "fields",
	)
	result["sprints"] = []any{"SprintMembership"}
	return result
}

func batchResultDefinition() map[string]any {
	result := object("operation", "dryRun", "jql", "total", "ready", "succeeded", "failed", "unchanged", "unknown", "notAttempted")
	result["items"] = []any{batchItemDefinition()}
	return result
}

func batchItemDefinition() map[string]any {
	return map[string]any{
		"issueKey":        "Issue Key",
		"outcome":         "ready|succeeded|failed|unchanged|unknown|not_attempted",
		"current":         "BatchCurrent",
		"target":          "BatchTarget",
		"actualIssueType": "present when Issue Type readback differs from the target",
		"error":           "present for failed, unknown, and not_attempted outcomes",
	}
}

func flag(name, short, kind string) flagSchema {
	return flagSchema{Name: name, Short: short, Type: kind}
}

func requiredFlag(name, short, kind string) flagSchema {
	value := flag(name, short, kind)
	value.Required = true
	return value
}

func conflictingFlag(name, short, kind string, conflicts ...string) flagSchema {
	value := flag(name, short, kind)
	value.ConflictsWith = append([]string(nil), conflicts...)
	return value
}

func repeatableFlag(name, short, kind string) flagSchema {
	value := flag(name, short, kind)
	value.Repeatable = true
	return value
}

func flagDefault(name, short, kind string, defaultValue any) flagSchema {
	value := flag(name, short, kind)
	value.Default = defaultValue
	return value
}
