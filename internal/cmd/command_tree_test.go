package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestCanonicalCommandTreeUsesSingularPaths(t *testing.T) {
	root := (&app{}).rootCommand()

	for _, path := range []string{
		"api",
		"board list",
		"jfm to-jira",
		"jfm from-jira",
		"cache refresh",
		"field list",
		"project list",
		"project show",
		"sprint list",
		"issue list",
		"issue show",
		"issue add",
		"issue clone",
		"issue update",
		"issue list-types",
		"issue comment add",
		"issue comment edit",
		"issue comment list",
		"issue link add",
		"issue link list",
		"issue link delete",
		"issue link types",
		"issue list-transitions",
		"issue move",
		"issue assign",
		"issue bulk move",
		"issue bulk assign",
		"issue bulk update",
	} {
		command := exactCommand(root, path)
		if command == nil || !command.Runnable() {
			t.Fatalf("canonical command %q is missing or not runnable", path)
		}
	}

	for _, path := range []string{
		"issues",
		"fields",
		"projects",
		"cache fields",
		"issue create",
		"issue comments",
		"issue links",
		"issue unlink",
		"issue link-types",
		"issue transition",
		"issue bulk-transition",
		"issue bulk-assign",
		"board add",
		"board update",
		"board delete",
		"sprint add",
		"sprint update",
		"sprint delete",
		"sprint start",
		"sprint close",
	} {
		if command := exactCommand(root, path); command != nil {
			t.Fatalf("removed command %q still exists", path)
		}
	}

	var checkAliases func(*cobra.Command)
	checkAliases = func(command *cobra.Command) {
		if len(command.Aliases) != 0 {
			t.Errorf("%s still exposes aliases %v", command.CommandPath(), command.Aliases)
		}
		for _, child := range command.Commands() {
			checkAliases(child)
		}
	}
	checkAliases(root)
	if root.PersistentFlags().Lookup("raw") != nil {
		t.Fatal("removed --raw flag is still registered")
	}
	for _, name := range []string{"host", "username", "auth-type"} {
		if root.PersistentFlags().Lookup(name) != nil {
			t.Fatalf("removed --%s flag is still registered", name)
		}
	}
}

func exactCommand(root *cobra.Command, path string) *cobra.Command {
	command := root
	for _, name := range strings.Fields(path) {
		var next *cobra.Command
		for _, child := range command.Commands() {
			if child.Name() == name {
				next = child
				break
			}
		}
		if next == nil {
			return nil
		}
		command = next
	}
	return command
}
