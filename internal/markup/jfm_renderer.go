package markup

import (
	"context"
	"fmt"
	"html"
	"strconv"
	"strings"
	"unicode/utf8"
)

func renderJFM(ctx context.Context, document semanticDocument) (string, error) {
	blocks := make([]string, 0, len(document.Blocks))
	for _, block := range document.Blocks {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		switch typed := block.(type) {
		case headingBlock:
			content, err := renderJFMInlines(ctx, typed.Inlines, jfmBlockOpeners)
			if err != nil {
				return "", err
			}
			heading := strings.Repeat("#", typed.Level)
			if content != "" {
				heading += " " + content
			}
			blocks = append(blocks, heading)
		case paragraphBlock:
			content, err := renderJFMInlines(ctx, typed.Inlines, jfmBlockOpeners)
			if err != nil {
				return "", err
			}
			blocks = append(blocks, content)
		case thematicBreakBlock:
			blocks = append(blocks, "---")
		case quoteBlock:
			content, err := renderJFM(ctx, semanticDocument{Blocks: typed.Blocks})
			if err != nil {
				return "", err
			}
			blocks = append(blocks, prefixQuotedLines(content))
		case listBlock:
			content, err := renderJFMList(ctx, typed, 0)
			if err != nil {
				return "", err
			}
			blocks = append(blocks, content)
		case tableBlock:
			content, err := renderJFMTable(ctx, typed)
			if err != nil {
				return "", err
			}
			blocks = append(blocks, content)
		case codeBlock:
			content, err := renderJFMCodeBlock(ctx, typed)
			if err != nil {
				return "", err
			}
			blocks = append(blocks, content)
		case panelBlock:
			body, err := renderJFM(ctx, semanticDocument{Blocks: typed.Blocks})
			if err != nil {
				return "", err
			}
			fence, err := safeContainerFence(ctx, body)
			if err != nil {
				return "", err
			}
			header := fence + "panel"
			if len(typed.Attributes) != 0 {
				serialized, err := serializeDirectiveAttributes(ctx, typed.Attributes, panelAttributeOrder())
				if err != nil {
					return "", err
				}
				header += "{" + serialized + "}"
			}
			blocks = append(blocks, header+"\n"+body+ensureLiteralClosingSeparation(body)+fence)
		case unsupportedMacroBlock:
			body, err := renderJFM(ctx, semanticDocument{Blocks: typed.Blocks})
			if err != nil {
				return "", err
			}
			blocks = append(blocks, typed.Opening+"\n"+body+ensureLiteralClosingSeparation(body)+typed.Closing)
		case literalBlock:
			content, err := escapeTextForJFM(ctx, typed.Text, jfmBlockOpeners)
			if err != nil {
				return "", err
			}
			blocks = append(blocks, content)
		default:
			return "", fmt.Errorf("%w: unsupported semantic block in JFM renderer", ErrConversion)
		}
	}
	return strings.TrimSpace(strings.Join(blocks, "\n\n")), nil
}

// jfmBlockOpeners are the characters that open a block where a JFM line begins,
// which is why a text fragment written there escapes them. jfmItemContentOpeners
// are the ones a list item's content start has to escape: Jira renders a
// heading, a quote and a horizontal rule there and JFM reads all three back, so
// `#` and `-` carry blocks that have to survive as text. `-` is also the bullet
// JFM reads at that start and Jira does not, so escaping it keeps a Jira item
// whose text opens with one from opening a nested list on the way back.
const (
	jfmBlockOpeners       = "#>+-"
	jfmItemContentOpeners = "#-"
)

