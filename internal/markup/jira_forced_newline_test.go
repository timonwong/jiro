package markup

import (
	"context"
	"testing"
)

// TestJiraForcedNewlineRunMatchesRenderer pins the run classifier against the
// live Jira renderer. Every row is a render that was observed; the rows whose
// reading jiro has to produce are checked in as archives under
// testdata/jfm/jira_evidence, and the rest are probes in the
// forced-newline-effect-kill campaign of hack/jira-render-evidence.py, which
// reproduces each capture. The line is given whole and the run under test is
// named by its start offset, because the rule is decided on the raw line rather
// than on the run.
func TestJiraForcedNewlineRunMatchesRenderer(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		line  string
		at    int
		want  bool
		runIs int
	}{
		{name: "pair between words", line: `a\\b`, at: 1, want: true, runIs: 2},
		{name: "pair at the line end", line: `a\\`, at: 1, want: true, runIs: 2},
		{name: "pair at the line start", line: `\\a`, at: 0, want: true, runIs: 2},
		{name: "lone backslash", line: `a\b`, at: 1, runIs: 1},
		{name: "run of three", line: `a\\\b`, at: 1, runIs: 3},
		{name: "run of four", line: `a\\\\b`, at: 1, runIs: 4},
		{name: "first pair of a token", line: `ab\\cd\\ef`, at: 2, runIs: 2},
		{name: "last pair of a token", line: `ab\\cd\\ef`, at: 6, want: true, runIs: 2},
		{name: "pair before a longer run", line: `a\\b\\\\c`, at: 1, runIs: 2},
		{name: "longer run before a pair", line: `a\\\\b\\c`, at: 6, want: true, runIs: 2},
		{name: "space separates tokens", line: `a\\b c\\d`, at: 1, want: true, runIs: 2},
		{name: "tab separates tokens", line: "a\\\\b\tc\\\\d", at: 1, want: true, runIs: 2},
		{name: "vertical tab separates tokens", line: "a\\\\b\vc\\\\d", at: 1, want: true, runIs: 2},
		{name: "form feed separates tokens", line: "a\\\\b\fc\\\\d", at: 1, want: true, runIs: 2},
		{name: "newline separates tokens", line: "a\\\\b\nc\\\\d", at: 1, want: true, runIs: 2},
		{name: "no-break space separates nothing", line: "a\\\\b\u00a0c\\\\d", at: 1, runIs: 2},
		{name: "period separates nothing", line: `a\\b.c\\d`, at: 1, runIs: 2},
		{name: "hyphen separates nothing", line: `a\\b-c\\d`, at: 1, runIs: 2},
		{name: "asterisk separates nothing", line: `a\\b*c\\d`, at: 1, runIs: 2},
		{name: "cell separator separates nothing", line: `a\\b|c\\d`, at: 1, runIs: 2},
		{name: "pair before a run at the line end", line: `x\\y\\`, at: 1, runIs: 2},
		{name: "later lone backslash Jira keeps", line: `a\\b\c`, at: 1, runIs: 2},
		{name: "later lone backslash Jira consumes", line: `a\\b\-c`, at: 1, want: true, runIs: 2},
		{name: "later trailing lone backslash", line: `a\\b\`, at: 1, runIs: 2},
		{name: "lone backslash before the pair", line: `a\b\\c`, at: 3, want: true, runIs: 2},
		{name: "later run of three", line: `a\\b\\\c`, at: 1, runIs: 2},
		{name: "consumed escape inside a span body", line: `{{a\\}}b\}}`, at: 3, want: true, runIs: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runEnd := jiraBackslashRunEnd(test.line, test.at, len(test.line))
			if runEnd-test.at != test.runIs {
				t.Fatalf("run length = %d, want %d", runEnd-test.at, test.runIs)
			}
			if got := jiraForcedNewlineRun(test.line, test.at, runEnd, len(test.line)); got != test.want {
				t.Fatalf("jiraForcedNewlineRun(%q, %d) = %v, want %v", test.line, test.at, got, test.want)
			}
		})
	}
}

// TestJiraForcedNewlineRunReadsItsDomain holds the two domains that are not the
// physical line: a table cell, whose `|` separates no token and which therefore
// has to end the scan itself, and a link's visible text, which Jira renders with
// no forced newline at all. The row here is the body line of the archived
// `||h1||h2||` probe (testdata/jfm/jira_evidence/backslash_pair_in_table_cells),
// whose render breaks in both cells; the link text is the archived
// link_alias_backslash_pair, whose render keeps both backslashes.
func TestJiraForcedNewlineRunReadsItsDomain(t *testing.T) {
	t.Parallel()
	const row = `|a\\b|c\\d|`
	if !jiraForcedNewlineRun(row, 2, 4, 4) {
		t.Fatalf("first cell of %q does not break; want a forced newline", row)
	}
	if jiraForcedNewlineRun(row, 2, 4, len(row)) {
		t.Fatalf("first cell of %q breaks only because the scan left the cell", row)
	}
	if !jiraForcedNewlineRun(row, 7, 9, 10) {
		t.Fatalf("second cell of %q does not break; want a forced newline", row)
	}
	const label = `a\\b`
	if jiraForcedNewlineRun(label, 1, 3, jiraNoForcedNewlineDomain) {
		t.Fatalf("link text %q breaks; Jira shows both backslashes", label)
	}
}

// TestJiraTableCellReadsItsOwnForcedNewline proves the cell domain reaches the
// inline parser: the cells of the row are converted separately even though the
// table itself is carried through as raw Jira Markup.
func TestJiraTableCellReadsItsOwnForcedNewline(t *testing.T) {
	t.Parallel()
	const row = "||h||\n|a\\\\b|c\\\\d|"
	document, _ := parseJiraMarkup(context.Background(), row)
	table, ok := document.Blocks[0].(tableBlock)
	if !ok {
		t.Fatalf("block = %T, want a table", document.Blocks[0])
	}
	for index, cell := range table.Rows[0] {
		breaks := 0
		for _, inline := range cell.Inlines {
			if _, isBreak := inline.(hardBreakInline); isBreak {
				breaks++
			}
		}
		if breaks != 1 {
			t.Fatalf("cell %d holds %d hard breaks, want 1", index, breaks)
		}
	}
}

// TestDecodeJiraEscapesKeepsBackslashRuns holds the span-body reading: only a
// lone backslash escapes, and a run of two or more is characters Jira shows.
// The trailing-backslash row guards the decoder's own contract rather than a
// reachable body: since #110 the span closer reports a body end that excludes
// the backslash it consumes, so a Monospace Span body never ends in one.
func TestDecodeJiraEscapesKeepsBackslashRuns(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{name: "lone escape", body: `x\-y`, want: "x-y"},
		{name: "lone backslash before a literal", body: `C:\temp`, want: `C:\temp`},
		{name: "trailing backslash", body: `a\`, want: `a\`},
		{name: "pair", body: `a\\b`, want: `a\\b`},
		{name: "run of three", body: `a\\\b`, want: `a\\\b`},
		{name: "run of four", body: `a\\\\b`, want: `a\\\\b`},
		{name: "run before an escapable character", body: `a\\\*b`, want: `a\\\*b`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := decodeJiraEscapes(context.Background(), test.body)
			if got != test.want {
				t.Fatalf("decodeJiraEscapes(%q) = %q, want %q", test.body, got, test.want)
			}
		})
	}
}

// TestJiraBackslashPrecedesKillsMarkup holds the lookbehind: the byte before a
// candidate decides it, so an even backslash run leaves no closer where the old
// sequential skip stepped over one. Every row is a render that was observed, in
// the forced-newline-effect-kill campaign of hack/jira-render-evidence.py and
// in the effect_escaped_closer_* and effect_closer_behind_* archives.
func TestJiraBackslashPrecedesKillsMarkup(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		run  string
		want bool
	}{
		{name: "escaped closer", run: `*ab\*`},
		{name: "closer behind a pair", run: `*ab\\*`},
		{name: "closer behind a run of four", run: `*ab\\\\*`},
		{name: "closer behind a word rune", run: `*ab*`, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			closeStart, _, killed := findJiraEffectClose(context.Background(), test.run, 1, len(test.run), '*')
			if killed {
				t.Fatalf("findJiraEffectClose(%q) killed the opener, want a plain scan", test.run)
			}
			if (closeStart >= 0) != test.want {
				t.Fatalf("findJiraEffectClose(%q) = %d, want closer: %v", test.run, closeStart, test.want)
			}
		})
	}
}
