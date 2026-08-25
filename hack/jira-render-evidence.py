#!/usr/bin/env python3
"""Probe and verify a real Jira Server wiki renderer (read-only, anonymous).

Target: ASF Jira, Jira Server 8.20.10, POST /rest/api/1.0/render. The script is
both an ad-hoc probing tool and the verification harness for the evidence
fixtures in internal/markup/testdata/jfm/jira_evidence, and it carries the
frozen archive of the historical probe campaigns that built them.

Subcommands:

  probe    render markup given on the command line or on stdin
             python3 hack/jira-render-evidence.py probe '{{*bold*}}' --json
  round    replay one archived probe campaign, or all of them in order
             python3 hack/jira-render-evidence.py round round16 --json
  verify   replay each evidence fixture's own input.jira and diff rendered.html
             python3 hack/jira-render-evidence.py verify 'autolink_*'

--json prints one {"in": ..., "out": ...} object per line so captures can be
turned into evidence fixtures without reparsing the human-readable output.

Equivalent single-case curl:

  curl -sS -X POST 'https://issues.apache.org/jira/rest/api/1.0/render' \
    -H 'Content-Type: application/json' -H 'X-Atlassian-Token: no-check' \
    -d '{"rendererType":"atlassian-wiki-renderer","unrenderedMarkup":"{{*bold*}}"}'

ROUND1..ROUND16 are a frozen historical archive: evidence fixtures and Go test
comments cite them by round number, so never rename, renumber, reorder, or edit
their contents. New probes go through `probe`, and the reproducibility of
jira_evidence is checked by `verify` against each fixture's own input.jira
rather than by keeping a superset of probe strings here.
"""
import argparse, fnmatch, json, re, sys, time, urllib.request
from pathlib import Path

URL = "https://issues.apache.org/jira/rest/api/1.0/render"
EVIDENCE_DIR = "internal/markup/testdata/jfm/jira_evidence"

# --- frozen probe archive: cited by round number from fixtures and tests; append-only, never edit ---

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

ZWSP = "\u200b"

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

NBSP = "\u00a0"
IDEOGRAPHIC_SPACE = "\u3000"

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
# ROUND1-4 did not already cover, from when this file doubled as the directory's
# reproducibility manifest; the verify subcommand now checks that directly.
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
    "{{a\u200b\\}}",
    "{{\u200b\\}}",
    "{{a\\\u200b}}",
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

# Probe strings backing the explicit JFM emoticon directive (#83): the token
# gate in every visible context, the escapes and character references that keep
# an emoticon-shaped literal visible, and the one backslash Jira's emoticon
# escape consumes in front of a token.
ROUND12 = [
    # --- the token gate: a preceding word rune leaves the icon, a following one
    #     kills it, and U+200B is not a following word rune ---
    "(x)", "(y)", "(n)", "(i)", "(/)", "(!)", "(?)", "(+)", "(-)",
    "(on)", "(off)", "(*)", "(*r)", "(*g)", "(*b)", "(*y)", "(flag)", "(flagoff)",
    ":)", ":(", ":P", ":D", ";)",
    "x:)", "x;)", ":)y", ";)y", ":)x", "a;)b", "x(y)", "(flag)off", "(*y)x",
    # --- case and shape variants: `(a)` names no icon, every parenthesized
    #     token is lower case only, and the hyphenated aliases are covered in
    #     round15 after the dedicated #120 probe pass ---
    "(a)", "(X)", "(Y)", "(N)", "(I)", "(ON)", "(OFF)", "(*R)", "(*Y)",
    "(FLAG)", "(Flag)", ":p", ":d", ";p", ";P", ":-)", "=)",
    "(x)" + ZWSP + "foo", "(x) ", " (x)", "((x))", "(:))", "(x)(y)",
    # --- escaping either parenthesis is enough to stop the token ---
    r"\(x\)", r"\(x)", r"(x\)", r"\(x\)y", r"a\(x\)b", r"\(x)y", r"\(y)",
    r"\(*y\)", r"\(flagoff\)", r"\(on)", r"\(flagoff)", r"\(*y)",
    r"\(\!\)", r"(\!)", r"\(-\)", r"\(*\)", r"\(?\)", r"\(+\)", r"\(/\)",
    r"print\(x\)",
    # --- a character reference is opaque to the token scan ---
    "&#58;)", "&#58;(", "&#58;P", "&#58;D", "&#59;)", "&#58;)y", "&#59;)y",
    "x&#58;)", "x&#59;)", "&#58;", "&#59;", "&#58;x", "&#58;P y", "&#58;)&#58;)",
    "&#40;x&#41;",
    # --- the emoticon escape consumes exactly one backslash, and only where the
    #     gate fires; the rest of the run then follows the ordinary rules ---
    r"\:)", r"\;)", r"\:P", r"\:D", r"\:(", r"x \:) y", r"x \;) y", r"x \:P y",
    r"a\:)b", r"a\;)b", r"a\:Pb",
    r"\\:)", r"\\;)", r"\\:P", r"\\\:)", r"\\\:P", r"\\\\:)",
    r"x \\:) y", r"a\\:)", r"\\:)y",
    r"\\(x)", r"\\\(x)", r"a\\(x)", r"a\\\(x)", r"\\(x)y", r"\\(*y)",
    r"\\\\(x)", r"\\ (x)", r"a \\ (x)",
    r"a\:b", r"a\;b", "\\:", "\\;", r"a\Pb", r"a\Db", r"a\(b", r"a\)b",
    # --- the same escape inside the constructs that read an inline run ---
    r"h1. \\(x)", r"* \\:)", r"[\:)|http://example.com]", r"{{\:)}}",
    r"{color:red}\:){color}", "||h||\n|\\\\:)|(x)|",
    r"[a|http://example.com]\:)",
    # --- the visible contexts a token renders in, and the delimited values it
    #     does not ---
    "*(x)*", "(x)*foo*", "*foo*(x)", "h1. (x)", "* (x)", "bq. (x)",
    "||h||\n|(x)|", "[(x)|http://example.com]", "[a|http://example.com/(x)]",
    # --- a delimiter inside a token pairs nothing, because Jira substitutes the
    #     icon before it reads any Text Effect ---
    "+(+)+", "{+}(+){+}", "*(*)*", "-(-)-", "^(x)^", "~(x)~",
    # --- and the JFM spelling itself is nothing to Jira ---
    ":emoticon[(x)]",
]

