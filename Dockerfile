# syntax=docker/dockerfile:1

# Build on the machine's own architecture and cross-compile, so arm64 images build fast on amd64 runners.
FROM --platform=$BUILDPLATFORM golang:1.27 AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/lootgobbler ./cmd/lootgobbler \
 && mkdir -p /out/data

# distroless/static: no shell or package manager, just CA certificates, time zones and a non-root user.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/lootgobbler /usr/local/bin/lootgobbler
COPY --from=build --chown=nonroot:nonroot /out/data /data
ENV DATA_DIR=/data \
    PORT=8080
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["lootgobbler", "health"]
ENTRYPOINT ["lootgobbler"]
CMD ["serve"]
