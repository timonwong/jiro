// Package markup translates between Jira Markup and Jiro Flavored Markdown.
package markup

import (
	"context"
	"errors"
	"sort"
	"unicode/utf8"
)

// ErrConversion identifies an internal JFM conversion-engine failure.
var ErrConversion = errors.New("JFM conversion failed")

// Conversion warning construct identifiers are stable lowercase kebab-case
// values emitted by the JFM conversion APIs. The vocabulary is intentionally
// open; callers must accept identifiers added by future jiro releases.
const (
	ConstructCodeBlock           = "code-block"
	ConstructBlockquote          = "blockquote"
	ConstructDirective           = "directive"
	ConstructEmoticon            = "emoticon"
	ConstructEscape              = "escape"
	ConstructHeading             = "heading"
	ConstructHTML                = "html"
	ConstructImage               = "image"
	ConstructInlineCode          = "inline-code"
	ConstructJiraMacro           = "jira-macro"
	ConstructLink                = "link"
	ConstructList                = "list"
	ConstructPlainText           = "plain-text"
	ConstructReferenceDefinition = "reference-definition"
	ConstructTable               = "table"
	ConstructUTF8                = "utf-8"
)

// JFMResult is the complete Jiro Flavored Markdown conversion result.
type JFMResult struct {
	Markdown string
	Warnings []ConversionWarning
}

// JiraMarkupResult is the complete Jira Markup conversion result.
type JiraMarkupResult struct {
	Markup   string
	Warnings []ConversionWarning
}

// ConversionWarning describes a non-fatal source construct that could not be
// represented with its complete semantics.
type ConversionWarning struct {
	Line      int
	Column    int
	Construct string
	Reason    string
}

type conversionDiagnostic struct {
	offset  int
	warning ConversionWarning
}

// ToJFM converts Jira Markup to canonical Jiro Flavored Markdown.
func ToJFM(ctx context.Context, jiraMarkup string) (result JFMResult, err error) {
	defer recoverConversionAbort(&result, &err)
	if err := ctx.Err(); err != nil {
		return JFMResult{}, err
	}
	source, diagnostics := normalizeConversionInput(jiraMarkup)
	document, parsedDiagnostics := parseJiraMarkup(ctx, source)
	diagnostics = append(diagnostics, parsedDiagnostics...)
	markdown, err := renderJFM(ctx, document)
	if err != nil {
		return JFMResult{}, err
	}
	return JFMResult{Markdown: markdown, Warnings: finalizeDiagnostics(source, diagnostics)}, nil
}

// FromJFM converts Jiro Flavored Markdown to canonical Jira Markup.
func FromJFM(ctx context.Context, jfm string) (result JiraMarkupResult, err error) {
	defer recoverConversionAbort(&result, &err)
	if err := ctx.Err(); err != nil {
		return JiraMarkupResult{}, err
	}
	source, diagnostics := normalizeConversionInput(jfm)
	document, parsedDiagnostics, err := parseJFM(ctx, source)
	if err != nil {
		return JiraMarkupResult{}, err
	}
	diagnostics = append(diagnostics, parsedDiagnostics...)
	markup, renderedDiagnostics, err := renderJiraMarkup(ctx, document)
	if err != nil {
		return JiraMarkupResult{}, err
	}
	diagnostics = append(diagnostics, renderedDiagnostics...)
	return JiraMarkupResult{Markup: markup, Warnings: finalizeDiagnostics(source, diagnostics)}, nil
}

// recoverConversionAbort turns the cancellation sentinel the conversion helpers
// panic with into the entry point's error result, leaving a zero result behind
// so a cancelled conversion never hands back a half-built document.
func recoverConversionAbort[Result any](result *Result, err *error) {
	recovered := recover()
	if recovered == nil {
		return
	}
	aborted, ok := recovered.(conversionContextAbort)
	if !ok {
		panic(recovered)
	}
	var zero Result
	*result, *err = zero, aborted.err
}

func normalizeConversionInput(input string) (string, []conversionDiagnostic) {
	if utf8.ValidString(input) {
		return input, nil
	}
	result := make([]byte, 0, len(input))
	diagnostics := make([]conversionDiagnostic, 0)
	for offset := 0; offset < len(input); {
		character, size := utf8.DecodeRuneInString(input[offset:])
		if character == utf8.RuneError && size == 1 {
			normalizedOffset := len(result)
			result = utf8.AppendRune(result, utf8.RuneError)
			diagnostics = append(diagnostics, conversionDiagnostic{
				offset: normalizedOffset,
				warning: ConversionWarning{
					Construct: ConstructUTF8,
					Reason:    "invalid UTF-8 byte was replaced with U+FFFD",
				},
			})
			offset++
			continue
		}
		result = append(result, input[offset:offset+size]...)
		offset += size
	}
	return string(result), diagnostics
}

func finalizeDiagnostics(source string, diagnostics []conversionDiagnostic) []ConversionWarning {
	if len(diagnostics) == 0 {
		return []ConversionWarning{}
	}
	sort.SliceStable(diagnostics, func(left, right int) bool {
		return diagnostics[left].offset < diagnostics[right].offset
	})
	warnings := make([]ConversionWarning, len(diagnostics))
	sourceOffset, line, column := 0, 1, 1
	for index, diagnostic := range diagnostics {
		target := min(max(diagnostic.offset, 0), len(source))
		for sourceOffset < target {
			switch source[sourceOffset] {
			case '\r':
				sourceOffset++
				if sourceOffset < len(source) && source[sourceOffset] == '\n' {
					sourceOffset++
				}
				line, column = line+1, 1
			case '\n':
				sourceOffset++
				line, column = line+1, 1
			default:
				_, size := utf8.DecodeRuneInString(source[sourceOffset:])
				sourceOffset += size
				column++
			}
		}
		warning := diagnostic.warning
		warning.Line, warning.Column = line, column
		warnings[index] = warning
	}
	return warnings
}
