package markup

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"
)

// This file is the single Jira inline grammar shared by the Jira parser and the
// Jira renderer escapers (ADR-0018). Its rules are backed by probes against Jira
// Server 8.20.10 (hack/jira-render-evidence.py); do not add a rule without
// evidence. Every rule below has a checked-in render in
// testdata/jfm/jira_evidence, named after the topic it settles.

func jiraInlineStyle(delimiter byte) (inlineStyle, bool) {
	switch delimiter {
	case '*':
		return styleBold, true
	case '_':
		return styleItalic, true
	case '-':
		return styleStrike, true
	case '+':
		return styleInserted, true
	case '^':
		return styleSuper, true
	case '~':
		return styleSub, true
	default:
		return "", false
	}
}

// isJiraWordRune reports whether value blocks an Effect Delimiter from opening
// (when preceding it) or from closing (when following it). Jira counts every
// Unicode letter or digit, so CJK and accented text tokenize like ASCII words;
// `_`, `-` and other punctuation are boundaries rather than word characters.
func isJiraWordRune(value rune) bool {
	return unicode.IsLetter(value) || unicode.IsDigit(value)
}

// jiraWordRuneBefore reports whether a word rune ends at offset. Every caller's
// range begins at a line start or directly after a delimiter or structural
// character, so an offset at start has no preceding rune rather than an unknown
// one.
func jiraWordRuneBefore(source string, start, offset int) bool {
	previous, size := utf8.DecodeLastRuneInString(source[start:offset])
	return size != 0 && isJiraWordRune(previous)
}

// jiraWordRuneAfter reports whether a word rune begins at offset.
func jiraWordRuneAfter(source string, offset, end int) bool {
	next, size := utf8.DecodeRuneInString(source[offset:end])
	return size != 0 && isJiraWordRune(next)
}

// isJiraEffectSpace reports Jira's notion of whitespace next to an Effect
// Delimiter. Jira recognizes ASCII whitespace only: `*<U+00A0>x*` and
// `*<U+3000>x<U+3000>*` still render bold, so unicode.IsSpace would reject
// openers Jira accepts.
func isJiraEffectSpace(value rune) bool {
	switch value {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	default:
		return false
	}
}

// jiraEffectCanOpen reports whether the Effect Delimiter at source[offset] may
// open a Text Effect inside the inline run source[start:end]. Ranges are
// half-open.
func jiraEffectCanOpen(source string, start, offset, end int) bool {
	if offset+1 >= end {
		return false
	}
	if next, _ := utf8.DecodeRuneInString(source[offset+1 : end]); isJiraEffectSpace(next) {
		return false
	}
	return !jiraWordRuneBefore(source, start, offset)
}

// findJiraEffectClose scans source[start:end] for the Effect Delimiter closing
// an opener at start-1. The plain-text escaper passes the delimiters it has
// already decided to backslash-escape as ignored, because Jira no longer reads
// an escaped delimiter as a closer; the parser has no such set and passes nil.
func findJiraEffectClose(ctx context.Context, source string, start, end int, delimiter byte, ignored []bool) (int, error) {
	for index := start; index < end; index++ {
		if (index-start)&255 == 0 {
			if err := ctx.Err(); err != nil {
				return -1, err
			}
		}
		if source[index] == '\\' {
			index++
			continue
		}
		if source[index] != delimiter || index <= start {
			continue
		}
		if ignored != nil && ignored[index] {
			continue
		}
		if previous, _ := utf8.DecodeLastRuneInString(source[start:index]); isJiraEffectSpace(previous) {
			continue
		}
		if jiraWordRuneAfter(source, index+1, end) {
			continue
		}
		return index, nil
	}
	return -1, ctx.Err()
}

// jiraInlineContext selects the rule subset that applies to a scanned run. Jira
// keeps reinterpreting inline markup inside `{{...}}`, so the Monospace Span
// context is the plain-text context plus the characters that only a Monospace
// Span body can lose.
type jiraInlineContext uint8

const (
	jiraPlainTextContext jiraInlineContext = iota
	jiraMonospaceContext
)

type jiraHazardKind uint8

const (
	jiraHazardEffect jiraHazardKind = iota
	jiraHazardCitation
	jiraHazardLink
	jiraHazardAutolink
	jiraHazardEmoticon
	jiraHazardDash
	jiraHazardForcedNewline
	jiraHazardMacro
	jiraHazardCellSeparator
	jiraHazardEscape
	// The kinds below exist only inside a Monospace Span: Jira either consumes
	// the character or refuses the whole span over it.
	jiraHazardMonospaceClose
	jiraHazardBrace
	jiraHazardTrailingBackslash
	jiraHazardTab
	jiraHazardEdgeSpace
	jiraHazardZeroWidthSpace
)

