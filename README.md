<p align="center">
  <img src="assets/logo.svg" alt="jiro" width="410">
</p>

# jiro

`jiro` is a Jira CLI for humans, scripts, and AI agents. It is built for Jira
Data Center and Server, with readable text for interactive use and stable,
versioned contracts for automation.

## Features

- Manage Issues, comments, links, transitions, Sprints, and Projects from one
  CLI.
- Search with flags or JQL. Run bulk operations with dry-run and confirmation
  guards.
- Interactive tables in the terminal, headerless TSV through a pipe, structured
  JSON for scripts and agents.
- Store credentials in the OS keyring with per-Profile isolation and read-only
  Profiles for safer automation.
- Convert Markdown to Jira Markup on the fly, or use `jiro jfm` for standalone
  conversion.
- Use `jiro api` for authenticated raw Jira REST requests when a typed command
  is not enough.

jiro is under initial development. The v1 compatibility target is Jira Data
Center and Server REST API v2. Jira Cloud REST API v3 and ADF are not part of
the compatibility contract.

## Installation

### Homebrew

On macOS or Linux:

```sh
brew install timonwong/tap/jiro
```

### GitHub Release

Download a pre-built binary from
[GitHub Releases](https://github.com/timonwong/jiro/releases) (Linux, macOS,
Windows; amd64 and arm64). On Linux and macOS:

```sh
chmod +x jiro_v0.1.0_darwin_arm64
sudo mv jiro_v0.1.0_darwin_arm64 /usr/local/bin/jiro
jiro --version
```

Replace `v0.1.0` and `darwin_arm64` with your release version and platform.
Each release includes a SHA-256 checksum file for verification.

### Docker

Run the latest container image from GitHub Container Registry:

```sh
docker run -it --rm ghcr.io/timonwong/jiro:latest
```

For Jira credentials, use environment variables or mount a configuration with
keyring storage disabled. See [Authentication and Profiles](docs/authentication.md).

### Manual

Install the latest version from source with Go 1.26 or later:

```sh
go install github.com/timonwong/jiro/cmd/jiro@latest
```

Source installations report the development version rather than a stamped
release version.

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

## Examples

Resource namespaces use singular canonical names: `issue`, `board`, `sprint`,
`project`, `field`.

### Find issues

```sh
jiro issue list --project OPS --status "In Progress"
jiro issue list --sprint active --created -7d
jiro issue show OPS-42
jiro search 'project = OPS AND assignee = currentUser() ORDER BY updated DESC'
```

Filters are combined with `AND`. `--assignee me` and `--reporter me` use
Jira's `currentUser()`. Sprint accepts `active`, `closed`, `future`, an ID,
or a name.

### Create and update issues

```sh
jiro issue add --project OPS --type Bug --summary "Broken deployment" \
  --parent OPS-10 --component API --fix-version 4.5 --sprint active
jiro issue update OPS-42 --priority High --component API
jiro issue comment add OPS-42 --body "Deployed to staging."
jiro issue comment edit OPS-42 10001 --body "Deployment verified."
jiro issue move OPS-42 --to Done --resolution Fixed
jiro issue assign OPS-42 --assignee me
```

`--component` and `--fix-version` replace the complete field; a single `none`
clears it.

### Boards and Sprints

```sh
jiro board list
jiro sprint list
jiro sprint list --board 12 --state future
jiro sprint list --state all --output=json
```

### Issue Links

```sh
jiro issue link add OPS-42 --to OPS-99 --type Blocks
jiro issue link list OPS-42
jiro issue link delete 10001
jiro issue link types
```

### Bulk operations

Bulk operations select Issues with JQL. Exactly one of `--dry-run` and `--yes`
is required:

```sh
jiro issue bulk move --jql 'project = OPS AND status = Open' --to Done \
  --resolution Fixed --dry-run
jiro issue bulk assign --jql 'project = OPS' --assignee me --yes
jiro issue bulk update --jql 'project = OPS' --type Story --dry-run
```

### Jira Markup and JFM

Descriptions and comment bodies use Jira Markup by default. Use `--input-format=jfm`
to convert from [Jiro Flavored Markdown](docs/jiro-flavored-markdown.md):

```sh
jiro issue add --project OPS --type Task --summary "Document rollout" \
  --description-file issue.md --input-format=jfm
```

Convert documents locally without Jira credentials:

```sh
jiro jfm to-jira description.md
jiro jfm from-jira description.jira
```

### Custom fields

```sh
jiro issue add --project OPS --type Story --summary "Agent-friendly output" \
  --field story-points=5
```

See [Field Selectors and Custom Fields](docs/fields.md) for alias resolution,
`--fields` read selectors, and Sprint Memberships.

### Raw API

When a typed command is not enough, `jiro api` sends one authenticated request
to a Jira REST endpoint:

```sh
jiro api rest/api/2/myself
jiro api rest/api/2/issue -F 'fields={"project":{"key":"OPS"},"summary":"Example","issuetype":{"name":"Task"}}'
```

See [Raw Jira API](docs/api.md) for request bodies, headers, form uploads, and
transport options.

## Documentation

| Topic | Link |
|---|---|
| Authentication, Profiles, and environment variables | [docs/authentication.md](docs/authentication.md) |
| Issue operations, bulk ops, and Sprint assignment | [docs/issues.md](docs/issues.md) |
| Output formats and JSON contract | [docs/output.md](docs/output.md) |
| Field Selectors and Custom Fields | [docs/fields.md](docs/fields.md) |
| Jiro Flavored Markdown specification | [docs/jiro-flavored-markdown.md](docs/jiro-flavored-markdown.md) |
| Raw Jira API | [docs/api.md](docs/api.md) |
| Shell completion | [docs/shell-completion.md](docs/shell-completion.md) |
| Domain glossary | [CONTEXT.md](CONTEXT.md) |
| Architecture Decision Records | [docs/adr](docs/adr) |

## Development

```sh
make build
./bin/jiro --help

make fmt
make check
```

## License

MIT
