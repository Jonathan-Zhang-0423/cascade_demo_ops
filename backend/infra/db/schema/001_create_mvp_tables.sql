-- Cascade DemoOps MVP initial schema.
-- This migration is intentionally JSONB-heavy for MVP speed, while preserving stable aggregate boundaries.

create table if not exists tenants (
  id text primary key,
  name text not null,
  plan text not null default 'pilot',
  created_at timestamptz not null default now()
);

create table if not exists users (
  id text primary key,
  tenant_id text not null references tenants(id),
  email text not null,
  role text not null,
  created_at timestamptz not null default now()
);

create table if not exists projects (
  id text primary key,
  tenant_id text not null references tenants(id),
  product_name text not null,
  status text not null default 'created',
  created_by text not null,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create table if not exists project_contexts (
  id text primary key,
  project_id text not null references projects(id),
  version integer not null,
  context_json jsonb not null,
  created_by text not null,
  created_at timestamptz not null default now(),
  unique(project_id, version)
);

create table if not exists github_integrations (
  id text primary key,
  project_id text not null references projects(id),
  installation_id text,
  repo_owner text not null,
  repo_name text not null,
  branch text not null,
  permission text not null default 'read-only',
  status text not null default 'configured',
  created_at timestamptz not null default now()
);

create table if not exists ssh_integrations (
  id text primary key,
  project_id text not null references projects(id),
  host text not null,
  port integer not null default 22,
  username text not null,
  auth_secret_ref jsonb not null,
  allowed_paths jsonb not null default '[]',
  allowed_commands jsonb not null default '[]',
  permission text not null default 'read-only',
  status text not null default 'configured',
  created_at timestamptz not null default now()
);

create table if not exists evidence_records (
  id text primary key,
  project_id text not null references projects(id),
  kind text not null,
  source_ref jsonb not null,
  summary text,
  data_json jsonb not null default '{}',
  artifacts_json jsonb not null default '[]',
  created_at timestamptz not null default now()
);

create table if not exists workflow_graphs (
  id text primary key,
  project_id text not null references projects(id),
  version integer not null,
  status text not null,
  graph_json jsonb not null,
  created_by text not null,
  created_at timestamptz not null default now(),
  unique(project_id, version)
);

create table if not exists execution_runs (
  id text primary key,
  project_id text not null references projects(id),
  workflow_graph_id text not null,
  mode text not null,
  status text not null,
  run_json jsonb not null default '{}',
  started_at timestamptz,
  completed_at timestamptz,
  created_at timestamptz not null default now()
);

create table if not exists step_results (
  id text primary key,
  execution_run_id text not null references execution_runs(id),
  step_id text not null,
  status text not null,
  attempts integer not null default 1,
  failure_type text,
  result_json jsonb not null default '{}',
  created_at timestamptz not null default now()
);

create table if not exists assets (
  id text primary key,
  project_id text not null references projects(id),
  workflow_graph_id text not null,
  execution_run_id text,
  type text not null,
  status text not null,
  version integer not null,
  artifact_json jsonb,
  provenance_json jsonb not null default '{}',
  freshness_status text not null default 'fresh',
  created_at timestamptz not null default now()
);

create table if not exists reviews (
  id text primary key,
  project_id text not null references projects(id),
  target_type text not null,
  target_id text not null,
  decision text not null,
  reviewer_id text not null,
  comments text,
  created_at timestamptz not null default now()
);

create table if not exists audit_logs (
  id text primary key,
  project_id text references projects(id),
  actor text not null,
  action text not null,
  target text not null,
  result text not null,
  metadata_json jsonb not null default '{}',
  created_at timestamptz not null default now()
);

create index if not exists idx_evidence_project_kind on evidence_records(project_id, kind);
create index if not exists idx_workflow_project_status on workflow_graphs(project_id, status);
create index if not exists idx_execution_project_status on execution_runs(project_id, status);
create index if not exists idx_assets_project_status on assets(project_id, status);
