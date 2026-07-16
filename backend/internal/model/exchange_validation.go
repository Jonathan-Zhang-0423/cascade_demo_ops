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
	if err := result.ValidateDeliverySecurity(); err != nil {
		return err
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
	if err := ValidateSandboxPolicyForPackage(pkg); err != nil {
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
	runtime := strings.TrimSpace(bundle.ScriptManifest.Runtime)
	if runtime == "" {
		return errors.New("executable script bundle script_manifest.runtime is required")
	}
	switch runtime {
	case ExecutableScriptRuntimePlaywrightRestrictedSandbox:
		if bundle.ScriptManifest.EntryFunction != "runCascadeRecording" {
			return errors.New("restricted Playwright bundle entry_function must be runCascadeRecording")
		}
	case ExecutableScriptRuntimeBrowserAgentOutlineV1:
		if bundle.ScriptManifest.EntryFunction != "runBrowserAgentOutline" {
			return errors.New("browser-agent outline bundle entry_function must be runBrowserAgentOutline")
		}
	default:
		return fmt.Errorf("executable script bundle runtime %q is not supported", runtime)
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
	switch runtime {
	case ExecutableScriptRuntimePlaywrightRestrictedSandbox:
		if err := validateRestrictedPlaywrightBundle(bundle); err != nil {
			return err
		}
	case ExecutableScriptRuntimeBrowserAgentOutlineV1:
		if err := validateBrowserAgentOutlineBundle(bundle); err != nil {
			return err
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

func validateRestrictedPlaywrightBundle(bundle *ExecutableRecordingScriptBundle) error {
	if bundle.ScriptManifest.Language != "typescript" {
		return errors.New("executable script bundle language must be typescript for restricted Playwright runtime")
	}
	if bundle.PlaywrightScript.InlineSource == "" && bundle.PlaywrightScript.Artifact == nil {
		return errors.New("executable script bundle playwright_script must include inline_source or artifact")
	}
	if bundle.PlaywrightScript.SHA256 == "" {
		return errors.New("playwright_script sha256 is required")
	}
	if bundle.PlaywrightScript.InlineSource == "" {
		return nil
	}
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
	return nil
}

func validateBrowserAgentOutlineBundle(bundle *ExecutableRecordingScriptBundle) error {
	if bundle.StageApprovalPlan == nil {
		return errors.New("browser-agent outline bundle stage_approval_plan is required")
	}
	if bundle.ScriptOutline == nil {
		return errors.New("browser-agent outline bundle script_outline is required")
	}
	if bundle.AgentPromptPolicy == nil {
		return errors.New("browser-agent outline bundle agent_prompt_policy is required")
	}
	if bundle.StageApprovalPlan.SchemaVersion != StageApprovalPlanSchemaVersion {
		return fmt.Errorf("stage_approval_plan schema_version must be %q", StageApprovalPlanSchemaVersion)
	}
	if bundle.ScriptOutline.SchemaVersion != BrowserAgentScriptOutlineSchemaVersion {
		return fmt.Errorf("script_outline schema_version must be %q", BrowserAgentScriptOutlineSchemaVersion)
	}
	if bundle.AgentPromptPolicy.SchemaVersion != BrowserAgentPromptPolicySchemaVersion {
		return fmt.Errorf("agent_prompt_policy schema_version must be %q", BrowserAgentPromptPolicySchemaVersion)
	}
	if bundle.StageApprovalPlan.ProjectID != bundle.ProjectID || bundle.StageApprovalPlan.WorkflowGraphID != bundle.WorkflowGraphID {
		return errors.New("stage_approval_plan identity does not match executable script bundle")
	}
	if bundle.ScriptOutline.ProjectID != bundle.ProjectID || bundle.ScriptOutline.WorkflowGraphID != bundle.WorkflowGraphID {
		return errors.New("script_outline identity does not match executable script bundle")
	}
	if bundle.AgentPromptPolicy.ProjectID != bundle.ProjectID || bundle.AgentPromptPolicy.WorkflowGraphID != bundle.WorkflowGraphID {
		return errors.New("agent_prompt_policy identity does not match executable script bundle")
	}
	if bundle.ScriptOutline.Runtime != ExecutableScriptRuntimeBrowserAgentOutlineV1 || bundle.AgentPromptPolicy.Runtime != ExecutableScriptRuntimeBrowserAgentOutlineV1 {
		return errors.New("browser-agent outline bundle runtime fields must be browser-agent-outline-v1")
	}
	if len(bundle.StageApprovalPlan.Stages) == 0 || len(bundle.ScriptOutline.Stages) == 0 {
		return errors.New("browser-agent outline bundle requires stage plan and outline stages")
	}
	if len(bundle.AgentPromptPolicy.ImmutableFields) == 0 || len(bundle.AgentPromptPolicy.EditableFields) == 0 || strings.TrimSpace(bundle.AgentPromptPolicy.SystemPrompt) == "" {
		return errors.New("agent_prompt_policy must declare system_prompt, immutable_fields, and editable_fields")
	}
	planNodeIDs := map[string]bool{}
	for _, step := range bundle.PlanJSON.Steps {
		planNodeIDs[step.NodeID] = true
	}
	stageNodeIDs := map[string]bool{}
	for _, stage := range bundle.StageApprovalPlan.Stages {
		if stage.NodeID == "" || !planNodeIDs[stage.NodeID] {
			return fmt.Errorf("stage_approval_plan stage node_id %q is not in plan_json", stage.NodeID)
		}
		if strings.TrimSpace(stage.Objective) == "" {
			return fmt.Errorf("stage_approval_plan stage %q is missing objective", stage.NodeID)
		}
		if stage.DurationMS > 0 && stage.DurationMS < 10000 {
			return fmt.Errorf("stage_approval_plan stage %q duration must be at least 10000ms", stage.NodeID)
		}
		if !stageHasEvidence(stage) {
			return fmt.Errorf("stage_approval_plan stage %q is missing evidence chain", stage.NodeID)
		}
		stageNodeIDs[stage.NodeID] = true
	}
	outlineNodeIDs := map[string]bool{}
	for _, stage := range bundle.ScriptOutline.Stages {
		if stage.NodeID == "" || !planNodeIDs[stage.NodeID] {
			return fmt.Errorf("script_outline stage node_id %q is not in plan_json", stage.NodeID)
		}
		if strings.TrimSpace(stage.Objective) == "" {
			return fmt.Errorf("script_outline stage %q is missing objective", stage.NodeID)
		}
		if len(stage.Interactions) == 0 {
			return fmt.Errorf("script_outline stage %q is missing interactions", stage.NodeID)
		}
		if len(stage.EvidenceRefs) == 0 && len(stage.Components) == 0 {
			return fmt.Errorf("script_outline stage %q is missing evidence chain", stage.NodeID)
		}
		outlineNodeIDs[stage.NodeID] = true
	}
	for nodeID := range planNodeIDs {
		if !stageNodeIDs[nodeID] {
			return fmt.Errorf("stage_approval_plan missing plan node_id %q", nodeID)
		}
		if !outlineNodeIDs[nodeID] {
			return fmt.Errorf("script_outline missing plan node_id %q", nodeID)
		}
	}
	for _, uncertainty := range append(append([]StageUncertainty{}, bundle.StageApprovalPlan.UncertaintyReport...), bundle.ScriptOutline.UncertaintyReport...) {
		if uncertainty.Blocking {
			return fmt.Errorf("browser-agent outline contains blocking uncertainty %q", uncertainty.ID)
		}
	}
	stageHash, err := DigestCanonicalJSON(bundle.StageApprovalPlan)
	if err != nil {
		return err
	}
	if bundle.Reproducibility.StagePlanHashSHA256 == "" || bundle.Reproducibility.StagePlanHashSHA256 != stageHash {
		return errors.New("executable script bundle stage plan hash mismatch")
	}
	outlineHash, err := DigestCanonicalJSON(bundle.ScriptOutline)
	if err != nil {
		return err
	}
	if bundle.Reproducibility.OutlineHashSHA256 == "" || bundle.Reproducibility.OutlineHashSHA256 != outlineHash {
		return errors.New("executable script bundle outline hash mismatch")
	}
	promptHash, err := DigestCanonicalJSON(bundle.AgentPromptPolicy)
	if err != nil {
		return err
	}
	if bundle.Reproducibility.PromptPolicyHashSHA256 == "" || bundle.Reproducibility.PromptPolicyHashSHA256 != promptHash {
		return errors.New("executable script bundle prompt policy hash mismatch")
	}
	if bundle.UnderstandingDossier != nil {
		dossierHash, err := DigestCanonicalJSON(bundle.UnderstandingDossier)
		if err != nil {
			return err
		}
		if bundle.Reproducibility.UnderstandingDossierHashSHA256 == "" || bundle.Reproducibility.UnderstandingDossierHashSHA256 != dossierHash {
			return errors.New("executable script bundle understanding dossier hash mismatch")
		}
	}
	if bundle.PlaywrightScript.InlineSource != "" {
		scriptHash := SHA256Hex([]byte(bundle.PlaywrightScript.InlineSource))
		if bundle.PlaywrightScript.SHA256 != "" && bundle.PlaywrightScript.SHA256 != scriptHash {
			return errors.New("playwright_script sha256 mismatch")
		}
		if bundle.Reproducibility.ScriptHashSHA256 != "" && bundle.Reproducibility.ScriptHashSHA256 != scriptHash {
			return errors.New("executable script bundle reproducibility script hash mismatch")
		}
	}
	return nil
}

func stageHasEvidence(stage StageApprovalStage) bool {
	return len(stage.EvidenceRefs) > 0 ||
		len(stage.ComponentRefs) > 0 ||
		len(stage.APIRefs) > 0 ||
		len(stage.StyleRefs) > 0 ||
		len(stage.DataModelRefs) > 0 ||
		len(stage.Interaction.EvidenceRefs) > 0 ||
		len(stage.Interaction.Target.EvidenceRefs) > 0
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
		if err := validateCaptureSpec(step.Capture, "execution script document step "+step.NodeID+" capture"); err != nil {
			return err
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

func validateCaptureSpec(capture CaptureSpec, path string) error {
	switch capture.Scope {
	case "", CaptureScopeViewport, CaptureScopeFullPage, CaptureScopeElement:
	default:
		return fmt.Errorf("%s scope must be one of %q, %q, or %q", path, CaptureScopeViewport, CaptureScopeFullPage, CaptureScopeElement)
	}
	if capture.FullPage && capture.Scope != "" && capture.Scope != CaptureScopeFullPage {
		return fmt.Errorf("%s full_page conflicts with scope %q", path, capture.Scope)
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
