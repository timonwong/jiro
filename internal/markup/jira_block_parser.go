package markup

import (
	"context"
	"regexp"
	"strings"
	"unicode"
)

var jiraListLinePattern = regexp.MustCompile(`^([*#]+)(?: (.*))?$`)

func isJiraBlockStart(line string) bool {
	if jiraHeadingPattern.MatchString(line) || jiraHeadingLikePattern.MatchString(line) || line == "----" || jiraListLinePattern.MatchString(line) ||
		strings.HasPrefix(line, "||") || strings.HasPrefix(line, "|") ||
		strings.HasPrefix(line, "bq.") {
		return true
	}
	return line == "{quote}" || line == "{noformat}" || strings.HasPrefix(line, "{code}") || strings.HasPrefix(line, "{code:") ||
		line == "{panel}" || strings.HasPrefix(line, "{panel:")
}

var jiraBlockMacroPattern = regexp.MustCompile(`^\{([A-Za-z][A-Za-z0-9-]*)(?::[^}]*)?\}$`)

func parseUnsupportedJiraBlockMacro(ctx context.Context, source string, lines []sourceLine, start, quoteDepth int) (jiraMacroParseResult, bool) {
	match := jiraBlockMacroPattern.FindStringSubmatch(lines[start].Text)
	if match == nil {
		return jiraMacroParseResult{}, false
	}
	name := strings.ToLower(match[1])
	if name == "quote" || name == "noformat" || name == "code" || name == "panel" || name == "color" {
		return jiraMacroParseResult{}, false
	}
	closing := "{" + match[1] + "}"
	closeIndex := start + 1
	for closeIndex < len(lines) && lines[closeIndex].Text != closing {
		closeIndex++
	}
	if closeIndex == len(lines) {
		line := lines[start]
		return jiraMacroParseResult{
			Block: literalBlock{Span: sourceSpan{Start: line.Start, End: line.End}, Text: line.Text},
			Next:  start + 1,
			Diagnostics: []conversionDiagnostic{{offset: line.Start, warning: ConversionWarning{
				Construct: ConstructJiraMacro,
				Reason:    "unsupported Jira macro remains literal",
			}}},
		}, true
	}
	bodyStart := lines[start].End + len(lines[start].EOL)
	bodyEnd := lines[closeIndex].Start
	bodyDocument, bodyDiagnostics, err := parseJiraMarkupAtQuoteDepth(ctx, source[bodyStart:bodyEnd], quoteDepth)
	if err != nil {
		return jiraMacroParseResult{Err: err}, true
	}
	for blockIndex, block := range bodyDocument.Blocks {
		bodyDocument.Blocks[blockIndex] = shiftSemanticBlock(block, bodyStart)
	}
	for diagnosticIndex := range bodyDiagnostics {
		bodyDiagnostics[diagnosticIndex].offset += bodyStart
	}
	bodyDiagnostics = append([]conversionDiagnostic{{offset: lines[start].Start, warning: ConversionWarning{
		Construct: ConstructJiraMacro,
		Reason:    "unsupported Jira macro delimiters remain literal while its body is converted best-effort",
	}}}, bodyDiagnostics...)
	return jiraMacroParseResult{
		Block: unsupportedMacroBlock{
			Span:    sourceSpan{Start: lines[start].Start, End: lines[closeIndex].End},
			Opening: lines[start].Text,
			Closing: lines[closeIndex].Text,
			Blocks:  bodyDocument.Blocks,
		},
		Next:        closeIndex + 1,
		Diagnostics: bodyDiagnostics,
	}, true
}

