# Release workflow

Releases are driven by release-please from Conventional Commits on `main`.
Every push to `main` runs `.github/workflows/release.yml`, which keeps a
release PR open with the next version and `CHANGELOG.md` entry. Merging that PR
makes release-please tag the merge commit and create the GitHub Release; the
same workflow run then calls the Ubuntu and macOS checks used by normal CI
before publishing artifacts.

Version bumps follow `release-please-config.json`: before 1.0, `feat` and
breaking changes bump the minor version and everything else bumps the patch.
`feat`, `fix`, `perf`, `revert`, and `chore` (including Dependabot's
`chore(deps)` base-image updates) appear in the changelog and make a release
due; other commit types do not. `.release-please-manifest.json` records the
current version and is updated only by the release PR.

release-please uses the workflow's `GITHUB_TOKEN`, so the release PR does not
trigger CI on its own; the full CI gate runs after merge, before publication.

The release publishes standalone `jiro` binaries for Linux, macOS, and Windows
on amd64 and arm64. It does not publish operating-system packages or archives.
Every release also includes one SHA-256 checksum file covering all six
binaries.

GoReleaser uploads the binaries to the existing release and leaves its notes
untouched. After the upload, a separate job updates
`timonwong/homebrew-tap` with the new stable Formula. Cross-repository writes use
an SSH deploy key scoped to the tap repository; its private key is stored in the
`HOMEBREW_TAP_PRIVATE_KEY` Actions secret. The tap commit uses `goreleaserbot`
as its author.

The release workflow also publishes a multi-platform container image to
`ghcr.io/timonwong/jiro` as both `vX.Y.Z` and `latest`. The image supports Linux on amd64 and
arm64, runs as UID/GID 65532, and retains the Wolfi base shell. Only the
container-publishing job receives `packages: write` permission.

Container images are built and inspected as disposable artifacts during normal
CI. Only a tagged release pushes an image to GHCR. The release build emits OCI
source, revision, version, and license labels together with provenance and an
SBOM. If any publishing job fails after the GitHub Release exists, rerun the
failed job; do not move or reuse the tag.

GitHub Packages creates the container package as private on its first publish.
After the first successful container release, a maintainer must make the
`jiro` package public once in its GitHub Package settings so the documented
anonymous pull works. Package visibility is not changed by the release job.

## Local verification

Before merging a release PR, run:

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

Release tags, release notes, and assets are not created manually. If a release
fails in a way a rerun cannot fix, land the fix on `main` and release the next
version rather than moving or reusing a published tag.
