package markup_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/timonwong/jiro/internal/markup"
)

const realisticJiraChunk = `h2. Deployment notes

The *scheduler* now retries _transient_ failures with {{exponential backoff}}.
See [JIRA-1234|https://issues.example.com/browse/JIRA-1234] for details and the
{color:red}rollback plan{color} before upgrading to the +new+ release train.

* verify the ^new^ configuration against {{jiro issue list}}
* keep the -old- staging server until the smoke tests pass

bq. Upgrades pause consumers for ~a few~ seconds while partitions rebalance.

`

func buildRealisticJira(size int) string {
	var builder strings.Builder
	builder.Grow(size + len(realisticJiraChunk))
	for builder.Len() < size {
		builder.WriteString(realisticJiraChunk)
	}
	return builder.String()
}

func BenchmarkToJFM(b *testing.B) {
	cases := []struct {
		name  string
		build func(size int) string
	}{
		{name: "open-brackets", build: func(size int) string { return strings.Repeat("[", size) }},
		{name: "color-macros", build: func(size int) string { return strings.Repeat("{color:x}", size/len("{color:x}")) }},
		{name: "style-delimiters", build: func(size int) string { return strings.Repeat("^x ", size/len("^x ")) }},
		{name: "realistic-mixed", build: buildRealisticJira},
	}
	for _, benchCase := range cases {
		for _, size := range []int{32 << 10, 100 << 10} {
			b.Run(fmt.Sprintf("%s/%dKB", benchCase.name, size>>10), func(b *testing.B) {
				input := benchCase.build(size)
				ctx := context.Background()
				b.ReportAllocs()
				for b.Loop() {
					if _, err := markup.ToJFM(ctx, input); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
