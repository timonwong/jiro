# Authentication and profiles

Use this branch for authentication, Profile management, or uncertainty about the effective Jira Instance or Credential.

## Select and verify

Pass a named Profile explicitly when the task identifies one, and carry that `--profile` through every later Jira command and readback:

```bash
jiro --profile bot auth status --output=json
```

Run `jiro auth status --output=json` before Jira work when authentication or the selected Jira Instance is uncertain. Success proves that the effective Profile and Credential are accepted without exposing the secret.

## Log in

Authenticate interactively for the default or a named Profile:

```bash
jiro auth login
jiro --profile bot auth login
```

For non-interactive login, use the atomic `JIRA_*` Credential contract or an explicit stdin mode. A non-empty `JIRA_TOKEN` selects PAT; otherwise `JIRA_USERNAME` and `JIRA_PASSWORD` must both be non-empty. `--password-stdin` requires `JIRA_USERNAME`; `--token-stdin` selects PAT. Keep credentials out of command-line arguments:

```bash
printf '%s' "$JIRA_PAT" | JIRA_HOST=https://jira.example.com \
  jiro --profile bot auth login --token-stdin --output=json
```

Use the OS keyring as the default credential store; each Profile owns an independent credential. Use `auth login --use-keyring=false` only when plaintext TOML storage is explicitly intended.

Complete login only after `auth status` succeeds for the intended Profile and Jira Instance.

## Log out

Remove only the selected Profile's persisted Credential:

```bash
jiro --profile bot auth logout --output=json
```

Read back `environmentCredentialActive` because logout cannot remove a Credential inherited from the shell. Complete logout only when the persisted Credential is removed and any remaining environment-sourced authentication is understood.
