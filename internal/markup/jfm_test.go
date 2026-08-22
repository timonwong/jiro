package markup_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/timonwong/jiro/internal/markup"
	"golang.org/x/tools/txtar"
)

func TestToJFMGolden(t *testing.T) {
	t.Parallel()
	convert := func(ctx context.Context, input string) (string, []markup.ConversionWarning, error) {
		result, err := markup.ToJFM(ctx, input)
		return result.Markdown, result.Warnings, err
	}
	runJFMGoldens(t, "testdata/jfm/to_jfm", "input.jira", "want.md", convert)
	runJFMGoldens(t, "testdata/jfm/jira_evidence", "input.jira", "want.md", convert)
}

// TestJiraEvidenceFixturesAreComplete guards the contract that makes the
// jira_evidence directory usable as grammar evidence: every archive names the
// renderer it was captured from and carries the verbatim response that justifies
// its expectation.
func TestJiraEvidenceFixturesAreComplete(t *testing.T) {
	t.Parallel()
	const directory = "testdata/jfm/jira_evidence"
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no Jira renderer evidence fixtures")
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".txtar" {
			t.Fatalf("unexpected non-archive entry %q", entry.Name())
		}
		t.Run(strings.TrimSuffix(entry.Name(), ".txtar"), func(t *testing.T) {
			t.Parallel()
			archive, err := txtar.ParseFile(filepath.Join(directory, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(string(archive.Comment), "source:") {
				t.Fatalf("comment header = %q, want a source: line naming the renderer", archive.Comment)
			}
			if !strings.Contains(string(archive.Comment), "\nobserved:") {
				t.Fatalf("comment header = %q, want an observed: line", archive.Comment)
			}
			sections := map[string][]byte{}
			for _, file := range archive.Files {
				sections[file.Name] = file.Data
			}
			for _, required := range []string{"input.jira", "rendered.html", "want.md", "warnings.json"} {
				if len(sections[required]) == 0 {
					t.Fatalf("section %q is missing or empty", required)
				}
			}
		})
	}
}

func TestFromJFMGolden(t *testing.T) {
	t.Parallel()
	runJFMGoldens(t, "testdata/jfm/from_jfm", "input.md", "want.jira", func(ctx context.Context, input string) (string, []markup.ConversionWarning, error) {
		result, err := markup.FromJFM(ctx, input)
		return result.Markup, result.Warnings, err
	})
}

func TestFromJFMInlineCodePreservesJiraSafeEscapes(t *testing.T) {
	t.Parallel()
	result, err := markup.FromJFM(context.Background(), "`&amp; <tag> x\\-y`")
	if err != nil {
		t.Fatal(err)
	}
	if result.Markup != "{{&amp; <tag> x\\-y}}" || len(result.Warnings) != 0 {
		t.Fatalf("result = %#v, want Jira-safe wire output without warnings", result)
	}
}

func TestJFMSpecExamplesConform(t *testing.T) {
	t.Parallel()
	source, err := os.ReadFile("../../docs/jiro-flavored-markdown.md")
	if err != nil {
		t.Fatal(err)
	}
	pattern := regexp.MustCompile(`(?ms)<!-- jfm-spec-example: ([a-z0-9-]+); direction: (jfm-to-jira|jira-to-jfm) -->\nInput:\n~~~(jfm|jira)\n(.*?)\n~~~\n\nOutput:\n~~~(jfm|jira)\n(.*?)\n~~~\n\nWarnings:\n~~~json\n(.*?)\n~~~`)
	matches := pattern.FindAllStringSubmatch(string(source), -1)
	markerCount := strings.Count(string(source), "<!-- jfm-spec-example:")
	if len(matches) != markerCount {
		t.Fatalf("parsed %d of %d specification example markers", len(matches), markerCount)
	}
	if len(matches) < 8 {
		t.Fatalf("spec contains %d executable examples, want at least 8", len(matches))
	}
	seen := map[string]bool{}
	for _, match := range matches {
		name, direction := match[1], match[2]
		inputLanguage, input := match[3], match[4]
		outputLanguage, want := match[5], match[6]
		if seen[name] {
			t.Fatalf("duplicate specification example name %q", name)
		}
		seen[name] = true
		wantInputLanguage, wantOutputLanguage := "jfm", "jira"
		if direction == "jira-to-jfm" {
			wantInputLanguage, wantOutputLanguage = "jira", "jfm"
		}
		if inputLanguage != wantInputLanguage || outputLanguage != wantOutputLanguage {
			t.Fatalf("example %s direction %s uses %s input and %s output fences", name, direction, inputLanguage, outputLanguage)
		}
		var wantWarnings []markup.ConversionWarning
		if err := json.Unmarshal([]byte(match[7]), &wantWarnings); err != nil {
			t.Fatalf("example %s warnings: %v", name, err)
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var got string
			var gotWarnings []markup.ConversionWarning
			if direction == "jfm-to-jira" {
				result, err := markup.FromJFM(context.Background(), input)
				if err != nil {
					t.Fatal(err)
				}
				got, gotWarnings = result.Markup, result.Warnings
			} else {
				result, err := markup.ToJFM(context.Background(), input)
				if err != nil {
					t.Fatal(err)
				}
				got, gotWarnings = result.Markdown, result.Warnings
			}
			if got != want {
				t.Fatalf("output = %q, want %q", got, want)
			}
			if !reflect.DeepEqual(gotWarnings, wantWarnings) {
				t.Fatalf("warnings = %#v, want %#v", gotWarnings, wantWarnings)
			}
		})
	}
}

