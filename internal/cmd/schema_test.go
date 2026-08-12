package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSchemaDocument(t *testing.T) {
	document := schemaDocument()
	if document.ContractVersion != "3" || len(document.Commands) == 0 {
		t.Fatalf("schema = %+v", document)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"name":"raw"`) {
		t.Fatalf("removed --raw flag remains in schema: %s", encoded)
	}
	globalFlags := make([]string, 0, len(document.GlobalFlags))
	for _, flag := range document.GlobalFlags {
		globalFlags = append(globalFlags, flag.Name)
	}
	if strings.Join(globalFlags, ",") != "config,profile,output,quiet" {
		t.Fatalf("global flags = %v", globalFlags)
	}
	if document.Output.SchemaVersion != "1" ||
		!strings.Contains(document.Output.PartialFailure, "stdout") ||
		!strings.Contains(document.Output.PartialFailure, "stderr") ||
		document.ExitCodes["7"] != "partial failure" {
		t.Fatalf("output contract = %+v exitCodes=%+v", document.Output, document.ExitCodes)
	}
	for _, name := range []string{"Board", "Issue", "IssueType", "Version", "Sprint", "SprintMembership", "FieldSelector", "FieldSelection", "IssueLink", "IssueLinkType", "BatchResult", "BatchItem"} {
		if document.Types[name] == nil {
			t.Fatalf("schema type %s is missing: %#v", name, document.Types)
		}
	}
	type commandMetadata struct {
		auth, mutating       bool
		args                 string
		outputMode           string
		supportsGlobalOutput bool
		mutationMode         string
		readOnlyMethods      []string
		jsonData             map[string]any
	}
	commands := make(map[string]commandMetadata)
	flags := make(map[string]map[string]struct {
		repeatable   bool
		required     bool
		kind         string
		defaultValue any
		conflicts    []string
	})
	for _, command := range document.Commands {
		if len(command.Aliases) != 0 {
			t.Fatalf("%s still exposes aliases %v", command.Name, command.Aliases)
		}
		commands[command.Name] = commandMetadata{
			auth: command.Auth, mutating: command.Mutating, args: command.Args, outputMode: command.OutputMode,
			supportsGlobalOutput: command.SupportsGlobalOutput, mutationMode: command.MutationMode,
			readOnlyMethods: command.ReadOnlyMethods, jsonData: command.JSONData,
		}
		flags[command.Name] = make(map[string]struct {
			repeatable   bool
			required     bool
			kind         string
			defaultValue any
			conflicts    []string
		}, len(command.Flags))
		for _, flag := range command.Flags {
			flags[command.Name][flag.Name] = struct {
				repeatable   bool
				required     bool
				kind         string
				defaultValue any
				conflicts    []string
			}{
				repeatable:   flag.Repeatable,
				required:     flag.Required,
				kind:         flag.Type,
				defaultValue: flag.Default,
				conflicts:    flag.ConflictsWith,
			}
		}
		if strings.HasPrefix(command.Name, "config") {
			t.Fatalf("removed config command remains in schema: %q", command.Name)
		}
		if command.Name != "api" && (command.OutputMode != "normalized" || !command.SupportsGlobalOutput) {
			t.Fatalf("%s output contract = mode %q supportsGlobalOutput=%t", command.Name, command.OutputMode, command.SupportsGlobalOutput)
		}
		if command.Name == "auth status" {
			for _, field := range []string{"profile", "instance", "authType", "user"} {
				if command.JSONData[field] == nil {
					t.Fatalf("auth status JSON schema is missing %q: %#v", field, command.JSONData)
				}
			}
			for _, field := range []string{"authenticated", "credentialStore"} {
				if command.JSONData[field] != nil {
					t.Fatalf("auth status JSON schema unexpectedly includes %q: %#v", field, command.JSONData)
				}
			}
		}
		if strings.HasPrefix(command.Name, "issue bulk ") {
			items, ok := command.JSONData["items"].([]any)
			if !ok || len(items) != 1 {
				t.Fatalf("%s items schema = %#v", command.Name, command.JSONData["items"])
			}
			item, ok := items[0].(map[string]any)
			if !ok || item["current"] == nil || item["target"] == nil || item["outcome"] == nil {
				t.Fatalf("%s item schema = %#v", command.Name, items[0])
			}
		}
	}
	api, ok := commands["api"]
	if !ok || !api.auth || !api.mutating || api.outputMode != "raw_http" || api.supportsGlobalOutput ||
		api.mutationMode != "http_method" || strings.Join(api.readOnlyMethods, ",") != "GET,HEAD,OPTIONS" || api.jsonData != nil {
		t.Fatalf("api schema = %+v, present=%t", api, ok)
	}
	for _, command := range []struct {
		name, dataField string
	}{
		{"jfm to-jira", "jiraMarkup"},
		{"jfm from-jira", "jfm"},
	} {
		metadata, ok := commands[command.name]
		if !ok || metadata.auth || metadata.mutating || metadata.args != "[FILE|-]" ||
			metadata.outputMode != "normalized" || !metadata.supportsGlobalOutput || metadata.jsonData[command.dataField] == nil {
			t.Fatalf("%s schema = %+v, present=%t", command.name, metadata, ok)
		}
		if len(flags[command.name]) != 0 {
			t.Fatalf("%s unexpectedly has command flags: %#v", command.name, flags[command.name])
		}
	}
	for _, name := range []string{"method", "input", "string-field", "field", "form", "header", "timeout", "insecure", "include"} {
		if _, ok := flags["api"][name]; !ok {
			t.Fatalf("api is missing --%s", name)
		}
	}
	if flags["api"]["method"].kind != "string" {
		t.Fatalf("api --method type = %q, want string", flags["api"]["method"].kind)
	}
	for _, name := range []string{"use-keyring", "password-stdin", "token-stdin"} {
		if _, ok := flags["auth login"][name]; !ok {
			t.Fatalf("auth login is missing --%s", name)
		}
	}
	for _, name := range []string{"auth login", "auth logout"} {
		metadata, ok := commands[name]
		if !ok || metadata.auth || !metadata.mutating {
			t.Fatalf("%s schema = %+v, present=%t", name, metadata, ok)
		}
	}
	if metadata, ok := commands["auth status"]; !ok || !metadata.auth || metadata.mutating {
		t.Fatalf("auth status schema = %+v, present=%t", metadata, ok)
	}
	for _, name := range []string{"login", "logout", "myself"} {
		if _, ok := commands[name]; ok {
			t.Fatalf("removed top-level command remains in schema: %q", name)
		}
	}
	if metadata, ok := commands["cache refresh"]; !ok || !metadata.auth || !metadata.mutating {
		t.Fatalf("cache refresh schema = %+v, present=%t", metadata, ok)
	}
	for _, command := range []struct {
		name     string
		mutating bool
		flags    []string
	}{
		{"issue list", false, []string{"resolution", "reporter", "label", "component", "fix-version", "sprint", "parent", "created", "updated"}},
		{"issue add", true, []string{"parent", "component", "fix-version", "sprint", "sprint-board"}},
		{"issue update", true, []string{"type", "component", "fix-version", "sprint", "sprint-board"}},
		{"issue list-types", false, nil},
		{"issue move", true, []string{"to", "comment", "input-format", "resolution", "field"}},
		{"issue assign", true, []string{"assignee"}},
		{"issue link list", false, nil},
		{"issue link add", true, []string{"to", "type"}},
		{"issue link delete", true, nil},
		{"issue link types", false, nil},
		{"issue bulk move", true, []string{"jql", "to", "resolution", "field", "dry-run", "yes"}},
		{"issue bulk assign", true, []string{"jql", "assignee", "dry-run", "yes"}},
		{"issue bulk update", true, []string{"jql", "type", "dry-run", "yes"}},
		{"board list", false, nil},
		{"sprint list", false, []string{"board", "state"}},
	} {
		metadata, ok := commands[command.name]
		if !ok || !metadata.auth || metadata.mutating != command.mutating {
			t.Fatalf("%s schema = %+v, present=%t", command.name, metadata, ok)
		}
		for _, name := range command.flags {
			if _, ok := flags[command.name][name]; !ok {
				t.Fatalf("%s is missing --%s", command.name, name)
			}
		}
	}
	for _, name := range []string{"issue add", "issue update", "issue move", "issue bulk move"} {
		if got := flags[name]["field"].kind; got != "custom-field-alias-or-id=value" {
			t.Fatalf("%s --field type = %q", name, got)
		}
	}
	for _, name := range []string{"search", "issue list", "issue show"} {
		if got := flags[name]["fields"].kind; got != "field-selector-list" {
			t.Fatalf("%s --fields type = %q", name, got)
		}
		selection, ok := commands[name].jsonData["fieldSelection"].([]any)
		if !ok || len(selection) != 1 || selection[0] != "FieldSelection" {
			t.Fatalf("%s JSON schema is missing fieldSelection: %#v", name, commands[name].jsonData)
		}
	}
	for _, name := range []string{"search", "issue list"} {
		issues, ok := commands[name].jsonData["issues"].([]any)
		if !ok || len(issues) != 1 || issues[0] != "Issue" {
			t.Fatalf("%s JSON schema is missing typed Issues: %#v", name, commands[name].jsonData)
		}
	}
	showSprints, ok := commands["issue show"].jsonData["sprints"].([]any)
	if !ok || len(showSprints) != 1 || showSprints[0] != "SprintMembership" {
		t.Fatalf("issue show JSON schema is missing Sprint Memberships: %#v", commands["issue show"].jsonData)
	}
	issueSprints, ok := document.Types["Issue"].(map[string]any)["sprints"].([]any)
	if !ok || len(issueSprints) != 1 || issueSprints[0] != "SprintMembership" {
		t.Fatalf("Issue type is missing Sprint Memberships: %#v", document.Types["Issue"])
	}
	for _, field := range []string{"id", "name", "state", "boardId", "goal", "startDate", "endDate", "completeDate"} {
		if membership, ok := document.Types["SprintMembership"].(map[string]any); !ok || membership[field] == nil {
			t.Fatalf("SprintMembership type is missing %q: %#v", field, document.Types["SprintMembership"])
		}
	}
	if got := flags["issue move"]["input-format"]; got.kind != "enum:jira|jfm|markdown" || got.defaultValue != "jira" {
		t.Fatalf("issue move --input-format schema = %+v", got)
	}
	for _, field := range []string{"commented", "resolution"} {
		if commands["issue move"].jsonData[field] == nil {
			t.Fatalf("issue move JSON schema is missing %q: %#v", field, commands["issue move"].jsonData)
		}
	}
	if target, ok := document.Types["BatchTarget"].(map[string]any); !ok || target["resolution"] == nil {
		t.Fatalf("BatchTarget schema is missing resolution: %#v", document.Types["BatchTarget"])
	}
	if target, ok := document.Types["BatchTarget"].(map[string]any); !ok || target["issueTypeInput"] == nil || target["issueType"] == nil {
		t.Fatalf("BatchTarget schema is missing Issue Type fields: %#v", document.Types["BatchTarget"])
	}
	if current, ok := document.Types["BatchCurrent"].(map[string]any); !ok || current["issueType"] == nil {
		t.Fatalf("BatchCurrent schema is missing issueType: %#v", document.Types["BatchCurrent"])
	}
	for _, field := range []string{"unchanged", "unknown"} {
		if batch, ok := document.Types["BatchResult"].(map[string]any); !ok || batch[field] == nil {
			t.Fatalf("BatchResult schema is missing %q: %#v", field, document.Types["BatchResult"])
		}
	}
	for _, field := range []string{"updated", "unchanged", "unknown", "issueType"} {
		if commands["issue update"].jsonData[field] == nil {
			t.Fatalf("issue update JSON schema is missing %q: %#v", field, commands["issue update"].jsonData)
		}
	}
	for _, field := range []string{"issueKey", "currentIssueType", "issueTypes"} {
		if commands["issue list-types"].jsonData[field] == nil {
			t.Fatalf("issue list-types JSON schema is missing %q: %#v", field, commands["issue list-types"].jsonData)
		}
	}
	if flags["issue update"]["type"].kind != "string" || flags["issue bulk update"]["type"].kind != "string" {
		t.Fatalf("Issue Type flag schemas = update:%+v bulk:%+v", flags["issue update"]["type"], flags["issue bulk update"]["type"])
	}
	if got := flags["issue update"]["type"].conflicts; strings.Join(got, ",") != strings.Join(issueTypeUpdateConflictingFlags, ",") {
		t.Fatalf("issue update --type conflictsWith = %v", got)
	}
	if got := flags["sprint list"]["state"]; got.kind != "enum:active|closed|future|all" || got.defaultValue != "active" {
		t.Fatalf("sprint list --state schema = %+v", got)
	}
	for _, field := range []string{"total", "boards"} {
		if commands["board list"].jsonData[field] == nil {
			t.Fatalf("board list JSON schema is missing %q: %#v", field, commands["board list"].jsonData)
		}
	}
	for _, field := range []string{"total", "sprints", "failedBoards"} {
		if commands["sprint list"].jsonData[field] == nil {
			t.Fatalf("sprint list JSON schema is missing %q: %#v", field, commands["sprint list"].jsonData)
		}
	}
	for _, field := range []string{"boardId", "boardName", "originBoardId", "goal", "startDate", "endDate", "completeDate"} {
		if sprintType, ok := document.Types["Sprint"].(map[string]any); !ok || sprintType[field] == nil {
			t.Fatalf("Sprint type is missing %q: %#v", field, document.Types["Sprint"])
		}
	}
	for _, command := range []string{"issue list", "issue add", "issue update"} {
		for _, name := range []string{"label", "component", "fix-version"} {
			if flag, ok := flags[command][name]; ok && !flag.repeatable {
				t.Fatalf("%s --%s is not repeatable", command, name)
			}
		}
	}
	for _, command := range []string{"issue add", "issue update", "issue comment add"} {
		inputFormat, ok := flags[command]["input-format"]
		if !ok || inputFormat.kind != "enum:jira|jfm|markdown" || inputFormat.defaultValue != "jira" {
			t.Fatalf("%s --input-format schema = %+v, present=%t", command, inputFormat, ok)
		}
	}
}