func renderJFMInlines(ctx context.Context, inlines []semanticInline, lineStart string) (string, error) {
	var result strings.Builder
	for _, inline := range inlines {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		switch typed := inline.(type) {
		case textInline:
			content, err := escapeTextForJFM(ctx, typed.Text, lineStart)
			if err != nil {
				return "", err
			}
			result.WriteString(content)
		case codeInline:
			content, err := renderJFMCodeSpan(ctx, typed.Text)
			if err != nil {
				return "", err
			}
			result.WriteString(content)
		case hardBreakInline:
			result.WriteString("\\\n")
		case styledInline:
			content, err := renderJFMInlines(ctx, typed.Children, "")
			if err != nil {
				return "", err
			}
			if combined, ok := combinedBoldItalic(typed); ok {
				content, err = renderJFMInlines(ctx, combined, "")
				if err != nil {
					return "", err
				}
				result.WriteString("***" + content + "***")
				break
			}
			switch typed.Style {
			case styleBold:
				result.WriteString("**" + content + "**")
			case styleItalic:
				result.WriteString("*" + content + "*")
			case styleStrike:
				result.WriteString("~~" + content + "~~")
			case styleInserted:
				result.WriteString("<ins>" + content + "</ins>")
			case styleSuper:
				result.WriteString("<sup>" + content + "</sup>")
			case styleSub:
				result.WriteString("<sub>" + content + "</sub>")
			case styleColor:
				result.WriteString(`<font color="` + html.EscapeString(typed.Value) + `">` + content + `</font>`)
			}
		case linkInline:
			label, err := renderJFMInlines(ctx, typed.Label, "")
			if err != nil {
				return "", err
			}
			if typed.Directive || typed.Dangerous {
				content, err := escapeDirectiveContent(ctx, label)
				if err != nil {
					return "", err
				}
				attributes := []directiveAttribute{{Name: "target", Value: typed.Target}}
				if typed.Title != "" {
					attributes = append(attributes, directiveAttribute{Name: "title", Value: typed.Title})
				}
				serialized, err := serializeDirectiveAttributes(ctx, attributes, nil)
				if err != nil {
					return "", err
				}
				result.WriteString(":link[" + content + "]{" + serialized + "}")
				break
			}
			title := ""
			if typed.Title != "" {
				escaped, err := escapeMarkdownLinkTitle(ctx, typed.Title)
				if err != nil {
					return "", err
				}
				title = ` "` + escaped + `"`
			}
			lowerTarget := strings.ToLower(typed.Target)
			// An autolink has no room for a title, so a titled link takes the
			// inline form even where its target is its own visible text.
			if typed.Unnamed && title == "" && (strings.HasPrefix(lowerTarget, "http://") || strings.HasPrefix(lowerTarget, "https://")) {
				result.WriteString("<" + typed.Target + ">")
			} else if typed.Unnamed && title == "" && strings.HasPrefix(lowerTarget, "mailto:") {
				result.WriteString("<" + strings.TrimPrefix(typed.Target, typed.Target[:len("mailto:")]) + ">")
			} else {
				target, err := escapeMarkdownDestination(ctx, typed.Target)
				if err != nil {
					return "", err
				}
				result.WriteString("[" + label + "](" + target + title + ")")
			}
		case imageInline:
			if !typed.Directive && !typed.Dangerous {
				alt, err := escapeMarkdownLabelText(ctx, typed.Alt)
				if err != nil {
					return "", err
				}
				source, err := escapeMarkdownDestination(ctx, typed.Source)
				if err != nil {
					return "", err
				}
				result.WriteString("![" + alt + "](" + source + ")")
				break
			}
			attributes := []directiveAttribute{{Name: "src", Value: typed.Source}}
			for _, attribute := range typed.Attributes {
				attributes = append(attributes, attribute)
			}
			alt, err := escapeDirectiveContent(ctx, typed.Alt)
			if err != nil {
				return "", err
			}
			serialized, err := serializeDirectiveAttributes(ctx, attributes, imageAttributeOrder())
			if err != nil {
				return "", err
			}
			result.WriteString(":image[" + alt + "]{" + serialized + "}")
		case emoticonInline:
			// A supported token holds neither `]` nor a backslash, so the
			// content needs no escaping.
			result.WriteString(":emoticon[" + typed.Token + "]")
		case literalInline:
			content, err := escapeTextForJFM(ctx, typed.Text, lineStart)
			if err != nil {
				return "", err
			}
			result.WriteString(content)
		default:
			return "", fmt.Errorf("%w: unsupported semantic inline in JFM renderer", ErrConversion)
		}
		// A newline the run itself writes opens a full line start: what the
		// enclosing block narrows is only where the run begins.
		lineStart = ""
		if inlineEndsAtLineStart(inline) {
			lineStart = jfmBlockOpeners
		}
	}
	return result.String(), nil
}

func renderJFMCodeSpan(ctx context.Context, value string) (string, error) {
	run, err := longestRunWithContext(ctx, value, '`')
	if err != nil {
		return "", err
	}
	delimiter := strings.Repeat("`", run+1)
	if delimiter == "" {
		delimiter = "`"
	}
	padding := ""
	if value != "" && (value[0] == ' ' || value[len(value)-1] == ' ' || value[0] == '`' || value[len(value)-1] == '`') && strings.Trim(value, " ") != "" {
		padding = " "
	}
	return delimiter + padding + value + padding + delimiter, nil
}

