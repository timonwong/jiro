#!/usr/bin/env python3
"""Probe a real Jira Server wiki renderer (read-only).

Target: ASF Jira, Jira Server 8.20.10, anonymous access to /rest/api/1.0/render.
Equivalent single-case curl:

  curl -sS -X POST 'https://issues.apache.org/jira/rest/api/1.0/render' \
    -H 'Content-Type: application/json' -H 'X-Atlassian-Token: no-check' \
    -d '{"rendererType":"atlassian-wiki-renderer","unrenderedMarkup":"{{*bold*}}"}'

Usage: python3 hack/jira-render-evidence.py [round1|...|round11|all] [--json]   (default: all)

--json prints one {"in": ..., "out": ...} object per line so captures can be
turned into evidence fixtures without reparsing the human-readable output.
"""
import json, sys, time, urllib.request

URL = "https://issues.apache.org/jira/rest/api/1.0/render"

ROUND1 = [
    # --- the originally requested 26 cases ---
    "{{*bold*}}", "{{a -b- c}}", "{{foo-bar}}", "{{foo_bar_baz}}", "{{_it_}}",
    "{{a}}b}}", "{{[x]}}", "{{[x] foo}}", "{{[x|http://example.com]}}",
    "{{http://example.com/a_b*c-d}}", "{{https://example.com/*bold*}}",
    "{{&amp;}}", "{{&#42;x&#42;}}", "{{x\\-y}}", "{{\\\\}}", "{{!img!}}",
    "{{??cite??}}", "{{a|b}}", "{{:)}}", "{{{code}}}", "{{café_中_x}}",
    "{{x * y * z}}", "foo_bar_baz", "a -b- c and foo-bar", "café_中_x", "{{*_x_*}}",
    # --- word-boundary follow-ups ---
    "{{a-b-c}}", "{{a_b_c}}", "{{a _b_ c}}", "{{foo _bar_ baz}}",
    "{{2024-01-01 - 2024-02-02}}", "{{a --b-- c}}", "{{-x-}}", "{{x*y*z}}",
    "{{x\\*y\\*z}}", "{{a\\_b\\_c}}", "{{*}}", "{{}}", "{{a}}",
    "{{a}}b", "{{a}} b", "x{{a}}y", "a{{b}}c",
    "x-y-z", "a _b_ c", "x*y*z", "\\_foo\\_", "a-b -c- d",
    "{{cmd --flag=value}}", "{{my_var - other_var}}", "{{--}}", "{{__}}",
    "{{a\nb}}", "{{ -b- }}",
    # --- brace-boundary follow-ups ---
    "({{a}})", "{{a}},", "{{a}}.", "see {{a}}!", "[{{a}}]", "{{a}}}", "{{}a}}",
    "{{a }}", "{{ a}}", "*{{a}}*", "{{a-}}", "{{-a}}", "{{a}b}}", "{{x}}{{y}}",
    "{{a\\}}b}}", "{{&#123;&#123;}}", "{{|}}", "{{h1. x}}", "{{* item}}", "{{{{a}}}}",
]

ZWSP = "​"

_EMOTICONS = ["(y)", "(n)", "(i)", "(/)", "(x)", "(!)", "(?)", "(+)", "(-)",
              "(on)", "(off)", "(*)", "(*r)", "(flag)", ":(", ":P", ":D", ";)"]

ROUND2 = (
    [
        # --- boundary / adjacency (word-char rule) ---
        "a" + ZWSP + "{{b}}" + ZWSP + "c",
        '"{{a}}"', "({{a}})", "{{a}}'s", "{{a}}-b", "{{a}}_b", "{{a}}*b",
        "中{{a}}文", "-{{a}}-", "_{{a}}_", "{{a}}:", "{{a}}" + ZWSP + "b",
        "x" + ZWSP + "{{a}}",
        # --- opener/closer gating for * and _ ---
        "(_x_)", '"*x*"', "a*b c*d", "*x*y", "x*y* z", "y *x*",
        "中*强*文", "中 *强* 文", "a_b_ c", "a _b_c",
        "{{(*x*)}}", "{{x,*y*}}", "{{*x*,y}}", "{{a_ b_}}",
        "{{--flag}}", "{{-}}", "{{- x -}}", "{{a -- b}}", "{{-x- -y-}}",
        "{{+ins+}}", "{{^sup^}}", "{{~sub~}}", "{{a^b^c}}", "{{a~b~c}}",
        "{{2^10}}", "{{x~y}}",
    ]
    # --- emoticons / parens: plain and inside {{}} ---
    + _EMOTICONS
    + ["{{%s}}" % e for e in _EMOTICONS]
    + [
        "(y)foo", "{{f(x)}}", "{{(a)}}", "{{a(y)b}}",
        # --- : ? ! | # narrowing ---
        "{{a:b}}", "{{x?y}}", "{{?x?}}", "{{??x}}", "{{a!b}}", "{{!x!y}}",
        "{{a#b}}", "{{#1}}", "{{a|b|c}}",
        "||h||\n|{{a|b}}|", "||h||\n|{{a&#124;b}}|", "||h||\n|{{a\\|b}}|",
        # --- links / URLs inside ---
        "{{[http://example.com]}}", "{{[a|b]}}", "{{[a|#anchor]}}",
        "{{mailto:a@b.c}}", "{{a@b.c}}", "{{ftp://x.y}}", "{{www.example.com}}",
        "{{http&#58;//example.com}}", "{{[x&#124;y]}}", "{{\\[x]}}", "{{&#91;x&#93;}}",
        # --- entities / HTML / whitespace ---
        "{{&lt;b&gt;}}", "{{<b>x</b>}}", "{{&nbsp;}}", "{{&#160;}}", "{{&bogus;}}",
        "{{a  b}}", "{{a\tb}}", "{{&#10;}}", "{{&#123;code&#125;}}",
        "{{&#92;&#92;}}", "{{a\\\\b}}", "{{a\\bb}}",
        # --- nesting with outer effects / block context ---
        "*{{a b}}*", "*{{*a*}}*", "{{*a*}}*b*", "[{{a}}|http://x]",
        "||{{h}}||\n|x|", "h1. {{t}}", "* {{li}}", "bq. {{q}}",
        "{quote}{{q}}{quote}", "{color:red}{{c}}{color}", "{panel}{{p}}{panel}",
    ]
)


