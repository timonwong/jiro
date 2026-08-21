# Issue Operations

This reference covers behavioral details of Issue mutations, Sprint assignment,
Issue Type changes, and bulk operations. For quick examples see the
[README](../README.md#examples).

## Create and update

`issue update --component` and `--fix-version` replace the complete field.
A single `none` clears it.

### Clone

`issue clone SOURCE` creates an independent Issue in the source Project with
the same Issue Type and Parent. It copies Description, Priority, Assignee,
Labels, Components, Fix Versions, and Custom Fields available on the target
Create screen. Summary defaults to `CLONE - <source summary>`; standard field
flags and repeated `--field alias-or-id=value` values replace copied values.
Sprint Custom Fields are never replayed through Create Issue.

jiro reads the source Custom Fields visible to the authenticated Principal. A
non-empty visible field that is absent from the target Create screen or lacks
the `set` operation is skipped and reported with the structured warning
`issue_clone_custom_field_skipped`.

By default jiro resolves the Jira Link Type named `Cloners` before creation and
then links the source outward to the clone. In Jira's link vocabulary,
`Cloners` is the Link Type name while `clones` is its outward relationship.
`--no-link` skips both Link Type discovery and link creation.

If the source has one valid active Sprint Membership, jiro adds the clone to
that Sprint after the link is created. The workflow supports one Sprint Custom
Field; multiple Sprint Custom Fields fail preflight rather than being merged.
Multiple active memberships also fail before creation. A failed link or Sprint
write preserves the created Issue, writes the completed stage result to stdout,
and exits with `partial_failure` (`7`).

### Issue Type changes

`issue list-types` reads the current Issue Type and the compatible values from
Jira's Edit metadata. `issue update --type/-t` accepts an exact Issue Type ID
or a case-insensitive unique name from that list. The flag is exclusive: it
cannot be combined with summary, description, priority, assignment, Sprint,
custom-field, or other update flags in one request.

Affected older Jira Server releases can accept an incompatible Issue Type
through the generic Edit API and leave the Issue attached to an invalid
workflow state. jiro therefore fails closed unless the target appears in that
Issue's `editmeta.fields.issuetype.allowedValues`. An already-matching target is
reported as `unchanged` without a write. After a successful PUT, jiro reads the
Issue Type back; an unavailable readback is reported as an unknown partial
result and must be checked before retrying. This operation is a compatible
same-Issue edit, not Jira's multi-step UI Move flow.

### Sprint assignment

`issue add --sprint` creates the Issue before moving it into the Sprint. A
failed move does not delete the new Issue. `issue update --sprint` resolves
the Sprint before writing, updates ordinary fields, and then moves the Issue. A
failed Sprint move after an ordinary field update is reported as a partial
failure.

Write-time Sprint specs accept a numeric ID, `active`, or a case-insensitive
name substring. jiro uses the first match in Jira board and page order.
`--sprint-board` scopes that resolution to one Board selector: a positive
number selects that exact Board ID, other values select every Board whose
name contains the selector case-insensitively, in Jira order. It requires
`--sprint`; numeric Sprint IDs never trigger resolution.

### Transition comments and resolution

`issue move --comment` adds a non-empty inline comment through the same Jira
transition request. It accepts Jira Markup by default, or JFM through
`--input-format=jfm`; `--input-format` without `--comment` is invalid.
`--resolution` accepts a resolution name, a numeric Jira resolution ID, or
`none` to clear it. The transition, comment, resolution, and custom fields are
submitted atomically: Jira rejects the complete request when its workflow does
not accept one of them. jiro does not fall back to a separate comment request.

## Boards and Sprints

Boards and Sprints are read-only discovery resources.

`board list` fetches every accessible Board page. `sprint list` defaults to
`--state active`; `closed` and `future` select those Jira states, while `all`
omits the state filter. State values are case-insensitive and validated before
the Agile API is called.

Without `--board`, jiro queries every accessible Board; per-Board Sprint
requests run with bounded concurrency, and results always keep Jira Board
order. A positive numeric selector matches only that Board ID and is fetched
directly without listing every Board. Other selectors perform a
case-insensitive Board name substring match and query every match in Jira
order. Empty, zero, and negative selectors are invalid; an unmatched selector
returns `not_found`.

Sprint rows represent `(queried Board, Sprint)` relationships and are not
deduplicated. JSON preserves `boardId` and `boardName` for the queried Board,
plus Jira's `originBoardId`, `goal`, and raw date values. Text and TSV use
`ID`, `NAME`, `STATE`, `BOARD ID`, `BOARD NAME`, `START`, `END`, and
`COMPLETE` columns. No Board or Sprint pagination flags are exposed because
these commands always fetch every page.

If some Board Sprint requests fail, jiro continues with the remaining Boards.
When at least one Board request succeeds, stdout preserves the successful
Sprint rows and JSON adds `failedBoards`; stderr receives `partial_failure`
and the process exits `7`. If every selected Board fails, jiro returns the
ordinary classified Jira API error without an empty partial result.

## Bulk operations

Bulk operations select Issues with JQL. Exactly one of `--dry-run` and
`--yes` is required. `--dry-run` preflights every matching Issue;
`--yes` performs serial writes without prompting. Bulk commands page through
all matches by default.

`issue bulk move --resolution` uses the same name, numeric ID, and `none`
forms as single-Issue move. A dry-run proves transition availability and shows
the requested resolution, but Jira validates transition fields only during
`--yes` execution. A per-Issue validation error fails that item and processing
continues; systemic errors stop the remaining writes.

`issue bulk update` currently supports only `--type/-t`. It resolves the target
independently against each Issue's compatible values. Items already at the
target are `unchanged`; compatible dry-run items are `ready`; confirmed writes
are `succeeded`. A PUT followed by an unavailable readback is `unknown`, stops
the serial run, and marks remaining Issues `not_attempted`. Aggregate JSON keeps
`unchanged` and `unknown` separate from `succeeded` and `failed`.

## Issue list filters

Issue list filters are combined with `AND`. Repeated `--label`,
`--component`, and `--fix-version` values use JQL `IN`.
`--resolution unresolved` selects Issues without a resolution.
`--assignee me` and `--reporter me` use Jira's `currentUser()`.

For `issue list`, Sprint accepts `active` or `open`, `closed`, `future`,
an ID, or a name. An absolute `--created` or `--updated` date
(`YYYY-MM-DD` or `YYYY/MM/DD`) selects that whole day. Relative values such
as `-7d` and allowlisted Jira date functions such as `startOfWeek()` select
values on or after that operand.
