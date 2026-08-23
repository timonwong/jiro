package markup

import (
	"context"
	"html"
	"strconv"
	"strings"
	"unicode/utf8"
)

// This file holds the parse-verify harness the Jira renderer uses to decide
// inline escaping (ADR-0018). Escaping decisions come from the shared inline
// grammar in jira_inline_grammar.go; this file only encodes what the grammar
// reports and then proves the decision by re-parsing the rendered run with
// jiro's own Jira parser. Nothing here may grow a hand-written character
// allowlist: a rule without a grammar hazard behind it is a rule without
// renderer evidence.

// jiraCodeEscapeMode selects how far a Monospace Span body is encoded. The
// renderer walks the modes in order and stops at the first one whose rendered
// run re-parses to the intended inlines.
type jiraCodeEscapeMode uint8

const (
	// jiraCodePredicted encodes only what the grammar reports as a hazard.
	jiraCodePredicted jiraCodeEscapeMode = iota
	// jiraCodeFullyEncoded encodes every ASCII character that is not a letter or
	// digit, which leaves Jira nothing inside the braces to reinterpret.
	jiraCodeFullyEncoded
	// jiraCodePlainText abandons the Monospace Span and emits the body through
	// the plain-text escaper.
	jiraCodePlainText
)

// jiraRenderState collects the diagnostics the Jira renderer raises while
// converting a document.
type jiraRenderState struct {
	diagnostics []conversionDiagnostic
}

// jiraRunContext describes the block position of one top-level inline run,
// which is everything the inline grammar and the verification re-parse need to
// know about the enclosing block.
type jiraRunContext struct {
	atLineStart bool
	// cellDelimiter is "" outside a table row and otherwise the delimiter the
	// Jira block parser splits that row on.
	cellDelimiter string
}

func (run jiraRunContext) inTableCell() bool { return run.cellDelimiter != "" }

// jiraInlineRender carries the escaping decisions down a nested inline run.
type jiraInlineRender struct {
	inTableCell bool
	mode        jiraCodeEscapeMode
	atLineStart bool
}

func (render jiraInlineRender) nested() jiraInlineRender {
	render.atLineStart = false
	return render
}

// jiraInlineOutput reports where a rendered fragment touches a Monospace Span
// brace, so that a caller can place the U+200B separator Jira needs between a
// word rune and the brace.
type jiraInlineOutput struct {
	text          string
	opensWithCode bool
	endsWithCode  bool
}

// renderJiraInlineRun renders one top-level inline run and proves the escaping
// by re-parsing it. A run without inline code needs no proof: plain-text
// escaping is unchanged by this harness and joins it separately.
func renderJiraInlineRun(ctx context.Context, state *jiraRenderState, inlines []semanticInline, run jiraRunContext) (string, error) {
	render := jiraInlineRender{inTableCell: run.inTableCell(), atLineStart: run.atLineStart}
	output, err := renderJiraInlines(ctx, inlines, render)
	if err != nil || !inlinesContainCode(inlines) {
		return output.text, err
	}
	intended := jiraVerificationKey(inlines, false)
	verdict, err := verifyJiraInlineRun(ctx, output.text, intended, inlines, run)
	if err != nil || verdict.accepts() {
		return output.text, err
	}
	render.mode = jiraCodeFullyEncoded
	output, err = renderJiraInlines(ctx, inlines, render)
	if err != nil {
		return "", err
	}
	verdict, err = verifyJiraInlineRun(ctx, output.text, intended, inlines, run)
	if err != nil || verdict.accepts() {
		return output.text, err
	}
	render.mode = jiraCodePlainText
	output, err = renderJiraInlines(ctx, inlines, render)
	if err != nil {
		return "", err
	}
	collectInlineCodeFallbackDiagnostics(state, inlines)
	return output.text, nil
}

func collectInlineCodeFallbackDiagnostics(state *jiraRenderState, inlines []semanticInline) {
	for _, inline := range inlines {
		switch typed := inline.(type) {
		case codeInline:
			state.diagnostics = append(state.diagnostics, conversionDiagnostic{
				offset: typed.Span.Start,
				warning: ConversionWarning{
					Construct: ConstructInlineCode,
					Reason:    "inline code could not be protected from Jira reinterpretation; emitted as plain text",
				},
			})
		case styledInline:
			collectInlineCodeFallbackDiagnostics(state, typed.Children)
		case linkInline:
			collectInlineCodeFallbackDiagnostics(state, typed.Label)
		}
	}
}

