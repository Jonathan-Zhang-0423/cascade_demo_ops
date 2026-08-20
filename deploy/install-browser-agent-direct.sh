#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
Usage: sudo ./deploy/install-browser-agent-direct.sh \
  --gateway-bin PATH --gateway-sha256 HEX \
  --worker-bin PATH --worker-sha256 HEX \
  --video-worker-dir PATH --playwright-browsers-dir PATH \
  --tls-cert PATH --tls-key PATH \
  --advertised-host DNS_NAME [--data-port-start 24000 --data-port-end 24031]

The script installs but does not change UFW or cloud security groups. It never
prints generated bootstrap or Worker tokens.
EOF
  exit 2
}

[[ "${EUID}" -eq 0 ]] || { echo "run this installer as root" >&2; exit 1; }

gateway_bin=""; gateway_sha=""; worker_bin=""; worker_sha=""
video_worker_dir=""; playwright_browsers_dir=""; tls_cert=""; tls_key=""; advertised_host=""
data_start=24000; data_end=24031
while (($#)); do
  case "$1" in
    --gateway-bin) gateway_bin="${2:-}"; shift 2 ;;
    --gateway-sha256) gateway_sha="${2:-}"; shift 2 ;;
    --worker-bin) worker_bin="${2:-}"; shift 2 ;;
    --worker-sha256) worker_sha="${2:-}"; shift 2 ;;
    --video-worker-dir) video_worker_dir="${2:-}"; shift 2 ;;
    --playwright-browsers-dir) playwright_browsers_dir="${2:-}"; shift 2 ;;
    --tls-cert) tls_cert="${2:-}"; shift 2 ;;
    --tls-key) tls_key="${2:-}"; shift 2 ;;
    --advertised-host) advertised_host="${2:-}"; shift 2 ;;
    --data-port-start) data_start="${2:-}"; shift 2 ;;
    --data-port-end) data_end="${2:-}"; shift 2 ;;
    *) usage ;;
  esac
done

for required in gateway_bin gateway_sha worker_bin worker_sha video_worker_dir playwright_browsers_dir tls_cert tls_key advertised_host; do
  [[ -n "${!required}" ]] || usage
done
[[ "$gateway_sha" =~ ^[0-9a-fA-F]{64}$ && "$worker_sha" =~ ^[0-9a-fA-F]{64}$ ]] || { echo "invalid expected SHA-256" >&2; exit 2; }
[[ "$advertised_host" =~ ^[A-Za-z0-9.-]+$ && "$advertised_host" != .* && "$advertised_host" != *. ]] || { echo "advertised host must be a DNS name" >&2; exit 2; }
[[ "$data_start" =~ ^[0-9]+$ && "$data_end" =~ ^[0-9]+$ ]] || { echo "data ports must be integers" >&2; exit 2; }
((data_start >= 1024 && data_end <= 65535 && data_start <= data_end)) || { echo "invalid data-port range" >&2; exit 2; }

for file in "$gateway_bin" "$worker_bin" "$tls_cert" "$tls_key"; do [[ -f "$file" ]] || { echo "missing file: $file" >&2; exit 1; }; done
for file in "$video_worker_dir/dist/index.js" "$video_worker_dir/package.json"; do [[ -f "$file" ]] || { echo "incomplete video-worker directory: $file" >&2; exit 1; }; done
[[ -d "$video_worker_dir/node_modules/playwright" ]] || { echo "video-worker node_modules/playwright is missing" >&2; exit 1; }
[[ -d "$playwright_browsers_dir" ]] || { echo "Playwright browser directory is missing" >&2; exit 1; }
find "$playwright_browsers_dir" -maxdepth 3 -type f -name chrome -perm -u+x -print -quit | grep -q . || { echo "no executable Chromium was found in the Playwright browser directory" >&2; exit 1; }

actual_gateway_sha="$(sha256sum "$gateway_bin" | awk '{print $1}')"
actual_worker_sha="$(sha256sum "$worker_bin" | awk '{print $1}')"
[[ "${actual_gateway_sha,,}" == "${gateway_sha,,}" ]] || { echo "gateway SHA-256 mismatch" >&2; exit 1; }
[[ "${actual_worker_sha,,}" == "${worker_sha,,}" ]] || { echo "worker SHA-256 mismatch" >&2; exit 1; }
openssl x509 -in "$tls_cert" -noout -checkhost "$advertised_host" >/dev/null 2>&1 || { echo "TLS certificate does not cover advertised host" >&2; exit 1; }
openssl x509 -in "$tls_cert" -noout -checkend 604800 >/dev/null 2>&1 || { echo "TLS certificate expires in less than seven days" >&2; exit 1; }
cert_key_hash="$(openssl x509 -in "$tls_cert" -pubkey -noout | openssl pkey -pubin -outform DER 2>/dev/null | sha256sum | awk '{print $1}')"
private_key_hash="$(openssl pkey -in "$tls_key" -pubout -outform DER 2>/dev/null | sha256sum | awk '{print $1}')"
[[ -n "$cert_key_hash" && "$cert_key_hash" == "$private_key_hash" ]] || { echo "TLS certificate and private key do not match" >&2; exit 1; }

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
for source in "$script_dir/systemd/cascade-browser-agent-gateway.service" "$script_dir/systemd/cascade-browser-agent-worker.service" "$script_dir/browser-agent-worker-preflight.sh" "$script_dir/preflight-browser-agent-direct.sh"; do
  [[ -f "$source" ]] || { echo "deployment source missing: $source" >&2; exit 1; }
