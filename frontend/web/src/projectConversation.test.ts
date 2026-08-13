import { describe, expect, it } from "vitest";
import { createProjectConversationIdentity, provisionalConversationProjectName } from "./projectConversation";

describe("project conversation identity", () => {
  it("creates a canonical assistant scope for a unique project", () => {
    expect(createProjectConversationIdentity(() => "A0B1-C2D3")).toEqual({
      projectID: "proj_a0b1c2d3",
      scopeKey: "project_proj_a0b1c2d3",
    });
  });

  it("does not reuse identities across conversation instances", () => {
    const first = createProjectConversationIdentity(() => "first");
    const second = createProjectConversationIdentity(() => "second");
    expect(first.projectID).not.toBe(second.projectID);
    expect(first.scopeKey).not.toBe(second.scopeKey);
  });

  it("derives a provisional workspace name before the model responds", () => {
    expect(provisionalConversationProjectName("Project name: Approval Flow. Create a demo for https://example.com")).toBe("Approval Flow");
    expect(provisionalConversationProjectName("Create a demo for https://cascade.ai")).toBe("Cascade demo");
    expect(provisionalConversationProjectName("   ")).toBe("New demo project");
  });
});
