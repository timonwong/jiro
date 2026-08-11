# Resolve read Field Selectors through Principal-scoped metadata

jiro treats each explicit `--fields` value on `search`, `issue list`, and `issue show` as a user-facing Field Selector. Exact `customfield_N` values and Jira's special selectors bypass metadata; every other selector is resolved against the current Jira Instance and Principal's complete Field Metadata Snapshot by exact Jira field ID first and locally normalized display name second, so callers do not need to know whether a field is standard, custom, or represented by an alias.

Unknown or ambiguous selectors fail before the Issue request. A missing fresh-snapshot match does not trigger an extra refresh; cold snapshots and expired snapshots retain their normal fetch and refresh behavior, stale metadata may still be used with a structured warning, and metadata acquisition failures keep their operational error category. Successful JSON includes an ordered Field Selection that preserves each original selector and its resolved Jira selector, while Issue `.fields`, text output, `contractVersion=3`, and `schemaVersion=1` remain unchanged.

## Considered options

- An explicit `alias:` prefix was rejected because it requires callers to understand Jira's field classification before selecting a field.
- A static standard-field allowlist was rejected because it would drift across Jira versions and installed applications.
- Passing unknown selectors through to Jira was rejected because Jira does not provide a stable unknown-selector failure contract and may silently omit the requested field.
