export type ProjectConversationIdentity = {
  projectID: string;
  scopeKey: string;
};

function browserConversationToken(): string {
  const uuid = globalThis.crypto?.randomUUID?.();
  if (uuid) return uuid;
  return `${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

export function createProjectConversationIdentity(tokenFactory: () => string = browserConversationToken): ProjectConversationIdentity {
  const token = tokenFactory().replace(/[^a-zA-Z0-9]/g, "").toLowerCase();
  const projectID = `proj_${token || Date.now()}`;
  return { projectID, scopeKey: `project_${projectID}` };
}

export function provisionalConversationProjectName(message: string): string {
  const normalized = message.replace(/\s+/g, " ").trim();
  const explicit = normalized.match(/(?:project name|项目名)(?:\s+is|\s*[:：]|叫)\s*[“"']?([^。；;,.，\n”"']{1,56})/i)?.[1]?.trim();
  if (explicit) return explicit;

  const rawURL = normalized.match(/https?:\/\/[^\s)\]}]+/i)?.[0];
  if (rawURL) {
    try {
      const host = new URL(rawURL).hostname.replace(/^www\./i, "").split(".")[0];
      if (host) return `${host.charAt(0).toUpperCase()}${host.slice(1)} demo`;
    } catch {
      // Fall through to the prompt-derived name.
    }
  }

  const words = normalized.split(" ").filter(Boolean).slice(0, 8).join(" ");
  return words.slice(0, 56).replace(/[\s.,:;!?—-]+$/g, "") || "New demo project";
}
