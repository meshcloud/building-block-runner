FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

WORKDIR /app

# Single root module (D11) — no go.work, no per-module COPY dance; download deps from
# go.mod/go.sum alone before the full COPY for better layer caching.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# cmd/bbrunner is both the k8s controller and the in-process superset (D1/D2): it links
# all handlers + both dispatchers and auto-detects which one to use at startup. This
# image ships it directly as the published run-controller entrypoint — the one
# adaptive/fat binary (PLAN_DETAIL_04 §3.6/§4.1).
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -buildvcs=false \
  -ldflags "-s -w -X 'github.com/meshcloud/building-block-runner/internal/build.Version=${VERSION}'" \
  -o /out/run-controller ./cmd/bbrunner

FROM alpine:3.22.4@sha256:310c62b5e7ca5b08167e4384c68db0fd2905dd9c7493756d356e893909057601

RUN apk update && \
    apk upgrade && \
    apk add --no-cache ca-certificates bash

RUN addgroup -S meshcloud -g 2000 && adduser -SD meshcloud -u 2000 -G meshcloud && \
    # required for users to add certificates to truststore
    chmod 0777 /etc/ssl/certs /usr/local/share/ca-certificates

ENV CUSTOM_CA_CERTS_PATH=/usr/local/share/ca-certificates

# Direct entrypoint (D8) — one binary, no persona-selecting symlink. Default (no
# subcommand) is the auto-detecting controller/superset; an operator may append a fit
# subcommand (e.g. `tf`) to force one persona in-process for local/standalone use.
COPY --from=builder --chown=meshcloud:meshcloud --chmod=755 /out/run-controller /app/run-controller
COPY --chown=meshcloud:meshcloud containers/run-controller/runner-config.yml /app/.
COPY --chown=meshcloud:meshcloud --chmod=755 containers/entrypoint-go.sh /app/entrypoint.sh

WORKDIR /app
USER 2000
ENTRYPOINT ["/app/entrypoint.sh", "/app/run-controller"]
