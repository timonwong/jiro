package markup

import (
	"context"
	"strings"
	"testing"
)

// TestJiraMonospaceSpanEndMatchesRenderer pins the closer search against the
// live renders archived in round10 of hack/jira-render-evidence.py: which `}}`
// closes a Monospace Span once backslashes sit in front of it, and where the
// body ends when Jira consumes one of them.
func TestJiraMonospaceSpanEndMatchesRenderer(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		source  string
		close   int
		bodyEnd int
		forms   bool
	}{
		{name: "no backslash", source: "{{a}}", close: 3, bodyEnd: 3, forms: true},
		{name: "single trailing backslash consumed", source: `{{a\}}`, close: 4, bodyEnd: 3, forms: true},
		{name: "single trailing backslash before brace run", source: `{{a\}}}`, close: 4, bodyEnd: 3, forms: true},
		{name: "empty after the consumed backslash", source: `{{\}}`, close: 3, bodyEnd: 2},
		{name: "trailing space after the consumed backslash", source: `{{a \}}`, close: 5, bodyEnd: 4},
		{name: "leading space with a consumed backslash", source: `{{ \}}`, close: 4, bodyEnd: 3},
		{name: "backslash before a literal space", source: `{{\ }}`, close: 4, bodyEnd: 4},
		{name: "pair hides the closer", source: `{{a\\}}`, close: -1, bodyEnd: -1},
		{name: "pair hides the closer of an empty body", source: `{{\\}}`, close: -1, bodyEnd: -1},
		{name: "three backslashes hide the closer", source: `{{a\\\}}`, close: -1, bodyEnd: -1},
		{name: "four backslashes hide the closer", source: `{{a\\\\}}`, close: -1, bodyEnd: -1},
		{name: "hidden closer consumes the whole brace run", source: `{{a\\}}}`, close: -1, bodyEnd: -1},
		{name: "hidden closer consumes a four brace run", source: `{{a\\}}}}`, close: -1, bodyEnd: -1},
		{name: "later closer after a hidden one", source: `{{a\\}}b}}`, close: 8, bodyEnd: 8, forms: true},
		{name: "consumed backslash after a hidden closer", source: `{{a\\}}b\}}`, close: 9, bodyEnd: 8, forms: true},
		{name: "two hidden closers", source: `{{a\\}}b\\}}`, close: -1, bodyEnd: -1},
		{name: "third closer after two hidden ones", source: `{{a\\}}b\\}}c}}`, close: 13, bodyEnd: 13, forms: true},
		{name: "reference is opaque before a consumed backslash", source: `{{a&#92;\}}`, close: 9, bodyEnd: 8, forms: true},
		{name: "reference does not join the hiding run", source: `{{a&#92;\\}}`, close: -1, bodyEnd: -1},
		{name: "backslash run mid body", source: `{{a\\b}}`, close: 6, bodyEnd: 6, forms: true},
		{name: "backslash run at the body start", source: `{{\\a}}`, close: 5, bodyEnd: 5, forms: true},
		{name: "zero width space before the consumed backslash", source: "{{a​\\}}", close: 7, bodyEnd: 6, forms: true},
		{name: "zero width space alone before the consumed backslash", source: "{{​\\}}", close: 6, bodyEnd: 5, forms: true},
		{name: "word rune after a consumed backslash closer", source: `{{a\}}b}}`, close: 4, bodyEnd: 3},
		{name: "hidden closer leaves no candidate whatever surrounds it", source: `x{{a\\}}y`, close: -1, bodyEnd: -1},
		{name: "hidden closer swallows a later opener", source: `{{a\\}} {{b}}`, close: 11, bodyEnd: 11, forms: true},
		{name: "reference is body content not a hiding run", source: `{{a&#92;}}`, close: 8, bodyEnd: 8, forms: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			offset := strings.Index(test.source, "{{")
			close, bodyEnd, ok, err := jiraMonospaceSpanEnd(context.Background(), test.source, 0, offset, len(test.source))
			if err != nil {
				t.Fatal(err)
			}
			if close != test.close || bodyEnd != test.bodyEnd || ok != test.forms {
				t.Fatalf("jiraMonospaceSpanEnd(%q) = (%d, %d, %t), want (%d, %d, %t)", test.source, close, bodyEnd, ok, test.close, test.bodyEnd, test.forms)
			}
		})
	}
}