// jiraHazardInPlainText is the plain-text subset of jiraHazardKind, kept as data
// so that both contexts run through one scanner.
var jiraHazardInPlainText = [...]bool{
	jiraHazardEffect:            true,
	jiraHazardCitation:          true,
	jiraHazardLink:              true,
	jiraHazardAutolink:          true,
	jiraHazardEmoticon:          true,
	jiraHazardDash:              true,
	jiraHazardForcedNewline:     true,
	jiraHazardMacro:             true,
	jiraHazardCellSeparator:     true,
	jiraHazardEscape:            true,
	jiraHazardMonospaceClose:    false,
	jiraHazardBrace:             false,
	jiraHazardTrailingBackslash: false,
	jiraHazardTab:               false,
	jiraHazardEdgeSpace:         false,
	jiraHazardZeroWidthSpace:    false,
}

// jiraInlineHazard is one construct Jira reinterprets in the scanned run.
type jiraInlineHazard struct {
	Kind  jiraHazardKind
	Style inlineStyle // set for jiraHazardEffect
	Start int
	End   int
	// TextChanges reports that Jira's visible text differs from the source
	// characters. A hazard that only refuses the Monospace Span leaves it false.
	TextChanges bool
}

// jiraEmoticonTokens are the tokens the renderer replaces with an icon. `(*y)`
// and `(*)` render the same yellow star, and `(flagoff)` is a distinct token
// rather than `(flag)` followed by text.
var jiraEmoticonTokens = []string{
	"(flagoff)", "(flag)", "(off)", "(on)",
	"(*r)", "(*g)", "(*b)", "(*y)", "(*)",
	"(y)", "(n)", "(i)", "(/)", "(x)", "(!)", "(?)", "(+)", "(-)",
	":(", ":)", ":P", ":D", ";)",
}

// jiraAutolinkSchemes are the bare-URL prefixes the renderer links. A bare email
// address and a scheme-less `www.` host stay text, so they are absent here.
var jiraAutolinkSchemes = []string{"http://", "https://", "ftp://", "mailto:"}

