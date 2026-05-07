FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

WORKDIR /app

# Copy workspace files and shared library first for better layer caching
# go.work references all three modules, so all go.mod/go.sum files must be present
COPY go.work go.work.sum ./
COPY go-meshapi-client/ ./go-meshapi-client/
COPY tf-block-runner/go.mod tf-block-runner/go.sum ./tf-block-runner/
COPY run-controller/go.mod run-controller/go.sum ./run-controller/

# Download dependencies (replace directive resolves to local path, no network needed)
WORKDIR /app/tf-block-runner
RUN go mod download

# Copy the rest of the source
WORKDIR /app
COPY tf-block-runner/ ./tf-block-runner/

WORKDIR /app/tf-block-runner
ARG VERSION=dev
ARG COMMIT_SHA=unknown
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -buildvcs=false -ldflags "-s -w -X 'github.com/meshcloud/meshfed-release/buildingblocks/tf-block-runner/build.Version=${VERSION}' -X 'github.com/meshcloud/meshfed-release/buildingblocks/tf-block-runner/build.Commit=${COMMIT_SHA}'" -o tfrunner .

FROM alpine:3

RUN apk update && \
    apk upgrade && \
    apk add --no-cache bash git jq openssh curl ca-certificates

RUN addgroup -S meshcloud -g 2000 && adduser -SD meshcloud -u 2000 -G meshcloud && \
    # required for users to add certificates to truststore
    chmod 0777 /etc/ssl/certs /usr/local/share/ca-certificates

ENV CUSTOM_CA_CERTS_PATH=/usr/local/share/ca-certificates

# Optional: Install additional packages
ARG EXTRA_PACKAGES=""
RUN if [ -n "$EXTRA_PACKAGES" ]; then apk add $EXTRA_PACKAGES; fi

COPY --from=builder --chown=meshcloud:meshcloud --chmod=755 /app/tf-block-runner/tfrunner /app/tfrunner
COPY --from=builder --chown=meshcloud:meshcloud /app/tf-block-runner/application.yml /app/.
COPY --from=builder --chown=meshcloud:meshcloud --chmod=644 /app/tf-block-runner/resources/known_hosts /app/known_hosts
COPY --chown=meshcloud:meshcloud --chmod=755 containers/entrypoint.sh /app/entrypoint.sh

ENV SSH_KNOWN_HOSTS=/app/known_hosts

WORKDIR /app
USER 2000
ENTRYPOINT ["/app/entrypoint.sh", "/app/tfrunner", "-workerCount=1"]
