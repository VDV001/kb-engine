# syntax=docker/dockerfile:1

# --- build stage -------------------------------------------------------------
# Pinned by digest (Dependabot's docker ecosystem keeps tag+digest current).
# Runs on the native BUILDPLATFORM and cross-compiles to TARGET* so multi-arch
# builds need no QEMU emulation (CGO is disabled).
FROM --platform=$BUILDPLATFORM golang:1.26-bookworm@sha256:5ae05f331ade3e10c2647be439a4f42dae789002f7d2e2112dcbf3d26bec25e9 AS build
WORKDIR /src

# The module has no third-party dependencies (stdlib only), so there is no
# go.sum; copying go.mod alone is enough to prime the module cache.
COPY go.mod ./
RUN go mod download

# frontend/dist is committed and embedded via go:embed, so a plain Go build
# produces a self-contained binary with the dashboard inside it.
COPY . .
ARG VERSION=dev
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/kbengine ./cmd/kbengine

# --- runtime stage -----------------------------------------------------------
# distroless/static: no shell, no package manager, runs as a non-root user.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:d093aa3e30dbadd3efe1310db061a14da60299baff8450a17fe0ccc514a16639
COPY --from=build /out/kbengine /kbengine
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/kbengine"]
# Run e.g. `docker run -p 8080:8080 -v $PWD/data:/data kbengine \
#   serve --catalog /data/catalog.json` (catalog is runtime data, not baked in).
