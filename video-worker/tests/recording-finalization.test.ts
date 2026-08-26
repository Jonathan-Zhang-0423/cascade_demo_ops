import { appendFile, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";

import { describe, expect, it } from "vitest";

import { waitForFileStable } from "../src/browser-agent-runtime.js";

describe("browser recording finalization", () => {
  it("waits through delayed encoder writes before declaring the recording stable", async () => {
    const root = await mkdtemp(path.join(tmpdir(), "demoops-recording-finalization-"));
    const recording = path.join(root, "recording.webm");
    try {
      await writeFile(recording, "initial");
      const delayedWrites = (async () => {
        await new Promise((resolve) => setTimeout(resolve, 25));
        await appendFile(recording, "-encoder-flush-one");
        await new Promise((resolve) => setTimeout(resolve, 25));
        await appendFile(recording, "-encoder-flush-two");
      })();

      expect(await waitForFileStable(recording, 3, 15, 500)).toBe(true);
      await delayedWrites;
    } finally {
      await rm(root, { recursive: true, force: true });
    }
  });

  it("returns false when a recording never appears within the bounded window", async () => {
    const root = await mkdtemp(path.join(tmpdir(), "demoops-recording-missing-"));
    try {
      expect(await waitForFileStable(path.join(root, "missing.webm"), 2, 10, 50)).toBe(false);
    } finally {
      await rm(root, { recursive: true, force: true });
    }
  });

  it("does not mistake a long encoder pause for final quiescence", async () => {
    const root = await mkdtemp(path.join(tmpdir(), "demoops-recording-pause-"));
    const recording = path.join(root, "recording.webm");
    try {
      await writeFile(recording, "initial");
      const delayedWrite = (async () => {
        await new Promise((resolve) => setTimeout(resolve, 80));
        await appendFile(recording, "-late-browser-flush");
      })();

      const started = Date.now();
      expect(await waitForFileStable(recording, 2, 10, 400, 100)).toBe(true);
      await delayedWrite;
      expect(Date.now() - started).toBeGreaterThanOrEqual(170);
    } finally {
      await rm(root, { recursive: true, force: true });
    }
  });

  it("requires the configured quiet period even after a close acknowledgement", async () => {
    const root = await mkdtemp(path.join(tmpdir(), "demoops-recording-drain-"));
    const recording = path.join(root, "recording.webm");
    try {
      await writeFile(recording, "complete");
      const started = Date.now();
      expect(await waitForFileStable(recording, 2, 10, 250, 80)).toBe(true);
      expect(Date.now() - started).toBeGreaterThanOrEqual(80);
    } finally {
      await rm(root, { recursive: true, force: true });
    }
  });
});
