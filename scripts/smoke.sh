#!/usr/bin/env bash
set -euo pipefail

runtime="${RUNTIME:-podman}"
compose=("${runtime}" compose -p pssst-smoke)
# Keep the smoke project independent of a normal demo that is already running.
export PSSST_EXPORTER_PORT="${PSSST_EXPORTER_PORT:-29099}"
export PSSST_PROMETHEUS_PORT="${PSSST_PROMETHEUS_PORT:-29090}"
export PSSST_FAKE_PORT="${PSSST_FAKE_PORT:-28080}"
export PSSST_BLACKBOX_PORT="${PSSST_BLACKBOX_PORT:-29115}"
prometheus_url="http://127.0.0.1:${PSSST_PROMETHEUS_PORT}"
fake_url="http://127.0.0.1:${PSSST_FAKE_PORT}"
for dependency in "${runtime}" curl jq; do
  command -v "${dependency}" >/dev/null || { printf 'missing prerequisite: %s\n' "${dependency}" >&2; exit 1; }
done

cleanup() {
  # Destroy only this test project; never POST to a potentially unrelated host port.
  "${compose[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

wait_for_query() {
  local description="$1"
  local query="$2"
  local deadline=$((SECONDS + 60))
  while (( SECONDS < deadline )); do
    if curl --silent --show-error --fail --get --data-urlencode "query=${query}" "${prometheus_url}/api/v1/query" \
      | jq --exit-status '.status == "success" and (.data.result | length > 0)' >/dev/null; then
      printf 'verified: %s\n' "${description}"
      return 0
    fi
    sleep 1
  done
  printf 'timed out waiting for: %s\n' "${description}" >&2
  return 1
}

"${compose[@]}" up --build --detach --force-recreate

wait_for_query "Prometheus scrapes the exporter" 'up{job="pssst"} == 1'
wait_for_query "initial declared signal is operational" 'psp_declared_operational{psp="demo_payments",component="overall"} == 1'
wait_for_query "initial observed signal succeeds" 'psp_probe_success{psp="demo_payments",endpoint="payment_api"} == 1'

curl --silent --show-error --fail --request POST "${fake_url}/control?incident=true&api_failure=false" >/dev/null
wait_for_query "declared-only incident reaches metrics" 'psp_declared_operational{psp="demo_payments",component="overall"} == 0'
wait_for_query "declared-only incident leaves probe healthy" 'psp_probe_success{psp="demo_payments",endpoint="payment_api"} == 1'

curl --silent --show-error --fail --request POST "${fake_url}/control?incident=false&api_failure=true" >/dev/null
wait_for_query "synthetic API failure reaches metrics" 'psp_probe_success{psp="demo_payments",endpoint="payment_api"} == 0'
wait_for_query "unannounced recording state" 'psp:unannounced_failure{psp="demo_payments"} == 1'

curl --silent --show-error --fail --request POST "${fake_url}/control?incident=true" >/dev/null
wait_for_query "synthetic declared incident reaches metrics" 'psp_declared_operational{psp="demo_payments",component="overall"} == 0'
wait_for_query "confirmed incident recording state" 'psp:confirmed_incident{psp="demo_payments"} == 1'

curl --silent --show-error --fail --request POST "${fake_url}/control?incident=false&api_failure=false" >/dev/null
wait_for_query "synthetic reset restores observed signal" 'psp_probe_success{psp="demo_payments",endpoint="payment_api"} == 1'
printf 'smoke test passed\n'
