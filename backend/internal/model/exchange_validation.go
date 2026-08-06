package model

import (
	"encoding/json"
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
	if err := ValidatePackageConfidenceSummary(pkg); err != nil {
		return err
	}
	if envelope.Policy.HumanApprovalRequired {
		if pkg.ApprovedAt.IsZero() || pkg.SafetyReport.HumanApproval.ApprovalID == "" || pkg.SafetyReport.HumanApproval.PlanDigestSHA256 == "" {
			return errors.New("human approval metadata is required by exchange policy")
		}
		if pkg.SafetyReport.HumanApproval.ApprovalSubjectDigestSHA256 != "" {
			digest, err := ComputePackageApprovalSubjectDigest(*pkg)
			if err != nil {
				return err
			}
			if digest != pkg.SafetyReport.HumanApproval.ApprovalSubjectDigestSHA256 {
				return errors.New("human approval subject digest does not match execution package")
			}
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
	if data, err := json.Marshal(pkg); err != nil {
		return err
	} else if len(data) > 256*1024 {
		return errors.New("package_size_exceeded: client execution package exceeds 256 KiB")
	}
	if err := validateClientExecutionPackageContents(pkg); err != nil {
		return err
	}
	return ValidatePackageConfidenceSummary(pkg)
}

// ValidateClientExecutionPackageForLocalTestWaiver validates the complete App
// draft structure without weakening the cloud upload contract. It accepts only
// a browser-agent-outline-v1 draft that remains explicitly non-uploadable.
func ValidateClientExecutionPackageForLocalTestWaiver(pkg *ClientExecutionPackage) error {
	if pkg == nil {
		return errors.New("client execution package is nil")
	}
	if pkg.PackageID == "" || pkg.OrgID == "" || pkg.ProjectID == "" {
		return errors.New("client execution package missing required identity fields")
	}
	if pkg.SchemaVersion != ClientExecutionPackageSchemaVersion {
		return fmt.Errorf("client execution package schema_version must be %q", ClientExecutionPackageSchemaVersion)
	}
	if data, err := json.Marshal(pkg); err != nil {
		return err
	} else if len(data) > 256*1024 {
		return errors.New("package_size_exceeded: client execution package exceeds 256 KiB")
	}
	if pkg.SafetyReport.AllowedToUpload {
		return errors.New("local test waiver requires safety_report.allowed_to_upload=false")
	}
	if err := validateClientExecutionPackageStructure(pkg); err != nil {
		return err
	}
	if pkg.ExecutableScriptBundle.ScriptManifest.Runtime != ExecutableScriptRuntimeBrowserAgentOutlineV1 {
		return errors.New("local test waiver requires browser-agent-outline-v1 runtime")
	}
	return nil
}

func ValidateRecordingResultPackageForRender(result *RecordingResultPackage, source *ClientExecutionPackage) error {
	if err := validateRecordingResultPackageCore(result, source); err != nil {
		return err
	}
	if err := result.ValidateDeliverySecurity(); err != nil {
		return err
	}
	return nil
}

// ValidateLocalTestRecordingResultPackageForRender is deliberately separate
// from the formal validator. Exchange Intake continues to call only the formal
// validator and therefore rejects these local, unencrypted artifacts.
func ValidateLocalTestRecordingResultPackageForRender(result *RecordingResultPackage, source *ClientExecutionPackage) error {
	if err := ValidateClientExecutionPackageForLocalTestWaiver(source); err != nil {
		return err
	}
	if err := validateRecordingResultPackageCore(result, source); err != nil {
		return err
	}
	metadata := result.Delivery.ResultPackageRef.Metadata
	if metadata == nil || metadata["dev_test_only"] != true || metadata["not_for_exchange_upload"] != true || metadata["test_only_waiver"] != true || metadata["formal_exchange"] != false || metadata["app_generated"] != true || metadata["transport_authenticated"] != false {
		return errors.New("local test result package markers are invalid")
	}
	waiverID, _ := metadata["waiver_id"].(string)
	bundleHash, _ := metadata["source_bundle_hash_sha256"].(string)
	planHash, _ := metadata["source_plan_hash_sha256"].(string)
	if strings.TrimSpace(waiverID) == "" || strings.TrimSpace(bundleHash) == "" || strings.TrimSpace(planHash) == "" {
		return errors.New("local test result package waiver and source hashes are required")
	}
	bundle := source.ExecutableScriptBundle
	if bundle == nil || bundleHash != bundle.Reproducibility.BundleHashSHA256 || planHash != bundle.Reproducibility.PlanHashSHA256 {
		return errors.New("local test result package source hashes do not match the App draft")
	}
	if result.Delivery.RecipientKind != "local_test_only" || result.Delivery.RecipientKeyID != "" || result.Delivery.EncryptionAlg != "" || result.Delivery.AckRequired {
		return errors.New("local test result package delivery must remain local, unencrypted, and acknowledgement-free")
	}
	if result.Delivery.ResultPackageRef.Encrypted || result.Delivery.ResultPackageRef.RecipientKeyID != "" {
		return errors.New("local test result package ref must not claim Exchange encryption")
	}
	for _, asset := range result.Delivery.AssetRefs {
		if asset.Encrypted || asset.RecipientKeyID != "" {
			return errors.New("local test result assets must not claim Exchange encryption")
		}
	}
	return nil
}

func validateRecordingResultPackageCore(result *RecordingResultPackage, source *ClientExecutionPackage) error {
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
	return validateClientExecutionPackageStructure(pkg)
}

func validateClientExecutionPackageStructure(pkg *ClientExecutionPackage) error {
	if pkg.WorkflowGraph == nil {
		return errors.New("client execution package workflow_graph is required")
	}
	if pkg.WorkflowGraph.ID == "" || pkg.WorkflowGraph.ProjectID != "" && pkg.WorkflowGraph.ProjectID != pkg.ProjectID {
		return errors.New("client execution package workflow_graph identity is invalid")
	}
	if pkg.ProjectContextSummary.ProductURL == "" || pkg.ProjectContextSummary.TargetAudience == "" {
		return errors.New("client execution package project_context_summary missing product_url or target_audience")
	}
	if err := validateSourceBindingSummary(pkg); err != nil {
		return err
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

func validateSourceBindingSummary(pkg *ClientExecutionPackage) error {
	if pkg == nil {
		return nil
	}
	if pkg.SourceBindingSummary == nil {
		if ClientPackageContainsSourceDerivedExecutionEvidence(pkg) {
			return errors.New("source_binding_summary is required for source-derived execution evidence")
		}
		return nil
	}
	summary := pkg.SourceBindingSummary
	if summary.SchemaVersion != ProductSourceBindingAssessmentSchemaVersion || summary.AssessmentHash == "" {
		return errors.New("source_binding_summary is invalid")
	}
	if summary.EffectiveMode == ProductSourceModeBlocked || summary.EffectiveMode == ProductSourceModeMixed && summary.Status != ProductSourceBindingMatched {
		return errors.New("product_source_mismatch: source binding does not allow mixed execution evidence")
	}
	if summary.EffectiveMode == ProductSourceModePageOnly && ClientPackageContainsSourceDerivedExecutionEvidence(pkg) {
		return &SourceEvidenceLeakageError{}
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
	if spec.Timeline.TargetDurationSec < 0 {
		return errors.New("recording_run_spec.timeline.target_duration_sec must not be negative")
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
	if bundle.BrowserAgentContract == nil {
		return errors.New("browser-agent outline bundle browser_agent_contract is required")
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
	if bundle.BrowserAgentContract.SchemaVersion != BrowserAgentContractSchemaVersion {
		return fmt.Errorf("browser_agent_contract schema_version must be %q", BrowserAgentContractSchemaVersion)
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
	if bundle.BrowserAgentContract.ProjectID != bundle.ProjectID || bundle.BrowserAgentContract.WorkflowGraphID != bundle.WorkflowGraphID {
		return errors.New("browser_agent_contract identity does not match executable script bundle")
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
		if err := validateStageApprovalStage(stage); err != nil {
			return err
		}
		if strings.TrimSpace(stage.Objective) == "" {
			return fmt.Errorf("stage_approval_plan stage %q is missing objective", stage.NodeID)
		}
		if stage.TargetContract == nil || strings.TrimSpace(stage.TargetContract.SemanticID) == "" {
			return fmt.Errorf("stage_approval_plan stage %q is missing target_contract", stage.NodeID)
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
		if stage.TargetContract == nil || strings.TrimSpace(stage.TargetContract.SemanticID) == "" {
			return fmt.Errorf("script_outline stage %q is missing target_contract", stage.NodeID)
		}
		outlineNodeIDs[stage.NodeID] = true
	}
	for _, step := range bundle.PlanJSON.Steps {
		if stepRequiresBrowserAgentValidation(step) && !stepHasRequiredBrowserAgentValidation(step) {
			return fmt.Errorf("execution script document step %q is missing required browser-agent validation", step.NodeID)
		}
		if stepRequiresBrowserAgentValidation(step) && (step.TargetContract == nil || strings.TrimSpace(step.TargetContract.SemanticID) == "") {
			return fmt.Errorf("execution script document step %q is missing target_contract", step.NodeID)
		}
	}
	for nodeID := range planNodeIDs {
		if !stageNodeIDs[nodeID] {
			return fmt.Errorf("stage_approval_plan missing plan node_id %q", nodeID)
		}
		if !outlineNodeIDs[nodeID] {
			return fmt.Errorf("script_outline missing plan node_id %q", nodeID)
		}
	}
	if err := ValidateBrowserAgentOutlineConsistency(bundle); err != nil {
		return err
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
	contractHash, err := DigestCanonicalJSON(bundle.BrowserAgentContract)
	if err != nil {
		return err
	}
	if bundle.Reproducibility.BrowserAgentContractHashSHA256 == "" || bundle.Reproducibility.BrowserAgentContractHashSHA256 != contractHash {
		return errors.New("executable script bundle browser agent contract hash mismatch")
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

// validateStageApprovalStage keeps the business facts required by the outline
// protocol in the App-approved plan. Server may adapt locators at runtime, but
// it must not infer a missing business stage, route state, or success rule.
func validateStageApprovalStage(stage StageApprovalStage) error {
	if stage.ID == "" || stage.Order < 1 || stage.NodeID == "" {
		return fmt.Errorf("stage_approval_plan stage %q is missing id, order, or node_id", stage.NodeID)
	}
	if !validBusinessStageKind(stage.StageKind) {
		return fmt.Errorf("stage_approval_plan stage %q has unsupported or missing stage_kind", stage.NodeID)
	}
	if !validBusinessRouteState(stage.RouteState) {
		return fmt.Errorf("stage_approval_plan stage %q has unsupported or missing route_state", stage.NodeID)
	}
	if strings.TrimSpace(stage.BusinessIntent) == "" {
		return fmt.Errorf("stage_approval_plan stage %q is missing business_intent", stage.NodeID)
	}
	if strings.TrimSpace(stage.EntryRoute) == "" && strings.TrimSpace(stage.TargetRoute) == "" && len(stage.CandidateRoutes) == 0 {
		return fmt.Errorf("stage_approval_plan stage %q requires entry_route, target_route, or candidate_routes", stage.NodeID)
	}
	if stage.Interaction.Kind == "" {
		return fmt.Errorf("stage_approval_plan stage %q is missing interaction", stage.NodeID)
	}
	if strings.TrimSpace(stage.SuccessState) == "" {
		return fmt.Errorf("stage_approval_plan stage %q is missing success_state", stage.NodeID)
	}
	if len(stage.WaitConditions) == 0 {
		return fmt.Errorf("stage_approval_plan stage %q is missing wait_conditions", stage.NodeID)
	}
	if len(stage.CapturePoints) == 0 && stage.CapturePlan == nil {
		return fmt.Errorf("stage_approval_plan stage %q requires capture_points or capture_plan", stage.NodeID)
	}
	if stage.Confidence < 0 || stage.Confidence > 1 {
		return fmt.Errorf("stage_approval_plan stage %q confidence must be between 0 and 1", stage.NodeID)
	}
	return nil
}

func validBusinessStageKind(value BusinessStageKind) bool {
	switch value {
	case BusinessStageKindSessionSetup, BusinessStageKindBusinessAction, BusinessStageKindBusinessInput,
		BusinessStageKindModeSelection, BusinessStageKindBusinessSubmit, BusinessStageKindObserveProgress,
		BusinessStageKindFinalObserve:
		return true
	default:
		return false
	}
}

func validBusinessRouteState(value BusinessRouteState) bool {
	switch value {
	case BusinessRouteStateUnauthenticated, BusinessRouteStateWorkspace, BusinessRouteStateCreationFlow,
		BusinessRouteStateProjectDetail, BusinessRouteStateBuildRunning:
		return true
	default:
		return false
	}
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

func stepRequiresBrowserAgentValidation(step ScriptStep) bool {
	switch step.Action.Type {
	case GraphActionNavigate, GraphActionClick, GraphActionFill, GraphActionSelect, GraphActionUpload, GraphActionAPICall:
		return true
	case GraphActionWait, GraphActionInspect:
		return step.StageKind == BusinessStageKindSessionSetup || step.StageKind == BusinessStageKindObserveProgress || step.StageKind == BusinessStageKindFinalObserve
	default:
		return false
	}
}

func stepHasRequiredBrowserAgentValidation(step ScriptStep) bool {
	for _, validation := range step.Validations {
		if !validation.Required {
			continue
		}
		switch validation.Kind {
		case "url_matches", "element_visible", "element_hidden", "text_contains", "attribute_equals", "value_equals", "element_count", "page_title_contains":
			return true
		}
	}
	return false
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
