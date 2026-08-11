# REST API fallback

Use `jiro api` as a bounded escape hatch only after the current schema and relevant typed-command help prove that jiro lacks the required operation. State that this branch returns Jira's raw, version-dependent response rather than jiro's normalized contract.

## Establish the request

1. Run `jiro api --help` and inspect the current `api` command entry in `jiro schema --output=json` before constructing the request.
2. Identify the installed Jira product and version, then consult the matching authoritative [Jira Data Center REST API documentation](https://developer.atlassian.com/server/jira/platform/about-the-jira-server-rest-apis/). Use the Jira Software API documentation for software-specific resources.
3. Reuse the selected Profile. If the task needs to supply, change, or diagnose Credentials, follow [Authentication and profiles](authentication.md). `JIRA_HOST` may independently override the Jira Instance.
4. Use `jiro api` so credentials are read at execution time and Authorization remains managed. Keep xtrace and verbose header logging disabled, and keep Authorization values out of command arguments and output.
5. Use the configured HTTPS Jira base URL, including any context path, with normal certificate verification. Send PATs as Bearer credentials and Basic credentials according to Atlassian's [PAT](https://developer.atlassian.com/server/jira/platform/personal-access-token/) and [Basic Auth](https://developer.atlassian.com/server/jira/platform/basic-authentication/) guidance.
6. Query endpoint and field metadata before a write, send the smallest payload that satisfies the request, and read the changed resource back through REST afterward.

Treat the response status and body as evidence, not as a stable jiro envelope. Stop and report the verified boundary when the endpoint, authentication path, required metadata, or final state cannot be established.

Keep Credentials in their provided environment source; leave jiro's TOML and OS keyring untouched. Keep the workflow jiro-first and REST-second rather than switching to another Jira CLI or browser UI.

Complete this branch only when the requested Jira state is visible in a REST readback or the verified boundary has been reported.
