#!/usr/bin/env bash
# Captures docs/media/grafana-overview.png from the offline demo: four
# fictional providers, one healthy, one failing unannounced, one confirmed
# incident and one declared incident, through the real exporter, Prometheus
# rules and the grafana.com dashboard. Never point it at real data.
#
#   RUNTIME=docker CHROME=/path/to/chrome-headless-shell scripts/capture-dashboard.sh
#
# Needs a container engine (RUNTIME, podman by default), curl, jq and a
# Chromium: chrome-headless-shell preferred, as for scripts/build-brand.sh.
# BUILD=0 reuses localhost/pssst:dev and localhost/pssst-fake:dev instead of
# building them. OUTPUT overrides the destination.
set -euo pipefail

cd "$(dirname "$0")/.."

runtime="${RUNTIME:-podman}"
compose=("${runtime}" compose -p pssst-capture -f compose.yaml -f scripts/capture/compose.yaml)
output="${OUTPUT:-docs/media/grafana-overview.png}"
# Its own ports, so a running demo or smoke test is left alone.
export PSSST_EXPORTER_PORT="${PSSST_EXPORTER_PORT:-39099}"
export PSSST_PROMETHEUS_PORT="${PSSST_PROMETHEUS_PORT:-39090}"
export PSSST_FAKE_PORT="${PSSST_FAKE_PORT:-38080}"
export PSSST_BLACKBOX_PORT="${PSSST_BLACKBOX_PORT:-39115}"
export PSSST_CONTOSO_PORT="${PSSST_CONTOSO_PORT:-38081}"
export PSSST_FABRIKAM_PORT="${PSSST_FABRIKAM_PORT:-38082}"
export PSSST_TAILSPIN_PORT="${PSSST_TAILSPIN_PORT:-38083}"
export PSSST_GRAFANA_PORT="${PSSST_GRAFANA_PORT:-33000}"
prometheus_url="http://127.0.0.1:${PSSST_PROMETHEUS_PORT}"
grafana_url="http://127.0.0.1:${PSSST_GRAFANA_PORT}"

chrome=${CHROME:-}
if [[ -z $chrome ]]; then
  for candidate in chrome-headless-shell chromium chromium-browser google-chrome google-chrome-stable; do
    if command -v "$candidate" >/dev/null; then chrome=$candidate; break; fi
  done
fi
[[ -n $chrome ]] || { echo 'missing prerequisite: a Chromium; set CHROME=/path/to/chrome' >&2; exit 1; }
for dependency in "${runtime}" curl jq; do
  command -v "${dependency}" >/dev/null || { printf 'missing prerequisite: %s\n' "${dependency}" >&2; exit 1; }
done

tmp=$(mktemp -d)
cleanup() {
  "${compose[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
  rm -rf "$tmp"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

wait_for() {
  local description="$1" deadline=$((SECONDS + 120))
  shift
  until "$@" >/dev/null 2>&1; do
    (( SECONDS < deadline )) || { printf 'timed out waiting for: %s\n' "$description" >&2; return 1; }
    sleep 1
  done
  printf 'ready: %s\n' "$description"
}

query_has_result() {
  curl --silent --fail --get --data-urlencode "query=$1" "${prometheus_url}/api/v1/query" \
    | jq --exit-status '.data.result | length > 0'
}

control() {
  curl --silent --show-error --fail --request POST "http://127.0.0.1:$1/control?$2" >/dev/null
}

build=(--build)
[[ ${BUILD:-1} == 0 ]] && build=()
"${compose[@]}" up "${build[@]}" --detach --force-recreate

wait_for "fake providers" curl --silent --fail "http://127.0.0.1:${PSSST_TAILSPIN_PORT}/control"
control "${PSSST_CONTOSO_PORT}" 'api_failure=true&incident=false'
control "${PSSST_FABRIKAM_PORT}" 'api_failure=true&incident=true'
control "${PSSST_TAILSPIN_PORT}" 'api_failure=false&incident=true'

wait_for "unannounced failure (Contoso)" query_has_result 'psp:unannounced_failure{psp="contoso_acquiring"} == 1'
wait_for "confirmed incident (Fabrikam)" query_has_result 'psp:confirmed_incident{psp="fabrikam_bank"} == 1'
wait_for "declared incident (Tailspin)" query_has_result 'psp:declared_unavailable{psp="tailspin_payments"} == 1'
wait_for "healthy provider (Northwind)" query_has_result 'psp_probe_success{psp="northwind_pay"} == 1'

wait_for "Grafana" curl --silent --fail "${grafana_url}/api/health"
curl --silent --show-error --fail --request POST --header 'Content-Type: application/json' \
  --data '{"name":"Prometheus","type":"prometheus","uid":"prometheus","url":"http://prometheus:9090","access":"proxy","isDefault":true}' \
  "${grafana_url}/api/datasources" >/dev/null
# The import API is the path a grafana.com import takes: the dashboard's
# datasource input is answered with the one just created.
jq '{dashboard: ., overwrite: true,
     inputs: [{name: "DS_PROMETHEUS", type: "datasource", pluginId: "prometheus", value: "prometheus"}]}' \
  deploy/grafana/pssst-grafana-com.json > "$tmp/import.json"
uid=$(curl --silent --show-error --fail --request POST --header 'Content-Type: application/json' \
  --data-binary @"$tmp/import.json" "${grafana_url}/api/dashboards/import" | jq --raw-output .uid)

# Grafana refuses to start in a browser whose locale is POSIX, so set one.
mkdir -p "$(dirname "$output")"
LANG=en_GB.UTF-8 "$chrome" --headless --disable-gpu --no-sandbox --hide-scrollbars \
  --lang=en-GB --force-device-scale-factor=1 --window-size=1600,1000 \
  --virtual-time-budget=20000 --user-data-dir="$tmp/profile" \
  --screenshot="$output" "${grafana_url}/d/${uid}?orgId=1&from=now-15m&to=now&kiosk" >/dev/null 2>&1
printf 'captured: %s\n' "$output"
