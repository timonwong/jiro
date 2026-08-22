#!/usr/bin/env python3
"""Probe a real Jira Server wiki renderer (read-only).

Target: ASF Jira, Jira Server 8.20.10, anonymous access to /rest/api/1.0/render.
Equivalent single-case curl:

  curl -sS -X POST 'https://issues.apache.org/jira/rest/api/1.0/render' \
    -H 'Content-Type: application/json' -H 'X-Atlassian-Token: no-check' \
    -d '{"rendererType":"atlassian-wiki-renderer","unrenderedMarkup":"{{*bold*}}"}'

Usage: python3 hack/jira-render-evidence.py [round1|round2|round3|all]   (default: all)
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


def render(markup, timeout=30):
    body = json.dumps({"rendererType": "atlassian-wiki-renderer",
                       "unrenderedMarkup": markup}).encode()
    req = urllib.request.Request(URL, data=body, method="POST", headers={
        "Content-Type": "application/json",
        "X-Atlassian-Token": "no-check",
    })
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return r.read().decode("utf-8", "replace").strip()


def run(cases, delay=0.3):
    seen = set()
    for m in cases:
        if m in seen:
            continue
        seen.add(m)
        try:
            out = render(m)
        except Exception as e:
            out = "ERR " + repr(e)
        print("IN : " + repr(m))
        print("OUT: " + out)
        print("-" * 66)
        time.sleep(delay)


if __name__ == "__main__":
    which = sys.argv[1] if len(sys.argv) > 1 else "all"
    run({"round1": ROUND1, "round2": ROUND2, "round3": ROUND3,
          "all": ROUND1 + ROUND2 + ROUND3}[which])
