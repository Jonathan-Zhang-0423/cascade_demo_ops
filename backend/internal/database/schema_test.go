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

func readCoreMigration(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "infra", "db", "migrations", "001_create_core_tables.sql")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	return string(data)
}
