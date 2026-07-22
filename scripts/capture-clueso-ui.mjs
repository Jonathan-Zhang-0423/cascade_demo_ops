#!/usr/bin/env node

import { mkdir, writeFile } from "node:fs/promises";
import path from "node:path";
import process from "node:process";

const DEFAULT_PORT = 9222;
const DEFAULT_OUTPUT = "artifacts/clueso-ui";

function usage() {
  console.log(`Usage:
  node scripts/capture-clueso-ui.mjs --name <state> [--hover <rail-control>] [--port 9222] [--out artifacts/clueso-ui]

The script attaches only to an already-open local Chrome DevTools Protocol port.
It captures a viewport PNG plus sanitized UI geometry, semantic control metadata,
selected computed styles, and design tokens. It does not read network traffic,
cookies, request bodies, form values, transcript text, or media URLs.

--hover temporarily moves the browser pointer over a named left-rail control
(for example, --hover Shapes) without clicking it.`);
}

function parseArgs(argv) {
  const options = { port: DEFAULT_PORT, out: DEFAULT_OUTPUT, name: "default" };
  for (let index = 0; index < argv.length; index += 1) {
    const argument = argv[index];
    if (argument === "--help" || argument === "-h") return { help: true };
    if (argument === "--port" || argument === "--out" || argument === "--name" || argument === "--hover") {
      const value = argv[index + 1];
      if (!value) throw new Error(`${argument} requires a value`);
      options[argument.slice(2)] = value;
      index += 1;
      continue;
    }
    throw new Error(`Unknown argument: ${argument}`);
  }
  options.port = Number(options.port);
  if (!Number.isInteger(options.port) || options.port < 1 || options.port > 65535) {
    throw new Error("--port must be a valid TCP port");
  }
  options.name = safeName(options.name);
  return options;
}

function safeName(value) {
  const normalized = String(value || "default").trim().replace(/[^a-zA-Z0-9_-]+/g, "-");
  if (!normalized) throw new Error("--name must contain letters, numbers, underscores, or dashes");
  return normalized;
}

async function hoverRailControl(cdp, label) {
  const expression = `(() => {
    const button = document.querySelector('button[aria-label=' + ${JSON.stringify(JSON.stringify(label))} + ']');
    if (!button) throw new Error('Left-rail control not found: ' + ${JSON.stringify(label)});
    const rect = button.getBoundingClientRect();
    if (!rect.width || !rect.height) throw new Error('Left-rail control is not visible: ' + ${JSON.stringify(label)});
    return { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 };
  })()`;
  const evaluation = await cdp.send("Runtime.evaluate", { expression, returnByValue: true });
  if (evaluation.exceptionDetails || !evaluation.result?.value) {
    throw new Error(`Unable to hover '${label}'.`);
  }
  await cdp.send("Input.dispatchMouseEvent", {
    type: "mouseMoved",
    x: evaluation.result.value.x,
    y: evaluation.result.value.y,
    buttons: 0
  });
  await new Promise((resolve) => setTimeout(resolve, 250));
  return evaluation.result.value;
}

class CdpConnection {
  constructor(socket) {
    this.socket = socket;
    this.nextID = 1;
    this.pending = new Map();
    socket.addEventListener("message", (event) => this.onMessage(event));
    socket.addEventListener("close", () => this.closePending(new Error("CDP connection closed")));
    socket.addEventListener("error", () => this.closePending(new Error("CDP connection failed")));
  }

  onMessage(event) {
    let message;
    try {
      message = JSON.parse(String(event.data));
    } catch {
      return;
    }
    if (!message.id) return;
    const pending = this.pending.get(message.id);
    if (!pending) return;
    this.pending.delete(message.id);
    if (message.error) {
      pending.reject(new Error(`${message.error.message || "CDP command failed"} (${message.error.code || "unknown"})`));
      return;
    }
    pending.resolve(message.result || {});
  }

  send(method, params = {}) {
    const id = this.nextID;
    this.nextID += 1;
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      this.socket.send(JSON.stringify({ id, method, params }));
    });
  }

  closePending(error) {
    for (const pending of this.pending.values()) pending.reject(error);
    this.pending.clear();
  }

  close() {
    this.socket.close();
  }
}

async function connectCDP(webSocketDebuggerUrl) {
  if (typeof WebSocket === "undefined") {
    throw new Error("This script requires Node.js with the built-in WebSocket client (Node 22 or newer).");
  }
  const socket = new WebSocket(webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    socket.addEventListener("open", resolve, { once: true });
    socket.addEventListener("error", () => reject(new Error("Unable to connect to Chrome DevTools Protocol.")), { once: true });
  });
  return new CdpConnection(socket);
}

