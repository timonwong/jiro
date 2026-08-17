package markup

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"unicode"

	"github.com/yuin/goldmark/util"
)

var codeLanguageAliases = map[string]string{
	"javascript": "javascript",
	"js":         "javascript",
	"jsx":        "javascript",
	"mjs":        "javascript",
	"bash":       "bash",
	"sh":         "bash",
	"shell":      "bash",
	"zsh":        "bash",
}

const legacyCodeSpanEscapedDelimiters = `{}[]|-*_`
const jiraCodeSpanEntityEscapedCharacters = `&<>\{}[]|!*?_-+^~:`

func decodedMarkdownText(value []byte, raw bool) []byte {
	if raw {
		return value
	}
	value = util.UnescapePunctuations(value)
	value = util.ResolveNumericReferences(value)
	return util.ResolveEntityNames(value)
}

func dangerousDestinationScheme(destination []byte) (string, bool) {
	separator := bytes.IndexByte(destination, ':')
	if separator <= 0 {
		return "", false
	}
	for index, character := range destination[:separator] {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(index > 0 && ((character >= '0' && character <= '9') || character == '+' || character == '-' || character == '.')) {
			continue
		}
		return "", false
	}
	scheme := strings.ToLower(string(destination[:separator]))
	switch scheme {
	case "javascript", "vbscript", "data":
		return scheme, true
	default:
		return "", false
	}
}

func jiraLineControlPrefixLength(value string) int {
	if len(value) >= 3 && value[0] == 'h' && value[1] >= '1' && value[1] <= '6' && value[2] == '.' &&
		(len(value) == 3 || value[3] == ' ') {
		return 3
	}
	if strings.HasPrefix(value, "bq.") && (len(value) == 3 || value[3] == ' ') {
		return 3
	}
	return 0
}

func escapeCodeSpan(value string) string {
	var result strings.Builder
	for _, character := range value {
		if strings.ContainsRune(jiraCodeSpanEntityEscapedCharacters, character) {
			result.WriteString("&#")
			result.WriteString(strconv.FormatInt(int64(character), 10))
			result.WriteByte(';')
			continue
		}
		result.WriteRune(character)
	}
	return result.String()
}

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

func utf8DecodeRune(value string) (rune, int) {
	for _, character := range value {
		return character, len(string(character))
	}
	return unicode.ReplacementChar, 1
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