ROUND3 = [
    # --- char-ref whitespace inside monospace ---
    "{{&#32;a}}", "{{a&#32;}}", "{{&#32;}}", "{{a&#32;b}}", "{{a&#9;b}}",
    "{{&#9;}}", "{{&#160;a}}", "{{&#8203;}}", "{{a&#8203;b}}",
    " {{a}}", "{{a}} ",
    # --- dashes ---
    "a -- b", "a --- b", "a--b", "{{a---b}}", "{{a --- b}}", "{{&#45;&#45;}}",
    "{{a &#45;- b}}", "{{a -&#45; b}}", "{{a &#45;&#45; b}}", "{{ -- }}",
    "{{x -- }}", "{{-- x}}", "{{--}}", "{{a--b--c}}",
    # --- emoticon neutralizers ---
    "{{&#40;y)}}", "{{(y&#41;}}", "{{f&#40;x)}}", "{{\\(y)}}", "{{(\\y)}}",
    "f(x)", "print(x)", "(x)", "\\(x)", "(x)y", "a (x) b",
    "{{:)x}}", "{{x:)}}", "{{:&#41;}}", "{{&#58;)}}", "{{;)}}", "{{a;)b}}",
    # --- other ---
    "{{a}}&#8203;b", "a&#8203;{{b}}", "{{\\\\}}x", "{{a\\\\}}", "{{\\\\a}}",
    "{{&#92;}}", "{{a\\}}", "{{\\{x\\}}}", "{{&#123;x&#125;}}",
    "{{{x}}}", "{{x{}}", "{{{}x}}",
]

NBSP = " "
IDEOGRAPHIC_SPACE = "　"

ROUND4 = [
    # --- word runes on either side of an effect delimiter ---
    "a--b--c", "foo__bar__", "1*2*3", "é*x*é", "*x*é", "*x*1",
    "*x*_", "*x*-y", "~x~-", "x_*y*", "_*x*_", "a *b_c_ d*",
    # --- what counts as space next to an effect delimiter ---
    "*" + NBSP + "x*", "*" + IDEOGRAPHIC_SPACE + "x" + IDEOGRAPHIC_SPACE + "*",
    "a *\tx*", "*x\t*",
    # --- canonical Jira output jiro emits for the literal forms above ---
    "a-\\-b\\--c", "foo_\\_bar\\__", "a \\_b\\_ c", "(\\_x\\_)", '"\\*x\\*"',
]


