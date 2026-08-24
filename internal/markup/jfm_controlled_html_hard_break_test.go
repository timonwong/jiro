package markup_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/timonwong/jiro/internal/markup"
)

// TestControlledHTMLKeepsTextBeforeHardBreak pins #109 on the shape that broke
// the round trip: ToJFM emits <font color=…> for {color:red}a\\b{color}, and the
// text before the break went missing on the way back. It pairs that with the
// paragraph-level break to state the invariant the fix rests on — the break maps
// the same way inside controlled HTML as outside it. The remaining controlled
// tags are covered by testdata/jfm/from_jfm/controlled_html_hard_break.txtar.
func TestControlledHTMLKeepsTextBeforeHardBreak(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name  string
		input string
		want  string
	}{
		{name: "font color", input: "<font color=\"red\">a\\\nb</font>", want: "{color:red}a\\\\\nb{color}"},
		{name: "paragraph level", input: "a\\\nb", want: "a\\\\\nb"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			result, err := markup.FromJFM(context.Background(), testCase.input)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Warnings) != 0 {
				t.Fatalf("FromJFM warnings = %#v, want none", result.Warnings)
			}
			if result.Markup != testCase.want {
				t.Fatalf("FromJFM(%q) = %q, want %q", testCase.input, result.Markup, testCase.want)
			}
		})
	}
}

// TestTableCellBreakStaysLiteralHTML pins the constraint the Jira renderer's
// hardBreakInline arm rests on. A GFM cell is one line and carries no hard
// break, and `<br>` is not controlled HTML in JFM, so no table cell can reach
// that arm; if `<br>` ever became a break, the row writer would owe the reader
// the line the break opens.
func TestTableCellBreakStaysLiteralHTML(t *testing.T) {
	t.Parallel()
	result, err := markup.FromJFM(context.Background(), "| h |\n| --- |\n| a<br>b |")
	if err != nil {
		t.Fatal(err)
	}
	if want := "||h||\n|a<br>b|"; result.Markup != want {
		t.Fatalf("FromJFM() = %q, want %q", result.Markup, want)
	}
	want := []markup.ConversionWarning{{
		Line: 3, Column: 4,
		Construct: markup.ConstructHTML,
		Reason:    "unsupported or malformed inline HTML remains literal",
	}}
	if !reflect.DeepEqual(result.Warnings, want) {
		t.Fatalf("warnings = %#v, want %#v", result.Warnings, want)
	}
}
