package model

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

func ValidateClientExecutionPackageIntake(envelope *ExchangeEnvelope, pkg *ClientExecutionPackage, now time.Time, seenNonces map[string]bool, verifier ExchangeSignatureVerifier) error {
	if envelope == nil {
		return errors.New("exchange envelope is nil")
	}
	if pkg == nil {
		return errors.New("client execution package is nil")
	}
	if err := envelope.ValidateForPayload(*pkg, now, seenNonces, verifier); err != nil {
		return err
	}
	if envelope.PackageKind != ExchangePackageKindClientExecution {
		return fmt.Errorf("exchange package_kind must be %q", ExchangePackageKindClientExecution)
	}
	if envelope.PayloadSchemaVersion != ClientExecutionPackageSchemaVersion {
		return fmt.Errorf("exchange payload_schema_version must be %q", ClientExecutionPackageSchemaVersion)
	}
	if envelope.OrgID != pkg.OrgID || envelope.ProjectID != pkg.ProjectID {
		return errors.New("exchange envelope identity does not match client execution package")
	}
	if pkg.PackageID == "" || pkg.OrgID == "" || pkg.ProjectID == "" {
		return errors.New("client execution package missing required identity fields")
	}
	if pkg.SchemaVersion != ClientExecutionPackageSchemaVersion {
		return fmt.Errorf("client execution package schema_version must be %q", ClientExecutionPackageSchemaVersion)
	}
	if err := validateClientExecutionPackageContents(pkg); err != nil {
		return err
	}
	if envelope.Policy.HumanApprovalRequired {
		if pkg.ApprovedAt.IsZero() || pkg.SafetyReport.HumanApproval.ApprovalID == "" || pkg.SafetyReport.HumanApproval.PlanDigestSHA256 == "" {
			return errors.New("human approval metadata is required by exchange policy")
		}
	}
	return nil
}

func ValidateClientExecutionPackageForCloudExecution(pkg *ClientExecutionPackage) error {
	if pkg == nil {
		return errors.New("client execution package is nil")
	}
	if pkg.PackageID == "" || pkg.OrgID == "" || pkg.ProjectID == "" {
		return errors.New("client execution package missing required identity fields")
	}
	if pkg.SchemaVersion != ClientExecutionPackageSchemaVersion {
		return fmt.Errorf("client execution package schema_version must be %q", ClientExecutionPackageSchemaVersion)
	}
	return validateClientExecutionPackageContents(pkg)
}

func ValidateRecordingResultPackageForRender(result *RecordingResultPackage, source *ClientExecutionPackage) error {
	if result == nil {
		return errors.New("recording result package is nil")
	}
	if result.SchemaVersion != RecordingResultPackageSchemaVersion {
		return fmt.Errorf("recording result package schema_version must be %q", RecordingResultPackageSchemaVersion)
	}
	if result.ResultID == "" || result.SourcePackageID == "" {
		return errors.New("recording result package missing required identity fields")
	}
	if source != nil && result.SourcePackageID != source.PackageID {
		return errors.New("recording result package source_package_id does not match client package")
	}
	if result.Status != RecordingResultStatusGenerated && result.Status != RecordingResultStatusDelivered && result.Status != RecordingResultStatusAcked {
		return fmt.Errorf("recording result package status %q is not renderable", result.Status)
	}
	if result.ExecutionTrace == nil {
		return errors.New("recording result package execution_trace is required for render")
	}
	if source != nil && source.WorkflowGraph != nil && result.ExecutionTrace.WorkflowGraphID != "" && result.ExecutionTrace.WorkflowGraphID != source.WorkflowGraph.ID {
		return errors.New("recording result package execution_trace workflow graph does not match client package")
	}
	if len(result.StepResults) == 0 && len(result.ExecutionTrace.StepResults) == 0 {
		return errors.New("recording result package must include step_results or execution_trace.step_results")
	}
	if len(result.GeneratedAssets) == 0 && len(result.ExecutionTrace.Artifacts) == 0 {
		return errors.New("recording result package must include generated_assets or execution_trace.artifacts")
	}
	for _, asset := range append(append([]ArtifactRef{}, result.GeneratedAssets...), result.ExecutionTrace.Artifacts...) {
		if asset.ID == "" || asset.URI == "" {
			return errors.New("recording result package contains artifact without id or uri")
		}
	}
	return nil
}

