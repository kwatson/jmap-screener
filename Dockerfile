# Build stage
FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /screener ./cmd/screener/

# Runtime stage
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 65532 screener \
    && adduser -S -D -H -u 65532 -G screener screener \
    && mkdir -p /data /etc/screener \
    && chown -R 65532:65532 /data /etc/screener
COPY --from=builder /screener /usr/local/bin/screener
WORKDIR /etc/screener
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["screener"]
