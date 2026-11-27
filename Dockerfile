# syntax=docker/dockerfile:1.7

# GO_VERSION mirrors go.mod's `toolchain` line, the Go fss is built with (its
# `go` line is only the minimum for importers). CI passes it from go.mod and a
# lint guard keeps this default in sync. It does not select the base image: the
# FROM lines below are literal tags with digests so Dependabot can bump them,
# and the builder stage fails if the toolchain it got differs from this.
ARG GO_VERSION=1.27.4

# ---- build ----
FROM --platform=$BUILDPLATFORM golang:1.27.4-alpine3.24@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS builder

ARG GO_VERSION
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown

WORKDIR /src

RUN [ "$(go env GOVERSION)" = "go${GO_VERSION}" ] || { echo "base image has $(go env GOVERSION), go.mod toolchain is go${GO_VERSION}: update the golang FROM line or the toolchain line"; exit 1; }

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath \
      -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE} -X main.channel=docker" \
      -o /out/fss .

# ---- runtime ----
FROM alpine:3.24@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf

RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S fss && adduser -S -G fss -h /home/fss fss && \
    mkdir -p /data /config && \
    chown -R fss:fss /data /config /home/fss

ENV XDG_CONFIG_HOME=/config

COPY --from=builder /out/fss /usr/local/bin/fss

USER fss
WORKDIR /data
VOLUME ["/data", "/config"]

ENTRYPOINT ["fss"]
CMD ["--help"]
