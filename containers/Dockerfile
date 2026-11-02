# builder builds the binary from a clean clone; prebuilt takes bin/runner-linux-<arch> of task image:build.
ARG BINARY=builder

FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

# The whole context with .git, so that go build stamps the version and the revision of a clean checkout.
COPY . .

ARG TAGS=""
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -tags "${TAGS}" -trimpath -buildvcs=true -ldflags "-s -w" -o runner ./cmd/runner

FROM scratch AS prebuilt
ARG TARGETARCH
COPY bin/runner-linux-${TARGETARCH} /app/runner

FROM ${BINARY} AS binary

FROM alpine:3.22.4@sha256:310c62b5e7ca5b08167e4384c68db0fd2905dd9c7493756d356e893909057601 AS base

RUN apk upgrade --no-cache && \
    apk add --no-cache ca-certificates bash

# 0777 lets the non-root user add certificates to the truststore.
RUN addgroup -S meshcloud -g 2000 && adduser -SD meshcloud -u 2000 -G meshcloud && \
    chmod 0777 /etc/ssl/certs /usr/local/share/ca-certificates

ENV CUSTOM_CA_CERTS_PATH=/usr/local/share/ca-certificates
ENV RUNNER_METRICS_PORT=2112
EXPOSE 2112

COPY --chown=meshcloud:meshcloud --chmod=755 containers/entrypoint.sh /app/entrypoint.sh

WORKDIR /app
USER 2000
ENTRYPOINT ["/app/entrypoint.sh", "/app/runner"]

FROM base AS runner

COPY --from=binary --chown=meshcloud:meshcloud --chmod=755 /app/runner /app/runner

ARG FOR_TYPES=""
ENV RUNNER_DISPATCHER_FOR_TYPES=${FOR_TYPES}

FROM base AS tf

USER root
RUN apk add --no-cache git jq openssh curl xz coreutils python3 && \
    mkdir -m 0755 /nix && chown meshcloud:meshcloud /nix
USER 2000

# Single-user nix lets a run install packages with `nix profile add nixpkgs#<package>`;
# the registry pins nixpkgs to containers/nix/flake.lock, so that needs no nixpkgs download.
ARG TARGETARCH
ARG NIX_VERSION=2.34.7
ARG NIX_INSTALL_SHA256=e9d447ce3d2ff62d7ff9cb6ef401de6fa8acb148839dd00f7271945d7b638b14
ENV PATH="/home/meshcloud/.nix-profile/bin:${PATH}"
RUN --mount=type=bind,source=containers/nix/flake.lock,target=/tmp/flake.lock \
  set -eux; \
  curl -fsSL -o /tmp/nix-install "https://releases.nixos.org/nix/nix-${NIX_VERSION}/install"; \
  echo "${NIX_INSTALL_SHA256}  /tmp/nix-install" | sha256sum -c -; \
  if [ "${TARGETARCH:-}" = "arm64" ]; then \
    export NIX_CONFIG=$'sandbox = false\nfilter-syscalls = false'; \
  fi; \
  sh /tmp/nix-install --no-daemon --yes --no-channel-add; \
  rm -f /tmp/nix-install; \
  mkdir -p "$HOME/.config/nix"; \
  echo 'experimental-features = nix-command flakes' > "$HOME/.config/nix/nix.conf"; \
  nix registry pin nixpkgs "$(jq -r '.nodes.nixpkgs.locked | "github:\(.owner)/\(.repo)/\(.rev)?narHash=\(.narHash | @uri)"' /tmp/flake.lock)"; \
  ln -s "$(nix flake metadata --json nixpkgs | jq -r .path)" /nix/var/nix/gcroots/nixpkgs; \
  rm -rf "$HOME/.cache/nix"

COPY --chmod=755 containers/aws /usr/local/bin/aws

COPY --chown=meshcloud:meshcloud --chmod=644 containers/known_hosts /app/known_hosts
ENV SSH_KNOWN_HOSTS=/app/known_hosts

COPY --from=binary --chown=meshcloud:meshcloud --chmod=755 /app/runner /app/runner

# Last, so that tf-block-runner and building-block-runner share the nix layer.
ARG FOR_TYPES=""
ENV RUNNER_DISPATCHER_FOR_TYPES=${FOR_TYPES}
