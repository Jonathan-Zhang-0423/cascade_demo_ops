import type { FastifyInstance } from "fastify";

export async function registerWorkflowGraphRoutes(app: FastifyInstance) {
  app.post("/v1/projects/:projectId/workflow-graphs/draft", async (request) => {
    const { projectId } = request.params as { projectId: string };
    return { projectId, status: "not_implemented", route: "draft_workflow_graph" };
  });

  app.post("/v1/workflow-graphs/:workflowGraphId/approve", async (request) => {
    const { workflowGraphId } = request.params as { workflowGraphId: string };
    return { workflowGraphId, status: "not_implemented", route: "approve_workflow_graph" };
  });

  app.post("/v1/workflow-graphs/:workflowGraphId/rehearsals", async (request) => {
    const { workflowGraphId } = request.params as { workflowGraphId: string };
    return { workflowGraphId, status: "not_implemented", route: "run_rehearsal" };
  });
}