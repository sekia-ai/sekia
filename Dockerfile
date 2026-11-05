# --- Builder ---
FROM golang:1.27-alpine AS builder
RUN apk add --no-cache git
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/sekiad          ./cmd/sekiad \
 && CGO_ENABLED=0 go build -o /out/sekiactl         ./cmd/sekiactl \
 && CGO_ENABLED=0 go build -o /out/sekia-github     ./cmd/sekia-github \
 && CGO_ENABLED=0 go build -o /out/sekia-google     ./cmd/sekia-google

# --- sekiad (daemon + CLI) ---
FROM alpine:3.24 AS sekiad
RUN apk add --no-cache ca-certificates && apk upgrade --no-cache zlib && adduser -D sekia
COPY --from=builder /out/sekiad /usr/local/bin/sekiad
COPY --from=builder /out/sekiactl /usr/local/bin/sekiactl
USER sekia
ENTRYPOINT ["sekiad"]

# --- sekia-github ---
FROM alpine:3.24 AS sekia-github
RUN apk add --no-cache ca-certificates && apk upgrade --no-cache zlib && adduser -D sekia
COPY --from=builder /out/sekia-github /usr/local/bin/sekia-github
USER sekia
ENTRYPOINT ["sekia-github"]

# --- sekia-google ---
FROM alpine:3.24 AS sekia-google
RUN apk add --no-cache ca-certificates && apk upgrade --no-cache zlib && adduser -D sekia
COPY --from=builder /out/sekia-google /usr/local/bin/sekia-google
USER sekia
ENTRYPOINT ["sekia-google"]
