package markup

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

var kindJFMInlineDirective = ast.NewNodeKind("JFMInlineDirective")

type jfmInlineDirective struct {
	ast.BaseInline
	Raw       text.Segment
	Name      string
	Content   text.Segment
	Attrs     text.Segment
	Malformed string
}

func (node *jfmInlineDirective) Kind() ast.NodeKind { return kindJFMInlineDirective }
func (node *jfmInlineDirective) Pos() int           { return node.Raw.Start }
func (node *jfmInlineDirective) Dump(source []byte, level int) {
	ast.DumpHelper(node, source, level, map[string]string{"Name": node.Name}, nil)
}

type jfmInlineDirectiveParser struct{}

func (jfmInlineDirectiveParser) Trigger() []byte { return []byte{':'} }

func (jfmInlineDirectiveParser) Parse(_ ast.Node, reader text.Reader, _ parser.Context) ast.Node {
	line, segment := reader.PeekLine()
	if len(line) < 3 || line[0] != ':' {
		return nil
	}
	nameEnd := 1
	for nameEnd < len(line) && line[nameEnd] != '[' && line[nameEnd] != '\r' && line[nameEnd] != '\n' {
		nameEnd++
	}
	if nameEnd == len(line) || line[nameEnd] != '[' {
		return nil
	}
	name := string(line[1:nameEnd])
	if name == "" {
		return nil
	}
	contentEnd := findDirectiveClosure(line, nameEnd+1, ']')
	if contentEnd < 0 {
		return nil
	}
	node := &jfmInlineDirective{
		Raw:       text.NewSegment(segment.Start, segment.Start+contentEnd+1),
		Name:      name,
		Content:   text.NewSegment(segment.Start+nameEnd+1, segment.Start+contentEnd),
		Malformed: validateDirectiveName(name),
	}
	end := contentEnd + 1
	if end >= len(line) || line[end] != '{' {
		node.Malformed = "inline directive is missing its attribute list"
		reader.Advance(end)
		return node
	}
	attributeEnd := findDirectiveClosure(line, end+1, '}')
	if attributeEnd < 0 {
		return nil
	}
	node.Raw.Stop = segment.Start + attributeEnd + 1
	node.Attrs = text.NewSegment(segment.Start+end+1, segment.Start+attributeEnd)
	reader.Advance(attributeEnd + 1)
	return node
}

func findDirectiveClosure(line []byte, start int, closure byte) int {
	for index := start; index < len(line); index++ {
		if line[index] == '\r' || line[index] == '\n' {
			return -1
		}
		if line[index] == '\\' {
			index++
			continue
		}
		if line[index] == closure {
			return index
		}
	}
	return -1
}

func validateDirectiveName(name string) string {
	for index, character := range name {
		if index == 0 {
			if !unicode.IsLetter(character) || character > unicode.MaxASCII {
				return "invalid directive name"
			}
			continue
		}
		if character > unicode.MaxASCII || !unicode.IsLetter(character) && !unicode.IsDigit(character) && character != '-' {
			return "invalid directive name"
		}
	}
	return ""
}

func parseDirectiveAttributes(source []byte, segment text.Segment) ([]directiveAttribute, []conversionDiagnostic, string) {
	if segment.Start == 0 && segment.Stop == 0 {
		return nil, nil, ""
	}
	attributes := make([]directiveAttribute, 0)
	diagnostics := make([]conversionDiagnostic, 0)
	for offset := segment.Start; offset < segment.Stop; {
		for offset < segment.Stop && (source[offset] == ' ' || source[offset] == '\t') {
			offset++
		}
		if offset == segment.Stop {
			break
		}
		nameStart := offset
		for offset < segment.Stop {
			character, size := utf8.DecodeRune(source[offset:segment.Stop])
			if !unicode.IsLetter(character) && !unicode.IsDigit(character) && character != '_' && character != '-' {
				break
			}
			offset += size
		}
		if offset == nameStart || !isASCIINameStart(source[nameStart]) {
			return attributes, diagnostics, "invalid directive attribute name"
		}
		name := string(source[nameStart:offset])
		for offset < segment.Stop && (source[offset] == ' ' || source[offset] == '\t') {
			offset++
		}
		attribute := directiveAttribute{Span: sourceSpan{Start: nameStart, End: offset}, Name: name, Bare: true}
		if offset < segment.Stop && source[offset] == '=' {
			attribute.Bare = false
			offset++
			for offset < segment.Stop && (source[offset] == ' ' || source[offset] == '\t') {
				offset++
			}
			if offset >= segment.Stop {
				return attributes, diagnostics, "directive attribute is missing a value"
			}
			if source[offset] == '"' {
				value, stop, warning, ok := parseDirectiveQuotedValue(source, offset, segment.Stop)
				if !ok {
					return attributes, diagnostics, "unterminated directive attribute value"
				}
				attribute.Value = value
				if warning != "" {
					diagnostics = append(diagnostics, conversionDiagnostic{offset: offset, warning: ConversionWarning{Construct: ConstructDirective, Reason: warning}})
				}
				offset = stop
			} else {
				valueStart := offset
				for offset < segment.Stop && source[offset] != ' ' && source[offset] != '\t' {
					offset++
				}
				attribute.Value = string(source[valueStart:offset])
			}
		}
		attribute.Span.End = offset
		attributes = append(attributes, attribute)
	}
	return attributes, diagnostics, ""
}

func isASCIINameStart(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func parseDirectiveQuotedValue(source []byte, start, end int) (string, int, string, bool) {
	var result strings.Builder
	warning := ""
	for offset := start + 1; offset < end; {
		if source[offset] == '"' {
			return result.String(), offset + 1, warning, true
		}
		if source[offset] != '\\' {
			character, size := utf8.DecodeRune(source[offset:end])
			result.WriteRune(character)
			offset += size
			continue
		}
		if offset+1 >= end {
			return "", 0, warning, false
		}
		switch source[offset+1] {
		case '"', '\\':
			result.WriteByte(source[offset+1])
		case 'n':
			result.WriteByte('\n')
		case 'r':
			result.WriteByte('\r')
		case 't':
			result.WriteByte('\t')
		default:
			result.WriteByte('\\')
			result.WriteByte(source[offset+1])
			warning = "unknown directive attribute escape remains literal"
		}
		offset += 2
	}
	return "", 0, warning, false
}

func decodeInlineDirectiveContent(value string) string {
	var result strings.Builder
	for index := 0; index < len(value); index++ {
		if value[index] == '\\' && index+1 < len(value) && (value[index+1] == '\\' || value[index+1] == ']') {
			index++
		}
		result.WriteByte(value[index])
	}
	return result.String()
}
