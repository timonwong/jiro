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
			heading := fmt.Sprintf("h%d.", typed.Level)
			if content != "" {
				heading += " " + content
			}
			blocks = append(blocks, heading)
		case paragraphBlock:
			content, err := renderJiraInlineRun(ctx, state, typed.Inlines, jiraRunContext{atLineStart: true})
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
						value, err := escapeJiraDelimitedValueWithContext(ctx, attribute.Value, `\{}|`)
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
			content, err := escapeTextForJira(ctx, typed.Text, true)
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
	writeText := func(value string) error {
		content, offsets, err := escapeTextForJiraText(ctx, value, render.atLineStart)
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
		switch typed := inline.(type) {
		case textInline:
			if err := writeText(typed.Text); err != nil {
				return jiraInlineOutput{}, err
			}
		case codeInline:
			if render.mode == jiraEscapeAbandoned {
				if err := writeText(typed.Text); err != nil {
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
			write("\\\\\n", false, false)
		case styledInline:
			if typed.Style == styleColor {
				content, err := renderJiraInlines(ctx, typed.Children, render.nested())
				if err != nil {
					return jiraInlineOutput{}, err
				}
				value, err := escapeJiraDelimitedValueWithContext(ctx, typed.Value, `\{}|`)
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
			label, err := renderJiraInlines(ctx, typed.Label, render.nested())
			if err != nil {
				return jiraInlineOutput{}, err
			}
			target, err := escapeJiraDelimitedValueWithContext(ctx, typed.Target, `\[]|`)
			if err != nil {
				return jiraInlineOutput{}, err
			}
			if typed.Unnamed {
				write("["+target+"]", false, false)
			} else {
				writeNested("[", label, "|"+target+"]")
			}
		case imageInline:
			source, err := escapeJiraDelimitedValueWithContext(ctx, typed.Source, `\!|`)
			if err != nil {
				return jiraInlineOutput{}, err
			}
			markup := "!" + source
			attributes := make([]string, 0, len(typed.Attributes)+1)
			if typed.Alt != "" {
				alt, err := escapeJiraDelimitedValueWithContext(ctx, typed.Alt, `\!|,=`)
				if err != nil {
					return jiraInlineOutput{}, err
				}
				attributes = append(attributes, "alt="+alt)
			}
			for _, attribute := range orderDirectiveAttributes(typed.Attributes, imageAttributeOrder()) {
				if attribute.Bare {
					attributes = append(attributes, attribute.Name)
				} else {
					value, err := escapeJiraDelimitedValueWithContext(ctx, attribute.Value, `\!|,=`)
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
		case literalInline:
			if err := writeText(typed.Text); err != nil {
				return jiraInlineOutput{}, err
			}
		default:
			return jiraInlineOutput{}, fmt.Errorf("%w: unsupported semantic inline in Jira renderer", ErrConversion)
		}
		render.atLineStart = inlineEndsAtLineStart(inline)
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

// jiraFirstRuneOfInlines reports the rune that will sit after a Text Effect's
// closing delimiter in the rendered run, which is where Jira decides whether a
// bare delimiter closes at all. Only a text inline can begin with a word rune:
// every other inline begins with `{`, `[`, `!`, `\` or an Effect Delimiter of
// its own.
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

func escapeTextForJira(ctx context.Context, value string, atLineStart bool) (string, error) {
	content, plainEffectOffsets, err := escapeTextForJiraText(ctx, value, atLineStart)
	if err != nil {
		return "", err
	}
	return escapePlainJiraEffects(ctx, content, plainEffectOffsets, jiraEscapePredicted)
}

// escapeTextForJiraText writes one plain-text fragment and reports the offsets
// of the characters that may still become markup, which the run-level escaper
// decides on. Only ASCII can carry a Jira rule, so the scan runs over bytes and
// copies the stretches between two of them whole.
func escapeTextForJiraText(ctx context.Context, value string, atLineStart bool) (string, []int, error) {
	var result strings.Builder
	var plainEffectOffsets []int
	pending, changed := 0, false
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
		if atLineStart {
			if prefixLength := jiraLineControlPrefixLength(value[offset:]); prefixLength != 0 {
				// A character reference is the only way to keep `h1.` and `bq.` off
				// the line start: Jira does not consume a backslash before `.`, so
				// `h1\. x` renders with the backslash visible.
				flush(offset + prefixLength - 1)
				result.WriteString("&#46;")
				pending, changed = offset+prefixLength, true
				offset, atLineStart = pending, false
				continue
			}
		}
		character := value[offset]
		atLineStart = character == '\n'
		switch jiraPlainTextByteClasses[character] {
		case jiraPlainTextByteStructural:
			flush(offset)
			result.WriteByte('\\')
			changed = true
		case jiraPlainTextByteDelimiter:
			flush(offset)
			plainEffectOffsets = append(plainEffectOffsets, result.Len())
		case jiraPlainTextByteBackslash:
			// An authored backslash may not become one of Jira's own. Jira reads
			// `\\` as a forced newline and `\X` as an escape, so a backslash that
			// would start either is written as a character reference; every other
			// one Jira shows as itself, which keeps `C:\dir\file` readable.
			if offset+1 < len(value) && isJiraEscapable(value[offset+1]) {
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

func escapeJiraDelimitedValueWithContext(ctx context.Context, value, delimiters string) (string, error) {
	return escapeSelectedRunes(ctx, value, delimiters)
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
		content, err := renderJiraInlineRun(ctx, state, item.Inlines, jiraRunContext{})
		if err != nil {
			return nil, err
		}
		line := marker
		if content != "" {
			line += " " + content
		}
		lines = append(lines, line)
		interrupted := false
		for _, block := range item.Blocks {
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
		value, err := renderJiraInlineRun(ctx, state, cell.Inlines, jiraRunContext{cellDelimiter: delimiter})
		if err != nil {
			return "", err
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
			value, err := escapeJiraDelimitedValueWithContext(ctx, attribute.Value, `\{}|`)
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

// jiraPlainTextEscapedCharacters are the characters the plain-text escaper
// backslash-escapes outside any grammar rule, as legacy safety escaping
// (ADR-0016).
func jiraLineControlPrefixLength(value string) int {
	if len(value) >= 3 && value[0] == 'h' && value[1] >= '1' && value[1] <= '6' && value[2] == '.' &&
		(len(value) == 3 || value[3] == ' ') {
		return 3
	}
	if strings.HasPrefix(value, "bq.") && (len(value) == 3 || value[3] == ' ') {
		return 3
	}
	return 0
}
