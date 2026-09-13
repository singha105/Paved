# The paved demo app: a minimal HTTP service that meets the platform's workload contract.
# Build from the repository root: docker build -f demo/app/Dockerfile .
ARG BASE_IMAGE=golang:1.26
FROM ${BASE_IMAGE} AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace
# Relies on the repository's .dockerignore, which admits only Go sources and module files.
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -o demo-app ./demo/app

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /workspace/demo-app /demo-app
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/demo-app"]
