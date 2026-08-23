package markup

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestJiraInlineRunEscalatesThroughBoundedModes drives the harness with a
// verifier that never accepts, which is the only way to reach the later modes:
// no real input reaches them, so without a stub the escalation and the bound on
// re-parses would be unexercised.
func TestJiraInlineRunEscalatesThroughBoundedModes(t *testing.T) {
	t.Parallel()
	const body = "a-b {x}"
	inlines := []semanticInline{codeInline{Span: sourceSpan{Start: 11, End: 20}, Text: body}}
	state := &jiraRenderState{diagnostics: make([]conversionDiagnostic, 0)}
	verified := make([]string, 0, 2)
	rejectEverything := func(_ context.Context, rendered, _ string, _ []semanticInline, _ jiraRunContext) (jiraVerificationVerdict, error) {
		verified = append(verified, rendered)
		return jiraVerificationVerdict{}, nil
	}

	output, err := renderJiraInlineRunWith(context.Background(), state, inlines, jiraRunContext{}, rejectEverything)
	if err != nil {
		t.Fatal(err)
	}

	// Two verification parses and no more, whatever the verifier answers.
	if len(verified) != 2 {
		t.Fatalf("verification calls = %d (%q), want 2", len(verified), verified)
	}
	// The predicted mode encodes what the grammar reports; the full-encode mode
	// encodes every ASCII character that is not a letter or digit.
	if want := "{{a-b &#123;x&#125;}}"; verified[0] != want {
		t.Errorf("predicted render = %q, want %q", verified[0], want)
	}
	if want := "{{a&#45;b&#32;&#123;x&#125;}}"; verified[1] != want {
		t.Errorf("full-encode render = %q, want %q", verified[1], want)
	}
	// The literal fallback abandons the Monospace Span and sends the body
	// through the fully escaped plain-text escaper.
	if want := `a\-b \{x\}`; output != want {
		t.Errorf("literal fallback = %q, want %q", output, want)
	}
	wantDiagnostics := []conversionDiagnostic{{
		offset: 11,
		warning: ConversionWarning{
			Construct: ConstructInlineCode,
			Reason:    "inline code could not be protected from Jira reinterpretation; emitted as plain text",
		},
	}}
	if !reflect.DeepEqual(state.diagnostics, wantDiagnostics) {
		t.Errorf("diagnostics = %#v, want %#v", state.diagnostics, wantDiagnostics)
	}
}

// TestJiraInlineRunSkipsVerificationWithoutHazards holds the cost bound: prose
// that carries no character any Jira inline rule starts from reads back as
// itself, so it must not pay for a re-parse.
func TestJiraInlineRunSkipsVerificationWithoutHazards(t *testing.T) {
	t.Parallel()
	inlines := []semanticInline{textInline{Span: sourceSpan{Start: 0, End: 5}, Text: "plain"}}
	state := &jiraRenderState{diagnostics: make([]conversionDiagnostic, 0)}
	calls := 0
	countCalls := func(context.Context, string, string, []semanticInline, jiraRunContext) (jiraVerificationVerdict, error) {
		calls++
		return jiraVerificationVerdict{matched: true}, nil
	}
	output, err := renderJiraInlineRunWith(context.Background(), state, inlines, jiraRunContext{}, countCalls)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 || output != "plain" || len(state.diagnostics) != 0 {
		t.Fatalf("calls = %d, output = %q, diagnostics = %#v; want 0, \"plain\", none", calls, output, state.diagnostics)
	}
}

