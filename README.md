<p align="center">
  <img src="assets/logo.svg" alt="jiro" width="410">
</p>

# jiro

`jiro` is a Jira CLI for humans, scripts, and AI agents. It is built for Jira
Data Center and Server, with readable text for interactive use and stable,
versioned contracts for automation.

## Features

- Work with Issues, comments, links, transitions, Sprints, Projects, and Custom
  Fields from one consistent CLI.
- Search with focused flags or JQL, and run guarded bulk operations with dry-run
  support and explicit confirmation.
- Read terminal-aware tables interactively or pipe complete, headerless TSV to
  other tools.
- Use normalized JSON, stable exit codes, and `jiro schema` for scripts and
  agents.
- Keep Basic Auth and PAT Credentials in Profile-scoped OS keyring entries, with
  read-only Profiles for safer automation.
- Convert Markdown descriptions and comments to Jira Markup while keeping Jira
  Markup as the explicit default.
- Drop down to `jiro api` for authenticated raw Jira REST requests when a typed
  command is not enough.

jiro is under initial development. The v1 compatibility target is Jira Data
Center and Server REST API v2. Jira Cloud REST API v3 and ADF are not part of
the compatibility contract.

## Install

[GitHub Releases](https://github.com/timonwong/jiro/releases) provides standalone
binaries for Linux, macOS, and Windows on amd64 and arm64. Download the asset
for your operating system and architecture. On Linux and macOS, make it
executable and optionally rename it:

```sh
chmod +x jiro_v0.1.0_darwin_arm64
mv jiro_v0.1.0_darwin_arm64 jiro
./jiro --version
```

Each release includes a SHA-256 checksum file. Verify the matching entry before
installing the binary. For example, on macOS:

```sh
grep 'jiro_v0.1.0_darwin_arm64$' jiro_v0.1.0_checksums.txt | shasum -a 256 -c -
```

On Linux, use `sha256sum -c -` instead of `shasum -a 256 -c -`.

## Quick start

Log in to the default Profile, verify the Credential, and list some Issues:

```sh
jiro auth login
jiro auth status
jiro issue list --project OPS --status "In Progress"
jiro issue show OPS-42
```

Use `--profile` when you need a named Profile:

```sh
jiro --profile bot auth login
jiro --profile bot auth status
```

## Authentication and profiles

The default config path is `$XDG_CONFIG_HOME/jiro/config.toml`, falling back to
`~/.config/jiro/config.toml`.

### Log in

`jiro auth login` prompts for connection settings and a fresh Credential. It
verifies the Credential with Jira before updating the Profile. A new Profile
must select `basic` or `pat`; jiro does not guess an authentication type.

```sh
jiro auth login
jiro --profile bot auth login
```

### Credential storage

Credentials are stored in the OS keyring by default, with one independent
keyring entry per Profile. To store the Credential in TOML, disable the keyring
explicitly:

```sh
jiro auth login --use-keyring=false
```

On Unix-like systems, jiro requires a config file containing `password` or
`token` to have mode `0600`. This applies to files written by
`auth login` and to manually maintained TOML.

When `use_keyring = true`, a missing keyring entry is an error. jiro does not
fall back to a plaintext secret in the config file.

### Configure profiles

You may maintain the config file manually:

```toml
[default]
host = "https://jira.example.com"
username = "timon"
auth_type = "basic"
api_version = 2
use_keyring = true
user_agent = "jiro-automation/1"

[profiles.bot]
host = "https://jira.example.com"
auth_type = "pat"
use_keyring = true
read_only = true
user_agent = "jiro-bot/1"
```

Select a Profile with `--profile bot` or `JIRO_PROFILE=bot`. `--config` and
`--profile` are the only global config-selection flags; connection and
authentication overrides use environment variables.

### Environment overrides

Supported environment variables include:

- `JIRA_HOST`, `JIRA_API_VERSION`, and `JIRA_USER_AGENT`
- `JIRA_TOKEN` for PAT, or the atomic `JIRA_USERNAME` and `JIRA_PASSWORD`
  pair for Basic Auth
- `JIRO_PROFILE`, `JIRO_CONFIG_FILE`, and `JIRO_CONFIG`
- `JIRO_READ_ONLY` and `JIRO_USE_KEYRING`
- `JIRO_FORCE_TTY` for terminal-style text tables through a pipe

A non-empty `JIRA_TOKEN` selects PAT and ignores the Basic Auth variables.
Without a token, setting either `JIRA_USERNAME` or `JIRA_PASSWORD` requires
both to be non-empty and selects Basic Auth.

If no environment Credential is present, jiro uses the selected Profile's
complete Credential. It never combines environment and persisted Credential
halves. `JIRA_HOST` is independent, so it can override the Jira Instance while
the selected Profile supplies its Credential.

`JIRA_USER_AGENT` overrides the selected Profile's `user_agent`, which
inherits from `[default]` when omitted. The built-in fallback is
`jiro/<version>`, or `jiro/dev` for development builds.

The legacy `JIRO_HOST`, `JIRO_API_VERSION`, `JIRO_TOKEN`,
`JIRO_USERNAME`, `JIRO_PASSWORD`, and `JIRO_AUTH_TYPE` variables are
ignored.

### Non-interactive login

Provide a complete environment Credential or use an explicit stdin mode.
`--password-stdin` requires `JIRA_USERNAME`. `--token-stdin` selects PAT
and ignores the username.

The two stdin modes are mutually exclusive and conflict with a non-empty
`JIRA_TOKEN` or `JIRA_PASSWORD`. Each mode reads to EOF, removes exactly one
trailing LF and an immediately preceding CR, and rejects only an empty result.
Secrets are never accepted as command-line arguments.

```sh
printf '%s' "$JIRA_PAT" | JIRA_HOST=https://jira.example.com \
  jiro --profile bot auth login --token-stdin

printf '%s' "$JIRA_PASSWORD" | JIRA_HOST=https://jira.example.com \
  JIRA_USERNAME=timon jiro auth login --password-stdin
```

### Check or remove a Credential

`auth status` verifies the effective Profile and Credential with
`/rest/api/2/myself`:

```sh
jiro auth status
jiro --profile bot auth status
```

It exits successfully only when Jira accepts the Credential. Its normalized
output includes the Profile, Jira Instance, authentication type, and Principal
without exposing the secret or its storage source.

`auth logout` removes a Profile's persisted Credential without deleting its
non-secret configuration:

```sh
jiro auth logout
jiro --profile bot auth logout
```

`auth logout` cannot unset `JIRA_PASSWORD` or `JIRA_TOKEN` inherited from
the shell. It reports when one of those environment Credentials remains active.

## Common workflows

Resource namespaces use singular canonical names without aliases:

```text
jiro issue
jiro board
jiro sprint
jiro project
jiro field
```

### Find issues

```sh
jiro issue list --project OPS --status "In Progress"
jiro issue list --resolution unresolved --reporter me --label agent --component API
jiro issue list --sprint active --created -7d --updated 'startOfWeek()'
jiro issue show OPS-42
jiro search 'project = OPS AND assignee = currentUser() ORDER BY updated DESC'
```

Issue list filters are combined with `AND`. Repeated `--label`,
`--component`, and `--fix-version` values use JQL `IN`.
`--resolution unresolved` selects Issues without a resolution.
`--assignee me` and `--reporter me` use Jira's `currentUser()`.

For `issue list`, Sprint accepts `active` or `open`, `closed`, `future`,
an ID, or a name. An absolute `--created` or `--updated` date
(`YYYY-MM-DD` or `YYYY/MM/DD`) selects that whole day. Relative values such
as `-7d` and allowlisted Jira date functions such as `startOfWeek()` select
values on or after that operand.

### Create and update issues

```sh
jiro issue add --project OPS --type Bug --summary "Broken deployment" \
  --parent OPS-10 --component API --fix-version 4.5 --sprint active
jiro issue update OPS-42 --priority High --component API --fix-version 4.5
jiro issue update OPS-42 --component none --sprint active
jiro issue list-types OPS-42
jiro issue update OPS-42 --type Story
jiro issue comment add OPS-42 --body "Deployed to staging."
jiro issue move OPS-42 --to Done --comment "**Verified** in staging." \
  --input-format=jfm --resolution Fixed
jiro issue assign OPS-42 --assignee me
```

`issue update --component` and `--fix-version` replace the complete field.
A single `none` clears it.

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

`issue add --sprint` creates the Issue before moving it into the Sprint. A
failed move does not delete the new Issue. `issue update --sprint` resolves
the Sprint before writing, updates ordinary fields, and then moves the Issue. A
failed Sprint move after an ordinary field update is reported as a partial
failure.

Write-time Sprint specs accept a numeric ID, `active`, or a case-insensitive
name substring. jiro uses the first match in Jira board and page order.

`issue move --comment` adds a non-empty inline comment through the same Jira
transition request. It accepts Jira Markup by default, or JFM through
`--input-format=jfm`; `--input-format` without `--comment` is invalid.
`--resolution` accepts a resolution name, a numeric Jira resolution ID, or
`none` to clear it. The transition, comment, resolution, and custom fields are
submitted atomically: Jira rejects the complete request when its workflow does
not accept one of them. jiro does not fall back to a separate comment request.

### Discover Boards and Sprints

Boards and Sprints are read-only discovery resources:

```sh
jiro board list
jiro sprint list
jiro sprint list --board 12
jiro sprint list --board platform --state future
jiro sprint list --state all --output=json
```

`board list` fetches every accessible Board page. `sprint list` defaults to
`--state active`; `closed` and `future` select those Jira states, while `all`
omits the state filter. State values are case-insensitive and validated before
the Agile API is called.

Without `--board`, jiro queries every accessible Board serially. A positive
numeric selector matches only that Board ID. Other selectors perform a
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

### Issue Links and metadata

```sh
jiro issue link add OPS-42 --to OPS-99 --type Blocks
jiro issue link list OPS-42
jiro issue link delete 10001
jiro issue link types

jiro issue list-types OPS-42

jiro project list
jiro field list --custom
jiro cache refresh
```

### Bulk operations

Bulk operations select Issues with JQL. Exactly one of `--dry-run` and
`--yes` is required. `--dry-run` preflights every matching Issue;
`--yes` performs serial writes without prompting. Bulk commands page through
all matches by default.

```sh
jiro issue bulk move --jql 'project = OPS AND status = Open' --to Done \
  --resolution Fixed --dry-run
jiro issue bulk assign --jql 'project = OPS' --assignee me --yes
jiro issue bulk update --jql 'project = OPS' --type Story --dry-run
```

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

## Automation and output

Text is the default output format. Normalized JSON is the automation contract,
and `jiro schema` describes that contract for agents and other tooling.

### Terminal and piped text

Commands with tabular text output adapt to stdout:

- A terminal receives a column-aligned table sized to the available width.
  Fixed Issue Key, ID, Alias, and similar columns remain complete. Descriptive
  columns may be truncated with `...`.
- A pipe or file receives headerless TSV with untruncated, single-line values.
  An empty result writes zero bytes.

Table cells are rendered as safe single-line text. JSON retains the original
values.

`jiro issue show` is the detail-view exception. Text output uses man-page-style
sections with uppercase field headers and four-space-indented values. TTY
headers are bold; piped or redirected headers contain no ANSI styling. Values
retain physical line breaks and are never wrapped or truncated by jiro. Empty
fields remain visible. JSON output is unchanged.

Set `JIRO_FORCE_TTY` to `1`, `true`, `yes`, or `on` to keep
terminal-style output in a pipeline:

```sh
JIRO_FORCE_TTY=1 jiro issue list --project OPS | less
```

The exact values `0`, `false`, `no`, and `off` leave normal terminal
detection in place. Any other value is a configuration error for text output.
When forced, jiro reads the controlling terminal width and falls back to 80
columns if the width is unavailable.

### JSON contract

The short and long output flags accept attached or separate values:

```sh
jiro issue list -o json
jiro issue list -ojson
jiro issue list --output json
jiro issue list --output=json
```

Normalized JSON uses a stable envelope:

```json
{"schemaVersion":"1","data":{"issues":[],"total":0,"startAt":0,"maxResults":50}}
```

Successful commands can include structured warnings without changing the exit
code:

```json
{"schemaVersion":"1","data":{"key":"OPS-42","updated":true},"warnings":[{"code":"stale_field_cache","message":"using stale custom field metadata","details":{"fetchedAt":"2026-07-29T10:00:00Z"}}]}
```

Successful data is written to stdout. Structured JSON failures are written to
stderr with stable exit codes, while human-readable warnings use stderr.

For a partial result with exit code `7`, jiro writes the complete normalized
result to stdout before writing a structured `partial_failure` error to
stderr. This includes a successful Issue creation whose Sprint move failed and
Issue Type writes whose readback is unknown, plus bulk runs with failed,
unknown, or unattempted items.
Cross-Board `sprint list` queries use the same contract: successful Sprint
relationships remain on stdout and JSON identifies failed Boards.

`jiro schema` describes automation-facing commands, flags, mutation status,
output shapes, and exit codes as machine-readable JSON. Shell completion emits
shell code and is outside that contract.

## Advanced usage

### Custom fields

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
Metadata Snapshot. The snapshot is scoped to the normalized Jira base URL and
the Principal returned by `/myself`. Missing or ambiguous aliases are errors.
Values are decoded as JSON first and fall back to strings.
System fields are not accepted through typed-command `--field`; use their
dedicated flags, such as `--resolution`, instead. The raw `jiro api --field`
request builder is a separate contract and is not subject to this restriction.

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
continues with the stale mapping, including for Issue mutations, and emits a
warning. `jiro field list --custom` uses the same snapshot; the complete
`field list` remains live.

### Jira Markup and Jiro Flavored Markdown

Descriptions and comments use Jira Markup by default. Request Jiro Flavored
Markdown (JFM) conversion explicitly:

```sh
jiro issue add \
  --project OPS \
  --type Task \
  --summary "Document rollout" \
  --description-file issue.md \
  --input-format=jfm
```

`markdown` is a permanent, warning-free alias for the canonical `jfm` value.
The bidirectional format and its exact conversion behavior are specified in
[`docs/jiro-flavored-markdown.md`](docs/jiro-flavored-markdown.md).

Issue descriptions and `issue comment add` accept inline input, a file path,
or `-` for stdin; inline and file forms are mutually exclusive. The atomic
transition comment on `issue move --comment` is inline-only.

Convert documents locally without loading Jira configuration or credentials:

```sh
jiro jfm to-jira description.md
jiro jfm from-jira description.jira
cat description.md | jiro jfm to-jira
```

Each command accepts at most one file path. With no path or with `-`, it reads
stdin. Text mode writes only the exact converted document to stdout; conversion
warnings go to stderr. `--output=json` uses the normal versioned envelope with
`jiraMarkup` or `jfm` data and structured warnings.

### Raw Jira API

`jiro api` sends one authenticated HTTP request to a relative endpoint of the
effective Jira Instance. It reuses the selected Profile, Credential,
authentication type, connection overrides, and configured context path such as
`/jira`. It does not call `/rest/api/2/myself` first or add a
`/rest/api/2/` prefix.

```sh
jiro api rest/api/2/myself
jiro api 'rest/api/2/search?jql=project%20%3D%20OPS'
jiro api rest/api/2/issue -F 'fields={"project":{"key":"OPS"},"summary":"Example","issuetype":{"name":"Task"}}'
jiro api rest/api/2/issue/OPS-42 --method PATCH --input update.json
jiro api rest/api/2/issue/OPS-42/attachments \
  --form file=@artifact.zip \
  -H 'X-Atlassian-Token: no-check'
```

#### Request target and method

The endpoint may start with `/` but must remain relative to the Jira Instance.
Absolute and scheme-relative URLs are rejected. Other URL syntax, including dot
segments, fragments, backslashes, controls, and encoding depth, is not
prevalidated. URL construction and Go's HTTP stack report any resulting error.

Supplying fields or a body changes the implicit method from GET to POST. An
explicit method is trimmed, uppercased, and passed to Go without an allowlist.
A `read_only` Profile permits only GET, HEAD, and OPTIONS.
`--input` and `--form` may still provide bodies for those explicit methods.

#### Request bodies

- `--input FILE|-` streams bytes unchanged. With `--input`, fields are
  appended to the query string.
- `-f, --string-field key=value` always creates a JSON string.
- `-F, --field key=value` decodes a complete JSON value and otherwise uses a
  string. `@file` and `@-` read the value from a file or stdin. Fields form
  a top-level JSON object and use command-line last-wins order for duplicate
  body keys. GET, HEAD, and OPTIONS place fields in the query string instead.
- `--form key=value` creates multipart form data. `@file` and `@-` create
  file parts, while `@@text` sends the text `@text`.

Form mode is mutually exclusive with the other body modes.

#### Headers and responses

Use repeatable `-H, --header 'Name: value'` flags for request headers.
Authorization, Proxy-Authorization, Host, Content-Length, and User-Agent are
managed by jiro. Content-Type is also managed in form mode.

Other header names and values are passed to Go's HTTP stack without
prevalidation. Repeated values for one name retain command-line order. An empty
value deletes all earlier values, and later values may add the header again.
Defaults are `Accept: application/json` and `Content-Type: application/json`
when a non-form body exists.

Go's standard compression behavior applies. When the Transport adds gzip, it
decompresses the response transparently. Supplying `Accept-Encoding`
explicitly leaves the response bytes to the caller.

Responses are Jira-owned bytes. Any 2xx body is streamed unchanged to stdout.
A non-2xx body is streamed unchanged to stderr and returns the normal
status-based exit code.

`--include` prepends the protocol, status, and sorted response headers visible
after Go's normal response processing. `Set-Cookie` values are not redacted.
Global `--quiet` suppresses only a successful body, and global `--output` is
unsupported for `api`.

The API command does not provide `jq` or template formatting, pagination,
caching, application-level retry, verbose tracing, or an output-file flag.

#### Transport

The default timeout is 30 seconds; `--timeout 0` disables it. Redirects are
never followed, so every 3xx response is returned as a raw non-2xx API error.
API Requests use HTTP/1.1.

`--insecure` disables TLS certificate and hostname verification for the
current invocation. Reserve it for a trusted Jira Instance with a known
certificate problem. It is a silent no-op for HTTP Instances.

### Shell completion

jiro uses Cobra's native completion support for Bash, Zsh, Fish, and
PowerShell. Generate and load a script for the current session with:

```sh
# Bash
source <(jiro completion bash)

# Zsh (requires compinit)
source <(jiro completion zsh)

# Fish
jiro completion fish | source

# PowerShell
jiro completion powershell | Out-String | Invoke-Expression
```

For normal use, generate the script once in the shell's completion directory
instead of running jiro during every shell startup:

```sh
mkdir -p ~/.zsh/completions
jiro completion zsh > ~/.zsh/completions/_jiro
# Add ~/.zsh/completions to fpath before running compinit in ~/.zshrc.

mkdir -p ~/.config/fish/completions
jiro completion fish > ~/.config/fish/completions/jiro.fish
```

Completion covers commands, flags, enum values, local input paths, and named
Profiles from the selected config file. It does not contact Jira or read
Credentials from the OS keyring.

## Development

```sh
make build
./bin/jiro --help

make fmt
make check
```

The domain glossary is in [`CONTEXT.md`](CONTEXT.md). Durable design decisions
are recorded in [`docs/adr`](docs/adr).

## License

MIT
