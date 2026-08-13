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

The release workflow also publishes a multi-platform container image to
`ghcr.io/timonwong/jiro`. Stable tags publish both `vX.Y.Z` and `latest`;
prereleases publish only their exact tag. The image supports Linux on amd64 and
arm64, runs as UID/GID 65532, and retains the Wolfi base shell. Only the
container-publishing job receives `packages: write` permission.

Container images are built and inspected as disposable artifacts during normal
CI. Only a tagged release pushes an image to GHCR. The release build emits OCI
source, revision, version, and license labels together with provenance and an
SBOM. If container publication fails after the GitHub Release exists, rerun the
failed job; do not move or reuse the tag.

GitHub Packages creates the container package as private on its first publish.
After the first successful container release, a maintainer must make the
`jiro` package public once in its GitHub Package settings so the documented
anonymous pull works. Package visibility is not changed by the release job.

## Local verification

Before pushing a release tag, run:

```sh
make check
./scripts/check-container.sh
go run github.com/goreleaser/goreleaser/v2@v2.17.1 check
GITHUB_TOKEN="$(gh auth token)" \
  go run github.com/goreleaser/goreleaser/v2@v2.17.1 release --snapshot --clean
```

Inspect the snapshot metadata and checksum file under `dist/`. A real release
must contain exactly six binaries plus the checksum file, and `jiro --version`
must report the tag version without the leading `v`.

The reusable CI workflow must also pass its container job before publication.
That job verifies stable and prerelease tag metadata, proves both supported
platforms build, then checks the host-native image's non-root identity,
entrypoint, writable home/config paths, retained shell, and injected version.
CI images are never pushed or retained.

The read-only release smoke passes an empty release-notes file so GoReleaser
does not call GitHub's write-gated generated-notes API. The final publish job
generates the GitHub-native release notes with `contents: write`.

Release assets are not created manually. If a release workflow fails, fix the
cause and create a new SemVer tag rather than moving or reusing a published tag.
