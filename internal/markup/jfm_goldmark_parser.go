package markup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"regexp"
	"strings"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extensionast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

var jfmAutolinkURLRegexp = regexp.MustCompile(
	`^(?:(?:http|https)://[-a-zA-Z0-9@:%._\+~#=]{1,256}\.[a-z]+(?::\d+)?(?:[/#?][-a-zA-Z0-9@:%_+.~#$!?&/=\(\);,'">\^{}\[\]` + "`" + `]*)?|` +
		`mailto:[a-zA-Z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)+)`,
)

func parseJFM(ctx context.Context, source string) (semanticDocument, []conversionDiagnostic, error) {
	if err := ctx.Err(); err != nil {
		return semanticDocument{}, nil, err
	}
	markdown := newJFMGoldmark()
	sourceBytes := []byte(source)
	root, err := parseGoldmarkWithContext(ctx, markdown, sourceBytes)
	if err != nil {
		return semanticDocument{}, nil, err
	}
	// The analysis reads only source, so a document holding no reference
	// definition never pays for its two full-document scans.
	var references *referenceDefinitionAnalysis
	seenReferences := map[string]int{}
	document := semanticDocument{}
	diagnostics := make([]conversionDiagnostic, 0)
	for node := root.FirstChild(); node != nil; node = node.NextSibling() {
		if err := ctx.Err(); err != nil {
			return semanticDocument{}, nil, err
		}
		if definition, ok := node.(*ast.LinkReferenceDefinition); ok {
			if references == nil {
				analysis := analyzeReferenceDefinitions(source)
				references = &analysis
			}
			label := normalizeReferenceLabel(string(definition.Label))
			occurrence := seenReferences[label]
			seenReferences[label] = occurrence + 1
			definitions := references.definitions[label]
			if occurrence < len(definitions) && occurrence == 0 && references.used[label] {
				continue
			}
			span, text := referenceDefinitionSource(source, definition.Pos())
			reason := "unused reference definition remains literal"
			if occurrence > 0 {
				reason = "duplicate reference definition shadowed by an earlier definition remains literal"
			}
			document.Blocks = append(document.Blocks, literalBlock{Span: span, Text: text})
			diagnostics = append(diagnostics, conversionDiagnostic{offset: span.Start, warning: ConversionWarning{Construct: ConstructReferenceDefinition, Reason: reason}})
			continue
		}
		block, blockDiagnostics, err := adaptGoldmarkBlock(sourceBytes, node)
		if err != nil {
			construct, reason, recoverable := topLevelLiteralFallback(node, err)
			if !recoverable {
				return semanticDocument{}, nil, err
			}
			literal, warning := literalGoldmarkBlock(sourceBytes, node, construct, reason)
			document.Blocks = append(document.Blocks, literal)
			diagnostics = append(diagnostics, warning)
			continue
		}
		document.Blocks = append(document.Blocks, block)
		diagnostics = append(diagnostics, blockDiagnostics...)
	}
	return document, diagnostics, nil
}

// topLevelLiteralFallback names the literal wording for the kinds a top-level failure may
// degrade instead of aborting the document; anything else propagates the error.
func topLevelLiteralFallback(node ast.Node, err error) (construct, reason string, recoverable bool) {
	if errors.Is(err, errUnsupportedGoldmarkBlock) {
		return ConstructDirective, "unsupported Markdown block remains literal", true
	}
	switch node.(type) {
	case *ast.Blockquote:
		return ConstructDirective, "unsupported complex blockquote remains literal", true
	case *ast.List:
		return ConstructList, "unsupported complex list item remains literal", true
	case *extensionast.Table:
		return ConstructTable, "unsupported GFM table content remains literal", true
	case *jfmContainerDirective:
		return ConstructDirective, "malformed container directive remains literal", true
	default:
		return "", "", false
	}
}

func literalGoldmarkBlock(source []byte, node ast.Node, construct, reason string) (literalBlock, conversionDiagnostic) {
	span := goldmarkAuthoredBlockSpan(source, node)
	return literalBlock{Span: span, Text: string(source[span.Start:span.End])}, conversionDiagnostic{offset: span.Start, warning: ConversionWarning{Construct: construct, Reason: reason}}
}

func goldmarkAuthoredBlockSpan(source []byte, node ast.Node) sourceSpan {
	start, end := len(source), -1
	var visit func(ast.Node)
	visit = func(current ast.Node) {
		if position := current.Pos(); position >= 0 && position < start {
			start = position
		}
		if current.Type() == ast.TypeBlock {
			lines := current.Lines()
			for index := 0; index < lines.Len(); index++ {
				segment := lines.At(index)
				if segment.Start < start {
					start = segment.Start
				}
				if segment.Stop > end {
					end = segment.Stop
				}
			}
		} else {
			switch typed := current.(type) {
			case *ast.Text:
				if typed.Segment.Start < start {
					start = typed.Segment.Start
				}
				if typed.Segment.Stop > end {
					end = typed.Segment.Stop
				}
			case *ast.RawHTML:
				for index := 0; index < typed.Segments.Len(); index++ {
					segment := typed.Segments.At(index)
					if segment.Start < start {
						start = segment.Start
					}
					if segment.Stop > end {
						end = segment.Stop
					}
				}
			}
		}
		for child := current.FirstChild(); child != nil; child = child.NextSibling() {
			visit(child)
		}
	}
	visit(node)
	if start == len(source) {
		start = 0
	}
	for start > 0 && source[start-1] != '\n' && source[start-1] != '\r' {
		start--
	}
	if end < start {
		end = start
	}
	for end < len(source) && source[end] != '\n' && source[end] != '\r' {
		end++
	}
	return sourceSpan{Start: start, End: end}
}

type referenceDefinitionAnalysis struct {
	definitions map[string][]sourceSpan
	used        map[string]bool
}

var referenceDefinitionPattern = regexp.MustCompile(`^ {0,3}\[([^]]+)\]:[ \t]+\S.*$`)
var fullReferencePattern = regexp.MustCompile(`!?\[([^]]+)\]\[([^]]*)\]`)

