# Release workflow

Pushing a SemVer tag beginning with `v` runs `.github/workflows/release.yml`.
The tagged commit must be reachable from `main`; the workflow then calls the
same Ubuntu and macOS checks used by normal CI before any job receives write
permission.

The release publishes standalone `jiro` binaries for Linux, macOS, and Windows
on amd64 and arm64. It does not publish operating-system packages or archives.
Every release also includes one SHA-256 checksum file covering all six
binaries.

After GoReleaser OSS publishes the GitHub Release, a separate job updates
`timonwong/homebrew-tap` with the new stable Formula. Cross-repository writes use
an SSH deploy key scoped to the tap repository; its private key is stored in the
`HOMEBREW_TAP_PRIVATE_KEY` Actions secret. The tap commit uses `goreleaserbot`
as its author. Prereleases do not match the updater's stable SemVer contract.

## Local verification

Before pushing a release tag, run:

```sh
make check
go run github.com/goreleaser/goreleaser/v2@v2.17.1 check
GITHUB_TOKEN="$(gh auth token)" \
  go run github.com/goreleaser/goreleaser/v2@v2.17.1 release --snapshot --clean
```

Inspect the snapshot metadata and checksum file under `dist/`. A real release
must contain exactly six binaries plus the checksum file, and `jiro --version`
must report the tag version without the leading `v`.

The read-only release smoke passes an empty release-notes file so GoReleaser
does not call GitHub's write-gated generated-notes API. The final publish job
generates the GitHub-native release notes with `contents: write`.

Release assets are not created manually. If a release workflow fails, fix the
cause and create a new SemVer tag rather than moving or reusing a published tag.