# Probe strings backing internal/markup/testdata/jfm/jira_evidence fixtures that
# ROUND1-4 do not already cover; every rendered.html in that directory must be
# reproducible from this script.
ROUND5 = [
    # --- autolink scheme coverage and where a URL ends ---
    "{{https://example.com/a_b*c-d}}", "{{http://example.com a}}",
    "{{http://example.com.}}", "{{(http://example.com)}}",
    "{{http://example.com,}}", "{{[http://example.com] x}}",
    # --- emoticon tokens and gating not in _EMOTICONS ---
    "{{(*g)}}", "{{(*b)}}", "{{(*y)}}", "{{(flagoff)}}", "{{(y)foo}}", "{{x(y)}}",
    # --- dash substitution needs a space on both sides ---
    "{{a--b}}", "{{a --b}}", "{{a-- b}}", "{{a --}}",
    # --- U+200B inside the body and in prose ---
    "{{a" + ZWSP + "b}}", "a" + ZWSP + "b",
    "{{" + ZWSP + "a}}", "{{a" + ZWSP + "}}", "a" + ZWSP + "{{b" + ZWSP + "c}}",
    # --- character reference token shapes ---
    "{{&#x20;a}}", "{{&nbsp;a}}",
    # --- an unknown macro name may carry spaces; citations gate like effects ---
    "{{a {b c} d}}", "{{a??x??}}", "{{??x??b}}",
    # --- which characters end an autolinked URL, and which are only trailing ---
    '{{http://example.com"a}}', "{{http://example.com<a}}",
    "{{http://example.com>a}}", "{{http://example.com|a}}",
    "{{http://example.com`a}}", "{{http://example.com{a}}",
    "{{http://example.com}a}}", "{{http://example.com[a}}",
    "{{http://example.com]a}}", "{{http://example.com(a}}",
    "{{http://example.com)a}}", "{{http://example.com;}}",
    "{{http://example.com:}}", "{{http://example.com!}}",
    "{{http://example.com?}}",
    # --- which side of the Monospace Span boundary a word rune refuses ---
    "x{{a}}", "{{a}}y",
    # --- plain-text context anchors ---
    "a@b.c", "http://example.com", "??cite??", "[x]",
]


# Probe strings backing the plain-text escaping rules (#87): which characters a
# backslash escapes, the brace form of an Effect Delimiter, how a table row
# splits around links and images, and line-control protection.
ROUND6 = [
    # --- which characters a backslash escapes in plain text ---
    r"plain \?\?", r"a\?", r"a \?b", r"a\!b", r"a\#b", r"a\.b", r"h1\. x",
    r"\h1. x", r"a\ab", r"a\,b", r"a\=b", r"a\:b", r"a\(b\)", 'a\\"b', r"a\'b",
    r"a\;b", r"a\/b", r"a\&b", r"a\<b", r"a\%b", r"a\@b", r"a\$b", r"a\^b",
    r"a\~b", r"a\+b", r"a\ b", r"a\-b", r"a\*b", r"a\_b", r"a\{b\}c",
    r"a\[b\]c", r"\# item", r"\* item", r"\- item", r"\|a|b|",
    # --- line-control protection by character reference ---
    "h1&#46; x", "bq&#46; x", "&#104;1. x", "h1.x", "x\nh1. y", "h1. x",
    "&#42; item", "&#45; item", "&#35; item", "&#124;a|b|",
    "a&#63;b", "what&#63;&#63;", "a&#46;b",
    # --- citation pairing in plain text ---
    "what??", "?? hello", "a ??cite?? b", "a??cite??b", "??cite??",
    "what?? and why??", r"a \?\?cite\?\? b", r"a\?\?cite\?\?b",
    # --- word runes on either side of a bare Effect Delimiter ---
    "a*b*", "a*b*c", "x*y* z", "a" + ZWSP + "*b*c", "a·*b* c",
    "a— *b* c", "é*b* c", "a *b*·c", "a *b*—c",
    "a *b*€c", "a *b*中", "*b*中", "中*b*",
    "a。*b* c", "a *b*。c", "a、*b*c", "a *b*、c",
    "a «*b*» c",
    # --- the brace form of an Effect Delimiter ---
    "a{*}b{*}c", "a{_}b{_}c", "a{-}b{-}c", "a{+}b{+}c", "a{^}b{^}c",
    "a{~}b{~}c", "a{??}b{??}c", "{*}b{*}", "{*}b{*} c", "x {*}b{*}",
    "中{*}强{*}文", "{*}a*", "a{*}b*c", "a*b{*}c",
    "a{*}b{*}{*}c{*}", "a{*} b {*}c", "{*} b{*}", "a{*}*b*{*}c",
    "a{**}b{**}c", "{*}", "a{*}", r"a{*}b{*}c\*", "[a{*}b{*}c|http://x]",
    "{{a{*}b{*}c}}", "h1. a{*}b{*}c", "* a{*}b{*}c",
    "||h1||h2||\n|a{*}b{*}c|c|", "{color:red}x{color}",
    "_i{_}t", "*a{_}b{_}c*", "-a{-}b",
    # --- a table row splits around links and images ---
    "||h1||h2||\n|[x|http://x]|c|",
    "||h1||h2||\n|[x|y]|c|",
    "||h1||h2||\n|[a|b|c]|d|",
    "||h1||h2||\n|[x|http://x|title]|c|",
    "||h1||h2||\n|[x|http://x]c|d|",
    "||h1||h2||\n|a [x|http://x] b|c|",
    "||h1||h2||\n|[~user]|c|",
    "||h1||h2||\n|[#anchor]|c|",
    "||h1||h2||\n|[^file.txt]|c|",
    "||h1||h2||\n|[x]|c|",
    "||h1||h2||\n|[a|c|",
    "||h1||h2||\n" + r"|\[x|y]|c|",
    "||h1||h2||\n|!http://x/i.png|alt=alt!|c|",
    "||h1||h2||\n|!http://x/i.png|alt=alt, width=10!|c|",
    "||h1||h2||\n|!http://x/i.png|alt=a|b!|c|",
    "||h1||h2||\n|!http://x/i.png!|c|",
    "||h1||h2||\n|!a!|b|",
    "||h1||h2||\n|!a|b|c|",
    "||h1||h2||\n|!a.png|b!x|c|",
    "||h1||h2||\n|! a|b!|c|",
    "||h1||h2||\n|a!b|c!d|",
    "||h1||h2||\n|{{a|b}}|c|",
    "||h1||h2||\n" + r"|a\|b|c|",
    "||h1||h2||\n" + r"|!http://x/i.png\|alt=alt!|c|",
    # --- inline runs in a link label ---
    "[a -b- c|http://x]", "[foo_bar_baz|http://x]", "[y *x*|http://x]",
    r"[a\-b\-c|http://x]", "[a{*}b{*}c|http://x]", "[x|http://x] *y*",
    r"[a \?\? b|http://x]", "[h1. x|http://x]", r"[a\[b|http://x]",
    "[a|b|http://x]",
    # --- the same plain text in the other block contexts ---
    "h1. a -b- c", "h1. foo_bar_baz", "* a -b- c", "* foo_bar_baz",
    "a -b- c", r"a\-b\-c", r"y \*x\*", r"a \-b\- c", r"a-b \-c\- d",
    "+ins+", "^sup^", "~sub~", "2^10", "a+b+c", "x~y~z", "a--b",
    r"\+ins\+", r"\^sup\^", r"\~sub\~", "(_x_)", '"*x*"',
    # --- an effect's content may not begin with its own delimiter ---
    "**x**", "a**b**c", "*x**", "**x*", "*a*b*", "a *b** c", "*a**b*",
    "a{*}{*}b", "{*}{_}x{_}{*}", "a{*}b{-}c{-}d{*}e", "{{a{-}b{-}c}}",
    # --- the brace form of the citation delimiter ---
    "a{??}b??c", "a??b{??}c", "{??}b{??}",
    # --- mixed runs the renderer has to spell two ways at once ---
    "a{*}b{*}c and x*y*z", "{*}a{*} {*}b{*}",
    # --- an authored backslash: which ones Jira reads as a forced newline ---
    r"a\\b", r"a \\ b", r"\\a", r"a\\", r"C:\\dir\\file", r"a\\\\b",
    r"C:\dir\file", "x\\", r"x\ y", r"C:\{x}", r"C:&#92;\{x\}",
    "C:&#92;dir&#92;file", "a &#92; b",
    # --- which escapes a Monospace Span body consumes ---
    r"{{a\?b}}", r"{{a\#b}}", r"{{a\(b\)}}", r"{{a\%b}}", r"{{a\@b}}",
    r"{{a\!b}}", r"{{a\.b}}", r"{{a\,b}}", r"{{a\\b}}",
]

