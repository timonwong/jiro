# Authentication and Profiles

The default config path is `$XDG_CONFIG_HOME/jiro/config.toml`, falling back to
`~/.config/jiro/config.toml`.

## Log in

`jiro auth login` prompts for connection settings and a fresh Credential. It
verifies the Credential with Jira before updating the Profile. A new Profile
must select `basic` or `pat`; jiro does not guess an authentication type.

```sh
jiro auth login
jiro --profile bot auth login
```

## Credential storage

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

## Configure profiles

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

## Environment overrides

| Variable | Effect |
|---|---|
| `JIRA_HOST` | Override Jira instance URL |
| `JIRA_API_VERSION` | Override REST API version |
| `JIRA_USER_AGENT` | Override `user_agent` from the selected Profile (inherits from `[default]` when omitted; built-in fallback is `jiro/<version>`) |
| `JIRA_TOKEN` | PAT credential; takes priority over the Basic Auth variables |
| `JIRA_USERNAME` + `JIRA_PASSWORD` | Basic Auth credential; both must be non-empty |
| `JIRO_PROFILE` | Select a named Profile |
| `JIRO_CONFIG_FILE` | Override the config file path |
| `JIRO_CONFIG` | Override the config directory |
| `JIRO_READ_ONLY` | Force read-only mode |
| `JIRO_USE_KEYRING` | Override keyring usage |
| `JIRO_FORCE_TTY` | Keep terminal-style text tables through a pipe |

If no environment Credential is present, jiro uses the selected Profile's
complete Credential. It never combines environment and persisted Credential
halves. `JIRA_HOST` is independent, so it can override the Jira Instance while
the selected Profile supplies its Credential.

The legacy `JIRO_HOST`, `JIRO_API_VERSION`, `JIRO_TOKEN`,
`JIRO_USERNAME`, `JIRO_PASSWORD`, and `JIRO_AUTH_TYPE` variables are
ignored.

## Non-interactive login

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

## Check or remove a Credential

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
