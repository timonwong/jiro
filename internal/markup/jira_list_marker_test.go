package markup

import "testing"

// TestJiraListMarkerPrefixMatchesRenderer pins the line-start reading against
// the captures in testdata/jfm/jira_evidence: every input here was rendered by
// the live Jira renderer, and want records whether that render was a list.
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
