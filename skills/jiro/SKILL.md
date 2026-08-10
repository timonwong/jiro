---
name: jiro
description: Manage Jira Data Center and Server with the jiro CLI. Use for profile authentication; issue search and mutation; project, field, transition, link, comment, or bulk workflows; JFM and Jira Markup conversion; automation through jiro's stable JSON contract; or authenticated raw Jira REST requests through jiro api.
---

# Jiro

Use `jiro` as the primary interface for Jira Data Center and Server. For every mutation, follow **inspect -> preflight -> mutate -> read back**. Finish only when a read shows the requested Jira state.

## Establish the contract

Check the installed CLI before relying on remembered syntax:

```bash
jiro --version
jiro schema --output=json
jiro COMMAND --help
```

Treat `jiro schema --output=json` as the source of truth for commands, flags, mutation status, output shapes, and exit codes. Use the singular resource namespaces `issue`, `project`, and `field`. jiro targets Jira Data Center and Server REST API v2; verify compatibility before acting on Jira Cloud or ADF data.

When the task involves authentication or Profile management, or the effective Jira Instance or Credential is uncertain or rejected, read [Authentication and profiles](references/authentication.md) before Jira work.

When the task authors or diagnoses Jiro Flavored Markdown, converts between JFM and Jira Markup, or needs rich-text conversion warnings, read [JFM workflows](references/jfm.md) before choosing flags or interpreting output. Standalone `jiro jfm` conversion is offline and does not enter the Jira mutation loop.

## Inspect

Read the current state and the Jira-owned metadata that constrains the requested write. Use normalized JSON for automation and verification.

```bash
jiro issue show OPS-42 --output=json
jiro issue list-types OPS-42 --output=json
jiro issue list-transitions OPS-42 --output=json
jiro issue link types --output=json
jiro field list --custom --output=json
jiro board list --output=json
jiro sprint list --state active --output=json
```

Prefer structured filters when they express the search and JQL when they do not:

```bash
jiro issue list --project OPS --status "In Progress" --assignee me --output=json
jiro search 'project = OPS AND updated >= -7d ORDER BY updated DESC' --all --output=json
```

Confirm every target Issue Key, Jira Instance, current status, assignee, relevant field value, transition, Link Type, and existing comment or link needed to judge the final state. Treat an empty list as valid unless the request requires a match.

## Preflight

Resolve Jira-owned names and write semantics before mutating:

- Match a transition by the exact ID, unique name, or unique destination status returned by `issue list-transitions`.
- Before changing an Issue Type, run `issue list-types ISSUE-KEY`. Resolve an exact ID or case-insensitive unique name only from that Issue's compatible values. Treat a missing target as incompatible even if the Issue Type exists elsewhere in the project or Jira Instance.
- Resolve a Link Type with `issue link types`; preserve the outward direction from `FROM` to `--to`.
- Use `customfield_N` directly when known. Use a human alias only after `field list --custom` or `cache refresh` proves it is unique for the current Jira Instance and Principal. Treat `stale_field_cache` as a verification risk: disclose it and verify the written field directly. Direct IDs bypass alias metadata.
- Determine whether description or comment input is Jira Markup or Jiro Flavored Markdown (JFM). Jira Markup is the byte-preserving default; request JFM conversion with `--input-format=jfm`.
- Use a file or `-` for stdin for long descriptions and `issue comment add`, keeping inline and file forms mutually exclusive. `issue move --comment` is inline-only.
- For `issue move --resolution`, use a resolution name, numeric ID, or `none` to clear. Do not pass system fields such as `resolution` through `--field`.
- Resolve Sprint input as a numeric ID, `active`, or a case-insensitive name substring. Confirm that the first match in Jira board/page order is intended.
- Use `board list` and `sprint list` only for read-only discovery. `sprint list --board SELECTOR` treats a positive number as an exact Board ID and other values as case-insensitive Board name substrings; multiple name matches are all queried. Its `--state` is `active` by default and also accepts `closed`, `future`, or `all`.
- Treat `--component` and `--fix-version` updates as full replacements. Use the single value `none` only for an empty final field.

Refresh custom-field aliases when they are stale, missing, ambiguous, or workflow-sensitive:

```bash
jiro cache refresh --output=json
jiro field list --custom --output=json
```

For bulk changes, preflight the complete JQL selection without changing Jira:

```bash
jiro issue bulk move --jql 'project = OPS AND status = Open' --to Done \
  --resolution Fixed --dry-run --output=json
jiro issue bulk assign --jql 'project = OPS' --assignee me --dry-run --output=json
jiro issue bulk update --jql 'project = OPS' --type Story --dry-run --output=json
```

