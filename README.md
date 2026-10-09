<p align="center">
  <img src="assets/logo.svg" alt="jiro" width="410">
</p>

# jiro

A Jira CLI for humans, scripts, and AI agents: readable tables in the
terminal, stable JSON for automation.

> [!NOTE]
> jiro targets **Jira Data Center and Server** (REST API v2). Jira Cloud
> (REST API v3, ADF) is not supported. jiro is under initial development.

## Usage

```sh
jiro auth login                                   # one-time setup
jiro issue list --project OPS --status "In Progress"
jiro issue show OPS-42
jiro issue move OPS-42 --to Done --resolution Fixed
jiro issue list --assignee me -o json             # stable JSON envelope
```

Markdown conversion works offline, without Jira credentials:

```console
$ cat rollout.md
## Rollout

- **Deploy** `api` to [staging](https://example.com)
- Verify OPS-42

$ jiro jfm to-jira rollout.md
h2. Rollout

* *Deploy* {{api}} to [staging|https://example.com]
* Verify OPS-42
```

What you get:

- Issues, comments, links, transitions, clones, Boards, Sprints, and Projects
  in one CLI.
- Search with flags or JQL; bulk operations guarded by `--dry-run` / `--yes`.
- Column-aligned tables in a terminal, headerless TSV through a pipe,
  versioned JSON with stable exit codes for scripts and agents.
- Credentials in the OS keyring, isolated per Profile, with read-only Profiles
  for safer automation.
- Markdown in, Jira Markup out: write descriptions and comments in
  [Jiro Flavored Markdown](docs/jiro-flavored-markdown.md).
- `jiro api` for authenticated raw REST requests when a typed command is not
  enough.

## Install

| Method | Command |
|---|---|
| Homebrew (macOS, Linux) | `brew install timonwong/tap/jiro` |
| Container image | `docker run --rm ghcr.io/timonwong/jiro:latest --help` |
| From source (Go 1.26+) | `go install github.com/timonwong/jiro/cmd/jiro@latest` |

Pre-built binaries for Linux, macOS, and Windows (amd64, arm64) are on
[GitHub Releases](https://github.com/timonwong/jiro/releases), each with a
SHA-256 checksum file. On Linux and macOS:

```sh
chmod +x jiro_v0.1.0_darwin_arm64        # use your version and platform
sudo mv jiro_v0.1.0_darwin_arm64 /usr/local/bin/jiro
jiro --version
```

Source builds report a development version instead of a release version.

## Authentication

`jiro auth login` prompts for the Jira URL and a Basic or PAT Credential,
verifies it, and stores it in the OS keyring:

```sh
jiro auth login
jiro auth status
jiro --profile bot auth login     # a separate, named Profile
```

In CI or containers, skip the keyring and pass the Credential through the
environment:

```sh
docker run --rm -e JIRA_HOST -e JIRA_TOKEN \
  ghcr.io/timonwong/jiro:latest issue show OPS-42
```

See [Authentication and Profiles](docs/authentication.md) for the config file,
all environment variables, read-only Profiles, and non-interactive login.

## Examples

### Find issues

```sh
jiro issue list --sprint active --created -7d
jiro search 'project = OPS AND assignee = currentUser() ORDER BY updated DESC'
```

Filters are combined with `AND`. `--assignee me` and `--reporter me` map to
`currentUser()`. `--sprint` accepts `active`, `closed`, `future`, an ID, or a
name.

### Create and update issues

```sh
jiro issue add --project OPS --type Bug --summary "Broken deployment" \
  --parent OPS-10 --component API --fix-version 4.5 --sprint active
jiro issue update OPS-42 --priority High --component API
jiro issue clone OPS-42
jiro issue assign OPS-42 --assignee me
jiro issue comment add OPS-42 --body "Deployed to staging."
jiro issue comment edit OPS-42 10001 --body "Deployment verified."
```

`--component` and `--fix-version` replace the whole field; a single `none`
clears it.

### Markdown descriptions

Descriptions and comments are Jira Markup by default. Add
`--input-format=jfm` to write them in Markdown:

```sh
jiro issue add --project OPS --type Task --summary "Document rollout" \
  --description-file rollout.md --input-format=jfm
jiro jfm from-jira description.jira    # the reverse direction, offline
```

### Boards, Sprints, and links

```sh
jiro board list
jiro sprint list --board 12 --state future
jiro issue link add OPS-42 --to OPS-99 --type Blocks
jiro issue link list OPS-42
jiro issue link types
```

### Bulk operations

Bulk commands select Issues with JQL and require exactly one of `--dry-run`
or `--yes`:

```sh
jiro issue bulk move --jql 'project = OPS AND status = Open' --to Done \
  --resolution Fixed --dry-run
jiro issue bulk assign --jql 'project = OPS' --assignee me --yes
```

### Custom fields

```sh
jiro issue add --project OPS --type Story --summary "Agent-friendly output" \
  --field story-points=5
```

Field aliases are resolved from Jira metadata. See
[Field Selectors and Custom Fields](docs/fields.md).

### Raw API

```sh
jiro api rest/api/2/myself
jiro api rest/api/2/issue -F 'fields={"project":{"key":"OPS"},"summary":"Example","issuetype":{"name":"Task"}}'
```

See [Raw Jira API](docs/api.md) for bodies, headers, and uploads.

## Automation and AI agents

- `-o json` wraps every result in a versioned envelope
  (`{"schemaVersion":"1","data":…}`). Errors go to stderr as JSON with stable
  exit codes; partial failures still print the completed results. See
  [Output and Automation](docs/output.md).
- `jiro schema` prints the command, flag, output, and exit-code contract as
  JSON for tools to consume.
- [`skills/jiro`](skills/jiro) is an agent skill that teaches coding agents
  the safe mutation workflow (inspect, preflight, mutate, read back). Install
  it with:

  ```sh
  npx skills add timonwong/jiro
  ```

## Documentation

| Topic | Link |
|---|---|
| Authentication, Profiles, and environment variables | [docs/authentication.md](docs/authentication.md) |
| Issue operations, bulk operations, and Sprint assignment | [docs/issues.md](docs/issues.md) |
| Output formats and JSON contract | [docs/output.md](docs/output.md) |
| Field Selectors and Custom Fields | [docs/fields.md](docs/fields.md) |
| Jiro Flavored Markdown specification | [docs/jiro-flavored-markdown.md](docs/jiro-flavored-markdown.md) |
| Raw Jira API | [docs/api.md](docs/api.md) |
| Shell completion | [docs/shell-completion.md](docs/shell-completion.md) |
| Domain glossary | [CONTEXT.md](CONTEXT.md) |
| Architecture Decision Records | [docs/adr](docs/adr) |

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

[MIT](LICENSE)
