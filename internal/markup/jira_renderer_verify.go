package markup

import (
	"context"
	"html"
	"strconv"
	"strings"
	"unicode/utf8"
)

// This file holds the parse-verify harness the Jira renderer uses to decide
// inline escaping (ADR-0018). What gets encoded is decided in
// jira_inline_grammar.go, by the hazard scan and by jiraMonospaceAlwaysEncoded;
// this file encodes what the grammar reports and then proves the decision by
// re-parsing the rendered run with jiro's own Jira parser. A character encoded
// here without one of those two sources behind it is a rule with no renderer
// evidence, which is what the allowlists this harness replaced were.

// jiraEscapeMode selects how far one inline run is encoded. The renderer walks
// the modes in order and stops at the first one whose rendered run re-parses to
// the intended inlines.
type jiraEscapeMode uint8

const (
	// jiraEscapePredicted encodes only what the grammar reports: the hazards
	// inside a Monospace Span body, the plain-text delimiters of a complete Text
	// Effect or citation pair, and a Text Effect's own delimiters in the brace
	// form only where a word rune would gate the bare one.
	jiraEscapePredicted jiraEscapeMode = iota
	// jiraEscapeFullyEncoded leaves Jira nothing to reinterpret: every ASCII
	// character of a Monospace Span body that is not a letter or digit becomes a
	// character reference, every plain-text delimiter is backslash-escaped, and
	// every Text Effect is written in the brace form.
	jiraEscapeFullyEncoded
	// jiraEscapeAbandoned additionally gives up the Monospace Span and emits its
	// body through the plain-text escaper.
	jiraEscapeAbandoned
)

// jiraVerifiedEscapeModes are the modes a rendered run is verified in, so their
// number is also the bound on re-parses per run.
var jiraVerifiedEscapeModes = [...]jiraEscapeMode{jiraEscapePredicted, jiraEscapeFullyEncoded}

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
	mode        jiraEscapeMode
	atLineStart bool
}

func (render jiraInlineRender) nested() jiraInlineRender {
	render.atLineStart = false
	return render
}

// jiraInlineOutput is one rendered inline run before plain-text escaping. It
// reports where the run touches a Monospace Span brace, so that a caller can
// place the U+200B separator Jira needs between a word rune and the brace, and
// carries the offsets of the plain-text characters that may become markup, so
// that one escaping decision covers the whole top-level run.
type jiraInlineOutput struct {
	text          string
	plainOffsets  []int
	opensWithCode bool
	endsWithCode  bool
}

// forEachInlineCode visits every inline code of a run in source order,
// descending into the children that can carry one. Whether a Monospace Span
// forms is decided per run rather than per inline, so every caller that asks a
// question about "the code in this run" has to agree on where that code can sit.
func forEachInlineCode(inlines []semanticInline, visit func(codeInline)) {
	for _, inline := range inlines {
		switch typed := inline.(type) {
		case codeInline:
			visit(typed)
		case styledInline:
			forEachInlineCode(typed.Children, visit)
		case linkInline:
			forEachInlineCode(typed.Label, visit)
		}
	}
}

// jiraInlineRunVerifier proves that a rendered run reads back as the inlines it
// was rendered from.
type jiraInlineRunVerifier func(ctx context.Context, rendered, intended string, inlines []semanticInline, run jiraRunContext) (jiraVerificationVerdict, error)

// renderJiraInlineRun renders one top-level inline run and proves the escaping
// by re-parsing it.
func renderJiraInlineRun(ctx context.Context, state *jiraRenderState, inlines []semanticInline, run jiraRunContext) (string, error) {
	return renderJiraInlineRunWith(ctx, state, inlines, run, verifyJiraInlineRun)
}

