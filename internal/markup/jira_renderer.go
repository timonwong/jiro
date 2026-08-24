package markup

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

func renderJiraMarkup(ctx context.Context, document semanticDocument) (string, []conversionDiagnostic, error) {
	state := &jiraRenderState{diagnostics: make([]conversionDiagnostic, 0)}
	markup, err := renderJiraBlocks(ctx, state, document)
	if err != nil {
		return "", nil, err
	}
	return markup, state.diagnostics, nil
}

func renderJiraBlocks(ctx context.Context, state *jiraRenderState, document semanticDocument) (string, error) {
	blocks := make([]string, 0, len(document.Blocks))
	for _, block := range document.Blocks {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		switch typed := block.(type) {
		case headingBlock:
			content, err := renderJiraInlineRun(ctx, state, typed.Inlines, jiraRunContext{})
			if err != nil {
				return "", err
			}
			blocks = append(blocks, jiraLineControlMarkup(typed.Level, false, content))
		case paragraphBlock:
			content, err := renderJiraInlineRun(ctx, state, typed.Inlines, jiraRunContext{lineStart: jiraLineStartEveryRule})
			if err != nil {
				return "", err
			}
			blocks = append(blocks, content)
		case thematicBreakBlock:
			blocks = append(blocks, "----")
		case quoteBlock:
			content, err := renderJiraBlocks(ctx, state, semanticDocument{Blocks: typed.Blocks})
			if err != nil {
				return "", err
			}
			blocks = append(blocks, "{quote}\n"+content+ensureLiteralClosingSeparation(content)+"{quote}")
		case listBlock:
			content, err := renderJiraList(ctx, state, typed, "")
			if err != nil {
				return "", err
			}
			blocks = append(blocks, content)
		case tableBlock:
			content, err := renderJiraTable(ctx, state, typed)
			if err != nil {
				return "", err
			}
			blocks = append(blocks, content)
		case codeBlock:
			content, err := renderJiraCodeBlock(ctx, typed)
			if err != nil {
				return "", err
			}
			blocks = append(blocks, content)
		case panelBlock:
			body, err := renderJiraBlocks(ctx, state, semanticDocument{Blocks: typed.Blocks})
			if err != nil {
				return "", err
			}
			header := "{panel}"
			if len(typed.Attributes) != 0 {
				parts := make([]string, 0, len(typed.Attributes))
				for _, attribute := range orderDirectiveAttributes(typed.Attributes, panelAttributeOrder()) {
					if attribute.Bare {
						parts = append(parts, attribute.Name)
					} else {
						value, err := encodeJiraMacroParameterValue(ctx, attribute.Value)
						if err != nil {
							return "", err
						}
						parts = append(parts, attribute.Name+"="+value)
					}
				}
				header = "{panel:" + strings.Join(parts, "|") + "}"
			}
			blocks = append(blocks, header+"\n"+body+ensureLiteralClosingSeparation(body)+"{panel}")
		case unsupportedMacroBlock:
			body, err := renderJiraBlocks(ctx, state, semanticDocument{Blocks: typed.Blocks})
			if err != nil {
				return "", err
			}
			blocks = append(blocks, typed.Opening+"\n"+body+ensureLiteralClosingSeparation(body)+typed.Closing)
		case literalBlock:
			content, err := escapeTextForJira(ctx, typed.Text, jiraLineStartEveryRule)
			if err != nil {
				return "", err
			}
			blocks = append(blocks, content)
		default:
			return "", fmt.Errorf("%w: unsupported semantic block in Jira renderer", ErrConversion)
		}
	}
	return strings.TrimSpace(strings.Join(blocks, "\n\n")), nil
}

