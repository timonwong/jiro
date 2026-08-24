package markup

import (
	"context"
	"reflect"
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
		{name: "separator behind an even backslash run", row: `|a\\|b|c|`, want: []string{`a\\|b`, "c"}},
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

// TestJiraTableRowWritesCellsThatReadBackAsCells pins the row assembly the
// per-cell verification cannot reach: an empty value and a value ending in a
// backslash are both harmless on their own and meet the delimiter only once the
// row is joined, so the row jiro writes is re-read here to prove every cell is
// still the one it was written as.
func TestJiraTableRowWritesCellsThatReadBackAsCells(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		cells []string
		want  string
		back  []string
	}{
		{name: "empty cell", cells: []string{"a", ""}, want: "|a| |", back: []string{"a", " "}},
		{name: "empty cell between two", cells: []string{"a", "", "b"}, want: "|a| |b|", back: []string{"a", " ", "b"}},
		{name: "cell ending in a backslash", cells: []string{`a\`, "b"}, want: "|a&#92;|b|", back: []string{"a&#92;", "b"}},
		{name: "last cell ending in a backslash", cells: []string{"a", `b\`}, want: "|a|b&#92;|", back: []string{"a", "b&#92;"}},
		{name: "cell that is one backslash", cells: []string{`\`, "b"}, want: "|&#92;|b|", back: []string{"&#92;", "b"}},
		{name: "cell holding a delimiter", cells: []string{"a|b", "c"}, want: `|a\|b|c|`, back: []string{`a\|b`, "c"}},
		{name: "cell ending in a delimiter", cells: []string{"a|", "c"}, want: `|a\||c|`, back: []string{`a\|`, "c"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cells := make([]tableCell, 0, len(test.cells))
			for _, value := range test.cells {
				inlines := make([]semanticInline, 0, 1)
				if value != "" {
					inlines = append(inlines, textInline{Text: value})
				}
				cells = append(cells, tableCell{Inlines: inlines})
			}
			state := &jiraRenderState{diagnostics: make([]conversionDiagnostic, 0)}
			row, err := renderJiraTableRow(context.Background(), state, cells, "|")
			if err != nil {
				t.Fatal(err)
			}
			if row != test.want {
				t.Fatalf("row = %q, want %q", row, test.want)
			}
			// The delimiter rewrite happens inside the verified render, so a
			// value it changed still has to read back as the run it was written
			// from; a diagnostic here would mean the harness fell back instead.
			if len(state.diagnostics) != 0 {
				t.Fatalf("diagnostics = %#v, want none", state.diagnostics)
			}
			parsed, _, _, _, err := parseJiraTableRow(context.Background(), row, sourceSpan{Start: 0, End: len(row)}, true)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, len(parsed))
			for index, cell := range parsed {
				got[index] = row[cell.Span.Start:cell.Span.End]
			}
			if !reflect.DeepEqual(got, test.back) {
				t.Fatalf("cells read back = %q, want %q", got, test.back)
			}
		})
	}
}
