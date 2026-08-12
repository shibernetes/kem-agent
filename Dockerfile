# Build the static agent binary, cross-compiling to the target platform so a
# multi-arch build under buildx runs the Go toolchain natively.
FROM --platform=${BUILDPLATFORM} golang:1.27.1@sha256:f44f6e88636cfb311f9ebace870ded69d943f227bb3cb27d32ffd84ea18c43ea AS build

WORKDIR /src

# Resolve modules first, so the dependency layer is cached across source edits.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build metadata for internal/version, passed with --build-arg. Left unset,
# the binary reports its own dev and unknown defaults, since .git stays out
# of the build context and VCS stamping is off.
ARG VERSION
ARG COMMIT
ARG TREE_STATE
ARG BUILD_DATE

ARG TARGETOS
ARG TARGETARCH

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -buildvcs=false \
    -ldflags="-s -w \
-X github.com/shibernetes/kem-agent/internal/version.version=${VERSION} \
-X github.com/shibernetes/kem-agent/internal/version.commit=${COMMIT} \
-X github.com/shibernetes/kem-agent/internal/version.treeState=${TREE_STATE} \
-X github.com/shibernetes/kem-agent/internal/version.date=${BUILD_DATE}" \
    -o /out/agent ./cmd/agent

FROM gcr.io/distroless/static:nonroot@sha256:1c2c046bc09ed40fad370b599a0b1ae7987f55b01e247cf27a7c27cd97e5bbc7

# Defaulted here, unlike in the build stage, so the labels match what the
# binary reports when no build argument is passed.
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

LABEL org.opencontainers.image.title="kem-agent" \
      org.opencontainers.image.description="Kubernetes Events collection and forwarding agent" \
      org.opencontainers.image.source="https://github.com/shibernetes/kem-agent" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.base.name="gcr.io/distroless/static:nonroot"

COPY --from=build /out/agent /usr/local/bin/agent

# A numeric user, so the kubelet can verify runAsNonRoot without a runAsUser
# in the pod spec. The distroless nonroot user is 65532.
USER 65532:65532

ENTRYPOINT ["/usr/local/bin/agent"]
CMD ["-c", "/etc/kem-agent/config.yaml"]
