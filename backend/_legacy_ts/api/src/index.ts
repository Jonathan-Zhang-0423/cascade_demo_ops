import Fastify from "fastify";
import { registerAccessRoutes } from "./routes/access.js";
import { registerAssetRoutes } from "./routes/assets.js";
import { registerExecutionRunRoutes } from "./routes/execution-runs.js";
import { registerProjectRoutes } from "./routes/projects.js";
import { registerWorkflowGraphRoutes } from "./routes/workflow-graphs.js";

const app = Fastify({ logger: true });

app.get("/health", async () => ({ status: "ok", service: "cascade-api" }));

await registerProjectRoutes(app);
await registerAccessRoutes(app);
await registerWorkflowGraphRoutes(app);
await registerExecutionRunRoutes(app);
await registerAssetRoutes(app);

const port = Number(process.env.PORT ?? 4000);
const host = process.env.HOST ?? "0.0.0.0";

await app.listen({ port, host });