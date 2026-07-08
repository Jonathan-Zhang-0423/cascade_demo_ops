-- Cascade DemoOps core schema v1.
-- Postgres-first, JSONB-friendly, and intentionally stores secret references
-- and artifact URIs only. Secret values and binary blobs do not belong here.

CREATE TABLE organizations (
    id text PRIMARY KEY,
    name text NOT NULL,
    slug text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id text PRIMARY KEY,
    email text NOT NULL UNIQUE,
    name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE organization_members (
    org_id text NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role text NOT NULL CHECK (role IN ('owner', 'admin', 'member', 'viewer')),
    status text NOT NULL CHECK (status IN ('active', 'invited', 'suspended', 'removed')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, user_id)
);

CREATE TABLE projects (
    id text PRIMARY KEY,
    org_id text NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    created_by_user_id text NOT NULL REFERENCES users(id),
    name text NOT NULL,
    mode text NOT NULL CHECK (mode IN ('web', 'desktop')),
    status text NOT NULL CHECK (status IN ('created', 'active', 'archived', 'failed')),
    product_url text,
    target_audience text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    archived_at timestamptz
);

CREATE TABLE audit_logs (
    id text PRIMARY KEY,
    org_id text NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id text REFERENCES projects(id) ON DELETE SET NULL,
    actor_user_id text REFERENCES users(id) ON DELETE SET NULL,
    action text NOT NULL,
    resource_type text NOT NULL,
    resource_id text NOT NULL,
    metadata_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE project_contexts (
    id text PRIMARY KEY,
    project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    version integer NOT NULL CHECK (version > 0),
    schema_version text NOT NULL,
    context_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    input_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    access_policy_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    security_policy_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    is_current boolean NOT NULL DEFAULT false,
    created_by_user_id text REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (project_id, version)
);

CREATE TABLE artifacts (
    id text PRIMARY KEY,
    org_id text NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    kind text NOT NULL,
    uri text NOT NULL,
    mime_type text,
    label text,
    sha256 text,
    size_bytes bigint CHECK (size_bytes IS NULL OR size_bytes >= 0),
    sensitive boolean NOT NULL DEFAULT false,
    metadata_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz
);

CREATE TABLE project_inputs (
    id text PRIMARY KEY,
    project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN (
        'product_url',
        'code',
        'requirement_document',
        'webpage_screenshot',
        'release_note',
        'brand_kit',
        'credential'
    )),
    title text,
    source_uri text,
    input_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    artifact_id text REFERENCES artifacts(id) ON DELETE SET NULL,
    fingerprint_sha256 text,
    status text NOT NULL CHECK (status IN ('created', 'ready', 'processed', 'failed', 'expired')),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE secret_refs (
    id text PRIMARY KEY,
    org_id text NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    kind text NOT NULL,
    provider text NOT NULL,
    secret_ref text NOT NULL,
    scope text,
    expires_at timestamptz,
    status text NOT NULL CHECK (status IN ('active', 'expired', 'revoked')),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE evidence_records (
    id text PRIMARY KEY,
    project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    kind text NOT NULL,
    source_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    summary text,
    confidence double precision CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1)),
    data_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    sensitive boolean NOT NULL DEFAULT false,
    captured_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE evidence_artifacts (
    evidence_id text NOT NULL REFERENCES evidence_records(id) ON DELETE CASCADE,
    artifact_id text NOT NULL REFERENCES artifacts(id) ON DELETE CASCADE,
    role text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (evidence_id, artifact_id, role)
);

CREATE TABLE knowledge_chunks (
    id text PRIMARY KEY,
    project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    evidence_id text NOT NULL REFERENCES evidence_records(id) ON DELETE CASCADE,
    source_kind text NOT NULL,
    title text,
    content_text text NOT NULL,
    content_sha256 text NOT NULL,
    chunk_index integer NOT NULL CHECK (chunk_index >= 0),
    metadata_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (evidence_id, chunk_index)
);

CREATE TABLE orchestrator_runs (
    id text PRIMARY KEY,
    project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    status text NOT NULL CHECK (status IN ('created', 'running', 'awaiting_human_approval', 'completed', 'failed', 'canceled')),
    current_node text,
    state_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    started_at timestamptz,
    completed_at timestamptz,
    error_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE agent_runs (
    id text PRIMARY KEY,
    project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    stage text NOT NULL,
    task_kind text NOT NULL,
    status text NOT NULL CHECK (status IN ('created', 'running', 'completed', 'needs_human', 'rejected_by_policy', 'failed')),
    input_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    output_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    model_trace_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    error_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    started_at timestamptz,
    completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE jobs (
    id text PRIMARY KEY,
    project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    job_type text NOT NULL,
    status text NOT NULL CHECK (status IN ('queued', 'running', 'completed', 'failed', 'canceled')),
    payload_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts integer NOT NULL DEFAULT 1 CHECK (max_attempts > 0),
    run_after timestamptz NOT NULL DEFAULT now(),
    locked_by text,
    locked_at timestamptz,
    error_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE product_maps (
    id text PRIMARY KEY,
    project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    version integer NOT NULL CHECK (version > 0),
    status text NOT NULL CHECK (status IN ('draft', 'review_ready', 'approved', 'deprecated')),
    summary text,
    map_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    generated_by_agent_run_id text REFERENCES agent_runs(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (project_id, version)
);

CREATE TABLE workflow_graphs (
    id text PRIMARY KEY,
    project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    graph_key text NOT NULL,
    version integer NOT NULL CHECK (version > 0),
    status text NOT NULL CHECK (status IN ('draft', 'review_ready', 'approved', 'rehearsing', 'validated', 'asset_ready', 'deprecated')),
    name text,
    summary text,
    entry_point text,
    schema_version text NOT NULL,
    graph_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_from_graph_id text REFERENCES workflow_graphs(id) ON DELETE SET NULL,
    created_by_agent_run_id text REFERENCES agent_runs(id) ON DELETE SET NULL,
    approved_by_user_id text REFERENCES users(id) ON DELETE SET NULL,
    approved_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (project_id, graph_key, version)
);

CREATE TABLE workflow_graph_patches (
    id text PRIMARY KEY,
    project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    base_graph_id text NOT NULL REFERENCES workflow_graphs(id) ON DELETE CASCADE,
    patch_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    status text NOT NULL CHECK (status IN ('proposed', 'approved', 'rejected', 'applied')),
    rationale text,
    created_by_agent_run_id text REFERENCES agent_runs(id) ON DELETE SET NULL,
    reviewed_by_user_id text REFERENCES users(id) ON DELETE SET NULL,
    applied_graph_id text REFERENCES workflow_graphs(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE execution_runs (
    id text PRIMARY KEY,
    project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    workflow_graph_id text NOT NULL REFERENCES workflow_graphs(id) ON DELETE CASCADE,
    run_type text NOT NULL CHECK (run_type IN ('rehearsal', 'recording', 'regression')),
    status text NOT NULL CHECK (status IN ('created', 'running', 'passed', 'failed', 'canceled')),
    pass_rate double precision CHECK (pass_rate IS NULL OR (pass_rate >= 0 AND pass_rate <= 1)),
    run_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    started_at timestamptz,
    completed_at timestamptz,
    error_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE step_results (
    id text PRIMARY KEY,
    execution_run_id text NOT NULL REFERENCES execution_runs(id) ON DELETE CASCADE,
    node_id text NOT NULL,
    status text NOT NULL CHECK (status IN ('created', 'running', 'passed', 'failed', 'skipped')),
    duration_ms integer CHECK (duration_ms IS NULL OR duration_ms >= 0),
    result_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    started_at timestamptz,
    completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE assets (
    id text PRIMARY KEY,
    project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    workflow_graph_id text REFERENCES workflow_graphs(id) ON DELETE SET NULL,
    execution_run_id text REFERENCES execution_runs(id) ON DELETE SET NULL,
    kind text NOT NULL,
    status text NOT NULL CHECK (status IN ('requested', 'generating', 'generated', 'failed', 'approved', 'deprecated')),
    title text,
    uri text,
    mime_type text,
    provenance_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    review_status text NOT NULL CHECK (review_status IN ('not_required', 'pending', 'approved', 'rejected')),
    approved_by_user_id text REFERENCES users(id) ON DELETE SET NULL,
    approved_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE asset_reviews (
    id text PRIMARY KEY,
    asset_id text NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    reviewer_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    decision text NOT NULL CHECK (decision IN ('approved', 'rejected', 'changes_requested')),
    notes text,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX project_contexts_one_current
    ON project_contexts(project_id)
    WHERE is_current;

CREATE INDEX organization_members_user_id_idx ON organization_members(user_id);
CREATE INDEX projects_org_created_at_idx ON projects(org_id, created_at DESC);
CREATE INDEX projects_status_idx ON projects(status);
CREATE INDEX audit_logs_project_created_at_idx ON audit_logs(project_id, created_at DESC);
CREATE INDEX audit_logs_org_created_at_idx ON audit_logs(org_id, created_at DESC);

CREATE INDEX project_contexts_project_created_at_idx ON project_contexts(project_id, created_at DESC);
CREATE INDEX project_inputs_project_created_at_idx ON project_inputs(project_id, created_at DESC);
CREATE INDEX project_inputs_kind_idx ON project_inputs(kind);
CREATE INDEX secret_refs_org_created_at_idx ON secret_refs(org_id, created_at DESC);
CREATE INDEX secret_refs_project_created_at_idx ON secret_refs(project_id, created_at DESC);
CREATE INDEX artifacts_org_created_at_idx ON artifacts(org_id, created_at DESC);
CREATE INDEX artifacts_project_created_at_idx ON artifacts(project_id, created_at DESC);

CREATE INDEX evidence_records_project_created_at_idx ON evidence_records(project_id, created_at DESC);
CREATE INDEX evidence_records_kind_idx ON evidence_records(kind);
CREATE INDEX knowledge_chunks_project_created_at_idx ON knowledge_chunks(project_id, created_at DESC);
CREATE INDEX product_maps_project_created_at_idx ON product_maps(project_id, created_at DESC);

CREATE INDEX workflow_graphs_project_created_at_idx ON workflow_graphs(project_id, created_at DESC);
CREATE INDEX workflow_graphs_status_idx ON workflow_graphs(status);
CREATE INDEX workflow_graphs_project_key_version_idx ON workflow_graphs(project_id, graph_key, version);
CREATE INDEX workflow_graph_patches_project_created_at_idx ON workflow_graph_patches(project_id, created_at DESC);

CREATE INDEX orchestrator_runs_project_created_at_idx ON orchestrator_runs(project_id, created_at DESC);
CREATE INDEX agent_runs_project_created_at_idx ON agent_runs(project_id, created_at DESC);
CREATE INDEX jobs_project_created_at_idx ON jobs(project_id, created_at DESC);
CREATE INDEX jobs_status_run_after_idx ON jobs(status, run_after);
CREATE INDEX execution_runs_project_created_at_idx ON execution_runs(project_id, created_at DESC);
CREATE INDEX execution_runs_status_idx ON execution_runs(status);
CREATE INDEX step_results_execution_node_idx ON step_results(execution_run_id, node_id);
CREATE INDEX assets_project_created_at_idx ON assets(project_id, created_at DESC);
CREATE INDEX assets_status_idx ON assets(status);
CREATE INDEX assets_workflow_kind_status_idx ON assets(workflow_graph_id, kind, status);

CREATE INDEX project_contexts_context_json_gin ON project_contexts USING gin (context_json);
CREATE INDEX project_inputs_input_json_gin ON project_inputs USING gin (input_json);
CREATE INDEX evidence_records_data_json_gin ON evidence_records USING gin (data_json);
CREATE INDEX workflow_graphs_graph_json_gin ON workflow_graphs USING gin (graph_json);
CREATE INDEX execution_runs_run_json_gin ON execution_runs USING gin (run_json);