func validateClientExecutionPackageContents(pkg *ClientExecutionPackage) error {
	if !pkg.SafetyReport.AllowedToUpload {
		return errors.New("client execution package safety_report.allowed_to_upload must be true")
	}
	if pkg.WorkflowGraph == nil {
		return errors.New("client execution package workflow_graph is required")
	}
	if pkg.WorkflowGraph.ID == "" || pkg.WorkflowGraph.ProjectID != "" && pkg.WorkflowGraph.ProjectID != pkg.ProjectID {
		return errors.New("client execution package workflow_graph identity is invalid")
	}
	if pkg.ProjectContextSummary.ProductURL == "" || pkg.ProjectContextSummary.TargetAudience == "" {
		return errors.New("client execution package project_context_summary missing product_url or target_audience")
	}
	if err := validateRecordingRunSpec(pkg.RecordingRunSpec); err != nil {
		return err
	}
	if pkg.ExecutableScriptBundle == nil {
		return errors.New("client execution package executable_script_bundle is required")
	}
	if err := validateExecutableBundleAgainstPackage(pkg.ExecutableScriptBundle, pkg); err != nil {
		return err
	}
	for _, grant := range pkg.CredentialGrants {
		if grant.GrantID == "" || grant.Kind == "" || grant.Purpose == "" {
			return errors.New("credential grant missing required identity fields")
		}
		if grant.CloudSecretRef == "" && grant.EncryptedSecretAttachmentID == "" {
			return errors.New("credential grant must use cloud_secret_ref or encrypted_secret_attachment_id")
		}
	}
	if pkg.Reproducibility.GraphHashSHA256 == "" {
		return errors.New("client execution package graph hash is required")
	}
	graphDigest, err := DigestCanonicalJSON(pkg.WorkflowGraph)
	if err != nil {
		return err
	}
	if graphDigest != pkg.Reproducibility.GraphHashSHA256 {
		return errors.New("client execution package graph hash mismatch")
	}
	return nil
}

func validateRecordingRunSpec(spec RecordingRunSpec) error {
	if spec.BaseURL == "" {
		return errors.New("recording_run_spec.base_url is required")
	}
	if len(spec.AllowedDomains) == 0 {
		return errors.New("recording_run_spec.allowed_domains is required")
	}
	if spec.Browser.Engine == "" {
		return errors.New("recording_run_spec.browser.engine is required")
	}
	if spec.Timeline.TargetDurationSec <= 0 {
		return errors.New("recording_run_spec.timeline.target_duration_sec must be positive")
	}
	if !spec.Outputs.RawRecording || !spec.Outputs.Trace {
		return errors.New("recording_run_spec.outputs must request raw_recording and trace")
	}
	return nil
}

func validateExecutableBundleAgainstPackage(bundle *ExecutableRecordingScriptBundle, pkg *ClientExecutionPackage) error {
	if bundle.SchemaVersion != ExecutableRecordingScriptBundleSchemaVersion {
		return fmt.Errorf("executable script bundle schema_version must be %q", ExecutableRecordingScriptBundleSchemaVersion)
	}
	if bundle.ProjectID != pkg.ProjectID || bundle.WorkflowGraphID != pkg.WorkflowGraph.ID {
		return errors.New("executable script bundle identity does not match client package")
	}
	if bundle.ScriptManifest.EntryFunction != "runCascadeRecording" {
		return errors.New("executable script bundle entry_function must be runCascadeRecording")
	}
	if bundle.ScriptManifest.Language != "typescript" || bundle.ScriptManifest.Runtime != "playwright-restricted-sandbox" {
		return errors.New("executable script bundle must use restricted TypeScript Playwright runtime")
	}
	if len(bundle.ScriptManifest.DependencyAllowlist) > 0 {
		return errors.New("executable script bundle dependency_allowlist must be empty")
	}
	if len(bundle.ScriptManifest.StepNodeIDs) == 0 {
		return errors.New("executable script bundle script_manifest.step_node_ids is required")
	}
	if bundle.PlanJSON == nil {
		return errors.New("executable script bundle plan_json is required")
	}
	if err := validateScriptDocumentAgainstPackage(bundle.PlanJSON, pkg); err != nil {
		return err
	}
	if err := validateScriptManifestAgainstPlan(bundle); err != nil {
		return err
	}
	if bundle.PlaywrightScript.InlineSource == "" && bundle.PlaywrightScript.Artifact == nil {
		return errors.New("executable script bundle playwright_script must include inline_source or artifact")
	}
	if bundle.PlaywrightScript.SHA256 == "" {
		return errors.New("playwright_script sha256 is required")
	}
	if bundle.PlaywrightScript.InlineSource != "" {
		scriptHash := SHA256Hex([]byte(bundle.PlaywrightScript.InlineSource))
		if bundle.PlaywrightScript.SHA256 != scriptHash {
			return errors.New("playwright_script sha256 mismatch")
		}
		if bundle.Reproducibility.ScriptHashSHA256 == "" {
			return errors.New("executable script bundle reproducibility script hash is required")
		}
		if bundle.Reproducibility.ScriptHashSHA256 != scriptHash {
			return errors.New("executable script bundle reproducibility script hash mismatch")
		}
	}
	if bundle.ApprovalMarkdown.SHA256 == "" {
		return errors.New("approval_markdown sha256 is required")
	}
	if bundle.ApprovalMarkdown.InlineMarkdown != "" {
		markdownHash := SHA256Hex([]byte(bundle.ApprovalMarkdown.InlineMarkdown))
		if bundle.ApprovalMarkdown.SHA256 != markdownHash {
			return errors.New("approval_markdown sha256 mismatch")
		}
		if bundle.Reproducibility.MarkdownHashSHA256 == "" {
			return errors.New("executable script bundle reproducibility markdown hash is required")
		}
		if bundle.Reproducibility.MarkdownHashSHA256 != markdownHash {
			return errors.New("executable script bundle reproducibility markdown hash mismatch")
		}
	}
	if bundle.Reproducibility.PlanHashSHA256 == "" {
		return errors.New("executable script bundle reproducibility plan hash is required")
	}
	planHash, err := bundle.PlanJSON.ComputeScriptHash()
	if err != nil {
		return err
	}
	if bundle.Reproducibility.PlanHashSHA256 != planHash {
		return errors.New("executable script bundle plan hash mismatch")
	}
	if bundle.Reproducibility.BundleHashSHA256 == "" {
		return errors.New("executable script bundle hash is required")
	}
	bundleHash, err := bundle.ComputeBundleHash()
	if err != nil {
		return err
	}
	if bundle.Reproducibility.BundleHashSHA256 != bundleHash {
		return errors.New("executable script bundle hash mismatch")
	}
	if bundle.Validation == nil || !bundle.Validation.Valid {
		return errors.New("executable script bundle must carry a successful validation")
	}
	if len(bundle.SecurityPolicy.AllowedDomains) == 0 {
		return errors.New("executable script bundle security_policy.allowed_domains is required")
	}
	if !allDomainsAllowed(bundle.SecurityPolicy.AllowedDomains, pkg.RecordingRunSpec.AllowedDomains) {
		return errors.New("executable script bundle allowed domains exceed recording_run_spec.allowed_domains")
	}
	return nil
}

