package markup

import "testing"

// TestJiraEmoticonAtMatchesRenderer pins the token gate against the live Jira
// renderer. Every row is a render that was observed; the rows whose reading jiro
// has to produce are checked in as archives under testdata/jfm/jira_evidence,
// and every row is a probe in hack/jira-render-evidence.py, which reproduces
// each capture. The two rows the renderer disagrees with are marked where they
// stand.
func TestJiraEmoticonAtMatchesRenderer(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		markup string
		index  int
		want   int
	}{
		// Every supported token, at the one index it stands on.
		{"(y)", 0, 3}, {"(n)", 0, 3}, {"(i)", 0, 3}, {"(/)", 0, 3}, {"(x)", 0, 3},
		{"(!)", 0, 3}, {"(?)", 0, 3}, {"(+)", 0, 3}, {"(-)", 0, 3},
		{"(on)", 0, 4}, {"(off)", 0, 5}, {"(*)", 0, 3}, {"(*r)", 0, 4},
		{"(*g)", 0, 4}, {"(*b)", 0, 4}, {"(*y)", 0, 4},
		{"(flag)", 0, 6}, {"(flagoff)", 0, 9},
		{":)", 0, 2}, {":(", 0, 2}, {":P", 0, 2}, {":D", 0, 2}, {";)", 0, 2},
		// The gate is asymmetric: a following letter or digit suppresses the
		// icon, a preceding one leaves it.
		{"(y)foo", 0, 0}, {":)x", 0, 0}, {"a;)b", 1, 0}, {"(*y)x", 0, 0},
		{"f(x)", 1, 3}, {"x:)", 1, 2}, {"x;)", 1, 2},
		// U+200B is no word rune, and neither is a parenthesis or a delimiter.
		{"(x)​foo", 0, 3}, {"(x)(y)", 0, 3}, {"((x))", 1, 3}, {"(:))", 1, 2},
		{"(x)*foo*", 0, 3},
		// `(flagoff)` is its own token rather than `(flag)` plus text, and a
		// parenthesized letter that names no icon is nothing.
		{"(flag)off", 0, 0}, {"(a)", 0, 0},
		// Matching is exact and case-sensitive. `:d` and every upper-case
		// parenthesized spelling name no icon. Jira 8.20.10 does render `:p` and
		// `:-)`, but neither is in the token set the grammar evidence
		// established, and #83 bounds the set to that evidence rather than
		// widening it here; both stay ordinary text in each direction.
		{":d", 0, 0}, {"(X)", 0, 0}, {"(FLAG)", 0, 0},
		{":p", 0, 0}, {":-)", 0, 0},
		// A backslash inside the token leaves nothing to match. A backslash in
		// front of one is not this gate's business: Jira consumes it as the
		// token's own escape, which parseJiraInlines and the plain-text escaper
		// decide on the byte before the token.
		{"\\(x\\)", 1, 0}, {"(x\\)", 0, 0}, {"\\(x)", 1, 3}, {"\\:)", 1, 2},
	} {
		if got := jiraEmoticonAt(test.markup, test.index, len(test.markup)); got != test.want {
			t.Errorf("jiraEmoticonAt(%q, %d) = %d, want %d", test.markup, test.index, got, test.want)
		}
	}
}

// TestCanonicalJiraEmoticonTokenAcceptsTheSupportedSet pins the membership and
// alias rules the emoticon directive validates its content with.
func TestCanonicalJiraEmoticonTokenAcceptsTheSupportedSet(t *testing.T) {
	t.Parallel()
	for _, token := range jiraEmoticonTokens {
		canonical, supported := canonicalJiraEmoticonToken(token)
		if !supported {
			t.Errorf("canonicalJiraEmoticonToken(%q) is not supported", token)
			continue
		}
		// A canonical token is its own canonical form, so canonicalization is
		// idempotent and JFM output never drifts.
		if again, _ := canonicalJiraEmoticonToken(canonical); again != canonical {
			t.Errorf("canonicalJiraEmoticonToken(%q) = %q, which canonicalizes on to %q", token, canonical, again)
		}
	}
	// `(*y)` and `(*)` render the same yellow star; nothing else is aliased.
	if canonical, _ := canonicalJiraEmoticonToken("(*y)"); canonical != "(*)" {
		t.Errorf("canonicalJiraEmoticonToken(\"(*y)\") = %q, want \"(*)\"", canonical)
	}
	for _, token := range []string{"", "(rocket)", "(x) ", "(x)(y)", ":p", "*x*", "(X)", " (x)"} {
		if _, supported := canonicalJiraEmoticonToken(token); supported {
			t.Errorf("canonicalJiraEmoticonToken(%q) is supported", token)
		}
	}
}

// TestJiraNeutralizedEmoticonAtReadsBackWhatTheEscaperWrites pins the two sides
// of the colon and semicolon neutralizer to each other: every encoding
// escapeTextForJiraText writes is one this reader turns back into its token, and
// nothing else is decoded.
func TestJiraNeutralizedEmoticonAtReadsBackWhatTheEscaperWrites(t *testing.T) {
	t.Parallel()
	for _, token := range jiraEmoticonTokens {
		reference, encoded := jiraEmoticonLeadingReferences[token[0]]
		if !encoded {
			continue
		}
		markup := reference + token[1:]
		got, length := jiraNeutralizedEmoticonAt(markup, 0, len(markup))
		if got != token || length != len(markup) {
			t.Errorf("jiraNeutralizedEmoticonAt(%q) = %q, %d; want %q, %d", markup, got, length, token, len(markup))
		}
	}
	// A reference that names no token, and a token that is already characters,
	// are both left alone.
	for _, markup := range []string{"&#58;", "&#58;x", "&#40;x&#41;", "&#59;x", ":)", "&amp;"} {
		if got, length := jiraNeutralizedEmoticonAt(markup, 0, len(markup)); length != 0 {
			t.Errorf("jiraNeutralizedEmoticonAt(%q) = %q, %d; want no match", markup, got, length)
		}
	}
}
