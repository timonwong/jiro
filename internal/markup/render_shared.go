package markup

import (
	"context"
	"html"
	"strings"
)

func combinedBoldItalic(inline styledInline) ([]semanticInline, bool) {
	if len(inline.Children) != 1 || inline.Style != styleBold && inline.Style != styleItalic {
		return nil, false
	}
	nested, ok := inline.Children[0].(styledInline)
	if !ok || inline.Style == nested.Style || nested.Style != styleBold && nested.Style != styleItalic {
		return nil, false
	}
	return nested.Children, true
}

func imageAttributeOrder() []string {
	return imageAttributeNames("src")
}

// imageAttributeNames spells the image attribute set. The first name is the one
// the surrounding syntax carries: `src` for a JFM image directive, `alt` for a
// Jira image, whose destination precedes the parameters.
func imageAttributeNames(carried string) []string {
	return []string{carried, "thumbnail", "align", "border", "bordercolor", "hspace", "vspace", "width", "height", "title"}
}

func escapeSelectedRunes(ctx context.Context, value, selected string) (string, error) {
	var result strings.Builder
	for offset, character := range value {
		if offset&255 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		if strings.ContainsRune(selected, character) {
			result.WriteByte('\\')
		}
		result.WriteRune(character)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return result.String(), nil
}

func orderDirectiveAttributes(attributes []directiveAttribute, order []string) []directiveAttribute {
	result := make([]directiveAttribute, 0, len(attributes))
	used := make([]bool, len(attributes))
	for _, name := range order {
		for index, attribute := range attributes {
			if !used[index] && strings.EqualFold(attribute.Name, name) {
				attribute.Name = name
				result = append(result, attribute)
				used[index] = true
			}
		}
	}
	for index, attribute := range attributes {
		if !used[index] {
			result = append(result, attribute)
		}
	}
	return result
}

func ensureLiteralClosingSeparation(body string) string {
	if body == "" || strings.HasSuffix(body, "\n") || strings.HasSuffix(body, "\r") {
		return ""
	}
	return "\n"
}

func containsDirectiveAttribute(attributes []directiveAttribute, name string) bool {
	for _, attribute := range attributes {
		if strings.EqualFold(attribute.Name, name) {
			return true
		}
	}
	return false
}

func inlineEndsAtLineStart(inline semanticInline) bool {
	if _, hardBreak := inline.(hardBreakInline); hardBreak {
		return true
	}
	return strings.HasSuffix(inlineLiteralText(inline), "\n")
}

// inlineLiteralText reports the characters an inline writes to the line as
// themselves, and "" for one that writes markup of its own.
func inlineLiteralText(inline semanticInline) string {
	switch typed := inline.(type) {
	case textInline:
		return typed.Text
	case literalInline:
		return typed.Text
	}
	return ""
}

// listItemLineControl reports the line control a list item leads with, as the
// heading level or the quote flag Jira reads at the item's content start, and
// the inlines that control carries. Jira reads `h1.` and `bq.` at the content
// start of every item and renders the heading or the quote inside the item, so
// an item that carries no inline text and leads with one of those blocks is
// written on the item line itself by both renderers rather than flattened out
// of the list. A run that cannot be written on one line is not one of them: the
// item line has nowhere to put a second line, so such an item keeps the
// flattening path both renderers already agree on.
func listItemLineControl(item listItem) (level int, quote bool, inlines []semanticInline, ok bool) {
	if len(item.Inlines) != 0 || len(item.Blocks) == 0 {
		return 0, false, nil, false
	}
	switch typed := item.Blocks[0].(type) {
	case headingBlock:
		if typed.Level < 1 || typed.Level > 6 || !inlinesFitOneLine(typed.Inlines) {
			return 0, false, nil, false
		}
		return typed.Level, false, typed.Inlines, true
	case quoteBlock:
		// A quote with no blocks in it is the one Jira renders empty, which `bq.`
		// spells with nothing after it and Markdown spells as a bare `>`.
		if len(typed.Blocks) == 0 {
			return 0, true, nil, true
		}
		paragraph, isParagraph := typed.Blocks[0].(paragraphBlock)
		if len(typed.Blocks) != 1 || !isParagraph || !inlinesFitOneLine(paragraph.Inlines) {
			return 0, false, nil, false
		}
		return 0, true, paragraph.Inlines, true
	}
	return 0, false, nil, false
}

// inlinesFitOneLine reports whether a run can be written on one line, which a
// run that breaks a line itself or carries an authored newline cannot.
func inlinesFitOneLine(inlines []semanticInline) bool {
	for _, inline := range inlines {
		switch typed := inline.(type) {
		case hardBreakInline:
			return false
		case textInline, literalInline:
			if strings.Contains(inlineLiteralText(inline), "\n") {
				return false
			}
		case styledInline:
			if !inlinesFitOneLine(typed.Children) {
				return false
			}
		case linkInline:
			if !inlinesFitOneLine(typed.Label) {
				return false
			}
		}
	}
	return true
}

// startsCharacterReference reports whether the `&` at offset begins something a
// reader decodes back into a different character: the reference syntax itself,
// or one of the legacy named references Go's html.UnescapeString resolves
// without a terminating semicolon. Jira and Markdown both resolve references, so
// one test serves every renderer that has to keep an authored `&` visible; a `&`
// that begins neither stays raw, so `a & b` keeps its ampersand.
func startsCharacterReference(value string, offset, end int) bool {
	if offset >= end || value[offset] != '&' {
		return false
	}
	if _, referenceEnd := jiraCharacterReference(value, offset, end); referenceEnd > 0 {
		return true
	}
	scan := offset + 1
	if scan < end && value[scan] == '#' {
		scan++
	}
	for scan < end && isASCIIAlphanumeric(value[scan]) {
		scan++
	}
	if scan < end && value[scan] == ';' {
		scan++
	}
	candidate := value[offset:scan]
	return html.UnescapeString(candidate) != candidate
}
