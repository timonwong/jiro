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
	return []string{"src", "thumbnail", "align", "border", "bordercolor", "hspace", "vspace", "width", "height", "title"}
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
	switch typed := inline.(type) {
	case hardBreakInline:
		return true
	case textInline:
		return strings.HasSuffix(typed.Text, "\n")
	case literalInline:
		return strings.HasSuffix(typed.Text, "\n")
	default:
		return false
	}
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
