#!/bin/sh
set -eu

image="jiro:container-check"
archive="$(mktemp "${TMPDIR:-/tmp}/jiro-container-check.XXXXXX")"

cleanup() {
  docker image rm --force "$image" >/dev/null 2>&1 || true
  rm -f "$archive"
}
trap cleanup EXIT INT TERM

docker buildx build \
  --platform linux/amd64,linux/arm64 \
  --build-arg VERSION=test \
  --provenance=false \
  --output "type=oci,dest=$archive" \
  .

index_digest="$(
  tar -xOf "$archive" index.json |
    jq -r '.manifests[] | select(.mediaType == "application/vnd.oci.image.index.v1+json") | .digest' |
    sed 's/^sha256://'
)"
if [ -z "$index_digest" ]; then
  echo 'OCI archive does not contain a multi-platform image index' >&2
  exit 1
fi
platforms="$(
  tar -xOf "$archive" "blobs/sha256/$index_digest" |
    jq -r '.manifests[].platform | "\(.os)/\(.architecture)"' |
    sort
)"
expected_platforms='linux/amd64
linux/arm64'
if [ "$platforms" != "$expected_platforms" ]; then
  printf 'unexpected container platforms:\n%s\n' "$platforms" >&2
  exit 1
fi

case "$(uname -m)" in
  x86_64) native_arch=amd64 ;;
  arm64 | aarch64) native_arch=arm64 ;;
  *)
    printf 'unsupported host architecture: %s\n' "$(uname -m)" >&2
    exit 1
    ;;
esac

docker buildx build \
  --platform "linux/$native_arch" \
  --build-arg VERSION=test \
  --load \
  --tag "$image" \
  .

config="$(docker image inspect "$image" --format '{{json .Config}}')"
if [ "$(printf '%s' "$config" | jq -r '.User')" != '65532:65532' ]; then
  echo 'container must run as UID/GID 65532' >&2
  exit 1
fi
if [ "$(printf '%s' "$config" | jq -r '.Entrypoint | join(" ")')" != '/usr/local/bin/jiro' ]; then
  echo 'container entrypoint must be /usr/local/bin/jiro' >&2
  exit 1
fi

[ "$(docker run --rm "$image" --version)" = 'jiro version test' ]
docker run --rm --entrypoint /bin/sh "$image" -c '
  test "$(id -u):$(id -g)" = "65532:65532"
  test -w "$HOME"
  test -w "$XDG_CONFIG_HOME"
'
