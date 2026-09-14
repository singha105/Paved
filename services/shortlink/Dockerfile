# shortlink: a URL shortener, and the second service onboarded onto paved.
# Build from this directory: docker build -t shortlink .
ARG BASE_IMAGE=golang:1.26
FROM ${BASE_IMAGE} AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -o /shortlink .

# Distroless, non-root (UID 65532), no shell: what the platform's pod security settings expect.
FROM gcr.io/distroless/static:nonroot
COPY --from=builder /shortlink /shortlink
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/shortlink"]
