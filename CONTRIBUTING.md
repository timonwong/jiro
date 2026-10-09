# Contributing

## Development

Requires Go 1.26 or later.

```sh
make build        # builds bin/jiro
./bin/jiro --help

make fmt          # gofmt all Go files
make check        # fmt-check, vet, race tests, build
```

## Conventions

- Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/);
  they drive versioning and the changelog. See
  [docs/agents/release.md](docs/agents/release.md).
- Domain terms are defined in [CONTEXT.md](CONTEXT.md); significant design
  decisions are recorded in [docs/adr](docs/adr).
- Golden testdata conventions are in
  [docs/agents/testing.md](docs/agents/testing.md).
- Issues are tracked in GitHub Issues; see
  [docs/agents/issue-tracker.md](docs/agents/issue-tracker.md) and
  [docs/agents/triage-labels.md](docs/agents/triage-labels.md).