func analyzeReferenceDefinitions(source string) referenceDefinitionAnalysis {
	analysis := referenceDefinitionAnalysis{definitions: map[string][]sourceSpan{}, used: map[string]bool{}}
	lines := splitSourceLines(source)
	var nonDefinitions strings.Builder
	for _, line := range lines {
		if match := referenceDefinitionPattern.FindStringSubmatch(line.Text); match != nil {
			label := normalizeReferenceLabel(match[1])
			analysis.definitions[label] = append(analysis.definitions[label], sourceSpan{Start: line.Start, End: line.End})
			nonDefinitions.WriteString(strings.Repeat(" ", len(line.Text)))
		} else {
			nonDefinitions.WriteString(line.Text)
		}
		nonDefinitions.WriteString(line.EOL)
	}
	nonDefinitionsText := nonDefinitions.String()
	for _, match := range fullReferencePattern.FindAllStringSubmatch(nonDefinitionsText, -1) {
		label := match[2]
		if label == "" {
			label = match[1]
		}
		analysis.used[normalizeReferenceLabel(label)] = true
	}
	lowerNonDefinitions := strings.ToLower(nonDefinitionsText)
	for label := range analysis.definitions {
		if analysis.used[label] {
			continue
		}
		needle := "[" + label + "]"
		if strings.Contains(lowerNonDefinitions, needle) {
			analysis.used[label] = true
		}
	}
	return analysis
}

func normalizeReferenceLabel(label string) string {
	return strings.ToLower(strings.Join(strings.Fields(label), " "))
}

func referenceDefinitionSource(source string, position int) (sourceSpan, string) {
	if position < 0 {
		position = 0
	}
	start := position
	for start > 0 && source[start-1] != '\n' && source[start-1] != '\r' {
		start--
	}
	end := position
	for end < len(source) && source[end] != '\n' && source[end] != '\r' {
		end++
	}
	return sourceSpan{Start: start, End: end}, source[start:end]
}

func adaptContainerDirective(source []byte, node *jfmContainerDirective) (semanticBlock, []conversionDiagnostic, error) {
	span := containerDirectiveSpan(node, len(source))
	raw := string(source[span.Start:span.End])
	diagnostics := make([]conversionDiagnostic, 0)
	if node.Malformed != "" {
		return literalBlock{Span: span, Text: raw}, []conversionDiagnostic{{offset: span.Start, warning: ConversionWarning{Construct: ConstructDirective, Reason: node.Malformed}}}, nil
	}
	attributes, attributeDiagnostics, malformed := parseDirectiveAttributes(source, node.Attrs)
	diagnostics = append(diagnostics, attributeDiagnostics...)
	if malformed != "" {
		return literalBlock{Span: span, Text: raw}, append(diagnostics, conversionDiagnostic{offset: span.Start, warning: ConversionWarning{Construct: ConstructDirective, Reason: malformed}}), nil
	}
	switch strings.ToLower(node.Name) {
	case "panel":
		_, attributes, validationDiagnostics, _ := validateDirectiveAttributes(attributes, containerDirectiveAttributeSchema(panelAttributeOrder(), nil, "panel"))
		diagnostics = append(diagnostics, validationDiagnostics...)
		panel := panelBlock{Span: span, Attributes: attributes}
		for child := node.FirstChild(); child != nil; child = child.NextSibling() {
			block, childDiagnostics, err := adaptGoldmarkChildBlock(source, child)
			if err != nil {
				literal, warning := literalGoldmarkBlock(source, child, ConstructDirective, "unsupported panel child remains literal")
				panel.Blocks = append(panel.Blocks, literal)
				diagnostics = append(diagnostics, warning)
				continue
			}
			panel.Blocks = append(panel.Blocks, block)
			diagnostics = append(diagnostics, childDiagnostics...)
		}
		return panel, diagnostics, nil
	case "code":
		booleanAttributes := map[string]bool{"collapse": true, "linenumbers": true}
		_, attributes, validationDiagnostics, _ := validateDirectiveAttributes(attributes, containerDirectiveAttributeSchema(codeAttributeOrder(), booleanAttributes, "code"))
		diagnostics = append(diagnostics, validationDiagnostics...)
		language := ""
		for index := range attributes {
			if strings.EqualFold(attributes[index].Name, "language") {
				attributes[index].Value = normalizeCodeLanguage(attributes[index].Value)
				language = attributes[index].Value
			}
		}
		return codeBlock{Span: span, Body: containerRawBody(source, node), Language: language, Attributes: attributes, Directive: true}, diagnostics, nil
	case "table":
		if len(attributes) != 0 {
			for _, attribute := range attributes {
				diagnostics = append(diagnostics, conversionDiagnostic{offset: attribute.Span.Start, warning: ConversionWarning{Construct: ConstructDirective, Reason: "table directive does not accept attributes"}})
			}
			return literalBlock{Span: span, Text: raw}, diagnostics, nil
		}
		return tableBlock{Span: span, Raw: trimOneTrailingLineEnding(containerRawBody(source, node)), Directive: true}, diagnostics, nil
	default:
		return literalBlock{Span: span, Text: raw}, []conversionDiagnostic{{offset: span.Start, warning: ConversionWarning{Construct: ConstructDirective, Reason: "unknown JFM container directive remains literal"}}}, nil
	}
}

// errUnsupportedGoldmarkBlock lets each caller pick its own literal fallback for an
// unhandled kind: the top level slices whole authored lines, nested blocks their own span.
var errUnsupportedGoldmarkBlock = errors.New("unsupported goldmark block")

func adaptGoldmarkChildBlock(source []byte, node ast.Node) (semanticBlock, []conversionDiagnostic, error) {
	block, diagnostics, err := adaptGoldmarkBlock(source, node)
	if errors.Is(err, errUnsupportedGoldmarkBlock) {
		literal, warning := literalGoldmarkChildBlock(source, node, ConstructDirective, "unsupported Markdown block remains literal")
		return literal, []conversionDiagnostic{warning}, nil
	}
	return block, diagnostics, err
}

