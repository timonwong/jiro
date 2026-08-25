package markup

import (
	"context"
	"strings"
)

func parseJiraMarkup(ctx context.Context, source string) (semanticDocument, []conversionDiagnostic, error) {
	return parseJiraMarkupAtQuoteDepth(ctx, source, 0, len(source), 0)
}

// parseJiraMarkupAtQuoteDepth reads source[start:end] as Jira Markup. source is
// always the whole document and the range is the part being read, so a macro
// body reaches the parser as a range of the document it came from and every
// span and diagnostic offset it produces is already a document offset.
func parseJiraMarkupAtQuoteDepth(ctx context.Context, source string, start, end, quoteDepth int) (semanticDocument, []conversionDiagnostic, error) {
	lines, err := splitSourceLinesWithContext(ctx, source, start, end)
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
			span := sourceSpan{Start: line.Start, End: line.End}
			block, controlDiagnostics, err := parseJiraLineControlBlock(ctx, source, span, line.Start+prefixEnd, level, quote)
			if err != nil {
				return semanticDocument{}, nil, err
			}
			document.Blocks = append(document.Blocks, block)
			diagnostics = append(diagnostics, controlDiagnostics...)
			index++
			continue
		}
		if jiraLineMalformedHeadingPrefix(line.Text) {
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
		if macro, ok, err := parseJiraBlockMacro(ctx, source, lines, index, quoteDepth); err != nil {
			return semanticDocument{}, nil, err
		} else if ok {
			document.Blocks = append(document.Blocks, macro.Block)
			diagnostics = append(diagnostics, macro.Diagnostics...)
			index = macro.Next
			continue
		}
		if macro, ok, err := parseUnsupportedJiraBlockMacro(ctx, source, lines, index, quoteDepth); err != nil {
			return semanticDocument{}, nil, err
		} else if ok {
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

		paragraphStart := index
		var raw strings.Builder
		for index < len(lines) && lines[index].Text != "" && !isJiraBlockStart(lines[index].Text) {
			if raw.Len() != 0 {
				raw.WriteByte('\n')
			}
			raw.WriteString(lines[index].Text)
			index++
		}
		paragraphEnd := lines[index-1].End
		// Joining the lines is the only span the inline parser can read a
		// multi-line paragraph from, so its positions are offsets into that
		// synthesized string and have to be rebased onto the document.
		inlines, inlineDiagnostics, err := parseJiraInlines(ctx, raw.String(), 0, raw.Len(), jiraLineDomain{End: raw.Len()})
		if err != nil {
			return semanticDocument{}, nil, err
		}
		for inlineIndex, inline := range inlines {
			inlines[inlineIndex] = shiftSemanticInline(inline, lines[paragraphStart].Start)
		}
		for diagnosticIndex := range inlineDiagnostics {
			inlineDiagnostics[diagnosticIndex].offset += lines[paragraphStart].Start
		}
		diagnostics = append(diagnostics, inlineDiagnostics...)
		document.Blocks = append(document.Blocks, paragraphBlock{
			Span:    sourceSpan{Start: lines[paragraphStart].Start, End: paragraphEnd},
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
	lines, _ := splitSourceLinesWithContext(context.Background(), source, 0, len(source))
	return lines
}

// splitSourceLinesWithContext splits source[start:end] into lines whose offsets
// are offsets into source. The range is the only bound the split honours: a line
// that runs to end takes no EOL, so a macro body never reads the delimiter line
// that follows it.
func splitSourceLinesWithContext(ctx context.Context, source string, start, end int) ([]sourceLine, error) {
	if start >= end {
		return nil, ctx.Err()
	}
	lines := make([]sourceLine, 0, strings.Count(source[start:end], "\n")+1)
	for lineStart := start; lineStart < end; {
		if (lineStart-start)&255 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		lineEnd := lineStart
		for lineEnd < end && source[lineEnd] != '\r' && source[lineEnd] != '\n' {
			if (lineEnd-start)&255 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			lineEnd++
		}
		eolEnd := lineEnd
		if eolEnd < end {
			if source[eolEnd] == '\r' && eolEnd+1 < end && source[eolEnd+1] == '\n' {
				eolEnd += 2
			} else {
				eolEnd++
			}
		}
		lines = append(lines, sourceLine{Start: lineStart, End: lineEnd, Text: source[lineStart:lineEnd], EOL: source[lineEnd:eolEnd]})
		lineStart = eolEnd
	}
	return lines, ctx.Err()
}
