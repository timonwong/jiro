package markup

import (
	"context"
	"testing"
)

// TestJiraTableRowSplitsAroundLinksAndImages pins the cell boundaries the
// evidence fixtures in testdata/jfm/jira_evidence record. A row whose cells
// cannot be written as GFM converts to a `:::table` passthrough, which hides the
// split, so the counts are asserted here rather than through a golden.
func TestJiraTableRowSplitsAroundLinksAndImages(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		row  string
		want []string
	}{
		{name: "link with a piped target", row: `|[x|http://x]|c|`, want: []string{`[x|http://x]`, "c"}},
		{name: "link Jira cannot resolve", row: `|[x|y]|c|`, want: []string{`[x|y]`, "c"}},
		{name: "link with two pipes", row: `|[a|b|c]|d|`, want: []string{`[a|b|c]`, "d"}},
		{name: "link with a title", row: `|[x|http://x|title]|c|`, want: []string{`[x|http://x|title]`, "c"}},
		{name: "text after a link", row: `|[x|http://x]c|d|`, want: []string{`[x|http://x]c`, "d"}},
		{name: "link inside text", row: `|a [x|http://x] b|c|`, want: []string{`a [x|http://x] b`, "c"}},
		{name: "user link", row: `|[~user]|c|`, want: []string{"[~user]", "c"}},
		{name: "anchor link", row: `|[#anchor]|c|`, want: []string{"[#anchor]", "c"}},
		{name: "attachment link", row: `|[^file.txt]|c|`, want: []string{"[^file.txt]", "c"}},
		{name: "bracketed text", row: `|[x]|c|`, want: []string{"[x]", "c"}},
		{name: "unclosed bracket", row: `|[a|c|`, want: []string{"[a", "c"}},
		{name: "escaped bracket", row: `|\[x|y]|c|`, want: []string{`\[x`, "y]", "c"}},
		{name: "image with alt", row: `|!http://x/i.png|alt=alt!|c|`, want: []string{"!http://x/i.png|alt=alt!", "c"}},
		{name: "image with two attributes", row: `|!http://x/i.png|alt=alt, width=10!|c|`, want: []string{"!http://x/i.png|alt=alt, width=10!", "c"}},
		{name: "image whose alt holds a pipe", row: `|!http://x/i.png|alt=a|b!|c|`, want: []string{"!http://x/i.png|alt=a|b!", "c"}},
		{name: "image without attributes", row: `|!http://x/i.png!|c|`, want: []string{"!http://x/i.png!", "c"}},
		{name: "image shape Jira shows as text", row: `|!a!|b|`, want: []string{"!a!", "b"}},
		{name: "unclosed image", row: `|!a|b|c|`, want: []string{"!a", "b", "c"}},
		{name: "word rune after the closing bang", row: `|!a.png|b!x|c|`, want: []string{"!a.png", "b!x", "c"}},
		{name: "space after the opening bang", row: `|! a|b!|c|`, want: []string{"! a", "b!", "c"}},
		{name: "bangs inside words", row: `|a!b|c!d|`, want: []string{"a!b", "c!d"}},
		{name: "monospace span protects nothing", row: `|{{a|b}}|c|`, want: []string{"{{a", "b}}", "c"}},
		{name: "escaped separator", row: `|a\|b|c|`, want: []string{`a\|b`, "c"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			bounds, err := jiraTableCellBounds(context.Background(), test.row, 1, len(test.row)-1, "|")
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, len(bounds))
			for index, bound := range bounds {
				got[index] = test.row[bound.Start:bound.End]
			}
			if len(got) != len(test.want) {
				t.Fatalf("cells = %q, want %q", got, test.want)
			}
			for index := range got {
				if got[index] != test.want[index] {
					t.Fatalf("cells = %q, want %q", got, test.want)
				}
			}
		})
	}
}
