package markup

import (
	"context"
	"strings"
)

// This file is the Jira delimited-value grammar: the rules for the values Jira
// reads between its own delimiters -- a link target, an image source, an image
// parameter value and a macro parameter value. Jira consumes a backslash
// differently in each of them, so one shared decoder can only be right in one
// context by accident; the Jira parser reads a value with the decoder of its
// context and every Jira renderer writes one with the encoder beside it
// (ADR-0018). The rules are backed by probes against Jira Server 8.20.10
// (hack/jira-render-evidence.py), each with a checked-in render in
// testdata/jfm/jira_evidence.
//
// Every context resolves character references, because Jira passes them into
// the HTML undecoded and the character the reader sees is the value. That also
// makes a reference the only encoding able to carry a separator into a value,
// since Jira splits these values on the raw character, and it is why every
// encoder rewrites an authored `&` that a reader would decode.
//
// A link title is the one value that resolves nothing at all: Jira HTML-escapes
// it into the `title` attribute instead of passing it through, so a reference
// arrives as its own characters and no encoding can carry a delimiter into it.
// decodeJiraLinkTitle and spellJiraLinkTitle below hold what is left of a
// grammar there.

// jiraValueEncoding is what one context does to each byte of a value: a
// replacement per byte, plus the replacement a leading space needs where the
// context refuses one.
type jiraValueEncoding struct {
	replacements [256]string
	leadingSpace string
}

func jiraValueEncodingOf(leadingSpace string, replacements map[byte]string) jiraValueEncoding {
	encoding := jiraValueEncoding{leadingSpace: leadingSpace}
	for character, replacement := range replacements {
		encoding.replacements[character] = replacement
	}
	return encoding
}

// jiraLinkTargetEncoding writes a link target. `\]` and `\[` survive the run
// rule below as the bare bracket, but a backslash cannot be doubled into
// itself, because a run of two loses one and leaves one rather than two.
var jiraLinkTargetEncoding = jiraValueEncodingOf("", map[byte]string{
	'|':  "&#124;",
	'\\': "&#92;",
	']':  `\]`,
	'[':  `\[`,
})

// jiraImageSourceEncoding writes an image source. Jira consumes no backslash
// here, so one would survive as itself, but what a backslash run does to the
// closing `!` and to the source separator depends on where it stands: a run
// before either is read as a forced newline or leaves the construct without a
// closer. Encoding every backslash keeps the rule free of that position test.
var jiraImageSourceEncoding = jiraValueEncodingOf("", map[byte]string{
	'|':  "&#124;",
	'!':  "&#33;",
	'\\': "&#92;",
})

// jiraImageParameterEncoding writes an image parameter value. `=` is absent:
// only the first `=` of a parameter separates the name, so a later one is part
// of the value and escaping it would put the backslash in the alt text.
var jiraImageParameterEncoding = jiraValueEncodingOf("&#32;", map[byte]string{
	',':  "&#44;",
	'!':  "&#33;",
	'|':  "&#124;",
	'\\': "&#92;",
})

// jiraMacroParameterEncoding writes a macro parameter value such as a code
// block title. This is the one context where a backslash escape reaches the
// value intact, so only the separator Jira splits on unconditionally needs a
// character reference.
var jiraMacroParameterEncoding = jiraValueEncodingOf("", map[byte]string{
	'=':  `\=`,
	',':  `\,`,
	'}':  `\}`,
	'\\': `\\`,
	'|':  "&#124;",
})

func encodeJiraLinkTarget(ctx context.Context, value string) string {
	return encodeJiraDelimitedValue(ctx, value, &jiraLinkTargetEncoding)
}

func encodeJiraImageSource(ctx context.Context, value string) string {
	return encodeJiraDelimitedValue(ctx, value, &jiraImageSourceEncoding)
}

func encodeJiraImageParameterValue(ctx context.Context, value string) string {
	return encodeJiraDelimitedValue(ctx, value, &jiraImageParameterEncoding)
}

func encodeJiraMacroParameterValue(ctx context.Context, value string) string {
	return encodeJiraDelimitedValue(ctx, value, &jiraMacroParameterEncoding)
}

// encodeJiraDelimitedValue writes value so that Jira's reader of that context
// yields it back. Only ASCII carries a rule, so the scan runs over bytes and
// copies the stretches between two of them whole.
func encodeJiraDelimitedValue(ctx context.Context, value string, encoding *jiraValueEncoding) string {
	var result strings.Builder
	pending := 0
	for offset := 0; offset < len(value); offset++ {
		sampleConversionCancel(ctx, offset)
		replacement := encoding.replacements[value[offset]]
		switch {
		case value[offset] == '&':
			// Only a `&` that a reader decodes back into another character has
			// to move out of the way; every other one stays readable.
			if !startsCharacterReference(value, offset, len(value)) {
				continue
			}
			replacement = "&#38;"
		case offset == 0 && value[offset] == ' ' && encoding.leadingSpace != "":
			replacement = encoding.leadingSpace
		case replacement == "":
			continue
		}
		if pending < offset {
			result.WriteString(value[pending:offset])
		}
		result.WriteString(replacement)
		pending = offset + 1
	}
	// A value Jira already reads as itself is the common case and needs no copy.
	if pending == 0 {
		return value
	}
	result.WriteString(value[pending:])
	return result.String()
}

