import type { SecretRef } from "@cascade/schemas";

export type PermissionDecision = "allow" | "deny";

export interface SecretProvider {
  getSecret(ref: SecretRef): Promise<string>;
  putSecret(scope: string, value: string): Promise<SecretRef>;
}

export interface AuditEvent {
  projectId: string;
  actor: string;
  action: string;
  target: string;
  result: "success" | "failure";
  metadata?: Record<string, unknown>;
}

export interface AuditLogWriter {
  write(event: AuditEvent): Promise<void>;
}

export interface CommandPolicy {
  isAllowed(command: string, allowedCommands: string[]): PermissionDecision;
}

export class ReadOnlyCommandPolicy implements CommandPolicy {
  private readonly denied = /\b(rm|mv|chmod|chown|sudo|reboot|shutdown|systemctl\s+restart|docker\s+exec|>|>>|tee)\b/i;

  isAllowed(command: string, allowedCommands: string[]): PermissionDecision {
    if (this.denied.test(command)) return "deny";
    return allowedCommands.some((allowed) => command === allowed || command.startsWith(`${allowed} `)) ? "allow" : "deny";
  }
}
