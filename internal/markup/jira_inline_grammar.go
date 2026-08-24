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

// jiraEffectDelimiters pairs each Effect Delimiter with the Text Effect it
// carries. Both directions are read from this one table so that a delimiter
// cannot mean one thing to the Jira parser and another to the Jira renderer.
var jiraEffectDelimiters = [...]struct {
	Delimiter byte
	Style     inlineStyle
}{
	{'*', styleBold},
	{'_', styleItalic},
	{'-', styleStrike},
	{'+', styleInserted},
	{'^', styleSuper},
	{'~', styleSub},
}

var jiraInlineStyles = func() (styles [256]inlineStyle) {
	for _, entry := range jiraEffectDelimiters {
		styles[entry.Delimiter] = entry.Style
	}
	return styles
}()

func jiraInlineStyle(delimiter byte) (inlineStyle, bool) {
	style := jiraInlineStyles[delimiter]
	return style, style != ""
}

// jiraEffectDelimiter reports the Effect Delimiter carrying style. A Text Effect
// Jira writes as a macro rather than as a delimiter pair, such as a color, has
// none.
func jiraEffectDelimiter(style inlineStyle) (byte, bool) {
	for _, entry := range jiraEffectDelimiters {
		if entry.Style == style {
			return entry.Delimiter, true
		}
	}
	return 0, false
}

// jiraPlainTextByteClass names what one byte can do to plain text on Jira. The
// classes are the whole plain-text escaping vocabulary: the renderer's escaper
// reads them to decide what to write, and its verification reads them to decide
// whether a rendered run can differ from the text it came from at all.
type jiraPlainTextByteClass uint8

const (
	// jiraPlainTextByteLiteral is a byte Jira shows as itself.
	jiraPlainTextByteLiteral jiraPlainTextByteClass = iota
	// jiraPlainTextByteStructural is a byte Jira reads as structure wherever it
	// stands, so plain text always backslash-escapes it (ADR-0016).
	jiraPlainTextByteStructural
	// jiraPlainTextByteDelimiter is a byte Jira reads as markup only where it
	// pairs, so plain text escapes it only where the grammar reads a complete
	// pair.
	jiraPlainTextByteDelimiter
	// jiraPlainTextByteBackslash is the byte that starts Jira's own escapes, so
	// plain text has to stop it from doing so without hiding it.
	jiraPlainTextByteBackslash
)

// jiraPlainTextStructuralCharacters are the characters plain text escapes
// outside any grammar rule, as legacy safety escaping (ADR-0016).
const jiraPlainTextStructuralCharacters = "{}[]!|#"

// jiraPlainTextByteClasses classifies every byte plain text cannot pass through
// unexamined. Dash characters are absent on purpose: a Jira dash in prose is
// Jira-flavored semantics jiro keeps, so plain text never escapes one and
// reading a run back over it would be pure cost. Emoticon characters are absent
// for a different reason: an emoticon token is neutralized as a whole token
// rather than byte by byte (ADR-0019), which escapeTextForJiraText decides on
// the same gate the Jira parser reads.
var jiraPlainTextByteClasses = func() (classes [256]jiraPlainTextByteClass) {
	for _, character := range jiraPlainTextStructuralCharacters {
		classes[character] = jiraPlainTextByteStructural
	}
	for _, entry := range jiraEffectDelimiters {
		classes[entry.Delimiter] = jiraPlainTextByteDelimiter
	}
	// A lone `?` is never markup; only a complete `??...??` is a citation.
	classes['?'] = jiraPlainTextByteDelimiter
	classes['\\'] = jiraPlainTextByteBackslash
	return classes
}()

// jiraPlainTextHazardBytes are the bytes a rendered run must be re-parsed over,
// which is every byte plain text may write differently plus the `&` that begins
// the escaper's two character-reference protections.
var jiraPlainTextHazardBytes = func() string {
	hazards := []byte{'&'}
	for character := 0; character < 256; character++ {
		if jiraPlainTextByteClasses[character] != jiraPlainTextByteLiteral {
			hazards = append(hazards, byte(character))
		}
	}
	return string(hazards)
}()

