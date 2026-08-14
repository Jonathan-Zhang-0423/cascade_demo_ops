#!/usr/bin/env bash
set -uo pipefail

[[ "${EUID}" -eq 0 ]] || { echo '{"ok":false,"error_class":"root_required"}'; exit 1; }
gateway_env=/etc/cascade-browser-agent/gateway.env
worker_env=/etc/cascade-browser-agent/worker.env
if [[ ! -r "$gateway_env" || ! -r "$worker_env" ]]; then
  echo '{"ok":false,"error_class":"configuration_missing"}'
  exit 1
fi

set -a
# Files are root-owned mode 0600 and contain deployment-controlled assignments.
source "$gateway_env"
source "$worker_env"
set +a

host="${CASCADE_DIRECT_ADVERTISED_HOST:-}"
cert="${CASCADE_DIRECT_TLS_CERT:-}"
control_port="${CASCADE_DIRECT_CONTROL_ADDR##*:}"
data_start="${CASCADE_DIRECT_DATA_PORT_START:-24000}"
data_end="${CASCADE_DIRECT_DATA_PORT_END:-24031}"
ok=true
configuration_isolated=true; service_accounts_isolated=false
gateway_active=false; worker_active=false; control_listening=false; worker_loopback_only=false
tls_hostname=false; tls_expiry=false; worker_runtime=false; health_authenticated=false; dns_resolves=false

grep -q '^CASCADE_DIRECT_BOOTSTRAP_TOKEN=' "$worker_env" && configuration_isolated=false
grep -q '^CASCADE_DIRECT_TLS_KEY=' "$worker_env" && configuration_isolated=false
[[ "$(stat -c '%a' "$gateway_env" 2>/dev/null)" == 600 && "$(stat -c '%a' "$worker_env" 2>/dev/null)" == 600 ]] || configuration_isolated=false
gateway_user="$(systemctl show -p User --value cascade-browser-agent-gateway.service 2>/dev/null)"
worker_user="$(systemctl show -p User --value cascade-browser-agent-worker.service 2>/dev/null)"
[[ "$gateway_user" == cascade-browser-gateway && "$worker_user" == cascade-browser-worker && "$gateway_user" != "$worker_user" ]] && service_accounts_isolated=true
systemctl is-active --quiet cascade-browser-agent-gateway.service && gateway_active=true
systemctl is-active --quiet cascade-browser-agent-worker.service && worker_active=true
ss -lntH 2>/dev/null | awk '{print $4}' | grep -Eq ":${control_port}$" && control_listening=true
worker_listeners="$(ss -lntH 2>/dev/null | awk '{print $4}' | grep ':18444$' || true)"
[[ -n "$worker_listeners" && "$worker_listeners" != *"0.0.0.0:18444"* && "$worker_listeners" != *"[::]:18444"* ]] && worker_loopback_only=true
getent ahosts "$host" >/dev/null 2>&1 && dns_resolves=true
openssl x509 -in "$cert" -noout -checkhost "$host" >/dev/null 2>&1 && tls_hostname=true
openssl x509 -in "$cert" -noout -checkend 604800 >/dev/null 2>&1 && tls_expiry=true
if runuser -u cascade-browser-worker -- env \
  NODE_BINARY_PATH="${NODE_BINARY_PATH:-/usr/bin/node}" \
  NODE_WORKER_PATH="${NODE_WORKER_PATH:-/opt/cascade-browser-agent/video-worker/dist/index.js}" \
  PLAYWRIGHT_BROWSERS_PATH="${PLAYWRIGHT_BROWSERS_PATH:-/opt/cascade-browser-agent/playwright-browsers}" \
  CASCADE_FFMPEG_PATH="${CASCADE_FFMPEG_PATH:-/usr/bin/ffmpeg}" \
  CASCADE_FFPROBE_PATH="${CASCADE_FFPROBE_PATH:-/usr/bin/ffprobe}" \
  /usr/local/libexec/cascade-browser-agent-worker-preflight >/dev/null 2>&1; then worker_runtime=true; fi
if curl --fail --silent --show-error --max-time 10 --resolve "${host}:${control_port}:127.0.0.1" -H "Authorization: Bearer ${CASCADE_DIRECT_BOOTSTRAP_TOKEN}" "https://${host}:${control_port}/v1/direct/health" >/dev/null 2>&1; then health_authenticated=true; fi
occupied_data_ports="$(ss -lntH 2>/dev/null | awk -v start="$data_start" -v end="$data_end" '{n=split($4,a,":"); p=a[n]+0; if (p>=start && p<=end) count++} END {print count+0}')"

for value in "$configuration_isolated" "$service_accounts_isolated" "$gateway_active" "$worker_active" "$control_listening" "$worker_loopback_only" "$dns_resolves" "$tls_hostname" "$tls_expiry" "$worker_runtime" "$health_authenticated"; do
  [[ "$value" == true ]] || ok=false
done

printf '{"ok":%s,"configuration_isolated":%s,"service_accounts_isolated":%s,"gateway_active":%s,"worker_active":%s,"control_listening":%s,"worker_loopback_only":%s,"dns_resolves":%s,"tls_hostname_valid":%s,"tls_valid_over_7d":%s,"worker_runtime":%s,"authenticated_health":%s,"data_port_range":"%s-%s","occupied_data_ports":%s}\n' \
  "$ok" "$configuration_isolated" "$service_accounts_isolated" "$gateway_active" "$worker_active" "$control_listening" "$worker_loopback_only" "$dns_resolves" "$tls_hostname" "$tls_expiry" "$worker_runtime" "$health_authenticated" "$data_start" "$data_end" "$occupied_data_ports"
[[ "$ok" == true ]]
