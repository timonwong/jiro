package markup

import (
	"context"
	"testing"
)

// TestJiraEffectOpenerKilledMatchesRenderer pins the one-rune rule against the
// live Jira renderer. Every row is a render that was observed; the rows whose
// reading jiro has to produce are checked in as archives under
// testdata/jfm/jira_evidence, and the rest are probes in the
// forced-newline-effect-kill campaign of hack/jira-render-evidence.py, which
// reproduces each capture. The run is given whole and the scan starts at the
// opener's content, which is where a caller asks the question.
func TestJiraEffectOpenerKilledMatchesRenderer(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		run       string
		start     int
		delimiter byte
		want      bool
	}{
		{name: "one rune before a word rune", run: `*a*b*`, start: 1, delimiter: '*', want: true},
		{name: "one multi-byte rune before a word rune", run: `*€*b*`, start: 1, delimiter: '*', want: true},
		{name: "one rune before an accented letter", run: `*a*é*`, start: 1, delimiter: '*', want: true},
		{name: "one rune before a digit", run: `*1*2*`, start: 1, delimiter: '*', want: true},
		{name: "one CJK rune before a CJK rune", run: `*中*文*`, start: 1, delimiter: '*', want: true},
		{name: "one rune before a closing candidate", run: `*a**b*`, start: 1, delimiter: '*'},
		{name: "one rune at the run end", run: `*a*`, start: 1, delimiter: '*'},
		{name: "one rune before punctuation", run: `*a*,b*`, start: 1, delimiter: '*'},
		{name: "one rune before a multi-byte non-word rune", run: `*€*€*`, start: 1, delimiter: '*'},
		{name: "two runes before a word rune", run: `*ab*c*`, start: 1, delimiter: '*'},
		{name: "two runes one of them multi-byte", run: `*a€*b*`, start: 1, delimiter: '*'},
		{name: "brace form one rune in", run: `*a{*}b*`, start: 1, delimiter: '*'},
		{name: "escaped candidate one rune in", run: `*\*b*`, start: 1, delimiter: '*'},
		{name: "space one rune in", run: `*a *b*`, start: 1, delimiter: '*'},
		{name: "content of a brace-form opener", run: `{*}a*b*`, start: 3, delimiter: '*', want: true},
		{name: "strikethrough", run: `-a-b-`, start: 1, delimiter: '-', want: true},
		{name: "italic", run: `_a_b_`, start: 1, delimiter: '_', want: true},
		{name: "inserted", run: `+a+b+`, start: 1, delimiter: '+', want: true},
		{name: "subscript", run: `~a~b~`, start: 1, delimiter: '~', want: true},
		{name: "superscript", run: `^a^b^`, start: 1, delimiter: '^', want: true},
		{name: "another delimiter one rune in", run: `*a_b*`, start: 1, delimiter: '*'},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := jiraEffectOpenerKilled(test.run, test.start, len(test.run), test.delimiter); got != test.want {
				t.Fatalf("jiraEffectOpenerKilled(%q, %d, %q) = %v, want %v", test.run, test.start, test.delimiter, got, test.want)
			}
		})
	}
}

// TestJiraCitationCloseHonoursTheEffectDelimiterRules holds that `??` is read
// by the same rules as a bare Effect Delimiter -- the one-rune kill and the
// backslash lookbehind -- so that the plain-text escaper and the Monospace Span
// hazard scan, its only readers, cannot disagree with the parser. Every row is
// a render that was observed, in the forced-newline-effect-kill campaign of
// hack/jira-render-evidence.py and in the citation_* archives.
func TestJiraCitationCloseHonoursTheEffectDelimiterRules(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		run  string
		at   int
		want int
	}{
		{name: "one rune before a word rune", run: `??a??b??`, want: -1},
		{name: "two runes before a word rune", run: `??ab??c??`, want: 7},
		{name: "one rune at the run end", run: `??a??`, want: 3},
		{name: "one multi-byte rune before a word rune", run: `??€??b??`, want: -1},
		{name: "the candidate the kill refuses", run: `??€??b??`, at: 5, want: 8},
		{name: "opener behind a backslash pair", run: `a\\??x??`, at: 3, want: -1},
		{name: "escaped candidate before a word rune", run: `??ab\??c??`, want: 8},
		{name: "escaped candidate before a space", run: `??ab\?? c??`, want: 9},
		{name: "candidate behind an even backslash run", run: `??ab\\\\??`, want: -1},
		{name: "escaped candidate one rune in", run: `??\??b??`, want: 6},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := jiraCitationClose(context.Background(), test.run, 0, test.at, len(test.run))
			if got != test.want {
				t.Fatalf("jiraCitationClose(%q, %d) = %d, want %d", test.run, test.at, got, test.want)
			}
		})
	}
}

// TestJiraKilledOpenerResumesAfterItsFirstByte holds the two consequences a
// kill has beyond finding no closer: the scan rereads from the byte after the
// opener, brace form included, and the kill is not a failed scan, so a later
// opener carrying the same delimiter can still pair.
func TestJiraKilledOpenerResumesAfterItsFirstByte(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		run   string
		pairs []string
	}{
		{name: "killed opener", run: `*a*b*`, pairs: nil},
		{name: "killed brace-form opener", run: `{*}a*b*`, pairs: []string{`*}a*b*`}},
		{name: "killed opener before a later pair", run: `*a*b* *c*`, pairs: []string{`*c*`}},
		{name: "killed opener after a closed pair", run: `*a**b*c*`, pairs: []string{`*a*`}},
		{name: "scan that continues", run: `*ab*c*`, pairs: []string{`*ab*c*`}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := make([]string, 0)
			forEachJiraEffectPair(context.Background(), test.run, 0, len(test.run), func(pair jiraEffectPair) {
				got = append(got, test.run[pair.OpenStart:pair.CloseEnd])
			})
			if len(got) != len(test.pairs) {
				t.Fatalf("pairs of %q = %q, want %q", test.run, got, test.pairs)
			}
			for index := range got {
				if got[index] != test.pairs[index] {
					t.Fatalf("pairs of %q = %q, want %q", test.run, got, test.pairs)
				}
			}
		})
	}
}