// isJiraEscapable reports whether Jira consumes a backslash written before
// character, which is what makes an authored backslash before one of them
// unwritable as itself.
func isJiraEscapable(character byte) bool {
	return strings.IndexByte(jiraEscapableCharacters, character) >= 0
}

// isJiraForcedNewlineSeparator reports the bytes that end the token a backslash
// run belongs to. Only ASCII whitespace separates tokens: `a\\b<U+00A0>c\\d`
// breaks once while `a\\b c\\d` breaks twice, and `.`, `,`, `-` and `*` end no
// token at all.
func isJiraForcedNewlineSeparator(character byte) bool {
	switch character {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	default:
		return false
	}
}

// jiraNoForcedNewlineDomain is the line domain of an inline run Jira reads
// without the forced-newline rule. A link's visible text is such a run: Jira
// renders `[a\\b|http://x]` with both backslashes visible and no break, while
// the same pair in the paragraph around it breaks.
const jiraNoForcedNewlineDomain = -1

// jiraLineDomain is the line context one Jira inline run is read in. It carries
// the two facts the forced-newline rule needs beyond the scanned range.
type jiraLineDomain struct {
	// End is where the scan for the token's last backslash run stops: the end
	// of a physical line, or of one table cell, since `|` separates no token.
	// jiraNoForcedNewlineDomain means Jira reads the run without the rule.
	End int
	// Unbreakable names the JFM construct whose canonical form cannot carry a
	// hard break, or "" when it can. Jira renders the forced newline there all
	// the same, so a run inside one keeps the backslashes as characters and the
	// conversion warns, rather than writing a break that would read back as
	// something Jira never showed.
	Unbreakable string
}

// jiraUnbreakableConstructNames names each construct whose JFM form cannot
// carry a hard break, for the warning that reports one.
var jiraUnbreakableConstructNames = map[string]string{
	ConstructHeading: "heading",
}

// jiraBackslashRunEnd reports the end of the maximal run of backslashes
// beginning at runStart, bounded by the end of the scanned range. No caller's
// range ever cuts a run, because a range always ends on a delimiter, a brace,
// a bracket, a cell separator or a line end.
func jiraBackslashRunEnd(source string, runStart, end int) int {
	runEnd := runStart
	for runEnd < end && source[runEnd] == '\\' {
		runEnd++
	}
	return runEnd
}

// jiraForcedNewlineRun reports whether the backslash run source[runStart:runEnd]
// is the one Jira renders as a forced newline. Exactly one run per
// whitespace-separated token breaks -- the last one, and only when it is two
// backslashes long -- so `ab\\cd\\ef` shows the first pair and breaks on the
// second. Every other backslash of a run of two or more is a character Jira
// shows, and none of them escapes what follows.
//
// lineEnd is the end of the line Jira reads the run in: a physical line, or one
// table cell, since `|` separates no token and both cells of `|a\\b|c\\d|`
// break. It is deliberately not the end of the scanned inline range, because
// Jira decides the break on the raw line and looks straight through a Text
// Effect closer: the pair in `*x\\y*-z\\w` stays literal inside the bold
// because a later backslash stands in the same token.
func jiraForcedNewlineRun(source string, runStart, runEnd, lineEnd int) bool {
	return jiraForcedNewlineRunFrom(source, runStart, runEnd, runEnd, lineEnd)
}

// jiraForcedNewlineRunFrom is jiraForcedNewlineRun with an explicit point for
// the rest-of-token scan to resume at. Only the emoticon escape needs one: the
// backslash it consumes is gone from the token before the break is decided, so
// the scan has to start past it and past the token it protected, which is why
// `\\\:)` breaks while `\\:)` shows one backslash.
func jiraForcedNewlineRunFrom(source string, runStart, runEnd, scanFrom, lineEnd int) bool {
	if runEnd-runStart != 2 || lineEnd == jiraNoForcedNewlineDomain {
		return false
	}
	for index := scanFrom; index < lineEnd; {
		if isJiraForcedNewlineSeparator(source[index]) {
			return true
		}
		if source[index] == '\\' {
			// Only a backslash Jira keeps blocks the break. A lone one before an
			// escapable character is consumed as an escape and never reaches the
			// decision, which is why `a\\b\-c` breaks while `a\\b\c` and the
			// trailing one of `a\\b\` stay literal, and why the `\}` of
			// `{{a\\}}b\}}` leaves the pair inside the span breaking.
			escape := jiraBackslashRunEnd(source, index, lineEnd)
			if escape-index != 1 || escape >= lineEnd || !isJiraEscapable(source[escape]) {
				return false
			}
			index = escape + 1
			continue
		}
		index++
	}
	return true
}