// escapeTextForJFM writes one plain-text fragment. lineStart names the block
// openers to escape where the fragment begins, and is "" for a fragment that
// begins inside a line; every newline inside the fragment opens a full line
// start of its own.
func escapeTextForJFM(ctx context.Context, value string, lineStart string) (string, error) {
	var result strings.Builder
	for index := 0; index < len(value); {
		if index&255 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		character, size := utf8.DecodeRuneInString(value[index:])
		if character == '\\' || strings.ContainsRune("*_~[]`<>", character) {
			result.WriteByte('\\')
		}
		if strings.ContainsRune(lineStart, character) {
			result.WriteByte('\\')
		}
		result.WriteRune(character)
		lineStart = ""
		if character == '\n' {
			lineStart = jfmBlockOpeners
		}
		index += size
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return result.String(), nil
}

// escapeMarkdownDestination writes a link target or an image source. Both are
// values jiro reads with character references resolved, so a `&` that begins one
// has to leave as `&amp;`: a Markdown reader resolves the reference exactly as
// Jira does, and without this the value would come back as the character it
// names instead of the reference the author wrote.
func escapeMarkdownDestination(ctx context.Context, value string) (string, error) {
	var result strings.Builder
	for offset, character := range value {
		if offset&255 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		if strings.ContainsRune(`\()<> `, character) {
			result.WriteByte('\\')
		}
		result.WriteRune(character)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return escapeMarkdownCharacterReferences(result.String()), nil
}

// escapeMarkdownLabelText writes an image's alternative text, which is a Jira
// image parameter value and so is read with character references resolved; a `&`
// that begins one leaves as `&amp;` for the same reason as in a destination.
func escapeMarkdownLabelText(ctx context.Context, value string) (string, error) {
	escaped, err := escapeSelectedRunes(ctx, value, `\[]`)
	if err != nil {
		return "", err
	}
	return escapeMarkdownCharacterReferences(escaped), nil
}

// escapeMarkdownLinkTitle writes a link title inside the double quotes that
// follow a destination. A Markdown reader resolves both a backslash escape and a
// character reference there, while the Jira title jiro carries is verbatim, so
// each of them has to leave as the escape that reads back as itself.
func escapeMarkdownLinkTitle(ctx context.Context, value string) (string, error) {
	escaped, err := escapeSelectedRunes(ctx, value, `\"`)
	if err != nil {
		return "", err
	}
	return escapeMarkdownCharacterReferences(escaped), nil
}

// escapeMarkdownCharacterReferences writes every `&` that would begin a
// character reference as `&amp;`. It runs after the backslash escaping above,
// which never touches a `&`.
func escapeMarkdownCharacterReferences(value string) string {
	if !strings.Contains(value, "&") {
		return value
	}
	var result strings.Builder
	result.Grow(len(value))
	for offset := 0; offset < len(value); offset++ {
		if value[offset] == '&' && startsCharacterReference(value, offset, len(value)) {
			result.WriteString("&amp;")
			continue
		}
		result.WriteByte(value[offset])
	}
	return result.String()
}

func escapeDirectiveContent(ctx context.Context, value string) (string, error) {
	return escapeSelectedRunes(ctx, value, `\]`)
}

func serializeDirectiveAttributes(ctx context.Context, attributes []directiveAttribute, order []string) (string, error) {
	ordered := orderDirectiveAttributes(attributes, order)
	parts := make([]string, 0, len(ordered))
	for index, attribute := range ordered {
		if index&255 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		if attribute.Bare {
			parts = append(parts, attribute.Name)
		} else {
			value, err := quoteDirectiveAttributeValue(ctx, attribute.Value)
			if err != nil {
				return "", err
			}
			parts = append(parts, attribute.Name+"="+value)
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return strings.Join(parts, " "), nil
}

// quoteDirectiveAttributeValue writes one attribute value between double quotes.
// A `}` is escaped beside the characters Go quotes, because the attribute list
// ends at the first `}` no backslash stands in front of: without it a value
// holding one -- a link title, a link target, a panel or code title -- would end
// the directive early and leave the whole source literal on the way back.
func quoteDirectiveAttributeValue(ctx context.Context, value string) (string, error) {
	var result strings.Builder
	result.WriteByte('"')
	for offset, character := range value {
		if offset&255 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		if character == '}' {
			result.WriteString(`\}`)
			continue
		}
		quoted := strconv.Quote(string(character))
		result.WriteString(quoted[1 : len(quoted)-1])
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	result.WriteByte('"')
	return result.String(), nil
}

func prefixQuotedLines(value string) string {
	lines := strings.Split(value, "\n")
	for index, line := range lines {
		if line == "" {
			lines[index] = ">"
		} else {
			lines[index] = "> " + line
		}
	}
	return strings.Join(lines, "\n")
}

func renderJFMList(ctx context.Context, list listBlock, depth int) (string, error) {
	segments := make([]string, 0, len(list.Items))
	lines := make([]string, 0, len(list.Items))
	flushLines := func() {
		if len(lines) == 0 {
			return
		}
		segments = append(segments, strings.Join(lines, "\n"))
		lines = nil
	}
	activeDepth := depth
	for _, item := range list.Items {
		indent := strings.Repeat(" ", activeDepth*4)
		marker := "-"
		if list.Ordered {
			marker = "1."
		}
		line, taken, err := renderJFMListItemLine(ctx, item, indent+marker)
		if err != nil {
			return "", err
		}
		lines = append(lines, line)
		interrupted, nested := false, false
		for _, block := range item.Blocks[taken:] {
			child, isList := block.(listBlock)
			if isList && !interrupted && !child.RequiresFlattening {
				childText, err := renderJFMList(ctx, child, activeDepth+1)
				if err != nil {
					return "", err
				}
				if nested {
					// Two lists under one item are two lists to Jira as well, and JFM
					// spells at most one bullet shape, so the blank line is all that
					// holds them apart — the same separation two adjacent top-level
					// lists get.
					lines = append(lines, "")
				}
				lines = append(lines, childText)
				nested = true
				continue
			}
			flushLines()
			interrupted = true
			activeDepth = 0
			if isList {
				childText, err := renderJFMList(ctx, child, 0)
				if err != nil {
					return "", err
				}
				segments = append(segments, childText)
				continue
			}
			content, err := renderJFM(ctx, semanticDocument{Blocks: []semanticBlock{block}})
			if err != nil {
				return "", err
			}
			segments = append(segments, content)
		}
	}
	flushLines()
	return strings.Join(segments, "\n\n"), nil
}

// renderJFMListItemLine writes one item's own line and reports how many of the
// item's blocks that line took. A leading line control and a leading horizontal
// rule are written on the item line itself: Jira reads a heading, a quote and
// its dash rule back from an item's content start (listItemLineControl,
// listItemLineThematicBreak) and so does JFM, so neither side has to flatten the
// block out of the list.
func renderJFMListItemLine(ctx context.Context, item listItem, marker string) (string, int, error) {
	if level, quote, inlines, ok := listItemLineControl(item); ok {
		content, err := renderJFMInlines(ctx, inlines, jfmBlockOpeners)
		if err != nil {
			return "", 0, err
		}
		control := strings.Repeat("#", level)
		if quote {
			control = ">"
		}
		if content != "" {
			control += " " + content
		}
		return marker + " " + control, 1, nil
	}
	if listItemLineThematicBreak(item) {
		// `- ---` is no item: a line of nothing but dashes and spaces is a thematic
		// break whatever the first dash was meant as, so the rule inside an item
		// takes the one spelling the bullet cannot be read into.
		return marker + " ***", 1, nil
	}
	content, err := renderJFMInlines(ctx, item.Inlines, jfmItemContentOpeners)
	if err != nil {
		return "", 0, err
	}
	if content == "" {
		return marker, 0, nil
	}
	return marker + " " + content, 0, nil
}

func renderJFMTable(ctx context.Context, table tableBlock) (string, error) {
	if table.Directive || len(table.Header) == 0 {
		fence, err := safeContainerFence(ctx, table.Raw)
		if err != nil {
			return "", err
		}
		return fence + "table\n" + table.Raw + ensureLiteralClosingSeparation(table.Raw) + fence, nil
	}
	rows := make([]string, 0, len(table.Rows)+2)
	header, err := renderJFMTableRow(ctx, table.Header)
	if err != nil {
		return "", err
	}
	rows = append(rows, header)
	separator := make([]string, len(table.Header))
	for index := range separator {
		separator[index] = "---"
	}
	rows = append(rows, "| "+strings.Join(separator, " | ")+" |")
	for _, row := range table.Rows {
		value, err := renderJFMTableRow(ctx, row)
		if err != nil {
			return "", err
		}
		rows = append(rows, value)
	}
	return strings.Join(rows, "\n"), nil
}

func renderJFMTableRow(ctx context.Context, cells []tableCell) (string, error) {
	values := make([]string, len(cells))
	for index, cell := range cells {
		value, err := renderJFMInlines(ctx, cell.Inlines, "")
		if err != nil {
			return "", err
		}
		values[index] = strings.ReplaceAll(value, "|", "\\|")
	}
	return "| " + strings.Join(values, " | ") + " |", nil
}

func renderJFMCodeBlock(ctx context.Context, block codeBlock) (string, error) {
	if block.Directive {
		attributes := block.Attributes
		if block.Language != "" && !containsDirectiveAttribute(attributes, "language") {
			attributes = append([]directiveAttribute{{Name: "language", Value: block.Language}}, attributes...)
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		serialized, err := serializeCodeDirectiveAttributes(ctx, attributes)
		if err != nil {
			return "", err
		}
		fence, err := safeContainerFence(ctx, block.Body)
		if err != nil {
			return "", err
		}
		header := fence + "code"
		if serialized != "" {
			header += "{" + serialized + "}"
		}
		return header + "\n" + block.Body + ensureLiteralClosingSeparation(block.Body) + fence, nil
	}
	run, err := longestRunWithContext(ctx, block.Body, '`')
	if err != nil {
		return "", err
	}
	fence := strings.Repeat("`", max(3, run+1))
	opening := fence
	if block.Language != "" {
		opening += block.Language
	}
	return opening + "\n" + block.Body + ensureLiteralClosingSeparation(block.Body) + fence, nil
}

func serializeCodeDirectiveAttributes(ctx context.Context, attributes []directiveAttribute) (string, error) {
	ordered := orderDirectiveAttributes(attributes, codeAttributeOrder())
	parts := make([]string, 0, len(ordered))
	for _, attribute := range ordered {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if strings.EqualFold(attribute.Name, "collapse") || strings.EqualFold(attribute.Name, "linenumbers") {
			if value := strings.ToLower(attribute.Value); value == "true" || value == "false" {
				parts = append(parts, attribute.Name+"="+value)
				continue
			}
		}
		value, err := quoteDirectiveAttributeValue(ctx, attribute.Value)
		if err != nil {
			return "", err
		}
		parts = append(parts, attribute.Name+"="+value)
	}
	return strings.Join(parts, " "), ctx.Err()
}

func safeContainerFence(ctx context.Context, body string) (string, error) {
	longest := 2
	for lineStart := 0; lineStart <= len(body); {
		if lineStart&255 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}

		lineEnd := lineStart
		for lineEnd < len(body) && body[lineEnd] != '\n' && body[lineEnd] != '\r' {
			if lineEnd&255 == 0 {
				if err := ctx.Err(); err != nil {
					return "", err
				}
			}
			lineEnd++
		}
		fenceStart := jfmContainerClosingFenceStart(body[lineStart:lineEnd])
		if fenceStart < 0 {
			fenceStart = lineEnd - lineStart
		}
		fenceStart += lineStart
		fenceEnd := fenceStart
		for fenceEnd < lineEnd && body[fenceEnd] == ':' {
			if fenceEnd&255 == 0 {
				if err := ctx.Err(); err != nil {
					return "", err
				}
			}
			fenceEnd++
		}
		if run := fenceEnd - fenceStart; run > longest {
			longest = run
		}

		if lineEnd == len(body) {
			break
		}
		lineStart = lineEnd + 1
		if body[lineEnd] == '\r' && lineStart < len(body) && body[lineStart] == '\n' {
			lineStart++
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return strings.Repeat(":", longest+1), nil
}

func longestRunWithContext(ctx context.Context, value string, target byte) (int, error) {
	longest, current := 0, 0
	for index := 0; index < len(value); index++ {
		if index&255 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		if value[index] == target {
			current++
			if current > longest {
				longest = current
			}
		} else {
			current = 0
		}
	}
	return longest, ctx.Err()
}
