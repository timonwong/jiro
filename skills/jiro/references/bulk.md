# Bulk workflows

Use this branch for every `issue bulk` operation. Bulk safety remains part of the core mutation loop; this reference supplies command patterns and operation-specific interpretation.

## Dry-run

Preflight the complete JQL selection without changing Jira:

```bash
jiro issue bulk move --jql 'project = OPS AND status = Open' --to Done \
  --resolution Fixed --dry-run --output=json
jiro issue bulk assign --jql 'project = OPS' --assignee me --dry-run --output=json
jiro issue bulk update --jql 'project = OPS' --type Story --dry-run --output=json
```

Review every returned item. Proceed only when the selection, target values, `ready` count, `unchanged` items, and failures match the intended scope.

Bulk move dry-run proves transition availability but does not ask Jira to validate Custom Fields or Resolution; Jira validates those fields during `--yes` execution. For bulk Issue Type changes, also follow [Issue Type changes](issue-types.md).

## Execute and verify

After the user authorizes the preflighted write, repeat the exact JQL and target with `--yes`. Keep dry-run and execution output distinct.

Bulk writes run serially. Preserve every `succeeded`, `failed`, `unknown`, and `not_attempted` item in its returned order. Never infer that an unattempted Issue changed.

Retain the dry-run Issue Keys and read back every target after execution. A list or search read is sufficient only when it proves the same complete key set and requested final values.

Complete the bulk workflow only when every preflighted Issue has a verified final value or an explicitly preserved failure, unknown, or not-attempted outcome.
