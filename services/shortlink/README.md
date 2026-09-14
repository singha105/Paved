# shortlink

A small URL shortener, and the second service onboarded onto paved. It is a separate Go module with
its own image. Nothing in it knows about the platform beyond the
[service contract](../../README.md#the-service-contract).

## API

```bash
curl -s -X POST localhost:8080/links -d '{"url": "https://example.com/docs"}'
# {"code":"q7XmP2a","short":"/q7XmP2a","url":"https://example.com/docs"}

curl -si localhost:8080/q7XmP2a
# HTTP/1.1 302 Found
# Location: https://example.com/docs
```

| Request | Answers |
|---|---|
| `POST /links` with `{"url": "<absolute http or https URL>"}` | `201` with the code; `400` for anything else |
| `GET /{code}` | `302` to the stored URL; `404` for an unknown code |
| `GET /healthz`, `GET /readyz` | `200`, for the platform's probes |
| `GET /metrics` | Prometheus metrics |

Links are kept in memory: they don't survive a restart and aren't shared between replicas.

## How it meets the service contract

- `http_requests_total{code,method}` and `http_request_duration_seconds{code,method}` cover the
  application endpoints only, not the probes or the metrics endpoint.
- The series for every status code it can answer are created at zero before it serves, so the
  first failure after a start counts. `TestEveryStatusSeriesExistsBeforeAnyRequest` fails if one
  is missing.
- It runs as UID 65532 on distroless, writes nothing to disk and listens on 8080.

## Build and run

```bash
go test ./...
go run . --addr :8080
../../hack/shortlink-image.sh     # build and push k3d-paved-registry:5001/shortlink:0.1.0
```

Its claim, [`deploy/claims/shortlink.yaml`](../../deploy/claims/shortlink.yaml), is everything the
team writes to run it on the platform.
