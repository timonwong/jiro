# Raw Jira API

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

## Request target and method

The endpoint may start with `/` but must remain relative to the Jira Instance.
Absolute and scheme-relative URLs are rejected. Other URL syntax, including dot
segments, fragments, backslashes, controls, and encoding depth, is not
prevalidated. URL construction and Go's HTTP stack report any resulting error.

Supplying fields or a body changes the implicit method from GET to POST. An
explicit method is trimmed, uppercased, and passed to Go without an allowlist.
A `read_only` Profile permits only GET, HEAD, and OPTIONS.
`--input` and `--form` may still provide bodies for those explicit methods.

## Request bodies

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

## Headers and responses

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

## Transport

The default timeout is 30 seconds; `--timeout 0` disables it. Redirects are
never followed, so every 3xx response is returned as a raw non-2xx API error.
API Requests use HTTP/1.1.

`--insecure` disables TLS certificate and hostname verification for the
current invocation. Reserve it for a trusted Jira Instance with a known
certificate problem. It is a silent no-op for HTTP Instances.
