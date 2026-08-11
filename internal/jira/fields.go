package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/timonwong/jiro/internal/apperr"
)

var customFieldID = regexp.MustCompile(`^customfield_[0-9]+$`)

// ResolveFieldSelectors resolves user-facing Field Selectors against Jira
// field metadata while preserving input order and spelling.
func ResolveFieldSelectors(selectors []string, fields []Field) ([]FieldSelection, error) {
	selection := make([]FieldSelection, 0, len(selectors))
	for _, selector := range selectors {
		resolved, err := resolveFieldSelector(selector, fields)
		if err != nil {
			return nil, err
		}
		selection = append(selection, FieldSelection{Selector: selector, Resolved: resolved})
	}
	return selection, nil
}

// FieldSelectorsNeedMetadata reports whether resolving selectors requires a
// Field Metadata Snapshot. It validates the shared selector grammar before a
// caller performs any metadata request.
func FieldSelectorsNeedMetadata(selectors []string) (bool, error) {
	for _, selector := range selectors {
		parsed, err := parseFieldSelector(selector)
		if err != nil {
			return false, err
		}
		if !fieldSelectorBypassesMetadata(parsed.target) {
			return true, nil
		}
	}
	return false, nil
}

// FieldSelectorSelectsMetadataField reports whether selector is a positive
// Field Selector whose target is resolved through Jira field metadata.
func FieldSelectorSelectsMetadataField(selector string) (bool, error) {
	parsed, err := parseFieldSelector(selector)
	if err != nil {
		return false, err
	}
	return !parsed.excluded && !fieldSelectorBypassesMetadata(parsed.target), nil
}

type parsedFieldSelector struct {
	target   string
	excluded bool
}

func parseFieldSelector(selector string) (parsedFieldSelector, error) {
	target := strings.TrimSpace(selector)
	if target == "" {
		return parsedFieldSelector{}, apperr.New(apperr.KindInvalidInput, "field selector is required")
	}
	excluded := strings.HasPrefix(target, "-")
	if excluded {
		target = strings.TrimSpace(strings.TrimPrefix(target, "-"))
		if target == "" {
			return parsedFieldSelector{}, apperr.New(apperr.KindInvalidInput, fmt.Sprintf("field selector %q is missing its exclusion target", selector))
		}
	}
	return parsedFieldSelector{target: target, excluded: excluded}, nil
}

func fieldSelectorBypassesMetadata(target string) bool {
	return customFieldID.MatchString(target) || target == "*all" || target == "*navigable"
}

func resolveFieldSelector(selector string, fields []Field) (string, error) {
	parsed, err := parseFieldSelector(selector)
	if err != nil {
		return "", err
	}
	if fieldSelectorBypassesMetadata(parsed.target) {
		if parsed.excluded {
			return "-" + parsed.target, nil
		}
		return parsed.target, nil
	}
	resolved, err := resolveFieldMetadataSelector(selector, parsed.target, fields)
	if err != nil {
		return "", err
	}
	if parsed.excluded {
		return "-" + resolved, nil
	}
	return resolved, nil
}

func resolveFieldMetadataSelector(selector, target string, fields []Field) (string, error) {
	for _, field := range fields {
		if field.ID == target {
			return field.ID, nil
		}
	}
	alias := Slug(target)
	matches := make([]Field, 0, 1)
	for _, field := range fields {
		if Slug(field.Name) == alias {
			matches = append(matches, field)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0].ID, nil
	case 0:
		return "", apperr.New(apperr.KindInvalidInput, fmt.Sprintf("field selector %q was not found", selector))
	default:
		ids := make([]string, len(matches))
		for i, field := range matches {
			ids[i] = field.ID
		}
		sort.Strings(ids)
		return "", apperr.New(apperr.KindInvalidInput, fmt.Sprintf("field selector %q is ambiguous: %s", selector, strings.Join(ids, ", ")))
	}
}

// ResolveCustomField resolves a jiro custom field alias against this Jira
// instance. A canonical custom field ID never causes a /field lookup.
func (c *Client) ResolveCustomField(ctx context.Context, alias string) (string, error) {
	if customFieldID.MatchString(alias) {
		return alias, nil
	}
	fields, err := c.ListFields(ctx)
	if err != nil {
		return "", err
	}
	return ResolveCustomField(alias, fields)
}

// ResolveCustomField resolves an alias using supplied live field definitions.
// It is useful when callers already fetched /field and want to avoid another
// request.
func ResolveCustomField(alias string, fields []Field) (string, error) {
	if customFieldID.MatchString(alias) {
		return alias, nil
	}
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return "", apperr.New(apperr.KindInvalidInput, "custom field alias is required")
	}
	matches := make([]Field, 0, 1)
	for _, field := range fields {
		if (field.Custom || customFieldID.MatchString(field.ID)) && Slug(field.Name) == alias {
			matches = append(matches, field)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0].ID, nil
	case 0:
		return "", apperr.New(apperr.KindInvalidInput, fmt.Sprintf("custom field alias %q was not found", alias))
	default:
		ids := make([]string, len(matches))
		for i, field := range matches {
			ids[i] = field.ID
		}
		return "", apperr.New(apperr.KindInvalidInput, fmt.Sprintf("custom field alias %q is ambiguous: %s", alias, strings.Join(ids, ", ")))
	}
}

// Slug turns a Jira field display name into its jiro alias. Unicode letters and
// numbers are retained; every run of separators becomes one hyphen.
func Slug(name string) string {
	var result []rune
	separator := false
	for _, char := range strings.TrimSpace(name) {
		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			if separator && len(result) > 0 {
				result = append(result, '-')
			}
			result = append(result, unicode.ToLower(char))
			separator = false
		} else {
			separator = true
		}
	}
	return strings.Trim(string(result), "-")
}

// ParseFieldValues parses repeated key=value flags. Values are decoded as JSON
// first with json.Number preserved, falling back to plain strings.
func ParseFieldValues(values []string) (map[string]any, error) {
	parsed := make(map[string]any, len(values))
	for _, value := range values {
		key, raw, found := strings.Cut(value, "=")
		key = strings.TrimSpace(key)
		if !found || key == "" {
			return nil, apperr.New(apperr.KindInvalidInput, fmt.Sprintf("field %q must be key=value", value))
		}
		parsedValue, err := decodeFieldValue(raw)
		if err != nil {
			return nil, err
		}
		parsed[key] = parsedValue
	}
	return parsed, nil
}

func decodeFieldValue(value string) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err == nil {
		var extra any
		if decoder.Decode(&extra) == io.EOF {
			return decoded, nil
		}
	}
	return value, nil
}