Review every returned item. Proceed only when the selection, targets, `ready` count, and failures match the intended scope. Bulk move dry-run proves transition availability but does not ask Jira to validate custom fields or resolution; field validation occurs during `--yes` execution.

Bulk Issue Type dry-run checks each Issue's `editmeta.fields.issuetype.allowedValues`. Preserve `unchanged` separately from `ready`. Affected older Jira Server releases can accept incompatible types and leave invalid workflow state, so the typed command must fail closed instead of sending a target absent from these values.

## Mutate

Use only the operation and fields requested.

```bash
jiro issue add --project OPS --type Bug --summary "Broken deployment" \
  --description-file issue.md --input-format=jfm --output=json
jiro issue update OPS-42 --priority High --component API --fix-version 4.5 --output=json
jiro issue update OPS-42 --type Story --output=json
jiro issue comment add OPS-42 --body-file comment.md --input-format=jfm --output=json
jiro issue move OPS-42 --to Done --comment "**Verified** in staging." \
  --input-format=jfm --resolution Fixed --field story-points=5 --output=json
jiro issue assign OPS-42 --assignee me --output=json
jiro issue assign OPS-42 --assignee none --output=json
jiro issue link add OPS-42 --to OPS-99 --type Blocks --output=json
jiro issue link delete 10001 --output=json
```

Typed-command `--field key=value` accepts only a Custom Field ID or Custom Field Alias, decodes JSON first, and otherwise uses a string; quote object and array values so the shell passes valid JSON. Use dedicated flags for system fields. `issue move` sends its transition, comment, resolution, and custom fields in one Jira transition request; it never falls back to a later comment request. Use the transition proven during preflight. Delete an Issue Link by its Jira Link ID.

`issue update --type` is exclusive and must not be combined with any other update flag. It sends the resolved Issue Type ID through `PUT /rest/api/2/issue/{key}`, then reads back only `issuetype`. An already-matching type is a no-op. Treat a readback mismatch as failed; treat an unavailable readback as unknown and do not retry until Jira is inspected.

Preserve partial results. A failed Sprint move can leave a newly created issue or ordinary update fields in Jira; retain the returned Issue Key and updated fields when reporting the failure.

After the user authorizes a preflighted broad write, repeat the same JQL and target with `--yes`:

```bash
jiro issue bulk move --jql 'project = OPS AND status = Open' --to Done \
  --resolution Fixed --yes --output=json
jiro issue bulk assign --jql 'project = OPS' --assignee me --yes --output=json
jiro issue bulk update --jql 'project = OPS' --type Story --yes --output=json
```

Keep dry-run and execution results distinct. Bulk writes run serially and may return `failed`, `unknown`, or `not_attempted` items. An unknown Issue Type readback stops the run because the preceding PUT may already have changed Jira.

## Read back

Read every consequential result through jiro after the write:

- Use `issue show` for creation, field updates, transitions, assignments, and Sprint membership fields returned by the instance.
- `issue update --type` and `issue bulk update --type` already perform an immediate Issue Type readback. Preserve their `unknown` result when Jira accepted the PUT but the readback failed; do not hide it behind a later retry.
- Use `issue comment list` for comments and `issue link list` for link changes.
- Retain the Issue Keys from a bulk dry-run and read back every targeted issue. A list or search read is sufficient only when it proves the same complete key set and final values.
- Read normalized standard fields such as status and assignee from `.data.status` and `.data.assignee`. Request custom fields with `issue show --fields` and read them from `.data.fields`.

A zero exit code is not the completion criterion. Treat a missing field or unintended destination status as incomplete work.

## Interpret output and failures

Text is the default. Use `--output=json` for agent or script consumption. Capture normalized stdout and structured stderr separately, and read exit-code meanings from the current schema.

Warnings are degraded successes and retain a successful exit status. On partial failure, preserve and report the complete normalized result from stdout and the structured error from stderr, including every succeeded, failed, and unattempted item.

For cross-Board `sprint list`, do not deduplicate repeated Sprint IDs: each row is one queried Board relationship. Preserve `boardId`, `boardName`, and Jira's distinct `originBoardId`. If JSON includes `failedBoards`, report those failures together with the successful Sprint rows.

Pause after permission errors, missing or ambiguous metadata, rate limiting, or uncertain output. Determine whether Jira applied the write before retrying.

## REST fallback

Continue with typed commands whenever they cover the request. When `jiro schema --output=json` and the relevant typed-command help prove that a required operation is unavailable, load [REST API fallback](references/rest-api-fallback.md) and follow it completely before issuing `jiro api`.
