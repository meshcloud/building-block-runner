FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

WORKDIR /app

# Single root module — no go.work, no per-module COPY dance; download deps from go.mod/go.sum
# alone before the full COPY for better layer caching.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -buildvcs=false \
  -ldflags "-s -w -X 'github.com/meshcloud/building-block-runner/internal/build.Version=${VERSION}'" \
  -o /out/tf-block-runner ./cmd/tf

FROM alpine:3.22.4@sha256:310c62b5e7ca5b08167e4384c68db0fd2905dd9c7493756d356e893909057601

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

# Direct entrypoint — one binary, no runner-type-selecting symlink. /app/tfrunner is a plain
# duplicate copy of the same binary: operators can override a k8s Job `command:` to that legacy
# path (run-controller/controller/kubernetes.go passes it through verbatim), and since this image
# ships exactly one runner type, argv[0]/the invoked path never selects behavior.
COPY --from=builder --chown=meshcloud:meshcloud --chmod=755 /out/tf-block-runner /app/tf-block-runner
COPY --from=builder --chown=meshcloud:meshcloud --chmod=755 /out/tf-block-runner /app/tfrunner
COPY --chown=meshcloud:meshcloud containers/tf-block-runner/runner-config.yml /app/.
COPY --chown=meshcloud:meshcloud --chmod=644 containers/tf-block-runner/known_hosts /app/known_hosts
COPY --chown=meshcloud:meshcloud --chmod=755 containers/entrypoint-go.sh /app/entrypoint.sh

ENV SSH_KNOWN_HOSTS=/app/known_hosts
ENV PORT=8080
EXPOSE 8080

WORKDIR /app
ENTRYPOINT ["/app/entrypoint.sh", "/app/tf-block-runner"]
