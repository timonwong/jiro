# Boards and Sprints

Use this branch for Board discovery, Sprint discovery, or any Issue mutation that supplies `--sprint`.

## Discover Boards and Sprints

`board list` and `sprint list` are read-only discovery commands:

```bash
jiro board list --output=json
jiro sprint list --state active --output=json
```

For `sprint list --board SELECTOR`, a positive number selects one exact Board ID; other values are case-insensitive Board name substrings, and every matching Board is queried. The default Sprint state is `active`; the other current values come from schema and command help.

For cross-Board results, preserve repeated Sprint IDs because each row represents one queried Board relationship. Preserve `boardId`, `boardName`, and Jira's distinct `originBoardId`. Report `failedBoards` together with successful Sprint rows.

Complete discovery only when the requested Board scope has been queried and every successful relationship and failed Board has been reported.

## Select a Sprint for mutation

Resolve Sprint input as a numeric ID, `active`, or a case-insensitive name substring. Confirm that the first match in Jira Board/page order is the intended Sprint before mutating.

When the target Board is known, pass `--sprint-board SELECTOR` on `issue add`/`issue update` to scope `--sprint` resolution to that Board. The selector uses the same forms as `sprint list --board` and requires `--sprint`.

After the mutation, read the Issue back. When typed Sprint Memberships are required, also follow [Fields and Sprint Memberships](fields.md).

For `issue clone`, inspect the source Sprint Memberships before creation. No
active membership is a successful no-op; exactly one valid active membership
is applied after the clone and its optional `Cloners` link are created; more
than one active membership is rejected before Create Issue. A failed Sprint
write preserves the created Issue and earlier link result as a partial failure.
