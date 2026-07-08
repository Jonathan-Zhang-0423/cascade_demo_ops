import type { FastifyInstance } from "fastify";

export async function registerAssetRoutes(app: FastifyInstance) {
  app.post("/v1/workflow-graphs/:workflowGraphId/assets", async (request) => {
    const { workflowGraphId } = request.params as { workflowGraphId: string };
    return { workflowGraphId, status: "not_implemented", route: "generate_assets" };
  });

  app.get("/v1/assets/:assetId", async (request) => {
    const { assetId } = request.params as { assetId: string };
    return { assetId, status: "not_implemented", route: "get_asset" };
  });

  app.post("/v1/assets/:assetId/approve", async (request) => {
    const { assetId } = request.params as { assetId: string };
    return { assetId, status: "not_implemented", route: "approve_asset" };
  });
}