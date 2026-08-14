#!/usr/bin/env bash
set -euo pipefail

node_binary="${NODE_BINARY_PATH:-/usr/bin/node}"
worker_path="${NODE_WORKER_PATH:-/opt/cascade-browser-agent/video-worker/dist/index.js}"
browser_root="${PLAYWRIGHT_BROWSERS_PATH:-/opt/cascade-browser-agent/playwright-browsers}"
ffmpeg_binary="${CASCADE_FFMPEG_PATH:-/usr/bin/ffmpeg}"
ffprobe_binary="${CASCADE_FFPROBE_PATH:-/usr/bin/ffprobe}"

for executable in "$node_binary" "$ffmpeg_binary" "$ffprobe_binary"; do
  [[ -x "$executable" ]] || { echo "required executable unavailable: $executable" >&2; exit 1; }
done
[[ -r "$worker_path" ]] || { echo "video worker is unreadable: $worker_path" >&2; exit 1; }
[[ -d "$browser_root" && -r "$browser_root" ]] || { echo "Playwright browser root is unreadable: $browser_root" >&2; exit 1; }

worker_root="$(dirname "$(dirname "$worker_path")")"
health_response="$(printf '%s\n' '{"jsonrpc":"2.0","id":"preflight","method":"health"}' | timeout 20s "$node_binary" "$worker_path")"
[[ "$health_response" == *'"service":"video-worker"'* && "$health_response" == *'"ok":true'* ]] || {
  echo "video worker health RPC failed" >&2
  exit 1
}

(
  cd "$worker_root"
  timeout 45s "$node_binary" --input-type=module -e \
    'import { chromium } from "playwright"; const browser = await chromium.launch({headless:true}); const page = await browser.newPage(); await page.goto("data:text/plain,cascade-browser-agent-preflight"); await browser.close();'
)

echo "Browser Agent Worker runtime preflight passed."
