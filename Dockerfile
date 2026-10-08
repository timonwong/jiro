FROM golang:1.27 AS build

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

FROM cgr.dev/chainguard/wolfi-base:latest@sha256:05d24163df148be377275af8374c16523a1dc7e19bf4f1c689784791553c5e45

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
