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

// TestJiraLineControlPrefixMatchesRenderer holds the other line-start rule on
// the same indent: Jira reads `h1.` and `bq.` past leading spaces and tabs too.
// The reported end reaches one past the `.` the escaper rewrites, and the level
// and quote flag are what the block parser opens the block from.
func TestJiraLineControlPrefixMatchesRenderer(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		line  string
		level int
		quote bool
		end   int
	}{
		{name: "heading", line: "h1. x", level: 1, end: 3},
		{name: "deepest heading", line: "h6. x", level: 6, end: 3},
		{name: "quote", line: "bq. x", quote: true, end: 3},
		{name: "heading alone", line: "h1.", level: 1, end: 3},
		{name: "indented heading", line: " h2. x", level: 2, end: 4},
		{name: "twice indented heading", line: "  h1. x", level: 1, end: 5},
		{name: "tab indented heading", line: "\th1. x", level: 1, end: 4},
		{name: "twice tab indented heading", line: "\t\th1. x", level: 1, end: 5},
		{name: "indented quote", line: " bq. x", quote: true, end: 4},
		{name: "twice indented quote", line: "  bq. x", quote: true, end: 5},
		{name: "heading without a space", line: "h1.x", level: 1, end: 3},
		{name: "quote without a space", line: "bq.x", quote: true, end: 3},
		{name: "heading with two spaces", line: "h1.  x", level: 1, end: 3},
		{name: "heading with a tab", line: "h1.\tx", level: 1, end: 3},
		{name: "indented heading without a space", line: "  h1.x", level: 1, end: 5},
		{name: "two digit heading level", line: "h10. x"},
		{name: "two digit heading level without a space", line: "h10.x"},
		{name: "heading level zero", line: "h0.x"},
		{name: "protected heading", line: "h1&#46; x"},
		{name: "indented protected heading", line: " h1&#46; x"},
		{name: "heading level Jira has none of", line: "h7. x"},
		{name: "text before the prefix", line: "x h1. x"},
		{name: "empty", line: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			level, quote, end := jiraLineControlPrefix(test.line)
			if level != test.level || quote != test.quote || end != test.end {
				t.Fatalf("jiraLineControlPrefix(%q) = %d, %t, %d; want %d, %t, %d", test.line, level, quote, end, test.level, test.quote, test.end)
			}
		})
	}
}

// TestJiraLineControlContentStartSkipsEveryGap holds the other half of the rule
// above: Jira keeps none of the spaces and tabs between the `.` and the content,
// so every row below renders the heading or the quote with the content `x`.
func TestJiraLineControlContentStartSkipsEveryGap(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		line string
		want int
	}{
		{name: "no gap", line: "h1.x", want: 3},
		{name: "one space", line: "h1. x", want: 4},
		{name: "two spaces", line: "h1.  x", want: 5},
		{name: "tab", line: "h1.\tx", want: 4},
		{name: "spaces and a tab", line: "h2.  \tx", want: 6},
		{name: "quote", line: "bq.\tx", want: 4},
		{name: "nothing after the control", line: "h1.", want: 3},
		{name: "gap and nothing else", line: "h1. ", want: 4},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, _, end := jiraLineControlPrefix(test.line)
			if got := jiraLineControlContentStart(test.line, end); got != test.want {
				t.Fatalf("jiraLineControlContentStart(%q, %d) = %d; want %d", test.line, end, got, test.want)
			}
		})
	}
}

// TestJiraLineMalformedHeadingPrefixKeepsTheLineVisible covers the shapes jiro
// reads as an attempt at a heading it does not convert: a level Jira has none of
// stays literal with a warning, whether or not a space follows the `.`, because
// Jira renders both as text.
func TestJiraLineMalformedHeadingPrefixKeepsTheLineVisible(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		line string
		want bool
	}{
		{name: "heading level Jira has none of", line: "h7. x", want: true},
		{name: "two digit heading level", line: "h12. x", want: true},
		{name: "heading level alone", line: "h7.", want: true},
		{name: "heading jiro converts", line: "h1. x", want: true},
		{name: "heading without a space", line: "h7.x", want: true},
		{name: "two digit heading level without a space", line: "h10.x", want: true},
		{name: "heading level zero without a space", line: "h0.x", want: true},
		{name: "quote without a space", line: "bq.x"},
		{name: "quote jiro converts", line: "bq. x"},
		{name: "letter after the h", line: "hx. y"},
		{name: "no period", line: "h1 x"},
		{name: "indented heading level Jira has none of", line: " h7. x"},
		{name: "empty", line: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := jiraLineMalformedHeadingPrefix(test.line); got != test.want {
				t.Fatalf("jiraLineMalformedHeadingPrefix(%q) = %t; want %t", test.line, got, test.want)
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
		{name: "heading without a space", text: "h1.x", want: "h1&#46;x"},
		{name: "quote without a space", text: "bq.x", want: "bq&#46;x"},
		{name: "heading level Jira has none of", text: "h7.x", want: "h7.x"},
		{name: "indented heading", text: " h1. x", want: " h1&#46; x"},
		{name: "tab indented heading", text: "\th1. x", want: "\th1&#46; x"},
		{name: "indented quote", text: " bq. x", want: " bq&#46; x"},
		{name: "indented text", text: " plain", want: " plain"},
		{name: "marker after a newline", text: "x\n* item", want: "x\n\\* item"},
		{name: "indented marker after a newline", text: "x\n\t* item", want: "x\n\t\\* item"},
		{name: "dash rule", text: "----", want: `\----`},
		{name: "five dash rule", text: "-----", want: `\-----`},
		{name: "indented dash rule", text: " ----", want: ` \----`},
		{name: "dash rule after a newline", text: "x\n----", want: "x\n\\----"},
		{name: "dash run behind a word", text: "x ----", want: "x ----"},
		{name: "dash run Jira draws no rule for", text: "------", want: "------"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, _, err := escapeTextForJiraText(context.Background(), test.text, jiraInlineRender{lineStart: jiraLineStartEveryRule})
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("escapeTextForJiraText(%q) = %q, want %q", test.text, got, test.want)
			}
		})
	}
}