func inlinesContainCode(inlines []semanticInline) bool {
	for _, inline := range inlines {
		switch typed := inline.(type) {
		case codeInline:
			return true
		case styledInline:
			if inlinesContainCode(typed.Children) {
				return true
			}
		case linkInline:
			if inlinesContainCode(typed.Label) {
				return true
			}
		}
	}
	return false
}

// jiraVerificationVerdict reports what re-parsing a rendered run proved.
// matched is the strict result ADR-0018 asks for. codePreserved is the
// attribution rule that keeps this harness honest about its own subject: when
// every Monospace Span still reads back as the inline code it was rendered from,
// a residual mismatch lives in the run's plain text, which this harness does not
// yet escape from the grammar, and encoding the code further cannot repair it.
type jiraVerificationVerdict struct {
	matched       bool
	codePreserved bool
}

// accepts reports whether the rendered run may stand as it is.
func (verdict jiraVerificationVerdict) accepts() bool {
	return verdict.matched || verdict.codePreserved
}

// verifyJiraInlineRun re-parses rendered in its block context. A table cell is
// verified against the row splitter first, because the block layer splits the
// row before any inline rule runs. The body comparison behind codePreserved is
// only reached when the strict comparison has already failed, so a run that
// verifies pays for one re-parse and one key.
func verifyJiraInlineRun(ctx context.Context, rendered, intended string, inlines []semanticInline, run jiraRunContext) (jiraVerificationVerdict, error) {
	if run.inTableCell() {
		bounds, err := jiraTableCellBounds(ctx, rendered, 0, len(rendered), run.cellDelimiter)
		if err != nil || len(bounds) != 1 {
			return jiraVerificationVerdict{}, err
		}
	}
	parsed, _, err := parseJiraInlines(ctx, rendered, 0, len(rendered))
	if err != nil {
		return jiraVerificationVerdict{}, err
	}
	if jiraVerificationKey(parsed, true) == intended {
		return jiraVerificationVerdict{matched: true}, nil
	}
	return jiraVerificationVerdict{codePreserved: jiraCodeBodyKey(parsed) == jiraCodeBodyKey(inlines)}, nil
}

// jiraCodeBodyKey serializes every inline code body of a run in source order.
func jiraCodeBodyKey(inlines []semanticInline) string {
	var builder strings.Builder
	appendJiraCodeBodyKey(&builder, inlines)
	return builder.String()
}

func appendJiraCodeBodyKey(builder *strings.Builder, inlines []semanticInline) {
	for _, inline := range inlines {
		switch typed := inline.(type) {
		case codeInline:
			appendVerificationScalar(builder, "c", typed.Text)
		case styledInline:
			appendJiraCodeBodyKey(builder, typed.Children)
		case linkInline:
			appendJiraCodeBodyKey(builder, typed.Label)
		}
	}
}

// jiraVerificationKey serializes the parts of an inline run that a conversion
// must preserve. Source offsets are absent because the rendered run has its own,
// and adjacent text is merged because the split between text inlines is an
// artifact of how each side scanned its source. Attributes a reader derives from
// the target rather than reads from the markup (linkInline.Directive and the
// Dangerous flags) are absent for the same reason.
func jiraVerificationKey(inlines []semanticInline, decodeSafetyEscapes bool) string {
	var builder strings.Builder
	appendJiraVerificationKey(&builder, inlines, decodeSafetyEscapes)
	return builder.String()
}