done

if ! id cascade-browser-gateway >/dev/null 2>&1; then
  useradd --system --home /var/lib/cascade-browser-agent/gateway --shell /usr/sbin/nologin cascade-browser-gateway
fi
if ! id cascade-browser-worker >/dev/null 2>&1; then
  useradd --system --home /var/lib/cascade-browser-agent/worker --shell /usr/sbin/nologin cascade-browser-worker
fi
install -d -o root -g root -m 0755 /var/lib/cascade-browser-agent /opt/cascade-browser-agent /etc/cascade-browser-agent
install -d -o cascade-browser-gateway -g cascade-browser-gateway -m 0700 /var/lib/cascade-browser-agent/gateway
install -d -o cascade-browser-worker -g cascade-browser-worker -m 0700 /var/lib/cascade-browser-agent/worker
install -d -o root -g cascade-browser-gateway -m 0750 /etc/cascade-browser-agent/tls
install -d -o root -g root -m 0755 /usr/local/libexec

install -o root -g root -m 0755 "$gateway_bin" /usr/local/bin/cascade-browser-agent-gateway
install -o root -g root -m 0755 "$worker_bin" /usr/local/bin/cascade-browser-agent-direct-worker
install -o root -g root -m 0755 "$script_dir/browser-agent-worker-preflight.sh" /usr/local/libexec/cascade-browser-agent-worker-preflight
install -o root -g root -m 0755 "$script_dir/preflight-browser-agent-direct.sh" /usr/local/libexec/cascade-browser-agent-direct-preflight
install -o root -g root -m 0644 "$script_dir/systemd/cascade-browser-agent-gateway.service" /etc/systemd/system/cascade-browser-agent-gateway.service
install -o root -g root -m 0644 "$script_dir/systemd/cascade-browser-agent-worker.service" /etc/systemd/system/cascade-browser-agent-worker.service
install -o root -g cascade-browser-gateway -m 0640 "$tls_cert" /etc/cascade-browser-agent/tls/fullchain.pem
install -o root -g cascade-browser-gateway -m 0640 "$tls_key" /etc/cascade-browser-agent/tls/privkey.pem

staged_worker="/opt/cascade-browser-agent/video-worker.new.$$"
staged_browsers="/opt/cascade-browser-agent/playwright-browsers.new.$$"
cleanup_staging() {
  [[ "$staged_worker" == /opt/cascade-browser-agent/video-worker.new.* ]] && rm -rf -- "$staged_worker"
  [[ "$staged_browsers" == /opt/cascade-browser-agent/playwright-browsers.new.* ]] && rm -rf -- "$staged_browsers"
}
trap cleanup_staging EXIT
install -d -o root -g cascade-browser-worker -m 0750 "$staged_worker"
cp -a -- "$video_worker_dir/dist" "$video_worker_dir/node_modules" "$video_worker_dir/package.json" "$staged_worker/"
chown -R root:cascade-browser-worker "$staged_worker"
# Source build directories may be mode 0700. Normalize traversal/read access
# for the dedicated Worker group without granting any access to other users;
# preserve execute only for files that were executable in the source.
chmod -R u=rwX,g=rX,o= "$staged_worker"
install -d -o root -g cascade-browser-worker -m 0750 "$staged_browsers"
cp -a -- "$playwright_browsers_dir"/. "$staged_browsers/"
chown -R root:cascade-browser-worker "$staged_browsers"
chmod -R u=rwX,g=rX,o= "$staged_browsers"
backup_suffix="$(date -u +%Y%m%dT%H%M%SZ)"
if [[ -e /opt/cascade-browser-agent/video-worker ]]; then
  previous_worker="/opt/cascade-browser-agent/video-worker.previous.${backup_suffix}"
  mv -- /opt/cascade-browser-agent/video-worker "$previous_worker"
fi
mv -- "$staged_worker" /opt/cascade-browser-agent/video-worker
if [[ -e /opt/cascade-browser-agent/playwright-browsers ]]; then
  previous_browsers="/opt/cascade-browser-agent/playwright-browsers.previous.${backup_suffix}"
  mv -- /opt/cascade-browser-agent/playwright-browsers "$previous_browsers"
