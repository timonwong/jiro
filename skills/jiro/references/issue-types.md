# Issue Type changes

Use this branch before changing the Issue Type of one Issue or a bulk JQL selection.

## Preflight

For a single Issue, run:

```bash
jiro issue list-types OPS-42 --output=json
```

Resolve an exact ID or case-insensitive unique name only from that Issue's compatible values. Treat a missing target as incompatible even if the Issue Type exists elsewhere in the Project or Jira Instance.

Bulk Issue Type dry-run checks every Issue's compatible values. Preserve `unchanged` separately from `ready`; the typed command fails closed instead of sending a target Jira reports as incompatible.

## Mutate and read back

`issue update --type` is exclusive and must not be combined with another update flag. An already-matching Issue Type is a no-op. Single and bulk Issue Type commands perform an immediate Issue Type readback.

Treat a readback mismatch as failed. When Jira may have accepted the write but readback is unavailable, preserve the `unknown` result and inspect Jira before retrying. In a bulk run, an `unknown` result stops later writes because the preceding update may already have changed Jira.

Complete this branch only when every targeted Issue Type is read back as the requested value, unchanged as intended, failed with its returned error, or preserved as `unknown` with no automatic retry.
