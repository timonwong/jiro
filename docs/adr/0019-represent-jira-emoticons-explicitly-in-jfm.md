---
status: accepted
---

# Represent Jira emoticons explicitly in JFM

Jira Server 8.20.10 interprets supported emoticon tokens such as `(x)`, `:)`, and `;)` as icons in visible text. The same source characters can also be intended as ordinary prose, and Jira Markup has no source-level marker that lets JFM recover that intent after the fact. Treating every token as an icon would corrupt literal prose; treating every token as text would lose genuine Jira icon semantics during Jira Markup to JFM to Jira conversion.

JFM therefore distinguishes a Jira Emoticon from literal text. The canonical representation is the attrless inline directive `:emoticon[token]`, where `token` is exactly one token already verified by the shared Jira inline grammar. The directive has no attributes, does not accept nested inline content, and does not accept unknown tokens. `(*y)` is accepted as an input alias and canonicalizes to `(*)`; no other token aliases are introduced without renderer evidence.

Jira Markup to JFM recognizes the same token gate only in visible inline text. An unescaped known token becomes a Jira Emoticon Directive. A token protected by Jira escaping remains ordinary JFM text. Link targets, image and macro values, code spans, code blocks, and Monospace Spans never produce Jira Emoticon semantics. JFM to Jira emits a directive's raw token. If a word character immediately follows the token, Jira suppresses the icon; jiro still emits the visible token and reports an `emoticon` warning because the icon semantic cannot be guaranteed. It does not insert a synthetic U+200B. A subsequent Jira Markup to JFM conversion therefore reads the result as ordinary text without attempting to reconstruct the directive.

Ordinary JFM text is protected with the smallest Jira-safe encoding that keeps it visible. Parenthesized tokens escape both parentheses with Jira backslashes (`\\(x\\)`). Colon-prefixed tokens encode the colon (`&#58;)`, `&#58;P`, `&#58;D`, `&#58;(`), because Jira does not consume a backslash before `:`. The wink token encodes its leading semicolon (`&#59;)`). These encodings produce no warning and convert back to ordinary JFM text.

Malformed or unknown `:emoticon` directives use literal fallback and report a warning with `Construct` set to `emoticon`. An unknown renderer icon cannot be inferred from Jira Markup source or from renderer-specific image paths, so it is outside this semantic surface rather than guessed into a directive.

This decision makes warning-free known emoticons reversible while explicitly accepting a lossy boundary for a directive followed by a word character. The normative syntax and conformance examples live in `docs/jiro-flavored-markdown.md`.
