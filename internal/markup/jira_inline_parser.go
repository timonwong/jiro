package markup

import (
	"context"
	"html"
	"strings"
	"unicode/utf8"
)

func parseJiraInlines(ctx context.Context, source string, start, end int) ([]semanticInline, []conversionDiagnostic, error) {
	result := make([]semanticInline, 0)
	diagnostics := make([]conversionDiagnostic, 0)
	textStart := start
	flushText := func(stop int) {
		if stop <= textStart {
			return
		}
		value := strings.ReplaceAll(source[textStart:stop], "\n", " ")
		result = append(result, textInline{Span: sourceSpan{Start: textStart, End: stop}, Text: value})
	}

	// A run without a `}}` anywhere after an opener has none after any later
	// opener either, so one exhausted scan settles the rest of the run.
	failedMonospaceScan := -1
	// Failed closer scans are memoized per delimiter: every scan below shares
	// the same end and starts directly after an opener byte that is never a
	// backslash, so escapedness of any candidate closer is identical for every
	// scan start and a scan that found no closer cannot succeed from a later
	// start. The Effect Delimiter gate preserves that property: it reads only
	// the candidate's own neighbouring runes, which do not depend on the scan
	// start, and its index > start bound only rejects more candidates as the
	// start moves right. Do not reuse these helpers for scans over other
	// strings or ranges; the Monospace Span closer in particular ignores
	// backslash escapes and uses its own memo.
	var failedCloserScans map[string]int
	findCloser := func(from int, delimiter string) (int, error) {
		if failedFrom, ok := failedCloserScans[delimiter]; ok && from >= failedFrom {
			return -1, nil
		}
		close, err := findUnescaped(ctx, source, from, end, delimiter)
		if err == nil && close < 0 {
			if failedCloserScans == nil {
				failedCloserScans = make(map[string]int)
			}
			failedCloserScans[delimiter] = from
		}
		return close, err
	}
	var failedStyleScans map[byte]int
	findStyleCloser := func(from int, delimiter byte) (int, int, error) {
		if failedFrom, ok := failedStyleScans[delimiter]; ok && from >= failedFrom {
			return -1, -1, nil
		}
		closeStart, closeEnd, err := findJiraEffectClose(ctx, source, from, end, delimiter)
		if err == nil && closeStart < 0 {
			if failedStyleScans == nil {
				failedStyleScans = make(map[byte]int)
			}
			failedStyleScans[delimiter] = from
		}
		return closeStart, closeEnd, err
	}

	for offset := start; offset < end; {
		if (offset-start)&255 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
		}
		if source[offset] == '\\' {
			if offset+1 < end && source[offset+1] == '\\' {
				next := offset + 2
				if next == end || source[next] == '\n' || source[next] == '\r' {
					flushText(offset)
					breakEnd := next
					if breakEnd < end && source[breakEnd] == '\r' {
						breakEnd++
						if breakEnd < end && source[breakEnd] == '\n' {
							breakEnd++
						}
					} else if breakEnd < end && source[breakEnd] == '\n' {
						breakEnd++
					}
					result = append(result, hardBreakInline{Span: sourceSpan{Start: offset, End: breakEnd}})
					offset, textStart = breakEnd, breakEnd
					continue
				}
			}
			if offset+1 < end && strings.ContainsRune(jiraEscapableCharacters, rune(source[offset+1])) {
				flushText(offset)
				result = append(result, textInline{Span: sourceSpan{Start: offset, End: offset + 2}, Text: source[offset+1 : offset+2]})
				offset, textStart = offset+2, offset+2
				continue
			}
		}

		if strings.HasPrefix(source[offset:end], "{{") && (failedMonospaceScan < 0 || offset+2 < failedMonospaceScan) {
			close, ok, err := jiraMonospaceSpanEnd(ctx, source, start, offset, end)
			if err != nil {
				return nil, nil, err
			}
			if close < 0 {
				failedMonospaceScan = offset + 2
			}
			if ok {
				raw := source[offset+2 : close]
				// Jira reads U+200B as a Monospace Span boundary rather than as
				// content, so a rune touching the outside of the braces is
				// delimiter protection and never reaches the JFM output.
				textEnd := offset
				if strings.HasSuffix(source[textStart:offset], "\u200b") {
					textEnd -= len("\u200b")
				}
				flushText(textEnd)
				body, err := decodeJiraDelimitedText(ctx, raw)
				if err != nil {
					return nil, nil, err
				}
				body = decodeJiraEntities(body)
				body, err = removeLegacyCodeSafetyRunes(ctx, body)
				if err != nil {
					return nil, nil, err
				}
				result = append(result, codeInline{Span: sourceSpan{Start: offset, End: close + 2}, Text: body})
				// The hazard scan reads the undecoded body: character references
				// and backslash escapes are what stop Jira from reinterpreting it.
				reinterpreted, warned, err := jiraMonospaceReinterpretation(ctx, raw)
				if err != nil {
					return nil, nil, err
				}
				if warned {
					diagnostics = append(diagnostics, conversionDiagnostic{offset: offset, warning: ConversionWarning{Construct: ConstructInlineCode, Reason: "Jira would render " + reinterpreted + " inside this Monospace Span; inline code keeps the characters literal"}})
				}
				next := close + 2
				if strings.HasPrefix(source[next:end], "\u200b") {
					next += len("\u200b")
				}
				offset, textStart = next, next
				continue
			}
		}

		if source[offset] == '[' {
			close, err := findCloser(offset+1, "]")
			if err != nil {
				return nil, nil, err
			}
			if close >= 0 {
				flushText(offset)
				body := source[offset+1 : close]
				separator, err := findUnescaped(ctx, body, 0, len(body), "|")
				if err != nil {
					return nil, nil, err
				}
				labelText, target, unnamed := body, body, true
				if separator >= 0 {
					labelText, target, unnamed = body[:separator], body[separator+1:], false
				}
				labelText, err = decodeJiraDelimitedText(ctx, labelText)
				if err != nil {
					return nil, nil, err
				}
				target, err = decodeJiraDelimitedText(ctx, target)
				if err != nil {
					return nil, nil, err
				}
				label, nestedDiagnostics, err := parseJiraInlines(ctx, labelText, 0, len(labelText))
				if err != nil {
					return nil, nil, err
				}
				for labelIndex, child := range label {
					label[labelIndex] = shiftSemanticInline(child, offset+1)
				}
				diagnostics = append(diagnostics, nestedDiagnostics...)
				_, dangerous := dangerousDestinationScheme([]byte(strings.TrimLeftFunc(target, unicodeSpaceOrControl)))
				result = append(result, linkInline{
					Span:      sourceSpan{Start: offset, End: close + 1},
					Label:     label,
					Target:    target,
					Unnamed:   unnamed,
					Directive: linkNeedsDirective(target) || dangerous,
					Dangerous: dangerous,
				})
				if dangerous {
					diagnostics = append(diagnostics, conversionDiagnostic{offset: offset, warning: ConversionWarning{Construct: ConstructLink, Reason: "dangerous destination scheme requires a reversible link directive"}})
				}
				offset, textStart = close+1, close+1
				continue
			}
		}

		if source[offset] == '!' {
			close, err := findCloser(offset+1, "!")
			if err != nil {
				return nil, nil, err
			}
			if close >= 0 {
				flushText(offset)
				body := source[offset+1 : close]
				destination, attributes, err := parseJiraImageBody(ctx, body, offset+1)
				if err != nil {
					return nil, nil, err
				}
				alt, preservedAttributes, attributeDiagnostics, invalid := validateJiraImageAttributes(attributes)
				diagnostics = append(diagnostics, attributeDiagnostics...)
				if invalid {
					result = append(result, literalInline{Span: sourceSpan{Start: offset, End: close + 1}, Text: source[offset : close+1]})
					offset, textStart = close+1, close+1
					continue
				}
				_, dangerous := dangerousDestinationScheme([]byte(strings.TrimLeftFunc(destination, unicodeSpaceOrControl)))
				directive := dangerous || len(preservedAttributes) != 0
				result = append(result, imageInline{
					Span:       sourceSpan{Start: offset, End: close + 1},
					Alt:        alt,
					Source:     destination,
					Attributes: preservedAttributes,
					Directive:  directive,
					Dangerous:  dangerous,
				})
				if dangerous {
					diagnostics = append(diagnostics, conversionDiagnostic{offset: offset, warning: ConversionWarning{Construct: ConstructImage, Reason: "dangerous destination scheme requires a reversible image directive"}})
				}
				offset, textStart = close+1, close+1
				continue
			}
		}

		if strings.HasPrefix(source[offset:end], "{color:") {
			openEnd, err := findCloser(offset+7, "}")
			if err != nil {
				return nil, nil, err
			}
			close := -1
			if openEnd >= 0 {
				close, err = findCloser(openEnd+1, "{color}")
				if err != nil {
					return nil, nil, err
				}
			}
			if close >= 0 {
				value, err := decodeJiraDelimitedText(ctx, source[offset+7:openEnd])
				if err != nil {
					return nil, nil, err
				}
				if value != "" {
					flushText(offset)
					children, nestedDiagnostics, err := parseJiraInlines(ctx, source, openEnd+1, close)
					if err != nil {
						return nil, nil, err
					}
					diagnostics = append(diagnostics, nestedDiagnostics...)
					result = append(result, styledInline{
						Span:     sourceSpan{Start: offset, End: close + len("{color}")},
						Style:    styleColor,
						Value:    value,
						Children: children,
					})
					offset, textStart = close+len("{color}"), close+len("{color}")
					continue
				}
			}
			fallbackEnd := end
			if openEnd >= 0 {
				if close >= 0 {
					fallbackEnd = close + len("{color}")
				} else {
					fallbackEnd = openEnd + 1
				}
			}
			flushText(offset)
			result = append(result, literalInline{Span: sourceSpan{Start: offset, End: fallbackEnd}, Text: source[offset:fallbackEnd]})
			diagnostics = append(diagnostics, conversionDiagnostic{offset: offset, warning: ConversionWarning{Construct: ConstructJiraMacro, Reason: "empty or malformed color macro remains literal"}})
			offset, textStart = fallbackEnd, fallbackEnd
			continue
		}

		if token, opens, scanned := jiraEffectOpener(source, start, offset, end); opens {
			closeStart, closeEnd, err := findStyleCloser(token.End, token.Delimiter)
			if err != nil {
				return nil, nil, err
			}
			if closeStart >= 0 {
				flushText(offset)
				children, nestedDiagnostics, err := parseJiraInlines(ctx, source, token.End, closeStart)
				if err != nil {
					return nil, nil, err
				}
				diagnostics = append(diagnostics, nestedDiagnostics...)
				result = append(result, styledInline{Span: sourceSpan{Start: offset, End: closeEnd}, Style: token.Style, Children: children})
				offset, textStart = closeEnd, closeEnd
				continue
			}
			offset = scanned
			continue
		} else if scanned > offset {
			offset = scanned
			continue
		}

		_, size := utf8.DecodeRuneInString(source[offset:end])
		offset += size
	}
	flushText(end)
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	return mergeAdjacentTextInlines(result), diagnostics, nil
}