# Probe strings backing the line controls a list item's content start reads
# (#101) and the forms that need no space after the `.` (#121): the controls
# at every item content start, the levels Jira has none of, and the line a
# malformed one keeps in the paragraph above it.
ROUND13 = [
    # --- `h1.` to `h6.` and `bq.` form at every list item's content start, at
    #     every nesting level and under every marker ---
    "* h1. y", "* h2. y", "* h6. y", "* h7. y", "* bq. y", "** h1. y",
    "# h1. y", "- h3. y", "#* bq. y", "*  h1. y", "* \th1. y",
    "* h1. *b*", "* bq. *b*",
    # --- a control item stays in the list, and the lines below it are blocks
    #     of their own inside the item ---
    "* h1. y\n* z", "* a\n* h1. y\n* z", "* h1. y\nz", "* bq. y\n* z",
    "* h1. y\n** z", "* h1. y\nh2. z", "* bq. y\nz", "* a\n  h1. y",
    "* a\\\\\nh1. y", "* a\\\\\nbq. y",
    # --- but a list marker does not re-form there, and a `&#46;` keeps the
    #     control off wherever it stands ---
    "* * y", "* # y", "* - y", "* h1&#46; y", "* bq&#46; y", "* h1&#46;y",
    # --- one control consumes its whole line and reads no second one ---
    "* h1. bq. y", "* bq. h1. y",
    # --- no space is needed after the `.`, at any line start Jira reads ---
    "h1.y", "bq.y", "h1.*b*", "h7.y", "h1. y", "* h1.y", "* bq.y",
    "||h||\n|h1.y|", "||h||\n|bq.y|",
    # --- and every space and tab before the content is skipped ---
    "* h1.  y", "* h1.\ty", "h2.  \ty", "bq.\tx",
    # --- the level is one digit of 1 to 6, with or without the space ---
    "h10. x", "h10.x", "* h10. y", "h123. x", "h0. x", "h0.x",
    # --- a control with nothing after it renders the block empty ---
    "h1.", "* h1.", "* bq.", "h1. y\\\\",
    # --- the dash run draws its rule inside an item too (#122, unimplemented) ---
    "* ---- ", "* ----", "* -----", "* ----x", "* ---- y",
    # --- a level Jira has none of opens no block: the paragraph and the item
    #     above it keep the line, while a level it has ends them ---
    "a\nh10.x", "a\nh10. x", "a\nh0.x", "a\nh7.y", "h10.x\nb",
    "a\nh1.x", "a\nh1. y", "a\nbq.y",
    # --- `bq.` is not the only quote an item reads ---
    "* {quote}y{quote}", "* a\n* {quote}b{quote}",
    # --- rows the unit tables read, so that every one of them is a render ---
    "* h1.  \ty", "  h1.x", "h1. ",
]


