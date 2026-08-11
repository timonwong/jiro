# Cache custom field metadata per principal

jiro keeps a disposable 24-hour snapshot of field IDs, Jira display names, aliases, and types for each Jira Instance and authenticated Principal instead of querying `/field` for every resolution. Exact `customfield_N` IDs still bypass metadata entirely; when an expired snapshot cannot be refreshed, jiro deliberately allows even mutating commands to use it when the requested field can still be resolved, but reports the risk through a structured warning. Principal isolation prevents field visibility from one Jira user leaking into another user's resolution, while an explicit `cache refresh` command restores a known-live snapshot.

The snapshot's read-side scope is extended to every visible Issue field by [ADR 0014](0014-resolve-read-field-selectors-through-metadata.md): read commands resolve Field Selectors through it and report the resulting ordered Field Selection in normalized JSON.
