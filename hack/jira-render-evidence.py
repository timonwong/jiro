#!/usr/bin/env python3
"""Probe a real Jira Server wiki renderer (read-only).

Target: ASF Jira, Jira Server 8.20.10, anonymous access to /rest/api/1.0/render.
Equivalent single-case curl:

  curl -sS -X POST 'https://issues.apache.org/jira/rest/api/1.0/render' \
    -H 'Content-Type: application/json' -H 'X-Atlassian-Token: no-check' \
    -d '{"rendererType":"atlassian-wiki-renderer","unrenderedMarkup":"{{*bold*}}"}'

Usage: python3 hack/jira-render-evidence.py [round1|...|round9|all] [--json]   (default: all)

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
          "round5": ROUND5, "round6": ROUND6, "round7": ROUND7, "round9": ROUND9,
          "all": ROUND1 + ROUND2 + ROUND3 + ROUND4 + ROUND5 + ROUND6 + ROUND7
                 + ROUND9}[which], as_json=as_json)