# Probe strings backing the row a Jira table reads across physical lines (#102):
# where a row ends, which lines open a row of their own, which ones an open row
# absorbs, and which `|` the cell split honours.
ROUND14 = [
    # --- a row runs on past the end of its line, whether or not the pair that
    #     ends the line renders a forced newline ---
    "||h||\n|a\\\\\nb|", "||h||\n|a\nb|", "||h||\n|a\\\\\nb\\\\\nc|",
    "||h||\n|a\\\\\nb\nc", "||h||\n|a \\\\\nb|", "||h||\n|a\\\\ \nb|",
    "||h||\n|a\\b\\\\\nc|", "||h||\n|\\\\\nb|", "||h||\n|a\\\nb|",
    "||h||\n|a\\\\\\\nb|", "||h||\n|a\\\\\\\\\nb|",
    # --- and its cells are still the ones its delimiters name ---
    "||h||\n|a\\\\\nb|c|", "||h||\n|a|\\\\\nb|", "||h||\n|a|x\\\\\nb|",
    "||h||\n|a|b\\\\\nc|d|", "||h||\n|a\nb|c|", "||h||\n|a|b\nc|",
    "||h||\n|a\nb|c\nd|",
    # --- a header row is one the delimiter it opens with names, and it runs on
    #     the same way ---
    "||h\\\\\ni||\n|a|", "||h||x\\\\\n|a|", "||h\nx||\n|a|", "||h||\n||x\ny||",
    # --- a line whose `|` stands at a line start, in front of it or behind the
    #     spaces and tabs Jira skips, opens a row of its own ---
    "||h||\n|a\\\\\n|b|", "||h||\n|a\\\\\n||b||", "||h||\n|a\\\\\n |b|",
    "||h||\n|a\\\\\n\t|b|", "||h||\n|a|\n |b|", "||h||\n|a|\n\t|b|",
    " |a|",
    # --- while an open row takes every other line into the cell it left open,
    #     and Jira reads a block at the start of each one ---
    "||h||\n|a\\\\\nbq. b|", "||h||\n|a\\\\\n# b|", "||h||\n|a\\\\\nh1. t|",
    "||h||\n|a\\\\\n----\nb|", "||h||\n|a\\\\\n---- x|",
    "||h||\n|a\\\\\n{quote}\nq\n{quote}\nb|",
    # --- the table ends at a blank or whitespace-only line, at the end of the
    #     document, and at the first line outside a row that is closed ---
    "||h||\n|a\\\\\n\nb|", "||h||\n|a\\\\\n \nb|", "|a\n\nb|",
    "||h||\n|a\\\\\nb|\nplain", "||h||\n|a\\\\\nb", "||h||\n|a", "|a\\\\\nb|",
    "x\\\\\n|a|",
    # --- a row closes on the `|` its last line ends with, whatever stands in
    #     front of that `|` and whatever spaces or tabs trail it ---
    "||h||\n|a| \nb|", "||h||\n|a|\t\nb|", "||h||\n|a\\|\nb|",
    "||h||\n|a\\\\|\nb|", "||h||\n|a\\\\ |\nb|", "||h||\n|a\\\\|",
    "||h|", "||h|\nx|", "||h||\n|\nb|",
    # --- and the `|` it closes on leaves no cell behind it ---
    "|a||", "|a||\nx|", "||h||i||\n|a| |", "||h||i||\n|a|&#32;|",
    # --- a `|` one backslash byte stands in front of separates no cells ---
    "||h||\n|a\\\\|b|c|", "||h||\n|a\\\\|b|\nc|", "||h||\n|a\\\\\nb\\| c|",
    "||h||i||\n|a|\\\\ \\\\|", "|x\\\\y|", "||h||\n|a&#92;|",
    # --- while `||` inside a data row opens a header cell (unimplemented) ---
    "||h||\n|a||b|",
]