func adaptGoldmarkBlock(source []byte, node ast.Node) (semanticBlock, []conversionDiagnostic, error) {
	switch typed := node.(type) {
	case *ast.Heading:
		inlines, diagnostics, err := adaptGoldmarkInlines(source, typed)
		return headingBlock{Span: goldmarkNodeSpan(typed), Level: typed.Level, Inlines: inlines}, diagnostics, err
	case *ast.Paragraph:
		inlines, diagnostics, err := adaptGoldmarkInlines(source, typed)
		return paragraphBlock{Span: goldmarkNodeSpan(typed), Inlines: inlines}, diagnostics, err
	case *ast.ThematicBreak:
		return thematicBreakBlock{Span: goldmarkNodeSpan(typed)}, nil, nil
	case *ast.Blockquote:
		if goldmarkBlockquoteDepth(typed) > maxStructuredQuoteDepth {
			block, warning := literalGoldmarkBlock(source, typed, ConstructBlockquote, "quote nesting exceeds the maximum structured depth and remains literal")
			return block, []conversionDiagnostic{warning}, nil
		}
		block, diagnostics, err := adaptGoldmarkBlockquote(source, typed)
		return block, diagnostics, err
	case *ast.List:
		block, diagnostics, err := adaptGoldmarkList(source, typed)
		return block, diagnostics, err
	case *extensionast.Table:
		block, diagnostics, err := adaptGoldmarkTable(source, typed)
		return block, diagnostics, err
	case *ast.CodeBlock:
		return codeBlock{Span: goldmarkNodeSpan(typed), Body: goldmarkBlockText(source, typed.Lines()), NoFormat: true}, nil, nil
	case *ast.FencedCodeBlock:
		block, diagnostics := adaptGoldmarkFencedCodeBlock(source, typed)
		return block, diagnostics, nil
	case *jfmContainerDirective:
		return adaptContainerDirective(source, typed)
	default:
		return nil, nil, errUnsupportedGoldmarkBlock
	}
}

func literalGoldmarkChildBlock(source []byte, node ast.Node, construct, reason string) (literalBlock, conversionDiagnostic) {
	span := goldmarkNodeSpan(node)
	text := sourceSliceForInline(source, node)
	if lines := node.Lines(); lines != nil && lines.Len() != 0 {
		text = trimOneTrailingLineEnding(goldmarkBlockText(source, lines))
	}
	return literalBlock{Span: span, Text: text}, conversionDiagnostic{offset: span.Start, warning: ConversionWarning{Construct: construct, Reason: reason}}
}

func goldmarkBlockquoteDepth(node *ast.Blockquote) int {
	depth := 1
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		if _, ok := parent.(*ast.Blockquote); ok {
			depth++
		}
	}
	return depth
}

func adaptGoldmarkFencedCodeBlock(source []byte, node *ast.FencedCodeBlock) (codeBlock, []conversionDiagnostic) {
	info := ""
	if node.Info != nil {
		info = string(node.Info.Segment.Value(source))
	}
	fields := strings.Fields(info)
	language := ""
	metadataDiscarded := false
	if len(fields) != 0 && !strings.Contains(fields[0], "=") {
		language = normalizeCodeLanguage(fields[0])
		metadataDiscarded = len(fields) > 1
	} else if len(fields) != 0 {
		metadataDiscarded = true
	}
	diagnostics := []conversionDiagnostic(nil)
	if metadataDiscarded {
		span, _ := goldmarkFencedBlockSource(source, node)
		diagnostics = append(diagnostics, conversionDiagnostic{offset: span.Start, warning: ConversionWarning{Construct: ConstructCodeBlock, Reason: "fenced code info string metadata was discarded"}})
	}
	return codeBlock{
		Span:     goldmarkNodeSpan(node),
		Body:     goldmarkBlockText(source, node.Lines()),
		Language: language,
		NoFormat: language == "" && !metadataDiscarded,
	}, diagnostics
}

func containerDirectiveSpan(node *jfmContainerDirective, sourceLength int) sourceSpan {
	end := sourceLength
	if node.Closed {
		end = node.Closing.Stop
	}
	return sourceSpan{Start: node.Opening.Start, End: end}
}

func containerRawBody(source []byte, node *jfmContainerDirective) string {
	var result strings.Builder
	for index := 0; index < node.Lines().Len(); index++ {
		segment := node.Lines().At(index)
		result.Write(segment.Value(source))
	}
	return result.String()
}

func trimOneTrailingLineEnding(value string) string {
	if strings.HasSuffix(value, "\r\n") {
		return value[:len(value)-2]
	}
	if strings.HasSuffix(value, "\r") || strings.HasSuffix(value, "\n") {
		return value[:len(value)-1]
	}
	return value
}

func panelAttributeOrder() []string {
	return []string{"title", "borderStyle", "borderColor", "borderWidth", "bgColor", "titleBGColor", "titleColor"}
}

func nextPhysicalLineStart(source []byte, offset int) int {
	for offset < len(source) {
		if source[offset] == '\r' {
			offset++
			if offset < len(source) && source[offset] == '\n' {
				offset++
			}
			return offset
		}
		if source[offset] == '\n' {
			return offset + 1
		}
		offset++
	}
	return len(source)
}

func adaptGoldmarkBlockquote(source []byte, node *ast.Blockquote) (quoteBlock, []conversionDiagnostic, error) {
	quote := quoteBlock{Span: goldmarkNodeSpan(node)}
	diagnostics := make([]conversionDiagnostic, 0)
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		block, childDiagnostics, err := adaptGoldmarkChildBlock(source, child)
		if err != nil {
			return quoteBlock{}, nil, err
		}
		quote.Blocks = append(quote.Blocks, block)
		diagnostics = append(diagnostics, childDiagnostics...)
	}
	return quote, diagnostics, nil
}

