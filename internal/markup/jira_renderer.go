package markup

import (
	"context"
	"fmt"
	"strings"
	"unicode"
)

func renderJiraMarkup(ctx context.Context, document semanticDocument) (string, error) {
	blocks := make([]string, 0, len(document.Blocks))
	for _, block := range document.Blocks {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		switch typed := block.(type) {
		case headingBlock:
			content, err := renderJiraInlines(ctx, typed.Inlines, false)
			if err != nil {
				return "", err
			}
			heading := fmt.Sprintf("h%d.", typed.Level)
			if content != "" {
				heading += " " + content
			}
			blocks = append(blocks, heading)
		case paragraphBlock:
			content, err := renderJiraInlines(ctx, typed.Inlines, true)
			if err != nil {
				return "", err
			}
			blocks = append(blocks, content)
		case thematicBreakBlock:
			blocks = append(blocks, "----")
		case quoteBlock:
			content, err := renderJiraMarkup(ctx, semanticDocument{Blocks: typed.Blocks})
			if err != nil {
				return "", err
			}
			blocks = append(blocks, "{quote}\n"+content+ensureLiteralClosingSeparation(content)+"{quote}")
		case listBlock:
			content, err := renderJiraList(ctx, typed, "")
			if err != nil {
				return "", err
			}
			blocks = append(blocks, content)
		case tableBlock:
			content, err := renderJiraTable(ctx, typed)
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
			body, err := renderJiraMarkup(ctx, semanticDocument{Blocks: typed.Blocks})
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
			body, err := renderJiraMarkup(ctx, semanticDocument{Blocks: typed.Blocks})
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

func renderJiraInlines(ctx context.Context, inlines []semanticInline, atLineStart bool) (string, error) {
	var result strings.Builder
	plainEffectOffsets := make([]int, 0)
	for _, inline := range inlines {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		switch typed := inline.(type) {
		case textInline:
			content, offsets, err := escapeTextForJiraText(ctx, typed.Text, atLineStart)
			if err != nil {
				return "", err
			}
			base := result.Len()
			for _, offset := range offsets {
				plainEffectOffsets = append(plainEffectOffsets, base+offset)
			}
			result.WriteString(content)
		case codeInline:
			result.WriteString("{{" + escapeCodeSpan(typed.Text) + "}}")
		case hardBreakInline:
			result.WriteString("\\\\\n")
		case styledInline:
			children := typed.Children
			if combined, ok := combinedBoldItalic(typed); ok {
				content, err := renderJiraInlines(ctx, combined, false)
				if err != nil {
					return "", err
				}
				result.WriteString("*_" + content + "_*")
				break
			}
			content, err := renderJiraInlines(ctx, children, false)
			if err != nil {
				return "", err
			}
			switch typed.Style {
			case styleBold:
				result.WriteString("*" + content + "*")
			case styleItalic:
				result.WriteString("_" + content + "_")
			case styleStrike:
				result.WriteString("-" + content + "-")
			case styleInserted:
				result.WriteString("+" + content + "+")
			case styleSuper:
				result.WriteString("^" + content + "^")
			case styleSub:
				result.WriteString("~" + content + "~")
			case styleColor:
				value, err := escapeJiraDelimitedValueWithContext(ctx, typed.Value, `\{}|`)
				if err != nil {
					return "", err
				}
				result.WriteString("{color:" + value + "}" + content + "{color}")
			}
		case linkInline:
			label, err := renderJiraInlines(ctx, typed.Label, false)
			if err != nil {
				return "", err
			}
			target, err := escapeJiraDelimitedValueWithContext(ctx, typed.Target, `\[]|`)
			if err != nil {
				return "", err
			}
			if typed.Unnamed {
				result.WriteString("[" + target + "]")
			} else {
				result.WriteString("[" + label + "|" + target + "]")
			}
		case imageInline:
			source, err := escapeJiraDelimitedValueWithContext(ctx, typed.Source, `\!|`)
			if err != nil {
				return "", err
			}
			markup := "!" + source
			attributes := make([]string, 0, len(typed.Attributes)+1)
			if typed.Alt != "" {
				alt, err := escapeJiraDelimitedValueWithContext(ctx, typed.Alt, `\!|,=`)
				if err != nil {
					return "", err
				}
				attributes = append(attributes, "alt="+alt)
			}
			for _, attribute := range orderDirectiveAttributes(typed.Attributes, imageAttributeOrder()) {
				if attribute.Bare {
					attributes = append(attributes, attribute.Name)
				} else {
					value, err := escapeJiraDelimitedValueWithContext(ctx, attribute.Value, `\!|,=`)
					if err != nil {
						return "", err
					}
					attributes = append(attributes, attribute.Name+"="+value)
				}
			}
			if len(attributes) != 0 {
				markup += "|" + strings.Join(attributes, ",")
			}
			result.WriteString(markup + "!")
		case literalInline:
			content, offsets, err := escapeTextForJiraText(ctx, typed.Text, atLineStart)
			if err != nil {
				return "", err
			}
			base := result.Len()
			for _, offset := range offsets {
				plainEffectOffsets = append(plainEffectOffsets, base+offset)
			}
			result.WriteString(content)
		default:
			return "", fmt.Errorf("%w: unsupported semantic inline in Jira renderer", ErrConversion)
		}
		atLineStart = inlineEndsAtLineStart(inline)
	}
	return escapePlainJiraEffects(ctx, result.String(), plainEffectOffsets)
}

func escapeTextForJira(ctx context.Context, value string, atLineStart bool) (string, error) {
	content, plainEffectOffsets, err := escapeTextForJiraText(ctx, value, atLineStart)
	if err != nil {
		return "", err
	}
	return escapePlainJiraEffects(ctx, content, plainEffectOffsets)
}

func escapeTextForJiraText(ctx context.Context, value string, atLineStart bool) (string, []int, error) {
	var result strings.Builder
	plainEffectOffsets := make([]int, 0)
	for offset := 0; offset < len(value); {
		if offset&255 == 0 {
			if err := ctx.Err(); err != nil {
				return "", nil, err
			}
		}
		remaining := value[offset:]
		if atLineStart {
			if prefixLength := jiraLineControlPrefixLength(remaining); prefixLength != 0 {
				result.WriteString(remaining[:prefixLength-1])
				result.WriteString(`\.`)
				offset += prefixLength
				atLineStart = false
				continue
			}
		}
		character, size := utf8DecodeRune(remaining)
		if strings.ContainsRune(`\{}[]!?|#`, character) {
			result.WriteByte('\\')
		}
		if strings.ContainsRune(`*_-+^~`, character) {
			plainEffectOffsets = append(plainEffectOffsets, result.Len())
		}
		result.WriteRune(character)
		offset += size
		atLineStart = character == '\n'
	}
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	return result.String(), plainEffectOffsets, nil
}

func escapePlainJiraEffects(ctx context.Context, value string, plainEffectOffsets []int) (string, error) {
	if len(plainEffectOffsets) == 0 {
		return value, ctx.Err()
	}
	plain := make([]bool, len(value))
	for _, offset := range plainEffectOffsets {
		plain[offset] = true
	}
	escaped := make([]bool, len(value))
	for {
		changed, err := markPlainJiraEffectEscapes(ctx, value, plain, escaped)
		if err != nil {
			return "", err
		}
		if !changed {
			break
		}
	}
	var result strings.Builder
	result.Grow(len(value) + len(plainEffectOffsets))
	for offset, character := range value {
		if escaped[offset] {
			result.WriteByte('\\')
		}
		result.WriteRune(character)
	}
	return result.String(), ctx.Err()
}

func markPlainJiraEffectEscapes(ctx context.Context, value string, plain, escaped []bool) (bool, error) {
	changed := false
	ranges := []sourceSpan{{Start: 0, End: len(value)}}
	for len(ranges) != 0 {
		last := len(ranges) - 1
		span := ranges[last]
		ranges = ranges[:last]
		for offset := span.Start; offset < span.End; {
			if offset&255 == 0 {
				if err := ctx.Err(); err != nil {
					return false, err
				}
			}
			delimiter := value[offset]
			if !escaped[offset] {
				if _, ok := jiraInlineStyle(delimiter); ok && jiraDelimiterCanOpen(value, offset, span.End) {
					close, err := findJiraStyleCloseIgnoring(ctx, value, offset+1, span.End, delimiter, escaped)
					if err != nil {
						return false, err
					}
					if close >= 0 {
						if plain[offset] && !escaped[offset] {
							escaped[offset], changed = true, true
						}
						if plain[close] && !escaped[close] {
							escaped[close], changed = true, true
						}
						if offset+1 < close {
							ranges = append(ranges, sourceSpan{Start: offset + 1, End: close})
						}
						offset = close + 1
						continue
					}
				}
			}
			_, size := utf8DecodeRune(value[offset:span.End])
			offset += size
		}
	}
	return changed, ctx.Err()
}

func findJiraStyleCloseIgnoring(ctx context.Context, source string, start, end int, delimiter byte, ignored []bool) (int, error) {
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
		if ignored[index] {
			continue
		}
		if source[index] == delimiter && index > start && !unicode.IsSpace(rune(source[index-1])) &&
			(delimiter != '-' || index+1 >= end || !isWordByte(source[index+1])) {
			return index, nil
		}
	}
	return -1, ctx.Err()
}

func escapeJiraDelimitedValueWithContext(ctx context.Context, value, delimiters string) (string, error) {
	return escapeSelectedRunes(ctx, value, delimiters)
}

func renderJiraList(ctx context.Context, list listBlock, parentMarkers string) (string, error) {
	segments, err := renderJiraListSegments(ctx, list, parentMarkers)
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

func renderJiraListSegments(ctx context.Context, list listBlock, parentMarkers string) ([]jiraListRenderSegment, error) {
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
		content, err := renderJiraInlines(ctx, item.Inlines, false)
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
				childSegments, err := renderJiraListSegments(ctx, child, marker)
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
				childSegments, err := renderJiraListSegments(ctx, child, "")
				if err != nil {
					return nil, err
				}
				for _, segment := range childSegments {
					appendSegment(segment)
				}
				continue
			}
			content, err := renderJiraMarkup(ctx, semanticDocument{Blocks: []semanticBlock{block}})
			if err != nil {
				return nil, err
			}
			appendSegment(jiraListRenderSegment{text: content})
		}
	}
	flushLines()
	return segments, nil
}

func renderJiraTable(ctx context.Context, table tableBlock) (string, error) {
	if table.Directive && table.Raw != "" {
		return table.Raw, nil
	}
	rows := make([]string, 0, len(table.Rows)+1)
	if len(table.Header) != 0 {
		header, err := renderJiraTableRow(ctx, table.Header, "||")
		if err != nil {
			return "", err
		}
		rows = append(rows, header)
	}
	for _, row := range table.Rows {
		value, err := renderJiraTableRow(ctx, row, "|")
		if err != nil {
			return "", err
		}
		rows = append(rows, value)
	}
	return strings.Join(rows, "\n"), nil
}

func renderJiraTableRow(ctx context.Context, cells []tableCell, delimiter string) (string, error) {
	values := make([]string, len(cells))
	for index, cell := range cells {
		value, err := renderJiraInlines(ctx, cell.Inlines, false)
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