func TestJFMConversionHonorsCancelledContextWithoutPartialResult(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	jfm, err := markup.ToJFM(ctx, "h1. Release")
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(jfm, markup.JFMResult{}) {
		t.Fatalf("ToJFM() = %#v, %v; want zero result and context.Canceled", jfm, err)
	}
	jira, err := markup.FromJFM(ctx, "# Release")
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(jira, markup.JiraMarkupResult{}) {
		t.Fatalf("FromJFM() = %#v, %v; want zero result and context.Canceled", jira, err)
	}
}

func TestJFMConversionPreservesDeadlineExceededWithoutPartialResult(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	jfm, err := markup.ToJFM(ctx, "h1. Release")
	if !errors.Is(err, context.DeadlineExceeded) || !reflect.DeepEqual(jfm, markup.JFMResult{}) {
		t.Fatalf("ToJFM() = %#v, %v; want zero result and context.DeadlineExceeded", jfm, err)
	}
	jira, err := markup.FromJFM(ctx, "# Release")
	if !errors.Is(err, context.DeadlineExceeded) || !reflect.DeepEqual(jira, markup.JiraMarkupResult{}) {
		t.Fatalf("FromJFM() = %#v, %v; want zero result and context.DeadlineExceeded", jira, err)
	}
}

func TestFromJFMObservesCancellationDuringGoldmarkParsing(t *testing.T) {
	t.Parallel()
	ctx := &cancelAfterChecksContext{}
	ctx.remaining.Store(32)
	input := strings.Repeat("paragraph with **formatting** and [link](https://example.com)\n\n", 1000)
	result, err := markup.FromJFM(ctx, input)
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(result, markup.JiraMarkupResult{}) {
		t.Fatalf("FromJFM() = %#v, %v; want zero result and context.Canceled", result, err)
	}
}

func TestToJFMObservesCancellationDuringSerialization(t *testing.T) {
	t.Parallel()
	ctx := &cancelAfterChecksContext{}
	ctx.remaining.Store(8)
	result, err := markup.ToJFM(ctx, strings.Repeat("*x* ", 1000))
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(result, markup.JFMResult{}) {
		t.Fatalf("ToJFM() = %#v, %v; want zero result and context.Canceled", result, err)
	}
}

func TestToJFMObservesCancellationDuringSingleLargeScalar(t *testing.T) {
	t.Parallel()
	ctx := &cancelAfterChecksContext{}
	ctx.remaining.Store(64)
	result, err := markup.ToJFM(ctx, strings.Repeat("a", 1<<20))
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(result, markup.JFMResult{}) {
		t.Fatalf("ToJFM() = %#v, %v; want zero result and context.Canceled", result, err)
	}
}

func TestToJFMObservesCancellationDuringLongDelimitedScan(t *testing.T) {
	t.Parallel()
	ctx := &cancelAfterChecksContext{}
	ctx.remaining.Store(64)
	result, err := markup.ToJFM(ctx, "[label|"+strings.Repeat("a", 1<<20)+"]")
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(result, markup.JFMResult{}) {
		t.Fatalf("ToJFM() = %#v, %v; want zero result and context.Canceled", result, err)
	}
}

func TestToJFMObservesCancellationDuringLongStyleScan(t *testing.T) {
	t.Parallel()
	ctx := &cancelAfterChecksContext{}
	ctx.remaining.Store(64)
	result, err := markup.ToJFM(ctx, "*"+strings.Repeat("a", 1<<20)+"*")
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(result, markup.JFMResult{}) {
		t.Fatalf("ToJFM() = %#v, %v; want zero result and context.Canceled", result, err)
	}
}

