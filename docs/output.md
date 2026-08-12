# Output and Automation

Text is the default output format. Normalized JSON is the automation contract,
and `jiro schema` describes that contract for agents and other tooling.

## Terminal and piped text

Commands with tabular text output adapt to stdout:

- A terminal receives a column-aligned table sized to the available width.
  Fixed Issue Key, ID, Alias, and similar columns remain complete. Descriptive
  columns may be truncated with `...`.
- A pipe or file receives headerless TSV with untruncated, single-line values.
  An empty result writes zero bytes.

Table cells are rendered as safe single-line text. JSON retains the original
values.

`jiro issue show` is the detail-view exception. Text output uses man-page-style
sections with uppercase field headers and four-space-indented values. TTY
headers are bold; piped or redirected headers contain no ANSI styling. Values
retain physical line breaks and are never wrapped or truncated by jiro. Empty
fields remain visible. JSON output is unchanged.

Set `JIRO_FORCE_TTY` to `1`, `true`, `yes`, or `on` to keep
terminal-style output in a pipeline:

```sh
JIRO_FORCE_TTY=1 jiro issue list --project OPS | less
```

The exact values `0`, `false`, `no`, and `off` leave normal terminal
detection in place. Any other value is a configuration error for text output.
When forced, jiro reads the controlling terminal width and falls back to 80
columns if the width is unavailable.

## JSON contract

The short and long output flags accept attached or separate values:

```sh
jiro issue list -o json
jiro issue list -ojson
jiro issue list --output json
jiro issue list --output=json
```

Normalized JSON uses a stable envelope:

```json
{"schemaVersion":"1","data":{"issues":[],"total":0,"startAt":0,"maxResults":50}}
```

Successful commands can include structured warnings without changing the exit
code:

```json
{"schemaVersion":"1","data":{"key":"OPS-42","updated":true},"warnings":[{"code":"stale_field_cache","message":"using stale field metadata","details":{"fetchedAt":"2026-07-29T10:00:00Z"}}]}
```

Successful data is written to stdout. Structured JSON failures are written to
stderr with stable exit codes, while human-readable warnings use stderr.

For a partial result with exit code `7`, jiro writes the complete normalized
result to stdout before writing a structured `partial_failure` error to
stderr. This includes a successful Issue creation whose Sprint move failed and
Issue Type writes whose readback is unknown, plus bulk runs with failed,
unknown, or unattempted items.
Cross-Board `sprint list` queries use the same contract: successful Sprint
relationships remain on stdout and JSON identifies failed Boards.

`jiro schema` describes automation-facing commands, flags, mutation status,
output shapes, and exit codes as machine-readable JSON. Shell completion emits
shell code and is outside that contract.