func renderJiraInlines(ctx context.Context, inlines []semanticInline, render jiraInlineRender) (jiraInlineOutput, error) {
	var result strings.Builder
	var output jiraInlineOutput
	written := false
	// write appends one rendered fragment, inserting the U+200B separator Jira
	// needs wherever a word rune would touch a Monospace Span brace, and reports
	// where the fragment starts so that plain-effect offsets stay aligned.
	write := func(content string, opensWithCode, endsWithCode bool) int {
		if content == "" {
			return result.Len()
		}
		if opensWithCode && jiraNeedsMonospaceSeparatorBefore(result.String()) ||
			output.endsWithCode && jiraNeedsMonospaceSeparatorAfter(content) {
			result.WriteString("\u200b")
		} else if !written {
			output.opensWithCode = opensWithCode
		}
		written, output.endsWithCode = true, endsWithCode
		offset := result.Len()
		result.WriteString(content)
		return offset
	}
	// writeText appends one plain-text fragment. beforeEmoticon reports that an
	// emoticon token follows it in the run, which is the one thing the escaper
	// cannot see in the fragment itself and which decides a trailing backslash:
	// Jira's emoticon escape would consume it and drop the icon.
	writeText := func(value string, beforeEmoticon bool) error {
		textRender := render
		textRender.beforeEmoticonToken = beforeEmoticon
		content, offsets, err := escapeTextForJiraText(ctx, value, textRender)
		if err != nil {
			return err
		}
		base := write(content, false, false)
		for _, offset := range offsets {
			output.plainOffsets = append(output.plainOffsets, base+offset)
		}
		return nil
	}
	// writeNested appends a fragment built around an already rendered child run
	// and rebases the child's plain-text delimiter offsets onto this buffer, so
	// that one escaping decision covers the whole top-level run.
	writeNested := func(prefix string, nested jiraInlineOutput, suffix string) {
		base := write(prefix+nested.text+suffix, false, false)
		for _, offset := range nested.plainOffsets {
			output.plainOffsets = append(output.plainOffsets, base+len(prefix)+offset)
		}
	}
	for index, inline := range inlines {
		if err := ctx.Err(); err != nil {
			return jiraInlineOutput{}, err
		}
		beforeEmoticon := nextInlineIsEmoticon(inlines[index+1:])
		switch typed := inline.(type) {
		case textInline:
			if err := writeText(typed.Text, beforeEmoticon); err != nil {
				return jiraInlineOutput{}, err
			}
		case codeInline:
			if render.mode == jiraEscapeAbandoned {
				if err := writeText(typed.Text, beforeEmoticon); err != nil {
					return jiraInlineOutput{}, err
				}
				break
			}
			body, err := renderJiraMonospaceSpanBody(ctx, typed.Text, render)
			if err != nil {
				return jiraInlineOutput{}, err
			}
			write("{{"+body+"}}", true, true)
		case hardBreakInline:
			// No table cell reaches this: a GFM cell is one line and carries no
			// hard break, and the `<br>` that would stand for one there is not
			// controlled HTML in JFM and stays literal text
			// (TestTableCellBreakStaysLiteralHTML).
			write("\\\\\n", false, false)
		case styledInline:
			if typed.Style == styleColor {
				content, err := renderJiraInlines(ctx, typed.Children, render.nested())
				if err != nil {
					return jiraInlineOutput{}, err
				}
				value, err := encodeJiraMacroParameterValue(ctx, typed.Value)
				if err != nil {
					return jiraInlineOutput{}, err
				}
				writeNested("{color:"+value+"}", content, "{color}")
				break
			}
			delimiter, ok := jiraEffectDelimiter(typed.Style)
			if !ok {
				return jiraInlineOutput{}, fmt.Errorf("%w: Text Effect %q has no Jira Effect Delimiter", ErrConversion, typed.Style)
			}
			children, delimiters := typed.Children, string(delimiter)
			if combined, ok := combinedBoldItalic(typed); ok {
				children, delimiters = combined, "*_"
			}
			content, err := renderJiraInlines(ctx, children, render.nested())
			if err != nil {
				return jiraInlineOutput{}, err
			}
			// The rune before the opener is read from the output so far, which is
			// where Jira will see it; an empty buffer decodes to RuneError, which
			// is not a word rune and so gates nothing.
			before, _ := utf8.DecodeLastRuneInString(result.String())
			opener, closer := jiraEffectDelimiterForms(delimiters, render.mode,
				before, jiraFirstRuneOfInlines(inlines[index+1:]))
			writeNested(opener, content, closer)
		case linkInline:
			label, err := renderJiraInlines(ctx, typed.Label, render.nested().linkLabel())
			if err != nil {
				return jiraInlineOutput{}, err
			}
			target, err := encodeJiraLinkTarget(ctx, typed.Target)
			if err != nil {
				return jiraInlineOutput{}, err
			}
			title := spellJiraLinkTitle(typed.Title).Text
			switch {
			case title == "" && typed.Unnamed:
				write("["+target+"]", false, false)
			case title == "":
				writeNested("[", label, "|"+target+"]")
			case typed.Unnamed:
				// `[target]` has no third part, so the title is what makes an
				// unnamed link take the named spelling; Jira shows the target as
				// the visible text either way.
				write("["+target+"|"+target+"|"+title+"]", false, false)
			default:
				writeNested("[", label, "|"+target+"|"+title+"]")
			}
		case imageInline:
			source, err := encodeJiraImageSource(ctx, typed.Source)
			if err != nil {
				return jiraInlineOutput{}, err
			}
			markup := "!" + source
			attributes := make([]string, 0, len(typed.Attributes)+1)
			if typed.Alt != "" {
				alt, err := encodeJiraImageParameterValue(ctx, typed.Alt)
				if err != nil {
					return jiraInlineOutput{}, err
				}
				attributes = append(attributes, "alt="+alt)
			}
			for _, attribute := range orderDirectiveAttributes(typed.Attributes, imageAttributeOrder()) {
				if attribute.Bare {
					attributes = append(attributes, attribute.Name)
				} else {
					value, err := encodeJiraImageParameterValue(ctx, attribute.Value)
					if err != nil {
						return jiraInlineOutput{}, err
					}
					attributes = append(attributes, attribute.Name+"="+value)
				}
			}
			if len(attributes) != 0 {
				markup += "|" + strings.Join(attributes, ",")
			}
			write(markup+"!", false, false)
		case emoticonInline:
			// The raw token is the whole spelling. Where a word rune follows it
			// Jira suppresses the icon; the token still goes out visible and
			// collectEmoticonDiagnostics reports the loss, because a synthetic
			// boundary character would be text Jira never showed (ADR-0019).
			write(typed.Token, false, false)
		case literalInline:
			if err := writeText(typed.Text, beforeEmoticon); err != nil {
				return jiraInlineOutput{}, err
			}
		default:
			return jiraInlineOutput{}, fmt.Errorf("%w: unsupported semantic inline in Jira renderer", ErrConversion)
		}
		// A newline the run itself writes opens a full line start: what the
		// enclosing block narrows is only where the run begins.
		render.lineStart = jiraLineStartInline
		if inlineEndsAtLineStart(inline) {
			render.lineStart = jiraLineStartEveryRule
		}
	}
	output.text = result.String()
	return output, ctx.Err()
}

