# syntax=docker/dockerfile:1

# --- web stage ---------------------------------------------------------------
# The dashboard bundle is a build artifact, not a source file, so it is not in
# the repository — the image builds it. Runs on the native BUILDPLATFORM: the
# output is plain static assets, identical for every target architecture.
#
# package.json and the lockfile are copied first so `npm ci` reuses its layer
# whenever only src/ changed.
FROM --platform=$BUILDPLATFORM node:24-bookworm-slim@sha256:235600a8101ab264e117b1768e925532262668dc9b581ef1dd7d96ced463b8e7 AS web
WORKDIR /web
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

# --- build stage -------------------------------------------------------------
# Pinned by digest (Dependabot's docker ecosystem keeps tag+digest current).
# Runs on the native BUILDPLATFORM and cross-compiles to TARGET* so multi-arch
# builds need no QEMU emulation (CGO is disabled).
FROM --platform=$BUILDPLATFORM golang:1.26-bookworm@sha256:1ecb7edf62a0408027bd5729dfd6b1b8766e578e8df93995b225dfd0944eb651 AS build
WORKDIR /src

# The module has no third-party dependencies (stdlib only), so there is no
# go.sum; copying go.mod alone is enough to prime the module cache.
COPY go.mod ./
RUN go mod download

# The bundle comes from the web stage, never from the build context: a stale
# local dist/ must not be able to reach a released image. go:embed then folds it
# into the binary, so the runtime stage stays a single static file.
COPY . .
COPY --from=web /web/dist ./frontend/dist
ARG VERSION=dev
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/kbengine ./cmd/kbengine

# --- runtime stage -----------------------------------------------------------
# distroless/static: no shell, no package manager, runs as a non-root user.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:f5b485ea962d9bd1186b2f6b3a061191539b905b82ec395de78cbfae51f20e35
COPY --from=build /out/kbengine /kbengine
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/kbengine"]
# The server binds loopback by default, which inside a container means the
# published port reaches nothing. In a container the interface is the container's
# boundary, so pass --addr :8080 explicitly:
#
#   docker run -p 8080:8080 -v $PWD/data:/data kbengine \
#     serve --catalog /data/catalog.json --addr :8080
#
# (the catalog is runtime data, not baked into the image).
