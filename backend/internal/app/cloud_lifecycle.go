package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"cascade-demoops/backend/internal/agents"
	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
)

const (
	defaultDesktopOrgID      = "org_desktop"
	defaultDesktopInstallID  = "desktop-dev-install"
	defaultDesktopAppVersion = "desktop-web-dev"
)

type ClientExecutionPackageBuild struct {
	OrgID      string                       `json:"org_id"`
	ProjectID  string                       `json:"project_id"`
	Package    model.ClientExecutionPackage `json:"package"`
	Envelope   model.ExchangeEnvelope       `json:"envelope"`
	PayloadRef model.EncryptedPayloadRef    `json:"payload_ref"`
}

type CloudUploadInitRequest struct {
	OrgID     string `json:"org_id,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
}

type CloudUploadInitResult struct {
	Build     *ClientExecutionPackageBuild       `json:"build,omitempty"`
	Init      model.ExecutionPackageInitResponse `json:"init"`
	CloudBase string                             `json:"cloud_base_url,omitempty"`
}

type CloudUploadPackageRequest struct {
	OrgID    string                       `json:"org_id,omitempty"`
	UploadID string                       `json:"upload_id"`
	Build    *ClientExecutionPackageBuild `json:"build,omitempty"`
}

type CloudUploadPackageResult struct {
	Build     *ClientExecutionPackageBuild         `json:"build,omitempty"`
	Upload    model.ExecutionPackageUploadResponse `json:"upload"`
	CloudBase string                               `json:"cloud_base_url,omitempty"`
}

type CloudStatusRequest struct {
	OrgID             string `json:"org_id,omitempty"`
	ExchangePackageID string `json:"exchange_package_id"`
}

type CloudResultRequest struct {
	OrgID           string `json:"org_id,omitempty"`
	ResultPackageID string `json:"result_package_id"`
}

type CloudAckRequest struct {
	OrgID             string   `json:"org_id,omitempty"`
	ResultPackageID   string   `json:"result_package_id"`
	ReceivedAssetIDs  []string `json:"received_asset_ids,omitempty"`
	VerifiedChecksums bool     `json:"verified_checksums"`
	AckedByInstallID  string   `json:"acked_by_install_id,omitempty"`
	ExchangePackageID string   `json:"exchange_package_id,omitempty"`
	UseResultSummary  bool     `json:"use_result_summary,omitempty"`
}

type CloudLifecycleRequest struct {
	UserInput          *orchestrator.UserInput `json:"user_input,omitempty"`
	OrgID              string                  `json:"org_id,omitempty"`
	ProjectID          string                  `json:"project_id,omitempty"`
	AutoAck            *bool                   `json:"auto_ack,omitempty"`
	PollIntervalMillis int                     `json:"poll_interval_ms,omitempty"`
	TimeoutSeconds     int                     `json:"timeout_sec,omitempty"`
}

type CloudLifecycleResult struct {
	State     *orchestrator.CascadeState           `json:"state,omitempty"`
	Build     *ClientExecutionPackageBuild         `json:"build,omitempty"`
	Init      model.ExecutionPackageInitResponse   `json:"init"`
	Upload    model.ExecutionPackageUploadResponse `json:"upload"`
	Status    model.ExecutionPackageStatusResponse `json:"status"`
	Result    *model.RecordingResultPackage        `json:"result,omitempty"`
	Ack       *model.ResultPackageAckResponse      `json:"ack,omitempty"`
	CloudBase string                               `json:"cloud_base_url,omitempty"`
}

func (s *Service) BuildClientExecutionPackage(ctx context.Context, projectID string, orgID string) (ClientExecutionPackageBuild, error) {
	state, err := s.states.Load(ctx, projectID)
	if err != nil {
		return ClientExecutionPackageBuild{}, err
	}
	return buildClientExecutionPackageFromState(state, orgID, time.Now().UTC())
}

func (s *Service) BuildCloudClientExecutionPackage(ctx context.Context, projectID string, orgID string) (ClientExecutionPackageBuild, ExchangeSession, error) {
	build, err := s.BuildClientExecutionPackage(ctx, projectID, orgID)
	if err != nil {
		return ClientExecutionPackageBuild{}, ExchangeSession{}, err
	}
	session, err := s.EnsureExchangeSession(ctx, build.Package.ProjectContextSummary.ProductURL, build.OrgID, build.ProjectID)
	if err != nil {
		return ClientExecutionPackageBuild{}, ExchangeSession{}, err
	}
	build, err = s.signBuildWithExchangeSession(build, session)
	if err != nil {
		return ClientExecutionPackageBuild{}, ExchangeSession{}, err
	}
	return build, session, nil
}

func (s *Service) signBuildWithExchangeSession(build ClientExecutionPackageBuild, session ExchangeSession) (ClientExecutionPackageBuild, error) {
	record, err := s.exchangeIdentityStore().load()
	if err != nil && session.AuthMode != exchangeAuthModeDevToken {
		return ClientExecutionPackageBuild{}, err
	}
	if err := normalizeClientExecutionPackageForUpload(&build.Package); err != nil {
		return ClientExecutionPackageBuild{}, err
	}
	canonical, err := model.CanonicalJSON(build.Package)
	if err != nil {
		return ClientExecutionPackageBuild{}, err
	}
	digest := model.SHA256Hex(canonical)
	build.Envelope.Producer.InstallID = session.InstallID
	build.Envelope.Producer.DeviceID = session.DeviceID
	build.Envelope.Crypto.ServerKeyID = session.ServerKeyID
	build.Envelope.Crypto.SignatureAlg = exchangeInstallationKeyAlg
	build.Envelope.Crypto.SignatureKeyID = session.SigningKeyID
	build.Envelope.Crypto.PayloadDigestSHA256 = digest
	build.Envelope.Crypto.CiphertextDigestSHA256 = digest
	build.Envelope.Crypto.EncryptedContentKey = "wrapped-for-" + session.ServerKeyID
	build.PayloadRef.SHA256 = digest
	build.PayloadRef.DevPlaintext = session.DevPlaintext
	build.Envelope.PayloadRef = build.PayloadRef
	if session.AuthMode == exchangeAuthModeDevToken && record.SigningPrivateKeyBase64 == "" {
		build.Envelope.Crypto.Signature = "desktop-dev-token-compatible-signature"
		return build, nil
	}
	signature, err := signCanonicalPayload(record, build.Envelope, canonical)
	if err != nil {
		return ClientExecutionPackageBuild{}, err
	}
	build.Envelope.Crypto.Signature = signature
	return build, nil
}

func (s *Service) InitCloudExecutionPackageUpload(ctx context.Context, build ClientExecutionPackageBuild) (model.ExecutionPackageInitResponse, error) {
	session, err := s.EnsureExchangeSession(ctx, build.Package.ProjectContextSummary.ProductURL, build.OrgID, build.ProjectID)
	if err != nil {
		return model.ExecutionPackageInitResponse{}, err
	}
	client := &http.Client{Timeout: 60 * time.Second}
	return cloudPostJSON[model.ExecutionPackageInitResponse](ctx, client, session.BaseURL+"/v1/execution-packages/init", session.SessionToken, "", model.ExecutionPackageInitRequest{
		OrgID:       build.OrgID,
		ProjectID:   build.ProjectID,
		PackageKind: model.ExchangePackageKindClientExecution,
		Producer: model.ExchangeProducer{
			AppVersion:     defaultDesktopAppVersion,
			InstallID:      session.InstallID,
			DeviceID:       session.DeviceID,
			RuntimeProfile: "desktop-product-run",
		},
	})
}

func (s *Service) UploadBuiltCloudExecutionPackage(ctx context.Context, uploadID string, build ClientExecutionPackageBuild) (model.ExecutionPackageUploadResponse, error) {
	if strings.TrimSpace(uploadID) == "" {
		return model.ExecutionPackageUploadResponse{}, errors.New("upload_id is required")
	}
	session, err := s.EnsureExchangeSession(ctx, build.Package.ProjectContextSummary.ProductURL, build.OrgID, build.ProjectID)
	if err != nil {
		return model.ExecutionPackageUploadResponse{}, err
	}
	build, err = s.signBuildWithExchangeSession(build, session)
	if err != nil {
		return model.ExecutionPackageUploadResponse{}, err
	}
	client := &http.Client{Timeout: 60 * time.Second}
	return cloudPostJSON[model.ExecutionPackageUploadResponse](ctx, client, session.BaseURL+"/v1/execution-packages", session.SessionToken, "", map[string]any{
		"upload_id":   uploadID,
		"envelope":    build.Envelope,
		"payload_ref": build.PayloadRef,
		"payload":     build.Package,
	})
}

func (s *Service) GetCloudExecutionPackageStatus(ctx context.Context, request CloudStatusRequest) (model.ExecutionPackageStatusResponse, error) {
	if strings.TrimSpace(request.ExchangePackageID) == "" {
		return model.ExecutionPackageStatusResponse{}, errors.New("exchange_package_id is required")
	}
	session, err := s.EnsureExchangeSession(ctx, "", request.OrgID, "")
	if err != nil {
		return model.ExecutionPackageStatusResponse{}, err
	}
	client := &http.Client{Timeout: 60 * time.Second}
	return cloudGetJSON[model.ExecutionPackageStatusResponse](ctx, client, session.BaseURL+"/v1/execution-packages/"+url.PathEscape(request.ExchangePackageID)+"/status", session.SessionToken, firstNonEmptyString(request.OrgID, defaultDesktopOrgID))
}

func (s *Service) GetCloudResultPackage(ctx context.Context, request CloudResultRequest) (model.RecordingResultPackage, error) {
	if strings.TrimSpace(request.ResultPackageID) == "" {
		return model.RecordingResultPackage{}, errors.New("result_package_id is required")
	}
	session, err := s.EnsureExchangeSession(ctx, "", request.OrgID, "")
	if err != nil {
		return model.RecordingResultPackage{}, err
	}
	client := &http.Client{Timeout: 60 * time.Second}
	return cloudGetJSON[model.RecordingResultPackage](ctx, client, session.BaseURL+"/v1/result-packages/"+url.PathEscape(request.ResultPackageID), session.SessionToken, firstNonEmptyString(request.OrgID, defaultDesktopOrgID))
}

func (s *Service) AckCloudResultPackage(ctx context.Context, request CloudAckRequest) (model.ResultPackageAckResponse, error) {
	if strings.TrimSpace(request.ResultPackageID) == "" {
		return model.ResultPackageAckResponse{}, errors.New("result_package_id is required")
	}
	session, err := s.EnsureExchangeSession(ctx, "", request.OrgID, "")
	if err != nil {
		return model.ResultPackageAckResponse{}, err
	}
	received := append([]string{}, request.ReceivedAssetIDs...)
	if len(received) == 0 && request.UseResultSummary && request.ExchangePackageID != "" {
		status, statusErr := s.GetCloudExecutionPackageStatus(ctx, CloudStatusRequest{OrgID: request.OrgID, ExchangePackageID: request.ExchangePackageID})
		if statusErr == nil && status.ResultSummary != nil {
			received = receivedAssetIDs(status.ResultSummary.Deliverables)
		}
	}
	client := &http.Client{Timeout: 60 * time.Second}
	return cloudPostJSON[model.ResultPackageAckResponse](ctx, client, session.BaseURL+"/v1/result-packages/"+url.PathEscape(request.ResultPackageID)+"/ack", session.SessionToken, firstNonEmptyString(request.OrgID, defaultDesktopOrgID), model.ResultPackageAckRequest{
		ResultPackageID:   request.ResultPackageID,
		AckedByInstallID:  firstNonEmptyString(request.AckedByInstallID, session.InstallID, defaultDesktopInstallID),
		ReceivedAssetIDs:  received,
		VerifiedChecksums: request.VerifiedChecksums,
		AckedAt:           time.Now().UTC(),
	})
}

func (s *Service) RunCloudLifecycle(ctx context.Context, request CloudLifecycleRequest) (CloudLifecycleResult, error) {
	orgID := firstNonEmptyString(request.OrgID, defaultDesktopOrgID)
	projectID := strings.TrimSpace(request.ProjectID)
	var (
		state *orchestrator.CascadeState
		err   error
	)
	if request.UserInput != nil {
		state, err = s.GenerateExecutionPackage(ctx, *request.UserInput)
		if err != nil {
			return CloudLifecycleResult{}, err
		}
		projectID = state.ProjectID
	} else if projectID != "" {
		state, err = s.states.Load(ctx, projectID)
		if err != nil {
			return CloudLifecycleResult{}, err
		}
	} else {
		return CloudLifecycleResult{}, errors.New("user_input or project_id is required")
	}
	build, err := buildClientExecutionPackageFromState(state, orgID, time.Now().UTC())
	if err != nil {
		return CloudLifecycleResult{}, err
	}
	session, err := s.EnsureExchangeSession(ctx, state.ProjectContext.ProductURL, build.OrgID, build.ProjectID)
	if err != nil {
		return CloudLifecycleResult{}, err
	}
	build, err = s.signBuildWithExchangeSession(build, session)
	if err != nil {
		return CloudLifecycleResult{}, err
	}
	client := &http.Client{Timeout: 60 * time.Second}
	initResponse, err := s.InitCloudExecutionPackageUpload(ctx, build)
	if err != nil {
		return CloudLifecycleResult{}, err
	}
	uploadResponse, err := s.UploadBuiltCloudExecutionPackage(ctx, initResponse.UploadID, build)
	if err != nil {
		return CloudLifecycleResult{}, err
	}
	status, err := s.pollCloudStatus(ctx, client, session.BaseURL, session.SessionToken, build.OrgID, uploadResponse.ExchangePackageID, request)
	if err != nil {
		return CloudLifecycleResult{}, err
	}
	var resultPackage *model.RecordingResultPackage
	if status.ResultPackageID != "" {
		result, err := s.GetCloudResultPackage(ctx, CloudResultRequest{OrgID: build.OrgID, ResultPackageID: status.ResultPackageID})
		if err != nil {
			return CloudLifecycleResult{}, err
		}
		resultPackage = &result
	}
	var ack *model.ResultPackageAckResponse
	autoAck := true
	if request.AutoAck != nil {
		autoAck = *request.AutoAck
	}
	if autoAck && resultPackage != nil && status.ResultSummary != nil && status.ResultSummary.DemoVideoCount > 0 {
		received := receivedAssetIDs(status.ResultSummary.Deliverables)
		ackResponse, err := s.AckCloudResultPackage(ctx, CloudAckRequest{
			OrgID:             build.OrgID,
			ResultPackageID:   status.ResultPackageID,
			AckedByInstallID:  defaultDesktopInstallID,
			ReceivedAssetIDs:  received,
			VerifiedChecksums: true,
		})
		if err != nil {
			return CloudLifecycleResult{}, err
		}
		ack = &ackResponse
		status, _ = cloudGetJSON[model.ExecutionPackageStatusResponse](ctx, client, session.BaseURL+"/v1/execution-packages/"+url.PathEscape(uploadResponse.ExchangePackageID)+"/status", session.SessionToken, build.OrgID)
	}
	return CloudLifecycleResult{
		State:     state,
		Build:     &build,
		Init:      initResponse,
		Upload:    uploadResponse,
		Status:    status,
		Result:    resultPackage,
		Ack:       ack,
		CloudBase: session.BaseURL,
	}, nil
}

func (s *Service) pollCloudStatus(ctx context.Context, client *http.Client, baseURL string, token string, orgID string, exchangePackageID string, request CloudLifecycleRequest) (model.ExecutionPackageStatusResponse, error) {
	interval := time.Second
	if request.PollIntervalMillis > 0 {
		interval = time.Duration(request.PollIntervalMillis) * time.Millisecond
	}
	timeout := 10 * time.Minute
	if request.TimeoutSeconds > 0 {
		timeout = time.Duration(request.TimeoutSeconds) * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		status, err := cloudGetJSON[model.ExecutionPackageStatusResponse](ctx, client, baseURL+"/v1/execution-packages/"+url.PathEscape(exchangePackageID)+"/status", token, orgID)
		if err != nil {
			return model.ExecutionPackageStatusResponse{}, err
		}
		if isTerminalExchangeStatus(status.Status) {
			return status, nil
		}
		select {
		case <-ctx.Done():
			return model.ExecutionPackageStatusResponse{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Service) cloudExchangeConfig() (string, string, error) {
	if strings.TrimSpace(s.runtime.CloudExchangeBaseURL) == "" {
		return "", "", errors.New("cloud exchange base URL discovery requires product_url or CASCADE_CLOUD_EXCHANGE_BASE_URL")
	}
	return strings.TrimRight(s.runtime.CloudExchangeBaseURL, "/"), strings.TrimSpace(s.runtime.CloudExchangeToken), nil
}

func buildClientExecutionPackageFromState(state *orchestrator.CascadeState, orgID string, now time.Time) (ClientExecutionPackageBuild, error) {
	if state == nil {
		return ClientExecutionPackageBuild{}, errors.New("cascade state is nil")
	}
	if state.ProjectContext == nil || state.WorkflowGraph == nil || state.ScriptDocument == nil || state.ExecutableScriptBundle == nil {
		return ClientExecutionPackageBuild{}, errors.New("project must have context, workflow graph, script document, and executable bundle")
	}
	if !scriptDocumentHasBusinessAction(state.ScriptDocument) {
		return ClientExecutionPackageBuild{}, errors.New("执行包没有真实业务动作，已阻止上传服务器录制。请补充页面扫描、截图标注或稳定 selector，使脚本包含 click/fill/select/upload/api_call 等至少一个有效步骤。")
	}
	orgID = firstNonEmptyString(orgID, defaultDesktopOrgID)
	project := state.ProjectContext
	graph := state.WorkflowGraph
	runSpec := state.ScriptDocument.RecordingRunSpec
	graphDigest, err := model.DigestCanonicalJSON(graph)
	if err != nil {
		return ClientExecutionPackageBuild{}, err
	}
	packageID := "pkg_" + firstNonEmptyString(state.ExecutableScriptBundle.ID, state.ScriptDocument.ID, graph.ID)
	pkg := model.ClientExecutionPackage{
		PackageID:              packageID,
		OrgID:                  orgID,
		ProjectID:              state.ProjectID,
		SchemaVersion:          model.ClientExecutionPackageSchemaVersion,
		CreatedAt:              now,
		ApprovedAt:             now,
		ProjectContextSummary:  projectContextSummaryForPackage(project, state, graphDigest),
		ProductMapSummary:      productMapSummaryForPackage(state.ProductMap),
		WorkflowGraph:          graph,
		RecordingRunSpec:       runSpec,
		ExecutableScriptBundle: state.ExecutableScriptBundle,
		EvidenceBundle:         evidenceBundleForPackage(state),
		Reproducibility: model.ReproducibilitySpec{
			GraphHashSHA256:       graphDigest,
			ScriptHashSHA256:      state.ExecutableScriptBundle.Reproducibility.PlanHashSHA256,
			SourceSnapshotDigest:  state.ExecutableScriptBundle.Reproducibility.SourceSnapshotDigest,
			InputFingerprints:     state.ExecutableScriptBundle.Reproducibility.InputFingerprints,
			DeterministicSeed:     state.ExecutableScriptBundle.Reproducibility.DeterministicSeed,
			CreatedWithAppVersion: defaultDesktopAppVersion,
		},
		SafetyReport: model.PackageSafetyReport{
			AllowedToUpload: true,
			UploadMode:      "structure_summary_only",
			HumanApproval: model.UserApprovalRecord{
				ApprovalID:       "approval_" + state.ProjectID,
				ApprovedByUserID: "desktop_user",
				ApprovedAt:       now,
				PlanDigestSHA256: state.ExecutableScriptBundle.Reproducibility.PlanHashSHA256,
				ReviewedNodeIDs:  scriptStepNodeIDsForPackage(state.ScriptDocument),
				Notes:            []string{"产品实战模式自动确认：不上传完整源码，仅上传结构摘要和受限执行脚本。"},
			},
			PIIHandling: "redaction_policy_required",
		},
		Metadata: map[string]any{
			"producer":                  "desktop_product_run",
			"auto_flow":                 true,
			"dev_plaintext_upload_mode": true,
		},
	}
	if err := redactClientExecutionPackageText(&pkg); err != nil {
		return ClientExecutionPackageBuild{}, err
	}
	findings := preflightClientExecutionPackage(state, &pkg)
	pkg.SafetyReport.PolicyFindings = append(pkg.SafetyReport.PolicyFindings, findings...)
	if blockers := blockingFindings(findings); len(blockers) > 0 {
		pkg.SafetyReport.AllowedToUpload = false
		return ClientExecutionPackageBuild{}, &packagePreflightError{Message: blockers[0].Summary, Findings: blockers}
	}
	if err := normalizeClientExecutionPackageForUpload(&pkg); err != nil {
		return ClientExecutionPackageBuild{}, err
	}
	if err := model.ValidateClientExecutionPackageForCloudExecution(&pkg); err != nil {
		return ClientExecutionPackageBuild{}, err
	}
	envelope, err := envelopeForClientExecutionPackage(pkg, now)
	if err != nil {
		return ClientExecutionPackageBuild{}, err
	}
	return ClientExecutionPackageBuild{
		OrgID:      orgID,
		ProjectID:  state.ProjectID,
		Package:    pkg,
		Envelope:   envelope,
		PayloadRef: envelope.PayloadRef,
	}, nil
}

func normalizeClientExecutionPackageForUpload(pkg *model.ClientExecutionPackage) error {
	if pkg == nil {
		return errors.New("client execution package is nil")
	}
	var graphDigest string
	if pkg.WorkflowGraph != nil {
		digest, err := model.DigestCanonicalJSON(pkg.WorkflowGraph)
		if err != nil {
			return err
		}
		graphDigest = digest
		pkg.Reproducibility.GraphHashSHA256 = digest
	}
	bundle := pkg.ExecutableScriptBundle
	if bundle == nil {
		return nil
	}
	if bundle.PlanJSON != nil {
		if graphDigest != "" {
			bundle.PlanJSON.Reproducibility.GraphHashSHA256 = graphDigest
		}
		planHash, err := bundle.PlanJSON.ComputeScriptHash()
		if err != nil {
			return err
		}
		bundle.PlanJSON.Reproducibility.ScriptHashSHA256 = planHash
		bundle.Reproducibility.PlanHashSHA256 = planHash
		pkg.Reproducibility.ScriptHashSHA256 = planHash
		if pkg.SafetyReport.HumanApproval.ApprovalID != "" {
			pkg.SafetyReport.HumanApproval.PlanDigestSHA256 = planHash
		}
		if bundle.PlaywrightScript.InlineSource != "" {
			bundle.PlaywrightScript.InlineSource = syncScriptPlanHashLiteral(bundle.PlaywrightScript.InlineSource, planHash)
		}
	}
	if bundle.PlaywrightScript.InlineSource != "" {
		source := bundle.PlaywrightScript.InlineSource
		scriptHash := model.SHA256Hex([]byte(source))
		bundle.PlaywrightScript.SHA256 = scriptHash
		bundle.PlaywrightScript.SizeBytes = int64(len([]byte(source)))
		if bundle.PlaywrightScript.Artifact != nil {
			bundle.PlaywrightScript.Artifact.SHA256 = scriptHash
			bundle.PlaywrightScript.Artifact.SizeBytes = int64(len([]byte(source)))
		}
		bundle.Reproducibility.ScriptHashSHA256 = scriptHash
	}
	if bundle.ApprovalMarkdown.InlineMarkdown != "" {
		markdown := bundle.ApprovalMarkdown.InlineMarkdown
		markdownHash := model.SHA256Hex([]byte(markdown))
		bundle.ApprovalMarkdown.SHA256 = markdownHash
		bundle.ApprovalMarkdown.SizeBytes = int64(len([]byte(markdown)))
		if bundle.ApprovalMarkdown.Artifact != nil {
			bundle.ApprovalMarkdown.Artifact.SHA256 = markdownHash
			bundle.ApprovalMarkdown.Artifact.SizeBytes = int64(len([]byte(markdown)))
		}
		bundle.Reproducibility.MarkdownHashSHA256 = markdownHash
	}
	if graphDigest != "" {
		bundle.Reproducibility.GraphHashSHA256 = graphDigest
	}
	bundleHash, err := bundle.ComputeBundleHash()
	if err != nil {
		return err
	}
	bundle.Reproducibility.BundleHashSHA256 = bundleHash
	return nil
}

func redactClientExecutionPackageText(pkg *model.ClientExecutionPackage) error {
	if pkg == nil {
		return nil
	}
	data, err := json.Marshal(pkg)
	if err != nil {
		return err
	}
	redacted := agents.RedactSensitiveUserText(string(data))
	if redacted == string(data) {
		return nil
	}
	return json.Unmarshal([]byte(redacted), pkg)
}

var scriptPlanHashLiteralPattern = regexp.MustCompile(`const cascadePlanHash = "([a-f0-9]{64})";`)
var packagePasswordLeakagePattern = regexp.MustCompile(`(?i)(密码|口令)\s*[:：=]?\s*[^\s,，。;；)）]{4,}|\b(password|passwd|pwd|passcode)\b\s*[:：=]\s*[^\s,，。;；)）]{4,}`)

func syncScriptPlanHashLiteral(source string, planHash string) string {
	if source == "" || planHash == "" || !scriptPlanHashLiteralPattern.MatchString(source) {
		return source
	}
	return scriptPlanHashLiteralPattern.ReplaceAllString(source, `const cascadePlanHash = "`+planHash+`";`)
}

func scriptDocumentHasBusinessAction(doc *model.ExecutionScriptDocument) bool {
	if doc == nil {
		return false
	}
	for _, step := range doc.Steps {
		switch step.Action.Type {
		case model.GraphActionClick, model.GraphActionFill, model.GraphActionSelect, model.GraphActionUpload, model.GraphActionAPICall:
			return true
		}
	}
	return false
}

func preflightClientExecutionPackage(state *orchestrator.CascadeState, pkg *model.ClientExecutionPackage) []model.AgentFinding {
	findings := []model.AgentFinding{}
	if pkg == nil || pkg.ExecutableScriptBundle == nil || pkg.ExecutableScriptBundle.PlanJSON == nil {
		return append(findings, packagePreflightFinding(
			"script_bundle_missing",
			model.FindingSeverityBlocking,
			"client execution package is missing executable script bundle or plan_json",
			"Regenerate the ExecutionScriptDocument and ExecutableRecordingScriptBundle before upload.",
		))
	}
	doc := pkg.ExecutableScriptBundle.PlanJSON
	if !scriptDocumentHasBusinessAction(doc) {
		findings = append(findings, packagePreflightFinding(
			"business_action_missing",
			model.FindingSeverityBlocking,
			"execution package has no real business action",
			"Add page scan evidence or stable selectors so the script includes click/fill/select/upload/api_call.",
		))
	}
	if state != nil && state.ScriptReadinessReport != nil && !state.ScriptReadinessReport.CanProceed {
		if len(state.ScriptReadinessReport.Blockers) > 0 {
			for _, blocker := range state.ScriptReadinessReport.Blockers {
				blocker.Severity = model.FindingSeverityBlocking
				findings = append(findings, blocker)
			}
		} else {
			findings = append(findings, packagePreflightFinding(
				"script_readiness_blocked",
				model.FindingSeverityBlocking,
				firstNonEmptyString(state.ScriptReadinessReport.Summary, "script readiness report blocks upload"),
				"Add missing inputs, selector evidence, or page scan data, then regenerate the package.",
			))
		}
	}
	if state != nil && state.ScriptReadinessReport != nil && state.ScriptReadinessReport.SelectorCoverage > 0 && state.ScriptReadinessReport.SelectorCoverage < 0.2 {
		findings = append(findings, packagePreflightFinding(
			"selector_coverage_low",
			model.FindingSeverityWarning,
			"selector coverage is low",
			"Add DOM/a11y scan evidence, screenshot annotations, or test ids before regenerating.",
		))
	}
	repeatedLoginCount := 0
	allowedDomains := pkg.RecordingRunSpec.AllowedDomains
	for _, step := range doc.Steps {
		if isLoginStep(step) {
			repeatedLoginCount++
		}
		if actionRequiresSelector(step.Action.Type) && !actionTargetHasStableHandle(step.Action.Target) {
			findings = append(findings, packagePreflightFinding(
				"selector_missing_"+shortID(step.NodeID),
				model.FindingSeverityBlocking,
				"business action is missing an executable selector: "+step.NodeID,
				"Provide selector, test_id, role/label/text, or selector_alternatives for click/fill/select/upload.",
			))
		}
		if isAbsoluteHTTPURL(step.Action.Target.URL) && !urlAllowedByDomains(step.Action.Target.URL, allowedDomains) {
			findings = append(findings, packagePreflightFinding(
				"domain_not_allowed_"+shortID(step.NodeID),
				model.FindingSeverityBlocking,
				"script step targets a URL outside allowed domains: "+step.NodeID,
				"Add the domain to recording_run_spec.allowed_domains or remove the navigation.",
			))
		}
		if step.Timing.DurationMS > 0 && step.Timing.DurationMS < 10000 && step.Action.Type != model.GraphActionNavigate {
			findings = append(findings, packagePreflightFinding(
				"stage_duration_short_"+shortID(step.NodeID),
				model.FindingSeverityWarning,
				"stage duration is shorter than the recommended 10 seconds: "+step.NodeID,
				"Keep each business stage around 10 seconds with natural wait/capture pacing.",
			))
		}
		if len(step.EvidenceRefs) == 0 && actionRequiresSelector(step.Action.Type) {
			findings = append(findings, packagePreflightFinding(
				"evidence_missing_"+shortID(step.NodeID),
				model.FindingSeverityWarning,
				"business action is missing route/component/selector evidence: "+step.NodeID,
				"Bind ProjectIntelligenceGraph route/component/API/selector evidence to the graph node.",
			))
		}
	}
	if repeatedLoginCount > 1 {
		findings = append(findings, packagePreflightFinding(
			"repeated_login_flow",
			model.FindingSeverityBlocking,
			"script contains repeated login steps",
			"Use login only once as a session precondition, then reuse authenticated state for business stages.",
		))
	}
	if leakage := detectPackageLeakage(pkg); leakage != "" {
		findings = append(findings, packagePreflightFinding(
			"payload_sensitive_leakage",
			model.FindingSeverityBlocking,
			"execution package may contain sensitive plaintext or source detail: "+leakage,
			"Upload only structure summary, hashes, selectors, and secret_ref; never raw secrets or full source.",
		))
	}
	return findings
}

func blockingFindings(findings []model.AgentFinding) []model.AgentFinding {
	blockers := []model.AgentFinding{}
	for _, finding := range findings {
		if finding.Severity == model.FindingSeverityBlocking {
			blockers = append(blockers, finding)
		}
	}
	return blockers
}

func packagePreflightFinding(id string, severity model.FindingSeverity, summary string, suggestedAction string) model.AgentFinding {
	return model.AgentFinding{
		ID:              id,
		Kind:            "package_preflight",
		Severity:        severity,
		Summary:         summary,
		SuggestedAction: suggestedAction,
		Confidence:      0.9,
	}
}

func actionRequiresSelector(action model.GraphActionType) bool {
	switch action {
	case model.GraphActionClick, model.GraphActionFill, model.GraphActionSelect, model.GraphActionUpload:
		return true
	default:
		return false
	}
}

func actionTargetHasStableHandle(target model.ActionTarget) bool {
	if strings.TrimSpace(target.Selector) != "" ||
		strings.TrimSpace(target.TestID) != "" ||
		strings.TrimSpace(target.Role) != "" ||
		strings.TrimSpace(target.Label) != "" ||
		strings.TrimSpace(target.Text) != "" {
		return true
	}
	for _, candidate := range target.SelectorAlternatives {
		if strings.TrimSpace(candidate.Value) != "" {
			return true
		}
	}
	return false
}

func isLoginStep(step model.ScriptStep) bool {
	text := strings.ToLower(strings.Join([]string{
		step.NodeID,
		step.Title,
		step.BusinessValue,
		step.ExpectedOutcome,
		step.Action.Target.Selector,
		step.Action.Target.Text,
		step.Action.Target.Label,
		step.Action.Target.TestID,
		step.Action.Target.URL,
	}, " "))
	return strings.Contains(text, "login") ||
		strings.Contains(text, "sign in") ||
		strings.Contains(text, "signin") ||
		strings.Contains(text, "登录") ||
		strings.Contains(text, "登入")
}

func urlAllowedByDomains(rawURL string, allowedDomains []string) bool {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" || strings.HasPrefix(rawURL, "/") || strings.HasPrefix(rawURL, "#") {
		return true
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	hostPort := strings.ToLower(parsed.Host)
	for _, allowed := range allowedDomains {
		allowedHost := normalizeAllowedDomain(allowed)
		if allowedHost == "" {
			continue
		}
		if strings.Contains(allowedHost, ":") && hostPort == allowedHost {
			return true
		}
		if host == allowedHost || strings.HasSuffix(host, "."+allowedHost) {
			return true
		}
	}
	return false
}

func normalizeAllowedDomain(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err == nil {
			return strings.TrimPrefix(parsed.Host, ".")
		}
	}
	return strings.TrimPrefix(strings.Split(value, "/")[0], ".")
}

func detectPackageLeakage(pkg *model.ClientExecutionPackage) string {
	data, err := json.Marshal(pkg)
	if err != nil {
		return ""
	}
	lower := strings.ToLower(string(data))
	switch {
	case strings.Contains(string(data), "BEGIN PRIVATE KEY"):
		return "private key"
	case strings.Contains(lower, "bearer "):
		return "authorization token"
	case strings.Contains(lower, "sk-"):
		return "api key"
	case strings.Contains(lower, ".env"):
		return ".env"
	case strings.Contains(lower, "password=") || strings.Contains(lower, "password:"):
		return "password literal"
	case packagePasswordLeakagePattern.Match(data):
		return "raw password literal"
	case strings.Contains(string(data), "function submitPayment"):
		return "source code body"
	default:
		return ""
	}
}

func envelopeForClientExecutionPackage(pkg model.ClientExecutionPackage, now time.Time) (model.ExchangeEnvelope, error) {
	digest, err := model.DigestCanonicalJSON(pkg)
	if err != nil {
		return model.ExchangeEnvelope{}, err
	}
	idSeed := fmt.Sprintf("%d_%s", now.UnixNano(), shortID(pkg.PackageID))
	return model.ExchangeEnvelope{
		EnvelopeID:           "env_desktop_" + idSeed,
		OrgID:                pkg.OrgID,
		ProjectID:            pkg.ProjectID,
		PackageKind:          model.ExchangePackageKindClientExecution,
		SchemaVersion:        model.ExchangeEnvelopeSchemaVersion,
		PayloadSchemaVersion: model.ClientExecutionPackageSchemaVersion,
		IdempotencyKey:       "idem_desktop_" + idSeed,
		CreatedAt:            now,
		ExpiresAt:            now.Add(30 * time.Minute),
		Producer: model.ExchangeProducer{
			AppVersion:     defaultDesktopAppVersion,
			InstallID:      defaultDesktopInstallID,
			RuntimeProfile: "desktop-product-run",
		},
		Crypto: model.ExchangeCrypto{
			CryptoSuite:            model.CryptoSuiteAES256GCM,
			ServerKeyID:            "cloud-dev/server-public-key",
			KeyWrappingMode:        model.KeyWrappingModeServerPublicKey,
			KeyEncryptionAlg:       "aes-256-gcm-dev-inline",
			ContentEncryptionAlg:   model.CryptoSuiteAES256GCM,
			CompressionAlg:         model.CompressionNone,
			PayloadDigestAlg:       "sha256",
			PayloadDigestSHA256:    digest,
			CiphertextDigestSHA256: digest,
			SignatureAlg:           "dev-static",
			SignatureKeyID:         defaultDesktopInstallID,
			Signature:              "desktop-dev-signature",
			Nonce:                  "nonce_desktop_" + idSeed,
			EncryptedContentKey:    "desktop-dev-inline-content-key",
		},
		PayloadRef: model.EncryptedPayloadRef{
			Kind:             model.PayloadRefKindInline,
			InlineCiphertext: "desktop-dev-inline-ciphertext",
			MimeType:         "application/json",
			SHA256:           digest,
			SizeBytes:        int64(len(digest)),
			Encrypted:        true,
			Sensitive:        true,
			CompressionAlg:   model.CompressionNone,
		},
		Policy: model.ExchangePackagePolicy{
			ReplayProtection:       true,
			MaxExecutionWindowSec:  900,
			DeletePayloadAfterRun:  true,
			HumanApprovalRequired:  true,
			StructureSummaryOnly:   true,
			RequiredIPAllowlistAck: false,
		},
	}, nil
}

func projectContextSummaryForPackage(project *model.ProjectContext, state *orchestrator.CascadeState, graphDigest string) model.ProjectContextSummary {
	inputFingerprints := map[string]string{}
	if state.ExecutableScriptBundle != nil {
		for key, value := range state.ExecutableScriptBundle.Reproducibility.InputFingerprints {
			inputFingerprints[key] = value
		}
	}
	if graphDigest != "" {
		inputFingerprints["workflow_graph"] = graphDigest
	}
	return model.ProjectContextSummary{
		ContextID:         "ctx_" + project.ID,
		SchemaVersion:     model.ProjectContextSchemaVersion,
		Mode:              project.Mode,
		Name:              project.Name,
		ProductURL:        project.ProductURL,
		TargetAudience:    project.TargetAudience,
		Goals:             append([]model.DemoGoal{}, project.Goals...),
		Audiences:         append([]model.AudienceProfile{}, project.Audiences...),
		AccessPolicy:      project.AccessPolicy,
		SecurityPolicy:    project.SecurityPolicy,
		InputFingerprints: inputFingerprints,
	}
}

func productMapSummaryForPackage(productMap *model.ProductMap) model.ProductMapSummary {
	if productMap == nil {
		return model.ProductMapSummary{}
	}
	components := make([]model.ComponentSummary, 0, len(productMap.Components))
	for _, component := range productMap.Components {
		if component == nil {
			continue
		}
		components = append(components, model.ComponentSummary{
			ID:            component.ID,
			Name:          component.Name,
			Kind:          component.Kind,
			SelectorCount: len(component.Selectors),
			ActionCount:   len(component.Actions),
			FeatureRefs:   append([]string{}, component.FeatureRefs...),
			EvidenceRefs:  append([]model.EvidenceRef{}, component.EvidenceRefs...),
		})
	}
	dataModels := make([]model.DataModelSummary, 0, len(productMap.DataModels))
	for _, dataModel := range productMap.DataModels {
		if dataModel == nil {
			continue
		}
		dataModels = append(dataModels, model.DataModelSummary{
			ID:           dataModel.ID,
			Name:         dataModel.Name,
			Kind:         dataModel.Kind,
			Fields:       append([]model.DataField{}, dataModel.Fields...),
			EvidenceRefs: append([]model.EvidenceRef{}, dataModel.EvidenceRefs...),
		})
	}
	return model.ProductMapSummary{
		ProductMapID: productMap.ID,
		Version:      productMap.Version,
		Summary:      productMap.Summary,
		Pages:        productMap.Pages,
		Features:     productMap.Features,
		Routes:       productMap.Routes,
		Components:   components,
		DataModels:   dataModels,
		Roles:        productMap.Roles,
		Workflows:    productMap.Workflows,
		EvidenceRefs: append([]model.EvidenceRef{}, productMap.EvidenceRefs...),
	}
}

func evidenceBundleForPackage(state *orchestrator.CascadeState) model.EvidenceBundle {
	refs := []model.EvidenceRef{}
	if state.UnderstandingReport != nil {
		refs = append(refs, state.UnderstandingReport.EvidenceRefs...)
	}
	if state.ProjectIntelligence != nil {
		refs = append(refs, state.ProjectIntelligence.EvidenceRefs...)
	}
	if state.ScriptDocument != nil {
		refs = append(refs, state.ScriptDocument.EvidenceRefs...)
	}
	return model.EvidenceBundle{EvidenceRefs: refs}
}

func scriptStepNodeIDsForPackage(doc *model.ExecutionScriptDocument) []string {
	if doc == nil {
		return nil
	}
	out := make([]string, 0, len(doc.Steps))
	for _, step := range doc.Steps {
		if step.NodeID != "" {
			out = append(out, step.NodeID)
		}
	}
	return out
}

func isAbsoluteHTTPURL(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func receivedAssetIDs(deliverables []model.ExecutionDeliverable) []string {
	out := []string{}
	for _, deliverable := range deliverables {
		if deliverable.Kind == "demo_video" && deliverable.ID != "" {
			out = append(out, deliverable.ID)
		}
	}
	return out
}

func cloudPostJSON[T any](ctx context.Context, client *http.Client, endpoint string, token string, orgID string, body any) (T, error) {
	var zero T
	payload, err := json.Marshal(body)
	if err != nil {
		return zero, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return zero, err
	}
	setExchangeAuthHeader(req, token)
	req.Header.Set("Content-Type", "application/json")
	if orgID != "" {
		req.Header.Set(cascadeOrgIDHeader, orgID)
	}
	return cloudDoJSON[T](client, req)
}

func cloudGetJSON[T any](ctx context.Context, client *http.Client, endpoint string, token string, orgID string) (T, error) {
	var zero T
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return zero, err
	}
	setExchangeAuthHeader(req, token)
	if orgID != "" {
		req.Header.Set(cascadeOrgIDHeader, orgID)
	}
	return cloudDoJSON[T](client, req)
}

func setExchangeAuthHeader(req *http.Request, token string) {
	token = strings.TrimSpace(token)
	if token == "" {
		return
	}
	if strings.HasPrefix(token, "cassess_") {
		req.Header.Set("Authorization", "Cascade-Session "+token)
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
}

func cloudDoJSON[T any](client *http.Client, req *http.Request) (T, error) {
	var zero T
	resp, err := client.Do(req)
	if err != nil {
		return zero, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return zero, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var exchangeErr exchangeHTTPError
		if err := json.Unmarshal(data, &exchangeErr); err == nil && exchangeErr.Error.Code != "" {
			return zero, &exchangeProtocolError{
				code:    exchangeErr.Error.Code,
				message: redactBridgeError(exchangeErr.Error.Message),
				details: exchangeErr.Error.Details,
			}
		}
		return zero, fmt.Errorf("%s %s returned %d: %s", req.Method, req.URL.String(), resp.StatusCode, redactBridgeError(string(data)))
	}
	if !json.Valid(data) {
		return zero, fmt.Errorf("%s %s returned non-JSON response: %s", req.Method, req.URL.String(), redactBridgeError(responseSnippet(data)))
	}
	var wrapped struct {
		OK        bool             `json:"ok"`
		Data      json.RawMessage  `json:"data,omitempty"`
		Error     string           `json:"error,omitempty"`
		ErrorInfo *BridgeErrorInfo `json:"error_info,omitempty"`
	}
	if err := json.Unmarshal(data, &wrapped); err == nil && (wrapped.OK || wrapped.Error != "" || len(wrapped.Data) > 0) {
		if !wrapped.OK {
			if wrapped.ErrorInfo != nil {
				return zero, &exchangeProtocolError{
					code:    wrapped.ErrorInfo.Code,
					message: redactBridgeError(wrapped.ErrorInfo.Message),
					details: wrapped.ErrorInfo.Details,
				}
			}
			return zero, errors.New(redactBridgeError(wrapped.Error))
		}
		if len(wrapped.Data) == 0 {
			return zero, errors.New("cloud response missing data")
		}
		var direct T
		if err := json.Unmarshal(wrapped.Data, &direct); err != nil {
			return zero, err
		}
		return direct, nil
	}

	var direct T
	if err := json.Unmarshal(data, &direct); err != nil {
		return zero, err
	}
	return direct, nil
}

func responseSnippet(data []byte) string {
	value := strings.Join(strings.Fields(string(data)), " ")
	if value == "" {
		return "(empty response)"
	}
	if len(value) > 240 {
		return value[:240] + "..."
	}
	return value
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func shortID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 16 {
		return strings.NewReplacer(":", "_", "/", "_", "\\", "_").Replace(value)
	}
	return strings.NewReplacer(":", "_", "/", "_", "\\", "_").Replace(value[:16])
}
