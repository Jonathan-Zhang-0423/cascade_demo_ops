import type { FastifyInstance } from "fastify";

export async function registerExecutionRunRoutes(app: FastifyInstance) {
  app.get("/v1/execution-runs/:executionRunId", async (request) => {
    const { executionRunId } = request.params as { executionRunId: string };
    return { executionRunId, status: "not_implemented", route: "get_execution_run" };
  });
}