func TestToJFMObservesCancellationDuringLongMonospaceScan(t *testing.T) {
	t.Parallel()
	ctx := &cancelAfterChecksContext{}
	ctx.remaining.Store(64)
	result, err := markup.ToJFM(ctx, "{{"+strings.Repeat("a", 1<<20))
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(result, markup.JFMResult{}) {
		t.Fatalf("ToJFM() = %#v, %v; want zero result and context.Canceled", result, err)
	}
}

func TestFromJFMObservesCancellationDuringLongDirectiveSerialization(t *testing.T) {
	t.Parallel()
	ctx := &cancelAfterChecksContext{}
	ctx.remaining.Store(64)
	result, err := markup.FromJFM(ctx, `:image[]{src="`+strings.Repeat("a", 1<<20)+`"}`)
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(result, markup.JiraMarkupResult{}) {
		t.Fatalf("FromJFM() = %#v, %v; want zero result and context.Canceled", result, err)
	}
}

type cancelAfterChecksContext struct {
	remaining atomic.Int32
}

func (ctx *cancelAfterChecksContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (ctx *cancelAfterChecksContext) Done() <-chan struct{}       { return nil }
func (ctx *cancelAfterChecksContext) Value(any) any               { return nil }
func (ctx *cancelAfterChecksContext) Err() error {
	if ctx.remaining.Add(-1) <= 0 {
		return context.Canceled
	}
	return nil
}

func TestJFMConversionReplacesEachInvalidUTF8ByteWithPositionedWarning(t *testing.T) {
	t.Parallel()
	input := string([]byte{'a', 0xff, 'b', 0xfe})
	wantWarnings := []markup.ConversionWarning{
		{Line: 1, Column: 2, Construct: markup.ConstructUTF8, Reason: "invalid UTF-8 byte was replaced with U+FFFD"},
		{Line: 1, Column: 4, Construct: markup.ConstructUTF8, Reason: "invalid UTF-8 byte was replaced with U+FFFD"},
	}
	jfm, err := markup.ToJFM(context.Background(), input)
	if err != nil || jfm.Markdown != "a�b�" || !reflect.DeepEqual(jfm.Warnings, wantWarnings) || !utf8.ValidString(jfm.Markdown) {
		t.Fatalf("ToJFM() = %#v, %v", jfm, err)
	}
	jira, err := markup.FromJFM(context.Background(), input)
	if err != nil || jira.Markup != "a�b�" || !reflect.DeepEqual(jira.Warnings, wantWarnings) || !utf8.ValidString(jira.Markup) {
		t.Fatalf("FromJFM() = %#v, %v", jira, err)
	}
}

func TestJFMWarningFreeCanonicalRoundTripsStabilize(t *testing.T) {
	t.Parallel()
	for _, direction := range []struct {
		name      string
		directory string
		input     string
	}{
		{name: "Jira to JFM to Jira", directory: "testdata/jfm/to_jfm", input: "input.jira"},
		{name: "JFM to Jira to JFM", directory: "testdata/jfm/from_jfm", input: "input.md"},
	} {
		direction := direction
		t.Run(direction.name, func(t *testing.T) {
			entries, err := os.ReadDir(direction.directory)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if filepath.Ext(entry.Name()) != ".txtar" {
					continue
				}
				entry := entry
				t.Run(strings.TrimSuffix(entry.Name(), ".txtar"), func(t *testing.T) {
					archive, err := txtar.ParseFile(filepath.Join(direction.directory, entry.Name()))
					if err != nil {
						t.Fatal(err)
					}
					sections := map[string]string{}
					for _, file := range archive.Files {
						sections[file.Name] = string(file.Data)
					}
					var expectedWarnings []markup.ConversionWarning
					if err := json.Unmarshal([]byte(sections["warnings.json"]), &expectedWarnings); err != nil {
						t.Fatal(err)
					}
					if len(expectedWarnings) != 0 {
						return
					}
					if direction.input == "input.jira" {
						first, err := markup.ToJFM(context.Background(), sections[direction.input])
						if err != nil || len(first.Warnings) != 0 {
							t.Fatalf("first ToJFM = %#v, %v", first, err)
						}
						middle, err := markup.FromJFM(context.Background(), first.Markdown)
						if err != nil || len(middle.Warnings) != 0 {
							t.Fatalf("FromJFM = %#v, %v", middle, err)
						}
						last, err := markup.ToJFM(context.Background(), middle.Markup)
						if err != nil || len(last.Warnings) != 0 || last.Markdown != first.Markdown {
							t.Fatalf("round trip = %#v, %v; want Markdown %q", last, err, first.Markdown)
						}
						return
					}
					first, err := markup.FromJFM(context.Background(), sections[direction.input])
					if err != nil || len(first.Warnings) != 0 {
						t.Fatalf("first FromJFM = %#v, %v", first, err)
					}
					middle, err := markup.ToJFM(context.Background(), first.Markup)
					if err != nil || len(middle.Warnings) != 0 {
						t.Fatalf("ToJFM = %#v, %v", middle, err)
					}
					last, err := markup.FromJFM(context.Background(), middle.Markdown)
					if err != nil || len(last.Warnings) != 0 || last.Markup != first.Markup {
						t.Fatalf("round trip = %#v, %v; want Markup %q", last, err, first.Markup)
					}
				})
			}
		})
	}
}

