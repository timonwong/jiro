# Testing

Repository-wide testdata conventions for implementation and review work.

## Golden testdata

- Write new golden testdata as `txtar` archives instead of ad hoc JSON fixtures or separate input/output files.
- Keep one independently understandable golden case in each archive.
- Parse archives with `golang.org/x/tools/txtar`.
- Do not mechanically migrate unrelated existing golden fixtures while implementing a feature; keep format-only migrations separately scoped.

For Jira Markup to Jiro Flavored Markdown fixtures, every archive contains all three sections, including an explicit empty warning list:

```text
-- input.jira --
h1. Example

-- want.md --
# Example

-- warnings.json --
[]
```

For Jiro Flavored Markdown to Jira Markup fixtures, use the corresponding section names and likewise include all three sections:

```text
-- input.md --
# Example

-- want.jira --
h1. Example

-- warnings.json --
[]
```

## Jira renderer evidence

`internal/markup/testdata/jfm/jira_evidence` holds one observed Jira renderer behaviour per archive, in four sections: `input.jira`, `rendered.html`, `want.md`, and `warnings.json`. `rendered.html` is the verbatim response of the live renderer; the golden loader ignores it and it exists so a grammar rule can be traced to the render that justifies it.

A two-line comment header precedes the sections:

```text
source: ASF Jira Server 8.20.10, POST /rest/api/1.0/render (hack/jira-render-evidence.py), captured 2026-08-22
observed: one sentence on what Jira rendered and what jiro therefore does
```

`python3 hack/jira-render-evidence.py verify` replays every archive's `input.jira` against the live renderer and diffs the stored `rendered.html`, so a new archive needs no entry anywhere else — its own `input.jira` is the probe. Capture new evidence with `python3 hack/jira-render-evidence.py probe '<markup>' --json`; the `roundN` lists in that script are a frozen historical archive cited by number from fixtures and test comments.

## JFM specification examples

Normative examples in `docs/jiro-flavored-markdown.md` are executable conformance cases. Each example has a stable `jfm-spec-example` marker, a direction, and exact `Input`, `Output`, and `Warnings` fences. Keep examples focused on format rules visible to users; parser, renderer, API, and test-harness details do not belong in the specification.

Use `txtar` golden fixtures for exhaustive edge cases. A specification example and a golden may cover the same rule when the example explains a core language guarantee and the golden protects additional boundary detail.