func findUnescaped(ctx context.Context, source string, start, end int, delimiter string) (int, error) {
	for index := start; index+len(delimiter) <= end; index++ {
		if (index-start)&255 == 0 {
			if err := ctx.Err(); err != nil {
				return -1, err
			}
		}
		if source[index] == '\\' {
			index++
			continue
		}
		if strings.HasPrefix(source[index:end], delimiter) {
			return index, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return -1, err
	}
	return -1, nil
}

func decodeJiraDelimitedText(ctx context.Context, value string) (string, error) {
	var result strings.Builder
	for index := 0; index < len(value); {
		if index&255 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		if value[index] == '\\' && index+1 < len(value) && strings.ContainsRune(`\{}[]|!*_-+^~,=`, rune(value[index+1])) {
			result.WriteByte(value[index+1])
			index += 2
			continue
		}
		result.WriteByte(value[index])
		index++
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return result.String(), nil
}

func decodeJiraEntities(value string) string {
	return html.UnescapeString(value)
}

func removeLegacyCodeSafetyRunes(ctx context.Context, value string) (string, error) {
	characters := []rune(value)
	var result strings.Builder
	for index, character := range characters {
		if index&255 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		if character == '\u200b' && index > 0 && characters[index-1] == '\\' &&
			(index == len(characters)-1 || strings.ContainsRune(legacyCodeSpanEscapedDelimiters, characters[index+1])) {
			continue
		}
		result.WriteRune(character)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return result.String(), nil
}

func linkNeedsDirective(target string) bool {
	if strings.HasPrefix(target, "#") {
		return false
	}
	lower := strings.ToLower(target)
	return !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") && !strings.HasPrefix(lower, "mailto:")
}

func parseJiraImageBody(ctx context.Context, body string, base int) (string, []directiveAttribute, error) {
	separator, err := findUnescaped(ctx, body, 0, len(body), "|")
	if err != nil {
		return "", nil, err
	}
	if separator < 0 {
		destination, err := decodeJiraDelimitedText(ctx, body)
		return destination, nil, err
	}
	destination, err := decodeJiraDelimitedText(ctx, body[:separator])
	if err != nil {
		return "", nil, err
	}
	attributes := make([]directiveAttribute, 0)
	for attributeStart := separator + 1; attributeStart <= len(body); {
		next, err := findUnescaped(ctx, body, attributeStart, len(body), ",")
		if err != nil {
			return "", nil, err
		}
		if next < 0 {
			next = len(body)
		}
		part := body[attributeStart:next]
		name, value, bare := part, "", true
		equals, err := findUnescaped(ctx, part, 0, len(part), "=")
		if err != nil {
			return "", nil, err
		}
		if equals >= 0 {
			value, err = decodeJiraDelimitedText(ctx, part[equals+1:])
			if err != nil {
				return "", nil, err
			}
			name, bare = part[:equals], false
		}
		name, err = decodeJiraDelimitedText(ctx, name)
		if err != nil {
			return "", nil, err
		}
		attributes = append(attributes, directiveAttribute{Span: sourceSpan{Start: base + attributeStart, End: base + next}, Name: name, Value: value, Bare: bare})
		if next == len(body) {
			break
		}
		attributeStart = next + 1
	}
	return destination, attributes, nil
}

func validateJiraImageAttributes(attributes []directiveAttribute) (string, []directiveAttribute, []conversionDiagnostic, bool) {
	known := map[string]string{
		"alt": "alt", "thumbnail": "thumbnail", "align": "align", "border": "border",
		"bordercolor": "bordercolor", "hspace": "hspace", "vspace": "vspace",
		"width": "width", "height": "height", "title": "title",
	}
	alt := ""
	foundAlt := false
	preserved := make([]directiveAttribute, 0, len(attributes))
	diagnostics := make([]conversionDiagnostic, 0)
	seen := map[string]bool{}
	invalid := false
	for _, attribute := range attributes {
		if !validDirectiveAttributeName(attribute.Name) {
			diagnostics = append(diagnostics, conversionDiagnostic{offset: attribute.Span.Start, warning: ConversionWarning{Construct: ConstructImage, Reason: "Jira image parameter name cannot be represented by JFM attribute grammar; complete image remains literal"}})
			invalid = true
			continue
		}
		key := strings.ToLower(attribute.Name)
		canonical, knownAttribute := known[key]
		if knownAttribute {
			attribute.Name = canonical
		} else {
			diagnostics = append(diagnostics, conversionDiagnostic{offset: attribute.Span.Start, warning: ConversionWarning{Construct: ConstructDirective, Reason: "unknown image attribute is preserved"}})
		}
		if seen[key] {
			diagnostics = append(diagnostics, conversionDiagnostic{offset: attribute.Span.Start, warning: ConversionWarning{Construct: ConstructDirective, Reason: "duplicate image attribute is preserved"}})
		}
		seen[key] = true
		if key == "alt" && !attribute.Bare && !foundAlt {
			alt, foundAlt = attribute.Value, true
			continue
		}
		if key != "thumbnail" && attribute.Bare {
			diagnostics = append(diagnostics, conversionDiagnostic{offset: attribute.Span.Start, warning: ConversionWarning{Construct: ConstructImage, Reason: "Jira image parameter without a value cannot be represented by JFM; complete image remains literal"}})
			invalid = true
		}
		if key == "thumbnail" && !attribute.Bare {
			diagnostics = append(diagnostics, conversionDiagnostic{offset: attribute.Span.Start, warning: ConversionWarning{Construct: ConstructDirective, Reason: "thumbnail value is preserved although thumbnail is presence-only in JFM"}})
		}
		preserved = append(preserved, attribute)
	}
	return alt, preserved, diagnostics, invalid
}

func shiftSemanticInline(inline semanticInline, delta int) semanticInline {
	switch typed := inline.(type) {
	case textInline:
		typed.Span.Start += delta
		typed.Span.End += delta
		return typed
	case codeInline:
		typed.Span.Start += delta
		typed.Span.End += delta
		return typed
	case hardBreakInline:
		typed.Span.Start += delta
		typed.Span.End += delta
		return typed
	case styledInline:
		typed.Span.Start += delta
		typed.Span.End += delta
		for index, child := range typed.Children {
			typed.Children[index] = shiftSemanticInline(child, delta)
		}
		return typed
	case linkInline:
		typed.Span.Start += delta
		typed.Span.End += delta
		for index, child := range typed.Label {
			typed.Label[index] = shiftSemanticInline(child, delta)
		}
		return typed
	case imageInline:
		typed.Span.Start += delta
		typed.Span.End += delta
		return typed
	case literalInline:
		typed.Span.Start += delta
		typed.Span.End += delta
		return typed
	default:
		return inline
	}
}

func mergeAdjacentTextInlines(inlines []semanticInline) []semanticInline {
	result := make([]semanticInline, 0, len(inlines))
	for _, inline := range inlines {
		text, ok := inline.(textInline)
		if ok && len(result) != 0 {
			if previous, previousOK := result[len(result)-1].(textInline); previousOK && previous.Span.End == text.Span.Start {
				previous.Span.End, previous.Text = text.Span.End, previous.Text+text.Text
				result[len(result)-1] = previous
				continue
			}
		}
		result = append(result, inline)
	}
	return result
}

const legacyCodeSpanEscapedDelimiters = `{}[]|-*_`

// jiraMonospaceReinterpretation names the first construct Jira would render
// inside the undecoded Monospace Span body instead of showing its characters.
// Emoticon, dash, macro and escape reinterpretations stay silent: they are Jira
// misrendering code, not semantics jiro drops. An autolink of any scheme,
// including mailto, is also silent: Jira's autolinker leaves the address
// visible and a REST read returns the raw markup unchanged, so nothing is
// lost by leaving it raw.
func jiraMonospaceReinterpretation(ctx context.Context, body string) (string, bool, error) {
	hazards, err := jiraInlineHazards(ctx, body, 0, len(body), jiraMonospaceContext, false)
	if err != nil {
		return "", false, err
	}
	for _, hazard := range hazards {
		switch hazard.Kind {
		case jiraHazardEffect:
			return jiraEffectReinterpretations[hazard.Style], true, nil
		case jiraHazardCitation:
			return "a citation", true, nil
		case jiraHazardLink:
			if hazard.TextChanges {
				return "a link", true, nil
			}
		}
	}
	return "", false, nil
}

var jiraEffectReinterpretations = map[inlineStyle]string{
	styleBold:     "a bold effect",
	styleItalic:   "an italic effect",
	styleStrike:   "a strikethrough effect",
	styleInserted: "an inserted effect",
	styleSuper:    "a superscript effect",
	styleSub:      "a subscript effect",
}