// decodeJiraLinkTarget reads the value Jira links to. Jira drops exactly one
// backslash from each run and shows the rest, whatever character follows the
// run: `\?` is `?`, `\\b` is `\b`, and six backslashes are five.
func decodeJiraLinkTarget(ctx context.Context, value string) string {
	if !strings.Contains(value, `\`) {
		return decodeJiraEntities(value)
	}
	var result strings.Builder
	result.Grow(len(value))
	for index := 0; index < len(value); {
		sampleConversionCancel(ctx, index)
		if value[index] != '\\' {
			result.WriteByte(value[index])
			index++
			continue
		}
		run := 1
		for index+run < len(value) && value[index+run] == '\\' {
			run++
		}
		result.WriteString(value[index+1 : index+run])
		index += run
	}
	return decodeJiraEntities(result.String())
}

// decodeJiraLinkTitle reads the third part of a link body, which Jira shows as
// the link's title. Nothing in it is decoded -- `t\|u`, `t\]u` and `t&#124;u`
// each keep every character they were written with -- and the only rule left is
// the ASCII space and tab Jira trims off both edges after the split. A newline
// reads as the space that joins the two lines, which is what every other inline
// run does with one; Jira itself never reads a title across a line, because a
// bracket body that spans one is not a link at all.
func decodeJiraLinkTitle(value string) string {
	if strings.ContainsAny(value, "\r\n") {
		value = jiraLinkTitleLineBreaks.Replace(value)
	}
	return strings.Trim(value, " \t")
}

// jiraLinkTitleSpelling is how one title is written between the last `|` and the
// closing `]`, together with the parts of it the spelling could not carry. It is
// the single answer to every question the renderer, its diagnostics and the
// verification key ask about one title: Text is what goes out, readBack is what
// Jira reads out of it again, and the flags are what the reader lost. Every one
// of them is a rule the probes pin: a raw `]` ends the link wherever it stands
// and a backslash in front of it stays visible in the title, so a title holding
// one has no Jira spelling at all; a body whose last character is a backslash
// refuses the whole link, which one trailing space undoes because the trim
// removes it again; and a link body is one line.
type jiraLinkTitleSpelling struct {
	Text                  string
	LineBreakFlattened    bool
	EdgeWhitespaceTrimmed bool
	WhitespaceDropped     bool
	BracketDropped        bool
}

func spellJiraLinkTitle(value string) jiraLinkTitleSpelling {
	spelling := jiraLinkTitleSpelling{Text: value}
	if strings.ContainsAny(spelling.Text, "\r\n") {
		spelling.Text = jiraLinkTitleLineBreaks.Replace(spelling.Text)
		spelling.LineBreakFlattened = true
	}
	if trimmed := strings.Trim(spelling.Text, " \t"); trimmed != spelling.Text {
		spelling.Text, spelling.EdgeWhitespaceTrimmed = trimmed, true
	}
	// A dropped title takes the smaller losses with it: they are no longer what
	// the reader is missing. A title of nothing but whitespace is one of them,
	// because the trim leaves Jira no title to show at all.
	if strings.Contains(spelling.Text, "]") {
		return jiraLinkTitleSpelling{BracketDropped: true}
	}
	if spelling.Text == "" && value != "" {
		return jiraLinkTitleSpelling{WhitespaceDropped: true}
	}
	if strings.HasSuffix(spelling.Text, `\`) {
		spelling.Text += " "
	}
	return spelling
}

var jiraLinkTitleLineBreaks = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ")

// readBack reports the title Jira reads back out of this spelling, which is the
// title the rendered link actually carries.
func (spelling jiraLinkTitleSpelling) readBack() string {
	return decodeJiraLinkTitle(spelling.Text)
}

// decodeJiraImageValue reads an image source or an image parameter value, where
// Jira consumes no backslash: `!http://x/i\!b.png!` keeps the backslash in the
// source it protected, and `alt=a\=b` is the alt text `a\=b`.
func decodeJiraImageValue(value string) string {
	return decodeJiraEntities(value)
}

// decodeJiraMacroParameterValue reads a macro parameter value. Jira consumes
// the backslash of every `\X` pair here, whatever X is, so `a\=b` is `a=b` and
// `a\\b` is `a\b`. A trailing lone backslash escapes nothing and Jira shows
// nothing for it, which is what `{panel:title=a\|b}` leaves behind once the
// separator has split the parameter.
func decodeJiraMacroParameterValue(ctx context.Context, value string) string {
	if !strings.Contains(value, `\`) {
		return decodeJiraEntities(value)
	}
	var result strings.Builder
	result.Grow(len(value))
	for index := 0; index < len(value); {
		sampleConversionCancel(ctx, index)
		if value[index] == '\\' {
			if index+1 < len(value) {
				result.WriteByte(value[index+1])
			}
			index += 2
			continue
		}
		result.WriteByte(value[index])
		index++
	}
	return decodeJiraEntities(result.String())
}

// jiraUnprotectedSplit reports the offset in value of the first separator at or
// after start, or -1. Every split around a delimited value is this one: the parts
// of a bracket body and of a macro parameter list, the image source and its
// parameter list, and the `=` inside one parameter. A backslash protects none of
// them, so `[x|http://x/a\|b]` links to `http://x/a` under the title `b` and
// `alt=a\,b` is the alt text `a\`.
func jiraUnprotectedSplit(value string, start int, separator byte) int {
	offset := strings.IndexByte(value[start:], separator)
	if offset < 0 {
		return -1
	}
	return start + offset
}

// jiraValueEndsInBackslash reports the rule that refuses a construct outright: a
// `[...]` link body or a `!...!` image body whose last character is a backslash
// is not a link or an image, and Jira shows the markup instead.
func jiraValueEndsInBackslash(value string) bool {
	return strings.HasSuffix(value, `\`)
}

// jiraImageParameterValueRefused reports Jira's other image refusal: a parameter
// value that starts with a space leaves the whole `!...!` as text, while a space
// before the parameter name is read away.
func jiraImageParameterValueRefused(value string) bool {
	return strings.HasPrefix(value, " ")
}