// renderJiraInlineRunWith takes the verifier as a value so that a test can
// exercise the later modes without an input Jira actually misreads.
func renderJiraInlineRunWith(ctx context.Context, state *jiraRenderState, inlines []semanticInline, run jiraRunContext, verify jiraInlineRunVerifier) (string, error) {
	render := jiraInlineRender{inTableCell: run.inTableCell(), atLineStart: run.atLineStart}
	rendered, err := renderJiraInlineRunIn(ctx, inlines, render)
	if err != nil || !jiraRunNeedsVerification(inlines, rendered) {
		return rendered, err
	}
	intended := jiraVerificationKey(inlines, false)
	var verdict jiraVerificationVerdict
	for index, mode := range jiraVerifiedEscapeModes {
		// The first mode is the render already in hand.
		if index != 0 {
			render.mode = mode
			if rendered, err = renderJiraInlineRunIn(ctx, inlines, render); err != nil {
				return "", err
			}
		}
		if verdict, err = verify(ctx, rendered, intended, inlines, run); err != nil {
			return "", err
		}
		if verdict.accepts() {
			return rendered, nil
		}
	}
	// Nothing verified. The last verdict says only which fallback answers: a
	// Monospace Span that did not read back as its inline code is a loss the
	// span itself caused, so the span goes; a run whose code survived owes the
	// mismatch to its plain text, which the fully escaped render already carries
	// as far as this harness can take it.
	if inlinesContainCode(inlines) && !verdict.codePreserved {
		render.mode = jiraEscapeAbandoned
		if rendered, err = renderJiraInlineRunIn(ctx, inlines, render); err != nil {
			return "", err
		}
		forEachInlineCode(inlines, func(code codeInline) {
			state.diagnostics = append(state.diagnostics, conversionDiagnostic{
				offset: code.Span.Start,
				warning: ConversionWarning{
					Construct: ConstructInlineCode,
					Reason:    "inline code could not be protected from Jira reinterpretation; emitted as plain text",
				},
			})
		})
		return rendered, nil
	}
	collectRunFallbackDiagnostic(state, inlines, ConversionWarning{
		Construct: ConstructPlainText,
		Reason:    "plain text could not be verified to read back as written on Jira; emitted fully escaped",
	})
	return rendered, nil
}

// renderJiraInlineRunIn renders one run in one escape mode. Plain-text escaping
// is decided over the finished run rather than per fragment, so rendering and
// escaping are one step and no caller may take only half of it.
func renderJiraInlineRunIn(ctx context.Context, inlines []semanticInline, render jiraInlineRender) (string, error) {
	output, err := renderJiraInlines(ctx, inlines, render)
	if err != nil {
		return "", err
	}
	return escapePlainJiraEffects(ctx, output.text, output.plainOffsets, render.mode)
}

// jiraRunNeedsVerification reports whether re-parsing the rendered run can tell
// the renderer anything. A run of nothing but text and hard breaks whose bytes
// hold no Jira delimiter reads back as itself, and skipping the parse there is
// what keeps the harness off the cost of ordinary prose.
func jiraRunNeedsVerification(inlines []semanticInline, rendered string) bool {
	if strings.ContainsAny(rendered, jiraPlainTextHazardBytes) {
		return true
	}
	for _, inline := range inlines {
		switch inline.(type) {
		case textInline, literalInline, hardBreakInline:
		default:
			return true
		}
	}
	return false
}

func collectRunFallbackDiagnostic(state *jiraRenderState, inlines []semanticInline, warning ConversionWarning) {
	offset := 0
	if len(inlines) != 0 {
		offset = jiraInlineSpanStart(inlines[0])
	}
	state.diagnostics = append(state.diagnostics, conversionDiagnostic{offset: offset, warning: warning})
}

func jiraInlineSpanStart(inline semanticInline) int {
	switch typed := inline.(type) {
	case textInline:
		return typed.Span.Start
	case literalInline:
		return typed.Span.Start
	case codeInline:
		return typed.Span.Start
	case styledInline:
		return typed.Span.Start
	case linkInline:
		return typed.Span.Start
	case imageInline:
		return typed.Span.Start
	case hardBreakInline:
		return typed.Span.Start
	default:
		return 0
	}
}

func inlinesContainCode(inlines []semanticInline) bool {
	found := false
	forEachInlineCode(inlines, func(codeInline) { found = true })
	return found
}

// jiraVerificationVerdict reports what re-parsing a rendered run proved. Only
// matched, the strict result ADR-0018 asks for, lets a run stand. codePreserved
// is the attribution rule, and it decides which fallback a run that never
// verified takes rather than whether it falls back: a Monospace Span that did
// not read back as its inline code is a loss the span caused and the span is
// given up, while a run whose code survived owes its mismatch to the plain text
// around it, which the fully escaped render already addresses. Both fallbacks
// warn.
type jiraVerificationVerdict struct {
	matched       bool
	codePreserved bool
}

// accepts reports whether the rendered run may stand as it is.
func (verdict jiraVerificationVerdict) accepts() bool {
	return verdict.matched
}