fi
mv -- "$staged_browsers" /opt/cascade-browser-agent/playwright-browsers
trap - EXIT

read_existing_secret() {
  local file="$1" key="$2" value=""
  if [[ -r "$file" ]]; then
    value="$(sed -n "s/^${key}=//p" "$file" | tail -n 1)"
  fi
  if ((${#value} < 32)); then value="$(openssl rand -base64 48)"; fi
  printf '%s' "$value"
}
bootstrap_token="$(read_existing_secret /etc/cascade-browser-agent/gateway.env CASCADE_DIRECT_BOOTSTRAP_TOKEN)"
worker_token="$(read_existing_secret /etc/cascade-browser-agent/gateway.env CASCADE_DIRECT_WORKER_TOKEN)"
[[ "$bootstrap_token" != "$worker_token" ]] || worker_token="$(openssl rand -base64 48)"

gateway_tmp="$(mktemp /etc/cascade-browser-agent/gateway.env.XXXXXX)"
worker_tmp="$(mktemp /etc/cascade-browser-agent/worker.env.XXXXXX)"
chmod 0600 "$gateway_tmp" "$worker_tmp"
cat >"$gateway_tmp" <<EOF
CASCADE_DIRECT_CONTROL_ADDR=0.0.0.0:18443
CASCADE_DIRECT_WORKER_ADDR=127.0.0.1:18444
CASCADE_DIRECT_DATA_BIND_HOST=0.0.0.0
CASCADE_DIRECT_ADVERTISED_HOST=$advertised_host
CASCADE_DIRECT_DATA_PORT_START=$data_start
CASCADE_DIRECT_DATA_PORT_END=$data_end
CASCADE_DIRECT_LEASE_TTL=30m
CASCADE_DIRECT_SPOOL_ROOT=/var/lib/cascade-browser-agent/gateway
CASCADE_DIRECT_TLS_CERT=/etc/cascade-browser-agent/tls/fullchain.pem
CASCADE_DIRECT_TLS_KEY=/etc/cascade-browser-agent/tls/privkey.pem
CASCADE_DIRECT_BOOTSTRAP_TOKEN=$bootstrap_token
CASCADE_DIRECT_WORKER_TOKEN=$worker_token
EOF
cat >"$worker_tmp" <<EOF
CASCADE_DIRECT_WORKER_GATEWAY_URL=http://127.0.0.1:18444
CASCADE_DIRECT_WORKER_TOKEN=$worker_token
CASCADE_DIRECT_WORKER_OUTPUT_ROOT=/var/lib/cascade-browser-agent/worker
CASCADE_DIRECT_WORKER_POLL_INTERVAL=2s
CASCADE_DIRECT_WORKER_RUN_TIMEOUT=45m
CASCADE_PROFILE=cloud
APP_MODE=web
CASCADE_DATA_ROOT=/var/lib/cascade-browser-agent/worker/state
CASCADE_ARTIFACT_ROOT=/var/lib/cascade-browser-agent/worker
CASCADE_CACHE_ROOT=/var/lib/cascade-browser-agent/worker/cache
CASCADE_LOG_ROOT=/var/lib/cascade-browser-agent/worker/logs
NODE_BINARY_PATH=/usr/bin/node
NODE_WORKER_PATH=/opt/cascade-browser-agent/video-worker/dist/index.js
PLAYWRIGHT_BROWSERS_PATH=/opt/cascade-browser-agent/playwright-browsers
CASCADE_FFMPEG_PATH=/usr/bin/ffmpeg
CASCADE_FFPROBE_PATH=/usr/bin/ffprobe
EOF
mv -f -- "$gateway_tmp" /etc/cascade-browser-agent/gateway.env
mv -f -- "$worker_tmp" /etc/cascade-browser-agent/worker.env
chown root:root /etc/cascade-browser-agent/gateway.env /etc/cascade-browser-agent/worker.env
chmod 0600 /etc/cascade-browser-agent/gateway.env /etc/cascade-browser-agent/worker.env
unset bootstrap_token worker_token

if grep -q '^CASCADE_DIRECT_BOOTSTRAP_TOKEN=' /etc/cascade-browser-agent/worker.env; then
  echo "refusing to start: bootstrap token leaked into Worker environment" >&2
  exit 1
fi

systemctl daemon-reload
systemctl enable cascade-browser-agent-gateway.service cascade-browser-agent-worker.service >/dev/null
systemctl restart cascade-browser-agent-gateway.service cascade-browser-agent-worker.service
echo "Browser Agent direct services installed. Tokens were not printed."
echo "Firewall/security-group changes remain manual: TCP 18443 and ${data_start}-${data_end}; never expose 18444."
echo "Run: sudo /usr/local/libexec/cascade-browser-agent-direct-preflight"