func adaptGoldmarkList(source []byte, node *ast.List) (listBlock, []conversionDiagnostic, error) {
	list := listBlock{Span: goldmarkNodeSpan(node), Ordered: node.IsOrdered()}
	diagnostics := make([]conversionDiagnostic, 0)
	if node.IsOrdered() && node.Start != 1 {
		diagnostics = append(diagnostics, conversionDiagnostic{offset: list.Span.Start, warning: ConversionWarning{Construct: ConstructList, Reason: "Jira Markup cannot retain an ordered list start other than 1"}})
	}
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		itemNode, ok := child.(*ast.ListItem)
		if !ok {
			return listBlock{}, nil, fmt.Errorf("%w: list contains %s", ErrConversion, child.Kind().String())
		}
		item := listItem{Span: goldmarkNodeSpan(itemNode)}
		flattened := false
		tailInterrupted := false
		for block := itemNode.FirstChild(); block != nil; block = block.NextSibling() {
			switch typed := block.(type) {
			case *ast.TextBlock, *ast.Paragraph:
				inlines, inlineDiagnostics, err := adaptGoldmarkInlines(source, block)
				if err != nil {
					return listBlock{}, nil, err
				}
				diagnostics = append(diagnostics, inlineDiagnostics...)
				if len(item.Inlines) != 0 || len(item.Blocks) != 0 {
					item.Blocks = append(item.Blocks, paragraphBlock{Span: goldmarkNodeSpan(block), Inlines: inlines})
					flattened = true
					tailInterrupted = true
					continue
				}
				item.Inlines, item.Blocks, tailInterrupted = splitListItemHardBreaks(inlines, goldmarkNodeSpan(block), item.Blocks)
				flattened = flattened || tailInterrupted
			case *ast.List:
				childList, childDiagnostics, err := adaptGoldmarkList(source, typed)
				if err != nil {
					return listBlock{}, nil, err
				}
				item.Blocks = append(item.Blocks, childList)
				if tailInterrupted || childList.RequiresFlattening {
					flattened = true
				}
				diagnostics = append(diagnostics, childDiagnostics...)
			default:
				continuation, continuationDiagnostics, err := adaptGoldmarkChildBlock(source, block)
				if err != nil {
					return listBlock{}, nil, err
				}
				item.Blocks = append(item.Blocks, continuation)
				diagnostics = append(diagnostics, continuationDiagnostics...)
				_, _, _, lineControl := listItemLineControl(item)
				if (lineControl || listItemLineThematicBreak(item)) && len(item.Blocks) == 1 {
					// Jira reads this block back from the item's content start, so the
					// item keeps it and nothing is flattened out of the list. Only the
					// block the item leads with is read there; a second one still has
					// nowhere to go.
					continue
				}
				flattened = true
				tailInterrupted = true
			}
		}
		if flattened {
			list.RequiresFlattening = true
			itemStart := goldmarkAuthoredBlockSpan(source, itemNode).Start
			diagnostics = append(diagnostics, conversionDiagnostic{offset: itemStart, warning: ConversionWarning{Construct: ConstructList, Reason: "list item block structure was flattened because Jira Markup cannot retain it"}})
		}
		list.Items = append(list.Items, item)
	}
	return list, diagnostics, nil
}

func splitListItemHardBreaks(inlines []semanticInline, span sourceSpan, blocks []semanticBlock) ([]semanticInline, []semanticBlock, bool) {
	chunks := make([][]semanticInline, 1)
	changed := false
	for _, inline := range inlines {
		if _, ok := inline.(hardBreakInline); ok {
			chunks = append(chunks, nil)
			changed = true
			continue
		}
		chunks[len(chunks)-1] = append(chunks[len(chunks)-1], inline)
	}
	if !changed {
		return inlines, blocks, false
	}
	for _, chunk := range chunks[1:] {
		if len(chunk) != 0 {
			blocks = append(blocks, paragraphBlock{Span: span, Inlines: chunk})
		}
	}
	return chunks[0], blocks, true
}

func adaptGoldmarkTable(source []byte, node *extensionast.Table) (tableBlock, []conversionDiagnostic, error) {
	table := tableBlock{Span: goldmarkNodeSpan(node)}
	diagnostics := make([]conversionDiagnostic, 0)
	for _, alignment := range node.Alignments {
		if alignment != extensionast.AlignNone {
			diagnostics = append(diagnostics, conversionDiagnostic{offset: nextPhysicalLineStart(source, table.Span.Start), warning: ConversionWarning{Construct: ConstructTable, Reason: "Jira Markup cannot retain GFM table alignment"}})
			break
		}
	}
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		row, header := child, false
		if _, ok := child.(*extensionast.TableHeader); ok {
			header = true
		}
		cells := make([]tableCell, 0, row.ChildCount())
		for cellNode := row.FirstChild(); cellNode != nil; cellNode = cellNode.NextSibling() {
			cell, ok := cellNode.(*extensionast.TableCell)
			if !ok {
				return tableBlock{}, nil, fmt.Errorf("%w: table row contains %s", ErrConversion, cellNode.Kind().String())
			}
			inlines, inlineDiagnostics, err := adaptGoldmarkInlines(source, cell)
			if err != nil {
				return tableBlock{}, nil, err
			}
			semanticCell := tableCell{Span: goldmarkNodeSpan(cell), Inlines: inlines}
			cells = append(cells, semanticCell)
			diagnostics = append(diagnostics, inlineDiagnostics...)
		}
		if header {
			table.Header = cells
		} else {
			table.Rows = append(table.Rows, cells)
		}
	}
	return table, diagnostics, nil
}

func goldmarkBlockText(source []byte, lines *text.Segments) string {
	var result strings.Builder
	for index := 0; index < lines.Len(); index++ {
		segment := lines.At(index)
		value := segment.Value(source)
		if segment.ForceNewline && !bytes.HasSuffix(source[segment.Start:segment.Stop], []byte("\n")) {
			result.Write(bytes.TrimSuffix(value, []byte("\n")))
			result.WriteByte('\n')
			continue
		}
		result.Write(value)
	}
	return result.String()
}

func goldmarkFencedBlockSource(source []byte, node *ast.FencedCodeBlock) (sourceSpan, string) {
	position := node.Pos()
	if position < 0 && node.Info != nil {
		position = node.Info.Segment.Start
	}
	if position < 0 {
		position = 0
	}
	start := position
	for start > 0 && source[start-1] != '\n' && source[start-1] != '\r' {
		start--
	}
	lineEnd := start
	for lineEnd < len(source) && source[lineEnd] != '\n' && source[lineEnd] != '\r' {
		lineEnd++
	}
	markerStart := start
	for markerStart < lineEnd && source[markerStart] == ' ' {
		markerStart++
	}
	if markerStart >= lineEnd || source[markerStart] != '`' && source[markerStart] != '~' {
		span := goldmarkAuthoredBlockSpan(source, node)
		return span, string(source[span.Start:span.End])
	}
	marker := source[markerStart]
	markerEnd := markerStart
	for markerEnd < lineEnd && source[markerEnd] == marker {
		markerEnd++
	}
	minimum := markerEnd - markerStart
	scan := nextPhysicalLineStart(source, lineEnd)
	for scan < len(source) {
		end := scan
		for end < len(source) && source[end] != '\n' && source[end] != '\r' {
			end++
		}
		contentStart := scan
		for contentStart < end && source[contentStart] == ' ' {
			contentStart++
		}
		closingEnd := contentStart
		for closingEnd < end && source[closingEnd] == marker {
			closingEnd++
		}
		if closingEnd-contentStart >= minimum && len(bytes.TrimSpace(source[closingEnd:end])) == 0 {
			span := sourceSpan{Start: start, End: end}
			return span, string(source[span.Start:span.End])
		}
		scan = nextPhysicalLineStart(source, end)
	}
	span := sourceSpan{Start: start, End: len(source)}
	return span, string(source[span.Start:span.End])
}

