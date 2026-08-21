# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /rent-scout ./cmd/rent-scout

# Runtime stage
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 1000 rentscout

WORKDIR /app
RUN mkdir -p /app/db /app/logs && chown -R rentscout:rentscout /app

COPY --from=builder /rent-scout /app/rent-scout
USER rentscout

ENV DB_PATH=/app/db/rent-scout.db \
    LOG_DIR=/app/logs
EXPOSE 7777
ENTRYPOINT ["/app/rent-scout"]