func TestJFMLossyComplexListStabilizesAfterFlattening(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name         string
		input        string
		want         string
		warningCount int
	}{
		{name: "nested list after paragraph", input: "- first\n\n  second\n\n  - child", want: "* first\n\nsecond\n\n* child", warningCount: 1},
		{name: "ordered nested item continuation", input: "1. a\n    1. b\n\n       c", want: "# a\n# b\n\nc", warningCount: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			first, err := markup.FromJFM(context.Background(), test.input)
			if err != nil || len(first.Warnings) != test.warningCount || first.Markup != test.want {
				t.Fatalf("first FromJFM() = %#v, %v; want Markup %q", first, err, test.want)
			}
			middle, err := markup.ToJFM(context.Background(), first.Markup)
			if err != nil || len(middle.Warnings) != 0 {
				t.Fatalf("ToJFM() = %#v, %v", middle, err)
			}
			last, err := markup.FromJFM(context.Background(), middle.Markdown)
			if err != nil || len(last.Warnings) != 0 || last.Markup != first.Markup {
				t.Fatalf("stabilized FromJFM() = %#v, %v; want Markup %q", last, err, first.Markup)
			}
		})
	}
}

func TestJFMQuoteNestingBeyondSafetyLimitFallsBackWithoutFatalError(t *testing.T) {
	t.Parallel()
	const depth = 65
	jfmInput := strings.Repeat("> ", depth) + "body"
	jira, err := markup.FromJFM(context.Background(), jfmInput)
	if err != nil || len(jira.Warnings) == 0 || jira.Warnings[0].Construct != markup.ConstructBlockquote {
		t.Fatalf("FromJFM() = %#v, %v", jira, err)
	}
}

func TestJFMConversionIsDeterministicUnderConcurrentCalls(t *testing.T) {
	t.Parallel()
	const workers = 32
	const iterations = 25
	start := make(chan struct{})
	problems := make(chan string, workers)
	var wait sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			for range iterations {
				jfm, err := markup.ToJFM(context.Background(), "{panel:title=Data}\nh1. Release\n{panel}")
				if err != nil || jfm.Markdown != ":::panel{title=\"Data\"}\n# Release\n:::" || len(jfm.Warnings) != 0 {
					problems <- "non-deterministic ToJFM result"
					return
				}
				jira, err := markup.FromJFM(context.Background(), jfm.Markdown)
				if err != nil || jira.Markup != "{panel:title=Data}\nh1. Release\n{panel}" || len(jira.Warnings) != 0 {
					problems <- "non-deterministic FromJFM result"
					return
				}
			}
		}()
	}
	close(start)
	wait.Wait()
	close(problems)
	for problem := range problems {
		t.Error(problem)
	}
}

func runJFMGoldens(t *testing.T, directory, inputSection, outputSection string, convert func(context.Context, string) (string, []markup.ConversionWarning, error)) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".txtar" {
			continue
		}
		t.Run(strings.TrimSuffix(entry.Name(), ".txtar"), func(t *testing.T) {
			t.Parallel()
			archive, err := txtar.ParseFile(filepath.Join(directory, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			sections := map[string][]byte{}
			for _, file := range archive.Files {
				sections[file.Name] = file.Data
			}
			for _, required := range []string{inputSection, outputSection, "warnings.json"} {
				if _, ok := sections[required]; !ok {
					t.Fatalf("missing required txtar section %q", required)
				}
			}
			var wantWarnings []markup.ConversionWarning
			if err := json.Unmarshal(sections["warnings.json"], &wantWarnings); err != nil {
				t.Fatalf("decode warnings.json: %v", err)
			}
			got, warnings, err := convert(context.Background(), string(sections[inputSection]))
			if err != nil {
				t.Fatal(err)
			}
			// txtar sections are line-oriented, while canonical converted documents
			// deliberately have no terminal newline.
			want := strings.TrimSuffix(string(sections[outputSection]), "\n")
			if got != want {
				t.Fatalf("output = %q, want %q", got, want)
			}
			if !reflect.DeepEqual(warnings, wantWarnings) {
				t.Fatalf("warnings = %#v, want %#v", warnings, wantWarnings)
			}
		})
	}
}
