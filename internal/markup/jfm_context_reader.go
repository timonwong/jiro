package markup

import (
	"context"
	"regexp"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

type conversionContextAbort struct {
	err error
}

// abortConversionOnCancel panics with conversionContextAbort when ctx is
// cancelled; ToJFM and FromJFM recover it into their error result. Cancellation
// travels as a panic because every scan helper below them can only fail this
// one way, and threading an error result through all of them buys nothing.
func abortConversionOnCancel(ctx context.Context) {
	if err := ctx.Err(); err != nil {
		panic(conversionContextAbort{err: err})
	}
}

// sampleConversionCancel is abortConversionOnCancel sampled to every 256th
// counter value, for the byte-granular scan loops. The sampling stride bounds
// how much work a cancelled conversion can still do inside one long scan, so
// widening it would weaken the cancellation contract the tests pin.
func sampleConversionCancel(ctx context.Context, counter int) {
	if counter&255 == 0 {
		abortConversionOnCancel(ctx)
	}
}

type contextCheckingReader struct {
	text.Reader
	ctx context.Context
}

func (reader *contextCheckingReader) check() {
	if err := reader.ctx.Err(); err != nil {
		panic(conversionContextAbort{err: err})
	}
}

func (reader *contextCheckingReader) ReadRune() (rune, int, error) {
	reader.check()
	return reader.Reader.ReadRune()
}

func (reader *contextCheckingReader) Peek() byte {
	reader.check()
	return reader.Reader.Peek()
}

func (reader *contextCheckingReader) PeekLine() ([]byte, text.Segment) {
	reader.check()
	return reader.Reader.PeekLine()
}

func (reader *contextCheckingReader) Advance(length int) {
	reader.check()
	reader.Reader.Advance(length)
}

func (reader *contextCheckingReader) AdvanceAndSetPadding(length, padding int) {
	reader.check()
	reader.Reader.AdvanceAndSetPadding(length, padding)
}

func (reader *contextCheckingReader) AdvanceToEOL() {
	reader.check()
	reader.Reader.AdvanceToEOL()
}

func (reader *contextCheckingReader) AdvanceLine() {
	reader.check()
	reader.Reader.AdvanceLine()
}

func (reader *contextCheckingReader) SkipSpaces() (text.Segment, int, bool) {
	reader.check()
	return reader.Reader.SkipSpaces()
}

func (reader *contextCheckingReader) SkipBlankLines() (text.Segment, int, bool) {
	reader.check()
	return reader.Reader.SkipBlankLines()
}

func (reader *contextCheckingReader) Match(expression *regexp.Regexp) bool {
	reader.check()
	return reader.Reader.Match(expression)
}

func (reader *contextCheckingReader) FindSubMatch(expression *regexp.Regexp) [][]byte {
	reader.check()
	return reader.Reader.FindSubMatch(expression)
}

func (reader *contextCheckingReader) FindClosure(opener, closer byte, options text.FindClosureOptions) (*text.Segments, bool) {
	reader.check()
	return reader.Reader.FindClosure(opener, closer, options)
}

func parseGoldmarkWithContext(ctx context.Context, markdown goldmark.Markdown, source []byte) ast.Node {
	reader := &contextCheckingReader{Reader: text.NewReader(source), ctx: ctx}
	document := markdown.Parser().Parse(reader)
	abortConversionOnCancel(ctx)
	return document
}
