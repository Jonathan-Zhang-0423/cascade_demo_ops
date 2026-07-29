-- Durable execution events and human delivery workflow metadata.
-- Binary recordings, screenshots, traces, and videos remain in artifact storage.

CREATE TABLE execution_stage_events (
    id text PRIMARY KEY,
    org_id text NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    exchange_package_id text NOT NULL REFERENCES exchange_packages(id) ON DELETE CASCADE,
    sequence_no bigint NOT NULL CHECK (sequence_no > 0),
    stage text NOT NULL,
    status text NOT NULL,
    progress_percent integer NOT NULL DEFAULT 0 CHECK (progress_percent BETWEEN 0 AND 100),
    message text NOT NULL DEFAULT '',
    metadata_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (exchange_package_id, sequence_no)
);

CREATE TABLE generated_scripts (
    id text PRIMARY KEY,
    org_id text NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    exchange_package_id text NOT NULL REFERENCES exchange_packages(id) ON DELETE CASCADE,
    cloud_recording_job_id text REFERENCES cloud_recording_jobs(id) ON DELETE CASCADE,
    runtime text NOT NULL,
    schema_version text NOT NULL,
    script_artifact_id text REFERENCES package_artifacts(id) ON DELETE SET NULL,
    script_sha256 text NOT NULL,
    outline_sha256 text NOT NULL,
    validation_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    provenance_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE render_jobs (
    id text PRIMARY KEY,
    org_id text NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    result_package_id text REFERENCES result_packages(id) ON DELETE CASCADE,
    source_recording_job_id text NOT NULL REFERENCES cloud_recording_jobs(id) ON DELETE CASCADE,
    status text NOT NULL CHECK (status IN ('queued', 'directing', 'rendering', 'validating', 'succeeded', 'failed', 'canceled')),
    edit_plan_artifact_id text REFERENCES package_artifacts(id) ON DELETE SET NULL,
    edit_plan_sha256 text,
    renderer_version text NOT NULL DEFAULT '',
    error_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    completed_at timestamptz
);

CREATE TABLE result_reviews (
    id text PRIMARY KEY,
    org_id text NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    result_package_id text NOT NULL REFERENCES result_packages(id) ON DELETE CASCADE,
    reviewer_install_id text,
    idempotency_key text NOT NULL,
    decision text NOT NULL CHECK (decision IN ('approved', 'reedit_requested', 'rerecord_requested')),
    summary text NOT NULL DEFAULT '',
    annotations_json jsonb NOT NULL DEFAULT '[]'::jsonb,
    reviewed_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (result_package_id, idempotency_key)
);

CREATE TABLE revision_requests (
    id text PRIMARY KEY,
    org_id text NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    result_package_id text NOT NULL REFERENCES result_packages(id) ON DELETE CASCADE,
    requested_by_install_id text,
    idempotency_key text NOT NULL,
    requested_action text NOT NULL CHECK (requested_action IN ('auto', 'reedit', 'rerecord')),
    resolved_action text NOT NULL CHECK (resolved_action IN ('reedit', 'rerecord')),
    status text NOT NULL CHECK (status IN ('queued', 'running', 'completed', 'failed', 'canceled')),
    summary text NOT NULL DEFAULT '',
    issues_json jsonb NOT NULL DEFAULT '[]'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    UNIQUE (result_package_id, idempotency_key)
);

CREATE INDEX execution_stage_events_package_sequence_idx ON execution_stage_events(exchange_package_id, sequence_no);
CREATE INDEX execution_stage_events_project_created_at_idx ON execution_stage_events(project_id, created_at DESC);
CREATE INDEX generated_scripts_package_created_at_idx ON generated_scripts(exchange_package_id, created_at DESC);
CREATE INDEX generated_scripts_sha256_idx ON generated_scripts(script_sha256);
CREATE INDEX render_jobs_project_created_at_idx ON render_jobs(project_id, created_at DESC);
CREATE INDEX render_jobs_status_idx ON render_jobs(status);
CREATE INDEX result_reviews_package_reviewed_at_idx ON result_reviews(result_package_id, reviewed_at DESC);
CREATE INDEX revision_requests_package_created_at_idx ON revision_requests(result_package_id, created_at DESC);
CREATE INDEX revision_requests_status_idx ON revision_requests(status);