# Probe strings backing the link title, the third part of a bracket body (#104):
# what the title decodes, what it trims, which spellings a title has no way to
# write, and which targets carry one. ROUND9 already holds `[x|http://x|t]`,
# `t|u`, `t|u|v`, `t\|u`, `t\]u`, `t\=u` and `[x|http://x/a\|b]`.
ROUND15 = [
    # --- emoticon aliases added from the 2026-08-24 renderer probes (#120) ---
    ":p", ":P", ":-)", ":)", ":-(", ":(", ";-)", ";)", ":-P", ":-D", ":-p",
    # --- the title decodes nothing: no backslash and no character reference ---
    r"[x|http://x|t\\u]", r"[x|http://x|t\u]",
    "[x|http://x|t&#124;u]", "[x|http://x|t&#93;u]", "[x|http://x|t&#92;u]",
    # --- and no markup runs inside it; Jira escapes it into the attribute ---
    '[x|http://x|a"b]', "[x|http://x|a'b]", "[x|http://x|<i>t</i>]",
    "[x|http://x|*t*]", "[x|http://x|:)]", "[x|http://x|café中]",
    # --- what the edges do: trimmed, and an empty part is no title ---
    "[x|http://x| t ]", "[x|http://x|\tt\t]", "[x|http://x|]",
    # --- a body ending in a backslash refuses the link, and one trailing space
    #     is what lets a title end in one ---
    "[x|http://x|t\\]", "[x|http://x|t\\\\]", "[x|http://x|t\\ ]",
    # --- a raw `]` ends the link wherever it stands ---
    "[x|http://x|t]u]",
    # --- which targets carry a title, and which resolve nothing with or
    #     without one ---
    "[x|#anchor|t]", "[x|mailto:a@b.c|t]", "[x|~admin|t]",
    "[http://x|http://x|t]", "[x|^file.pdf|t]", "[x|^file.pdf]",
    "[x|javascript:alert(1)|t]",
    # --- and no link at all forms across a physical line, whichever part the
    #     line break falls in ---
    "[y|http://y|a\nb]", "[y|http://y|a \nb]", "[y|http://y/a\nb]",
    "[y|http://y|a\n]",
]


# The `* ----`, `* ---- `, `* -----`, `* ----x` and `* ---- y` renders the dash
# rule inside a list item was found in are ROUND13's, and the archives built from
# them cite that round rather than repeat the probe here.
ROUND16 = [
    # --- a lone backslash at the end of a line is no forced newline: Jira
    #     shows it and breaks the line as it breaks any other (#119) ---
    "a\\\nb", "a\\", "a&#92;\nb",
    # --- the dash rule draws at every line start, under every marker and at
    #     every depth, and the item it draws in stays one item of the list ---
    "** ----", "# ----", "* a\n* ----\n* b", "* a\n* ----", "* ----\n** b",
    "|----|", "||h||\n|----|",
    # --- the line the rule leaves behind is a block of its own inside the item,
    #     the reading a line control already gets there ---
    "* ----\nb",
    # --- how long the run may be: four or five dashes, and no more ---
    "-----", "------", "* ------", "---- -", "* ---- -", "* ---", "----\t",
    # --- and one backslash keeps every one of them off the line start ---
    "\\----", "* \\----", "* \\---", "* \\----x", "||h||\n|\\----|",
]


# An archived round is added as one constant above plus one entry here; `all` is
# the concatenation in this order.
ROUNDS = {
    "round1": ROUND1, "round2": ROUND2, "round3": ROUND3, "round4": ROUND4,
    "round5": ROUND5, "round6": ROUND6, "round7": ROUND7, "round8": ROUND8,
    "round9": ROUND9, "round10": ROUND10, "round11": ROUND11, "round12": ROUND12,
    "round13": ROUND13, "round14": ROUND14, "round15": ROUND15, "round16": ROUND16,
}

DEFAULT_DELAY = 0.3


def render(markup, timeout=30):
    body = json.dumps({"rendererType": "atlassian-wiki-renderer",
                       "unrenderedMarkup": markup}).encode()
    req = urllib.request.Request(URL, data=body, method="POST", headers={
        "Content-Type": "application/json",
        "X-Atlassian-Token": "no-check",
    })
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return r.read().decode("utf-8", "replace").strip()


def run(cases, delay=DEFAULT_DELAY, as_json=False):
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


_TXTAR_SECTION = re.compile(r"^-- (.+?) --$")


def parse_txtar(text):
    """Split a txtar archive into {section name: content}; leading comment dropped."""
    sections = {}
    name, body = None, []
    for line in text.split("\n"):
        header = _TXTAR_SECTION.match(line)
        if header:
            if name is not None:
                sections[name] = "\n".join(body)
            name, body = header.group(1).strip(), []
        elif name is not None:
            body.append(line)
    if name is not None:
        sections[name] = "\n".join(body)
    return sections


def txtar_probe(section):
    """Undo the one trailing newline txtar adds; a probe may end in space or tab."""
    return section[:-1] if section.endswith("\n") else section


def read_stdin_probes():
    """One probe per line, JSON-encoded so a probe can show \\n and zero-width runes."""
    for line in sys.stdin:
        raw = line[:-1] if line.endswith("\n") else line
        try:
            decoded = json.loads(raw)
        except ValueError:
            decoded = raw
        yield decoded if isinstance(decoded, str) else raw


