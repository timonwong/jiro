package markup

import "strings"

// jiraLineIndentLength reports the ASCII spaces and tabs Jira skips at a line
// start before it reads any of the rules below. Jira indents no block by them,
// so `\t* item` is the same list as `* item`.
func jiraLineIndentLength(line string) int {
	length := 0
	for length < len(line) && (line[length] == ' ' || line[length] == '\t') {
		length++
	}
	return length
}

// jiraLineControlPrefix reports the heading level Jira reads at a line start,
// whether the line start is a `bq.` quote instead, and the offset one past the
// `.`. It reports 0, false, 0 when the line start is neither. The end reaches
// one past the `.` so that the escaper can spell that `.` as `&#46;` instead of
// escaping it: `.` is not in Jira's escapable set, and a backslash before it
// would stay visible.
func jiraLineControlPrefix(line string) (level int, quote bool, end int) {
	indent := jiraLineIndentLength(line)
	rest := line[indent:]
	if len(rest) < 3 || rest[2] != '.' || len(rest) > 3 && rest[3] != ' ' {
		return 0, false, 0
	}
	if rest[0] == 'h' && rest[1] >= '1' && rest[1] <= '6' {
		return int(rest[1] - '0'), false, indent + 3
	}
	if rest[0] == 'b' && rest[1] == 'q' {
		return 0, true, indent + 3
	}
	return 0, false, 0
}

// jiraLineControlLikePrefix reports the line starts that spell a heading or a
// quote the rule above rejects. malformedHeading is `h`, digits and a `.` at a
// level Jira has none of, which jiro keeps literal with a warning rather than
// guessing. blockStart also covers a `bq.` that no space follows: Jira opens a
// block there, so the line cannot be folded into the paragraph above it even
// though jiro converts neither shape.
func jiraLineControlLikePrefix(line string) (malformedHeading, blockStart bool) {
	if strings.HasPrefix(line, "bq.") {
		return false, true
	}
	if len(line) == 0 || line[0] != 'h' {
		return false, false
	}
	end := 1
	for end < len(line) && line[end] >= '0' && line[end] <= '9' {
		end++
	}
	if end == 1 || end == len(line) || line[end] != '.' {
		return false, false
	}
	if end+1 != len(line) && line[end+1] != ' ' {
		return false, false
	}
	return true, true
}

// jiraLineThematicBreak reports whether Jira draws a horizontal rule for the
// line. Jira reads the four dashes past the indent and ignores the spaces and
// tabs that trail them.
func jiraLineThematicBreak(line string) bool {
	rest := line[jiraLineIndentLength(line):]
	if !strings.HasPrefix(rest, "----") {
		return false
	}
	return strings.TrimRight(rest[len("----"):], " \t") == ""
}

// jiraLineMarkerRun reports the marker run Jira reads at a line start and the
// offset where the item content begins, which is past the run's separating
// space or tab and past every space and tab that follows it, because Jira keeps
// none of them in the item. After the indent Jira takes a run of `*`, `#` and
// `-` in any mix as the marker when a space or a tab follows it; a run with
// anything else after it is text.
//
// dashRun reports that the run is nothing but `-` and longer than one. Jira
// reads such a run as a list marker only while a list is already open, and as
// its en or em dash otherwise, so the caller has to supply that context.
func jiraLineMarkerRun(line string) (run string, contentStart int, dashRun bool) {
	start := jiraLineIndentLength(line)
	end, dashes := start, 0
	for end < len(line) && (line[end] == '*' || line[end] == '#' || line[end] == '-') {
		if line[end] == '-' {
			dashes++
		}
		end++
	}
	if end == start || end == len(line) || line[end] != ' ' && line[end] != '\t' {
		return "", 0, false
	}
	content := end
	for content < len(line) && (line[content] == ' ' || line[content] == '\t') {
		content++
	}
	return line[start:end], content, dashes == end-start && dashes > 1
}

// jiraListMarkerPrefix reports the byte range of the marker run that makes Jira
// read line as a list item outside any open list, and 0, 0 when it reads no
// list there. A run of two or more `-` is Jira's en or em dash outside a list
// and a semantic jiro keeps (see jiraPlainTextByteClasses), so it is not a
// marker here.
func jiraListMarkerPrefix(line string) (int, int) {
	run, _, dashRun := jiraLineMarkerRun(line)
	if run == "" || dashRun {
		return 0, 0
	}
	start := jiraLineIndentLength(line)
	return start, start + len(run)
}

// jiraLineStartBlockName names the block Jira renders for a line start, in the
// wording a warning uses, and reports "" when the line start begins no block.
func jiraLineStartBlockName(line string) string {
	if _, end := jiraListMarkerPrefix(line); end != 0 {
		return "a list"
	}
	if _, quote, end := jiraLineControlPrefix(line); end != 0 {
		if quote {
			return "a block quote"
		}
		return "a heading"
	}
	if jiraLineThematicBreak(line) {
		return "a horizontal rule"
	}
	return ""
}
