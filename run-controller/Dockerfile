FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

WORKDIR /app

# Copy workspace files and shared library first for better layer caching
# go.work references all three modules, so all go.mod/go.sum files must be present
COPY go.work go.work.sum ./
COPY go-meshapi-client/ ./go-meshapi-client/
COPY run-controller/go.mod run-controller/go.sum ./run-controller/
COPY tf-block-runner/go.mod tf-block-runner/go.sum ./tf-block-runner/

# Download dependencies (replace directive resolves to local path, no network needed)
WORKDIR /app/run-controller
RUN go mod download

# Copy the rest of the source
WORKDIR /app
COPY run-controller/ ./run-controller/

WORKDIR /app/run-controller
ARG VERSION=dev
ARG COMMIT_SHA=unknown
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -buildvcs=false -ldflags "-s -w -X 'github.com/meshcloud/building-block-runner/run-controller/build.Version=${VERSION}' -X 'github.com/meshcloud/building-block-runner/run-controller/build.Commit=${COMMIT_SHA}'" -o run-controller .

FROM alpine:3

RUN apk update && \
    apk upgrade && \
    apk add --no-cache ca-certificates bash

RUN addgroup -S meshcloud -g 2000 && adduser -SD meshcloud -u 2000 -G meshcloud && \
    # required for users to add certificates to truststore
    chmod 0777 /etc/ssl/certs /usr/local/share/ca-certificates

ENV CUSTOM_CA_CERTS_PATH=/usr/local/share/ca-certificates

COPY --from=builder --chown=meshcloud:meshcloud --chmod=755 /app/run-controller/run-controller /app/run-controller
COPY --from=builder --chown=meshcloud:meshcloud /app/run-controller/application.yml /app/.
COPY --chown=meshcloud:meshcloud --chmod=755 containers/entrypoint.sh /app/entrypoint.sh

WORKDIR /app
USER 2000
ENTRYPOINT ["/app/entrypoint.sh", "/app/run-controller"]