// jiraEffectDelimiterForms spells the delimiters of one Text Effect. Jira gates
// a bare delimiter by the word rune beside it, so an effect that touches a word
// on that side is written in the brace form `{X}`, which waives the gate:
// `a**b**c` is `a{*}b{*}c` and `*i*t` is `_i{_}t`. delimiters is the outermost
// delimiter first, so a combined bold-italic nests as `*_x_*`.
func jiraEffectDelimiterForms(delimiters string, mode jiraEscapeMode, before, after rune) (string, string) {
	brace := mode == jiraEscapeFullyEncoded
	braceOpener, braceCloser := brace || isJiraWordRune(before), brace || isJiraWordRune(after)
	if !braceOpener && !braceCloser {
		return delimiters, jiraReversedDelimiters(delimiters)
	}
	opener := make([]byte, 0, 3*len(delimiters))
	closer := make([]byte, 0, 3*len(delimiters))
	appendForm := func(target []byte, delimiter byte, useBrace bool) []byte {
		if !useBrace {
			return append(target, delimiter)
		}
		return append(target, '{', delimiter, '}')
	}
	for index := 0; index < len(delimiters); index++ {
		opener = appendForm(opener, delimiters[index], brace || index == 0 && braceOpener)
		closerIndex := len(delimiters) - 1 - index
		closer = appendForm(closer, delimiters[closerIndex], brace || closerIndex == 0 && braceCloser)
	}
	return string(opener), string(closer)
}

