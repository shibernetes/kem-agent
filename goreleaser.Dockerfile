# This file builds the image published by a release.
#
# goreleaser compiles the agent for each platform first, then builds this file from
# a temporary directory holding one binary per platform, at linux/amd64/agent and
# linux/arm64/agent. TARGETPLATFORM selects the binary for the image being built,
# and the image labels are set in .goreleaser.yaml.
#
# Nothing is compiled here, so this file cannot build from the repository root.
# To build an image from source, use the Dockerfile beside it.
FROM gcr.io/distroless/static:nonroot@sha256:1c2c046bc09ed40fad370b599a0b1ae7987f55b01e247cf27a7c27cd97e5bbc7

ARG TARGETPLATFORM

COPY ${TARGETPLATFORM}/agent /usr/local/bin/agent

# A numeric user, so the kubelet can verify runAsNonRoot without a runAsUser
# in the pod spec. The distroless nonroot user is 65532.
USER 65532:65532

ENTRYPOINT ["/usr/local/bin/agent"]
CMD ["-c", "/etc/kem-agent/config.yaml"]