// verifyJiraInlineRun re-parses rendered in its block context. A table cell is
// verified against the row splitter first, because the block layer splits the
// row before any inline rule runs, and every line start is read against the
// block layer's list-marker and line-control rules, which the inline re-parse
// cannot report. The body comparison behind codePreserved is
// only reached when the strict comparison has already failed, so a run that
// verifies pays for one re-parse and one key. A run without inline code has an
// empty body key on both sides; that is the truthful answer, since there is no
// code for the plain-text fallback to protect.
func verifyJiraInlineRun(ctx context.Context, rendered, intended string, inlines []semanticInline, run jiraRunContext) (jiraVerificationVerdict, error) {
	if run.inTableCell() {
		bounds, err := jiraTableCellBounds(ctx, rendered, 0, len(rendered), run.cellDelimiter)
		if err != nil || len(bounds) != 1 {
			return jiraVerificationVerdict{}, err
		}
	}
	if !jiraLineStartsReadAsText(rendered, run.atLineStart) {
		return jiraVerificationVerdict{}, nil
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

// jiraLineStartsReadAsText reports whether every line start of a rendered run is
// one Jira keeps inside the block it was rendered for. A forced newline opens a
// line start wherever it stands, so the scan follows the newlines of the run
// itself and only the run's own first offset depends on the block context.
func jiraLineStartsReadAsText(rendered string, atLineStart bool) bool {
	for offset := 0; offset < len(rendered); {
		if atLineStart {
			if _, markerEnd := jiraListMarkerPrefix(rendered[offset:]); markerEnd != 0 {
				return false
			}
			if jiraLineControlPrefixLength(rendered[offset:]) != 0 {
				return false
			}
		}
		newline := strings.IndexByte(rendered[offset:], '\n')
		if newline < 0 {
			break
		}
		offset, atLineStart = offset+newline+1, true
	}
	return true
}

// jiraCodeBodyKey keys the inline code of a run together with the Text Effects
// and links it sits inside. The nesting is part of the key because a Monospace
// Span that surrounding text has absorbed into an effect or a link still has
// its body intact, and without the structure that reads as preserved code when
// the code has in fact moved.
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
			style, children := string(typed.Style), typed.Children
			if combined, ok := combinedBoldItalic(typed); ok {
				style, children = "bold-italic", combined
			}
			appendVerificationScalar(builder, "s", style)
			builder.WriteByte('(')
			appendJiraCodeBodyKey(builder, children)
			builder.WriteByte(')')
		case linkInline:
			builder.WriteString("k(")
			appendJiraCodeBodyKey(builder, typed.Label)
			builder.WriteByte(')')
		}
	}
}

// jiraVerificationKey serializes the parts of an inline run that a conversion
// must preserve, as a sequence of length-prefixed `tag<length>:<value>;`
// scalars with parentheses around nested runs. The tags are: t text, c inline
// code, br hard break, s Text Effect kind, v Text Effect value, a link target,
// u link is unnamed, m image source, l image alt, and n/w/b for one image
// attribute's name, value and bare flag; ? is an inline no rule below claims.
//
// Source offsets are absent because the rendered run has its own, and adjacent
// text is merged because the split between text inlines is an artifact of how
// each side scanned its source. Attributes a reader derives from the target
// rather than reads from the markup (linkInline.Directive and the Dangerous
// flags) are absent for the same reason.
func jiraVerificationKey(inlines []semanticInline, reparsed bool) string {
	var builder strings.Builder
	appendJiraVerificationKey(&builder, inlines, reparsed)
	return builder.String()
}

