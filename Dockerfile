# syntax=docker/dockerfile:1.7

FROM golang:1.25.4-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" \
    go build -trimpath -ldflags="-s -w" -o /out/ryoko ./cmd/api

FROM alpine:3.22 AS runtime

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S ryoko \
    && adduser -S -G ryoko ryoko

WORKDIR /app

COPY --from=build --chown=ryoko:ryoko /out/ryoko /app/ryoko

USER ryoko

EXPOSE 8080

ENTRYPOINT ["/app/ryoko"]
