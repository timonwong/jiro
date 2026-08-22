# Jiro Flavored Markdown

Use this branch when authoring or diagnosing Jiro Flavored Markdown (JFM), supplying JFM to a Jira mutation, converting between JFM and Jira Markup, or interpreting `jfm_conversion` warnings.

For a Jira mutation, use this branch together with the core `inspect -> preflight -> mutate -> read back` loop. For standalone `jiro jfm` conversion, use this branch without Jira authentication, inspection, mutation, or readback.

## Select the contract

- Jira Markup is the byte-preserving default for Description and Comment Body input.
- Typed Issue and Comment reads expose Jira Markup. When reusing that text, omit `--input-format` or pass `--input-format=jira`; do not send it through JFM conversion.
- Use the canonical `--input-format=jfm` value for JFM. `markdown` is a permanent, warning-free compatibility alias, but generate new commands with `jfm`.
- Use `--input-format` only where the current schema and command help expose it: Issue creation, Issue update, Comment creation, and the inline transition comment on `issue move`. Custom fields retain their declared Jira value contract.
- Treat Jira Markup as the only typed Issue and Comment read representation; the current contract has no JFM projection flags or sibling fields such as `--jfm`, `descriptionJfm`, or `bodyJfm`.

## Supply JFM to a Jira mutation

Use JFM with Issue Description or Comment Body input:

```bash
jiro issue add --project OPS --type Task --summary "Document rollout" \
  --description-file issue.md --input-format=jfm --output=json
jiro issue update OPS-42 --description-file issue.md --input-format=jfm --output=json
jiro issue comment add OPS-42 --body-file comment.md --input-format=jfm --output=json
jiro issue move OPS-42 --to Done --comment "**Verified** in staging." \
  --input-format=jfm --resolution Fixed --output=json
```

Keep inline and file inputs mutually exclusive. Use `-` as the file value to read long input from stdin. `issue move --comment` is inline-only, and an explicitly supplied `--input-format` without `--comment` is invalid.

JFM conversion is best-effort. Conversion warnings retain every target-representable semantic, keep the mutation successful, and use code `jfm_conversion`. In JSON output, inspect `warnings[].details.direction`, `field`, `line`, `column`, `construct`, and `reason`; in text output, preserve stderr alongside the successful result. A fatal conversion failure stops before the Jira write and returns no successful mutation result.

JFM inline code follows Jira renderer behavior rather than treating `{{...}}` as an opaque literal container. Canonical output keeps complete HTTP(S) URLs, simple bracketed literal text, identifier-internal `-` and `_`, literal `&<>`, and Jira backslash escapes readable, while protecting effect spans and Jira structure characters that would be reinterpreted. Jira-specific rendering behavior around a `}}` boundary is not by itself a `jfm_conversion` warning.

## Convert documents offline

Use the non-mutating JFM subcommands when no Jira request is needed:

```bash
jiro jfm to-jira [FILE|-]
jiro jfm from-jira [FILE|-]
jiro --output=json jfm to-jira [FILE|-]
jiro --output=json jfm from-jira [FILE|-]
```

Each command accepts at most one file. With no file or with `-`, it reads stdin. These commands do not load Jira configuration or Credentials and do not access the network.

Text mode writes only the exact converted document to stdout without adding a newline; conversion warnings go to stderr. JSON mode writes the normal versioned envelope with `.data.jiraMarkup` for `to-jira` or `.data.jfm` for `from-jira`, plus structured warnings when present. Invalid input or a fatal conversion failure produces no partial result; read the current schema for its exit code.

Complete standalone conversion only after capturing the exact converted stdout and every warning from stderr, or reporting the fatal conversion failure without a partial result.

## Interpret exact JFM semantics

Treat the JFM specification matching the installed jiro version as the source of truth for directives, canonicalization, round-trip guarantees, lossy conversion, and literal fallback. In a jiro source checkout, use `docs/jiro-flavored-markdown.md`; for a release binary, use the same release tag's specification. Keep detailed syntax solely in that normative specification.
