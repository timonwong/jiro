#!/usr/bin/env python3
"""Probe a real Jira Server wiki renderer (read-only).

Target: ASF Jira, Jira Server 8.20.10, anonymous access to /rest/api/1.0/render.
Equivalent single-case curl:

  curl -sS -X POST 'https://issues.apache.org/jira/rest/api/1.0/render' \
    -H 'Content-Type: application/json' -H 'X-Atlassian-Token: no-check' \
    -d '{"rendererType":"atlassian-wiki-renderer","unrenderedMarkup":"{{*bold*}}"}'

Usage: python3 hack/jira-render-evidence.py [round1|...|round5|all] [--json]   (default: all)

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
          "round5": ROUND5,
          "all": ROUND1 + ROUND2 + ROUND3 + ROUND4 + ROUND5}[which], as_json=as_json)