func validateScriptManifestAgainstPlan(bundle *ExecutableRecordingScriptBundle) error {
	planNodeIDs := map[string]bool{}
	for _, step := range bundle.PlanJSON.Steps {
		if step.NodeID != "" {
			planNodeIDs[step.NodeID] = true
		}
	}
	manifestNodeIDs := map[string]bool{}
	for _, nodeID := range bundle.ScriptManifest.StepNodeIDs {
		if !planNodeIDs[nodeID] {
			return fmt.Errorf("executable script bundle manifest node_id %q is not in plan_json", nodeID)
		}
		manifestNodeIDs[nodeID] = true
	}
	for nodeID := range planNodeIDs {
		if !manifestNodeIDs[nodeID] {
			return fmt.Errorf("executable script bundle manifest missing plan node_id %q", nodeID)
		}
		if bundle.PlaywrightScript.InlineSource != "" && !strings.Contains(bundle.PlaywrightScript.InlineSource, `"`+nodeID+`"`) {
			return fmt.Errorf("executable script source missing plan node_id %q", nodeID)
		}
	}
	return nil
}

func validateScriptDocumentAgainstPackage(doc *ExecutionScriptDocument, pkg *ClientExecutionPackage) error {
	if doc.SchemaVersion != ExecutionScriptDocumentSchemaVersion {
		return fmt.Errorf("execution script document schema_version must be %q", ExecutionScriptDocumentSchemaVersion)
	}
	if doc.ProjectID != pkg.ProjectID || doc.WorkflowGraphID != pkg.WorkflowGraph.ID || doc.GraphVersion != pkg.WorkflowGraph.Version {
		return errors.New("execution script document identity does not match client package workflow graph")
	}
	if len(doc.Steps) == 0 {
		return errors.New("execution script document steps are required")
	}
	graphNodeIDs := map[string]bool{}
	for _, node := range pkg.WorkflowGraph.Nodes {
		if node != nil && node.ID != "" {
			graphNodeIDs[node.ID] = true
		}
	}
	for _, step := range doc.Steps {
		if step.NodeID == "" || !graphNodeIDs[step.NodeID] {
			return fmt.Errorf("execution script document step node_id %q is not in workflow graph", step.NodeID)
		}
	}
	if err := validateRecordingRunSpec(doc.RecordingRunSpec); err != nil {
		return err
	}
	if !allDomainsAllowed(doc.RecordingRunSpec.AllowedDomains, pkg.RecordingRunSpec.AllowedDomains) {
		return errors.New("execution script document allowed domains exceed package recording_run_spec.allowed_domains")
	}
	return nil
}

func allDomainsAllowed(values []string, allowed []string) bool {
	allowedSet := map[string]bool{}
	for _, domain := range allowed {
		normalized := strings.ToLower(strings.TrimSpace(domain))
		if normalized != "" {
			allowedSet[normalized] = true
		}
	}
	for _, domain := range values {
		normalized := strings.ToLower(strings.TrimSpace(domain))
		if normalized == "" || !allowedSet[normalized] {
			return false
		}
	}
	return true
}
