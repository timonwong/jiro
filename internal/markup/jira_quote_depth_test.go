package markup

import (
	"context"
	"testing"
)

func TestParseJiraQuoteAtSafetyLimitFallsBackWithoutFatalError(t *testing.T) {
	t.Parallel()
	const input = "{quote}\nbody\n{quote}"

	document, diagnostics := parseJiraMarkupAtQuoteDepth(context.Background(), input, 0, len(input), maxStructuredQuoteDepth)
	if len(document.Blocks) != 1 {
		t.Fatalf("blocks = %#v; want one literal block", document.Blocks)
	}
	literal, ok := document.Blocks[0].(literalBlock)
	if !ok || literal.Text != input {
		t.Fatalf("block = %#v; want literal %q", document.Blocks[0], input)
	}
	if len(diagnostics) != 1 || diagnostics[0].warning.Construct != ConstructBlockquote || diagnostics[0].warning.Reason != "quote nesting exceeds the maximum structured depth and remains literal" {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}
