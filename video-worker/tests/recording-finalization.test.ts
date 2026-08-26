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
});
