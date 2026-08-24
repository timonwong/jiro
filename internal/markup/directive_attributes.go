package markup

import "strings"

// directiveAttributeRule is one diagnostic a schema may report; an empty reason
// means the directive has no such rule.
type directiveAttributeRule struct{ construct, reason string }

// directiveAttributeSchema spells one directive's attribute rules for
// validateDirectiveAttributes; Jira macro parameters and JFM directive
// attributes share the walk and differ only in the rules and flags here.
type directiveAttributeSchema struct {
	// known lists the canonical spellings; a case-insensitive match is rewritten.
	known    []string
	booleans map[string]bool
	// presenceOnly is exempt from the bare rule and reports carrying a value.
	presenceOnly string
	// extract is carried by the surrounding syntax: its first occurrence with a
	// value is returned instead of kept.
	extract     string
	invalidName directiveAttributeRule
	// dropInvalidName leaves an unspellable attribute out of the kept slice.
	dropInvalidName bool
	unknown         directiveAttributeRule
	// unknownFallsThrough runs the remaining rules for an unknown attribute, so it
	// also takes part in duplicate tracking; elsewhere it is preserved as written.
	unknownFallsThrough bool
	duplicate           directiveAttributeRule
	presenceValue       directiveAttributeRule
	bare                directiveAttributeRule
	// bareInvalid means a value-less attribute leaves the whole source literal.
	bareInvalid bool
	boolean     directiveAttributeRule
}

func (schema directiveAttributeSchema) canonicalName(name string) (string, bool) {
	for _, canonical := range schema.known {
		if strings.EqualFold(name, canonical) {
			return canonical, true
		}
	}
	return "", false
}

// validateDirectiveAttributes canonicalizes attributes against schema and
// reports every rule it carries. An extract the source did not carry comes back
// as the zero attribute, which an extract written with an empty value is not.
// An invalid result means the source cannot be represented at all, which
// callers answer by leaving it literal.
func validateDirectiveAttributes(attributes []directiveAttribute, schema directiveAttributeSchema) (directiveAttribute, []directiveAttribute, []conversionDiagnostic, bool) {
	extracted, invalid := directiveAttribute{}, false
	kept := make([]directiveAttribute, 0, len(attributes))
	diagnostics := make([]conversionDiagnostic, 0)
	seen := map[string]bool{}
	report := func(rule directiveAttributeRule, attribute directiveAttribute) {
		diagnostics = append(diagnostics, conversionDiagnostic{offset: attribute.Span.Start, warning: ConversionWarning{Construct: rule.construct, Reason: rule.reason}})
	}
	for _, attribute := range attributes {
		if schema.invalidName.reason != "" && !validDirectiveAttributeName(attribute.Name) {
			report(schema.invalidName, attribute)
			invalid = true
			if !schema.dropInvalidName {
				kept = append(kept, attribute)
			}
			continue
		}
		key := strings.ToLower(attribute.Name)
		if canonical, known := schema.canonicalName(attribute.Name); known {
			attribute.Name = canonical
		} else {
			report(schema.unknown, attribute)
			if !schema.unknownFallsThrough {
				kept = append(kept, attribute)
				continue
			}
		}
		if seen[key] {
			report(schema.duplicate, attribute)
		}
		seen[key] = true
		switch {
		case schema.presenceOnly != "" && key == schema.presenceOnly:
			if !attribute.Bare {
				report(schema.presenceValue, attribute)
			}
		case attribute.Bare && schema.bare.reason != "":
			report(schema.bare, attribute)
			invalid = invalid || schema.bareInvalid
		}
		// A value-less boolean is left to the bare rule wherever the schema has one.
		if schema.booleans[key] && !(attribute.Bare && schema.bare.reason != "") {
			if value := strings.ToLower(attribute.Value); !attribute.Bare && (value == "true" || value == "false") {
				attribute.Value = value
			} else {
				report(schema.boolean, attribute)
			}
		}
		if schema.extract != "" && key == schema.extract && !attribute.Bare && extracted.Name == "" {
			extracted = attribute
			continue
		}
		kept = append(kept, attribute)
	}
	return extracted, kept, diagnostics, invalid
}