// jiraBackslashPrecedes reports whether a backslash byte stands directly before
// offset in the raw source. Such a character never acts as markup: it opens no
// Text Effect, closes none, and starts neither a Monospace Span nor a link. The
// lookbehind is the whole rule, which is what makes an even run behave like an
// odd one -- `*ab\\\\*` finds no closer -- and it reads past the start of the
// scanned range, because Jira reads the raw line rather than the range.
func jiraBackslashPrecedes(source string, offset int) bool {
	return offset > 0 && source[offset-1] == '\\'
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

// jiraEffectToken is one Effect Delimiter occurrence: either the bare character
// or its brace form `{X}`, which Jira accepts for every delimiter and which
// waives the word-rune gate on the token's outer side.
type jiraEffectToken struct {
	Style     inlineStyle
	Delimiter byte
	Start     int
	End       int
	Brace     bool
}

// jiraEffectOpener reports the Effect Delimiter token opening a Text Effect at
// offset inside the inline run source[start:end]. scanned is where a caller
// resumes when the token opens nothing: a recognized brace form is passed over
// whole, so in `a{*}*b*{*}c` the `*` behind the braces opens rather than the one
// inside them, while an unrecognized `{*}` leaves its delimiter available and
// `a{*} b {*}c` pairs the bare `*` characters across the braces.
func jiraEffectOpener(source string, start, offset, end int) (jiraEffectToken, bool, int) {
	if jiraBackslashPrecedes(source, offset) {
		return jiraEffectToken{}, false, offset
	}
	if source[offset] == '{' && offset+2 < end && source[offset+2] == '}' {
		if style, ok := jiraInlineStyle(source[offset+1]); ok && jiraEffectContentFollows(source, offset+3, end) {
			if source[offset+3] == source[offset+1] {
				return jiraEffectToken{}, false, offset + 3
			}
			return jiraEffectToken{Style: style, Delimiter: source[offset+1], Start: offset, End: offset + 3, Brace: true}, true, offset + 3
		}
	}
	style, ok := jiraInlineStyle(source[offset])
	if !ok || !jiraEffectContentFollows(source, offset+1, end) {
		return jiraEffectToken{}, false, offset
	}
	// An effect's content may not begin with its own delimiter: Jira reads
	// `**x**` as a literal `*` around bold `x`, not as bold `*x`.
	if source[offset+1] == source[offset] || jiraWordRuneBefore(source, start, offset) {
		return jiraEffectToken{}, false, offset
	}
	return jiraEffectToken{Style: style, Delimiter: source[offset], Start: offset, End: offset + 1}, true, offset + 1
}

// jiraEffectContentFollows reports whether source[at:end] can begin the content
// of a Text Effect, which Jira requires to start with a non-space character.
func jiraEffectContentFollows(source string, at, end int) bool {
	if at >= end {
		return false
	}
	next, _ := utf8.DecodeRuneInString(source[at:end])
	return !isJiraEffectSpace(next)
}

// jiraEffectOpenerKilled reports whether the opener of a scan starting at start
// opens nothing at all. When an effect's content is one rune followed by that
// effect's own bare delimiter, and a word rune after the delimiter refuses the
// close, Jira gives the opener up instead of scanning on for a later closer: it
// rereads the line from the character after the opener, which is why `*a*b*` is
// literal text, `{*}a*b*` shows a `{` and bolds `}a*b`, and `*€*b*` bolds the
// `b` on the very candidate it just refused. Two runes of content are already
// enough to make the scan continue, so `*ab*c*` bolds `ab*c`.
//
// The test reads one fixed position, because a candidate can only sit directly
// after the first rune; nothing between them can be a delimiter.
func jiraEffectOpenerKilled(source string, start, end int, delimiter byte) bool {
	first, size := utf8.DecodeRuneInString(source[start:end])
	if size == 0 || isJiraEffectSpace(first) {
		return false
	}
	candidate := start + size
	// A brace form closes even before a word rune, and a delimiter a backslash
	// precedes is not a candidate at all, so neither kills the opener.
	if candidate >= end || source[candidate] != delimiter || jiraBackslashPrecedes(source, candidate) {
		return false
	}
	return jiraWordRuneAfter(source, candidate+1, end)
}

// findJiraEffectClose scans source[start:end] for the Effect Delimiter token
// closing an opener whose content begins at start, and reports its half-open
// range or -1. A brace form that cannot close leaves the delimiter inside it
// available, which is how `a{*}{*}b` closes on the second `*`. killed reports
// the separate outcome of jiraEffectOpenerKilled: no closer, and the opener
// itself is given up rather than merely left unpaired, so a caller resumes one
// byte after the opener and may not memoize the scan.
func findJiraEffectClose(ctx context.Context, source string, start, end int, delimiter byte) (int, int, bool, error) {
	if jiraEffectOpenerKilled(source, start, end, delimiter) {
		return -1, -1, true, nil
	}
	for index := start; index < end; index++ {
		if (index-start)&255 == 0 {
			if err := ctx.Err(); err != nil {
				return -1, -1, false, err
			}
		}
		if jiraBackslashPrecedes(source, index) {
			continue
		}
		// Jira substitutes an emoticon token before it pairs any delimiter, so
		// a delimiter inside one closes nothing: `+(+)+` is an inserted effect
		// around the plus icon, and `*(*)*` a bold one around the star. A token
		// the gate suppresses or a backslash escapes is no token, and its
		// characters stay candidates.
		if tokenLength := jiraEmoticonAt(source, index, end); tokenLength != 0 {
			index += tokenLength - 1
			continue
		}
		closeEnd, brace := 0, false
		switch {
		case source[index] == '{' && index+2 < end && source[index+1] == delimiter && source[index+2] == '}':
			closeEnd, brace = index+3, true
		case source[index] == delimiter:
			closeEnd = index + 1
		default:
			continue
		}
		if index <= start {
			continue
		}
		if previous, _ := utf8.DecodeLastRuneInString(source[start:index]); isJiraEffectSpace(previous) {
			continue
		}
		if !brace && jiraWordRuneAfter(source, closeEnd, end) {
			continue
		}
		return index, closeEnd, false, nil
	}
	return -1, -1, false, ctx.Err()
}

// jiraEffectPair is one complete Text Effect or citation, as the half-open
// ranges of its two delimiter tokens.
type jiraEffectPair struct {
	OpenStart  int
	OpenEnd    int
	CloseStart int
	CloseEnd   int
}

// forEachJiraEffectPair visits every complete Text Effect and citation pair in
// source[start:end], descending into each pair's content the way the Jira parser
// does so that a nested pair is scanned in its own range. It is the plain-text
// escaper's whole decision: a delimiter this walk does not report is one Jira
// shows as a character, and escaping it would only add noise.
func forEachJiraEffectPair(ctx context.Context, source string, start, end int, visit func(jiraEffectPair)) error {
	ranges := []sourceSpan{{Start: start, End: end}}
	for len(ranges) != 0 {
		span := ranges[len(ranges)-1]
		ranges = ranges[:len(ranges)-1]
		for offset := span.Start; offset < span.End; {
			if (offset-span.Start)&255 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			if source[offset] == '\\' {
				// Only a lone backslash escapes; a run of two or more is
				// characters Jira shows and the delimiter behind it stays dead
				// by lookbehind.
				runEnd := jiraBackslashRunEnd(source, offset, span.End)
				if runEnd-offset == 1 && offset+1 < span.End && isJiraEscapable(source[offset+1]) {
					offset += 2
					continue
				}
				offset = runEnd
				continue
			}
			if strings.HasPrefix(source[offset:span.End], "??") {
				close, err := jiraCitationClose(ctx, source, span.Start, offset, span.End)
				if err != nil {
					return err
				}
				if close > 0 {
					visit(jiraEffectPair{OpenStart: offset, OpenEnd: offset + 2, CloseStart: close, CloseEnd: close + 2})
					ranges = append(ranges, sourceSpan{Start: offset + 2, End: close})
					offset = close + 2
					continue
				}
			}
			token, opens, scanned := jiraEffectOpener(source, span.Start, offset, span.End)
			if opens {
				closeStart, closeEnd, killed, err := findJiraEffectClose(ctx, source, token.End, span.End, token.Delimiter)
				if err != nil {
					return err
				}
				if killed {
					// A killed opener opens nothing, so the walk rereads from
					// the byte after it -- inside a brace form too, which is how
					// `{*}a*b*` still reports the pair around `}a*b`.
					offset = token.Start + 1
					continue
				}
				if closeStart >= 0 {
					visit(jiraEffectPair{OpenStart: token.Start, OpenEnd: token.End, CloseStart: closeStart, CloseEnd: closeEnd})
					ranges = append(ranges, sourceSpan{Start: token.End, End: closeStart})
					offset = closeEnd
					continue
				}
			}
			if scanned > offset {
				offset = scanned
				continue
			}
			_, size := utf8.DecodeRuneInString(source[offset:span.End])
			offset += size
		}
	}
	return ctx.Err()
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

// jiraMonospaceAlwaysEncoded are the characters a Monospace Span body can never
// keep raw, whichever hazards the scan reports around them. Which construct a
// `{`, `}` or `\` is consumed into -- a macro, the span's own closer, a forced
// newline, a legacy escape -- depends only on what follows it, and U+200B is a
// span boundary wherever it sits, so a body keeps none of them even at an offset
// where no hazard fires.
const jiraMonospaceAlwaysEncoded = "{}\\\u200b"

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

// jiraEmoticonAliases maps a token onto the spelling JFM writes for the icon it
// renders. `(*y)` and `(*)` are the same yellow star, so JFM has one canonical
// form for it; every other token names an icon of its own.
var jiraEmoticonAliases = map[string]string{"(*y)": "(*)"}

// jiraEmoticonLeadingReferences are the character references plain text writes
// for the first byte of a colon or semicolon token. Neither byte is escapable
// on its own, so a token that opens with one is kept visible by encoding that
// byte rather than by protecting a pair of parentheses (ADR-0019).
var jiraEmoticonLeadingReferences = map[byte]string{':': "&#58;", ';': "&#59;"}

// jiraEmoticonNeutralizations maps each encoding jiraEmoticonLeadingReferences
// produces back onto the token it keeps visible, so that Jira-to-JFM reads a
// neutralized token as the ordinary text it was written from. No key is a
// prefix of another.
var jiraEmoticonNeutralizations = func() map[string]string {
	result := make(map[string]string, len(jiraEmoticonLeadingReferences))
	for _, token := range jiraEmoticonTokens {
		if reference, encoded := jiraEmoticonLeadingReferences[token[0]]; encoded {
			result[reference+token[1:]] = token
		}
	}
	return result
}()

// canonicalJiraEmoticonToken reports the canonical spelling of one supported
// emoticon token. Matching is exact and case-sensitive, because Jira reads `:P`
// as a token and `:p` as text.
func canonicalJiraEmoticonToken(token string) (string, bool) {
	for _, supported := range jiraEmoticonTokens {
		if token != supported {
			continue
		}
		if canonical, aliased := jiraEmoticonAliases[token]; aliased {
			return canonical, true
		}
		return token, true
	}
	return "", false
}

// jiraNeutralizedEmoticonAt reports the token the character reference at index
// keeps visible, and the length of that encoding.
func jiraNeutralizedEmoticonAt(source string, index, end int) (string, int) {
	if index >= end || source[index] != '&' {
		return "", 0
	}
	for encoded, token := range jiraEmoticonNeutralizations {
		if strings.HasPrefix(source[index:end], encoded) {
			return token, len(encoded)
		}
	}
	return "", 0
}

// jiraAutolinkSchemes are the bare-URL prefixes the renderer links. A bare email
// address and a scheme-less `www.` host stay text, so they are absent here.
var jiraAutolinkSchemes = []string{"http://", "https://", "ftp://", "mailto:"}

// jiraInlineHazards reports, in source order, every construct Jira would
// reinterpret in source[start:end]. lineEnd is the end of the line the run
// stands in, which the forced-newline rule needs and which reaches past end for
// a run nested inside a line, such as a Monospace Span body. inTableCell adds
// the cell-separator hazard for a run that a table row has already split.
func jiraInlineHazards(ctx context.Context, source string, start, end, lineEnd int, inlineContext jiraInlineContext, inTableCell bool) ([]jiraInlineHazard, error) {
	hazards := make([]jiraInlineHazard, 0)
	monospace := inlineContext == jiraMonospaceContext
	add := func(kind jiraHazardKind, style inlineStyle, hazardStart, hazardEnd int, textChanges bool) {
		if !monospace && !jiraHazardInPlainText[kind] {
			return
		}
		hazards = append(hazards, jiraInlineHazard{Kind: kind, Style: style, Start: hazardStart, End: hazardEnd, TextChanges: textChanges})
	}
	var failedEffectScans map[byte]int
	// scanEffect reports the Text Effect opening at index and where the scan
	// resumes, or -1 when no Effect Delimiter token opens there.
	scanEffect := func(index int) (int, error) {
		token, opens, scanned := jiraEffectOpener(source, start, index, end)
		if !opens {
			if scanned > index {
				return scanned, nil
			}
			return -1, nil
		}
		// A killed opener is not an exhausted scan, so it is tested before the
		// memo and never recorded in it: a later opener carrying the same
		// delimiter can still pair.
		if jiraEffectOpenerKilled(source, token.End, end, token.Delimiter) {
			return token.Start + 1, nil
		}
		if failedFrom, known := failedEffectScans[token.Delimiter]; !known || token.End < failedFrom {
			closeStart, closeEnd, _, err := findJiraEffectClose(ctx, source, token.End, end, token.Delimiter)
			if err != nil {
				return -1, err
			}
			if closeStart < 0 {
				if failedEffectScans == nil {
					failedEffectScans = make(map[byte]int)
				}
				failedEffectScans[token.Delimiter] = token.End
			} else {
				add(jiraHazardEffect, token.Style, token.Start, closeEnd, true)
			}
		}
		return scanned, nil
	}
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
			// Only the pair that actually breaks is a forced newline, and the
			// rule is read on the whole line: the pair in `{{a\\b}}-c\\d` is two
			// characters Jira shows because the later backslash stands in the
			// same token. Every other run of two or more is literal too, and
			// escapes nothing; a lone backslash still starts a legacy escape,
			// which Jira-to-JFM conversion keeps decoding.
			runEnd := jiraBackslashRunEnd(source, index, end)
			if jiraForcedNewlineRun(source, index, runEnd, lineEnd) {
				add(jiraHazardForcedNewline, "", index, runEnd, true)
				index = runEnd
				continue
			}
			if runEnd-index == 1 && index+1 < end && isJiraEscapable(source[index+1]) {
				add(jiraHazardEscape, "", index, index+2, true)
				index += 2
				continue
			}
			if runEnd == end {
				add(jiraHazardTrailingBackslash, "", index, runEnd, true)
			}
			index = runEnd
			continue
		}
		if character == '}' && index+1 < end && source[index+1] == '}' {
			add(jiraHazardMonospaceClose, "", index, index+2, false)
			index += 2
			continue
		}
		if character == '{' || character == '}' {
			// The brace form of an Effect Delimiter outranks the macro rule:
			// Jira renders `a{*}b{*}c` bold rather than lifting `{*}` out.
			if next, err := scanEffect(index); err != nil {
				return nil, err
			} else if next > index {
				index = next
				continue
			}
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
		if _, autolinkEnd := jiraAutolinkExtent(source, start, index, end); autolinkEnd > 0 {
			// An autolink of any scheme, including mailto, leaves Jira's visible
			// text derived from the address rather than the raw markup, but no
			// caller marks it: Jira's autolinker leaves the address visible and
			// a REST read returns the raw markup unchanged, so nothing is lost
			// by leaving it raw.
			add(jiraHazardAutolink, "", index, autolinkEnd, false)
			index = autolinkEnd
			continue
		}
		if next, err := scanEffect(index); err != nil {
			return nil, err
		} else if next > index {
			index = next
			continue
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
// that the `{{` at offset opens inside the inline run source[start:end], and the
// offset where the span body ends: the two differ when Jira consumes a backslash
// written immediately before the closer. A returned close of -1 means the run
// holds no closer this opener can reach, which lets callers memoize the failure
// for every later opener: whether a `}}` is hidden depends only on the raw bytes
// in front of it, and a later opener's scan converges onto the same skips
// because `{{` can never start inside a run of `}`.
func jiraMonospaceSpanEnd(ctx context.Context, source string, start, offset, end int) (int, int, bool, error) {
	body := offset + 2
	close, bodyEnd := -1, -1
	// Backslashes written in front of a `}}` decide whether it closes anything.
	// Two or more hide it, and Jira then scans past the whole `}` run it starts,
	// so `{{a\\}}` and `{{a\\}}}` find no closer at all while `{{a\\}}b}}` pairs
	// with the second one. A single backslash closes the span and vanishes, so
	// `{{a\}}` reads `a`. The first `}}` left standing is the only candidate:
	// `{{a\}}b}}` stays literal text rather than pairing with the second.
	for index := body; index+1 < end; index++ {
		if (index-body)&255 == 0 {
			if err := ctx.Err(); err != nil {
				return -1, -1, false, err
			}
		}
		if source[index] != '}' || source[index+1] != '}' {
			continue
		}
		backslashes := 0
		for scan := index - 1; scan >= body && source[scan] == '\\'; scan-- {
			backslashes++
		}
		if backslashes >= 2 {
			for index+1 < end && source[index+1] == '}' {
				index++
			}
			continue
		}
		close, bodyEnd = index, index-backslashes
		break
	}
	if err := ctx.Err(); err != nil {
		return -1, -1, false, err
	}
	if close < 0 {
		return -1, -1, false, nil
	}
	// Braces touching a word rune are literal text on either side, so `x{{a}}`,
	// `{{a}}y` and `中{{a}}文` all stay prose.
	if previous, size := utf8.DecodeLastRuneInString(source[start:offset]); size != 0 && isJiraWordRune(previous) {
		return close, bodyEnd, false, nil
	}
	if next, size := utf8.DecodeRuneInString(source[close+2 : end]); size != 0 && isJiraWordRune(next) {
		return close, bodyEnd, false, nil
	}
	// An empty body, a literal edge space, a tab or a newline all refuse the
	// span; a space written as a character reference does not, because Jira
	// resolves references after this rule has run. Each check reads the body a
	// consumed backslash leaves behind, so `{{\}}` is empty and `{{a \}}` ends in
	// a space.
	if bodyEnd == body {
		return close, bodyEnd, false, nil
	}
	if source[body] == ' ' || source[bodyEnd-1] == ' ' {
		return close, bodyEnd, false, nil
	}
	if strings.ContainsAny(source[body:bodyEnd], "\t\n\r") {
		return close, bodyEnd, false, nil
	}
	// U+200B reads as a boundary inside the body too: `{{a<U+200B>b}}` stays
	// literal text while `{{<U+200B>a}}` and `{{a<U+200B>}}` still render a span.
	for scan := body; scan < bodyEnd; {
		offset := strings.Index(source[scan:bodyEnd], "\u200b")
		if offset < 0 {
			break
		}
		at := scan + offset
		if at != body && at+len("\u200b") != bodyEnd {
			return close, bodyEnd, false, nil
		}
		scan = at + len("\u200b")
	}
	return close, bodyEnd, true, nil
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
// index, gated exactly like the single-character Effect Delimiters: the same
// word-rune boundaries, the same one-rune kill, and the same backslash
// lookbehind, which is why `a\\??x??` opens nothing, `??ab\\\\??` finds no closer,
// and `??ab\?? c??` closes on the later `??`.
func jiraCitationClose(ctx context.Context, source string, start, index, end int) (int, error) {
	if jiraBackslashPrecedes(source, index) {
		return -1, nil
	}
	next, size := utf8.DecodeRuneInString(source[index+2 : end])
	if size == 0 || isJiraEffectSpace(next) {
		return -1, nil
	}
	if previous, size := utf8.DecodeLastRuneInString(source[start:index]); size != 0 && isJiraWordRune(previous) {
		return -1, nil
	}
	// One rune of content followed by a `??` that a word rune refuses gives the
	// opener up rather than scanning on, exactly like a bare Effect Delimiter:
	// `??a??b??` is literal text, while `??ab??c??` is a citation whose content
	// holds the inner `??` and `??€??b??` cites the `b` alone. A candidate a
	// backslash precedes is no candidate, so it kills nothing either and
	// `??\??b??` cites `\??b`.
	if candidate := index + 2 + size; candidate+1 < end && source[candidate] == '?' &&
		source[candidate+1] == '?' && !jiraBackslashPrecedes(source, candidate) &&
		jiraWordRuneAfter(source, candidate+2, end) {
		return -1, nil
	}
	for scan := index + 2; scan+1 < end; scan++ {
		if (scan-index)&255 == 0 {
			if err := ctx.Err(); err != nil {
				return -1, err
			}
		}
		if source[scan] != '?' || source[scan+1] != '?' || jiraBackslashPrecedes(source, scan) {
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

// jiraRowShapeEnd reports the end of the link or image shape starting at offset,
// or -1 when none does. A table row separator may not split one: Jira reads
// every `|` inside `[...]` or inside an image `!...!` as part of the construct,
// so `|[x|http://x]|c|` is two cells while `|{{a|b}}|c|` is three -- a Monospace
// Span protects nothing at the row level.
func jiraRowShapeEnd(ctx context.Context, source string, offset, end int) (int, error) {
	switch source[offset] {
	case '[':
		close, _, err := jiraLinkEnd(ctx, source, offset, end)
		if err != nil || close < 0 {
			return -1, err
		}
		return close + 1, nil
	case '!':
		return jiraImageShapeEnd(ctx, source, offset, end)
	default:
		return -1, nil
	}
}

// jiraImageShapeEnd reports the end of the image shape opened by the `!` at
// offset. The gates are asymmetric and both observed at the row level: a space
// directly after the opening `!` refuses the shape (`|! a|b!|c|` is three
// cells), and a word rune directly after a candidate closing `!` refuses that
// closer (`|!a.png|b!x|c|` is three cells).
func jiraImageShapeEnd(ctx context.Context, source string, offset, end int) (int, error) {
	if next, size := utf8.DecodeRuneInString(source[offset+1 : end]); size == 0 || isJiraEffectSpace(next) {
		return -1, nil
	}
	for scan := offset + 1; scan < end; scan++ {
		if (scan-offset)&255 == 0 {
			if err := ctx.Err(); err != nil {
				return -1, err
			}
		}
		switch source[scan] {
		case '\\':
			scan++
		case '\n', '\r':
			return -1, nil
		case '!':
			if !jiraWordRuneAfter(source, scan+1, end) {
				return scan + 1, nil
			}
		}
	}
	return -1, ctx.Err()
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

// jiraEscapableCharacters are the characters whose backslash Jira consumes,
// showing the character alone. A backslash before any other character stays
// visible, so `a\.b` renders with the backslash and `\h1. x` is not a heading.
const jiraEscapableCharacters = `\{}[]|!#?()%@*_-+^~`
