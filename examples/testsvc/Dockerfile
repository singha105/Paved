# The paved test service: a minimal HTTP service that meets the platform's service contract.
# Build from the repository root: docker build -f examples/testsvc/Dockerfile .
ARG BASE_IMAGE=golang:1.26
FROM ${BASE_IMAGE} AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace
# Relies on the repository's .dockerignore, which admits only Go sources and module files.
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -o testsvc ./examples/testsvc

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /workspace/testsvc /testsvc
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/testsvc"]
