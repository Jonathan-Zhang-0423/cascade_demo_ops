import type { ProjectContext } from "@cascade/schemas";

export interface CreateProjectInput {
  tenantId: string;
  productName: string;
  requestedBy: string;
}

export interface AccessHealthReport {
  projectId: string;
  frontend: "unknown" | "healthy" | "failed";
  github: "not_configured" | "healthy" | "failed";
  ssh: "not_configured" | "healthy" | "failed";
  findings: Array<{ severity: "info" | "warning" | "error"; message: string }>;
}

export interface ProjectContextBuilder {
  build(projectId: string): Promise<ProjectContext>;
  validate(context: ProjectContext): Promise<AccessHealthReport>;
}