func jiraReversedDelimiters(delimiters string) string {
	if len(delimiters) < 2 {
		return delimiters
	}
	reversed := make([]byte, len(delimiters))
	for index := range reversed {
		reversed[index] = delimiters[len(delimiters)-1-index]
	}
	return string(reversed)
}

// jiraFirstRuneOfInlines reports the rune that will open the rendered run, which
// is where Jira decides whether a bare Text Effect delimiter closes at all and
// whether an emoticon token in front of it still renders as an icon. Only a text
// inline can begin with a word rune: every other inline begins with `{`, `[`,
// `!`, `\`, an emoticon token's `(`, `:` or `;`, or an Effect Delimiter of its
// own.
func jiraFirstRuneOfInlines(inlines []semanticInline) rune {
	for _, inline := range inlines {
		switch typed := inline.(type) {
		case textInline:
			if character, size := utf8.DecodeRuneInString(typed.Text); size != 0 {
				return character
			}
		case literalInline:
			if character, size := utf8.DecodeRuneInString(typed.Text); size != 0 {
				return character
			}
		case codeInline:
			return '{'
		case linkInline:
			return '['
		case imageInline:
			return '!'
		case hardBreakInline:
			return '\\'
		case emoticonInline:
			if character, size := utf8.DecodeRuneInString(typed.Token); size != 0 {
				return character
			}
		case styledInline:
			if delimiter, ok := jiraEffectDelimiter(typed.Style); ok {
				return rune(delimiter)
			}
			// A color is written as a macro, so its run begins with a brace.
			return '{'
		}
	}
	return 0
}

// nextInlineIsEmoticon reports whether the rendered run continues with an
// emoticon token, which is what makes a trailing backslash in front of it
// unwritable as itself.
func nextInlineIsEmoticon(inlines []semanticInline) bool {
	if len(inlines) == 0 {
		return false
	}
	_, emoticon := inlines[0].(emoticonInline)
	return emoticon
}

// jiraNeedsMonospaceSeparatorBefore and jiraNeedsMonospaceSeparatorAfter read
// the rendered runes that will sit next to a Monospace Span brace, which is
// where Jira decides whether the span forms at all. A word rune refuses the
// span. An authored U+200B needs the separator for the opposite reason: Jira
// already reads it as the boundary, but Jira-to-JFM conversion strips exactly
// one U+200B touching the outside of the braces, so without a second one the
// authored character would not survive the round trip.
func jiraNeedsMonospaceSeparatorBefore(value string) bool {
	character, size := utf8.DecodeLastRuneInString(value)
	return size != 0 && (isJiraWordRune(character) || character == '\u200b')
}

func jiraNeedsMonospaceSeparatorAfter(value string) bool {
	character, size := utf8.DecodeRuneInString(value)
	return size != 0 && (isJiraWordRune(character) || character == '\u200b')
}

func escapeTextForJira(ctx context.Context, value string, lineStart jiraLineStartRules) (string, error) {
	content, plainEffectOffsets, err := escapeTextForJiraText(ctx, value, jiraInlineRender{lineStart: lineStart})
	if err != nil {
		return "", err
	}
	return escapePlainJiraEffects(ctx, content, plainEffectOffsets, jiraEscapePredicted)
}

