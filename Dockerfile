FROM --platform=$BUILDPLATFORM golang:1.26.0-alpine3.23@sha256:d4c4845f5d60c6a974c6000ce58ae079328d03ab7f721a0734277e69905473e5 AS build

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

FROM cgr.dev/chainguard/wolfi-base:latest@sha256:5ec604f42453ccad5058c32094de2347b4bf8f67980465a8f1505ccec4fc6883

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
