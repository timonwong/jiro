package markup

import (
	"context"
	"unicode"
	"unicode/utf8"
)

// This file is the single Jira inline grammar shared by the Jira parser and the
// Jira renderer escapers (ADR-0018). Its rules are backed by probes against Jira
// Server 8.20.10 (hack/jira-render-evidence.py); do not add a rule without
// evidence.

func jiraInlineStyle(delimiter byte) (inlineStyle, bool) {
	switch delimiter {
	case '*':
		return styleBold, true
	case '_':
		return styleItalic, true
	case '-':
		return styleStrike, true
	case '+':
		return styleInserted, true
	case '^':
		return styleSuper, true
	case '~':
		return styleSub, true
	default:
		return "", false
	}
}

// isJiraWordRune reports whether value blocks an Effect Delimiter from opening
// (when preceding it) or from closing (when following it). Jira counts every
// Unicode letter or digit, so CJK and accented text tokenize like ASCII words;
// `_`, `-` and other punctuation are boundaries rather than word characters.
func isJiraWordRune(value rune) bool {
	return unicode.IsLetter(value) || unicode.IsDigit(value)
}

// isJiraEffectSpace reports Jira's notion of whitespace next to an Effect
// Delimiter. Jira recognizes ASCII whitespace only: `*<U+00A0>x*` and
// `*<U+3000>x<U+3000>*` still render bold, so unicode.IsSpace would reject
// openers Jira accepts.
func isJiraEffectSpace(value rune) bool {
	switch value {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	default:
		return false
	}
}

// jiraEffectCanOpen reports whether the Effect Delimiter at source[offset] may
// open a Text Effect inside the inline run source[start:end]. Ranges are
// half-open and every caller's range begins at a line start or directly after a
// delimiter or structural character, so an offset at start has no preceding
// rune rather than an unknown one.
func jiraEffectCanOpen(source string, start, offset, end int) bool {
	if offset+1 >= end {
		return false
	}
	next, _ := utf8.DecodeRuneInString(source[offset+1 : end])
	if isJiraEffectSpace(next) {
		return false
	}
	previous, size := utf8.DecodeLastRuneInString(source[start:offset])
	return size == 0 || !isJiraWordRune(previous)
}

// findJiraEffectClose scans source[start:end] for the Effect Delimiter closing
// an opener at start-1. The plain-text escaper passes the delimiters it has
// already decided to backslash-escape as ignored, because Jira no longer reads
// an escaped delimiter as a closer; the parser has no such set and passes nil.
func findJiraEffectClose(ctx context.Context, source string, start, end int, delimiter byte, ignored []bool) (int, error) {
	for index := start; index < end; index++ {
		if (index-start)&255 == 0 {
			if err := ctx.Err(); err != nil {
				return -1, err
			}
		}
		if source[index] == '\\' {
			index++
			continue
		}
		if source[index] != delimiter || index <= start {
			continue
		}
		if ignored != nil && ignored[index] {
			continue
		}
		if previous, _ := utf8.DecodeLastRuneInString(source[start:index]); isJiraEffectSpace(previous) {
			continue
		}
		if next, size := utf8.DecodeRuneInString(source[index+1 : end]); size != 0 && isJiraWordRune(next) {
			continue
		}
		return index, nil
	}
	return -1, ctx.Err()
}
