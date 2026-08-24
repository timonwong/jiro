package markup

import (
	"context"
	"testing"
)

type jiraValueCase struct{ markup, value string }

// TestJiraDelimitedValueRoundTrips pins each delimited-value context to the
// renders in testdata/jfm/jira_evidence: what the decoder reads out of Jira's
// markup, and that the encoder beside it writes a value the same decoder gives
// back. The `&` cases are here rather than in a golden because Markdown resolves
// a character reference in its own text and destinations, so a JFM document
// cannot carry an authored one into either direction of a golden round trip.
func TestJiraDelimitedValueRoundTrips(t *testing.T) {
	t.Parallel()
	decodeLinkTarget := func(t *testing.T, value string) string {
		decoded, err := decodeJiraLinkTarget(context.Background(), value)
		if err != nil {
			t.Fatal(err)
		}
		return decoded
	}
	decodeMacroParameter := func(t *testing.T, value string) string {
		decoded, err := decodeJiraMacroParameterValue(context.Background(), value)
		if err != nil {
			t.Fatal(err)
		}
		return decoded
	}
	encodeWith := func(encode func(context.Context, string) (string, error)) func(*testing.T, string) string {
		return func(t *testing.T, value string) string {
			encoded, err := encode(context.Background(), value)
			if err != nil {
				t.Fatal(err)
			}
			return encoded
		}
	}
	for _, test := range []struct {
		name   string
		decode func(*testing.T, string) string
		encode func(*testing.T, string) string
		cases  []jiraValueCase
	}{
		{
			name:   "link target",
			decode: decodeLinkTarget,
			encode: encodeWith(encodeJiraLinkTarget),
			cases: []jiraValueCase{
				{markup: `http://x/a?b=1`, value: `http://x/a?b=1`},
				{markup: `http://x/a\?b`, value: `http://x/a?b`},
				{markup: `http://x/a\]b`, value: `http://x/a]b`},
				{markup: `http://x/a\\b`, value: `http://x/a\b`},
				{markup: `http://x/a\\\b`, value: `http://x/a\\b`},
				{markup: `http://x/a\\\\\\b`, value: `http://x/a\\\\\b`},
				{markup: `http://x/a&#124;b`, value: `http://x/a|b`},
				{markup: `http://x/a&#92;b`, value: `http://x/a\b`},
			},
		},
		{
			name:   "image parameter value",
			decode: func(_ *testing.T, value string) string { return decodeJiraImageValue(value) },
			encode: encodeWith(encodeJiraImageParameterValue),
			cases: []jiraValueCase{
				{markup: `a\=b`, value: `a\=b`},
				{markup: `a=b=c`, value: `a=b=c`},
				{markup: `a\\b`, value: `a\\b`},
				{markup: `a&#44;b`, value: `a,b`},
				{markup: `a&#33;b`, value: `a!b`},
				{markup: `a&#124;b`, value: `a|b`},
				{markup: `&#32;a`, value: ` a`},
			},
		},
		{
			name:   "image source",
			decode: func(_ *testing.T, value string) string { return decodeJiraImageValue(value) },
			encode: encodeWith(encodeJiraImageSource),
			cases: []jiraValueCase{
				{markup: `http://x/i\!b.png`, value: `http://x/i\!b.png`},
				{markup: `http://x/i\\b.png`, value: `http://x/i\\b.png`},
				{markup: `http://x/i&#92;b.png`, value: `http://x/i\b.png`},
				{markup: `http://x/i&#124;b.png`, value: `http://x/i|b.png`},
			},
		},
		{
			name:   "macro parameter",
			decode: decodeMacroParameter,
			encode: encodeWith(encodeJiraMacroParameterValue),
			cases: []jiraValueCase{
				{markup: `a\=b`, value: `a=b`},
				{markup: `a\,b`, value: `a,b`},
				{markup: `a\}b`, value: `a}b`},
				{markup: `a\\b`, value: `a\b`},
				{markup: `a\:b`, value: `a:b`},
				{markup: `a\`, value: `a`},
				{markup: `a&#61;b`, value: `a=b`},
				{markup: `a&#124;b`, value: `a|b`},
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			for _, value := range test.cases {
				if got := test.decode(t, value.markup); got != value.value {
					t.Errorf("decode(%q) = %q, want %q", value.markup, got, value.value)
				}
				encoded := test.encode(t, value.value)
				if got := test.decode(t, encoded); got != value.value {
					t.Errorf("decode(encode(%q)) = %q via %q", value.value, got, encoded)
				}
			}
			// An authored character reference has to survive as the text it is
			// rather than as the character a reader would resolve it to.
			for _, value := range []string{`a&#124;b`, `a&amp;b`, `a & b`} {
				encoded := test.encode(t, value)
				if got := test.decode(t, encoded); got != value {
					t.Errorf("decode(encode(%q)) = %q via %q", value, got, encoded)
				}
			}
		})
	}
}