# Probe strings backing the line-start list marker rule (#93): which marker runs
# Jira reads as a list, where a line start is, and what keeps one off it.
ROUND7 = [
    # --- which marker runs at a line start are a list ---
    "* item", "- item", "# item", "** item", "*# item", "#* item",
    "-* item", "*- item", "-- item", "--- item", "-# item", "#- item",
    "*item", "-item", "*", "-", "#", "**", "* ", "*\titem",
    "*\nfoo", "*\\\\\nfoo", "* \\\\\nfoo",
    # --- what keeps a marker run off the line start ---
    r"\* item", r"\- item", r"\** item", r"\*# item", r"\#* item",
    r"\-- item", r"\-* item", r"\--- item", "&#42;* item", "&#45;- item",
    r"\*\* item",
    # --- where a line start is ---
    "x\\\\\n* item", "x\\\\\n" + r"\* item", "x\\\\\n- item", "x\\\\\n** item",
    "{{* item}}", "{{** item}}", "h1. * item", "* * item", r"* \* item",
    "* foo\\\\\n* bar", "* foo\\\\\n" + r"\* bar",
    " * item", "*  item", "x\n * item",
    # --- Jira skips leading spaces and tabs before either line-start rule ---
    "\t* item", " \\* item", " h1. x", "  h1. x", "\th1. x", " bq. x",
    " h1&#46; x",
    # --- every table cell is its own line start ---
    "||h||\n|* item|", "||h||\n" + r"|\* item|",
    "||h||\n|h1. x|", "||h||\n|bq. x|", "||h||\n|h1&#46; x|",
    "||h||\n|- item|", "||h||\n| * item|", "||h||\n|x|* item|",
    "||h||\n|a\\\\\n* item|", "||h||\n|a\\\\\n" + r"\* item|",
]

