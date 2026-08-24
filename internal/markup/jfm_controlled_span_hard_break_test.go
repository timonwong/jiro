package markup_test

import (
	"context"
	"testing"

	"github.com/timonwong/jiro/internal/markup"
)

// TestControlledSpanKeepsTextBeforeHardBreak covers #109: a hard break inside a
// controlled span (<font color=…>, <ins>, <sup>, <sub>) must carry the text that
// precedes it, exactly as the same break does at paragraph level. ToJFM emits
// such spans for {color:red}a\\b{color}, so dropping the text broke the round
// trip.
func TestControlledSpanKeepsTextBeforeHardBreak(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name  string
		input string
		want  string
	}{
		{name: "font color", input: "<font color=\"red\">a\\\nb</font>", want: "{color:red}a\\\\\nb{color}"},
		{name: "inserted", input: "<ins>a\\\nb</ins>", want: "+a\\\\\nb+"},
		{name: "paragraph level", input: "a\\\nb", want: "a\\\\\nb"},
	} {
		testCase := testCase
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