func parseJiraLists(ctx context.Context, source string, lines []sourceLine, start int) ([]semanticBlock, int, []conversionDiagnostic, error) {
	blocks := make([]semanticBlock, 0)
	diagnostics := make([]conversionDiagnostic, 0)
	index := start
	for index < len(lines) {
		match := jiraListLinePattern.FindStringSubmatch(lines[index].Text)
		if match == nil {
			break
		}
		if len(match[1]) != 1 {
			line := lines[index]
			blocks = append(blocks, literalBlock{Span: sourceSpan{Start: line.Start, End: line.End}, Text: line.Text})
			diagnostics = append(diagnostics, conversionDiagnostic{offset: line.Start, warning: ConversionWarning{Construct: ConstructList, Reason: "list item has no parent at its authored nesting level"}})
			index++
			continue
		}
		block, next, blockDiagnostics, err := parseJiraListLevel(ctx, source, lines, index, "")
		if err != nil {
			return nil, 0, nil, err
		}
		blocks = append(blocks, block)
		diagnostics = append(diagnostics, blockDiagnostics...)
		index = next
	}
	return blocks, index, diagnostics, nil
}

func parseJiraListLevel(ctx context.Context, source string, lines []sourceLine, start int, parentMarkers string) (listBlock, int, []conversionDiagnostic, error) {
	first := jiraListLinePattern.FindStringSubmatch(lines[start].Text)
	level := len(parentMarkers)
	marker := first[1][level]
	expectedPrefix := parentMarkers + string(marker)
	block := listBlock{Ordered: marker == '#', Span: sourceSpan{Start: lines[start].Start}}
	diagnostics := make([]conversionDiagnostic, 0)
	index := start
	for index < len(lines) {
		match := jiraListLinePattern.FindStringSubmatch(lines[index].Text)
		if match == nil || len(match[1]) < len(expectedPrefix) || match[1][:len(expectedPrefix)] != expectedPrefix {
			break
		}
		if len(match[1]) == len(expectedPrefix) && match[1][level] != marker {
			break
		}
		if len(match[1]) != len(expectedPrefix) {
			break
		}
		line := lines[index]
		contentStart := line.Start + len(match[1])
		if match[2] != "" {
			contentStart++
		}
		inlines, inlineDiagnostics, err := parseJiraInlines(ctx, source, contentStart, line.End)
		if err != nil {
			return listBlock{}, 0, nil, err
		}
		diagnostics = append(diagnostics, inlineDiagnostics...)
		item := listItem{Span: sourceSpan{Start: line.Start, End: line.End}, Inlines: inlines}
		index++
		for index < len(lines) {
			nextMatch := jiraListLinePattern.FindStringSubmatch(lines[index].Text)
			if nextMatch == nil || len(nextMatch[1]) <= len(expectedPrefix) || !strings.HasPrefix(nextMatch[1], expectedPrefix) {
				break
			}
			if len(nextMatch[1]) != len(expectedPrefix)+1 {
				diagnostics = append(diagnostics, conversionDiagnostic{offset: lines[index].Start, warning: ConversionWarning{Construct: ConstructList, Reason: "list nesting skips an authored parent level"}})
				break
			}
			child, next, childDiagnostics, err := parseJiraListLevel(ctx, source, lines, index, expectedPrefix)
			if err != nil {
				return listBlock{}, 0, nil, err
			}
			item.Blocks = append(item.Blocks, child)
			diagnostics = append(diagnostics, childDiagnostics...)
			index = next
		}
		item.Span.End = lines[index-1].End
		block.Items = append(block.Items, item)
	}
	if index == start {
		index++
	}
	block.Span.End = lines[index-1].End
	return block, index, diagnostics, nil
}

