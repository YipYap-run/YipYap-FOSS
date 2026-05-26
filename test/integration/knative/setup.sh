#!/usr/bin/env bash
# setup.sh, idempotent kind + Knative Eventing bootstrap for yipyap
# integration tests.
#
# Usage:
#   ./test/integration/knative/setup.sh up        (default)
#   ./test/integration/knative/setup.sh teardown
#
# Requirements on PATH: kind, kubectl, docker.
# Pinned Knative Eventing release: v1.21.0.

set -euo pipefail

CLUSTER_NAME="${YIPYAP_KNATIVE_CLUSTER:-yipyap-knative}"
KNATIVE_VERSION="${YIPYAP_KNATIVE_VERSION:-v1.21.0}"
EVENTING_BASE="https://github.com/knative/eventing/releases/download/knative-${KNATIVE_VERSION}"

log() { printf '[knative-setup] %s\n' "$*"; }
die() { printf '[knative-setup] ERROR: %s\n' "$*" >&2; exit 1; }

require_bin() {
  command -v "$1" >/dev/null 2>&1 || die "required binary not found on PATH: $1"
}

cmd_up() {
  require_bin kind
  require_bin kubectl
  require_bin docker

  if kind get clusters 2>/dev/null | grep -qx "$CLUSTER_NAME"; then
    log "kind cluster '$CLUSTER_NAME' already exists; reusing"
  else
    log "creating kind cluster '$CLUSTER_NAME'"
    kind create cluster --name "$CLUSTER_NAME" --wait 120s
  fi

  # Point kubectl at this cluster's context for the remainder of the script.
  kubectl config use-context "kind-${CLUSTER_NAME}" >/dev/null

  log "installing Knative Eventing CRDs (${KNATIVE_VERSION})"
  kubectl apply -f "${EVENTING_BASE}/eventing-crds.yaml"

  log "installing Knative Eventing core"
  kubectl apply -f "${EVENTING_BASE}/eventing-core.yaml"

  log "installing InMemoryChannel"
  kubectl apply -f "${EVENTING_BASE}/in-memory-channel.yaml"

  log "installing MTChannelBasedBroker"
  kubectl apply -f "${EVENTING_BASE}/mt-channel-broker.yaml"

  log "waiting for knative-eventing pods to be Ready (up to 5 minutes)"
  # `kubectl wait` fails if zero pods match; give controllers a moment to schedule.
  for _ in $(seq 1 30); do
    if [[ "$(kubectl get pods -n knative-eventing -o name 2>/dev/null | wc -l)" -gt 0 ]]; then
      break
    fi
    sleep 2
  done
  kubectl wait --for=condition=ready pod --all -n knative-eventing --timeout=300s

  log "Knative Eventing ${KNATIVE_VERSION} ready on kind cluster '${CLUSTER_NAME}'"
}

cmd_teardown() {
  require_bin kind
  if kind get clusters 2>/dev/null | grep -qx "$CLUSTER_NAME"; then
    log "deleting kind cluster '$CLUSTER_NAME'"
    kind delete cluster --name "$CLUSTER_NAME"
  else
    log "no kind cluster '$CLUSTER_NAME' to delete"
  fi
}

subcmd="${1:-up}"
case "$subcmd" in
  up) cmd_up ;;
  teardown|down) cmd_teardown ;;
  *)
    die "unknown subcommand: $subcmd (expected 'up' or 'teardown')"
    ;;
esac