func appendJiraVerificationKey(builder *strings.Builder, inlines []semanticInline, decodeSafetyEscapes bool) {
	var text strings.Builder
	flush := func() {
		if text.Len() == 0 {
			return
		}
		appendVerificationScalar(builder, "t", text.String())
		text.Reset()
	}
	for _, inline := range inlines {
		switch typed := inline.(type) {
		case textInline:
			text.WriteString(normalizeVerificationText(typed.Text, decodeSafetyEscapes))
		case literalInline:
			text.WriteString(normalizeVerificationText(typed.Text, decodeSafetyEscapes))
		case codeInline:
			flush()
			appendVerificationScalar(builder, "c", typed.Text)
		case hardBreakInline:
			flush()
			builder.WriteString("br;")
		case styledInline:
			flush()
			style, children := string(typed.Style), typed.Children
			// A bold-italic pair has one canonical Jira spelling, `*_x_*`, so the
			// two nestings are the same inline run written two ways.
			if combined, ok := combinedBoldItalic(typed); ok {
				style, children = "bold-italic", combined
			}
			appendVerificationScalar(builder, "s", style)
			appendVerificationScalar(builder, "v", typed.Value)
			builder.WriteByte('(')
			appendJiraVerificationKey(builder, children, decodeSafetyEscapes)
			builder.WriteByte(')')
		case linkInline:
			flush()
			appendVerificationScalar(builder, "a", typed.Target)
			appendVerificationScalar(builder, "u", strconv.FormatBool(typed.Unnamed))
			builder.WriteByte('(')
			// An unnamed link shows its target, so its label is derived rather
			// than authored and each side derives it from its own conventions.
			if !typed.Unnamed {
				appendJiraVerificationKey(builder, typed.Label, decodeSafetyEscapes)
			}
			builder.WriteByte(')')
		case imageInline:
			flush()
			appendVerificationScalar(builder, "m", typed.Source)
			appendVerificationScalar(builder, "l", typed.Alt)
			builder.WriteByte('(')
			for _, attribute := range typed.Attributes {
				appendVerificationScalar(builder, "n", attribute.Name)
				appendVerificationScalar(builder, "w", attribute.Value)
				appendVerificationScalar(builder, "b", strconv.FormatBool(attribute.Bare))
			}
			builder.WriteByte(')')
		default:
			flush()
			builder.WriteString("?;")
		}
	}
	flush()
}

func appendVerificationScalar(builder *strings.Builder, tag, value string) {
	builder.WriteString(tag)
	builder.WriteString(strconv.Itoa(len(value)))
	builder.WriteByte(':')
	builder.WriteString(value)
	builder.WriteByte(';')
}