func appendJiraVerificationKey(builder *strings.Builder, inlines []semanticInline, reparsed bool) {
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
			text.WriteString(normalizeVerificationText(typed.Text, reparsed))
		case literalInline:
			text.WriteString(normalizeVerificationText(typed.Text, reparsed))
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
			appendJiraVerificationKey(builder, children, reparsed)
			builder.WriteByte(')')
		case linkInline:
			flush()
			appendVerificationScalar(builder, "a", typed.Target)
			appendVerificationScalar(builder, "u", strconv.FormatBool(typed.Unnamed))
			builder.WriteByte('(')
			// An unnamed link shows its target, so its label is derived rather
			// than authored and each side derives it from its own conventions.
			if !typed.Unnamed {
				appendJiraVerificationKey(builder, typed.Label, reparsed)
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

// normalizeVerificationText folds the differences that are not an escaping
// decision. A Jira inline run is one line, so the Jira parser reads a newline
// inside text as the space that joins the line to the next one. On the re-parsed
// side it also decodes numeric character references, which the Jira parser keeps
// verbatim in plain text: the line-control and backslash protections at
// escapeTextForJiraText are the only places this renderer writes one, so
// `h1&#46; x` and `C:&#92;\{x\}` have to compare equal to the text they were
// rendered from.
func normalizeVerificationText(value string, reparsed bool) string {
	if strings.Contains(value, "\n") {
		value = strings.ReplaceAll(value, "\n", " ")
	}
	if !reparsed || !strings.Contains(value, "&#") {
		return value
	}
	var result strings.Builder
	result.Grow(len(value))
	for index := 0; index < len(value); {
		character, referenceEnd := jiraCharacterReference(value, index, len(value))
		if referenceEnd > 0 && character != utf8.RuneError {
			result.WriteRune(character)
			index = referenceEnd
			continue
		}
		result.WriteByte(value[index])
		index++
	}
	return result.String()
}

// renderJiraMonospaceSpanBody encodes a Monospace Span body so that Jira renders
// its characters literally. A decimal character reference is the only encoding a
// span body survives, so it is the only one emitted here.
func renderJiraMonospaceSpanBody(ctx context.Context, body string, render jiraInlineRender) (string, error) {
	encoded := make([]bool, len(body))
	var marked bool
	if render.mode == jiraEscapeFullyEncoded {
		marked = markFullyEncodedMonospaceSpan(body, encoded)
	} else {
		var err error
		if marked, err = markPredictedMonospaceSpanEncoding(ctx, body, encoded, render.inTableCell); err != nil {
			return "", err
		}
	}
	// A body Jira already reads literally is the common case and needs no copy.
	if !marked {
		return body, ctx.Err()
	}
	var result strings.Builder
	result.Grow(len(body))
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

func markFullyEncodedMonospaceSpan(body string, encoded []bool) bool {
	marked := false
	for offset := 0; offset < len(body); {
		character, size := utf8.DecodeRuneInString(body[offset:])
		if character < utf8.RuneSelf && !isASCIIAlphanumericRune(character) || strings.ContainsRune(jiraMonospaceAlwaysEncoded, character) {
			encoded[offset], marked = true, true
		}
		offset += size
	}
	return marked
}

func isASCIIAlphanumericRune(character rune) bool {
	return character < utf8.RuneSelf && isASCIIAlphanumeric(byte(character))
}

// markPredictedMonospaceSpanEncoding marks every byte offset the grammar reports
// as unsafe inside a Monospace Span: the characters a body can never keep raw,
// the `&` that starts a character reference, and the start of each scanned
// hazard whose visible text would differ from the body.
func markPredictedMonospaceSpanEncoding(ctx context.Context, body string, encoded []bool, inTableCell bool) (bool, error) {
	marked := false
	for offset := 0; offset < len(body); {
		character, size := utf8.DecodeRuneInString(body[offset:])
		if strings.ContainsRune(jiraMonospaceAlwaysEncoded, character) || jiraCharacterReferenceStart(body, offset, len(body)) {
			encoded[offset], marked = true, true
		}
		offset += size
	}
	hazards, err := jiraInlineHazards(ctx, body, 0, len(body), jiraMonospaceContext, inTableCell)
	if err != nil {
		return false, err
	}
	mark := func(offsets ...int) {
		for _, offset := range offsets {
			encoded[offset], marked = true, true
		}
	}
	for _, hazard := range hazards {
		switch hazard.Kind {
		case jiraHazardEffect:
			// Both delimiters of a complete pair, because Jira pairs them.
			mark(hazard.Start, hazard.End-1)
		case jiraHazardCitation:
			// One `?` per `??` delimiter is enough to break the pair.
			mark(hazard.Start, hazard.End-2)
		case jiraHazardLink:
			// The opening bracket alone is enough to stop Jira recognizing the
			// link, and only matters when Jira's visible text differs from the
			// body, so a lone `]` stays raw and the body stays readable. An
			// autolink of any scheme is never marked: Jira's autolinker leaves
			// the address visible and a REST read returns the raw markup
			// unchanged, so nothing is lost by leaving it raw.
			if hazard.TextChanges {
				mark(hazard.Start)
			}
		case jiraHazardEmoticon:
			// Every token starts with the `(`, `:` or `;` that identifies it.
			mark(hazard.Start)
		case jiraHazardDash, jiraHazardCellSeparator, jiraHazardTab, jiraHazardEdgeSpace:
			mark(hazard.Start)
		}
	}
	return marked, nil
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
