# Knative Eventing integration harness

End-to-end integration tests that spin up a real [Knative Eventing]
installation in a local [kind] cluster and exercise yipyap's
CloudEvents plumbing against it.

These tests are **gated twice** and do NOT run in the normal
`go test ./...` flow:

1. Build tag `knative` (files start with `//go:build knative`), without
   `-tags knative` the compiler never sees them.
2. Runtime env var `KNATIVE=1`, without it every test skips.

## What is tested

- **Outbound** (`outbound_test.go`): the `cloudevent_http` provider POSTs a
  real CloudEvent through a Knative `Broker` + `Trigger` and the test
  asserts the event reaches an `event-display` sink in-cluster.
- **Inbound** (`inbound_test.go`): wire-compatibility of the
  `POST /api/v1/cloudevents/ingest/{token}` handler against the exact
  binary-mode HTTP shape Knative emits. Full cluster-to-host routing is
  deferred to a future harness improvement (see note in the test).

## Requirements

- [`kind`](https://kind.sigs.k8s.io/) on PATH
- `kubectl` on PATH
- `docker` (or a compatible runtime) reachable by kind
- Go 1.22+

### Pinned versions

| Tool             | Version   |
| ---------------- | --------- |
| Knative Eventing | `v1.21.0` |

The Knative release is pinned in `setup.sh` and can be overridden via the
`YIPYAP_KNATIVE_VERSION` env var if needed. The kind cluster name is
`yipyap-knative` and can be overridden with `YIPYAP_KNATIVE_CLUSTER`.

## Running

From the repo root:

```bash
# One-time setup, creates the kind cluster and installs Knative Eventing.
./test/integration/knative/setup.sh up

# Run the integration tests (both build-tag AND env-var gated).
KNATIVE=1 go test -tags knative ./test/integration/knative/... -v

# Tear down when done.
./test/integration/knative/setup.sh teardown
```

`setup.sh` is idempotent: re-running `up` against an existing cluster is
safe, it just re-applies the Knative YAML and waits.

## CI

A separate `knative-integration` job in `.github/workflows/ci.yml` runs
these tests only when the PR carries the `knative` label. Without the
label the job is skipped. The job is `continue-on-error: true` until
kind-on-hosted-runners is shown to be stable enough for blocking gates.

## Troubleshooting

- **"failed to create cluster"**: ensure Docker is running and the user
  can run containers (`docker ps`). kind does not require privileged
  containers but does require a working Docker daemon.
- **Pods never Ready**: `kubectl get pods -n knative-eventing` and
  `kubectl describe pod <pod>`. Image pulls can be slow on first run.
- **event-display logs empty**: check that the `Broker` and `Trigger`
  are `Ready`:
  `kubectl get broker,trigger -A`. Broker takes ~30s to become Ready.
