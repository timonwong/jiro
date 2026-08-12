package cmd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// This file diffs schemaDocument() (schema.go) against the real Cobra tree
// built by rootCommand() (root.go), in both directions, so a flag or command
// added to one side without the other fails a test instead of shipping
// stale metadata. See issue #53.
//
// A few divergences between the two sides are deliberate. They are named
// here, in one small table each, so a future addition is a conscious,
// reviewable decision rather than a silent exception buried in assertions.

// excludedCommandPaths are Cobra-injected commands with no place in jiro's
// documented CLI contract, so schemaDocument() has no entry for them.
var excludedCommandPaths = map[string]bool{
	"help": true,
}

// excludedCommandPrefixes excludes whole families of Cobra-injected commands
// by their leading path segment, e.g. "completion bash", "completion zsh", ...
var excludedCommandPrefixes = []string{
	"completion",
}

// excludedFlagNames are flags Cobra injects on every command that jiro's
// schema intentionally does not document.
var excludedFlagNames = map[string]bool{
	"help": true,
}

// effectiveDefaultAllowlist documents flags where schemaDocument() records
// the value jiro *resolves* an unset flag to at runtime rather than the
// literal zero value pflag registers as the flag's default. Both sides of
// the divergence are pinned down here, so unrelated drift on either the
// schema or the flag registration still fails the test.
type effectiveDefault struct {
	schemaDefault, pflagDefault string
}

var effectiveDefaultAllowlist = map[string]effectiveDefault{
	// api --method registers with an empty pflag default; prepareAPIRequest
	// (api.go) resolves an unset --method to GET (POST when a body is
	// supplied via --input/--field/--form). The schema documents the
	// resolved default, which is correct.
	"api|method": {schemaDefault: "GET", pflagDefault: ""},
}

// customRepeatableFlags documents flags backed by a pflag.Value whose
// Type() does not follow pflag's built-in stringSlice/stringArray naming
// (so repeatability cannot be derived from Value.Type() alone), but whose
// Set() accumulates across repeated invocations exactly like a slice flag,
// matching the schema's Repeatable: true.
var customRepeatableFlags = map[string]bool{
	"api|field":        true, // apiFieldFlag (api.go), Type() == "key=value"
	"api|string-field": true, // apiFieldFlag (api.go), Type() == "key=value"
}

// TestSchemaConformsToCommandTree walks the real Cobra tree built by
// rootCommand() and diffs it against schemaDocument() in both directions:
// every runnable command must have a schema entry and vice versa, and for
// commands present on both sides, flag names, shorthands, and defaults must
// match modulo the escape hatches above.
func TestSchemaConformsToCommandTree(t *testing.T) {
	root := (&app{}).rootCommand()
	// Materialize the commands and flags Cobra only adds lazily at execution
	// time (help, completion, --help) so the tree we diff matches what a
	// user actually sees, and so the escape hatches above have something to
	// exclude rather than being dead code.
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()

	document := schemaDocument()
	schemaCommands := make(map[string]commandSchema, len(document.Commands))
	for _, command := range document.Commands {
		schemaCommands[command.Name] = command
	}

	realCommands := make(map[string]*cobra.Command)
	var walk func(command *cobra.Command, path string)
	walk = func(command *cobra.Command, path string) {
		command.InitDefaultHelpFlag()
		if path != "" && command.Runnable() && !isExcludedCommandPath(path) {
			realCommands[path] = command
		}
		for _, child := range command.Commands() {
			childPath := child.Name()
			if path != "" {
				childPath = path + " " + child.Name()
			}
			walk(child, childPath)
		}
	}
	walk(root, "")

	for name := range schemaCommands {
		if _, ok := realCommands[name]; !ok {
			t.Errorf("schema documents command %q but no matching runnable Cobra command exists", name)
		}
	}
	for path := range realCommands {
		if _, ok := schemaCommands[path]; !ok {
			t.Errorf("Cobra command %q is runnable but schemaDocument() has no entry for it", path)
		}
	}

	compareFlagSet(t, "(global)", root.PersistentFlags(), document.GlobalFlags)

	for path, command := range realCommands {
		schema, ok := schemaCommands[path]
		if !ok {
			continue // already reported above
		}
		compareFlagSet(t, path, command.LocalFlags(), schema.Flags)
	}
}

func isExcludedCommandPath(path string) bool {
	if excludedCommandPaths[path] {
		return true
	}
	for _, prefix := range excludedCommandPrefixes {
		if path == prefix || strings.HasPrefix(path, prefix+" ") {
			return true
		}
	}
	return false
}