# Probe strings backing the block parser's reading of list lines (#99): which
# level a marker run nests at, when a run of dashes is a marker rather than a
# dash, and which line starts Jira reads inside a list item or a table cell.
ROUND8 = [
    # --- the run's last character decides the type of the level it names ---
    "*- a", "-# a", "#- a",
    "* a\n- b", "- a\n* b", "- a\n- b", "# a\n- b", "- a\n# b", "* a\n- b\n* c",
    "- a\n-* b", "-* a\n-* b", "* a\n*- b", "-* a\n** b", "- a\n** b",
    "* a\n-* b", "* a\n-# b", "* a\n** b\n* c",
    # --- a run of only dashes is a marker just while a list is open ---
    "-- a", "--- a", "-- a\n-- b", "text\n-- b", "h1. x\n-- y", "bq. x\n-- y",
    "* a\n\n-- b", "* a\n-- b", "# a\n-- b", "- a\n-- b", "* a\n-- b\n-- c",
    "- a\n-- b\n--- c", "* a\n-- b\n* c", "* a\n** b\n-- c", "* a\nfoo\n-- b",
    "* a\n--\n* b", "* a\n-- \n* b", "* a\n--- b", "- a\n--- b", "* a\n*-- b",
    "* a\n*** b",
    # --- a plain line below a marker stays inside the item ---
    "* a\nfoo",
    # --- a run that no space or tab follows is text ---
    "##", "*\n", "* \n", "** ",
    # --- the indent Jira skips before a marker run or a line control ---
    "-\titem", "#\titem", "  - item", "* a\n  ** b", "* a\n\t** b",
    "\t\th1. x", "  bq. x", " h2. x", " ---- ", " ----", "\t----",
    "x\\\\\nh1. y", "x\\\\\n h1. y",
    # --- a blank line ends a list ---
    "- a\n\n- b", "* a\n\n* b",
    # --- every table cell is a line start Jira renders a block at ---
    "||h||\n|* a|", "||h||\n|- a|", "||h||\n|# a|", "||h||\n|h2. x|y|",
    "||h||\n|* a\\\\\n* b|", "||h||\n|h1. x\\\\\ny|", "||h||\n|- a\\\\\n-- b|",
    # --- a list item's own content is a line start for h1. and bq. only ---
    "* * y", "* h1. y", "- h1. y", "* bq. y",
]


