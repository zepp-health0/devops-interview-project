# syntax=docker/dockerfile:1

# --- dependencies -------------------------------------------------------------------------
# Resolved in their own stage so that editing a .go file does not invalidate the module cache.
FROM golang:1.26 AS deps
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

# --- build --------------------------------------------------------------------------------
FROM deps AS build
WORKDIR /src
COPY . .

# The image must stay under 15 MiB, so the binary is the image: fully static, stripped of the
# symbol table (-s) and DWARF (-w), with absolute build paths removed (-trimpath).
#
# VERSION/REVISION arrive as build args rather than being read from git, because .dockerignore
# excludes .git/ from the build context. REVISION is what makes a running container traceable
# back to the commit that produced it.
ARG VERSION=dev
ARG REVISION=unknown
RUN CGO_ENABLED=0 GOOS=linux go build \
        -trimpath \
        -ldflags="-s -w -X main.version=${VERSION} -X main.revision=${REVISION}" \
        -o /out/task-api .

# --- runtime ------------------------------------------------------------------------------
FROM scratch

# Numeric UID/GID: scratch has no /etc/passwd, so a named user would fail to resolve.
# 65532 is the conventional "nonroot" ID used by distroless.
USER 65532:65532

COPY --from=build /out/task-api /task-api

ENV PORT=8080
EXPOSE 8080

# scratch has no shell and no curl, so the shell form of HEALTHCHECK and the usual
# `curl -f http://localhost/healthz` both fail silently and leave the container permanently
# unhealthy. The binary probes itself instead, invoked in exec form.
HEALTHCHECK --interval=5s --timeout=3s --start-period=2s --retries=3 \
    CMD ["/task-api", "-healthcheck"]

# Absolute path: there is no $PATH to search on scratch.
ENTRYPOINT ["/task-api"]
