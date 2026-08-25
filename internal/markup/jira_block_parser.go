package markup

import (
	"context"
	"regexp"
	"strings"
	"unicode"
)

// parseJiraLineControlBlock reads the block one line control opens. span is the
// physical line the control stands on, or the item content that begins with it,
// and controlEnd is the offset one past the control's `.`. Jira ends the block
// at the end of that one line and reads no second control inside it, so
// `h1. bq. y` is a heading whose text is `bq. y`.
func parseJiraLineControlBlock(ctx context.Context, source string, span sourceSpan, controlEnd, level int, quote bool) (semanticBlock, []conversionDiagnostic) {
	contentStart := jiraLineControlContentStart(source[:span.End], controlEnd)
	domain := jiraLineDomain{End: span.End}
	if !quote {
		// A JFM ATX heading is one line and cannot carry a hard break, so a forced
		// newline Jira renders here stays literal and is reported.
		domain.Unbreakable = ConstructHeading
	}
	inlines, diagnostics := parseJiraInlines(ctx, source, contentStart, span.End, domain)
	if quote {
		paragraph := paragraphBlock{Span: sourceSpan{Start: contentStart, End: span.End}, Inlines: inlines}
		return quoteBlock{Span: span, Blocks: []semanticBlock{paragraph}}, diagnostics
	}
	return headingBlock{Span: span, Level: level, Inlines: inlines}, diagnostics
}

func isJiraBlockStart(line string) bool {
	if _, markerEnd := jiraListMarkerPrefix(line); markerEnd != 0 {
		return true
	}
	return isJiraBlockStartBesidesList(line)
}

