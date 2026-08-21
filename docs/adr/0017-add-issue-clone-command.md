# Add an Issue Clone command

`jiro issue clone SOURCE` creates an Issue Clone through Jira Data Center/Server
REST API v2. The command keeps the source Project, Issue Type, and Parent. It
copies the source Description, Priority, Assignee, Labels, Components, Fix
Versions, and Custom Fields that are available on the target Create screen.
The Summary defaults to `CLONE - <source summary>` and explicit flags replace
copied values. Explicit `--field` values override copied Custom Fields.

Custom Fields absent from the target Create screen are skipped when their source
value is non-empty and are reported through a structured warning. Sprint is
excluded from generic Custom Field replay. A single valid active Sprint
Membership is added after creation; zero active memberships are ignored and
multiple active memberships fail before creation. Comments, Attachments,
Subtasks, historical Sprint Memberships, and unrelated Issue Links are not
copied.

By default the source Issue is linked outward to the new Issue with the
`Cloners` Link Type (`clones` / `is cloned by`). `--no-link` disables that step.
The command follows the existing `partial_failure` contract when a post-create
link or Sprint write fails: the new Issue is preserved, the complete stage
result is rendered, and the process exits with code 7.

## Considered options

- Cross-Project and cross-Issue-Type cloning was rejected. Native Jira cloning
  retains both; moving a clone to another Project is a separate operation.
- Replaying every source field was rejected. Custom Field GET and POST values
  are not generally interchangeable, and read-only fields must not be sent to
  Create Issue. Create-screen metadata is the copy boundary.
- Raw Sprint Custom Field replay was rejected. Sprint Memberships are normalized
  and written through the Agile API, consistent with other jiro mutations.
- A public REST clone endpoint was not assumed. The implementation therefore
  treats Create Issue, clone-link, and Sprint assignment as separate operations;
  only the first can create the Issue atomically.
