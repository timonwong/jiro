# Fields and Sprint Memberships

Use this branch for explicit read fields, Field Selectors, Custom Field Aliases, Field Metadata Snapshots, or typed Sprint Memberships.

## Select read fields

For read-side `--fields`, use Field Selectors: exact Jira field IDs, direct `customfield_N` IDs, or Jira display names such as `Sprint` and `Story Points`.

jiro resolves exact field IDs first and locally normalized display names second. Duplicate display names require an exact ID. Direct `customfield_N`, `*all`, and `*navigable` selectors bypass metadata; exclusions such as `-Sprint` resolve their inner selector.

Successful JSON returns the ordered resolution in `.data.fieldSelection`. Field Selection proves selector resolution, not that Jira returned a non-empty value.

## Manage metadata

A read-side miss in a fresh Field Metadata Snapshot is `invalid_input` and does not auto-refresh. Run `cache refresh` only when a newly added field is expected. Cold and expired snapshots retain normal fetch and refresh behavior. Treat `stale_field_cache` as a verification risk and report it.

```bash
jiro cache refresh --output=json
jiro field list --custom --output=json
```

For typed mutations, use `customfield_N` directly when known. Use a Custom Field Alias only after `field list --custom` or `cache refresh` proves it is unique for the current Jira Instance and Principal. Direct IDs bypass alias metadata.

Typed-command `--field key=value` accepts only a Custom Field ID or Custom Field Alias, decodes JSON first, and otherwise uses a string. Quote object and array values so the shell passes valid JSON, and use dedicated flags for system fields.

## Read Sprint Memberships

Select the Sprint field through its display-name Field Selector to request typed Sprint Memberships. When metadata identifies Jira Software's Sprint schema, each Issue adds `.sprints` while preserving the complete value under `.fields[customfield_N]`.

Direct IDs, special selectors, exclusions, and implicit fields remain raw-only. Treat `sprint_membership_normalization` as a degraded success: valid memberships remain ordered in `.sprints`, malformed data remains raw, and the command keeps exit code `0`.

For readback, use `.data.fieldSelection` to map each requested selector to its Jira field ID, then read the value from `.data.fields`. For a semantically selected Sprint field, read typed membership from `.data.sprints` and retain `.data.fields[resolved]` as the raw compatibility path.

Complete this branch only when every requested Field Selector is resolved and every required value is present, explicitly empty, or reported as unavailable with its warning or error.

For `issue clone`, Create-screen metadata determines which non-empty Custom
Fields can be copied. A non-creatable value is omitted from the Create Issue
payload and reported as `issue_clone_custom_field_skipped`; explicit
`--field` values still win after copied values are resolved. Sprint fields are
handled through the typed membership path described above and are not replayed
through Create Issue.
