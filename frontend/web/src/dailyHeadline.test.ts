import { describe, expect, it } from "vitest";
import { getDailyAgentHeadline, millisecondsUntilNextLocalDay } from "./dailyHeadline";

describe("daily Home headline", () => {
  it("is stable throughout the same local calendar day", () => {
    const morning = new Date(2026, 7, 6, 8, 15);
    const evening = new Date(2026, 7, 6, 22, 45);

    expect(getDailyAgentHeadline(morning)).toBe(getDailyAgentHeadline(evening));
  });

  it("advances on the next local calendar day", () => {
    const today = new Date(2026, 7, 6, 12);
    const tomorrow = new Date(2026, 7, 7, 12);

    expect(getDailyAgentHeadline(tomorrow)).not.toBe(getDailyAgentHeadline(today));
  });

  it("repeats after the complete 21-headline rotation", () => {
    const firstCycle = new Date(2026, 7, 6, 12);
    const nextCycle = new Date(2026, 7, 27, 12);

    expect(getDailyAgentHeadline(nextCycle)).toBe(getDailyAgentHeadline(firstCycle));
  });

  it("calculates the exact delay until the next local midnight", () => {
    const now = new Date(2026, 7, 6, 23, 59, 30, 250);

    expect(millisecondsUntilNextLocalDay(now)).toBe(29_750);
  });
});

