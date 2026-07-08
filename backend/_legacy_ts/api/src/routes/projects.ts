import type { FastifyInstance } from "fastify";

export async function registerProjectRoutes(app: FastifyInstance) {
  app.post("/v1/projects", async () => ({ status: "not_implemented", route: "create_project" }));

  app.get("/v1/projects/:projectId", async (request) => {
    const { projectId } = request.params as { projectId: string };
    return { projectId, status: "not_implemented", route: "get_project" };
  });

  app.post("/v1/projects/:projectId/context", async (request) => {
    const { projectId } = request.params as { projectId: string };
    return { projectId, status: "not_implemented", route: "upsert_project_context" };
  });
}