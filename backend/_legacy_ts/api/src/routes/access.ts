import type { FastifyInstance } from "fastify";

export async function registerAccessRoutes(app: FastifyInstance) {
  app.post("/v1/projects/:projectId/access/github", async (request) => {
    const { projectId } = request.params as { projectId: string };
    return { projectId, status: "not_implemented", route: "configure_github_access" };
  });

  app.post("/v1/projects/:projectId/access/ssh", async (request) => {
    const { projectId } = request.params as { projectId: string };
    return { projectId, status: "not_implemented", route: "configure_ssh_access" };
  });

  app.post("/v1/projects/:projectId/evidence/snapshot", async (request) => {
    const { projectId } = request.params as { projectId: string };
    return { projectId, status: "not_implemented", route: "request_evidence_snapshot" };
  });
}