func newJFMGoldmark() goldmark.Markdown {
	return goldmark.New(
		goldmark.WithExtensions(
			extension.Table,
			extension.Strikethrough,
			extension.NewLinkify(
				extension.WithLinkifyAllowedProtocols([]string{"http:", "https:", "mailto:"}),
				extension.WithLinkifyURLRegexp(jfmAutolinkURLRegexp),
			),
		),
		goldmark.WithParserOptions(
			parser.WithBlockParsers(util.Prioritized(jfmContainerDirectiveParser{}, 650)),
			parser.WithInlineParsers(util.Prioritized(jfmInlineDirectiveParser{}, 50)),
		),
	)
}

func adaptGoldmarkInlines(source []byte, parent ast.Node) ([]semanticInline, []conversionDiagnostic, error) {
	inlines := make([]semanticInline, 0, parent.ChildCount())
	diagnostics := make([]conversionDiagnostic, 0)
	for child := parent.FirstChild(); child != nil; {
		adapted, inlineDiagnostics, next, err := adaptInlineSibling(source, child)
		if err != nil {
			return nil, nil, err
		}
		inlines = append(inlines, adapted...)
		diagnostics = append(diagnostics, inlineDiagnostics...)
		child = next
	}
	return mergeAdjacentTextInlines(inlines), diagnostics, nil
}

// adaptInlineSibling adapts one node of an inline sibling chain and is the only
// entry point those walks may use. Two nodes expand to more than one inline, a
// pair adaptGoldmarkInline's single return value cannot express: a text node
// ending in a hard break, whose text would otherwise be dropped in front of the
// break (#109), and inline HTML, which an unclosed opening tag leaves as the
// literal tag followed by everything the tag was never closed around (#118).
func adaptInlineSibling(source []byte, node ast.Node) ([]semanticInline, []conversionDiagnostic, ast.Node, error) {
	if textNode, isText := node.(*ast.Text); isText && textNode.HardLineBreak() {
		span := sourceSpan{Start: textNode.Segment.Start, End: textNode.Segment.Stop}
		inlines := make([]semanticInline, 0, 2)
		if value := decodedMarkdownText(textNode.Segment.Value(source), textNode.IsRaw()); len(value) != 0 {
			inlines = append(inlines, textInline{Span: span, Text: string(value)})
		}
		return append(inlines, hardBreakInline{Span: span}), nil, node.NextSibling(), nil
	}
	if rawHTML, isHTML := node.(*ast.RawHTML); isHTML {
		return adaptControlledHTML(source, rawHTML)
	}
	inline, diagnostics, next, err := adaptGoldmarkInline(source, node)
	if err != nil {
		return nil, nil, nil, err
	}
	if inline == nil {
		return nil, diagnostics, next, nil
	}
	return []semanticInline{inline}, diagnostics, next, nil
}

func adaptGoldmarkInline(source []byte, node ast.Node) (semanticInline, []conversionDiagnostic, ast.Node, error) {
	next := node.NextSibling()
	switch typed := node.(type) {
	case *ast.Text:
		value := decodedMarkdownText(typed.Segment.Value(source), typed.IsRaw())
		spanEnd := typed.Segment.Stop
		text := string(value)
		if typed.SoftLineBreak() {
			text += " "
		}
		return textInline{Span: sourceSpan{Start: typed.Segment.Start, End: spanEnd}, Text: text}, nil, next, nil
	case *ast.String:
		value := decodedMarkdownText(typed.Value, typed.IsRaw() || typed.IsCode())
		return textInline{Span: sourceSpan{Start: typed.Pos(), End: typed.Pos() + len(typed.Value)}, Text: string(value)}, nil, next, nil
	case *ast.CodeSpan:
		return codeInline{Span: inlineNodeSpan(typed), Text: goldmarkCodeSpanText(source, typed)}, nil, next, nil
	case *ast.Emphasis:
		children, diagnostics, err := adaptGoldmarkInlines(source, typed)
		if err != nil {
			return nil, nil, nil, err
		}
		style := styleItalic
		if typed.Level == 2 {
			style = styleBold
		}
		return styledInline{Span: inlineNodeSpan(typed), Style: style, Children: children}, diagnostics, next, nil
	case *extensionast.Strikethrough:
		children, diagnostics, err := adaptGoldmarkInlines(source, typed)
		if err != nil {
			return nil, nil, nil, err
		}
		return styledInline{Span: inlineNodeSpan(typed), Style: styleStrike, Children: children}, diagnostics, next, nil
	case *ast.Link:
		constructStart := goldmarkInlineConstructStart(source, typed, '[')
		label, diagnostics, err := adaptGoldmarkInlines(source, typed)
		if err != nil {
			return nil, nil, nil, err
		}
		var hardBreakDiscarded bool
		label, hardBreakDiscarded = replaceInlineHardBreaksWithSpaces(label)
		if hardBreakDiscarded {
			diagnostics = append(diagnostics, conversionDiagnostic{offset: constructStart, warning: ConversionWarning{Construct: ConstructLink, Reason: "hard break in Markdown link label was converted to a space"}})
		}
		target := string(decodedMarkdownText(typed.Destination, false))
		title := string(decodedMarkdownText(typed.Title, false))
		_, dangerous := dangerousDestinationScheme([]byte(strings.TrimLeftFunc(target, unicodeSpaceOrControl)))
		if dangerous {
			diagnostics = append(diagnostics, conversionDiagnostic{offset: constructStart, warning: ConversionWarning{Construct: ConstructLink, Reason: "dangerous destination scheme remains reversible through Jira Markup"}})
		}
		return linkInline{Span: inlineNodeSpan(typed), Label: label, Target: target, Title: title, Dangerous: dangerous, Directive: dangerous}, diagnostics, next, nil
	case *ast.AutoLink:
		label := string(typed.Label(source))
		target := string(typed.URL(source))
		if typed.AutoLinkType == ast.AutoLinkEmail && !strings.HasPrefix(strings.ToLower(target), "mailto:") {
			target = "mailto:" + target
		}
		unnamed := label == target || strings.EqualFold(target, "mailto:"+label)
		return linkInline{Span: inlineNodeSpan(typed), Label: []semanticInline{textInline{Span: inlineNodeSpan(typed), Text: label}}, Target: target, Unnamed: unnamed}, nil, next, nil
	case *ast.Image:
		constructStart := goldmarkInlineConstructStart(source, typed, '!')
		alt := goldmarkImageAlt(source, typed)
		target := string(decodedMarkdownText(typed.Destination, false))
		attributes := []directiveAttribute(nil)
		directive := len(typed.Title) != 0
		if len(typed.Title) != 0 {
			attributes = append(attributes, directiveAttribute{Name: "title", Value: string(decodedMarkdownText(typed.Title, false))})
		}
		_, dangerous := dangerousDestinationScheme([]byte(strings.TrimLeftFunc(target, unicodeSpaceOrControl)))
		diagnostics := make([]conversionDiagnostic, 0, 1)
		if dangerous {
			diagnostics = append(diagnostics, conversionDiagnostic{offset: constructStart, warning: ConversionWarning{Construct: ConstructImage, Reason: "dangerous destination scheme remains reversible through Jira Markup"}})
		}
		return imageInline{Span: inlineNodeSpan(typed), Alt: alt, Source: target, Attributes: attributes, Directive: directive || dangerous, Dangerous: dangerous}, diagnostics, next, nil
	case *jfmInlineDirective:
		return adaptInlineDirective(source, typed, next)
	default:
		span := inlineNodeSpan(node)
		return literalInline{Span: span, Text: sourceSliceForInline(source, node)}, []conversionDiagnostic{{offset: span.Start, warning: ConversionWarning{Construct: ConstructDirective, Reason: "unsupported Markdown inline remains literal"}}}, next, nil
	}
}

