package cmd

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/timonwong/jiro/internal/jira"
	"github.com/timonwong/jiro/internal/output"
)

type issueShowSection struct {
	header string
	value  string
}

type issueShowOutput struct {
	jira.Issue
	FieldSelection []jira.FieldSelection `json:"fieldSelection,omitempty"`
}

func (a *app) renderIssueShow(issue jira.Issue, fieldSelection []jira.FieldSelection) error {
	renderer, err := a.renderer()
	if err != nil {
		return err
	}
	if renderer.Format != output.FormatText {
		return renderer.Success(issueShowOutput{Issue: issue, FieldSelection: fieldSelection})
	}

	return renderer.Success(issueShowPresentation(issue, renderer.IsTTY()))
}

func issueShowPresentation(issue jira.Issue, isTTY bool) []byte {
	sections := issueShowSections(issue)
	var rendered strings.Builder
	for index, section := range sections {
		rendered.WriteString(issueShowHeader(section.header, isTTY))
		rendered.WriteByte('\n')

		value := sanitizeIssueShowValue(section.value)
		if value != "" {
			for _, line := range issueShowPhysicalLines(value) {
				if line != "" {
					rendered.WriteString("    ")
					rendered.WriteString(line)
				}
				rendered.WriteByte('\n')
			}
		}
		if index < len(sections)-1 {
			rendered.WriteByte('\n')
		}
	}

	return []byte(rendered.String())
}

func issueShowPhysicalLines(value string) []string {
	lines := strings.Split(value, "\n")
	if strings.HasSuffix(value, "\n") {
		return lines[:len(lines)-1]
	}
	return lines
}

func issueShowHeader(header string, isTTY bool) string {
	profile := termenv.Ascii
	if isTTY {
		profile = termenv.ANSI
	}
	// The profile is selected solely from jiro's Terminal.IsTTY decision; this
	// deliberately avoids termenv's environment and TTY detection.
	return profile.String(header).Bold().String()
}

func sanitizeIssueShowValue(value string) string {
	value = strings.ReplaceAll(ansi.Strip(value), "\r\n", "\n")
	var sanitized strings.Builder
	sanitized.Grow(len(value))
	for _, char := range value {
		switch char {
		case '\r':
			sanitized.WriteByte('\n')
		case '\n':
			sanitized.WriteByte('\n')
		case '\t':
			sanitized.WriteByte(' ')
		default:
			if !unicode.IsControl(char) && !unicode.Is(unicode.Cf, char) {
				sanitized.WriteRune(char)
			}
		}
	}
	return sanitized.String()
}
