# syntax=docker/dockerfile:1

# --- Build stage ---
FROM golang:1.21-alpine AS build
WORKDIR /src

# Cache deps first.
COPY go.mod go.sum* ./
RUN go mod download

# Build the rest of the source.
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath -ldflags="-s -w" \
    -o /out/dechat ./cmd/server

# --- Runtime stage ---
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 10001 dechat
USER dechat
COPY --from=build /out/dechat /usr/local/bin/dechat

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/dechat"]
