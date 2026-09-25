FROM golang:1.25-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY contracts ./contracts
COPY internal ./internal

ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/signa-worker ./cmd/worker

FROM alpine:3.22

RUN apk add --no-cache ca-certificates \
    && addgroup -S signa \
    && adduser -S -G signa signa

WORKDIR /app
COPY --from=build --chown=signa:signa /out/signa-worker /app/signa-worker
COPY --from=build --chown=signa:signa /src/contracts /app/contracts

USER signa
ENTRYPOINT ["/app/signa-worker"]
