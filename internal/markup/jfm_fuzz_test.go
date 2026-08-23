package markup_test

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/timonwong/jiro/internal/markup"
)

func FuzzToJFMNeverPanicsAndReturnsValidUTF8(f *testing.F) {
	for _, seed := range [][]byte{nil, []byte("h1. Release"), []byte("{panel}\n* item\n{panel}"), {0xff, 0xfe, 'x'}} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		result, err := markup.ToJFM(context.Background(), string(input))
		if err != nil {
			t.Fatal(err)
		}
		if !utf8.ValidString(result.Markdown) {
			t.Fatalf("invalid UTF-8 output %q", result.Markdown)
		}
	})
}

func FuzzFromJFMNeverPanicsAndReturnsValidUTF8(f *testing.F) {
	for _, seed := range [][]byte{nil, []byte("# Release"), []byte(":::panel\n- item\n:::"), {0xff, 0xfe, 'x'}} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		result, err := markup.FromJFM(context.Background(), string(input))
		if err != nil {
			t.Fatal(err)
		}
		if !utf8.ValidString(result.Markup) {
			t.Fatalf("invalid UTF-8 output %q", result.Markup)
		}
	})
}

// FuzzFromJFMEmitsZeroWidthSpaceOnlyAsASpanSeparator pins the one place the
// Jira renderer is allowed to invent a character: the U+200B that lets a
// Monospace Span form next to a word rune (ADR-0018). Anywhere else a U+200B
// would be invisible content that Jira-to-JFM conversion cannot tell from a
// separator, so it would silently change the document. Inputs carrying their
// own U+200B are out of scope because there the character is content the
// renderer is passing through, not one it created.
func FuzzFromJFMEmitsZeroWidthSpaceOnlyAsASpanSeparator(f *testing.F) {
	for _, seed := range []string{
		"a`b`c", "中`代码`文", "`x`", "x`a`", "`a`y", "a`b`c`d`e", "**a`b`**c",
		"`​`", "`a​b`", "a`{{x}}`b", "1`2`3", "_`a`_b", "[a`b`](u)c",
		"| h |\n| --- |\n| a`b`c |", "# a`b`c", "- a`b`c", "> a`b`c",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		const zeroWidthSpace = "​"
		if strings.Contains(input, zeroWidthSpace) {
			return
		}
		result, err := markup.FromJFM(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		for offset := 0; ; {
			index := strings.Index(result.Markup[offset:], zeroWidthSpace)
			if index < 0 {
				return
			}
			at := offset + index
			after := at + len(zeroWidthSpace)
			separator := strings.HasPrefix(result.Markup[after:], "{{") || strings.HasSuffix(result.Markup[:at], "}}")
			if !separator {
				t.Fatalf("U+200B at byte %d of %q is not a Monospace Span separator (input %q)", at, result.Markup, input)
			}
			offset = after
		}
	})
}
