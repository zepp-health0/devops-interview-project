# Stage 1: Build the Go application
FROM golang:1.26 AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /bin/task-api .


# Stage 2: Small runtime image
FROM alpine:3.22

# Install wget for the Docker healthcheck.
RUN apk add --no-cache wget

COPY --from=builder /bin/task-api /task-api

EXPOSE 8080

HEALTHCHECK --interval=10s --timeout=5s --start-period=5s --retries=5 \
    CMD ["wget", "--spider", "--quiet", "http://127.0.0.1:8080/healthz"]

ENTRYPOINT ["/task-api"]

