FROM golang:1.26-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o run-controller .

FROM alpine:3

RUN apk update && apk upgrade && apk add --no-cache ca-certificates

COPY --from=builder /app/run-controller /run-controller
COPY application.yml /.

ENTRYPOINT ["/run-controller"]
