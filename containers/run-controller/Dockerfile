FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

WORKDIR /app

# Single root module — no go.work, no per-module COPY dance; download deps from go.mod/go.sum
# alone before the full COPY for better layer caching.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# The official run-controller image is the LEAN, k8s-Job-dispatching controller: `-tags k8s`
# links the real in-cluster Kubernetes-Job dispatcher (internal/k8sjob + client-go) and NONE of
# the per-type in-process handlers. The controller executes no run itself — it claims runs and
# creates one single-run Job per run from the config's type→image map (runner-config.yml
# `implementations`), so it needs zero linked runner-type handlers, and thus none of internal/tf's
# go-git + OpenTofu toolchain. The in-process superset (all types via go-func, the meshfed-release
# multiplexing-block-runner replacement) is the SAME cmd/bbrunner built with NO tags — a
# `go build ./cmd/bbrunner` developer binary, deliberately NOT shipped as an official image yet.
# Build-tag seam: cmd/bbrunner/registry.go + k8s_dispatcher{,_noop}.go.
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -buildvcs=false \
  -tags 'k8s' \
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

# Direct entrypoint — one binary, no runner-type-selecting symlink. This lean image runs solely
# as the in-cluster k8s-Job-dispatching controller: it links no in-process type handlers, so the
# `bbrunner <type>` fit subcommands and the in-process superset are not available here (use a
# `go build ./cmd/bbrunner` binary for those).
COPY --from=builder --chown=meshcloud:meshcloud --chmod=755 /out/run-controller /app/run-controller
COPY --chown=meshcloud:meshcloud containers/run-controller/runner-config.yml /app/.
COPY --chown=meshcloud:meshcloud --chmod=755 containers/entrypoint-go.sh /app/entrypoint.sh

WORKDIR /app
USER 2000
ENTRYPOINT ["/app/entrypoint.sh", "/app/run-controller"]