# Probe strings backing the per-context delimited value rules (#96): what a
# link target, a link's visible text, an image source, an image parameter and
# a macro parameter each do with a backslash and with a character reference.
ROUND9 = [
    # --- which characters a link target decodes ---
    r"[x|http://x/a\?b=1]",
    r"[x|http://x/a\#f]",
    r"[x|http://x/a\]b]",
    r"[x|http://x/a\|b]",
    r"[x|http://x/a\,b]",
    r"[x|http://x/a\=b]",
    r"[x|http://x/a\%b]",
    r"[x|http://x/a\(b\)]",
    r"[x|http://x/a\\b]",
    r"[x|http://x/a\\\\b]",
    r"[x|http://x/a\ab]",
    r"[x|http://x/a\ b]",
    r"[x|http://x/a\]",
    r"[x|http://x/a\[b]",
    r"[x|http://x/a\*b]",
    r"[x|http://x/a\_b]",
    r"[x|http://x/a\-b]",
    r"[x|http://x/a\{b]",
    r"[x|http://x/a\~b]",
    r"[x|http://x/a\^b]",
    r"[x|http://x/a\+b]",
    r"[x|http://x/a\!b]",
    r"[x|http://x/a\&b]",
    r"[x|http://x/a\中b]",
    r"[http://x/a\]b]",
    r"[http://x/a\|b]",
    r"[http://x/a\?b]",
    r"[http://x/a\\b]",
    r"[x|http://x|t\|u]",
    r"[x|http://x|t\]u]",
    r"[x|http://x|t\=u]",
    "[x|http://x|t]",
    r"[x|http://x/a\?b\?c]",
    r"[x|http://x/a\\b\\c]",
    r"[x|http://x/a\\\b]",
    r"[x|http://x/a\?\?b]",
    r"[x|http://x/a\|b|c]",
    "[x|http://x|t|u]",
    "[x|http://x|t|u|v]",
    "[x|http://x/a&#124;b]",
    "[x|http://x/a&#38;#124;b]",
    "[x|http://x/a&#93;b]",
    r"[x|http://x/a\&#93;b]",
    r"[x|mailto:a\@b.c]",
    r"[x|#anchor\-x]",
    r"[x|JIRA-1\?focusedId=2]",
    r"[x|http://x/a\\]",
    r"[x|http://x/a\\\]",
    "[x|http://x/a&#92;b]",
    "[x|http://x/a&#32;b]",
    "[x|http://x/a&#124;b|t]",
    r"[x|http://x/a\\b]",
    r"[x|http://x/a\\\\\\b]",
    # --- what a link's visible text reads ---
    r"[a\|b|http://x]",
    r"[a\]b|http://x]",
    r"[a\[b|http://x]",
    r"[a\*b|http://x]",
    r"[a\\b|http://x]",
    r"[a\-b|http://x]",
    r"[a\_b|http://x]",
    "[a&#124;b|http://x]",
    "[a&#93;b|http://x]",
    r"[a\]|http://x]",
    r"[a\\|http://x]",
    "[a&#92;b|http://x]",
    # --- what an image source and its parameters keep ---
    r"!http://x/i\|b.png!",
    r"!http://x/i\!b.png!",
    r"!http://x/i\ b.png!",
    r"!http://x/i\\b.png!",
    r"!http://x/i\,b.png!",
    r"!http://x/i\]b.png!",
    r"!http://x/i.png|alt=a\=b!",
    r"!http://x/i.png|alt=a\,b!",
    r"!http://x/i.png|alt=a\?b!",
    r"!http://x/i.png|alt=a\#b!",
    r"!http://x/i.png|alt=a\!b!",
    r"!http://x/i.png|alt=a\|b!",
    r"!http://x/i.png|alt=a\\b!",
    r"!http://x/i.png|alt=a\*b!",
    r"!http://x/i.png|alt=a\-b!",
    r"!http://x/i.png|alt=a\]b!",
    "!http://x/i.png|alt=a&#44;b!",
    "!http://x/i.png|alt=a&#38;#44;b!",
    "!http://x/i.png|alt=a&#61;b!",
    "!http://x/i.png|alt=a&#124;b!",
    "!http://x/i.png|alt=a&#33;b!",
    r"!http://x/i.png|title=a\=b!",
    r"!http://x/i.png|alt=a\=b, title=c\,d!",
    "!http://x/i.png|alt=a|title=b!",
    r"!http://x/i.png|alt=a\!",
    r"!http://x/i.png|alt=a\\\\b!",
    "!http://x/i.png|alt=a=b!",
    "!http://x/i.png|alt=a=b,c=d!",
    "!http://x/i.png|alt=a, b!",
    "!http://x/i.png|alt= a !",
    "!http://x/i.png| alt=a!",
    "!http://x/i&#124;b.png!",
    "!http://x/i&#33;b.png!",
    r"!http://x/i\\!",
    r"!http://x/i\\b.png|alt=a!",
    "!http://x/i.png|alt=a!b!",
    r"!http://x/i.png|alt=a\\!",
    r"!http://x/i.png|alt=a\\\,b!",
    r"!http://x/i.png|alt=a\=b\=c!",
    "!http://x/i&#92;b.png!",
    "!http://x/i.png|alt=&#32;a!",
    "!http://x/i.png|alt=a&#92;b!",
    "!http://x/i.png|alt=a&#10;b!",
    "!http://x/i.png|alt=a&#124;b!\n||h||\n|!http://x/i.png|alt=a&#124;b!|c|",
    "!http://x/i.png|alt=a=b=c!",
    "!http://x/i.png|alt=a,,b!",
    "!http://x/i.png|,alt=a!",
    "!http://x/i.png|alt=!",
    # --- what a macro parameter decodes ---
    r"{code:title=a\=b}x{code}",
    r"{code:title=a\|b}x{code}",
    r"{code:title=a\,b}x{code}",
    r"{code:title=a\}b}x{code}",
    r"{panel:title=a\=b}x{panel}",
    r"{panel:title=a\|b}x{panel}",
    r"{panel:title=a\\b}x{panel}",
    "{code:title=a&#61;b}x{code}",
    r"{noformat:title=a\=b}x{noformat}",
    r"{quote:title=a\=b}x{quote}",
    r"{color:\#ff0000}x{color}",
    "{color:#ff0000}x{color}",
    r"{code:title=a\\\\b}x{code}",
    r"{code:title=a\=b\=c}x{code}",
    "{code:title=a&#124;b}x{code}",
    r"{code:title=a\:b}x{code}",
    "{panel:title=a&#124;b|borderStyle=solid}x{panel}",
    r"{anchor:a\=b}",
    r"{color:red\}}x{color}",
    "{code:title=a&#92;b}x{code}",
    "{code:title=a&#44;b}x{code}",
    "{code:title=a&#125;b}x{code}",
    r"{code:title=a\\b}x{code}",
    "{code:title=a\\=b}\ncode\n{code}",
    "{code:title=a\\,b}\ncode\n{code}",
    "{code:title=a\\}b}\ncode\n{code}",
    "{code:title=a\\\\b}\ncode\n{code}",
    "{code:title=a&#61;b}\ncode\n{code}",
    "{code:title=a&#124;b}\ncode\n{code}",
    "{code:title=a\\:b}\ncode\n{code}",
    "{panel:title=a\\|b}\ntext\n{panel}",
    "{panel:title=a\\=b|borderStyle=solid}\ntext\n{panel}",
    # --- the same rules inside a table row ---
    "||h||\n|!http://x/i.png|alt=a|b!|c|",
    "||h||\n|!http://x/i.png|alt=a&#124;b!|c|",
    "||h1||h2||\n|!http://x/i.png|alt=a&#124;b!|c|",
    "||h1||h2||\n|[x|http://x/a&#124;b]|c|",
]


