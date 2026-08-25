package markup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/txtar"
)

// TestInlineCodeBodiesRoundTripLosslessly holds ADR-0018's stricter-than-the-
// specification promise: warning-free inline code conversion is byte-lossless
// for the body. It reads the goldens rather than its own table so that any case
// added to describe rendering also has to survive the round trip, and it
// compares parsed bodies rather than documents because only the body is
// promised: the surrounding markup is free to canonicalize.
func TestInlineCodeBodiesRoundTripLosslessly(t *testing.T) {
	t.Parallel()
	t.Run("from_jfm", func(t *testing.T) {
		t.Parallel()
		forEachWarningFreeGolden(t, "testdata/jfm/from_jfm", "input.md", func(t *testing.T, source string) {
			want := codeSpanBodies(t, parseSourceAsJFM(t, source))
			if len(want) == 0 {
				t.Skip("golden has no inline code")
			}
			jira, err := FromJFM(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			if len(jira.Warnings) != 0 {
				t.Fatalf("FromJFM warnings = %#v, want none", jira.Warnings)
			}
			back, err := ToJFM(context.Background(), jira.Markup)
			if err != nil {
				t.Fatal(err)
			}
			got := codeSpanBodies(t, parseSourceAsJFM(t, back.Markdown))
			assertSameBodies(t, got, want, jira.Markup, back.Markdown)
		})
	})
	t.Run("to_jfm", func(t *testing.T) {
		t.Parallel()
		for _, directory := range []string{"testdata/jfm/to_jfm", "testdata/jfm/jira_evidence"} {
			forEachWarningFreeGolden(t, directory, "input.jira", func(t *testing.T, source string) {
				if !strings.Contains(source, "{{") {
					t.Skip("golden has no Monospace Span")
				}
				jfm, err := ToJFM(context.Background(), source)
				if err != nil {
					t.Fatal(err)
				}
				if len(jfm.Warnings) != 0 {
					t.Fatalf("ToJFM warnings = %#v, want none", jfm.Warnings)
				}
				want := codeSpanBodies(t, parseSourceAsJFM(t, jfm.Markdown))
				if len(want) == 0 {
					t.Skip("converted golden has no inline code")
				}
				jira, err := FromJFM(context.Background(), jfm.Markdown)
				if err != nil {
					t.Fatal(err)
				}
				if len(jira.Warnings) != 0 {
					t.Fatalf("FromJFM warnings = %#v, want none", jira.Warnings)
				}
				document, _ := parseJiraMarkup(context.Background(), jira.Markup)
				assertSameBodies(t, codeSpanBodies(t, document), want, jfm.Markdown, jira.Markup)
			})
		}
	})
}

func assertSameBodies(t *testing.T, got, want []string, stages ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("inline code bodies = %q, want %q (stages %q)", got, want, stages)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("inline code body %d = %q, want %q (stages %q)", index, got[index], want[index], stages)
		}
	}
}

func forEachWarningFreeGolden(t *testing.T, directory, inputSection string, check func(*testing.T, string)) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".txtar" {
			continue
		}
		t.Run(filepath.Base(directory)+"/"+strings.TrimSuffix(entry.Name(), ".txtar"), func(t *testing.T) {
			t.Parallel()
			archive, err := txtar.ParseFile(filepath.Join(directory, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			sections := map[string][]byte{}
			for _, file := range archive.Files {
				sections[file.Name] = file.Data
			}
			var warnings []ConversionWarning
			if err := json.Unmarshal(sections["warnings.json"], &warnings); err != nil {
				t.Fatalf("decode warnings.json: %v", err)
			}
			if len(warnings) != 0 {
				t.Skip("golden expects conversion warnings")
			}
			check(t, string(sections[inputSection]))
		})
	}
}

func parseSourceAsJFM(t *testing.T, source string) semanticDocument {
	t.Helper()
	document, _, err := parseJFM(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func codeSpanBodies(t *testing.T, document semanticDocument) []string {
	t.Helper()
	bodies := make([]string, 0)
	walkInlines := func(inlines []semanticInline) {
		forEachInlineCode(inlines, func(code codeInline) { bodies = append(bodies, code.Text) })
	}
	var walkBlocks func([]semanticBlock)
	walkBlocks = func(blocks []semanticBlock) {
		for _, block := range blocks {
			switch typed := block.(type) {
			case paragraphBlock:
				walkInlines(typed.Inlines)
			case headingBlock:
				walkInlines(typed.Inlines)
			case quoteBlock:
				walkBlocks(typed.Blocks)
			case panelBlock:
				walkBlocks(typed.Blocks)
			case unsupportedMacroBlock:
				walkBlocks(typed.Blocks)
			case listBlock:
				for _, item := range typed.Items {
					walkInlines(item.Inlines)
					walkBlocks(item.Blocks)
				}
			case tableBlock:
				for _, cell := range typed.Header {
					walkInlines(cell.Inlines)
				}
				for _, row := range typed.Rows {
					for _, cell := range row {
						walkInlines(cell.Inlines)
					}
				}
			}
		}
	}
	walkBlocks(document.Blocks)
	return bodies
}
