package database

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoreMigrationContainsRequiredTables(t *testing.T) {
	sql := readCoreMigration(t)
	requiredTables := []string{
		"organizations",
		"users",
		"organization_members",
		"projects",
		"project_contexts",
		"project_inputs",
		"secret_refs",
		"artifacts",
		"evidence_records",
		"evidence_artifacts",
		"knowledge_chunks",
		"product_maps",
		"workflow_graphs",
		"workflow_graph_patches",
		"orchestrator_runs",
		"agent_runs",
		"jobs",
		"execution_runs",
		"step_results",
		"assets",
		"asset_reviews",
		"audit_logs",
	}
	for _, table := range requiredTables {
		if !strings.Contains(sql, "CREATE TABLE "+table+" ") {
			t.Fatalf("migration is missing table %s", table)
		}
	}
}

func TestCoreMigrationKeepsSecretsAndArtifactsOutOfRows(t *testing.T) {
	sql := strings.ToLower(readCoreMigration(t))
	forbidden := []string{
		"secret_value",
		"password text",
		"private_key text",
		"bytea",
	}
	for _, token := range forbidden {
		if strings.Contains(sql, token) {
			t.Fatalf("migration must not persist raw secret or binary payload token %q", token)
		}
	}
	if !strings.Contains(sql, "secret_ref text not null") {
		t.Fatal("migration should persist secret_ref metadata")
	}
	if !strings.Contains(sql, "uri text not null") {
		t.Fatal("migration should persist artifact URI metadata")
	}
}

func TestCoreMigrationHasJSONBAndProvenanceIndexes(t *testing.T) {
	sql := strings.ToLower(readCoreMigration(t))
	required := []string{
		"project_contexts_context_json_gin",
		"project_inputs_input_json_gin",
		"evidence_records_data_json_gin",
		"workflow_graphs_graph_json_gin",
		"execution_runs_run_json_gin",
		"workflow_graphs_project_key_version_idx",
		"step_results_execution_node_idx",
		"assets_workflow_kind_status_idx",
		"audit_logs_org_created_at_idx",
		"project_contexts_one_current",
	}
	for _, token := range required {
		if !strings.Contains(sql, token) {
			t.Fatalf("migration is missing required index %s", token)
		}
	}
}

func TestCoreMigrationAllowsExpectedProjectInputKinds(t *testing.T) {
	sql := readCoreMigration(t)
	kinds := []string{
		"'product_url'",
		"'code'",
		"'requirement_document'",
		"'webpage_screenshot'",
		"'release_note'",
		"'brand_kit'",
		"'credential'",
	}
	for _, kind := range kinds {
		if !strings.Contains(sql, kind) {
			t.Fatalf("migration is missing project input kind %s", kind)
		}
	}
}

func TestExchangeMigrationContainsRequiredTables(t *testing.T) {
	sql := readExchangeMigration(t)
	requiredTables := []string{
		"exchange_packages",
		"package_artifacts",
		"cloud_recording_jobs",
		"credential_grants",
		"result_packages",
	}
	for _, table := range requiredTables {
		if !strings.Contains(sql, "CREATE TABLE "+table+" ") {
			t.Fatalf("exchange migration is missing table %s", table)
		}
	}
}

func TestExchangeMigrationKeepsPlaintextPayloadsAndSecretsOutOfRows(t *testing.T) {
	sql := strings.ToLower(readExchangeMigration(t))
	forbidden := []string{
		"payload_json",
		"plaintext",
		"raw_secret",
		"secret_value",
		"password text",
		"private_key text",
		"source_archive",
		"bytea",
	}
	for _, token := range forbidden {
		if strings.Contains(sql, token) {
			t.Fatalf("exchange migration must not persist forbidden token %q", token)
		}
	}
	required := []string{
		"payload_digest_sha256 text not null",
		"ciphertext_digest_sha256 text",
		"crypto_suite text not null",
		"key_wrapping_mode text not null",
		"server_key_id text not null",
		"cloud_secret_ref text",
		"encrypted_secret_artifact_id text",
		"uri text not null",
		"encrypted boolean not null default true",
		"recipient_kind text not null",
		"recipient_key_id text not null",
		"encryption_alg text not null",
	}
	for _, token := range required {
		if !strings.Contains(sql, token) {
			t.Fatalf("exchange migration is missing security token %q", token)
		}
	}
}

func TestExchangeMigrationHasIdempotencyStatusAndJSONBIndexes(t *testing.T) {
	sql := strings.ToLower(readExchangeMigration(t))
	required := []string{
		"unique (org_id, idempotency_key)",
		"exchange_packages_org_idempotency_idx",
		"exchange_packages_payload_digest_idx",
		"exchange_packages_ciphertext_digest_idx",
		"exchange_packages_crypto_suite_idx",
		"cloud_recording_jobs_worker_status_idx",
		"package_artifacts_sha256_idx",
		"package_artifacts_recipient_key_idx",
		"result_packages_exchange_idx",
		"exchange_packages_policy_json_gin",
		"cloud_recording_jobs_run_spec_json_gin",
		"result_packages_verification_json_gin",
	}
	for _, token := range required {
		if !strings.Contains(sql, token) {
			t.Fatalf("exchange migration is missing required index or constraint %s", token)
		}
	}
}

func TestExecutionDeliveryMigrationContainsWorkflowTablesAndIdempotency(t *testing.T) {
	sql := strings.ToLower(readMigration(t, "003_create_execution_delivery_workflow.sql"))
	for _, table := range []string{"execution_stage_events", "generated_scripts", "render_jobs", "result_reviews", "revision_requests"} {
		if !strings.Contains(sql, "create table "+table+" ") {
			t.Fatalf("execution delivery migration is missing table %s", table)
		}
	}
	for _, token := range []string{
		"unique (exchange_package_id, sequence_no)",
		"unique (result_package_id, idempotency_key)",
		"execution_stage_events_package_sequence_idx",
		"generated_scripts_sha256_idx",
		"revision_requests_status_idx",
	} {
		if !strings.Contains(sql, token) {
			t.Fatalf("execution delivery migration is missing %s", token)
		}
	}
	for _, forbidden := range []string{"video bytea", "recording bytea", "source_archive", "raw_secret", "password text"} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("execution delivery migration must not persist %q", forbidden)
		}
	}
}

func readCoreMigration(t *testing.T) string {
	return readMigration(t, "001_create_core_tables.sql")
}

func readExchangeMigration(t *testing.T) string {
	return readMigration(t, "002_create_exchange_protocol_tables.sql")
}

func readMigration(t *testing.T, fileName string) string {
	t.Helper()
	path := filepath.Join("..", "..", "infra", "db", "migrations", fileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	return string(data)
}
