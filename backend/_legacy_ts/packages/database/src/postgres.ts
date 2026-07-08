import type { AuditEvent, AuditLogWriter } from "@cascade/security";
import type {
  Asset,
  DemoWorkflowGraph,
  EvidenceRecord,
  ExecutionRun,
  ProjectContext,
  StepResult
} from "@cascade/schemas";
import { createId } from "@cascade/shared";
import type { Pool } from "pg";
import type {
  AssetRepository,
  EvidenceRepository,
  ExecutionRepository,
  ProjectContextRepository,
  ProjectRecord,
  ProjectRepository,
  UnitOfWork,
  WorkflowGraphRepository
} from "./index.js";

type Queryable = Pick<Pool, "query">;

type ProjectRow = {
  id: string;
  tenant_id: string;
  product_name: string;
  status: ProjectRecord["status"];
  created_by: string;
  created_at: Date | string;
  updated_at: Date | string;
};

function toIso(value: Date | string) {
  return value instanceof Date ? value.toISOString() : new Date(value).toISOString();
}

function mapProject(row: ProjectRow): ProjectRecord {
  return {
    id: row.id,
    tenantId: row.tenant_id,
    productName: row.product_name,
    status: row.status,
    createdBy: row.created_by,
    createdAt: toIso(row.created_at),
    updatedAt: toIso(row.updated_at)
  };
}

export class PostgresProjectRepository implements ProjectRepository {
  constructor(private readonly db: Queryable) {}

  async create(input: { tenantId: string; productName: string; createdBy: string }): Promise<ProjectRecord> {
    const id = createId("proj");
    await this.db.query(
      `insert into tenants (id, name) values ($1, $1) on conflict (id) do nothing`,
      [input.tenantId]
    );
    const result = await this.db.query<ProjectRow>(
      `insert into projects (id, tenant_id, product_name, status, created_by)
       values ($1, $2, $3, 'created', $4)
       returning id, tenant_id, product_name, status, created_by, created_at, updated_at`,
      [id, input.tenantId, input.productName, input.createdBy]
    );
    return mapProject(result.rows[0]!);
  }

  async get(projectId: string): Promise<ProjectRecord | undefined> {
    const result = await this.db.query<ProjectRow>(
      `select id, tenant_id, product_name, status, created_by, created_at, updated_at from projects where id = $1`,
      [projectId]
    );
    return result.rows[0] ? mapProject(result.rows[0]) : undefined;
  }

  async updateStatus(projectId: string, status: ProjectRecord["status"]): Promise<ProjectRecord> {
    const result = await this.db.query<ProjectRow>(
      `update projects set status = $2, updated_at = now() where id = $1
       returning id, tenant_id, product_name, status, created_by, created_at, updated_at`,
      [projectId, status]
    );
    if (!result.rows[0]) throw new Error(`Project not found: ${projectId}`);
    return mapProject(result.rows[0]);
  }
}

export class PostgresProjectContextRepository implements ProjectContextRepository {
  constructor(private readonly db: Queryable) {}

  async createVersion(input: { projectId: string; context: ProjectContext; createdBy: string }): Promise<{ id: string; version: number }> {
    const id = createId("ctx");
    const versionResult = await this.db.query<{ next_version: number }>(
      `select coalesce(max(version), 0) + 1 as next_version from project_contexts where project_id = $1`,
      [input.projectId]
    );
    const version = Number(versionResult.rows[0]?.next_version ?? 1);
    await this.db.query(
      `insert into project_contexts (id, project_id, version, context_json, created_by)
       values ($1, $2, $3, $4, $5)`,
      [id, input.projectId, version, JSON.stringify(input.context), input.createdBy]
    );
    await this.db.query(`update projects set status = 'context_ready', updated_at = now() where id = $1`, [input.projectId]);
    return { id, version };
  }

  async getLatest(projectId: string): Promise<ProjectContext | undefined> {
    const result = await this.db.query<{ context_json: ProjectContext }>(
      `select context_json from project_contexts where project_id = $1 order by version desc limit 1`,
      [projectId]
    );
    return result.rows[0]?.context_json;
  }
}