func parseJiraTable(ctx context.Context, source string, lines []sourceLine, start int) (tableBlock, int, []conversionDiagnostic, error) {
	index := start
	for index < len(lines) && (strings.HasPrefix(lines[index].Text, "||") || strings.HasPrefix(lines[index].Text, "|")) {
		index++
	}
	block := tableBlock{Span: sourceSpan{Start: lines[start].Start, End: lines[index-1].End}}
	rawRows := make([]string, 0, index-start)
	diagnostics := make([]conversionDiagnostic, 0)
	columnCount := -1
	for rowIndex := start; rowIndex < index; rowIndex++ {
		line := lines[rowIndex]
		rawRows = append(rawRows, line.Text)
		header := strings.HasPrefix(line.Text, "||")
		cells, cellDiagnostics, edgeWhitespace, err := parseJiraTableRow(ctx, source, line, header)
		if err != nil {
			return tableBlock{}, 0, nil, err
		}
		diagnostics = append(diagnostics, cellDiagnostics...)
		if edgeWhitespace || columnCount >= 0 && len(cells) != columnCount || rowIndex != start && header {
			block.Directive = true
		}
		if columnCount < 0 {
			columnCount = len(cells)
		}
		for _, cell := range cells {
			if !tableCellSupportsGFM(cell) {
				block.Directive = true
			}
		}
		if rowIndex == start && header {
			block.Header = cells
		} else {
			block.Rows = append(block.Rows, cells)
		}
	}
	if len(block.Header) == 0 {
		block.Directive = true
	}
	block.Raw = strings.Join(rawRows, "\n")
	return block, index, diagnostics, nil
}

func parseJiraTableRow(ctx context.Context, source string, line sourceLine, header bool) ([]tableCell, []conversionDiagnostic, bool, error) {
	delimiter := "|"
	if header {
		delimiter = "||"
	}
	text := line.Text
	innerStart, innerEnd := len(delimiter), len(text)-len(delimiter)
	if innerEnd < innerStart || !strings.HasSuffix(text, delimiter) {
		return []tableCell{{Span: sourceSpan{Start: line.Start, End: line.End}, Inlines: []semanticInline{textInline{Span: sourceSpan{Start: line.Start, End: line.End}, Text: text}}}}, nil, false, nil
	}
	cells := make([]tableCell, 0)
	diagnostics := make([]conversionDiagnostic, 0)
	edgeWhitespace := false
	for cellStart := innerStart; cellStart <= innerEnd; {
		next, err := findUnescaped(ctx, text, cellStart, innerEnd, delimiter)
		if err != nil {
			return nil, nil, false, err
		}
		if next < 0 {
			next = innerEnd
		}
		value := text[cellStart:next]
		if strings.TrimSpace(value) != value {
			edgeWhitespace = true
		}
		absoluteStart := line.Start + cellStart
		inlines, inlineDiagnostics, err := parseJiraInlines(ctx, source, absoluteStart, line.Start+next)
		if err != nil {
			return nil, nil, false, err
		}
		diagnostics = append(diagnostics, inlineDiagnostics...)
		cells = append(cells, tableCell{Span: sourceSpan{Start: absoluteStart, End: line.Start + next}, Inlines: inlines})
		if next == innerEnd {
			break
		}
		cellStart = next + len(delimiter)
	}
	return cells, diagnostics, edgeWhitespace, nil
}

func tableCellSupportsGFM(cell tableCell) bool {
	for _, inline := range cell.Inlines {
		switch typed := inline.(type) {
		case textInline, codeInline:
		case styledInline:
			if typed.Style != styleBold && typed.Style != styleItalic && typed.Style != styleStrike {
				return false
			}
		case linkInline:
			if typed.Directive || typed.Dangerous {
				return false
			}
		default:
			return false
		}
	}
	return true
}

type jiraMacroParseResult struct {
	Block       semanticBlock
	Next        int
	Diagnostics []conversionDiagnostic
	Err         error
}

