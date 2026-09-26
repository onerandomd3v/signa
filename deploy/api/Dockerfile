# syntax note: build context is the repository ROOT (see
# deploy/compose.openship.yaml and docs/deployment-openship.md). The COPY
# instructions below reference go.mod, cmd/, contracts/, internal/ at that root.
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
    go build -trimpath -ldflags="-s -w" -o /out/signa-api ./cmd/api

FROM alpine:3.22

RUN apk add --no-cache ca-certificates \
    && addgroup -S signa \
    && adduser -S -G signa signa

WORKDIR /app
COPY --from=build --chown=signa:signa /out/signa-api /app/signa-api
COPY --from=build --chown=signa:signa /src/contracts /app/contracts

USER signa
EXPOSE 8080
ENTRYPOINT ["/app/signa-api"]
