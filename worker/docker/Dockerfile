# syntax=docker/dockerfile:1.7

# Matches go.mod's `go` directive.
ARG GO_VERSION=1.27

# ----- STAGE 1: deps (cached module download) -----
# BUILDPLATFORM keeps this stage native, cross-compiling for TARGETARCH.
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS deps
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

# ----- STAGE 2: build -----
FROM deps AS build
ARG TARGETOS
ARG TARGETARCH
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/worker ./worker/cmd/worker

# ----- STAGE 3: runtime (distroless nonroot, no shell/exec surface) -----
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/worker /worker
USER nonroot:nonroot
ENTRYPOINT ["/worker"]
