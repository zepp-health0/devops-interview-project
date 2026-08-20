FROM golang:1.26 AS builder
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /bin/task-api .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /bin/task-api /task-api
EXPOSE 8080
USER nonroot:nonroot
HEALTHCHECK --interval=5s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/task-api", "-healthcheck"]
ENTRYPOINT ["/task-api"]