// escapeTextForJiraText writes one plain-text fragment and reports the offsets
// of the characters that may still become markup, which the run-level escaper
// decides on. Only ASCII can carry a Jira rule, so the scan runs over bytes and
// copies the stretches between two of them whole.
func escapeTextForJiraText(ctx context.Context, value string, render jiraInlineRender) (string, []int, error) {
	var result strings.Builder
	var plainEffectOffsets []int
	lineStart := render.lineStart
	pending, changed := 0, false
	// emoticonCloseParenthesis is where the parenthesis closing a neutralized
	// token stands, so that the token's own bytes still pass through the byte
	// rules below and only its two parentheses gain a backslash.
	emoticonCloseParenthesis := -1
	flush := func(upto int) {
		if upto > pending {
			result.WriteString(value[pending:upto])
			pending = upto
		}
	}
	for offset := 0; offset < len(value); {
		if offset&255 == 0 {
			if err := ctx.Err(); err != nil {
				return "", nil, err
			}
		}
		if claim, claimed := lineStart.claim(value[offset:]); claimed {
			if claim.control {
				// A character reference is the only way to keep `h1.` and `bq.` off
				// the line start: Jira does not consume a backslash before `.`, so
				// `h1\. x` renders with the backslash visible.
				flush(offset + claim.end - 1)
				result.WriteString("&#46;")
				pending, changed = offset+claim.end, true
			} else {
				// Jira reads the whole marker run as a list, and stops reading one
				// as soon as the first marker carries a backslash, so the rest of
				// the run needs nothing beyond what its own byte class asks for.
				flush(offset + claim.start)
				result.WriteByte('\\')
				result.WriteByte(value[offset+claim.start])
				pending, changed = offset+claim.start+1, true
			}
			offset, lineStart = pending, jiraLineStartInline
			continue
		}
		character := value[offset]
		lineStart = jiraLineStartInline
		if character == '\n' {
			lineStart = jiraLineStartEveryRule
		}
		// Jira renders a supported emoticon token in visible text as an icon, so
		// text that only looks like one is kept visible with the smallest
		// encoding that stops it: both parentheses of a parenthesized token are
		// escaped, and a colon or semicolon token leaves its first byte as a
		// character reference (ADR-0019). The gate is the one the Jira parser
		// reads, so a token Jira would not render is written as it stands and
		// `print(x)foo` keeps its parentheses.
		if offset == emoticonCloseParenthesis {
			flush(offset)
			result.WriteByte('\\')
			changed = true
		} else if length := jiraEmoticonAt(value, offset, len(value)); length != 0 {
			flush(offset)
			if reference, encoded := jiraEmoticonLeadingReferences[character]; encoded {
				result.WriteString(reference)
				pending = offset + 1
			} else {
				result.WriteByte('\\')
				emoticonCloseParenthesis = offset + length - 1
			}
			changed = true
		}
		switch jiraPlainTextByteClasses[character] {
		case jiraPlainTextByteStructural:
			// A backslash before a `|` in a link's visible text protects
			// nothing: Jira splits the bracket body on every `|` and renders an
			// error span for the target it is left with, so a character
			// reference goes in instead.
			if character == '|' && render.inLinkLabel {
				flush(offset)
				result.WriteString("&#124;")
				pending, changed = offset+1, true
				break
			}
			flush(offset)
			result.WriteByte('\\')
			changed = true
		case jiraPlainTextByteDelimiter:
			flush(offset)
			plainEffectOffsets = append(plainEffectOffsets, result.Len())
		case jiraPlainTextByteBackslash:
			// An authored backslash may not become one of Jira's own. Jira reads
			// `\\` as a forced newline, `\X` as an escape and the one in front of
			// an emoticon token as that token's escape, so a backslash that would
			// start any of them is written as a character reference; every other
			// one Jira shows as itself, which keeps `C:\dir\file` readable. Only
			// the last backslash of a run can reach the token that follows the
			// fragment, so only it is encoded there.
			if offset+1 < len(value) && isJiraEscapable(value[offset+1]) ||
				offset+1 == len(value) && render.beforeEmoticonToken {
				flush(offset)
				result.WriteString("&#92;")
				pending, changed = offset+1, true
			}
		}
		offset++
	}
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	// A fragment Jira already reads literally passes through raw, and the
	// recorded offsets index it unchanged.
	if !changed {
		return value, plainEffectOffsets, nil
	}
	flush(len(value))
	return result.String(), plainEffectOffsets, nil
}

