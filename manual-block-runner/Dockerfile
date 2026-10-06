FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY go-meshapi-client/ ./go-meshapi-client/
COPY manual-block-runner/ ./manual-block-runner/

WORKDIR /app/manual-block-runner
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -buildvcs=false -ldflags "-s -w -X 'github.com/meshcloud/building-block-runner/manual-block-runner/build.Version=${VERSION}'" -o manual-block-runner .

FROM alpine:3.22.4@sha256:310c62b5e7ca5b08167e4384c68db0fd2905dd9c7493756d356e893909057601

RUN apk update && \
    apk upgrade && \
    apk add --no-cache ca-certificates bash

RUN addgroup -S meshcloud -g 2000 && adduser -SD meshcloud -u 2000 -G meshcloud && \
    # required for users to add certificates to truststore
    chmod 0777 /etc/ssl/certs /usr/local/share/ca-certificates

ENV CUSTOM_CA_CERTS_PATH=/usr/local/share/ca-certificates
# The JVM image this one replaces read custom CA certificates from /certs.
ENV SSL_CERT_DIR=/certs:/etc/ssl/certs

COPY --from=builder --chown=meshcloud:meshcloud --chmod=755 /app/manual-block-runner/manual-block-runner /app/manual-block-runner
COPY --chown=meshcloud:meshcloud --chmod=755 containers/entrypoint-go.sh /app/entrypoint.sh

ENV PORT=8080
EXPOSE 8080

WORKDIR /app
USER 2000
ENTRYPOINT ["/app/entrypoint.sh", "/app/manual-block-runner"]
