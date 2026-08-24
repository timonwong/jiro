package markup

import (
	"context"
	"html"
	"strings"
	"unicode/utf8"
)

// parseJiraInlines reads source[start:end] as one Jira inline run. domain is
// the line context the run stands in -- a physical line, or one table cell --
// which the forced-newline rule needs and whose end reaches past end whenever
// the run is nested inside a line, such as the content of a Text Effect. A
// recursive call passes it through unchanged, because Jira decides a forced
// newline on the raw line and sees no inline structure there; the one exception
// is a link's visible text, which Jira reads with no line domain at all.
func parseJiraInlines(ctx context.Context, source string, start, end int, domain jiraLineDomain) ([]semanticInline, []conversionDiagnostic, error) {
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
	// the same end, and a candidate's deadness is decided by the byte before it
	// rather than by where the scan began, so a scan that found no closer
	// cannot succeed from a later start. The Effect Delimiter gate preserves
	// that property: it reads only the candidate's own neighbouring runes,
	// which do not depend on the scan start, and its index > start bound only
	// rejects more candidates as the start moves right. A killed opener is the
	// one outcome that is not memoizable, and findStyleCloser keeps it out.
	// Do not reuse these helpers for scans over other strings or ranges; the
	// Monospace Span closer in particular ignores backslash escapes and uses
	// its own memo.
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
	findStyleCloser := func(from int, delimiter byte) (int, int, bool, error) {
		// The kill is read before the memo and never recorded in it: it gives
		// up one opener rather than exhausting the run, so a later opener with
		// the same delimiter can still pair, as the bold `c` of `*a*b* *c*`.
		if jiraEffectOpenerKilled(source, from, end, delimiter) {
			return -1, -1, true, nil
		}
		if failedFrom, ok := failedStyleScans[delimiter]; ok && from >= failedFrom {
			return -1, -1, false, nil
		}
		closeStart, closeEnd, killed, err := findJiraEffectClose(ctx, source, from, end, delimiter)
		if err == nil && closeStart < 0 && !killed {
			if failedStyleScans == nil {
				failedStyleScans = make(map[byte]int)
			}
			failedStyleScans[delimiter] = from
		}
		return closeStart, closeEnd, killed, err
	}

	for offset := start; offset < end; {
		if (offset-start)&255 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
		}
		if source[offset] == '\\' {
			runEnd := jiraBackslashRunEnd(source, offset, end)
			// Jira's emoticon escape consumes exactly one backslash directly in
			// front of a token the gate fires on and shows the token's
			// characters. It runs before every other backslash rule, so the run
			// left in front of it is one backslash shorter than it looks:
			// `\\(x)` shows one backslash and no icon, while `\\\(x)` still
			// breaks the line. A gate the following word rune suppresses
			// consumes nothing, which is why `a\:)b` keeps its backslash.
			if tokenLength := jiraEmoticonAt(source, runEnd, end); tokenLength != 0 {
				tokenEnd := runEnd + tokenLength
				// The backslashes in front of the consumed one keep their
				// ordinary meaning, so they are decided here and the loop
				// re-enters on the escape itself.
				if leadEnd := runEnd - 1; leadEnd > offset {
					if jiraForcedNewlineRunFrom(source, offset, leadEnd, tokenEnd, domain.End) {
						if name, unbreakable := jiraUnbreakableConstructNames[domain.Unbreakable]; unbreakable {
							diagnostics = append(diagnostics, conversionDiagnostic{offset: offset, warning: ConversionWarning{Construct: domain.Unbreakable, Reason: "Jira would render a forced newline inside this " + name + "; a JFM " + name + " cannot carry one, so the characters stay literal"}})
							offset = leadEnd
							continue
						}
						flushText(offset)
						result = append(result, hardBreakInline{Span: sourceSpan{Start: offset, End: leadEnd}})
						offset, textStart = leadEnd, leadEnd
						continue
					}
					// Every remaining backslash is a character Jira shows.
					offset = leadEnd
					continue
				}
				flushText(offset)
				result = append(result, textInline{Span: sourceSpan{Start: offset, End: tokenEnd}, Text: source[runEnd:tokenEnd]})
				offset, textStart = tokenEnd, tokenEnd
				continue
			}
			if jiraForcedNewlineRun(source, offset, runEnd, domain.End) {
				if name, unbreakable := jiraUnbreakableConstructNames[domain.Unbreakable]; unbreakable {
					// The construct around this run has no JFM spelling that
					// carries a hard break, so writing one would read back as
					// characters Jira never showed. The backslashes stay in the
					// text and the loss is reported instead.
					diagnostics = append(diagnostics, conversionDiagnostic{offset: offset, warning: ConversionWarning{Construct: domain.Unbreakable, Reason: "Jira would render a forced newline inside this " + name + "; a JFM " + name + " cannot carry one, so the characters stay literal"}})
					offset = runEnd
					continue
				}
				flushText(offset)
				// A run that ends the line takes the newline with it, because
				// the JFM hard break already carries one.
				breakEnd := runEnd
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
			if runEnd-offset == 1 && offset+1 < end && isJiraEscapable(source[offset+1]) {
				flushText(offset)
				result = append(result, textInline{Span: sourceSpan{Start: offset, End: offset + 2}, Text: source[offset+1 : offset+2]})
				offset, textStart = offset+2, offset+2
				continue
			}
			// Every remaining backslash is a character Jira shows, so the run
			// stays in the flushed text and escapes nothing behind it.
			offset = runEnd
			continue
		}

		// An unescaped token the gate fires on is the icon Jira renders, and JFM
		// spells it as a directive so that the icon and the prose that merely
		// looks like one stay apart (ADR-0019). None of `(`, `:` and `;` opens
		// any other construct, so the branch may stand wherever the scan can
		// reach the token's first byte.
		if tokenLength := jiraEmoticonAt(source, offset, end); tokenLength != 0 && !jiraBackslashPrecedes(source, offset) {
			if token, supported := canonicalJiraEmoticonToken(source[offset : offset+tokenLength]); supported {
				flushText(offset)
				result = append(result, emoticonInline{Span: sourceSpan{Start: offset, End: offset + tokenLength}, Token: token})
				offset, textStart = offset+tokenLength, offset+tokenLength
				continue
			}
		}

		// The character references jiro writes to keep an emoticon-shaped
		// literal visible read back as the characters they stand for, so that
		// ordinary text survives a JFM round trip as ordinary text. No other
		// reference is decoded in plain text.
		if token, encodedLength := jiraNeutralizedEmoticonAt(source, offset, end); encodedLength != 0 {
			flushText(offset)
			result = append(result, textInline{Span: sourceSpan{Start: offset, End: offset + encodedLength}, Text: token})
			offset, textStart = offset+encodedLength, offset+encodedLength
			continue
		}

		if strings.HasPrefix(source[offset:end], "{{") && !jiraBackslashPrecedes(source, offset) &&
			(failedMonospaceScan < 0 || offset+2 < failedMonospaceScan) {
			close, bodyEnd, ok, err := jiraMonospaceSpanEnd(ctx, source, start, offset, end)
			if err != nil {
				return nil, nil, err
			}
			if close < 0 {
				failedMonospaceScan = offset + 2
			}
			if ok {
				// A backslash Jira consumes in front of the closer is gone from
				// the visible text, so the body jiro reads and round-trips stops
				// in front of it: `{{a\}}` is the code span `a`.
				raw := source[offset+2 : bodyEnd]
				// Jira reads U+200B as a Monospace Span boundary rather than as
				// content, so a rune touching the outside of the braces is
				// delimiter protection and never reaches the JFM output.
				textEnd := offset
				if strings.HasSuffix(source[textStart:offset], "\u200b") {
					textEnd -= len("\u200b")
				}
				flushText(textEnd)
				body, err := decodeJiraEscapes(ctx, raw)
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
				reinterpreted, warned, err := jiraMonospaceReinterpretation(ctx, source, offset+2, bodyEnd, domain.End)
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

		if source[offset] == '[' && !jiraBackslashPrecedes(source, offset) {
			close, err := findCloser(offset+1, "]")
			if err != nil {
				return nil, nil, err
			}
			// A link body whose last character is a backslash is not a link:
			// Jira shows the markup, whether the backslash protected the closing
			// bracket or was consumed before it.
			if close >= 0 && !jiraValueEndsInBackslash(source[offset+1:close]) {
				flushText(offset)
				body := source[offset+1 : close]
				// Jira splits the body on every `|`: the first part is the
				// visible text, the second the target, and everything after it
				// the link title.
				separator := jiraUnprotectedSplit(body, 0, '|')
				labelEnd, labelPart, targetPart, unnamed, titled := close, "", body, true, false
				if separator >= 0 {
					labelEnd, labelPart, targetPart, unnamed = offset+1+separator, body[:separator], body[separator+1:], false
					if titleStart := jiraUnprotectedSplit(targetPart, 0, '|'); titleStart >= 0 {
						targetPart, titled = targetPart[:titleStart], true
					}
				}
				target, err := decodeJiraLinkTarget(ctx, targetPart)
				if err != nil {
					return nil, nil, err
				}
				// A backslash before one of those separators protects nothing
				// and Jira splits there anyway. Where the target it is left with
				// is not one Markdown can carry, Jira resolves nothing and shows
				// an error span, so the bracket stays the text Jira shows.
				if (jiraValueEndsInBackslash(labelPart) || jiraValueEndsInBackslash(targetPart)) && linkNeedsDirective(target) {
					result = append(result, literalInline{Span: sourceSpan{Start: offset, End: close + 1}, Text: source[offset : close+1]})
					diagnostics = append(diagnostics, conversionDiagnostic{offset: offset, warning: ConversionWarning{Construct: ConstructLink, Reason: "backslash before a Jira link separator leaves a target Jira cannot resolve; complete link remains literal"}})
					offset, textStart = close+1, close+1
					continue
				}
				if titled {
					diagnostics = append(diagnostics, conversionDiagnostic{offset: offset, warning: ConversionWarning{Construct: ConstructLink, Reason: "Jira link title is dropped; jiro carries no link title (#104)"}})
				}
				// The label is parsed from the source rather than from a decoded
				// copy: Jira shows an escaped delimiter in link text as the
				// character, so decoding first would let `[a \-b\- c|url]` read
				// back as a strikethrough Jira never renders.
				// Jira reads a link's visible text without the forced-newline
				// rule: `[a\\b|http://x]` shows both backslashes, while the
				// same pair outside the brackets breaks.
				label, nestedDiagnostics, err := parseJiraInlines(ctx, source, offset+1, labelEnd, jiraLineDomain{End: jiraNoForcedNewlineDomain})
				if err != nil {
					return nil, nil, err
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
			destination, attributes, isImage := "", []directiveAttribute(nil), false
			if close >= 0 {
				destination, attributes, isImage = parseJiraImageBody(source[offset+1:close], offset+1)
			}
			// An image body Jira refuses is not an image at all: the run stays
			// text, which is what Jira renders there.
			if isImage {
				flushText(offset)
				alt, preservedAttributes, attributeDiagnostics, invalid := validateDirectiveAttributes(attributes, jiraImageAttributeSchema)
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
					Alt:        alt.Value,
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
				value, err := decodeJiraMacroParameterValue(ctx, source[offset+7:openEnd])
				if err != nil {
					return nil, nil, err
				}
				if value != "" {
					flushText(offset)
					children, nestedDiagnostics, err := parseJiraInlines(ctx, source, openEnd+1, close, domain)
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
			closeStart, closeEnd, killed, err := findStyleCloser(token.End, token.Delimiter)
			if err != nil {
				return nil, nil, err
			}
			if killed {
				// A killed opener opens nothing and is text; the scan rereads
				// from the byte after it, so the `*` inside `{*}` of `{*}a*b*`
				// still gets its turn.
				offset = token.Start + 1
				continue
			}
			if closeStart >= 0 {
				flushText(offset)
				children, nestedDiagnostics, err := parseJiraInlines(ctx, source, token.End, closeStart, domain)
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

// decodeJiraEscapes resolves the backslash escapes Jira consumes wherever it
// reads inline markup, which is the grammar's escapable set. Only a lone
// backslash escapes: a run of two or more is characters Jira shows and escapes
// nothing behind it, so `a\\\b` keeps all three and `a\\\*b` keeps the `*` as
// well. A backslash before any other character is text Jira shows, so `C:\temp`
// survives. A delimited value such as a link target or an image parameter is
// not this: each is read with the decoder of its own context in
// jira_value_grammar.go.
func decodeJiraEscapes(ctx context.Context, value string) (string, error) {
	if !strings.Contains(value, `\`) {
		return value, ctx.Err()
	}
	var result strings.Builder
	result.Grow(len(value))
	for index := 0; index < len(value); {
		if index&255 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		if value[index] == '\\' {
			runEnd := jiraBackslashRunEnd(value, index, len(value))
			if runEnd-index == 1 && index+1 < len(value) && isJiraEscapable(value[index+1]) {
				result.WriteByte(value[index+1])
				index += 2
				continue
			}
			result.WriteString(value[index:runEnd])
			index = runEnd
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

// parseJiraImageBody reads the body of a `!...!` into an image source and its
// parameters, and reports whether Jira reads an image there at all.
func parseJiraImageBody(body string, base int) (string, []directiveAttribute, bool) {
	if jiraValueEndsInBackslash(body) {
		return "", nil, false
	}
	separator := jiraUnprotectedSplit(body, 0, '|')
	if separator < 0 {
		return decodeJiraImageValue(body), nil, true
	}
	destination := decodeJiraImageValue(body[:separator])
	attributes := make([]directiveAttribute, 0)
	for attributeStart := separator + 1; attributeStart <= len(body); {
		next := jiraUnprotectedSplit(body, attributeStart, ',')
		if next < 0 {
			next = len(body)
		}
		part := body[attributeStart:next]
		name, value, bare := part, "", true
		if equals := jiraUnprotectedSplit(part, 0, '='); equals >= 0 {
			if jiraImageParameterValueRefused(part[equals+1:]) {
				return "", nil, false
			}
			// The name is read verbatim for the same reason as the value: Jira
			// consumes nothing inside an image.
			name, value, bare = part[:equals], decodeJiraImageValue(part[equals+1:]), false
		}
		attributes = append(attributes, directiveAttribute{Span: sourceSpan{Start: base + attributeStart, End: base + next}, Name: name, Value: value, Bare: bare})
		if next == len(body) {
			break
		}
		attributeStart = next + 1
	}
	return destination, attributes, true
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
	case emoticonInline:
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
// inside the undecoded Monospace Span body source[bodyStart:bodyEnd] instead of
// showing its characters. The body is read in place rather than as a detached
// string because the forced-newline rule is decided on the whole line: the pair
// in `{{a\\b}}-c\\d` is two characters Jira shows, while the same body alone
// would break. Emoticon, dash, macro and escape reinterpretations stay silent:
// they are Jira misrendering code, not semantics jiro drops. An autolink of any
// scheme, including mailto, is also silent: Jira's autolinker leaves the
// address visible and a REST read returns the raw markup unchanged, so nothing
// is lost by leaving it raw.
func jiraMonospaceReinterpretation(ctx context.Context, source string, bodyStart, bodyEnd, lineEnd int) (string, bool, error) {
	hazards, err := jiraInlineHazards(ctx, source, bodyStart, bodyEnd, lineEnd, jiraMonospaceContext, false)
	if err != nil {
		return "", false, err
	}
	for _, hazard := range hazards {
		switch hazard.Kind {
		case jiraHazardEffect:
			return jiraEffectReinterpretations[hazard.Style], true, nil
		case jiraHazardCitation:
			return "a citation", true, nil
		case jiraHazardForcedNewline:
			return "a forced newline", true, nil
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