// escapePlainJiraEffects backslash-escapes the plain-text delimiters of every
// complete Text Effect and citation the grammar reads in the rendered run. One
// pass is enough because the escaping decision is only predicted here: the
// harness proves it by re-parsing the run, and a run whose escaping the pass got
// wrong escalates to the fully escaped mode rather than to another pass.
func escapePlainJiraEffects(ctx context.Context, value string, plainOffsets []int, mode jiraEscapeMode) (string, error) {
	if len(plainOffsets) == 0 {
		return value, ctx.Err()
	}
	escaped := make([]bool, len(value))
	if mode == jiraEscapePredicted {
		plain := make([]bool, len(value))
		for _, offset := range plainOffsets {
			plain[offset] = true
		}
		err := forEachJiraEffectPair(ctx, value, 0, len(value), func(pair jiraEffectPair) {
			markPlainJiraDelimiter(escaped, plain, pair.OpenStart, pair.OpenEnd)
			markPlainJiraDelimiter(escaped, plain, pair.CloseStart, pair.CloseEnd)
		})
		if err != nil {
			return "", err
		}
	} else {
		for _, offset := range plainOffsets {
			escaped[offset] = true
		}
	}
	marked := 0
	for _, offset := range plainOffsets {
		if escaped[offset] {
			marked++
		}
	}
	if marked == 0 {
		return value, ctx.Err()
	}
	result := make([]byte, 0, len(value)+marked)
	for offset := 0; offset < len(value); offset++ {
		if escaped[offset] {
			result = append(result, '\\')
		}
		result = append(result, value[offset])
	}
	return string(result), ctx.Err()
}

// markPlainJiraDelimiter escapes the characters of one delimiter token that came
// from plain text. The token may be one byte, the two bytes of `??`, or a brace
// form, whose braces a plain-text `{` never reaches because it is escaped
// already; marking the whole range therefore hits exactly the plain characters
// Jira would read as markup.
func markPlainJiraDelimiter(escaped, plain []bool, start, end int) {
	for offset := start; offset < end; offset++ {
		if plain[offset] {
			escaped[offset] = true
		}
	}
}

func renderJiraList(ctx context.Context, state *jiraRenderState, list listBlock, parentMarkers string) (string, error) {
	segments, err := renderJiraListSegments(ctx, state, list, parentMarkers)
	if err != nil {
		return "", err
	}
	values := make([]string, len(segments))
	for index, segment := range segments {
		values[index] = segment.text
	}
	return strings.Join(values, "\n\n"), nil
}

type jiraListRenderSegment struct {
	text     string
	listType byte
}

func renderJiraListSegments(ctx context.Context, state *jiraRenderState, list listBlock, parentMarkers string) ([]jiraListRenderSegment, error) {
	segments := make([]jiraListRenderSegment, 0, len(list.Items))
	lines := make([]string, 0, len(list.Items))
	listType := byte('*')
	if list.Ordered {
		listType = '#'
	}
	appendSegment := func(segment jiraListRenderSegment) {
		if segment.text == "" {
			return
		}
		if segment.listType != 0 && len(segments) != 0 && segments[len(segments)-1].listType == segment.listType {
			segments[len(segments)-1].text += "\n" + segment.text
			return
		}
		segments = append(segments, segment)
	}
	flushLines := func() {
		if len(lines) == 0 {
			return
		}
		appendSegment(jiraListRenderSegment{text: strings.Join(lines, "\n"), listType: listType})
		lines = nil
	}
	activeParentMarkers := parentMarkers
	for _, item := range list.Items {
		marker := activeParentMarkers + "*"
		if list.Ordered {
			marker = activeParentMarkers + "#"
		}
		line, taken, err := renderJiraListItemLine(ctx, state, item, marker)
		if err != nil {
			return nil, err
		}
		lines = append(lines, line)
		blocks := item.Blocks[taken:]
		interrupted := false
		for _, block := range blocks {
			child, isList := block.(listBlock)
			if isList && !interrupted && !child.RequiresFlattening {
				childSegments, err := renderJiraListSegments(ctx, state, child, marker)
				if err != nil {
					return nil, err
				}
				for _, segment := range childSegments {
					lines = append(lines, segment.text)
				}
				continue
			}
			flushLines()
			interrupted = true
			activeParentMarkers = ""
			if isList {
				childSegments, err := renderJiraListSegments(ctx, state, child, "")
				if err != nil {
					return nil, err
				}
				for _, segment := range childSegments {
					appendSegment(segment)
				}
				continue
			}
			content, err := renderJiraBlocks(ctx, state, semanticDocument{Blocks: []semanticBlock{block}})
			if err != nil {
				return nil, err
			}
			appendSegment(jiraListRenderSegment{text: content})
		}
	}
	flushLines()
	return segments, nil
}