async function findCluesoTarget(port) {
  const response = await fetch(`http://127.0.0.1:${port}/json/list`);
  if (!response.ok) throw new Error(`Chrome DevTools Protocol returned HTTP ${response.status}.`);
  const targets = await response.json();
  const target = targets.find((item) => item.type === "page" && /^https:\/\/web\.clueso\.io\//.test(item.url));
  if (!target?.webSocketDebuggerUrl) {
    throw new Error("No open https://web.clueso.io/ page was found on the local Chrome debugging port.");
  }
  return target;
}

const CAPTURE_EXPRESSION = String.raw`(() => {
  const STYLE_KEYS = [
    "display", "position", "width", "height", "minWidth", "maxWidth", "minHeight", "maxHeight",
    "paddingTop", "paddingRight", "paddingBottom", "paddingLeft", "marginTop", "marginRight", "marginBottom", "marginLeft",
    "gap", "color", "backgroundColor", "borderTopWidth", "borderRightWidth", "borderBottomWidth", "borderLeftWidth",
    "borderTopColor", "borderRightColor", "borderBottomColor", "borderLeftColor", "borderRadius", "boxShadow",
    "fontFamily", "fontSize", "fontWeight", "lineHeight", "letterSpacing", "justifyContent", "alignItems",
    "flexDirection", "flexGrow", "flexShrink", "overflowX", "overflowY", "opacity", "cursor", "zIndex"
  ];
  const TOKEN_PREFIXES = ["--color-", "--spacing-", "--radius-", "--text-", "--shadow-", "--font-"];
  const TOOL_LABELS = ["Select", "Assets", "Text", "Shapes", "Effects", "Animation", "Template", "Transition", "Captions", "Comments"];

  const numeric = (value) => Math.round(Number(value) * 100) / 100;
  const rectFor = (element) => {
    const rect = element.getBoundingClientRect();
    return { x: numeric(rect.x), y: numeric(rect.y), width: numeric(rect.width), height: numeric(rect.height) };
  };
  const stylesFor = (element) => {
    const computed = getComputedStyle(element);
    return Object.fromEntries(STYLE_KEYS.map((key) => [key, computed[key]]));
  };
  const stateFor = (element) => ({
    tag: element.tagName.toLowerCase(),
    role: element.getAttribute("role") || undefined,
    ariaLabel: element.getAttribute("aria-label") || undefined,
    ariaExpanded: element.getAttribute("aria-expanded") || undefined,
    ariaChecked: element.getAttribute("aria-checked") || undefined,
    dataState: element.getAttribute("data-state") || undefined,
    type: element.getAttribute("type") || undefined,
    placeholder: element.getAttribute("placeholder") || undefined,
    disabled: "disabled" in element ? Boolean(element.disabled) : false,
    className: typeof element.className === "string" ? element.className : undefined,
    rect: rectFor(element),
    styles: stylesFor(element)
  });
  const recordSection = (name, element, selector) => element ? {
    name,
    matchedSelector: selector,
    ...stateFor(element),
    childElementCount: element.children.length
  } : { name, matchedSelector: selector, missing: true };
  const findToolRail = () => {
    const first = document.querySelector('button[aria-label="Select"]');
    let candidate = first?.parentElement;
    while (candidate && candidate !== document.body) {
      const labels = Array.from(candidate.querySelectorAll("button[aria-label]")).map((button) => button.getAttribute("aria-label"));
      if (TOOL_LABELS.every((label) => labels.includes(label))) return candidate;
      candidate = candidate.parentElement;
    }
    return undefined;
  };
  const tokenStyles = getComputedStyle(document.documentElement);
  const tokens = {};
  for (const key of tokenStyles) {
    if (TOKEN_PREFIXES.some((prefix) => key.startsWith(prefix))) tokens[key] = tokenStyles.getPropertyValue(key).trim();
  }
  const sections = [
    recordSection("editor_platform", document.querySelector(".EditorPlatform"), ".EditorPlatform"),
    recordSection("topbar", document.querySelector(".EditorTopbarV2"), ".EditorTopbarV2"),
    recordSection("tool_rail", findToolRail(), "ancestor of button[aria-label=Select] containing all tool labels"),
    recordSection("transcript", document.querySelector(".gigauser-TextEditor-videoTranscript"), ".gigauser-TextEditor-videoTranscript"),
    recordSection("canvas", document.querySelector("canvas"), "canvas"),
    recordSection("right_panel", document.querySelector(".RightPanel-container"), ".RightPanel-container"),
    recordSection("timeline", document.querySelector(".timelineContainerRef, .TimelineScroller, .element-timeline"), ".timelineContainerRef, .TimelineScroller, .element-timeline")
  ];
  const controls = Array.from(document.querySelectorAll("button, input, select, textarea, [role=button], [aria-label]"))
    .filter((element) => {
      const style = getComputedStyle(element);
      return style.display !== "none" && style.visibility !== "hidden";
    })
    .map(stateFor);
  const toolStates = TOOL_LABELS.map((label) => {
    const button = document.querySelector('button[aria-label="' + label + '"]');
    if (!button) return { label, missing: true };
    const style = getComputedStyle(button);
    return {
      label,
      ariaExpanded: button.getAttribute("aria-expanded") || undefined,
      dataState: button.getAttribute("data-state") || undefined,
      active: button.className.includes("bg-inverted") || style.backgroundColor === "oklch(0.24 0 0)"
    };
  });
  const expandedControls = controls
    .filter((control) => control.ariaExpanded === "true")
    .map((control) => control.ariaLabel || control.role || control.tag);
  return {
    schemaVersion: "cascade.clueso-ui-capture.v1",
    capturedAt: new Date().toISOString(),
    page: { host: location.host, protocol: location.protocol },
    viewport: { innerWidth: window.innerWidth, innerHeight: window.innerHeight, devicePixelRatio: window.devicePixelRatio },
    sections,
    controls,
    toolStates,
    expandedControls,
    designTokens: tokens,
    privacy: {
      excluded: ["textContent", "input values", "cookies", "network requests", "media URLs", "local storage", "session storage"]
    }
  };
})()`;

async function main() {
  const options = parseArgs(process.argv.slice(2));
  if (options.help) {
    usage();
    return;
  }
  const target = await findCluesoTarget(options.port);
  const cdp = await connectCDP(target.webSocketDebuggerUrl);
  try {
    await cdp.send("Page.enable");
    await cdp.send("Runtime.enable");
    const hoverPoint = options.hover ? await hoverRailControl(cdp, options.hover) : undefined;
    const evaluation = await cdp.send("Runtime.evaluate", { expression: CAPTURE_EXPRESSION, returnByValue: true, awaitPromise: true });
    if (evaluation.exceptionDetails) throw new Error(`Page capture failed: ${evaluation.exceptionDetails.text || "unknown page exception"}`);
    const capture = evaluation.result?.value;
    if (!capture) throw new Error("Chrome did not return a capture payload.");
    const screenshot = await cdp.send("Page.captureScreenshot", { format: "png", fromSurface: true, captureBeyondViewport: false });
    const outputDir = path.resolve(options.out, options.name);
    await mkdir(outputDir, { recursive: true });
    await writeFile(path.join(outputDir, "ui-capture.json"), `${JSON.stringify(capture, null, 2)}\n`, "utf8");
    await writeFile(path.join(outputDir, "viewport.png"), Buffer.from(screenshot.data, "base64"));
    await writeFile(path.join(outputDir, "manifest.json"), `${JSON.stringify({
      schemaVersion: "cascade.clueso-ui-capture-manifest.v1",
      state: options.name,
      capturedAt: capture.capturedAt,
      files: ["ui-capture.json", "viewport.png"],
      hoveredControl: options.hover || undefined,
      privacy: "No network data, cookies, form values, transcript text, media URLs, or storage values were captured."
    }, null, 2)}\n`, "utf8");
    console.log(`Captured state '${options.name}' to ${outputDir}`);
    console.log(`Viewport: ${capture.viewport.innerWidth}x${capture.viewport.innerHeight} @ DPR ${capture.viewport.devicePixelRatio}`);
    console.log(`Visible controls: ${capture.controls.length}`);
    if (options.hover) console.log(`Hovered rail control: ${options.hover} at ${Math.round(hoverPoint.x)},${Math.round(hoverPoint.y)}`);
    console.log(`Highlighted rail controls: ${capture.toolStates.filter((tool) => tool.active).map((tool) => tool.label).join(", ") || "none"}`);
    console.log(`Controls exposing aria-expanded=true: ${capture.expandedControls.join(", ") || "none"}`);
  } finally {
    cdp.close();
  }
}

main().catch((error) => {
  console.error(`capture-clueso-ui failed: ${error.message}`);
  process.exitCode = 1;
});