func replaceInlineHardBreaksWithSpaces(inlines []semanticInline) ([]semanticInline, bool) {
	result := make([]semanticInline, len(inlines))
	changed := false
	for index, inline := range inlines {
		switch typed := inline.(type) {
		case hardBreakInline:
			result[index] = textInline{Span: typed.Span, Text: " "}
			changed = true
		case styledInline:
			children, childChanged := replaceInlineHardBreaksWithSpaces(typed.Children)
			typed.Children = children
			result[index] = typed
			changed = changed || childChanged
		default:
			result[index] = inline
		}
	}
	return mergeAdjacentTextInlines(result), changed
}

func goldmarkNodeSpan(node ast.Node) sourceSpan {
	if lines := node.Lines(); lines != nil && lines.Len() != 0 {
		return sourceSpan{Start: lines.At(0).Start, End: lines.At(lines.Len() - 1).Stop}
	}
	return sourceSpan{Start: node.Pos(), End: node.Pos()}
}

func inlineNodeSpan(node ast.Node) sourceSpan {
	start, end := node.Pos(), node.Pos()
	var visit func(ast.Node)
	visit = func(parent ast.Node) {
		for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
			position := child.Pos()
			if position >= 0 && (start < 0 || position < start) {
				start = position
			}
			switch typed := child.(type) {
			case *ast.Text:
				if typed.Segment.Stop > end {
					end = typed.Segment.Stop
				}
			case *ast.RawHTML:
				if typed.Segments.Len() != 0 && typed.Segments.At(typed.Segments.Len()-1).Stop > end {
					end = typed.Segments.At(typed.Segments.Len() - 1).Stop
				}
			}
			visit(child)
		}
	}
	visit(node)
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}
	return sourceSpan{Start: start, End: end}
}

func goldmarkInlineConstructStart(source []byte, node ast.Node, marker byte) int {
	start := inlineNodeSpan(node).Start
	lineStart := start
	for lineStart > 0 && source[lineStart-1] != '\n' && source[lineStart-1] != '\r' {
		lineStart--
	}
	for index := start; index >= lineStart; index-- {
		if index < len(source) && source[index] == marker {
			return index
		}
	}
	return start
}

func goldmarkCodeSpanText(source []byte, code *ast.CodeSpan) string {
	var result strings.Builder
	for child := code.FirstChild(); child != nil; child = child.NextSibling() {
		textNode, ok := child.(*ast.Text)
		if !ok {
			continue
		}
		value := textNode.Segment.Value(source)
		if bytes.HasSuffix(value, []byte("\n")) {
			result.Write(value[:len(value)-1])
			result.WriteByte(' ')
		} else {
			result.Write(value)
		}
	}
	return result.String()
}

func goldmarkImageAlt(source []byte, image *ast.Image) string {
	var result strings.Builder
	var appendText func(ast.Node)
	appendText = func(parent ast.Node) {
		for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
			switch typed := child.(type) {
			case *ast.Text:
				result.Write(decodedMarkdownText(typed.Segment.Value(source), typed.IsRaw()))
			case *ast.String:
				result.Write(decodedMarkdownText(typed.Value, typed.IsRaw() || typed.IsCode()))
			case *ast.CodeSpan:
				result.WriteString(goldmarkCodeSpanText(source, typed))
			default:
				appendText(child)
			}
		}
	}
	appendText(image)
	return result.String()
}

func sourceSliceForInline(source []byte, node ast.Node) string {
	span := inlineNodeSpan(node)
	if span.Start < 0 || span.End < span.Start || span.End > len(source) {
		return string(node.Text(source))
	}
	return string(source[span.Start:span.End])
}

func unicodeSpaceOrControl(character rune) bool {
	return unicode.IsSpace(character) || unicode.IsControl(character)
}

var controlledTagPattern = regexp.MustCompile(`(?i)^<\s*(ins|sup|sub)\s*>$`)
var controlledFontPattern = regexp.MustCompile(`(?i)^<\s*font\s+color\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>` + "`" + `]+))\s*>$`)
var genericOpeningTagPattern = regexp.MustCompile(`(?i)^<\s*([A-Za-z][A-Za-z0-9-]*)(?:\s[^>]*)?>$`)

