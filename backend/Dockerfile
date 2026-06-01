FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -o /lara ./cmd/lara

FROM alpine:3.19
RUN apk add --no-cache ca-certificates sqlite
WORKDIR /app
COPY --from=builder /lara .
EXPOSE 8400
HEALTHCHECK --interval=30s --timeout=5s \
  CMD wget -q -O- http://localhost:8400/health || exit 1
CMD ["./lara"]