// jiraImageAttributeSchema reads the parameters of a Jira `!image!`, whose
// alternative text is the `alt` parameter. A parameter JFM cannot spell keeps
// the complete image literal: an image rendered without it is a different image.
var jiraImageAttributeSchema = directiveAttributeSchema{
	known:               imageAttributeNames("alt"),
	presenceOnly:        "thumbnail",
	extract:             "alt",
	invalidName:         directiveAttributeRule{ConstructImage, "Jira image parameter name cannot be represented by JFM attribute grammar; complete image remains literal"},
	dropInvalidName:     true,
	unknown:             directiveAttributeRule{ConstructDirective, "unknown image attribute is preserved"},
	unknownFallsThrough: true,
	duplicate:           directiveAttributeRule{ConstructDirective, "duplicate image attribute is preserved"},
	presenceValue:       directiveAttributeRule{ConstructDirective, "thumbnail value is preserved although thumbnail is presence-only in JFM"},
	bare:                directiveAttributeRule{ConstructImage, "Jira image parameter without a value cannot be represented by JFM; complete image remains literal"},
	bareInvalid:         true,
}

// imageDirectiveAttributeSchema reads the attributes of a JFM `:image[]{}`,
// whose destination is the `src` attribute.
var imageDirectiveAttributeSchema = directiveAttributeSchema{
	known:         imageAttributeNames("src"),
	presenceOnly:  "thumbnail",
	extract:       "src",
	unknown:       directiveAttributeRule{ConstructDirective, "unknown image directive attribute is preserved"},
	duplicate:     directiveAttributeRule{ConstructDirective, "duplicate image directive attribute is preserved"},
	presenceValue: directiveAttributeRule{ConstructDirective, "thumbnail is a presence-only image attribute"},
	bare:          directiveAttributeRule{ConstructDirective, "image directive attribute requires a value"},
}

// linkDirectiveAttributeSchema names the attributes of a JFM `:link[]{}`. The
// directive has no location for anything else, so its reader answers an unknown
// or repeated attribute by leaving the complete directive literal rather than by
// preserving it; only the canonical spellings come from here.
var linkDirectiveAttributeSchema = directiveAttributeSchema{known: []string{"target", "title"}}

// linkDirectiveValueRequired is the reason one `:link` attribute reports when it
// is written without a value.
var linkDirectiveValueRequired = map[string]string{
	"target": "link directive target requires a value",
	"title":  "link directive title requires a value",
}

// jiraMacroAttributeSchema reads the parameters of a `{code}` or `{panel}`
// header, where a parameter JFM cannot spell keeps the complete macro literal.
func jiraMacroAttributeSchema(known []string, booleans map[string]bool, macro string) directiveAttributeSchema {
	return directiveAttributeSchema{
		known:               known,
		booleans:            booleans,
		invalidName:         directiveAttributeRule{ConstructJiraMacro, "Jira parameter name cannot be represented by JFM attribute grammar; complete " + macro + " remains literal"},
		unknown:             directiveAttributeRule{ConstructDirective, "unknown " + macro + " attribute is preserved"},
		unknownFallsThrough: true,
		duplicate:           directiveAttributeRule{ConstructDirective, "duplicate " + macro + " attribute is preserved"},
		bare:                directiveAttributeRule{ConstructJiraMacro, "Jira parameter without a value cannot be represented by this JFM attribute; complete " + macro + " remains literal"},
		bareInvalid:         true,
		boolean:             directiveAttributeRule{ConstructDirective, "invalid boolean " + macro + " attribute value is preserved"},
	}
}

// containerDirectiveAttributeSchema reads the attributes of a JFM container
// directive, which stays a directive whatever its attributes say.
func containerDirectiveAttributeSchema(known []string, booleans map[string]bool, directive string) directiveAttributeSchema {
	return directiveAttributeSchema{
		known:     known,
		booleans:  booleans,
		unknown:   directiveAttributeRule{ConstructDirective, "unknown " + directive + " directive attribute is preserved"},
		duplicate: directiveAttributeRule{ConstructDirective, "duplicate " + directive + " directive attribute is preserved"},
		boolean:   directiveAttributeRule{ConstructDirective, "invalid boolean directive attribute value is preserved"},
	}
}
