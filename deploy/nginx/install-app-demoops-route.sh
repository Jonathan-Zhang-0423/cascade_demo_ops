#!/usr/bin/env bash
set -euo pipefail

config_path="${1:-/etc/nginx/sites-available/cascadeai-cn.conf}"
include_path="${2:-/etc/nginx/snippets/demoops-control-plane.conf}"
include_line="    include ${include_path};"

if grep -Fqx "$include_line" "$config_path"; then
  echo "DemoOps Nginx include already installed."
  exit 0
fi

backup_path="${config_path}.bak.demoops-$(date -u +%Y%m%dT%H%M%SZ)"
cp -a "$config_path" "$backup_path"

temporary_path="$(mktemp)"
trap 'rm -f "$temporary_path"' EXIT

awk -v include_line="$include_line" '
  /server_name app\.cascadeai\.cn;/ { in_app_server = 1 }
  in_app_server && !inserted && /^[[:space:]]*location \/ \{/ {
    print include_line
    print ""
    inserted = 1
  }
  { print }
  END {
    if (!inserted) {
      exit 42
    }
  }
' "$config_path" > "$temporary_path"

install -o root -g root -m 0644 "$temporary_path" "$config_path"
nginx -t
echo "Installed DemoOps Nginx include. Backup: $backup_path"
