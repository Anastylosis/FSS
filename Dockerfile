# syntax=docker/dockerfile:1.7

# GO_VERSION mirrors the `go` directive in go.mod. CI workflows override this
# from go.mod via scripts/go-version.sh; the default below is the fallback for
# `docker build .` invocations that don't pass --build-arg, and a lint guard
# in CI enforces that this default stays in sync with go.mod.
ARG GO_VERSION=1.26.5
ARG ALPINE_VERSION=3.21

# ---- build ----
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS builder

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown

WORKDIR /src

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
FROM alpine:${ALPINE_VERSION}

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