func parseJiraBlockMacro(ctx context.Context, source string, lines []sourceLine, start, quoteDepth int) (jiraMacroParseResult, bool) {
	opening := lines[start].Text
	name := ""
	switch {
	case opening == "{quote}":
		name = "quote"
	case opening == "{noformat}":
		name = "noformat"
	case opening == "{code}" || strings.HasPrefix(opening, "{code:") && strings.HasSuffix(opening, "}"):
		name = "code"
	case opening == "{panel}" || strings.HasPrefix(opening, "{panel:") && strings.HasSuffix(opening, "}"):
		name = "panel"
	default:
		return jiraMacroParseResult{}, false
	}
	closing := "{" + name + "}"
	closeIndex := start + 1
	if name == "panel" || name == "quote" {
		var err error
		closeIndex, err = findJiraSymmetricMacroClose(ctx, lines, start, name)
		if err != nil {
			return jiraMacroParseResult{Err: err}, true
		}
	} else {
		for closeIndex < len(lines) && lines[closeIndex].Text != closing {
			closeIndex++
		}
	}
	if closeIndex == len(lines) {
		return jiraMacroParseResult{}, false
	}
	bodyStart := lines[start].End + len(lines[start].EOL)
	bodyEnd := lines[closeIndex].Start
	span := sourceSpan{Start: lines[start].Start, End: lines[closeIndex].End}
	if name == "quote" {
		for index := start + 1; index < closeIndex; index++ {
			if lines[index].Text == "{quote}" && index+1 < closeIndex && lines[index+1].Text == "{quote}" {
				return jiraMacroParseResult{
					Block: literalBlock{Span: span, Text: source[span.Start:span.End]},
					Next:  closeIndex + 1,
					Diagnostics: []conversionDiagnostic{{offset: span.Start, warning: ConversionWarning{
						Construct: ConstructBlockquote,
						Reason:    "adjacent nested Jira quote delimiters are ambiguous and remain literal",
					}}},
				}, true
			}
		}
		if quoteDepth >= maxStructuredQuoteDepth {
			return jiraMacroParseResult{
				Block: literalBlock{Span: span, Text: source[span.Start:span.End]},
				Next:  closeIndex + 1,
				Diagnostics: []conversionDiagnostic{{offset: span.Start, warning: ConversionWarning{
					Construct: ConstructBlockquote,
					Reason:    "quote nesting exceeds the maximum structured depth and remains literal",
				}}},
			}, true
		}
		bodyDocument, diagnostics, err := parseJiraMarkupAtQuoteDepth(ctx, source[bodyStart:bodyEnd], quoteDepth+1)
		if err != nil {
			return jiraMacroParseResult{Err: err}, true
		}
		for blockIndex, block := range bodyDocument.Blocks {
			bodyDocument.Blocks[blockIndex] = shiftSemanticBlock(block, bodyStart)
		}
		for diagnosticIndex := range diagnostics {
			diagnostics[diagnosticIndex].offset += bodyStart
		}
		return jiraMacroParseResult{Block: quoteBlock{Span: span, Blocks: bodyDocument.Blocks}, Next: closeIndex + 1, Diagnostics: diagnostics}, true
	}
	if name == "panel" {
		bodyDocument, bodyDiagnostics, err := parseJiraMarkupAtQuoteDepth(ctx, source[bodyStart:bodyEnd], quoteDepth)
		if err != nil {
			return jiraMacroParseResult{Err: err}, true
		}
		for blockIndex, block := range bodyDocument.Blocks {
			bodyDocument.Blocks[blockIndex] = shiftSemanticBlock(block, bodyStart)
		}
		for diagnosticIndex := range bodyDiagnostics {
			bodyDiagnostics[diagnosticIndex].offset += bodyStart
		}
		attributes, err := parseJiraNamedAttributes(ctx, opening, "panel", lines[start].Start)
		if err != nil {
			return jiraMacroParseResult{Err: err}, true
		}
		attributes, attributeDiagnostics, invalid := validateJiraMacroAttributes(attributes, panelAttributeOrder(), nil, "panel")
		bodyDiagnostics = append(bodyDiagnostics, attributeDiagnostics...)
		if invalid {
			return jiraMacroParseResult{Block: literalBlock{Span: span, Text: source[span.Start:span.End]}, Next: closeIndex + 1, Diagnostics: bodyDiagnostics}, true
		}
		return jiraMacroParseResult{Block: panelBlock{Span: span, Attributes: attributes, Blocks: bodyDocument.Blocks}, Next: closeIndex + 1, Diagnostics: bodyDiagnostics}, true
	}
	body := source[bodyStart:bodyEnd]
	if name == "noformat" {
		return jiraMacroParseResult{Block: codeBlock{Span: span, Body: body, NoFormat: true}, Next: closeIndex + 1}, true
	}
	attributes, err := parseJiraCodeAttributes(ctx, opening, lines[start].Start)
	if err != nil {
		return jiraMacroParseResult{Err: err}, true
	}
	attributes, attributeDiagnostics, invalid := validateJiraMacroAttributes(attributes, codeAttributeOrder(), map[string]bool{"collapse": true, "linenumbers": true}, "code")
	if invalid {
		return jiraMacroParseResult{Block: literalBlock{Span: span, Text: source[span.Start:span.End]}, Next: closeIndex + 1, Diagnostics: attributeDiagnostics}, true
	}
	language := ""
	for _, attribute := range attributes {
		if strings.EqualFold(attribute.Name, "language") {
			language = normalizeCodeLanguage(attribute.Value)
		}
	}
	directive := len(attributes) == 0 && language == "" || len(attributes) > 1
	if len(attributes) == 1 && !strings.EqualFold(attributes[0].Name, "language") {
		directive = true
	}
	if language != "" && !safeCodeFenceLanguage(language) {
		directive = true
	}
	return jiraMacroParseResult{Block: codeBlock{Span: span, Body: body, Language: language, Attributes: attributes, Directive: directive}, Next: closeIndex + 1, Diagnostics: attributeDiagnostics}, true
}

