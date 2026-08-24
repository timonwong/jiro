package markup_test

import (
	"context"
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
