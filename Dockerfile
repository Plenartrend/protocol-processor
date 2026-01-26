FROM golang:1.25.5-alpine AS builder

WORKDIR /build

RUN apk add --no-cache git ca-certificates tzdata

COPY go.mod go.sum ./
RUN go mod download

COPY src/ ./src/

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s -extldflags "-static"' \
    -a -installsuffix cgo \
    -o protocol-processor \
    ./src

FROM alpine:latest

RUN apk --no-cache add ca-certificates tzdata wget

RUN addgroup -g 1000 appuser && \
    adduser -D -u 1000 -G appuser appuser

WORKDIR /app

COPY --from=builder /build/protocol-processor .

RUN chown -R appuser:appuser /app

USER appuser

CMD ["./protocol-processor"]