// renderJiraListItemLine writes one item's own line and reports how many of the
// item's blocks that line took. A line control is written on the item line
// itself, because that is where Jira reads it back (listItemLineControl); every
// other block stays for the caller, which has only the flattening path for it.
func renderJiraListItemLine(ctx context.Context, state *jiraRenderState, item listItem, marker string) (string, int, error) {
	if level, quote, inlines, ok := listItemLineControl(item); ok {
		// The control's content is no line start of its own: Jira has read the
		// block by the time it begins, so `* h1. bq. y` keeps the `bq.` as text.
		content, err := renderJiraInlineRun(ctx, state, inlines, jiraRunContext{lineStart: jiraLineStartInline})
		if err != nil {
			return "", 0, err
		}
		return marker + " " + jiraLineControlMarkup(level, quote, content), 1, nil
	}
	content, err := renderJiraInlineRun(ctx, state, item.Inlines, jiraRunContext{lineStart: jiraLineStartItemContent})
	if err != nil {
		return "", 0, err
	}
	if content == "" {
		return marker, 0, nil
	}
	return marker + " " + content, 0, nil
}

// jiraLineControlMarkup spells one line control. Jira reads the control whether
// or not a space follows the `.`, and jiro writes that space only where content
// follows it, so an empty heading is `h1.` alone.
func jiraLineControlMarkup(level int, quote bool, content string) string {
	prefix := fmt.Sprintf("h%d.", level)
	if quote {
		prefix = "bq."
	}
	if content == "" {
		return prefix
	}
	return prefix + " " + content
}

func renderJiraTable(ctx context.Context, state *jiraRenderState, table tableBlock) (string, error) {
	if table.Directive && table.Raw != "" {
		return table.Raw, nil
	}
	rows := make([]string, 0, len(table.Rows)+1)
	if len(table.Header) != 0 {
		header, err := renderJiraTableRow(ctx, state, table.Header, "||")
		if err != nil {
			return "", err
		}
		rows = append(rows, header)
	}
	for _, row := range table.Rows {
		value, err := renderJiraTableRow(ctx, state, row, "|")
		if err != nil {
			return "", err
		}
		rows = append(rows, value)
	}
	return strings.Join(rows, "\n"), nil
}

func renderJiraTableRow(ctx context.Context, state *jiraRenderState, cells []tableCell, delimiter string) (string, error) {
	values := make([]string, len(cells))
	for index, cell := range cells {
		// Jira reads every cell of every row from its own line start, so a cell
		// whose content opens with a list marker or a `h1.` prefix renders a list
		// or a heading inside the cell rather than the text it was written as.
		value, err := renderJiraInlineRun(ctx, state, cell.Inlines, jiraRunContext{lineStart: jiraLineStartEveryRule, cellDelimiter: delimiter})
		if err != nil {
			return "", err
		}
		if value == "" {
			// An empty value would leave `||` in the row, which Jira reads as a
			// header-cell boundary or as the delimiter the row closes on rather
			// than as a cell. One space is the empty-looking cell it does read,
			// and an empty run has nothing for the verification to prove.
			value = " "
		}
		values[index] = value
	}
	return delimiter + strings.Join(values, delimiter) + delimiter, nil
}

func renderJiraCodeBlock(ctx context.Context, block codeBlock) (string, error) {
	if block.NoFormat && block.Language == "" && !block.Directive {
		return "{noformat}\n" + block.Body + ensureLiteralClosingSeparation(block.Body) + "{noformat}", ctx.Err()
	}
	attributes := block.Attributes
	if block.Language != "" && !containsDirectiveAttribute(attributes, "language") {
		attributes = append([]directiveAttribute{{Name: "language", Value: block.Language}}, attributes...)
	}
	header := "{code}"
	if len(attributes) != 0 {
		parts := make([]string, 0, len(attributes))
		for _, attribute := range orderDirectiveAttributes(attributes, codeAttributeOrder()) {
			value, err := encodeJiraMacroParameterValue(ctx, attribute.Value)
			if err != nil {
				return "", err
			}
			parts = append(parts, attribute.Name+"="+value)
		}
		header = "{code:" + strings.Join(parts, "|") + "}"
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return header + "\n" + block.Body + ensureLiteralClosingSeparation(block.Body) + "{code}", nil
}
