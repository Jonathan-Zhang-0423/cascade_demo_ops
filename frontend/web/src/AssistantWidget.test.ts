import { describe, expect, it } from "vitest";
import { isChatComposerProposal, isClientActionProposal, isComposerSubmitKey, questionChoiceOptions, visibleAssistantMessages } from "./AssistantWidget";
import { createMockBridgeClient } from "./bridge";
import type { AssistantMessageView } from "./domain";

function message(id: string, role: AssistantMessageView["role"], text: string): AssistantMessageView {
  return { id, role, kind: "answer", text, createdAt: "2026-08-04T00:00:00Z" };
}

describe("Home assistant presentation", () => {
  const messages = [
    message("welcome", "agent", "Which product should this demo cover?"),
    message("user-1", "user", "Show the approval workflow."),
    message("agent-1", "agent", "Who is this demo for?"),
  ];

  it("hides bootstrap assistant messages before the first Home prompt", () => {
    expect(visibleAssistantMessages(messages.slice(0, 1), "home")).toEqual([]);
    expect(visibleAssistantMessages(messages, "home").map((item) => item.id)).toEqual(["user-1", "agent-1"]);
  });

  it("hides the bootstrap message in contextual assistant panels", () => {
    expect(visibleAssistantMessages(messages, "default").map((item) => item.id)).toEqual(["user-1", "agent-1"]);
  });

  it("keeps mock assistant replies visible after the hidden bootstrap message", async () => {
    const bridge = createMockBridgeClient();
    const created = await bridge.createAssistantSession({ surface: "projects", scopeKey: "project_proj_mock_visible", projectID: "proj_mock_visible", createProjectOnFirstTurn: true });
    const replied = await bridge.submitAssistantTurn(created.data!.id, "Show the repository connection flow.");
    const projects = await bridge.listProjectSummaries();

    expect(visibleAssistantMessages(replied.data!.messages, "default").map((item) => item.role)).toEqual(["user", "agent"]);
    expect(replied.data!.context.projectID).toBe("proj_mock_visible");
    expect(projects.data?.map((project) => project.id)).toContain("proj_mock_visible");
  });

  it("sends with Enter while preserving Shift+Enter for a new line", () => {
    expect(isComposerSubmitKey({ key: "Enter", shiftKey: false, nativeEvent: { isComposing: false } as KeyboardEvent })).toBe(true);
    expect(isComposerSubmitKey({ key: "Enter", shiftKey: true, nativeEvent: { isComposing: false } as KeyboardEvent })).toBe(false);
    expect(isComposerSubmitKey({ key: "Enter", shiftKey: false, nativeEvent: { isComposing: true } as KeyboardEvent })).toBe(false);
  });

  it("renders structured ambiguity guesses without turning confirmations into choices", () => {
    const options = questionChoiceOptions({
      field: "targetAudience",
      prompt: "Who is this for?",
      options: [
        { label: "Prospective customers", value: "Prospective customers", description: "Lead with product value.", recommended: true },
        { label: "New users", value: "New users", description: "Prioritize first success." },
      ],
    });

    expect(options).toHaveLength(2);
    expect(options[0]).toMatchObject({ label: "Prospective customers", recommended: true });
    expect(isClientActionProposal("select_local_project")).toBe(true);
    expect(isClientActionProposal("connect_github")).toBe(true);
    expect(isChatComposerProposal("connect_github")).toBe(true);
    expect(isChatComposerProposal("store_demo_credential")).toBe(false);
    expect(isClientActionProposal("confirm_configuration")).toBe(false);
    expect(isClientActionProposal("retry_page_scan")).toBe(false);
  });

  it("keeps legacy suggestions usable as descriptive choices", () => {
    expect(questionChoiceOptions({ field: "targetAudience", prompt: "Who is this for?", suggestions: ["Sales teams", "New users"] })).toEqual([
      { label: "Sales teams", value: "Sales teams", description: "Use sales teams as the working answer." },
      { label: "New users", value: "New users", description: "Use new users as the working answer." },
    ]);
  });
});
