FROM golang:1.25-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download
RUN go install github.com/pressly/goose/v3/cmd/goose@v3.27.0

FROM alpine:3.22

RUN apk add --no-cache ca-certificates \
    && addgroup -S signa \
    && adduser -S -G signa signa

WORKDIR /app
COPY --from=build --chown=signa:signa /go/bin/goose /app/goose
COPY --chown=signa:signa migrations /app/migrations

USER signa
ENTRYPOINT ["/app/goose"]
CMD ["-h"]
