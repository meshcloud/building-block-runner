# syntax=docker/dockerfile:1
#
# ONE multi-target Dockerfile for every shipped image. A single `go build` of ./cmd/bbrunner,
# parameterized by the CMD_TAGS build-arg, produces each binary:
#   - CMD_TAGS=""            -> the in-process superset (dev-local; not shipped as an image)
#   - CMD_TAGS="type_<x>"    -> a lean single-type fit image (manual/github/gitlab/azdevops/tf)
#   - CMD_TAGS="k8s"         -> the run-controller (Kubernetes-Job dispatcher, no type handlers)
# The four HTTP fit images and the run-controller share the scratch-runtime stage (they differ
# only by CMD_TAGS and the baked config); tf needs an alpine runtime for its git/tofu/nix chain.
# CA setup is in-binary (internal/catrust) — there is no shell wrapper.

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

WORKDIR /app

# Single root module — download deps from go.mod/go.sum before the full COPY for layer caching,
# shared across every target.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG CMD_TAGS=""
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/root/.cache/go-build \
  CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -buildvcs=false \
  -tags "${CMD_TAGS}" \
  -ldflags "-s -w -X 'github.com/meshcloud/building-block-runner/internal/build.Version=${VERSION}'" \
  -o /out/runner ./cmd/bbrunner

# Scratch runtime shared by the four HTTP fit images AND the run-controller: no shell, no
# update-ca-certificates — catrust.RootCAs seeds egress trust in-binary from the frozen bundle
# below (main.go). USER 2000 is numeric because scratch has no /etc/passwd.
FROM scratch AS scratch-runtime
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
ARG CONFIG_FILE
COPY --from=builder --chmod=755 /out/runner /app/runner
COPY ${CONFIG_FILE} /app/runner-config.yml
ENV CUSTOM_CA_CERTS_PATH=/usr/local/share/ca-certificates
# No baked management port: each build uses its own compiled-in default (run-controller 2112,
# fit types 810x) via MANAGEMENT_PORT. Baking the legacy PORT alias here both emitted a
# deprecation warning on every boot and collided with the fit types' default API url (:8080);
# a deployment/Helm chart sets MANAGEMENT_PORT explicitly and declares the containerPort.
WORKDIR /app
USER 2000
ENTRYPOINT ["/app/runner"]

# Alpine runtime for tf: cannot be scratch — it shells out to git/jq/openssh/curl/xz/coreutils/
# python3/aws-cli plus a single-user nix install for on-demand runtime tooling.
FROM alpine:3.22.4@sha256:310c62b5e7ca5b08167e4384c68db0fd2905dd9c7493756d356e893909057601 AS tf-runtime

RUN apk update && \
    apk upgrade && \
    apk add --no-cache bash git jq openssh curl ca-certificates xz coreutils python3 aws-cli

# required for users to add certificates to truststore and single-user nix installs
RUN addgroup -S meshcloud -g 2000 && adduser -SD meshcloud -u 2000 -G meshcloud && \
  chmod 0777 /etc/ssl/certs /usr/local/share/ca-certificates && \
  mkdir -m 0755 /nix && chown meshcloud:meshcloud /nix

ENV CUSTOM_CA_CERTS_PATH=/usr/local/share/ca-certificates

USER 2000

# Install nix in single-user mode for meshcloud.
# Runtime package installs can then be done with:
#   nix profile add nixpkgs#<package>
ARG TARGETARCH
ARG NIX_VERSION=2.34.7
ARG NIX_INSTALL_SHA256=e9d447ce3d2ff62d7ff9cb6ef401de6fa8acb148839dd00f7271945d7b638b14
RUN set -eux; \
  curl -fsSL -o /tmp/nix-install "https://releases.nixos.org/nix/nix-${NIX_VERSION}/install"; \
  echo "${NIX_INSTALL_SHA256}  /tmp/nix-install" | sha256sum -c -; \
  if [ "${TARGETARCH:-}" = "arm64" ]; then \
    NIX_CONFIG=$'sandbox = false\nfilter-syscalls = false' \
      sh /tmp/nix-install --no-daemon --yes --no-channel-add; \
  else \
    sh /tmp/nix-install --no-daemon; \
  fi; \
  rm -f /tmp/nix-install

ENV PATH="/home/meshcloud/.nix-profile/bin:${PATH}"
ENV NIX_CONFIG="experimental-features = nix-command flakes"

# One binary run directly. /app/tfrunner is a plain duplicate copy: operators can override a k8s
# Job `command:` to that legacy path (DEPRECATIONS §8), and since this image ships exactly one
# runner type, the invoked path never selects behavior.
COPY --from=builder --chmod=755 /out/runner /app/tf-block-runner
COPY --from=builder --chmod=755 /out/runner /app/tfrunner
COPY cmd/runner-config.yml /app/runner-config.yml
COPY --chmod=644 cmd/tf/known_hosts /app/known_hosts

ENV SSH_KNOWN_HOSTS=/app/known_hosts
# No baked management port (see scratch-runtime): tf uses its compiled-in default (8100) via
# MANAGEMENT_PORT; a deployment/Helm chart sets it explicitly and declares the containerPort.

WORKDIR /app
USER 2000
ENTRYPOINT ["/app/tf-block-runner"]