// TestJiraLineMarkerRunMatchesRenderer pins the reading the block parser needs
// on top of the escaper's: the whole marker run, where the item content starts,
// and whether the run is one of the dash runs Jira reads as a marker only while
// a list is open. Every row is a render captured in round7 or round8 of
// hack/jira-render-evidence.py.
func TestJiraLineMarkerRunMatchesRenderer(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		line    string
		run     string
		content int
		dashRun bool
	}{
		{name: "bullet", line: "* item", run: "*", content: 2},
		{name: "square bullet", line: "- item", run: "-", content: 2},
		{name: "number", line: "# item", run: "#", content: 2},
		{name: "square bullet then bullet", line: "-* item", run: "-*", content: 3},
		{name: "bullet then square bullet", line: "*- item", run: "*-", content: 3},
		{name: "tab after the marker", line: "*\titem", run: "*", content: 2},
		{name: "tab after a square bullet", line: "-\titem", run: "-", content: 2},
		{name: "two spaces after the marker", line: "*  item", run: "*", content: 3},
		{name: "indented square bullet", line: "  - item", run: "-", content: 4},
		{name: "tab indented bullet", line: "\t* item", run: "*", content: 3},
		{name: "empty item", line: "* ", run: "*", content: 2},
		{name: "empty nested item", line: "** ", run: "**", content: 3},
		{name: "en dash run", line: "-- item", run: "--", content: 3, dashRun: true},
		{name: "em dash run", line: "--- item", run: "---", content: 4, dashRun: true},
		{name: "dash run with an empty item", line: "-- ", run: "--", content: 3, dashRun: true},
		{name: "mixed run ending in dashes", line: "*-- item", run: "*--", content: 4},
		{name: "run without a space", line: "**"},
		{name: "number run without a space", line: "##"},
		{name: "dash run without a space", line: "--"},
		{name: "lone bullet", line: "*"},
		{name: "bullet without a space", line: "*item"},
		{name: "escaped bullet", line: `\* item`},
		{name: "empty", line: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			run, content, dashRun := jiraLineMarkerRun(test.line)
			if run != test.run || content != test.content || dashRun != test.dashRun {
				t.Fatalf("jiraLineMarkerRun(%q) = %q, %d, %t; want %q, %d, %t", test.line, run, content, dashRun, test.run, test.content, test.dashRun)
			}
		})
	}
}

// TestJiraLineThematicBreakMatchesRenderer holds the third line-start rule on
// the same indent: Jira draws the rule past leading spaces and tabs for a run of
// four or five dashes and ignores the ones trailing it, while a sixth dash and
// anything else behind the run leave the line its text. Every row is a render
// captured in round13 or round16 of hack/jira-render-evidence.py.
func TestJiraLineThematicBreakMatchesRenderer(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		line string
		want bool
	}{
		{name: "rule", line: "----", want: true},
		{name: "indented rule", line: " ----", want: true},
		{name: "tab indented rule", line: "\t----", want: true},
		{name: "trailing space", line: " ---- ", want: true},
		{name: "trailing tab", line: "----\t", want: true},
		{name: "five dashes", line: "-----", want: true},
		{name: "three dashes", line: "---"},
		{name: "six dashes", line: "------"},
		{name: "dash behind the run", line: "---- -"},
		{name: "dash run with an item", line: "---- x"},
		{name: "text before the rule", line: "x ----"},
		{name: "rule on a later line", line: "x\n----"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := jiraLineThematicBreak(test.line); got != test.want {
				t.Fatalf("jiraLineThematicBreak(%q) = %t, want %t", test.line, got, test.want)
			}
		})
	}
}

// TestJiraLineStartBlockNameNamesEveryCellBlock covers the reading a table cell
// gets: Jira renders a block at the cell's own line start, and the name is what
// the warning tells the reader jiro kept as text instead.
func TestJiraLineStartBlockNameNamesEveryCellBlock(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		line string
		want string
	}{
		{name: "bullet", line: "* item", want: "a list"},
		{name: "square bullet", line: "- item", want: "a list"},
		{name: "number", line: "# item", want: "a list"},
		{name: "indented bullet", line: " * item", want: "a list"},
		{name: "heading", line: "h1. x", want: "a heading"},
		{name: "quote", line: "bq. x", want: "a block quote"},
		{name: "rule", line: "----", want: "a horizontal rule"},
		{name: "dash run", line: "-- item"},
		{name: "escaped bullet", line: `\* item`},
		{name: "plain text", line: "x"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := jiraLineStartBlockName(test.line); got != test.want {
				t.Fatalf("jiraLineStartBlockName(%q) = %q, want %q", test.line, got, test.want)
			}
		})
	}
}