func findJiraSymmetricMacroClose(ctx context.Context, lines []sourceLine, start int, name string) (int, error) {
	closing := "{" + name + "}"
	prefix := "{" + name + ":"
	candidates := make([]int, 0)
	suffixCounts := make([]int, len(lines)+1)
	for index := len(lines) - 1; index > start; index-- {
		if (len(lines)-index)&255 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		suffixCounts[index] = suffixCounts[index+1]
		if lines[index].Text == closing || strings.HasPrefix(lines[index].Text, prefix) && strings.HasSuffix(lines[index].Text, "}") {
			suffixCounts[index]++
		}
	}
	for index := start + 1; index < len(lines); index++ {
		if (index-start)&255 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		if lines[index].Text == closing {
			candidates = append(candidates, index)
		}
	}
	if len(candidates) == 0 {
		return len(lines), ctx.Err()
	}
	for candidateIndex, candidate := range candidates[:len(candidates)-1] {
		if candidateIndex&255 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		hasBlankSeparator := candidate+1 < len(lines) && lines[candidate+1].Text == ""
		if hasBlankSeparator && suffixCounts[candidate+1]%2 == 0 {
			return candidate, nil
		}
	}
	return candidates[len(candidates)-1], ctx.Err()
}

func parseJiraCodeAttributes(ctx context.Context, opening string, base int) ([]directiveAttribute, error) {
	if opening == "{code}" {
		return nil, nil
	}
	value := strings.TrimSuffix(strings.TrimPrefix(opening, "{code:"), "}")
	parts, err := splitJiraParameterParts(ctx, value)
	if err != nil {
		return nil, err
	}
	attributes := make([]directiveAttribute, 0, len(parts))
	prefixOffset := base + len("{code:")
	for _, part := range parts {
		name, attributeValue := "language", part.Value
		equals, err := findUnescaped(ctx, part.Value, 0, len(part.Value), "=")
		if err != nil {
			return nil, err
		}
		if equals >= 0 {
			attributeValue, err = decodeJiraDelimitedText(ctx, part.Value[equals+1:])
			if err != nil {
				return nil, err
			}
			name = part.Value[:equals]
		}
		if strings.EqualFold(name, "language") {
			attributeValue = normalizeCodeLanguage(attributeValue)
		}
		attributes = append(attributes, directiveAttribute{Span: sourceSpan{Start: prefixOffset + part.Start, End: prefixOffset + part.End}, Name: canonicalCodeAttributeName(name), Value: attributeValue})
	}
	return attributes, nil
}

