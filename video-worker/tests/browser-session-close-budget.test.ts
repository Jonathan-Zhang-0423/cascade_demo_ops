import { describe, expect, it } from "vitest";

import { adaptiveObservationProgressDeadline, browserSessionCloseBudgets } from "../src/browser-agent-runtime.js";

describe("browser session close budgets", () => {
  it("closes short protocol sessions without inheriting long-recording drain time", () => {
    expect(browserSessionCloseBudgets(30_000)).toEqual({
      contextMS: 1_500,
      browserAfterContextMS: 1_500,
      browserAfterTimeoutMS: 3_000,
      recordingStableMaxMS: 15_000,
      recordingStableGraceMS: 0,
    });
  });

  it("retains conservative encoder drain time for recordings over two minutes", () => {
    expect(browserSessionCloseBudgets(120_000)).toEqual({
      contextMS: 90_000,
      browserAfterContextMS: 60_000,
      browserAfterTimeoutMS: 120_000,
      recordingStableMaxMS: 900_000,
      recordingStableGraceMS: 60_000,
    });
  });
});

describe("post-refresh observation deadline", () => {
  it("does not let reload-induced progress reopen the build waiting budget", () => {
    const fixedFinalDeadline = 33 * 60_000;
    expect(adaptiveObservationProgressDeadline(fixedFinalDeadline, 0, 31 * 60_000, 30 * 60_000, 3 * 60_000, true)).toBe(fixedFinalDeadline);
  });

  it("allows real progress to extend the idle deadline before the hard refresh", () => {
    expect(adaptiveObservationProgressDeadline(30 * 60_000, 0, 5 * 60_000, 30 * 60_000, 3 * 60_000, false)).toBe(38 * 60_000);
  });
});
