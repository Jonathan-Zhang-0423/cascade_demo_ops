#!/usr/bin/env bash
set -euo pipefail

control_port="${CASCADE_DIRECT_CONTROL_PORT:-18443}"
data_start="${CASCADE_DIRECT_DATA_PORT_START:-24000}"
data_end="${CASCADE_DIRECT_DATA_PORT_END:-24031}"

# The public exposure is intentionally fixed. A wider or caller-supplied
# range would undermine per-installation port isolation and could accidentally
# expose the loopback-only Worker API.
if [[ "$control_port" != "18443" || "$data_start" != "24000" || "$data_end" != "24031" ]]; then
  echo "refusing non-standard Browser Agent firewall range; expected TCP 18443 and 24000-24031" >&2
  exit 1
fi
if [[ "$control_port" == "18444" || "$data_start" == "18444" || "$data_end" == "18444" ]]; then
  echo "refusing to expose the loopback-only Worker port 18444" >&2
  exit 1
fi

if [[ ! "$control_port" =~ ^[0-9]+$ || ! "$data_start" =~ ^[0-9]+$ || ! "$data_end" =~ ^[0-9]+$ ]]; then
  echo "Ports must be integers." >&2
  exit 2
fi
if (( control_port < 1024 || control_port > 65535 || data_start < 1024 || data_end > 65535 || data_start > data_end )); then
  echo "Invalid control or data-port range." >&2
  exit 2
fi
if ! command -v ufw >/dev/null 2>&1; then
  echo "ufw is unavailable; configure the provider security group and host firewall manually." >&2
  exit 1
fi

ufw allow "${control_port}/tcp" comment "Cascade Browser Agent control"
ufw allow "${data_start}:${data_end}/tcp" comment "Cascade Browser Agent leased data ports"
echo "Opened TCP ${control_port} and ${data_start}-${data_end}. Worker port 18444 was not opened."
