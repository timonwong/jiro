package markup

import (
	"context"
	"strings"
)

func parseJiraMarkup(ctx context.Context, source string) (semanticDocument, []conversionDiagnostic, error) {
	return parseJiraMarkupAtQuoteDepth(ctx, source, 0)
}

func parseJiraMarkupAtQuoteDepth(ctx context.Context, source string, quoteDepth int) (semanticDocument, []conversionDiagnostic, error) {
	lines, err := splitSourceLinesWithContext(ctx, source)
	if err != nil {
		return semanticDocument{}, nil, err
	}
	document := semanticDocument{}
	diagnostics := make([]conversionDiagnostic, 0)
	for index := 0; index < len(lines); {
		if err := ctx.Err(); err != nil {
			return semanticDocument{}, nil, err
		}
		if lines[index].Text == "" {
			index++
			continue
		}
		line := lines[index]
		if level, quote, prefixEnd := jiraLineControlPrefix(line.Text); prefixEnd != 0 {
			contentStart := line.Start + prefixEnd
			if contentStart < line.End && source[contentStart] == ' ' {
				contentStart++
			}
			inlines, inlineDiagnostics, err := parseJiraInlines(ctx, source, contentStart, line.End)
			if err != nil {
				return semanticDocument{}, nil, err
			}
			diagnostics = append(diagnostics, inlineDiagnostics...)
			span := sourceSpan{Start: line.Start, End: line.End}
			if quote {
				document.Blocks = append(document.Blocks, quoteBlock{Span: span, Blocks: []semanticBlock{paragraphBlock{Span: sourceSpan{Start: contentStart, End: line.End}, Inlines: inlines}}})
			} else {
				document.Blocks = append(document.Blocks, headingBlock{Span: span, Level: level, Inlines: inlines})
			}
			index++
			continue
		}
		if malformedHeading, _ := jiraLineControlLikePrefix(line.Text); malformedHeading {
			document.Blocks = append(document.Blocks, literalBlock{Span: sourceSpan{Start: line.Start, End: line.End}, Text: line.Text})
			diagnostics = append(diagnostics, conversionDiagnostic{offset: line.Start, warning: ConversionWarning{Construct: ConstructHeading, Reason: "malformed Jira heading remains literal"}})
			index++
			continue
		}
		if jiraLineThematicBreak(line.Text) {
			document.Blocks = append(document.Blocks, thematicBreakBlock{Span: sourceSpan{Start: line.Start, End: line.End}})
			index++
			continue
		}
		if _, markerEnd := jiraListMarkerPrefix(line.Text); markerEnd != 0 {
			blocks, next, listDiagnostics, err := parseJiraLists(ctx, source, lines, index)
			if err != nil {
				return semanticDocument{}, nil, err
			}
			document.Blocks = append(document.Blocks, blocks...)
			diagnostics = append(diagnostics, listDiagnostics...)
			index = next
			continue
		}
		if strings.HasPrefix(line.Text, "||") || strings.HasPrefix(line.Text, "|") {
			table, next, tableDiagnostics, err := parseJiraTable(ctx, source, lines, index)
			if err != nil {
				return semanticDocument{}, nil, err
			}
			document.Blocks = append(document.Blocks, table)
			diagnostics = append(diagnostics, tableDiagnostics...)
			index = next
			continue
		}
		if macro, ok := parseJiraBlockMacro(ctx, source, lines, index, quoteDepth); ok {
			if macro.Err != nil {
				return semanticDocument{}, nil, macro.Err
			}
			document.Blocks = append(document.Blocks, macro.Block)
			diagnostics = append(diagnostics, macro.Diagnostics...)
			index = macro.Next
			continue
		}
		if macro, ok := parseUnsupportedJiraBlockMacro(ctx, source, lines, index, quoteDepth); ok {
			if macro.Err != nil {
				return semanticDocument{}, nil, macro.Err
			}
			document.Blocks = append(document.Blocks, macro.Block)
			diagnostics = append(diagnostics, macro.Diagnostics...)
			index = macro.Next
			continue
		}
		if isJiraBlockStart(line.Text) {
			document.Blocks = append(document.Blocks, literalBlock{Span: sourceSpan{Start: line.Start, End: line.End}, Text: line.Text})
			diagnostics = append(diagnostics, conversionDiagnostic{offset: line.Start, warning: ConversionWarning{Construct: ConstructJiraMacro, Reason: "malformed or unclosed Jira block construct remains literal"}})
			index++
			continue
		}

		start := index
		var raw strings.Builder
		for index < len(lines) && lines[index].Text != "" && !isJiraBlockStart(lines[index].Text) {
			if raw.Len() != 0 {
				raw.WriteByte('\n')
			}
			raw.WriteString(lines[index].Text)
			index++
		}
		end := lines[index-1].End
		inlines, inlineDiagnostics, err := parseJiraInlines(ctx, raw.String(), 0, raw.Len())
		if err != nil {
			return semanticDocument{}, nil, err
		}
		for inlineIndex, inline := range inlines {
			inlines[inlineIndex] = shiftSemanticInline(inline, lines[start].Start)
		}
		for diagnosticIndex := range inlineDiagnostics {
			inlineDiagnostics[diagnosticIndex].offset += lines[start].Start
		}
		diagnostics = append(diagnostics, inlineDiagnostics...)
		document.Blocks = append(document.Blocks, paragraphBlock{
			Span:    sourceSpan{Start: lines[start].Start, End: end},
			Inlines: inlines,
		})
	}
	return document, diagnostics, nil
}

type sourceLine struct {
	Start int
	End   int
	Text  string
	EOL   string
}

func splitSourceLines(source string) []sourceLine {
	lines, _ := splitSourceLinesWithContext(context.Background(), source)
	return lines
}

func splitSourceLinesWithContext(ctx context.Context, source string) ([]sourceLine, error) {
	if source == "" {
		return nil, ctx.Err()
	}
	lines := make([]sourceLine, 0, strings.Count(source, "\n")+1)
	for start := 0; start < len(source); {
		if start&255 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		end := start
		for end < len(source) && source[end] != '\r' && source[end] != '\n' {
			if end&255 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			end++
		}
		eolEnd := end
		if eolEnd < len(source) {
			if source[eolEnd] == '\r' && eolEnd+1 < len(source) && source[eolEnd+1] == '\n' {
				eolEnd += 2
			} else {
				eolEnd++
			}
		}
		lines = append(lines, sourceLine{Start: start, End: end, Text: source[start:end], EOL: source[end:eolEnd]})
		start = eolEnd
	}
	return lines, ctx.Err()
}
