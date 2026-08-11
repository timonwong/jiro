---
name: jiro
description: Manage Jira Data Center and Server through jiro. Use for Jira reads or writes, Profile authentication, normalized JSON automation, offline JFM and Jira Markup conversion, or authenticated raw REST fallback through jiro api.
---

# Jiro

Use `jiro` as the primary interface for Jira Data Center and Server. For every typed Jira state mutation, follow **inspect -> preflight -> mutate -> read back**. Finish a mutation only when a read shows the requested Jira state.

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
- For Board or Sprint discovery, or any `--sprint` selector, read [Boards and Sprints](references/agile.md).
- For JFM authoring, conversion, mutation input, or `jfm_conversion` warnings, read [JFM workflows](references/jfm.md). Standalone `jiro jfm` conversion is offline and skips the Jira mutation loop.
- For any `issue bulk` operation, read [Bulk workflows](references/bulk.md) before dry-run.

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

- Match a transition by the exact ID, unique name, or unique destination status returned by `issue list-transitions`.
- Resolve a Link Type with `issue link types`; preserve the outward direction from `FROM` to `--to`.
- Use a file or `-` for stdin for long descriptions and Comment Bodies, keeping inline and file forms mutually exclusive. `issue move --comment` is inline-only.
- For `issue move --resolution`, use a resolution name, numeric ID, or `none` to clear. Use dedicated flags rather than passing system fields through `--field`.
- Treat `--component` and `--fix-version` updates as full replacements. Use the single value `none` only for an empty final field.

For every bulk write, run the complete JQL selection with `--dry-run`, review every returned item, and proceed only when the target set, targets, `ready` count, and failures match the intended scope. After the user authorizes that exact write, repeat the same JQL and target with `--yes`.

## Mutate

Use only the operation and fields requested. Carry an explicitly selected `--profile` through inspection, mutation, and readback.

```bash
jiro issue update OPS-42 --priority High --output=json
jiro issue assign OPS-42 --assignee none --output=json
jiro issue link add OPS-42 --to OPS-99 --type Blocks --output=json
```

`issue move` sends its transition, Comment, Resolution, and Custom Fields in one Jira transition request; it does not fall back to a later Comment request. Delete an Issue Link by its Jira Link ID.

Preserve partial results. A failed follow-up operation can leave a newly created Issue or ordinary update fields in Jira; retain the returned Issue Key and every confirmed update when reporting the failure.

Keep bulk dry-run and execution results distinct. Bulk writes run serially and may return `failed`, `unknown`, or `not_attempted` items. Preserve the complete ordered result.

## Read back

Read every consequential result through jiro after the write:

- Use `issue show` for creation, field updates, transitions, and assignments.
- Use `issue comment list` for Comments and `issue link list` for Issue Link changes.
- Retain the Issue Keys from a bulk dry-run and read back every targeted Issue. A list or search read is sufficient only when it proves the same complete key set and final values.

A zero exit code is not the completion criterion. A missing value, unintended destination status, unresolved partial result, or unavailable readback means the work is incomplete or its final state is unknown.

## Interpret output and failures

Text is the default. Use `--output=json` for agent or script consumption. Capture normalized stdout and structured stderr separately, and read exit-code meanings from the current schema.

Warnings are degraded successes and retain a successful exit status. On partial failure, preserve and report the complete normalized result from stdout and the structured error from stderr, including every succeeded, failed, unknown, and unattempted item.

Before retrying after permission errors, missing or ambiguous metadata, rate limiting, or uncertain output, inspect Jira to determine whether it applied the write.

## REST fallback

Continue with typed commands whenever they cover the request. When `jiro schema --output=json` and the relevant typed-command help prove that a required operation is unavailable, load [REST API fallback](references/rest-api-fallback.md) and follow it completely before issuing `jiro api`.