export class PostgresEvidenceRepository implements EvidenceRepository {
  constructor(private readonly db: Queryable) {}

  async write(record: EvidenceRecord): Promise<void> {
    await this.db.query(
      `insert into evidence_records (id, project_id, kind, source_ref, summary, data_json, artifacts_json, created_at)
       values ($1, $2, $3, $4, $5, $6, $7, $8)`,
      [
        record.evidenceId,
        record.projectId,
        record.kind,
        JSON.stringify(record.sourceRef),
        record.summary ?? null,
        JSON.stringify(record.data),
        JSON.stringify(record.artifacts),
        record.createdAt
      ]
    );
  }

  async listByProject(projectId: string): Promise<EvidenceRecord[]> {
    const result = await this.db.query<{ id: string; project_id: string; kind: EvidenceRecord["kind"]; source_ref: EvidenceRecord["sourceRef"]; summary: string | null; data_json: Record<string, unknown>; artifacts_json: EvidenceRecord["artifacts"]; created_at: Date | string }>(
      `select id, project_id, kind, source_ref, summary, data_json, artifacts_json, created_at
       from evidence_records where project_id = $1 order by created_at asc`,
      [projectId]
    );
    return result.rows.map((row) => ({
      evidenceId: row.id,
      projectId: row.project_id,
      kind: row.kind,
      sourceRef: row.source_ref,
      summary: row.summary ?? undefined,
      data: row.data_json,
      artifacts: row.artifacts_json,
      createdAt: toIso(row.created_at)
    }));
  }

  async listByKind(projectId: string, kind: EvidenceRecord["kind"]): Promise<EvidenceRecord[]> {
    const records = await this.listByProject(projectId);
    return records.filter((record) => record.kind === kind);
  }
}

export class PostgresWorkflowGraphRepository implements WorkflowGraphRepository {
  constructor(private readonly db: Queryable) {}

  async createVersion(input: { projectId: string; graph: DemoWorkflowGraph; createdBy: string }): Promise<{ id: string; version: number }> {
    const versionResult = await this.db.query<{ next_version: number }>(
      `select coalesce(max(version), 0) + 1 as next_version from workflow_graphs where project_id = $1`,
      [input.projectId]
    );
    const version = Number(versionResult.rows[0]?.next_version ?? 1);
    await this.db.query(
      `insert into workflow_graphs (id, project_id, version, status, graph_json, created_by)
       values ($1, $2, $3, $4, $5, $6)`,
      [input.graph.workflowGraphId, input.projectId, version, input.graph.status, JSON.stringify(input.graph), input.createdBy]
    );
    return { id: input.graph.workflowGraphId, version };
  }

  async getLatest(projectId: string): Promise<DemoWorkflowGraph | undefined> {
    const result = await this.db.query<{ graph_json: DemoWorkflowGraph }>(
      `select graph_json from workflow_graphs where project_id = $1 order by version desc limit 1`,
      [projectId]
    );
    return result.rows[0]?.graph_json;
  }

  async get(workflowGraphId: string): Promise<DemoWorkflowGraph | undefined> {
    const result = await this.db.query<{ graph_json: DemoWorkflowGraph }>(
      `select graph_json from workflow_graphs where id = $1 order by version desc limit 1`,
      [workflowGraphId]
    );
    return result.rows[0]?.graph_json;
  }

  async updateStatus(workflowGraphId: string, status: DemoWorkflowGraph["status"]): Promise<void> {
    await this.db.query(
      `update workflow_graphs set status = $2, graph_json = jsonb_set(graph_json, '{status}', to_jsonb($2::text), false) where id = $1`,
      [workflowGraphId, status]
    );
  }
}

export class PostgresExecutionRepository implements ExecutionRepository {
  constructor(private readonly db: Queryable) {}

  async createRun(run: ExecutionRun): Promise<void> {
    await this.db.query(
      `insert into execution_runs (id, project_id, workflow_graph_id, mode, status, run_json, started_at, completed_at)
       values ($1, $2, $3, $4, $5, $6, $7, $8)`,
      [run.executionRunId, run.projectId, run.workflowGraphId, run.mode, run.status, JSON.stringify(run), run.startedAt ?? null, run.completedAt ?? null]
    );
  }

