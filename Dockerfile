FROM golang:1.17.3-alpine as builder

ENV GOPATH=/go \
    GO111MODULE=on \
    PROJECT_ROOT=/go/src/github.com/butterv/gload

WORKDIR $PROJECT_ROOT

COPY go.mod .
COPY go.sum .
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o gload -a ./cmd/

# image for release
FROM alpine:latest
ENV BUILDER_ROOT /go/src/github.com/butterv/gload
ENV PROJECT_ROOT /
COPY --from=builder $BUILDER_ROOT/gload $PROJECT_ROOT/gload