func normalizeCodeLanguage(language string) string {
	if alias, ok := codeLanguageAliases[strings.ToLower(language)]; ok {
		return alias
	}
	return language
}

func safeCodeFenceLanguage(language string) bool {
	if language == "" {
		return true
	}
	for _, character := range language {
		if character == '`' || character == '~' || unicode.IsSpace(character) || unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func canonicalCodeAttributeName(name string) string {
	for _, canonical := range codeAttributeOrder() {
		if strings.EqualFold(name, canonical) {
			return canonical
		}
	}
	return name
}

func codeAttributeOrder() []string {
	return []string{"language", "title", "theme", "linenumbers", "firstline", "collapse", "borderStyle", "borderColor", "borderWidth", "bgColor", "titleBGColor", "titleColor"}
}

func parseJiraNamedAttributes(ctx context.Context, opening, name string, base int) ([]directiveAttribute, error) {
	prefix := "{" + name + ":"
	if opening == "{"+name+"}" {
		return nil, nil
	}
	value := strings.TrimSuffix(strings.TrimPrefix(opening, prefix), "}")
	parts, err := splitJiraParameterParts(ctx, value)
	if err != nil {
		return nil, err
	}
	attributes := make([]directiveAttribute, 0, len(parts))
	prefixOffset := base + len(prefix)
	for _, part := range parts {
		attributeName, attributeValue, bare := part.Value, "", true
		equals, err := findUnescaped(ctx, part.Value, 0, len(part.Value), "=")
		if err != nil {
			return nil, err
		}
		if equals >= 0 {
			attributeValue, err = decodeJiraDelimitedText(ctx, part.Value[equals+1:])
			if err != nil {
				return nil, err
			}
			attributeName, bare = part.Value[:equals], false
		}
		attributes = append(attributes, directiveAttribute{Span: sourceSpan{Start: prefixOffset + part.Start, End: prefixOffset + part.End}, Name: canonicalPanelAttributeName(attributeName), Value: attributeValue, Bare: bare})
	}
	return attributes, nil
}

type jiraParameterPart struct {
	Start int
	End   int
	Value string
}

func splitJiraParameterParts(ctx context.Context, value string) ([]jiraParameterPart, error) {
	parts := make([]jiraParameterPart, 0)
	for start := 0; start <= len(value); {
		end, err := findUnescaped(ctx, value, start, len(value), "|")
		if err != nil {
			return nil, err
		}
		if end < 0 {
			end = len(value)
		}
		parts = append(parts, jiraParameterPart{Start: start, End: end, Value: value[start:end]})
		if end == len(value) {
			break
		}
		start = end + 1
	}
	return parts, nil
}

func canonicalPanelAttributeName(name string) string {
	for _, canonical := range panelAttributeOrder() {
		if strings.EqualFold(name, canonical) {
			return canonical
		}
	}
	return name
}

func validateJiraMacroAttributes(attributes []directiveAttribute, known []string, booleans map[string]bool, macro string) ([]directiveAttribute, []conversionDiagnostic, bool) {
	diagnostics := make([]conversionDiagnostic, 0)
	seen := map[string]bool{}
	invalid := false
	for index := range attributes {
		if !validDirectiveAttributeName(attributes[index].Name) {
			diagnostics = append(diagnostics, conversionDiagnostic{offset: attributes[index].Span.Start, warning: ConversionWarning{Construct: ConstructJiraMacro, Reason: "Jira parameter name cannot be represented by JFM attribute grammar; complete " + macro + " remains literal"}})
			invalid = true
			continue
		}
		key := strings.ToLower(attributes[index].Name)
		knownAttribute := false
		for _, name := range known {
			if strings.EqualFold(name, attributes[index].Name) {
				attributes[index].Name = name
				knownAttribute = true
				break
			}
		}
		if !knownAttribute {
			diagnostics = append(diagnostics, conversionDiagnostic{offset: attributes[index].Span.Start, warning: ConversionWarning{Construct: ConstructDirective, Reason: "unknown " + macro + " attribute is preserved"}})
		}
		if seen[key] {
			diagnostics = append(diagnostics, conversionDiagnostic{offset: attributes[index].Span.Start, warning: ConversionWarning{Construct: ConstructDirective, Reason: "duplicate " + macro + " attribute is preserved"}})
		}
		seen[key] = true
		if attributes[index].Bare {
			diagnostics = append(diagnostics, conversionDiagnostic{offset: attributes[index].Span.Start, warning: ConversionWarning{Construct: ConstructJiraMacro, Reason: "Jira parameter without a value cannot be represented by this JFM attribute; complete " + macro + " remains literal"}})
			invalid = true
		}
		if booleans[key] && !attributes[index].Bare {
			value := strings.ToLower(attributes[index].Value)
			if value == "true" || value == "false" {
				attributes[index].Value = value
			} else {
				diagnostics = append(diagnostics, conversionDiagnostic{offset: attributes[index].Span.Start, warning: ConversionWarning{Construct: ConstructDirective, Reason: "invalid boolean " + macro + " attribute value is preserved"}})
			}
		}
	}
	return attributes, diagnostics, invalid
}

func validDirectiveAttributeName(name string) bool {
	if name == "" || !isASCIINameStart(name[0]) {
		return false
	}
	for index := 1; index < len(name); index++ {
		character := name[index]
		if !isASCIINameStart(character) && (character < '0' || character > '9') && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func shiftSemanticBlock(block semanticBlock, delta int) semanticBlock {
	switch typed := block.(type) {
	case paragraphBlock:
		typed.Span.Start += delta
		typed.Span.End += delta
		for index, inline := range typed.Inlines {
			typed.Inlines[index] = shiftSemanticInline(inline, delta)
		}
		return typed
	case headingBlock:
		typed.Span.Start += delta
		typed.Span.End += delta
		for index, inline := range typed.Inlines {
			typed.Inlines[index] = shiftSemanticInline(inline, delta)
		}
		return typed
	case thematicBreakBlock:
		typed.Span.Start += delta
		typed.Span.End += delta
		return typed
	case quoteBlock:
		typed.Span.Start += delta
		typed.Span.End += delta
		for blockIndex, block := range typed.Blocks {
			typed.Blocks[blockIndex] = shiftSemanticBlock(block, delta)
		}
		return typed
	case listBlock:
		typed.Span.Start += delta
		typed.Span.End += delta
		for itemIndex := range typed.Items {
			typed.Items[itemIndex].Span.Start += delta
			typed.Items[itemIndex].Span.End += delta
			for inlineIndex, inline := range typed.Items[itemIndex].Inlines {
				typed.Items[itemIndex].Inlines[inlineIndex] = shiftSemanticInline(inline, delta)
			}
			for blockIndex, block := range typed.Items[itemIndex].Blocks {
				typed.Items[itemIndex].Blocks[blockIndex] = shiftSemanticBlock(block, delta)
			}
		}
		return typed
	case tableBlock:
		typed.Span.Start += delta
		typed.Span.End += delta
		return typed
	case codeBlock:
		typed.Span.Start += delta
		typed.Span.End += delta
		return typed
	case panelBlock:
		typed.Span.Start += delta
		typed.Span.End += delta
		for index, child := range typed.Blocks {
			typed.Blocks[index] = shiftSemanticBlock(child, delta)
		}
		return typed
	case unsupportedMacroBlock:
		typed.Span.Start += delta
		typed.Span.End += delta
		for index, child := range typed.Blocks {
			typed.Blocks[index] = shiftSemanticBlock(child, delta)
		}
		return typed
	case literalBlock:
		typed.Span.Start += delta
		typed.Span.End += delta
		return typed
	default:
		return block
	}
}

var codeLanguageAliases = map[string]string{
	"javascript": "javascript",
	"js":         "javascript",
	"jsx":        "javascript",
	"mjs":        "javascript",
	"bash":       "bash",
	"sh":         "bash",
	"shell":      "bash",
	"zsh":        "bash",
}
