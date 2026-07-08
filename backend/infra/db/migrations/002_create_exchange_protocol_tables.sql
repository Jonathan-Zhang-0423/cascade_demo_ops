-- Cascade DemoOps exchange package protocol schema v1.
-- This migration stores package metadata, cloud job state, vault references,
-- and encrypted artifact descriptors. It intentionally stores only metadata
-- and references for execution content, credentials, source summaries, and files.

CREATE TABLE exchange_packages (
    id text PRIMARY KEY,
    org_id text NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    envelope_id text NOT NULL UNIQUE,
    package_kind text NOT NULL CHECK (package_kind IN ('client_execution', 'recording_result')),
    status text NOT NULL CHECK (status IN (
        'initialized',
        'uploaded',
        'accepted',
        'queued',
        'running',
        'completed',
        'failed',
        'canceled',
        'expired'
    )),
    schema_version text NOT NULL,
    payload_schema_version text NOT NULL,
    idempotency_key text NOT NULL,
    payload_digest_sha256 text NOT NULL,
    payload_size_bytes bigint CHECK (payload_size_bytes IS NULL OR payload_size_bytes >= 0),
    producer_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    crypto_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    policy_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    envelope_metadata_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    error_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz,
    accepted_at timestamptz,
    completed_at timestamptz,
    UNIQUE (org_id, idempotency_key)
);

CREATE TABLE package_artifacts (
    id text PRIMARY KEY,
    org_id text NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    exchange_package_id text REFERENCES exchange_packages(id) ON DELETE CASCADE,
    result_package_id text,
    role text NOT NULL,
    kind text NOT NULL,
    uri text NOT NULL,
    mime_type text,
    sha256 text NOT NULL,
    size_bytes bigint CHECK (size_bytes IS NULL OR size_bytes >= 0),
    encrypted boolean NOT NULL DEFAULT true,
    sensitive boolean NOT NULL DEFAULT false,
    compression_alg text,
    metadata_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz
);

CREATE TABLE cloud_recording_jobs (
    id text PRIMARY KEY,
    org_id text NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    exchange_package_id text NOT NULL REFERENCES exchange_packages(id) ON DELETE CASCADE,
    workflow_graph_id text REFERENCES workflow_graphs(id) ON DELETE SET NULL,
    execution_run_id text REFERENCES execution_runs(id) ON DELETE SET NULL,
    status text NOT NULL CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'canceled')),
    worker_id text,
    run_spec_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    runtime_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    error_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    completed_at timestamptz
);

CREATE TABLE credential_grants (
    id text PRIMARY KEY,
    org_id text NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    exchange_package_id text NOT NULL REFERENCES exchange_packages(id) ON DELETE CASCADE,
    grant_id text NOT NULL,
    kind text NOT NULL,
    purpose text NOT NULL,
    scope text,
    cloud_secret_ref text,
    encrypted_secret_artifact_id text REFERENCES package_artifacts(id) ON DELETE SET NULL,
    allowed_domains_json jsonb NOT NULL DEFAULT '[]'::jsonb,
    allowed_operations_json jsonb NOT NULL DEFAULT '[]'::jsonb,
    expires_at timestamptz,
    rotation_required_after_run boolean NOT NULL DEFAULT true,
    delete_after_run boolean NOT NULL DEFAULT true,
    status text NOT NULL CHECK (status IN ('active', 'used', 'revoked', 'expired')),
    created_at timestamptz NOT NULL DEFAULT now(),
    used_at timestamptz,
    UNIQUE (exchange_package_id, grant_id)
);

CREATE TABLE result_packages (
    id text PRIMARY KEY,
    org_id text NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    exchange_package_id text NOT NULL REFERENCES exchange_packages(id) ON DELETE CASCADE,
    cloud_recording_job_id text NOT NULL REFERENCES cloud_recording_jobs(id) ON DELETE CASCADE,
    result_id text NOT NULL UNIQUE,
    status text NOT NULL CHECK (status IN ('generated', 'delivered', 'acked', 'failed')),
    schema_version text NOT NULL,
    result_digest_sha256 text,
    trace_summary_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    verification_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    delivery_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    error_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    delivered_at timestamptz,
    acked_at timestamptz,
    expires_at timestamptz
);

ALTER TABLE package_artifacts
    ADD CONSTRAINT package_artifacts_result_package_fk
    FOREIGN KEY (result_package_id) REFERENCES result_packages(id) ON DELETE CASCADE;

CREATE INDEX exchange_packages_org_created_at_idx ON exchange_packages(org_id, created_at DESC);
CREATE INDEX exchange_packages_project_created_at_idx ON exchange_packages(project_id, created_at DESC);
CREATE INDEX exchange_packages_status_idx ON exchange_packages(status);
CREATE INDEX exchange_packages_org_idempotency_idx ON exchange_packages(org_id, idempotency_key);
CREATE INDEX exchange_packages_payload_digest_idx ON exchange_packages(payload_digest_sha256);

CREATE INDEX package_artifacts_project_created_at_idx ON package_artifacts(project_id, created_at DESC);
CREATE INDEX package_artifacts_exchange_role_idx ON package_artifacts(exchange_package_id, role);
CREATE INDEX package_artifacts_result_role_idx ON package_artifacts(result_package_id, role);
CREATE INDEX package_artifacts_sha256_idx ON package_artifacts(sha256);

CREATE INDEX cloud_recording_jobs_project_created_at_idx ON cloud_recording_jobs(project_id, created_at DESC);
CREATE INDEX cloud_recording_jobs_exchange_idx ON cloud_recording_jobs(exchange_package_id);
CREATE INDEX cloud_recording_jobs_status_idx ON cloud_recording_jobs(status);
CREATE INDEX cloud_recording_jobs_worker_status_idx ON cloud_recording_jobs(worker_id, status);

CREATE INDEX credential_grants_project_created_at_idx ON credential_grants(project_id, created_at DESC);
CREATE INDEX credential_grants_exchange_idx ON credential_grants(exchange_package_id);
CREATE INDEX credential_grants_status_idx ON credential_grants(status);

CREATE INDEX result_packages_project_created_at_idx ON result_packages(project_id, created_at DESC);
CREATE INDEX result_packages_exchange_idx ON result_packages(exchange_package_id);
CREATE INDEX result_packages_job_idx ON result_packages(cloud_recording_job_id);
CREATE INDEX result_packages_status_idx ON result_packages(status);

CREATE INDEX exchange_packages_policy_json_gin ON exchange_packages USING gin (policy_json);
CREATE INDEX exchange_packages_crypto_json_gin ON exchange_packages USING gin (crypto_json);
CREATE INDEX cloud_recording_jobs_run_spec_json_gin ON cloud_recording_jobs USING gin (run_spec_json);
CREATE INDEX result_packages_verification_json_gin ON result_packages USING gin (verification_json);
