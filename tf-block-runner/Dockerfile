FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o tfrunner .

FROM alpine:3

RUN apk update && apk upgrade && \
    apk add --no-cache bash git jq openssh

COPY --from=builder /app/tfrunner /tfrunner
COPY application.yml /.
COPY resources/known_hosts /tmp/.

ENV SSH_KNOWN_HOSTS=/tmp/known_hosts

ENTRYPOINT ["/tfrunner", "-workerCount=1"]
