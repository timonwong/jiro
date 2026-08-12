# Field Selectors and Custom Fields

## Read Field Selectors

`search`, `issue list`, and `issue show` accept comma-separated Field
Selectors through `--fields`. A Field Selector can be an exact Jira field ID,
a Custom Field ID, or a Jira display name; callers do not need to know which
category Jira uses for a field.

```sh
jiro search 'project = OPS ORDER BY updated DESC' \
  --all \
  --fields 'summary,Sprint,Story Points' \
  --output=json
```

Exact Jira field IDs take priority. Otherwise jiro normalizes the selector and
Jira display names locally, so `Sprint` and `sprint` are equivalent, as are
`Story Points`, `story-points`, and `story_points`. Duplicate normalized names
are ambiguous and require an exact field ID such as `customfield_10006`.
Jira's `*all` and `*navigable` selectors are preserved, and exclusions resolve
their inner selector: `-Sprint` can become `-customfield_10001`.

Normalized JSON records the ordered Field Selection next to the Issue or
search result:

```json
{
  "fieldSelection": [
    {"selector": "summary", "resolved": "summary"},
    {"selector": "Sprint", "resolved": "customfield_10001"}
  ],
  "issues": []
}
```

Issue `.fields` remains keyed by Jira field ID. Use `fieldSelection` to map the
requested selector to that key; jiro does not rename or duplicate field data.
Text output is unchanged. Successful resolution proves only which selector was
sent to Jira, not that every returned Issue has a non-empty field value.

### Sprint Memberships

When a positive, explicit Field Selector resolves to Jira Software's Sprint
field identity (`com.pyxis.greenhopper.jira:gh-sprint`), jiro also projects its
value into typed Sprint Memberships while retaining the original Custom Field:

```json
{
  "key": "OPS-42",
  "fields": {
    "customfield_10001": [
      "com.atlassian.greenhopper.service.sprint.Sprint@...[id=1839,state=ACTIVE,name=OPS-S2]"
    ]
  },
  "sprints": [
    {"id": 1839, "name": "OPS-S2", "state": "active"}
  ],
  "fieldSelection": [
    {"selector": "Sprint", "resolved": "customfield_10001"}
  ]
}
```

Sprint Memberships expose `id`, `name`, `state`, `boardId`, `goal`,
`startDate`, `endDate`, and `completeDate` when present. `id` is required,
states are lowercase, `rapidViewId` becomes `boardId`, and Jira date strings
are preserved. Jira order and duplicate memberships are retained.

Use the Sprint field's display name, including an instance-specific renamed
display name, when the typed projection is required. Direct `customfield_N`,
`*all`, `*navigable`, exclusions, and reads without explicit `--fields` remain
raw-only and do not add a metadata lookup. An explicitly selected empty Sprint
field produces `"sprints": []`; without semantic selection, `sprints` is
omitted.

Malformed entries remain available under `.fields`. jiro keeps every valid
Membership and emits one `sprint_membership_normalization` warning per affected
Issue and Sprint field. The warning contains zero-based raw entry indexes,
retains exit code `0`, and appears in JSON `.warnings[]` or text stderr.

Direct `customfield_N`, `*all`, and `*navigable` selectors bypass field
metadata. Other selectors use the current Principal's Field Metadata Snapshot.
A fresh snapshot miss fails without an automatic refresh; use `jiro cache
refresh` when a newly added field is expected. Cold and expired snapshots keep
their normal fetch and refresh behavior.

## Custom fields

Typed Issue mutations accept only custom fields through repeatable
`--field key=value` flags:

```sh
jiro issue add \
  --project OPS \
  --type Story \
  --summary "Agent-friendly output" \
  --field story-points=5 \
  --field customfield_10006='{"id":"123"}'
```

A Custom Field ID such as `customfield_10006` is used as-is and has priority.
It does not call `/myself`, read the cache, or query Jira field metadata.

A Custom Field Alias such as `story-points` is resolved through a 24-hour Field
Metadata Snapshot. The snapshot contains every visible Issue field, including
its Jira schema custom identity, and is
scoped to the normalized Jira base URL and the Principal returned by
`/myself`. Missing or ambiguous aliases are errors. Values are decoded as JSON
first and fall back to strings.
System fields are not accepted through typed-command `--field`; use their
dedicated flags, such as `--resolution`, instead. The raw `jiro api --field`
request builder is a separate contract and is not subject to this restriction.

## Field Metadata Snapshot cache

The disposable JSON snapshots live under `github.com/adrg/xdg`'s
`xdg.CacheHome` at:

```text
jiro/fields/hosts/<url-slug+hash>--<principal-slug+hash>.json
```

This honors the platform XDG cache location, including `XDG_CACHE_HOME` where
applicable. macOS defaults to
`~/Library/Caches/jiro/fields/hosts/`. There is no jiro-specific cache
environment variable.

Refresh the current Principal's snapshot explicitly with:

```sh
jiro cache refresh
```

An expired snapshot is refreshed before use. If Jira cannot refresh it, jiro
continues with the stale mapping when it can resolve the requested field,
including for Issue mutations, and emits a warning. `jiro field list --custom`
filters the same snapshot; the complete `field list` remains live.