def evidence_dir(override=None):
    if override:
        return Path(override)
    return Path(__file__).resolve().parent.parent / EVIDENCE_DIR


def verify(globs, directory, limit=None, delay=DEFAULT_DELAY):
    files = sorted(directory.glob("*.txtar"))
    if globs:
        files = [p for p in files
                 if any(fnmatch.fnmatch(p.name, g) for g in globs)]
    if limit is not None:
        files = files[:limit]
    counts = {"ok": 0, "diff": 0, "skip": 0, "err": 0}
    for path in files:
        sections = parse_txtar(path.read_text(encoding="utf-8"))
        missing = [s for s in ("input.jira", "rendered.html") if s not in sections]
        if missing:
            counts["skip"] += 1
            print("SKIP %s (no %s)" % (path.name, ", ".join(missing)))
            sys.stdout.flush()
            continue
        want = sections["rendered.html"].strip()
        try:
            got = render(txtar_probe(sections["input.jira"]))
        except Exception as e:
            counts["err"] += 1
            print("ERR %s (%r)" % (path.name, e))
        else:
            if got == want:
                counts["ok"] += 1
                print("OK %s" % path.name)
            else:
                counts["diff"] += 1
                print("DIFF %s" % path.name)
                print("  want: " + want)
                print("  got : " + got)
        sys.stdout.flush()
        time.sleep(delay)
    print("%d ok, %d diff, %d skip, %d err of %d fixtures"
          % (counts["ok"], counts["diff"], counts["skip"], counts["err"], len(files)))
    return 1 if counts["diff"] or counts["err"] else 0


def build_parser():
    parser = argparse.ArgumentParser(
        prog="jira-render-evidence.py",
        description="Probe the live ASF Jira wiki renderer and verify the "
                    "evidence fixtures against it.")
    subcommands = parser.add_subparsers(dest="command")

    probe = subcommands.add_parser(
        "probe", help="render markup given on the command line or on stdin")
    probe.add_argument("strings", nargs="*", metavar="STRING")
    probe.add_argument("--stdin", action="store_true",
                       help="read one JSON-encoded probe per line from stdin")
    probe.add_argument("--json", dest="as_json", action="store_true",
                       help='print one {"in": ..., "out": ...} object per line')
    probe.add_argument("--delay", type=float, default=DEFAULT_DELAY,
                       help="seconds between requests (default: %(default)s)")

    round_ = subcommands.add_parser(
        "round", help="replay an archived probe campaign")
    round_.add_argument("name", nargs="?", choices=list(ROUNDS) + ["all"])
    round_.add_argument("--list", dest="list_rounds", action="store_true",
                        help="print each round with its probe count, no requests")
    round_.add_argument("--json", dest="as_json", action="store_true",
                        help='print one {"in": ..., "out": ...} object per line')
    round_.add_argument("--delay", type=float, default=DEFAULT_DELAY,
                        help="seconds between requests (default: %(default)s)")

    verify_ = subcommands.add_parser(
        "verify", help="replay each fixture's input.jira and diff its rendered.html")
    verify_.add_argument("globs", nargs="*", metavar="GLOB",
                         help="match fixture file names (default: every archive)")
    verify_.add_argument("--limit", type=int,
                         help="stop after this many archives")
    verify_.add_argument("--dir", dest="directory",
                         help="evidence directory (default: %s)" % EVIDENCE_DIR)
    verify_.add_argument("--delay", type=float, default=DEFAULT_DELAY,
                         help="seconds between requests (default: %(default)s)")
    return parser


def main(argv):
    parser = build_parser()
    args = parser.parse_args(argv)
    if args.command is None:
        parser.print_help(sys.stderr)
        return 2

    if args.command == "probe":
        cases = list(args.strings)
        if args.stdin:
            cases += list(read_stdin_probes())
        if not cases:
            parser.error("probe needs at least one STRING or --stdin")
        run(cases, delay=args.delay, as_json=args.as_json)
        return 0

    if args.command == "round":
        if args.list_rounds:
            for name, probes in ROUNDS.items():
                print("%-8s %4d probes" % (name, len(probes)))
            return 0
        if args.name is None:
            parser.error("round needs a round name or --list")
        if args.name == "all":
            cases = [m for probes in ROUNDS.values() for m in probes]
        else:
            cases = ROUNDS[args.name]
        run(cases, delay=args.delay, as_json=args.as_json)
        return 0

    return verify(args.globs, evidence_dir(args.directory),
                  limit=args.limit, delay=args.delay)


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
