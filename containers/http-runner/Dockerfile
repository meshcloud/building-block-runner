FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

WORKDIR /app

# Single root module — download deps from go.mod/go.sum before the full COPY for better
# layer caching.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CMD_PKG selects which HTTP-only runner type this image builds (./cmd/manual, ./cmd/github,
# ./cmd/gitlab, ./cmd/azdevops). One shared Dockerfile builds all four HTTP-only runner
# types: they are pure HTTP clients (no git/tofu/nix, no k8s/client-go) that differ ONLY in
# the compiled package and the baked runner-type config below, so the runtime image is
# otherwise byte-identical across them.
ARG CMD_PKG
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -buildvcs=false \
  -ldflags "-s -w -X 'github.com/meshcloud/building-block-runner/internal/build.Version=${VERSION}'" \
  -o /out/runner ${CMD_PKG}

FROM alpine:3.22.4@sha256:310c62b5e7ca5b08167e4384c68db0fd2905dd9c7493756d356e893909057601

# HTTP-only runner types need no git/tofu/nix — ca-certificates + bash only (the entrypoint's
# CA import needs bash), a much smaller surface than the tf image.
RUN apk update && apk upgrade && \
  apk add --no-cache bash ca-certificates

RUN addgroup -S meshcloud -g 2000 && adduser -SD meshcloud -u 2000 -G meshcloud && \
  chmod 0777 /etc/ssl/certs /usr/local/share/ca-certificates

ENV CUSTOM_CA_CERTS_PATH=/usr/local/share/ca-certificates

USER 2000

# CONFIG_DIR names the runner-type config directory under containers/ (e.g. manual-block-runner);
# its runner-config.yml is baked as this image's runner-type default layer.
ARG CONFIG_DIR

# Direct entrypoint — one runner-type binary at the uniform in-image path /app/runner. The
# published image *name* (manual-block-runner, gitlab-block-runner, …) is the customer-referenced
# surface that distinguishes the four; the in-image binary path is not a documented command:
# override target here. The Go entrypoint imports mounted CA certs then execs it directly.
COPY --from=builder --chown=meshcloud:meshcloud --chmod=755 /out/runner /app/runner
# The shared top-level base layer (the well-known dev private key) is consumed ONLY by gitlab,
# whose config wires the base path (RUNNER_BASE_CONFIG_FILE, default containers/runner-config.yml);
# manual/github/azdevops load an EMPTY base path, so this file is inert for them — it is copied
# uniformly to keep a single Dockerfile and is never read outside gitlab. Both layers live under
# containers/ inside the image so the compiled-in default paths resolve identically to local-dev
# (cwd = repo root) and in-container (WORKDIR /app).
COPY --chown=meshcloud:meshcloud containers/runner-config.yml /app/containers/runner-config.yml
COPY --chown=meshcloud:meshcloud containers/${CONFIG_DIR}/runner-config.yml /app/runner-config.yml
COPY --chown=meshcloud:meshcloud --chmod=755 containers/entrypoint-go.sh /app/entrypoint.sh

# Bake PORT=8080 (never MANAGEMENT_PORT, so operator MANAGEMENT_PORT overrides still win). The
# per-type management-port default (8103/8104/…) lives in the Go config, not here.
ENV PORT=8080
EXPOSE 8080

WORKDIR /app
ENTRYPOINT ["/app/entrypoint.sh", "/app/runner"]