// TestJiraPlainTextRunFallsBackFullyEscaped drives a run without inline code to
// the end of the escalation. No real input reaches it, so only a verifier that
// never accepts exercises the fully escaped output and the plain-text warning.
func TestJiraPlainTextRunFallsBackFullyEscaped(t *testing.T) {
	t.Parallel()
	inlines := []semanticInline{
		textInline{Span: sourceSpan{Start: 4, End: 8}, Text: "a-b "},
		styledInline{Span: sourceSpan{Start: 8, End: 15}, Style: styleBold, Children: []semanticInline{
			textInline{Span: sourceSpan{Start: 10, End: 11}, Text: "c"},
		}},
	}
	state := &jiraRenderState{diagnostics: make([]conversionDiagnostic, 0)}
	verified := make([]string, 0, 2)
	rejectEverything := func(_ context.Context, rendered, _ string, _ []semanticInline, _ jiraRunContext) (jiraVerificationVerdict, error) {
		verified = append(verified, rendered)
		return jiraVerificationVerdict{}, nil
	}

	output, err := renderJiraInlineRunWith(context.Background(), state, inlines, jiraRunContext{}, rejectEverything)
	if err != nil {
		t.Fatal(err)
	}

	if len(verified) != 2 {
		t.Fatalf("verification calls = %d (%q), want 2", len(verified), verified)
	}
	// The predicted mode leaves an unpaired `-` alone and writes the effect
	// bare; the fully escaped mode escapes every plain-text delimiter and writes
	// the effect in the brace form.
	if want := "a-b *c*"; verified[0] != want {
		t.Errorf("predicted render = %q, want %q", verified[0], want)
	}
	if want := `a\-b {*}c{*}`; verified[1] != want {
		t.Errorf("fully escaped render = %q, want %q", verified[1], want)
	}
	if output != verified[1] {
		t.Errorf("fallback = %q, want the fully escaped render %q", output, verified[1])
	}
	wantDiagnostics := []conversionDiagnostic{{
		offset: 4,
		warning: ConversionWarning{
			Construct: ConstructPlainText,
			Reason:    "plain text could not be verified to read back as written on Jira; emitted fully escaped",
		},
	}}
	if !reflect.DeepEqual(state.diagnostics, wantDiagnostics) {
		t.Errorf("diagnostics = %#v, want %#v", state.diagnostics, wantDiagnostics)
	}
}

// TestJiraCodeBodyKeySeparatesNestingLevels holds the attribution rule's
// precision: a Monospace Span that surrounding markup has absorbed into a Text
// Effect or a link keeps its body, so only the nesting in the key stops that
// from reading as preserved code.
func TestJiraCodeBodyKeySeparatesNestingLevels(t *testing.T) {
	t.Parallel()
	code := codeInline{Text: "x"}
	bare := []semanticInline{code}
	inEffect := []semanticInline{styledInline{Style: styleBold, Children: []semanticInline{code}}}
	inLink := []semanticInline{linkInline{Target: "u", Label: []semanticInline{code}}}
	keys := map[string]string{
		"bare":     jiraCodeBodyKey(bare),
		"inEffect": jiraCodeBodyKey(inEffect),
		"inLink":   jiraCodeBodyKey(inLink),
	}
	for left := range keys {
		for right := range keys {
			if left < right && keys[left] == keys[right] {
				t.Errorf("%s and %s share the key %q", left, right, keys[left])
			}
		}
	}
}

// TestJiraPlainTextHazardBytesStayNarrow holds both halves of the cost bound:
// every byte the plain-text escaper can write differently must force a re-parse,
// and nothing else may. The emoticon and dash characters are the ones that would
// silently widen it, because plain text keeps them as Jira semantics.
func TestJiraPlainTextHazardBytesStayNarrow(t *testing.T) {
	t.Parallel()
	if want := `&{}[]!|#*_-+^~?\`; sortedBytes(jiraPlainTextHazardBytes) != sortedBytes(want) {
		t.Fatalf("hazard bytes = %q, want the characters of %q", jiraPlainTextHazardBytes, want)
	}
	for _, character := range "()%@:;" {
		if strings.ContainsRune(jiraPlainTextHazardBytes, character) {
			t.Errorf("%q forces a re-parse although plain text never escapes it", character)
		}
	}
}

func sortedBytes(value string) string {
	bytes := []byte(value)
	sort.Slice(bytes, func(left, right int) bool { return bytes[left] < bytes[right] })
	return string(bytes)
}
