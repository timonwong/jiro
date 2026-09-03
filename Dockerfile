FROM golang:1.26 AS build

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" \
    go build -trimpath \
    -ldflags="-s -w -X github.com/timonwong/jiro/internal/cmd.version=${VERSION}" \
    -o /out/jiro ./cmd/jiro && \
    mkdir -p /out/home/nonroot/.config && \
    chown -R 65532:65532 /out/home/nonroot

FROM cgr.dev/chainguard/wolfi-base:latest@sha256:103eb3f4444c68ea2453bf3aad09d860eaa5a698effb3e656cd607f630f0e46d

ARG VERSION=dev
ARG REVISION=unknown
ARG SOURCE=https://github.com/timonwong/jiro

LABEL org.opencontainers.image.source="$SOURCE" \
      org.opencontainers.image.revision="$REVISION" \
      org.opencontainers.image.version="$VERSION" \
      org.opencontainers.image.licenses="MIT"

COPY --from=build --chown=65532:65532 /out/jiro /usr/local/bin/jiro
COPY --from=build --chown=65532:65532 /out/home/nonroot /home/nonroot

ENV HOME=/home/nonroot \
    XDG_CONFIG_HOME=/home/nonroot/.config

USER 65532:65532
ENTRYPOINT ["/usr/local/bin/jiro"]