func adaptControlledHTML(source []byte, opening *ast.RawHTML) ([]semanticInline, []conversionDiagnostic, ast.Node, error) {
	raw := string(opening.Segments.Value(source))
	tag, color, ok := parseControlledOpeningTag(raw)
	if !ok {
		span := rawHTMLSpan(opening)
		unsupported := []conversionDiagnostic{{offset: span.Start, warning: ConversionWarning{Construct: ConstructHTML, Reason: "unsupported or malformed inline HTML remains literal"}}}
		if match := genericOpeningTagPattern.FindStringSubmatch(raw); match != nil {
			for sibling := opening.NextSibling(); sibling != nil; sibling = sibling.NextSibling() {
				if closing, isHTML := sibling.(*ast.RawHTML); isHTML && isControlledClosingTag(string(closing.Segments.Value(source)), strings.ToLower(match[1])) {
					complete := sourceSpan{Start: span.Start, End: rawHTMLSpan(closing).End}
					return literalSourceInlines(source, complete, opening.NextSibling(), closing), unsupported, closing.NextSibling(), nil
				}
			}
		}
		return []semanticInline{literalInline{Span: span, Text: raw}}, unsupported, opening.NextSibling(), nil
	}
	children := make([]semanticInline, 0)
	diagnostics := make([]conversionDiagnostic, 0)
	for sibling := opening.NextSibling(); sibling != nil; sibling = sibling.NextSibling() {
		if closing, isHTML := sibling.(*ast.RawHTML); isHTML && isControlledClosingTag(string(closing.Segments.Value(source)), tag) {
			span := sourceSpan{Start: rawHTMLSpan(opening).Start, End: rawHTMLSpan(closing).End}
			return []semanticInline{styledInline{Span: span, Style: controlledStyle(tag), Value: color, Children: children}}, diagnostics, closing.NextSibling(), nil
		}
		adapted, childDiagnostics, next, err := adaptInlineSibling(source, sibling)
		if err != nil {
			return nil, nil, nil, err
		}
		children = append(children, adapted...)
		diagnostics = append(diagnostics, childDiagnostics...)
		if next == nil {
			break
		}
		sibling = next.PreviousSibling()
	}
	// The scan above adapted every sibling the closing tag never came for, so
	// the opening tag goes out as the literal text the warning promises and the
	// children go out behind it. Dropping them would lose input the warning
	// never mentions (#118), and there is nothing left to hand back to the walk.
	span := rawHTMLSpan(opening)
	inlines := append([]semanticInline{literalInline{Span: span, Text: raw}}, children...)
	return inlines, append(diagnostics, conversionDiagnostic{offset: span.Start, warning: ConversionWarning{Construct: ConstructHTML, Reason: "unclosed controlled HTML remains literal"}}), nil, nil
}

// literalSourceInlines keeps one span of JFM source as the literal text it was
// authored as, with the hard breaks inside it lifted out. A hard break is the
// one thing the source bytes cannot carry through: the `\` in front of the
// newline is JFM syntax and no character of the text, so written as authored it
// reaches Jira as a backslash Jira shows in front of an ordinary line break and
// comes back as that backslash and a space instead of the break (#119). Lifted
// out, the writer spells it as Jira's forced newline, which reads back as the
// break it was.
func literalSourceInlines(source []byte, span sourceSpan, first, limit ast.Node) []semanticInline {
	inlines := make([]semanticInline, 0, 1)
	cursor := span.Start
	for _, offset := range inlineHardBreakOffsets(first, limit) {
		newline := bytes.IndexByte(source[offset:span.End], '\n')
		if offset < cursor || newline < 0 {
			continue
		}
		if offset > cursor {
			inlines = append(inlines, literalInline{Span: sourceSpan{Start: cursor, End: offset}, Text: string(source[cursor:offset])})
		}
		cursor = offset + newline + 1
		inlines = append(inlines, hardBreakInline{Span: sourceSpan{Start: offset, End: cursor}})
	}
	if cursor < span.End {
		inlines = append(inlines, literalInline{Span: sourceSpan{Start: cursor, End: span.End}, Text: string(source[cursor:span.End])})
	}
	return inlines
}

// inlineHardBreakOffsets reports where each hard break begins in the sibling
// chain that runs from first up to but not including limit, descendants
// included, in source order. A break begins where the text before it stops,
// which is the first byte of the `\` or the two spaces that spell it.
func inlineHardBreakOffsets(first, limit ast.Node) []int {
	offsets := []int(nil)
	var visit func(ast.Node)
	visit = func(node ast.Node) {
		if text, isText := node.(*ast.Text); isText && text.HardLineBreak() {
			offsets = append(offsets, text.Segment.Stop)
		}
		for child := node.FirstChild(); child != nil; child = child.NextSibling() {
			visit(child)
		}
	}
	for sibling := first; sibling != nil && sibling != limit; sibling = sibling.NextSibling() {
		visit(sibling)
	}
	return offsets
}

func parseControlledOpeningTag(raw string) (tag, color string, ok bool) {
	if match := controlledTagPattern.FindStringSubmatch(raw); match != nil {
		return strings.ToLower(match[1]), "", true
	}
	if match := controlledFontPattern.FindStringSubmatch(raw); match != nil {
		for _, candidate := range match[1:] {
			if candidate != "" {
				return "font", html.UnescapeString(candidate), true
			}
		}
		return "font", "", true
	}
	return "", "", false
}

func isControlledClosingTag(raw, tag string) bool {
	return strings.EqualFold(strings.TrimSpace(raw), "</"+tag+">")
}

func controlledStyle(tag string) inlineStyle {
	switch tag {
	case "ins":
		return styleInserted
	case "sup":
		return styleSuper
	case "sub":
		return styleSub
	default:
		return styleColor
	}
}

func rawHTMLSpan(node *ast.RawHTML) sourceSpan {
	if node.Segments.Len() == 0 {
		return sourceSpan{Start: node.Pos(), End: node.Pos()}
	}
	return sourceSpan{Start: node.Segments.At(0).Start, End: node.Segments.At(node.Segments.Len() - 1).Stop}
}

