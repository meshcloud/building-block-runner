FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

WORKDIR /app

# Single root module (D11) — download deps from go.mod/go.sum before the full COPY for
# better layer caching.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -buildvcs=false \
  -ldflags "-s -w -X 'github.com/meshcloud/building-block-runner/internal/build.Version=${VERSION}'" \
  -o /out/github-block-runner ./cmd/github

FROM alpine:3.22.4@sha256:310c62b5e7ca5b08167e4384c68db0fd2905dd9c7493756d356e893909057601

# GitHub is an HTTP-only runner (no git/tofu/nix) — ca-certificates + bash only (the
# entrypoint's CA import needs bash), a much smaller surface than the tf image (D8).
RUN apk update && apk upgrade && \
  apk add --no-cache bash ca-certificates

RUN addgroup -S meshcloud -g 2000 && adduser -SD meshcloud -u 2000 -G meshcloud && \
  chmod 0777 /etc/ssl/certs /usr/local/share/ca-certificates

ENV CUSTOM_CA_CERTS_PATH=/usr/local/share/ca-certificates

USER 2000

# Direct entrypoint (D8) — one persona binary, no argv[0]/symlink multiplexing. The Go
# entrypoint imports mounted CA certs then execs the github-block-runner binary directly.
COPY --from=builder --chown=meshcloud:meshcloud --chmod=755 /out/github-block-runner /app/github-block-runner
COPY --chown=meshcloud:meshcloud containers/github-block-runner/runner-config.yml /app/runner-config.yml
COPY --chown=meshcloud:meshcloud --chmod=755 containers/entrypoint-go.sh /app/entrypoint.sh

# Parity with jvm.Dockerfile:18-19 — the image bakes PORT=8080 (never MANAGEMENT_PORT, so
# operator MANAGEMENT_PORT overrides still win; plan 04 §10.7). The resolved default when
# neither is set is the legacy github port 8102 (D12).
ENV PORT=8080
EXPOSE 8080

WORKDIR /app
ENTRYPOINT ["/app/entrypoint.sh", "/app/github-block-runner"]
