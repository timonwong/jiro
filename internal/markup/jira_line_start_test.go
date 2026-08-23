package markup

import (
	"context"
	"testing"
)

// TestJiraListMarkerPrefixMatchesRenderer pins the line-start reading against
// the live Jira renderer. Every row is a render that was observed; the rows
// whose protected form jiro has to produce are checked in as archives under
// testdata/jfm/jira_evidence, and the rest are probes in round7 of
// hack/jira-render-evidence.py, which reproduces each capture.
func TestJiraListMarkerPrefixMatchesRenderer(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		line  string
		start int
		end   int
	}{
		{name: "bullet", line: "* item", start: 0, end: 1},
		{name: "square bullet", line: "- item", start: 0, end: 1},
		{name: "number", line: "# item", start: 0, end: 1},
		{name: "nested bullet", line: "** item", start: 0, end: 2},
		{name: "bullet then number", line: "*# item", start: 0, end: 2},
		{name: "number then bullet", line: "#* item", start: 0, end: 2},
		{name: "square bullet then bullet", line: "-* item", start: 0, end: 2},
		{name: "bullet then square bullet", line: "*- item", start: 0, end: 2},
		{name: "square bullet then number", line: "-# item", start: 0, end: 2},
		{name: "number then square bullet", line: "#- item", start: 0, end: 2},
		{name: "en dash", line: "-- item"},
		{name: "em dash", line: "--- item"},
		{name: "bullet without a space", line: "*item"},
		{name: "square bullet without a space", line: "-item"},
		{name: "lone bullet", line: "*"},
		{name: "lone square bullet", line: "-"},
		{name: "lone number", line: "#"},
		{name: "run without a space", line: "**"},
		{name: "trailing space", line: "* ", start: 0, end: 1},
		{name: "tab after the marker", line: "*\titem", start: 0, end: 1},
		{name: "newline after the marker", line: "*\nfoo"},
		{name: "forced newline after the marker", line: `* \\`, start: 0, end: 1},
		{name: "indented bullet", line: " * item", start: 1, end: 2},
		{name: "twice indented bullet", line: "  * item", start: 2, end: 3},
		{name: "tab indented bullet", line: "\t* item", start: 1, end: 2},
		{name: "indented escaped bullet", line: ` \* item`},
		{name: "two spaces after the marker", line: "*  item", start: 0, end: 1},
		{name: "escaped bullet", line: `\* item`},
		{name: "character reference", line: "&#42; item"},
		{name: "monospace span", line: "{{* item}}"},
		{name: "heading before the marker", line: "h1. * item"},
		{name: "text before the marker", line: "x * item"},
		{name: "empty", line: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			start, end := jiraListMarkerPrefix(test.line)
			if start != test.start || end != test.end {
				t.Fatalf("jiraListMarkerPrefix(%q) = %d, %d; want %d, %d", test.line, start, end, test.start, test.end)
			}
		})
	}
}

// TestJiraLineControlPrefixLengthMatchesRenderer holds the other line-start rule
// on the same indent: Jira reads `h1.` and `bq.` past leading spaces and tabs
// too, and the reported length reaches one past the `.` the escaper rewrites.
func TestJiraLineControlPrefixLengthMatchesRenderer(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		line string
		want int
	}{
		{name: "heading", line: "h1. x", want: 3},
		{name: "deepest heading", line: "h6. x", want: 3},
		{name: "quote", line: "bq. x", want: 3},
		{name: "heading alone", line: "h1.", want: 3},
		{name: "indented heading", line: " h1. x", want: 4},
		{name: "twice indented heading", line: "  h1. x", want: 5},
		{name: "tab indented heading", line: "\th1. x", want: 4},
		{name: "indented quote", line: " bq. x", want: 4},
		{name: "heading without a space", line: "h1.x"},
		{name: "protected heading", line: "h1&#46; x"},
		{name: "indented protected heading", line: " h1&#46; x"},
		{name: "heading level Jira has none of", line: "h7. x"},
		{name: "text before the prefix", line: "x h1. x"},
		{name: "empty", line: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := jiraLineControlPrefixLength(test.line); got != test.want {
				t.Fatalf("jiraLineControlPrefixLength(%q) = %d, want %d", test.line, got, test.want)
			}
		})
	}
}

// TestEscapeTextForJiraTextProtectsLineStarts holds what the two rules above are
// for: the protection has to land on the marker or the `.` itself, whatever
// indent Jira skipped to reach it.
func TestEscapeTextForJiraTextProtectsLineStarts(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		text string
		want string
	}{
		{name: "bullet", text: "* item", want: `\* item`},
		{name: "indented bullet", text: " * item", want: ` \* item`},
		{name: "tab indented bullet", text: "\t* item", want: "\t\\* item"},
		{name: "nested markers", text: "** item", want: `\** item`},
		{name: "heading", text: "h1. x", want: "h1&#46; x"},
		{name: "indented heading", text: " h1. x", want: " h1&#46; x"},
		{name: "tab indented heading", text: "\th1. x", want: "\th1&#46; x"},
		{name: "indented quote", text: " bq. x", want: " bq&#46; x"},
		{name: "indented text", text: " plain", want: " plain"},
		{name: "marker after a newline", text: "x\n* item", want: "x\n\\* item"},
		{name: "indented marker after a newline", text: "x\n\t* item", want: "x\n\t\\* item"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, _, err := escapeTextForJiraText(context.Background(), test.text, true)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("escapeTextForJiraText(%q) = %q, want %q", test.text, got, test.want)
			}
		})
	}
}
