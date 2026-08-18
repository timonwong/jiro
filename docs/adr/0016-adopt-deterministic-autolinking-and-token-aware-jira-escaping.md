---
status: accepted
---

# Adopt deterministic autolinking and token-aware Jira escaping

JFM adopts the GitHub Flavored Markdown autolink-literal extension and additionally recognizes bare `mailto:` URIs. jiro serializes every recognized autolink as explicit Jira link notation instead of relying on Jira's renderer to discover links. This keeps link semantics deterministic across Jira Data Center and Server instances while retaining GFM's established URL boundaries, trailing-punctuation handling, and balanced-parenthesis rules.

Jira effect delimiters in plain JFM text are escaped only when they would participate in a complete Jira formatting span in that context. Escaping every effect-delimiter character is rejected because ordinary word punctuation and URL content remain literal unless Jira can tokenize them as formatting. Other Jira structural and line-control characters retain their existing safety escaping. This preserves readable canonical Jira Markup without allowing decoded JFM text to acquire unintended Jira semantics.

The normative syntax, canonical representations, and conformance examples remain in `docs/jiro-flavored-markdown.md`. The existing `ToJFM` and `FromJFM` interfaces and shared semantic model remain unchanged.
