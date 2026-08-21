# Issue Clone workflow

Use this branch for `jiro issue clone SOURCE`.

## Inspect and preflight

Read the source Issue as normalized JSON and confirm its Project, Issue Type,
Parent, standard fields, Custom Fields, and Sprint Memberships before creating
anything. The clone stays in the source Project and keeps its Issue Type and
Parent; there is no cross-Project or cross-Issue-Type form.

Before a default linked clone, resolve the exact `Cloners` Link Type and verify
that its outward relationship is `clones`. A missing, ambiguous, or malformed
link type is a preflight failure. `--no-link` skips that lookup and the link
write.

Use the target Create-screen metadata for copied Custom Fields. Explicit
`--field alias-or-id=value` values use the normal Custom Field resolution rules
and override copied values. Sprint is handled through Agile membership and is
never sent as a raw Custom Field. Zero active Sprints is valid, one valid active
Sprint is selected, and multiple active Sprints fail before Issue creation;
malformed memberships remain visible through the structured warning.

## Mutate

Run the command with normalized JSON when another agent or script will consume
the result:

```bash
jiro issue clone OPS-42 --output=json
```

Writes are staged as Create Issue, source-to-clone `Cloners` link, and active
Sprint assignment. A later failure does not roll back an earlier write. Keep
the created Issue Key and every completed stage from the partial result; exit
code `7` means the clone exists but follow-up work needs reconciliation.

## Read back

After a successful clone, read the new Issue with `issue show`. When linking is
enabled, read its links with `issue link list` and confirm the source points
outward to the clone through `Cloners`/`clones`. When Sprint assignment was
selected, request the Sprint field and confirm the clone's typed membership.
Retain any structured warnings, especially `issue_clone_custom_field_skipped`
and `sprint_membership_normalization`.
