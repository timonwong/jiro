package markup_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/timonwong/jiro/internal/markup"
)

// buildFromJFMBenchmarkDocument returns a Jiro Flavored Markdown document of
// approximately targetSize bytes, built from repeating a unit of mixed
// content: a heading, a paragraph mixing emphasis and links, and a periodic
// fenced code block.
func buildFromJFMBenchmarkDocument(targetSize int) string {
	var builder strings.Builder
	for index := 0; builder.Len() < targetSize; index++ {
		fmt.Fprintf(&builder, "## Section %d\n\n", index)
		fmt.Fprintf(&builder, "This paragraph mixes *italic emphasis*, **bold text**, and a [link to the docs](https://example.com/docs/%d) alongside some `inline code` and plain prose that keeps the paragraph long enough to matter for the parser.\n\n", index)
		if index%5 == 0 {
			fmt.Fprintf(&builder, "```go\nfunc handler%d(ctx context.Context) error {\n\treturn nil\n}\n```\n\n", index)
		}
	}
	return builder.String()
}

func BenchmarkFromJFM(b *testing.B) {
	sizes := []struct {
		name   string
		target int
	}{
		{"8KB", 8 * 1024},
		{"32KB", 32 * 1024},
		{"200KB", 200 * 1024},
	}
	for _, size := range sizes {
		document := buildFromJFMBenchmarkDocument(size.target)
		b.Run(size.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(document)))
			for i := 0; i < b.N; i++ {
				if _, err := markup.FromJFM(context.Background(), document); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