// normalizeVerificationText folds the differences that are not the Monospace
// Span's to answer for. A Jira inline run is one line, so the Jira parser reads
// a newline inside text as the space that joins the line to the next one. On the
// re-parsed side it also decodes the plain-text safety escapes below, whose
// visible backslash is a plain-text defect this harness does not yet own; a
// Monospace Span cannot produce one, so decoding it hides no escaping failure.
func normalizeVerificationText(value string, decodeSafetyEscapes bool) string {
	if strings.Contains(value, "\n") {
		value = strings.ReplaceAll(value, "\n", " ")
	}
	if !decodeSafetyEscapes || !strings.Contains(value, `\`) {
		return value
	}
	var result strings.Builder
	for index := 0; index < len(value); index++ {
		if value[index] == '\\' && index+1 < len(value) && isJiraPlainTextSafetyEscape(value[index+1]) {
			continue
		}
		result.WriteByte(value[index])
	}
	return result.String()
}

// isJiraPlainTextSafetyEscape reports whether the plain-text escaper writes a
// backslash before character although Jira's escape grammar does not name it, so
// that Jira shows the backslash instead of consuming it.
func isJiraPlainTextSafetyEscape(character byte) bool {
	if strings.IndexByte(jiraEscapableCharacters, character) >= 0 {
		return false
	}
	return strings.IndexByte(jiraPlainTextEscapedCharacters, character) >= 0 || character == jiraLineControlEscapedCharacter
}

// renderJiraCodeSpanBody encodes a Monospace Span body so that Jira renders its
// characters literally. A decimal character reference is the only encoding a
// span body survives, so it is the only one emitted here.
func renderJiraCodeSpanBody(ctx context.Context, body string, render jiraInlineRender) (string, error) {
	encoded := make([]bool, len(body))
	if render.mode == jiraCodeFullyEncoded {
		markFullyEncodedCodeSpan(body, encoded)
	} else if err := markPredictedCodeSpanEncoding(ctx, body, encoded, render.inTableCell); err != nil {
		return "", err
	}
	var result strings.Builder
	for offset := 0; offset < len(body); {
		character, size := utf8.DecodeRuneInString(body[offset:])
		if encoded[offset] {
			result.WriteString("&#")
			result.WriteString(strconv.FormatInt(int64(character), 10))
			result.WriteByte(';')
		} else {
			result.WriteString(body[offset : offset+size])
		}
		offset += size
	}
	return result.String(), ctx.Err()
}

func markFullyEncodedCodeSpan(body string, encoded []bool) {
	for offset := 0; offset < len(body); {
		character, size := utf8.DecodeRuneInString(body[offset:])
		if character < utf8.RuneSelf && !isASCIIAlphanumericRune(character) || character == '\u200b' {
			encoded[offset] = true
		}
		offset += size
	}
}

func isASCIIAlphanumericRune(character rune) bool {
	return character < utf8.RuneSelf && isASCIIAlphanumeric(byte(character))
}

// markPredictedCodeSpanEncoding marks every byte offset the shared grammar
// reports as a hazard inside a Monospace Span, plus the three characters the
// grammar reports only in context but that a body can never keep raw: a brace or
// a backslash is consumed by a macro, a span closer, a forced newline or a
// legacy escape depending on what follows it, and U+200B is a span boundary
// wherever it sits.
func markPredictedCodeSpanEncoding(ctx context.Context, body string, encoded []bool, inTableCell bool) error {
	for offset := 0; offset < len(body); {
		switch {
		case body[offset] == '{' || body[offset] == '}' || body[offset] == '\\':
			encoded[offset] = true
			offset++
			continue
		case strings.HasPrefix(body[offset:], "\u200b"):
			encoded[offset] = true
			offset += len("\u200b")
			continue
		case jiraCharacterReferenceStart(body, offset, len(body)):
			encoded[offset] = true
		}
		_, size := utf8.DecodeRuneInString(body[offset:])
		offset += size
	}
	hazards, err := jiraInlineHazards(ctx, body, 0, len(body), jiraMonospaceContext, inTableCell)
	if err != nil {
		return err
	}
	for _, hazard := range hazards {
		switch hazard.Kind {
		case jiraHazardEffect:
			// Both delimiters of a complete pair, because Jira pairs them.
			encoded[hazard.Start], encoded[hazard.End-1] = true, true
		case jiraHazardCitation:
			// One `?` per `??` delimiter is enough to break the pair.
			encoded[hazard.Start], encoded[hazard.End-2] = true, true
		case jiraHazardLink, jiraHazardAutolink:
			// The token's first character alone, which is enough to stop Jira
			// recognizing it: the opening bracket of a link, the scheme's first
			// letter of an autolink. Both only matter when Jira's visible text
			// differs from the body, so a lone `]` and an autolink Jira prints
			// verbatim stay raw and the body stays readable.
			if hazard.TextChanges {
				encoded[hazard.Start] = true
			}
		case jiraHazardEmoticon:
			// Every token starts with the `(`, `:` or `;` that identifies it.
			encoded[hazard.Start] = true
		case jiraHazardDash, jiraHazardCellSeparator, jiraHazardTab, jiraHazardEdgeSpace:
			encoded[hazard.Start] = true
		}
	}
	return nil
}

// jiraCharacterReferenceStart reports whether a `&` at offset begins something a
// reader decodes back into a different character: Jira's own reference syntax,
// or one of the legacy named references Go's html.UnescapeString resolves
// without a terminating semicolon. A `&` that begins neither stays raw, so
// `a & b` keeps its ampersand.
func jiraCharacterReferenceStart(body string, offset, end int) bool {
	if offset >= end || body[offset] != '&' {
		return false
	}
	if _, referenceEnd := jiraCharacterReference(body, offset, end); referenceEnd > 0 {
		return true
	}
	scan := offset + 1
	if scan < end && body[scan] == '#' {
		scan++
	}
	for scan < end && isASCIIAlphanumeric(body[scan]) {
		scan++
	}
	if scan < end && body[scan] == ';' {
		scan++
	}
	candidate := body[offset:scan]
	return html.UnescapeString(candidate) != candidate
}