func adaptInlineDirective(source []byte, node *jfmInlineDirective, next ast.Node) (semanticInline, []conversionDiagnostic, ast.Node, error) {
	raw := string(node.Raw.Value(source))
	span := sourceSpan{Start: node.Raw.Start, End: node.Raw.Stop}
	diagnostics := make([]conversionDiagnostic, 0)
	if node.Malformed != "" {
		return literalInline{Span: span, Text: raw}, []conversionDiagnostic{{offset: span.Start, warning: ConversionWarning{Construct: ConstructDirective, Reason: node.Malformed}}}, next, nil
	}
	// `:emoticon` is read before the attribute list is, because it accepts none
	// at all: letting the generic reader run first would report the shape of a
	// list the directive was never going to take, and the one warning the
	// specification promises for a malformed emoticon would arrive beside a
	// `directive` one.
	if strings.EqualFold(node.Name, "emoticon") {
		return adaptEmoticonDirective(source, node, raw, span, next)
	}
	attributes, attributeDiagnostics, malformed := parseDirectiveAttributes(source, node.Attrs)
	diagnostics = append(diagnostics, attributeDiagnostics...)
	if malformed != "" {
		return literalInline{Span: span, Text: raw}, append(diagnostics, conversionDiagnostic{offset: span.Start, warning: ConversionWarning{Construct: ConstructDirective, Reason: malformed}}), next, nil
	}
	switch strings.ToLower(node.Name) {
	case "link":
		// An unknown name, a second spelling of a known one, and a value-less
		// attribute each leave the complete directive literal rather than render a
		// link jiro read only half of. The walk is kept here rather than in
		// validateDirectiveAttributes because that answer is the opposite of the
		// shared validator's, which preserves an unknown attribute; only the
		// canonical spellings come from the schema.
		values := map[string]string{}
		for _, attribute := range attributes {
			name, known := linkDirectiveAttributeSchema.canonicalName(attribute.Name)
			if _, duplicate := values[name]; !known || duplicate {
				diagnostics = append(diagnostics, conversionDiagnostic{offset: attribute.Span.Start, warning: ConversionWarning{Construct: ConstructDirective, Reason: "link directive has an unknown or duplicate attribute"}})
				return literalInline{Span: span, Text: raw}, diagnostics, next, nil
			}
			if attribute.Bare {
				diagnostics = append(diagnostics, conversionDiagnostic{offset: attribute.Span.Start, warning: ConversionWarning{Construct: ConstructDirective, Reason: linkDirectiveValueRequired[name]}})
				return literalInline{Span: span, Text: raw}, diagnostics, next, nil
			}
			values[name] = attribute.Value
		}
		target, found := values["target"]
		if !found {
			return literalInline{Span: span, Text: raw}, append(diagnostics, conversionDiagnostic{offset: span.Start, warning: ConversionWarning{Construct: ConstructDirective, Reason: "link directive is missing required target attribute"}}), next, nil
		}
		content := decodeInlineDirectiveContent(string(node.Content.Value(source)))
		label, contentDiagnostics, contentErr := parseJFMInlineFragment(content, node.Content.Start)
		if contentErr != nil {
			return literalInline{Span: span, Text: raw}, append(diagnostics, conversionDiagnostic{offset: node.Content.Start, warning: ConversionWarning{Construct: ConstructDirective, Reason: "link directive content is not supported inline JFM"}}), next, nil
		}
		diagnostics = append(diagnostics, contentDiagnostics...)
		_, dangerous := dangerousDestinationScheme([]byte(strings.TrimLeftFunc(target, unicodeSpaceOrControl)))
		if dangerous {
			diagnostics = append(diagnostics, conversionDiagnostic{offset: span.Start, warning: ConversionWarning{Construct: ConstructLink, Reason: "dangerous destination scheme remains reversible through the link directive"}})
		}
		return linkInline{Span: span, Label: label, Target: target, Title: values["title"], Directive: true, Dangerous: dangerous}, diagnostics, next, nil
	case "image":
		destination, canonical, validationDiagnostics, _ := validateDirectiveAttributes(attributes, imageDirectiveAttributeSchema)
		diagnostics = append(diagnostics, validationDiagnostics...)
		if destination.Name == "" {
			return literalInline{Span: span, Text: raw}, append(diagnostics, conversionDiagnostic{offset: span.Start, warning: ConversionWarning{Construct: ConstructDirective, Reason: "image directive is missing required src attribute"}}), next, nil
		}
		_, dangerous := dangerousDestinationScheme([]byte(strings.TrimLeftFunc(destination.Value, unicodeSpaceOrControl)))
		if dangerous {
			diagnostics = append(diagnostics, conversionDiagnostic{offset: span.Start, warning: ConversionWarning{Construct: ConstructImage, Reason: "dangerous destination scheme remains reversible through the image directive"}})
		}
		return imageInline{Span: span, Alt: decodeInlineDirectiveContent(string(node.Content.Value(source))), Source: destination.Value, Attributes: canonical, Directive: true, Dangerous: dangerous}, diagnostics, next, nil
	default:
		return literalInline{Span: span, Text: raw}, []conversionDiagnostic{{offset: span.Start, warning: ConversionWarning{Construct: ConstructDirective, Reason: "unknown JFM directive remains literal"}}}, next, nil
	}
}

// adaptEmoticonDirective reads the attrless `:emoticon[token]` form. Its content
// is one raw token rather than inline JFM and it takes no attribute list, so
// every other shape is a semantics jiro will not guess: the whole directive
// stays visible and reports exactly one `emoticon` warning.
func adaptEmoticonDirective(source []byte, node *jfmInlineDirective, raw string, span sourceSpan, next ast.Node) (semanticInline, []conversionDiagnostic, ast.Node, error) {
	literal := func(reason string) (semanticInline, []conversionDiagnostic, ast.Node, error) {
		return literalInline{Span: span, Text: raw}, []conversionDiagnostic{{offset: span.Start, warning: ConversionWarning{Construct: ConstructEmoticon, Reason: reason}}}, next, nil
	}
	if node.HasAttrs {
		return literal("emoticon directive takes no attribute list")
	}
	token, supported := canonicalJiraEmoticonToken(decodeInlineDirectiveContent(string(node.Content.Value(source))))
	if !supported {
		return literal("emoticon directive content is not one supported Jira emoticon token")
	}
	return emoticonInline{Span: span, Token: token}, []conversionDiagnostic{}, next, nil
}

// jfmFragmentGoldmark parses the small inline-directive fragments handled by
// parseJFMInlineFragment. The custom block/inline parsers registered by
// newJFMGoldmark are stateless, so a single pipeline can be shared across
// every fragment parse instead of building one per inline directive.
var jfmFragmentGoldmark = newJFMGoldmark()

func parseJFMInlineFragment(fragment string, base int) ([]semanticInline, []conversionDiagnostic, error) {
	if fragment == "" {
		return []semanticInline{}, nil, nil
	}
	fragmentBytes := []byte(fragment)
	root := jfmFragmentGoldmark.Parser().Parse(text.NewReader(fragmentBytes))
	if root.ChildCount() != 1 {
		return nil, nil, fmt.Errorf("inline fragment produced %d blocks", root.ChildCount())
	}
	paragraph, ok := root.FirstChild().(*ast.Paragraph)
	if !ok {
		return nil, nil, fmt.Errorf("inline fragment produced %s", root.FirstChild().Kind().String())
	}
	inlines, diagnostics, err := adaptGoldmarkInlines(fragmentBytes, paragraph)
	if err != nil {
		return nil, nil, err
	}
	for index, inline := range inlines {
		inlines[index] = shiftSemanticInline(inline, base)
	}
	for index := range diagnostics {
		diagnostics[index].offset += base
	}
	return inlines, diagnostics, nil
}

func decodedMarkdownText(value []byte, raw bool) []byte {
	if raw {
		return value
	}
	value = util.UnescapePunctuations(value)
	value = util.ResolveNumericReferences(value)
	return util.ResolveEntityNames(value)
}
