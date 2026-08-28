---
name: jiro
description: Manage Jira Data Center and Server through jiro. Use for Jira operations or offline JFM and Jira Markup conversion.
---

# Jiro

Use `jiro` as the primary interface for Jira Data Center and Server. For every typed Jira state mutation, follow **inspect -> preflight -> mutate -> read back**. Finish a mutation only when a read shows the requested Jira state.

## Description and Comment text

Choose the body format before composing the mutation command:

| Body source | Body syntax | Input flag |
| --- | --- | --- |
| Newly authored Markdown/JFM | `[label](target)` | `--input-format=jfm` |
| Text copied from a `jiro` read | Jira Markup, for example `[label|target]` | `--input-format=jira` or omit |

For example:

```text
New JFM body:       [MR !139](https://gitlab.example/mr/139)
Jira Markup body:   [MR !139|https://gitlab.example/mr/139]
```

Treat `[label|target]` as Jira Markup, not JFM. Keep the body syntax and input
flag matched.

## Establish the contract

Check the installed CLI before relying on remembered syntax:

```bash
jiro --version
jiro schema --output=json
jiro COMMAND --help
```

Treat `jiro schema --output=json` as the source of truth for commands, flags, mutation status, output shapes, and exit codes. Use the singular resource namespaces `issue`, `project`, and `field`. jiro targets Jira Data Center and Server REST API v2; verify compatibility before acting on Jira Cloud or ADF data.

Load each conditional branch before using it:

- For authentication, Profile management, or uncertainty about the effective Jira Instance or Credential, read [Authentication and profiles](references/authentication.md).
- For explicit read fields, Custom Field Aliases, Field Metadata Snapshots, or Sprint Memberships, read [Fields and Sprint Memberships](references/fields.md).
- For a single or bulk Issue Type change, read [Issue Type changes](references/issue-types.md).
- For `issue clone SOURCE`, read [Issue Clone workflow](references/issue-clone.md) before mutation.
- For Board or Sprint discovery, or any `--sprint` selector, read [Boards and Sprints](references/agile.md).
- Before constructing any command containing an Issue Description or Comment body, read [JFM workflows](references/jfm.md) and choose the body syntax and `--input-format` from the table above. Standalone `jiro jfm` conversion is offline and skips the Jira mutation loop.
- For any `issue bulk` operation, read [Bulk workflows](references/bulk.md) before dry-run.
- When the required operation is unavailable in typed commands (confirmed by `schema` and `--help`), read [REST API fallback](references/rest-api-fallback.md).

## Inspect

Read the current state and the Jira-owned metadata that constrains the request. Use normalized JSON for automation and verification.

```bash
jiro issue show OPS-42 --output=json
jiro issue list-transitions OPS-42 --output=json
jiro issue link types --output=json
```

Prefer structured filters when they express the search and JQL when they do not:

```bash
jiro issue list --project OPS --status "In Progress" --assignee me --output=json
jiro search 'project = OPS AND updated >= -7d ORDER BY updated DESC' --all --output=json
```

For read-only work, finish only when the returned data covers the requested scope. Exhaust pagination when completeness matters, and report every warning or partial failure. An empty result is valid unless the request requires a match.

Before a write, confirm the Jira Instance, every target Issue Key, and every current value or Jira-owned choice needed to determine whether the final state already holds and whether the write is valid.

## Preflight

Resolve Jira-owned names and write semantics before mutating:

- For every Description or Comment body, record `source`, `syntax`, and
  `--input-format` before drafting the payload: authored Markdown/JFM uses
  Markdown syntax and `jfm`; typed-read text uses Jira Markup syntax and `jira`
  or omitted.
- Match a transition by the exact ID, unique name, or unique destination status returned by `issue list-transitions`.
- Resolve a Link Type with `issue link types`; preserve the outward direction from `FROM` to `--to`.
- Use a file or `-` for stdin for long descriptions and Comment Bodies, keeping inline and file forms mutually exclusive. `issue move --comment` is inline-only.
- For `issue move --resolution`, use a resolution name, numeric ID, or `none` to clear. Use dedicated flags rather than passing system fields through `--field`.
- Treat `--component` and `--fix-version` updates as full replacements. Use the single value `none` only for an empty final field.

Preflight is complete when the exact Jira Instance, target Issues, current values,
every text body's input-format classification, Jira-owned selectors, and final
payload are resolved with no remaining ambiguity.

## Mutate

Use only the operation and fields requested. Carry an explicitly selected `--profile` through inspection, mutation, and readback.

```bash
jiro issue update OPS-42 --priority High --output=json
jiro issue assign OPS-42 --assignee none --output=json
jiro issue link add OPS-42 --to OPS-99 --type Blocks --output=json
```

`issue move` sends its transition, Comment, Resolution, and Custom Fields in one Jira transition request. Delete an Issue Link by its Jira Link ID.

Preserve partial results. A failed follow-up operation can leave a newly created Issue or ordinary update fields in Jira; retain the returned Issue Key and every confirmed update when reporting the failure.

Mutation is complete when the command has returned a normalized result recording
every succeeded, failed, unknown, and unattempted operation; Jira readback still
determines whether the requested final state was achieved.

## Read back

Read every consequential result through jiro after the write:

- Use `issue show` for creation, field updates, transitions, and assignments.
- Use `issue comment list` for Comments and `issue link list` for Issue Link changes.
- Use `issue comment edit ISSUE-KEY COMMENT-ID` to replace one Comment body. It
  requires `--body` or `--body-file`, defaults to Jira Markup, and uses
  `--input-format=jfm` for newly authored Markdown.

Complete readback only when every requested change is visible with its requested
final value and every consequential resource has been checked. A missing value,
unintended destination status, unresolved partial result, or unavailable readback
keeps the work incomplete or its final state unknown.

## Interpret output and failures

Text is the default. Use `--output=json` for agent or script consumption. Capture normalized stdout and structured stderr separately, and read exit-code meanings from the current schema.

Warnings are degraded successes and retain a successful exit status. On partial failure, preserve and report the complete normalized result from stdout and the structured error from stderr, including every succeeded, failed, unknown, and unattempted item.

Before retrying after permission errors, missing or ambiguous metadata, rate limiting, or uncertain output, inspect Jira to determine whether it applied the write.
