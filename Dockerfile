# --- Builder ---
FROM golang:1.25-alpine AS builder
RUN apk add --no-cache git
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/sekiad          ./cmd/sekiad \
 && CGO_ENABLED=0 go build -o /out/sekiactl         ./cmd/sekiactl \
 && CGO_ENABLED=0 go build -o /out/sekia-github     ./cmd/sekia-github \
 && CGO_ENABLED=0 go build -o /out/sekia-slack      ./cmd/sekia-slack \
 && CGO_ENABLED=0 go build -o /out/sekia-linear     ./cmd/sekia-linear \
 && CGO_ENABLED=0 go build -o /out/sekia-google     ./cmd/sekia-google \
 && CGO_ENABLED=0 go build -o /out/sekia-mcp        ./cmd/sekia-mcp

# --- sekiad (daemon + CLI) ---
FROM alpine:3.23 AS sekiad
RUN apk add --no-cache ca-certificates
COPY --from=builder /out/sekiad /usr/local/bin/sekiad
COPY --from=builder /out/sekiactl /usr/local/bin/sekiactl
ENTRYPOINT ["sekiad"]

# --- sekia-github ---
FROM alpine:3.23 AS sekia-github
RUN apk add --no-cache ca-certificates
COPY --from=builder /out/sekia-github /usr/local/bin/sekia-github
ENTRYPOINT ["sekia-github"]

# --- sekia-slack ---
FROM alpine:3.23 AS sekia-slack
RUN apk add --no-cache ca-certificates
COPY --from=builder /out/sekia-slack /usr/local/bin/sekia-slack
ENTRYPOINT ["sekia-slack"]

# --- sekia-linear ---
FROM alpine:3.23 AS sekia-linear
RUN apk add --no-cache ca-certificates
COPY --from=builder /out/sekia-linear /usr/local/bin/sekia-linear
ENTRYPOINT ["sekia-linear"]

# --- sekia-google ---
FROM alpine:3.23 AS sekia-google
RUN apk add --no-cache ca-certificates
COPY --from=builder /out/sekia-google /usr/local/bin/sekia-google
ENTRYPOINT ["sekia-google"]

# --- sekia-mcp ---
FROM alpine:3.23 AS sekia-mcp
RUN apk add --no-cache ca-certificates
COPY --from=builder /out/sekia-mcp /usr/local/bin/sekia-mcp
ENTRYPOINT ["sekia-mcp"]