// isJiraBlockStartBesidesList reports every block start but the list, for the
// callers that have already read the line's marker run themselves.
func isJiraBlockStartBesidesList(line string) bool {
	if _, _, controlEnd := jiraLineControlPrefix(line); controlEnd != 0 {
		return true
	}
	// A heading level Jira has none of opens nothing: Jira keeps `h10. x` and
	// `h10.x` in the paragraph or the item above them, and only the levels it
	// does have interrupt one.
	if jiraLineThematicBreak(line) || strings.HasPrefix(line, "||") || strings.HasPrefix(line, "|") {
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
	bodyDocument, bodyDiagnostics := parseJiraMarkupAtQuoteDepth(ctx, source, bodyStart, bodyEnd, quoteDepth)
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

// jiraListFrame is one Jira list level the block reader has open. Jira names a
// level by the length of the marker run and its type by the run's last
// character alone, so the characters before that one never decide where a line
// nests and a deeper line joins whatever item is open however its run is spelled.
type jiraListFrame struct {
	marker byte
	items  []listItem
	start  int
	end    int
}

func parseJiraLists(ctx context.Context, source string, lines []sourceLine, start int) ([]semanticBlock, int, []conversionDiagnostic) {
	blocks := make([]semanticBlock, 0)
	diagnostics := make([]conversionDiagnostic, 0)
	stack := make([]jiraListFrame, 0, 4)
	closeTo := func(depth int) {
		for len(stack) > depth {
			frame := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			list := listBlock{Span: sourceSpan{Start: frame.start, End: frame.end}, Ordered: frame.marker == '#', Items: frame.items}
			if len(stack) == 0 {
				blocks = append(blocks, list)
				continue
			}
			parent := &stack[len(stack)-1]
			item := &parent.items[len(parent.items)-1]
			item.Blocks = append(item.Blocks, list)
			item.Span.End = list.Span.End
			parent.end = list.Span.End
		}
	}
	index := start
	for index < len(lines) {
		abortConversionOnCancel(ctx)
		line := lines[index]
		if jiraLineThematicBreak(line.Text) {
			break
		}
		run, contentStart, dashRun := jiraLineMarkerRun(line.Text)
		if run == "" || dashRun && len(stack) == 0 {
			break
		}
		depth := len(run)
		if depth > len(stack)+1 {
			if len(stack) != 0 {
				// Jira opens an empty item for every level the run skips over; JFM
				// forbids fabricating those parents, so the line stays visible.
				diagnostics = append(diagnostics, conversionDiagnostic{offset: line.Start, warning: ConversionWarning{Construct: ConstructList, Reason: "list nesting skips an authored parent level"}})
				closeTo(0)
			}
			blocks = append(blocks, literalBlock{Span: sourceSpan{Start: line.Start, End: line.End}, Text: line.Text})
			diagnostics = append(diagnostics, conversionDiagnostic{offset: line.Start, warning: ConversionWarning{Construct: ConstructList, Reason: "list item has no parent at its authored nesting level"}})
			index++
			continue
		}
		marker := run[depth-1]
		if depth <= len(stack) {
			closeTo(depth)
			if stack[depth-1].marker != marker {
				closeTo(depth - 1)
			}
		}
		if depth == len(stack)+1 {
			stack = append(stack, jiraListFrame{marker: marker, start: line.Start, end: line.End})
		}
		item := listItem{Span: sourceSpan{Start: line.Start, End: line.End}}
		index++
		if jiraLineThematicBreak(line.Text[contentStart:]) {
			// Jira reads its dash rule at every item's content start and draws the
			// rule inside the item, on that line and no more, exactly as it reads a
			// line control there.
			item.Blocks = append(item.Blocks, thematicBreakBlock{Span: sourceSpan{Start: line.Start + contentStart, End: line.End}})
			diagnostics = append(diagnostics, jiraItemLineBlockContinuation(lines, index, "horizontal rule")...)
		} else if level, quote, controlEnd := jiraLineControlPrefix(line.Text[contentStart:]); controlEnd != 0 {
			// Jira reads its line controls at every item's content start and renders
			// the block inside the item. The control takes its own line and no more:
			// the lines below it that Jira keeps in the item are blocks of their own
			// rather than more of this one, so the item takes none of them and the
			// list ends there -- the reading a control line already gets below a
			// plain item (jiraListItemContinues).
			block, controlDiagnostics := parseJiraLineControlBlock(ctx, source, sourceSpan{Start: line.Start + contentStart, End: line.End}, line.Start+contentStart+controlEnd, level, quote)
			item.Blocks = append(item.Blocks, block)
			diagnostics = append(diagnostics, controlDiagnostics...)
			diagnostics = append(diagnostics, jiraItemLineBlockContinuation(lines, index, "line control")...)
		} else {
			continuation := index
			for continuation < len(lines) && jiraListItemContinues(lines[continuation].Text) {
				continuation++
			}
			inlines, inlineDiagnostics := parseJiraListItemContent(ctx, source, line, contentStart, lines[index:continuation])
			if continuation != index {
				item.Span.End = lines[continuation-1].End
				index = continuation
			}
			diagnostics = append(diagnostics, inlineDiagnostics...)
			item.Inlines = inlines
		}
		frame := &stack[depth-1]
		frame.items = append(frame.items, item)
		frame.end = item.Span.End
	}
	closeTo(0)
	return blocks, index, diagnostics
}

// jiraItemLineBlockContinuation reports the diagnostic the line at index owes
// the reader when the item above it leads with a block written on the item's own
// line. Jira keeps that line inside the item and renders it as a block of its
// own, which a JFM item holding a line control or a rule has nowhere to put, so
// the line follows the list instead. blockName names the block already on the
// item line, and the two warnings differ in nothing else.
func jiraItemLineBlockContinuation(lines []sourceLine, index int, blockName string) []conversionDiagnostic {
	if index >= len(lines) || !jiraListItemContinues(lines[index].Text) {
		return nil
	}
	return []conversionDiagnostic{{offset: lines[index].Start, warning: ConversionWarning{
		Construct: ConstructList,
		Reason:    "Jira keeps this line inside the list item above it; a JFM item holds nothing after its " + blockName + ", so the line follows the list",
	}}}
}

// jiraListItemContinues reports whether the line is one Jira keeps inside the
// list item above it rather than one that opens a block of its own. A marker
// run of any spelling ends the item, dash runs included: the open list gives
// them a level to nest at.
func jiraListItemContinues(line string) bool {
	if line == "" {
		return false
	}
	if run, _, _ := jiraLineMarkerRun(line); run != "" {
		return false
	}
	return !isJiraBlockStartBesidesList(line)
}

// parseJiraListItemContent reads one item's inlines from the marker's own line
// and the plain lines Jira keeps inside the item below it. Joining those lines
// is the only span the inline parser can read them from, so every position it
// reports inside them is an offset from the item's own start.
func parseJiraListItemContent(ctx context.Context, source string, marker sourceLine, contentStart int, continuations []sourceLine) ([]semanticInline, []conversionDiagnostic) {
	itemStart := marker.Start + contentStart
	if len(continuations) == 0 {
		return parseJiraInlines(ctx, source, itemStart, marker.End, jiraLineDomain{End: marker.End})
	}
	var raw strings.Builder
	raw.WriteString(source[itemStart:marker.End])
	for _, line := range continuations {
		raw.WriteByte('\n')
		raw.WriteString(line.Text)
	}
	inlines, diagnostics := parseJiraInlines(ctx, raw.String(), 0, raw.Len(), jiraLineDomain{End: raw.Len()})
	for index, inline := range inlines {
		inlines[index] = shiftSemanticInline(inline, itemStart)
	}
	for index := range diagnostics {
		diagnostics[index].offset += itemStart
	}
	return inlines, diagnostics
}

// jiraTableRowLine reports whether the line opens a table row. Jira skips the
// spaces and tabs in front of the delimiter, so ` |b|` below an open row opens a
// row of its own rather than continuing the one above it.
func jiraTableRowLine(text string) bool {
	return strings.HasPrefix(text[jiraLineIndentLength(text):], "|")
}

// jiraTableRowCloserEnd reports the offset one past the `|` a line closes its
// row on, and 0 when the line leaves the row open. Jira closes the row on the
// delimiter the line ends with whatever stands in front of that delimiter, so
// `|a\\|` and `|a\|` both close their row and leave the line below it outside;
// only spaces and tabs may trail the delimiter. Everything that reads where a
// row ends reads it here, because the row scanner and the cell split have to
// find the same closer.
func jiraTableRowCloserEnd(text string) int {
	end := len(strings.TrimRight(text, " \t"))
	if end == 0 || text[end-1] != '|' {
		return 0
	}
	return end
}

func jiraTableRowClosed(text string) bool {
	return jiraTableRowCloserEnd(text) != 0
}

// jiraTableRowDelimiter reports the delimiter a row's cells are separated by,
// which is `||` for a header row and `|` for a data row.
func jiraTableRowDelimiter(text string) string {
	if strings.HasPrefix(text[jiraLineIndentLength(text):], "||") {
		return "||"
	}
	return "|"
}

// parseJiraTable reads the table that starts at lines[start]. A Jira row is not
// one physical line: it runs on until a line ends on its own delimiter, and
// every line it takes on the way is content of the cell that was left open,
// which Jira reads a block at the start of. The table ends at a blank line, at
// the end of the range, or at the first line that opens no row while the row
// above it is closed.
func parseJiraTable(ctx context.Context, source string, lines []sourceLine, start int) (tableBlock, int, []conversionDiagnostic) {
	block := tableBlock{}
	rawLines := make([]string, 0, len(lines)-start)
	diagnostics := make([]conversionDiagnostic, 0)
	cellBlockDiagnostics := make([]conversionDiagnostic, 0)
	columnCount, rowCount := -1, 0
	index := start
	for index < len(lines) && jiraTableRowLine(lines[index].Text) {
		abortConversionOnCancel(ctx)
		rowStart := index
		index++
		for index < len(lines) && !jiraTableRowClosed(lines[index-1].Text) {
			text := lines[index].Text
			if jiraLineIndentLength(text) == len(text) || jiraTableRowLine(text) {
				break
			}
			index++
		}
		for _, line := range lines[rowStart:index] {
			rawLines = append(rawLines, line.Text)
		}
		if index-rowStart > 1 || jiraLineIndentLength(lines[rowStart].Text) != 0 {
			// Neither a row that runs across physical lines nor one Jira reads
			// past an indent has a GFM spelling, while the raw rows keep both.
			block.Directive = true
		}
		header := jiraTableRowDelimiter(lines[rowStart].Text) == "||"
		span := sourceSpan{Start: lines[rowStart].Start, End: lines[index-1].End}
		cells, cellDiagnostics, cellBlockWarnings, edgeWhitespace := parseJiraTableRow(ctx, source, span, jiraTableRowClosed(lines[index-1].Text))
		diagnostics = append(diagnostics, cellDiagnostics...)
		cellBlockDiagnostics = append(cellBlockDiagnostics, cellBlockWarnings...)
		if edgeWhitespace || columnCount >= 0 && len(cells) != columnCount || rowCount != 0 && header {
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
		if rowCount == 0 && header {
			block.Header = cells
		} else {
			block.Rows = append(block.Rows, cells)
		}
		rowCount++
	}
	block.Span = sourceSpan{Start: lines[start].Start, End: lines[index-1].End}
	if len(block.Header) == 0 {
		block.Directive = true
	}
	// A directive table keeps its rows verbatim, so nothing a cell would have
	// rendered as a block is lost there; only a GFM table flattens the cell to
	// text and owes the reader a warning.
	if !block.Directive {
		diagnostics = append(diagnostics, cellBlockDiagnostics...)
	}
	block.Raw = strings.Join(rawLines, "\n")
	return block, index, diagnostics
}

// parseJiraTableRow reads one row's cells from the source the row spans. A row
// is contiguous source however many physical lines it takes, so every cell keeps
// the offsets it was written at. closed says the row ended on its own delimiter;
// an open row -- one a new row line, a blank line or the end of the document cut
// short -- has no closing delimiter to strip and Jira reads its last cell to the
// end.
func parseJiraTableRow(ctx context.Context, source string, span sourceSpan, closed bool) ([]tableCell, []conversionDiagnostic, []conversionDiagnostic, bool) {
	text := source[span.Start:span.End]
	delimiter := jiraTableRowDelimiter(text)
	innerStart, innerEnd := jiraLineIndentLength(text)+len(delimiter), len(text)
	if closed {
		innerEnd = jiraTableRowInnerEnd(text, innerStart, delimiter)
	}
	if innerEnd <= innerStart {
		// `|` alone is a row Jira closes with no cell in it at all.
		return nil, nil, nil, false
	}
	bounds := jiraTableCellBounds(ctx, text, innerStart, innerEnd, delimiter)
	// The delimiter a row closes on leaves no cell behind it, so `|a||` is the
	// one cell `a` rather than `a` and an empty one.
	if last := len(bounds) - 1; last > 0 && bounds[last].Start == bounds[last].End {
		bounds = bounds[:last]
	}
	cells := make([]tableCell, 0, len(bounds))
	diagnostics := make([]conversionDiagnostic, 0)
	cellBlockWarnings := make([]conversionDiagnostic, 0)
	edgeWhitespace := false
	for _, bound := range bounds {
		value := text[bound.Start:bound.End]
		if strings.TrimSpace(value) != value {
			edgeWhitespace = true
		}
		cellSpan := sourceSpan{Start: span.Start + bound.Start, End: span.Start + bound.End}
		// A table cell is its own line domain: `|` separates no token, so a
		// forced newline is decided inside the cell and both cells of
		// `|a\\b|c\\d|` break.
		inlines, inlineDiagnostics := parseJiraInlines(ctx, source, cellSpan.Start, cellSpan.End, jiraLineDomain{End: cellSpan.End})
		diagnostics = append(diagnostics, inlineDiagnostics...)
		cellBlockWarnings = append(cellBlockWarnings, jiraTableCellBlockDiagnostics(value, cellSpan.Start)...)
		cells = append(cells, tableCell{Span: cellSpan, Inlines: inlines})
	}
	return cells, diagnostics, cellBlockWarnings, edgeWhitespace
}

// jiraTableRowInnerEnd reports where a closed row's cells end. The closer
// jiraTableRowCloserEnd finds is not part of the last cell, and neither are the
// spaces and tabs behind it -- unless a backslash stands in front of it, because
// then it closes the row without leaving it and `|a\|` is the one cell `a\|`
// that Jira renders as a literal `|`. A header row closes on a single `|` as
// readily as on two.
func jiraTableRowInnerEnd(text string, innerStart int, delimiter string) int {
	closer := jiraTableRowCloserEnd(text) - 1
	if closer < innerStart {
		// The row's opening delimiter is the only one it has, and it closes it.
		return innerStart
	}
	if text[closer-1] == '\\' {
		return len(text)
	}
	if delimiter == "||" && closer > innerStart && text[closer-1] == '|' {
		return closer - 1
	}
	return closer
}

// jiraTableCellBlockDiagnostics reports the warning a cell earns when Jira reads
// a block at its start. Every cell is a line start to Jira and it renders the
// block inside the cell, while a GFM cell holds inline content only.
func jiraTableCellBlockDiagnostics(value string, offset int) []conversionDiagnostic {
	name := jiraLineStartBlockName(value)
	if name == "" {
		return nil
	}
	return []conversionDiagnostic{{offset: offset, warning: ConversionWarning{
		Construct: ConstructTable,
		Reason:    "Jira renders " + name + " inside this table cell; JFM keeps its text",
	}}}
}

// jiraTableCellBounds splits the inside of a table row into cells. The Jira
// renderer reuses it to prove that a rendered cell still reaches the inline
// parser as one cell, so a row and a candidate cell must never be split by two
// different rules.
//
// A delimiter one backslash byte stands in front of is inside a cell rather than
// between two. The lookbehind is the whole rule, which is what makes an even run
// of backslashes protect the delimiter exactly as an odd one does: `|a\\|b|c|`
// is the two cells `a\\|b` and `c`.
func jiraTableCellBounds(ctx context.Context, text string, innerStart, innerEnd int, delimiter string) []sourceSpan {
	bounds := make([]sourceSpan, 0, 1)
	cellStart := innerStart
	for index := innerStart; index < innerEnd; {
		sampleConversionCancel(ctx, index-innerStart)
		if text[index] == '\\' {
			// A backslash keeps the byte behind it from opening a link or an
			// image shape. Whether that byte also separates two cells is the
			// lookbehind's answer below.
			index += 2
			continue
		}
		shapeEnd := jiraRowShapeEnd(ctx, text, index, innerEnd)
		if shapeEnd > 0 {
			index = shapeEnd
			continue
		}
		if !strings.HasPrefix(text[index:innerEnd], delimiter) || index > 0 && text[index-1] == '\\' {
			index++
			continue
		}
		bounds = append(bounds, sourceSpan{Start: cellStart, End: index})
		index += len(delimiter)
		cellStart = index
	}
	return append(bounds, sourceSpan{Start: cellStart, End: innerEnd})
}

func tableCellSupportsGFM(cell tableCell) bool {
	for _, inline := range cell.Inlines {
		switch typed := inline.(type) {
		case textInline, codeInline, emoticonInline:
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
		closeIndex = findJiraSymmetricMacroClose(ctx, lines, start, name)
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
		bodyDocument, diagnostics := parseJiraMarkupAtQuoteDepth(ctx, source, bodyStart, bodyEnd, quoteDepth+1)
		return jiraMacroParseResult{Block: quoteBlock{Span: span, Blocks: bodyDocument.Blocks}, Next: closeIndex + 1, Diagnostics: diagnostics}, true
	}
	if name == "panel" {
		bodyDocument, bodyDiagnostics := parseJiraMarkupAtQuoteDepth(ctx, source, bodyStart, bodyEnd, quoteDepth)
		attributes := parseJiraNamedAttributes(ctx, opening, "panel", lines[start].Start)
		_, attributes, attributeDiagnostics, invalid := validateDirectiveAttributes(attributes, jiraMacroAttributeSchema(panelAttributeOrder(), nil, "panel"))
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
	attributes := parseJiraCodeAttributes(ctx, opening, lines[start].Start)
	_, attributes, attributeDiagnostics, invalid := validateDirectiveAttributes(attributes, jiraMacroAttributeSchema(codeAttributeOrder(), map[string]bool{"collapse": true, "linenumbers": true}, "code"))
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

func findJiraSymmetricMacroClose(ctx context.Context, lines []sourceLine, start int, name string) int {
	closing := "{" + name + "}"
	prefix := "{" + name + ":"
	candidates := make([]int, 0)
	suffixCounts := make([]int, len(lines)+1)
	for index := len(lines) - 1; index > start; index-- {
		sampleConversionCancel(ctx, len(lines)-index)
		suffixCounts[index] = suffixCounts[index+1]
		if lines[index].Text == closing || strings.HasPrefix(lines[index].Text, prefix) && strings.HasSuffix(lines[index].Text, "}") {
			suffixCounts[index]++
		}
	}
	for index := start + 1; index < len(lines); index++ {
		sampleConversionCancel(ctx, index-start)
		if lines[index].Text == closing {
			candidates = append(candidates, index)
		}
	}
	if len(candidates) == 0 {
		return len(lines)
	}
	for candidateIndex, candidate := range candidates[:len(candidates)-1] {
		sampleConversionCancel(ctx, candidateIndex)
		hasBlankSeparator := candidate+1 < len(lines) && lines[candidate+1].Text == ""
		if hasBlankSeparator && suffixCounts[candidate+1]%2 == 0 {
			return candidate
		}
	}
	return candidates[len(candidates)-1]
}

func parseJiraCodeAttributes(ctx context.Context, opening string, base int) []directiveAttribute {
	if opening == "{code}" {
		return nil
	}
	value := strings.TrimSuffix(strings.TrimPrefix(opening, "{code:"), "}")
	parts := splitJiraParameterParts(ctx, value)
	attributes := make([]directiveAttribute, 0, len(parts))
	prefixOffset := base + len("{code:")
	for _, part := range parts {
		name, attributeValue := "language", part.Value
		if equals := jiraUnprotectedSplit(part.Value, 0, '='); equals >= 0 {
			name, attributeValue = part.Value[:equals], decodeJiraMacroParameterValue(ctx, part.Value[equals+1:])
		}
		if strings.EqualFold(name, "language") {
			attributeValue = normalizeCodeLanguage(attributeValue)
		}
		attributes = append(attributes, directiveAttribute{Span: sourceSpan{Start: prefixOffset + part.Start, End: prefixOffset + part.End}, Name: name, Value: attributeValue})
	}
	return attributes
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

func codeAttributeOrder() []string {
	return []string{"language", "title", "theme", "linenumbers", "firstline", "collapse", "borderStyle", "borderColor", "borderWidth", "bgColor", "titleBGColor", "titleColor"}
}

func parseJiraNamedAttributes(ctx context.Context, opening, name string, base int) []directiveAttribute {
	prefix := "{" + name + ":"
	if opening == "{"+name+"}" {
		return nil
	}
	value := strings.TrimSuffix(strings.TrimPrefix(opening, prefix), "}")
	parts := splitJiraParameterParts(ctx, value)
	attributes := make([]directiveAttribute, 0, len(parts))
	prefixOffset := base + len(prefix)
	for _, part := range parts {
		attributeName, attributeValue, bare := part.Value, "", true
		if equals := jiraUnprotectedSplit(part.Value, 0, '='); equals >= 0 {
			attributeName, attributeValue, bare = part.Value[:equals], decodeJiraMacroParameterValue(ctx, part.Value[equals+1:]), false
		}
		attributes = append(attributes, directiveAttribute{Span: sourceSpan{Start: prefixOffset + part.Start, End: prefixOffset + part.End}, Name: attributeName, Value: attributeValue, Bare: bare})
	}
	return attributes
}

type jiraParameterPart struct {
	Start int
	End   int
	Value string
}

// splitJiraParameterParts splits a macro header into its parameters. A
// backslash protects no separator here: `{code:title=a\|b}` is titled `a`.
func splitJiraParameterParts(ctx context.Context, value string) []jiraParameterPart {
	parts := make([]jiraParameterPart, 0)
	for start := 0; start <= len(value); {
		abortConversionOnCancel(ctx)
		end := jiraUnprotectedSplit(value, start, '|')
		if end < 0 {
			end = len(value)
		}
		parts = append(parts, jiraParameterPart{Start: start, End: end, Value: value[start:end]})
		if end == len(value) {
			break
		}
		start = end + 1
	}
	return parts
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
