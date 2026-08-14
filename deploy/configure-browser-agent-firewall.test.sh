#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
script="$script_dir/configure-browser-agent-firewall.sh"

assert_rejected() {
  if env "$@" bash "$script" >/dev/null 2>&1; then
    echo "expected firewall configuration to be rejected" >&2
    exit 1
  fi
}

# The script exits before invoking ufw when the range is unsafe or the worker
# port is selected. Use a missing ufw binary to keep this test non-mutating.
assert_rejected CASCADE_DIRECT_CONTROL_PORT=18444
assert_rejected CASCADE_DIRECT_DATA_PORT_START=23999
assert_rejected CASCADE_DIRECT_DATA_PORT_END=25000
echo "firewall range validation passed"