# Probe strings backing the Monospace Span closer rules (#106): how many
# backslashes in front of a `}}` hide it, what a single one takes with it, and
# where the scan resumes once a closer is hidden.
ROUND10 = [
    # --- how long a backslash run in front of the closer has to be to hide it ---
    r"{{a\\\}}",
    r"{{a\\\\}}",
    r"{{a\\\\\}}",
    r"{{a\\\\\\}}",
    r"{{\}}",
    r"{{\\\}}",
    r"{{\\\\}}",
    r"{{\\\\\}}",
    # --- runs that do not reach the end of the body ---
    r"{{\a}}",
    r"{{\\\a}}",
    r"{{\\\\a}}",
    r"{{a\\\b}}",
    r"{{a\\\\b}}",
    # --- the body a consumed backslash leaves for the edge-space rules ---
    r"{{a \}}",
    r"{{a \\}}",
    r"{{a\\ }}",
    r"{{ a\}}",
    r"{{\ }}",
    r"{{ \}}",
    # --- a backslash written as a reference is not part of the run ---
    r"{{a&#92;}}",
    r"{{a&#92;&#92;}}",
    r"{{a\&#92;}}",
    r"{{a&#92;\}}",
    r"{{a&#92;\\}}",
    r"{{a\&#92;\}}",
    # --- where the scan resumes once a closer is hidden ---
    r"{{a\\}}b}}",
    r"{{a\\\}}b}}",
    r"{{a\\\\}}b}}",
    r"{{a\\}} b}}",
    r"{{a\}}b",
    r"{{a\}} b",
    r"x{{a\\}}y",
    r"{{a\\}} {{b}}",
    r"{{a\}}}",
    r"{{a\\}}}",
    r"{{a\\\}}}",
    r"{{a\\}}}}",
    r"{{a\\}}b\}}",
    r"{{a\\}}b\\}}",
    r"{{a\\}}b\\}}c}}",
    # --- U+200B at the body edge a consumed backslash leaves ---
    "{{a​\\}}",
    "{{​\\}}",
    "{{a\\​}}",
    # --- the same escapes outside a span ---
    r"a\}b",
    r"a\}}b",
    r"\}",
]

