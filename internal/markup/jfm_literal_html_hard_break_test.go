package markup_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/timonwong/jiro/internal/markup"
)

// TestLiteralHTMLHardBreakRoundTrips pins #119 on the shape that broke the round
// trip: inline HTML jiro does not model stays literal, and the hard break inside
// it used to reach Jira as its authored `\` in front of an ordinary line break —
// a backslash Jira shows (ASF Jira 8.20.10 renders `a\` + newline as
// `<p>a\<br/>\nb</p>`, no forced newline), which came back as that backslash and
// a space. The break now goes out as Jira's forced newline, so the literal reads
// back as the document it was written from.
func TestLiteralHTMLHardBreakRoundTrips(t *testing.T) {
	t.Parallel()
	const input = "<mark>a\\\nb</mark>"
	const markup1 = "<mark>a\\\\\nb</mark>"
	const jfm = "\\<mark\\>a\\\nb\\</mark\\>"
	first, err := markup.FromJFM(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Markup != markup1 {
		t.Fatalf("FromJFM(%q) = %q, want %q", input, first.Markup, markup1)
	}
	want := []markup.ConversionWarning{{
		Line: 1, Column: 1,
		Construct: markup.ConstructHTML,
		Reason:    "unsupported or malformed inline HTML remains literal",
	}}
	if !reflect.DeepEqual(first.Warnings, want) {
		t.Fatalf("warnings = %#v, want %#v", first.Warnings, want)
	}
	back, err := markup.ToJFM(context.Background(), first.Markup)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Warnings) != 0 || back.Markdown != jfm {
		t.Fatalf("ToJFM(%q) = %q, %#v, want %q and no warnings", first.Markup, back.Markdown, back.Warnings, jfm)
	}
	again, err := markup.FromJFM(context.Background(), back.Markdown)
	if err != nil {
		t.Fatal(err)
	}
	if again.Markup != first.Markup {
		t.Fatalf("FromJFM(%q) = %q, want %q", back.Markdown, again.Markup, first.Markup)
	}
}