// compareFlagSet diffs a Cobra flag set against a command's schema flags in
// both directions and, for flags present on both sides, compares shorthand,
// default value, and (where derivable) repeatability. Flag "type" is
// intentionally never compared: schema.Type carries semantic vocabulary
// (issue-key, jira-date, enum:..., field-selector-list,
// custom-field-alias-or-id=value, ...) that pflag has no notion of, and
// "required" is intentionally never compared either: jiro enforces required
// flags by hand in RunE (there is no cobra.MarkFlagRequired call anywhere in
// this codebase), so pflag exposes nothing to diff it against.
func compareFlagSet(t *testing.T, path string, flags *pflag.FlagSet, schemaFlags []flagSchema) {
	t.Helper()

	schemaByName := make(map[string]flagSchema, len(schemaFlags))
	for _, flag := range schemaFlags {
		schemaByName[flag.Name] = flag
	}

	real := make(map[string]*pflag.Flag)
	flags.VisitAll(func(flag *pflag.Flag) {
		if excludedFlagNames[flag.Name] {
			return
		}
		real[flag.Name] = flag
	})

	for name := range schemaByName {
		if _, ok := real[name]; !ok {
			t.Errorf("%s: schema documents --%s but the Cobra command has no such flag", path, name)
		}
	}
	for name, flag := range real {
		schema, ok := schemaByName[name]
		if !ok {
			t.Errorf("%s: Cobra flag --%s has no schema entry", path, name)
			continue
		}
		compareFlag(t, path, flag, schema)
	}
}

func compareFlag(t *testing.T, path string, flag *pflag.Flag, schema flagSchema) {
	t.Helper()
	key := path + "|" + flag.Name

	if flag.Shorthand != schema.Short {
		t.Errorf("%s --%s: shorthand = %q, schema = %q", path, flag.Name, flag.Shorthand, schema.Short)
	}

	switch allowed, escaped := effectiveDefaultAllowlist[key]; {
	case escaped:
		if schemaDefaultString(schema.Default) != allowed.schemaDefault || flag.DefValue != allowed.pflagDefault {
			t.Errorf("%s --%s: effective-default allowlist entry is stale: schema default = %v, pflag default = %q; want schema default %q, pflag default %q",
				path, flag.Name, schema.Default, flag.DefValue, allowed.schemaDefault, allowed.pflagDefault)
		}
	case schema.Default != nil:
		if want := schemaDefaultString(schema.Default); flag.DefValue != want {
			t.Errorf("%s --%s: default = %q, schema = %q", path, flag.Name, flag.DefValue, want)
		}
	default:
		if zero, known := zeroDefault(flag.Value.Type()); known && flag.DefValue != zero {
			t.Errorf("%s --%s: default = %q, but schema documents no default (want zero value %q)", path, flag.Name, flag.DefValue, zero)
		}
	}

	// Repeatable is only checked one way: schema.Repeatable == true must be
	// backed by a slice-shaped flag (or a documented custom one). A
	// slice-shaped flag whose schema.Repeatable is false is not a conflict:
	// e.g. --fields (field-selector-list) is a comma-separated single value
	// by convention even though StringSliceVar also happens to accept
	// repeated occurrences.
	if schema.Repeatable {
		if repeatable, known := derivedRepeatable(flag.Value.Type()); known && !repeatable && !customRepeatableFlags[key] {
			t.Errorf("%s --%s: schema marks it repeatable, but pflag type %q is not slice-shaped", path, flag.Name, flag.Value.Type())
		}
	}
}

func schemaDefaultString(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

// zeroDefault reports the pflag DefValue string for a type's zero value, for
// flags schemaDocument() documents with no explicit default. known is false
// for pflag types this test does not recognize (e.g. custom pflag.Value
// implementations), so the default is simply not checked rather than guessed at.
func zeroDefault(pflagType string) (value string, known bool) {
	switch pflagType {
	case "string":
		return "", true
	case "bool":
		return "false", true
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
		return "0", true
	case "duration":
		return "0s", true
	case "stringSlice", "stringArray":
		return "[]", true
	default:
		return "", false
	}
}

// derivedRepeatable reports whether a pflag type name is slice-shaped, for
// pflag types this test recognizes.
func derivedRepeatable(pflagType string) (repeatable, known bool) {
	switch pflagType {
	case "string", "bool", "duration",
		"int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64":
		return false, true
	case "stringSlice", "stringArray",
		"intSlice", "int32Slice", "uintSlice", "boolSlice", "durationSlice",
		"float32Slice", "float64Slice":
		return true, true
	default:
		return false, false
	}
}