# Probe strings backing the mid-line forced newline placement and the one-rune
# effect kill (#94): backslash runs, token separators, line domains, lookbehind
# deadness, and the killed openers.
ROUND11 = [
    # --- where a `\\` pair is a forced newline: the last run of a token, and
    #     only when it is exactly two backslashes long ---
    r"a\\b", r"a \\ b", r"\\a", r"a\\", r"\\", r"C:\\dir", r"C:\\dir\\file",
    r"ab\\cd\\ef", r"a\\b\\c\\d", r"x\\y z\\w", r"x\\y\\", r"\\a\\", r"\\ a",
    r"a \\", r"a\\ \\b", r"(\\)", r"a.\\b", r"a\\-b", r"a\\*b",
    # --- runs of other lengths are characters Jira shows, and escape nothing ---
    r"a\\\b", r"\\\a", r"a\\\\b", r"\\\\a", r"a\\\\", r"a\\\\\b",
    r"a\\\\\\b", r"a\\\\b\\c", r"a\\b\\\\c", r"a\\b \\\\c", r"\\\\ \\",
    r"a\\\*b", "&#92;&#92;",
    # --- what ends the token the run belongs to ---
    r"a\\b.c\\d", r"a\\b-c\\d", r"a\\b*c\\d", r"a\\b,c\\d d\\e",
    r"a\\b\\ c", r"a\\b\\c \\d", "a\\\\b\tc\\\\d", "a\\\\b\u00a0c\\\\d",
    "a\\\\b\fc\\\\d", "a\\\\b\vc\\\\d", "a\\\\b\nc\\\\d", "\u4e2d\\\\\u6587",
    # --- the domain the rule is read in: a line, a heading, a list item, a cell ---
    r"h1. a\\b", "||h||\n|a\\\\b|c\\\\d|", r"* a\\b\\c d\\e",
    # --- the pair is decided on the raw line, straight through an effect ---
    r"*x\\y*", r"*a\\*", r"*a\\b*c*", r"*ab\\cd*ef*", r"*x\\y*-z\\w",
    r"*x\\y* -z\\w", r"a\\b *c*d*", r"x\\ *b*",
    # --- a delimiter a backslash precedes never acts as markup ---
    r"a\\*b*", r"a\\\\*b*", r"*ab\\\\* c", r"*ab\\\\*", "&#92;*b*",
    r"a\\{{b}}", r"a\\[x|http://example.com]",
    r"C:\dir\file", "x\\", r"x\ y", r"a\ab",
    # --- a link's visible text is read without the rule; a color macro is not ---
    r"[a\\b|http://x]", r"[a\\b c\\d|http://x]", r"[a\\\\b|http://x]",
    r"{color:red}a\\b{color}",
    # --- a Monospace Span body is read in the line it stands in ---
    r"{{a\\\\b}}", r"{{ab\\cd\\ef}}", r"{{a\\b c\\d}}", r"{{a\\\b}}",
    r"{{a\\b}}-c\\d", r"{{a\\b}}c\\d", r"x{{a\\b}}y", r"x {{a\\b}} y",
    r"{{a\\ b}}", r"{{a\\b}}y", r"a{{b\\c}}", r"{{a}} \\", r"a\\b{{c}}",
    # --- parked: what trailing backslashes do to span formation (not modelled) ---
    r"{{a\}}", r"{{\}}", r"{{a\\}}", r"{{a\\\\}}", r"{{\\a}}", r"{{ \\ }}",
    r"{{\\}}",
    # --- the one-rune kill: an opener whose only closer candidate sits one rune
    #     in and is refused by a word rune opens nothing at all ---
    "*a*b*", "*a*bc*", "_a_b_", "-a-b-", "+a+b+", "~a~b~", "^a^b^", "x^2^3^",
    "*a*b*c*", "*1*2*", "*\u4e2d*\u6587*", "*a*\u00e9*", "*\u20ac*b*",
    "*a*1*", "*a*b", "*a*,b*", "*a*-b*",
    # --- two runes of content, or a candidate nothing refuses, scan on ---
    "*ab*cd*", "*ab*c*d*", "*aa*b*", "-ab-c-", "_ab_c_", "*abc*d*",
    "*a\u20ac*b*", "*\u20ac*\u20ac*", "*a**b", "*ab*c", "*a *b*", "*a *b* c*",
    "*a**b*", "*a* b*", "*ab* c*", "*a* *b*",
    # --- what a kill settles, and what it does not ---
    "x *a*b* y", "*a*b* *c*", "*ab*c* *d*", "*a**b*c*", "_a_b_ *c*",
    "*x _a_b_ y*", "h1. *a*b*", "{{*a*b*}}",
    # --- the brace form closes before a word rune, so it kills nothing ---
    "{*}a*b*", "{*}a*b{*}", "*a*b{*}", "*a{*}b*", "*ab{*}c*", "*{*}a*",
    # --- an escaped candidate is skipped without killing the opener ---
    r"*a\*b*", r"*a\*b*c*", r"*a\*b* c",
    # --- the citation obeys the same rule ---
    "??a??b??", "??ab??c??", "??a??b", "??a??", "??\u20ac??b??",
    # --- only a backslash Jira keeps blocks the break; a consumed escape does not ---
    r"a\\b\c", r"a\\b\-c", "a\\\\b\\",
    r"a\b\\c", r"a\\b\\\c",
    # --- and the citation obeys the same backslash lookbehind ---
    r"a\\??x??", r"??ab\??c??", r"??ab\?? c??", r"??ab\\\\??", r"??ab\\??",
    r"??\??b??",
    # --- rows the unit tables read, so that every one of them is a render ---
    r"a\b", r"a\\b|c\\d", "*a_b*", r"*\*b*", r"*ab\*", r"*ab\\*", "*ab*",
    r"h1. a\\b c\\d",
]

def render(markup, timeout=30):
    body = json.dumps({"rendererType": "atlassian-wiki-renderer",
                       "unrenderedMarkup": markup}).encode()
    req = urllib.request.Request(URL, data=body, method="POST", headers={
        "Content-Type": "application/json",
        "X-Atlassian-Token": "no-check",
    })
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return r.read().decode("utf-8", "replace").strip()


def run(cases, delay=0.3, as_json=False):
    seen = set()
    for m in cases:
        if m in seen:
            continue
        seen.add(m)
        try:
            out = render(m)
        except Exception as e:
            out = "ERR " + repr(e)
        if as_json:
            print(json.dumps({"in": m, "out": out}, ensure_ascii=False))
        else:
            print("IN : " + repr(m))
            print("OUT: " + out)
            print("-" * 66)
        sys.stdout.flush()
        time.sleep(delay)


if __name__ == "__main__":
    arguments = sys.argv[1:]
    as_json = "--json" in arguments
    arguments = [a for a in arguments if a != "--json"]
    which = arguments[0] if arguments else "all"
    run({"round1": ROUND1, "round2": ROUND2, "round3": ROUND3, "round4": ROUND4,
          "round5": ROUND5, "round6": ROUND6, "round7": ROUND7, "round8": ROUND8,
          "round9": ROUND9, "round10": ROUND10, "round11": ROUND11,
          "all": ROUND1 + ROUND2 + ROUND3 + ROUND4 + ROUND5 + ROUND6 + ROUND7
                 + ROUND8 + ROUND9 + ROUND10 + ROUND11}[which], as_json=as_json)