  async updateRun(run: ExecutionRun): Promise<void> {
    await this.db.query(
      `update execution_runs set status = $2, run_json = $3, started_at = $4, completed_at = $5 where id = $1`,
      [run.executionRunId, run.status, JSON.stringify(run), run.startedAt ?? null, run.completedAt ?? null]
    );
  }

  async appendStepResult(result: StepResult): Promise<void> {
    await this.db.query(
      `insert into step_results (id, execution_run_id, step_id, status, attempts, failure_type, result_json)
       values ($1, $2, $3, $4, $5, $6, $7)`,
      [result.stepResultId, result.executionRunId, result.stepId, result.status, result.attempts, result.failureType ?? null, JSON.stringify(result)]
    );
  }

  async getRun(executionRunId: string): Promise<ExecutionRun | undefined> {
    const result = await this.db.query<{ run_json: ExecutionRun }>(
      `select run_json from execution_runs where id = $1`,
      [executionRunId]
    );
    return result.rows[0]?.run_json;
  }
}

export class PostgresAssetRepository implements AssetRepository {
  constructor(private readonly db: Queryable) {}

  async create(asset: Asset): Promise<void> {
    await this.db.query(
      `insert into assets (id, project_id, workflow_graph_id, execution_run_id, type, status, version, artifact_json, provenance_json, freshness_status)
       values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
      [asset.assetId, asset.projectId, asset.workflowGraphId, asset.executionRunId ?? null, asset.type, asset.status, asset.version, asset.artifact ? JSON.stringify(asset.artifact) : null, JSON.stringify(asset.provenance), asset.freshnessStatus]
    );
  }

  async update(asset: Asset): Promise<void> {
    await this.db.query(
      `update assets set status = $2, artifact_json = $3, provenance_json = $4, freshness_status = $5 where id = $1`,
      [asset.assetId, asset.status, asset.artifact ? JSON.stringify(asset.artifact) : null, JSON.stringify(asset.provenance), asset.freshnessStatus]
    );
  }

  async listByProject(projectId: string): Promise<Asset[]> {
    const result = await this.db.query<{ asset_json: Asset }>(
      `select jsonb_build_object(
        'assetId', id,
        'projectId', project_id,
        'workflowGraphId', workflow_graph_id,
        'executionRunId', execution_run_id,
        'type', type,
        'status', status,
        'version', version,
        'artifact', artifact_json,
        'provenance', provenance_json,
        'freshnessStatus', freshness_status
      ) as asset_json from assets where project_id = $1 order by created_at desc`,
      [projectId]
    );
    return result.rows.map((row) => row.asset_json);
  }

  async get(assetId: string): Promise<Asset | undefined> {
    const result = await this.db.query<{ asset_json: Asset }>(
      `select jsonb_build_object(
        'assetId', id,
        'projectId', project_id,
        'workflowGraphId', workflow_graph_id,
        'executionRunId', execution_run_id,
        'type', type,
        'status', status,
        'version', version,
        'artifact', artifact_json,
        'provenance', provenance_json,
        'freshnessStatus', freshness_status
      ) as asset_json from assets where id = $1`,
      [assetId]
    );
    return result.rows[0]?.asset_json;
  }
}

export class PostgresAuditLogWriter implements AuditLogWriter {
  constructor(private readonly db: Queryable) {}

  async write(event: AuditEvent): Promise<void> {
    await this.db.query(
      `insert into audit_logs (id, project_id, actor, action, target, result, metadata_json)
       values ($1, $2, $3, $4, $5, $6, $7)`,
      [createId("audit"), event.projectId, event.actor, event.action, event.target, event.result, JSON.stringify(event.metadata ?? {})]
    );
  }
}

export function createPostgresUnitOfWork(pool: Pool): UnitOfWork {
  return {
    projects: new PostgresProjectRepository(pool),
    contexts: new PostgresProjectContextRepository(pool),
    evidence: new PostgresEvidenceRepository(pool),
    workflowGraphs: new PostgresWorkflowGraphRepository(pool),
    executions: new PostgresExecutionRepository(pool),
    assets: new PostgresAssetRepository(pool)
  };
}