// jiraInlineHazards reports, in source order, every construct Jira would
// reinterpret in source[start:end]. inTableCell adds the cell-separator hazard
// for a run that a table row has already split.
func jiraInlineHazards(ctx context.Context, source string, start, end int, inlineContext jiraInlineContext, inTableCell bool) ([]jiraInlineHazard, error) {
	hazards := make([]jiraInlineHazard, 0)
	monospace := inlineContext == jiraMonospaceContext
	add := func(kind jiraHazardKind, style inlineStyle, hazardStart, hazardEnd int, textChanges bool) {
		if !monospace && !jiraHazardInPlainText[kind] {
			return
		}
		hazards = append(hazards, jiraInlineHazard{Kind: kind, Style: style, Start: hazardStart, End: hazardEnd, TextChanges: textChanges})
	}
	var failedEffectScans map[byte]int
	for index := start; index < end; {
		if (index-start)&255 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if _, referenceEnd := jiraCharacterReference(source, index, end); referenceEnd > 0 {
			index = referenceEnd
			continue
		}
		character := source[index]
		if character == ' ' {
			if index == start || index == end-1 {
				add(jiraHazardEdgeSpace, "", index, index+1, false)
			}
			index++
			continue
		}
		if character == '\t' {
			add(jiraHazardTab, "", index, index+1, false)
			index++
			continue
		}
		if character == '\\' {
			// Jira renders `\\` as a line break and refuses a span whose body is one,
			// but Jira-to-JFM conversion keeps decoding legacy backslash escapes.
			if index+1 < end && source[index+1] == '\\' {
				add(jiraHazardForcedNewline, "", index, index+2, true)
				index += 2
				continue
			}
			if index+1 < end && strings.ContainsRune(jiraEscapableCharacters, rune(source[index+1])) {
				add(jiraHazardEscape, "", index, index+2, true)
				index += 2
				continue
			}
			if index+1 == end {
				add(jiraHazardTrailingBackslash, "", index, index+1, true)
			}
			index++
			continue
		}
		if character == '}' && index+1 < end && source[index+1] == '}' {
			add(jiraHazardMonospaceClose, "", index, index+2, false)
			index += 2
			continue
		}
		if character == '{' || character == '}' {
			if macroEnd := jiraMacroEnd(source, index, end); macroEnd > 0 {
				add(jiraHazardMacro, "", index, macroEnd, true)
				index = macroEnd
				continue
			}
			add(jiraHazardBrace, "", index, index+1, true)
			index++
			continue
		}
		if character == '|' && inTableCell {
			add(jiraHazardCellSeparator, "", index, index+1, true)
			index++
			continue
		}
		// U+200B is a Monospace Span boundary rather than content, so one strictly
		// inside a body refuses the span; as the body's first or last rune it is
		// only the boundary Jira already expects and the span still forms.
		if strings.HasPrefix(source[index:end], "\u200b") {
			if index != start && index+len("\u200b") != end {
				add(jiraHazardZeroWidthSpace, "", index, index+len("\u200b"), false)
			}
			index += len("\u200b")
			continue
		}
		// Dash substitution runs before the effect rules: in `a -- b -- c` Jira
		// consumes all four hyphens as dashes rather than pairing the inner two
		// into a strikethrough.
		if character == '-' {
			if run := jiraDashRun(source, start, index, end); run > 0 {
				add(jiraHazardDash, "", index, index+run, true)
				index += run
				continue
			}
		}
		if token := jiraEmoticonAt(source, index, end); token > 0 {
			add(jiraHazardEmoticon, "", index, index+token, true)
			index += token
			continue
		}
		if strings.HasPrefix(source[index:end], "??") {
			if close, err := jiraCitationClose(ctx, source, start, index, end); err != nil {
				return nil, err
			} else if close > 0 {
				add(jiraHazardCitation, "", index, close+2, true)
				index += 2
				continue
			}
		}
		if character == '[' {
			if close, changes, err := jiraLinkEnd(ctx, source, index, end); err != nil {
				return nil, err
			} else if close > 0 {
				add(jiraHazardLink, "", index, close+1, changes)
				index = close + 1
				continue
			}
		}
		if scheme, autolinkEnd := jiraAutolinkExtent(source, start, index, end); autolinkEnd > 0 {
			add(jiraHazardAutolink, "", index, autolinkEnd, scheme == "mailto:")
			index = autolinkEnd
			continue
		}
		if style, ok := jiraInlineStyle(character); ok && jiraEffectCanOpen(source, start, index, end) {
			if failedFrom, known := failedEffectScans[character]; !known || index+1 < failedFrom {
				close, err := findJiraEffectClose(ctx, source, index+1, end, character, nil)
				if err != nil {
					return nil, err
				}
				if close < 0 {
					if failedEffectScans == nil {
						failedEffectScans = make(map[byte]int)
					}
					failedEffectScans[character] = index + 1
				} else {
					add(jiraHazardEffect, style, index, close+1, true)
				}
			}
		}
		_, size := utf8.DecodeRuneInString(source[index:end])
		index += size
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return hazards, nil
}

// jiraMonospaceSpanEnd reports the offset of the `}}` closing the Monospace Span
// that the `{{` at offset opens inside the inline run source[start:end]. A
// returned close of -1 means the run holds no `}}` at all, which lets callers
// memoize the failure for every later opener.
func jiraMonospaceSpanEnd(ctx context.Context, source string, start, offset, end int) (int, bool, error) {
	body := offset + 2
	close := -1
	// The first `}}` is the only closer candidate, and a backslash does not hide
	// it: Jira reads `{{a\}}b}}` as literal text rather than pairing the opener
	// with the second `}}`.
	for index := body; index+1 < end; index++ {
		if (index-body)&255 == 0 {
			if err := ctx.Err(); err != nil {
				return -1, false, err
			}
		}
		if source[index] == '}' && source[index+1] == '}' {
			close = index
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return -1, false, err
	}
	if close < 0 {
		return -1, false, nil
	}
	// Braces touching a word rune are literal text on either side, so `x{{a}}`,
	// `{{a}}y` and `中{{a}}文` all stay prose.
	if previous, size := utf8.DecodeLastRuneInString(source[start:offset]); size != 0 && isJiraWordRune(previous) {
		return close, false, nil
	}
	if next, size := utf8.DecodeRuneInString(source[close+2 : end]); size != 0 && isJiraWordRune(next) {
		return close, false, nil
	}
	// An empty body, a literal edge space, a tab or a newline all refuse the
	// span; a space written as a character reference does not, because Jira
	// resolves references after this rule has run.
	if close == body {
		return close, false, nil
	}
	if source[body] == ' ' || source[close-1] == ' ' {
		return close, false, nil
	}
	if strings.ContainsAny(source[body:close], "\t\n\r") {
		return close, false, nil
	}
	// U+200B reads as a boundary inside the body too: `{{a<U+200B>b}}` stays
	// literal text while `{{<U+200B>a}}` and `{{a<U+200B>}}` still render a span.
	for scan := body; scan < close; {
		offset := strings.Index(source[scan:close], "\u200b")
		if offset < 0 {
			break
		}
		at := scan + offset
		if at != body && at+len("\u200b") != close {
			return close, false, nil
		}
		scan = at + len("\u200b")
	}
	return close, true, nil
}

// jiraCharacterReference reports the decoded rune and the end offset of the
// character reference at start, or -1 when there is none. Every rule but one
// treats a reference as an opaque token, because Jira resolves references after
// that rule has run: `&#42;x&#42;` is not an effect pair and neither `&#32;a`,
// `&#x20;a` nor `&nbsp;a` has a leading space. The exception is a link body,
// where Jira decodes a numeric reference before looking for the target
// separator, so the decoded rune is reported for jiraLinkEnd. A named reference
// decodes to utf8.RuneError because no rule needs its value.
func jiraCharacterReference(source string, start, end int) (rune, int) {
	if start >= end || source[start] != '&' {
		return 0, -1
	}
	index := start + 1
	if index < end && source[index] == '#' {
		index++
		base := 10
		if index < end && (source[index] == 'x' || source[index] == 'X') {
			base, index = 16, index+1
		}
		digits, value := index, 0
		for index < end {
			digit := jiraDigitValue(source[index], base)
			if digit < 0 {
				break
			}
			if value <= utf8.MaxRune {
				value = value*base + digit
			}
			index++
		}
		if index == digits || index >= end || source[index] != ';' {
			return 0, -1
		}
		if value > utf8.MaxRune {
			return utf8.RuneError, index + 1
		}
		return rune(value), index + 1
	}
	for index < end && isASCIIAlphanumeric(source[index]) {
		index++
	}
	if index == start+1 || index >= end || source[index] != ';' {
		return 0, -1
	}
	return utf8.RuneError, index + 1
}

func isASCIIAlphanumeric(character byte) bool {
	return character >= '0' && character <= '9' ||
		character >= 'a' && character <= 'z' ||
		character >= 'A' && character <= 'Z'
}

func jiraDigitValue(character byte, base int) int {
	value := -1
	switch {
	case character >= '0' && character <= '9':
		value = int(character - '0')
	case character >= 'a' && character <= 'z':
		value = int(character-'a') + 10
	case character >= 'A' && character <= 'Z':
		value = int(character-'A') + 10
	}
	if value < 0 || value >= base {
		return -1
	}
	return value
}

// jiraMacroEnd reports the end of the `{name}` or `{}` macro at start. Jira
// resolves macros before the Monospace Span rules, so `{{{}x}}` renders as the
// span body `x`. An unknown name is still a macro, spaces and all: Jira lifts
// `{b c}` out of the paragraph too.
func jiraMacroEnd(source string, start, end int) int {
	if start >= end || source[start] != '{' {
		return -1
	}
	for index := start + 1; index < end; index++ {
		switch source[index] {
		case '}':
			return index + 1
		case '{', '\n', '\r':
			return -1
		}
	}
	return -1
}

// jiraDashRun reports the length of the `--` or `---` run at index that Jira
// replaces with an en or em dash. The substitution needs a literal space on both
// sides: `--flag`, `a--b`, `a --b` and `a --` all stay hyphens.
func jiraDashRun(source string, start, index, end int) int {
	if index == start || source[index-1] != ' ' {
		return 0
	}
	run := 0
	for index+run < end && source[index+run] == '-' {
		run++
	}
	if run != 2 && run != 3 {
		return 0
	}
	if index+run >= end || source[index+run] != ' ' {
		return 0
	}
	return run
}

// jiraEmoticonAt reports the length of the emoticon token at index. The gate is
// asymmetric: a following word rune suppresses the icon (`(y)foo`), a preceding
// one does not (`f(x)` still ends in an icon).
func jiraEmoticonAt(source string, index, end int) int {
	for _, token := range jiraEmoticonTokens {
		if !strings.HasPrefix(source[index:end], token) {
			continue
		}
		if next, size := utf8.DecodeRuneInString(source[index+len(token) : end]); size != 0 && isJiraWordRune(next) {
			return 0
		}
		return len(token)
	}
	return 0
}

// jiraCitationClose reports the offset of the `??` closing the citation opened at
// index, gated exactly like the single-character Effect Delimiters.
func jiraCitationClose(ctx context.Context, source string, start, index, end int) (int, error) {
	if next, size := utf8.DecodeRuneInString(source[index+2 : end]); size == 0 || isJiraEffectSpace(next) {
		return -1, nil
	}
	if previous, size := utf8.DecodeLastRuneInString(source[start:index]); size != 0 && isJiraWordRune(previous) {
		return -1, nil
	}
	for scan := index + 2; scan+1 < end; scan++ {
		if (scan-index)&255 == 0 {
			if err := ctx.Err(); err != nil {
				return -1, err
			}
		}
		if source[scan] != '?' || source[scan+1] != '?' {
			continue
		}
		if previous, _ := utf8.DecodeLastRuneInString(source[index+2 : scan]); isJiraEffectSpace(previous) {
			continue
		}
		if next, size := utf8.DecodeRuneInString(source[scan+2 : end]); size != 0 && isJiraWordRune(next) {
			continue
		}
		return scan, nil
	}
	return -1, ctx.Err()
}

// jiraLinkEnd reports the offset of the `]` closing the link opened at index and
// whether Jira's visible text differs from the source characters. A piped link
// always hides its target, because whether it renders depends on the Jira
// Instance rather than on the markup: an issue key, user, anchor or attachment
// target resolves where the probed instance shows an error span. A bracketed URL
// loses its brackets. An unpiped link to a page keeps them.
func jiraLinkEnd(ctx context.Context, source string, index, end int) (int, bool, error) {
	close := -1
	for scan := index + 1; scan < end; scan++ {
		if (scan-index)&255 == 0 {
			if err := ctx.Err(); err != nil {
				return -1, false, err
			}
		}
		if source[scan] == '\\' {
			scan++
			continue
		}
		if source[scan] == '\n' || source[scan] == '\r' {
			return -1, false, nil
		}
		if source[scan] == ']' {
			close = scan
			break
		}
	}
	if close < 0 {
		return -1, false, ctx.Err()
	}
	body := source[index+1 : close]
	for scan := 0; scan < len(body); {
		value, referenceEnd := jiraCharacterReference(body, scan, len(body))
		if referenceEnd > 0 {
			if value == '|' {
				return close, true, nil
			}
			scan = referenceEnd
			continue
		}
		if body[scan] == '|' {
			return close, true, nil
		}
		scan++
	}
	return close, jiraAutolinkScheme(body, 0, len(body)) != "", nil
}

// jiraAutolinkTerminators end a bare URL. Brackets and parentheses are absent on
// purpose: Jira swallows them into the URL (`http://example.com(a` links whole),
// and a bracketed URL is a link rather than an autolink.
const jiraAutolinkTerminators = " \t\n\r\"<>{}|`"

// jiraAutolinkTrailing is dropped from the end of a bare URL. `:` and `?` are
// absent because Jira keeps them: `http://example.com?` links with the question
// mark, `http://example.com!` without the exclamation mark.
const jiraAutolinkTrailing = ".,;!)"

// jiraAutolinkExtent reports the scheme and the end of the bare URL at index.
func jiraAutolinkExtent(source string, start, index, end int) (string, int) {
	if jiraWordRuneBefore(source, start, index) {
		return "", -1
	}
	scheme := jiraAutolinkScheme(source, index, end)
	if scheme == "" {
		return "", -1
	}
	scan := index + len(scheme)
	for scan < end && !strings.ContainsRune(jiraAutolinkTerminators, rune(source[scan])) {
		scan++
	}
	for scan > index+len(scheme) && strings.ContainsRune(jiraAutolinkTrailing, rune(source[scan-1])) {
		scan--
	}
	if scan == index+len(scheme) {
		return "", -1
	}
	return scheme, scan
}

func jiraAutolinkScheme(source string, index, end int) string {
	for _, scheme := range jiraAutolinkSchemes {
		if strings.HasPrefix(source[index:end], scheme) {
			return scheme
		}
	}
	return ""
}

const jiraEscapableCharacters = `\{}[]|!*_-+^~`
