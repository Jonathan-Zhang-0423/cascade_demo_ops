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
	"strings"
	"time"

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

func (s *Service) RunCloudLifecycle(ctx context.Context, request CloudLifecycleRequest) (CloudLifecycleResult, error) {
	if strings.TrimSpace(s.runtime.CloudExchangeBaseURL) == "" {
		return CloudLifecycleResult{}, errors.New("CASCADE_CLOUD_EXCHANGE_BASE_URL is required for product run")
	}
	if strings.TrimSpace(s.runtime.CloudExchangeToken) == "" {
		return CloudLifecycleResult{}, errors.New("CASCADE_CLOUD_EXCHANGE_TOKEN is required for product run")
	}
	orgID := firstNonEmptyString(request.OrgID, defaultDesktopOrgID)
	projectID := strings.TrimSpace(request.ProjectID)
	var state *orchestrator.CascadeState
	var err error
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
	client := &http.Client{Timeout: 60 * time.Second}
	baseURL := strings.TrimRight(s.runtime.CloudExchangeBaseURL, "/")
	initResponse, err := cloudPostJSON[model.ExecutionPackageInitResponse](ctx, client, baseURL+"/v1/execution-packages/init", s.runtime.CloudExchangeToken, "", model.ExecutionPackageInitRequest{
		OrgID:       build.OrgID,
		ProjectID:   build.ProjectID,
		PackageKind: model.ExchangePackageKindClientExecution,
		Producer: model.ExchangeProducer{
			AppVersion:     defaultDesktopAppVersion,
			InstallID:      defaultDesktopInstallID,
			RuntimeProfile: "desktop-product-run",
		},
	})
	if err != nil {
		return CloudLifecycleResult{}, err
	}
	uploadResponse, err := cloudPostJSON[model.ExecutionPackageUploadResponse](ctx, client, baseURL+"/v1/execution-packages", s.runtime.CloudExchangeToken, "", map[string]any{
		"upload_id":   initResponse.UploadID,
		"envelope":    build.Envelope,
		"payload_ref": build.PayloadRef,
		"payload":     build.Package,
	})
	if err != nil {
		return CloudLifecycleResult{}, err
	}
	status, err := s.pollCloudStatus(ctx, client, baseURL, build.OrgID, uploadResponse.ExchangePackageID, request)
	if err != nil {
		return CloudLifecycleResult{}, err
	}
	var resultPackage *model.RecordingResultPackage
	if status.ResultPackageID != "" {
		result, err := cloudGetJSON[model.RecordingResultPackage](ctx, client, baseURL+"/v1/result-packages/"+url.PathEscape(status.ResultPackageID), s.runtime.CloudExchangeToken, build.OrgID)
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
		ackResponse, err := cloudPostJSON[model.ResultPackageAckResponse](ctx, client, baseURL+"/v1/result-packages/"+url.PathEscape(status.ResultPackageID)+"/ack", s.runtime.CloudExchangeToken, build.OrgID, model.ResultPackageAckRequest{
			ResultPackageID:   status.ResultPackageID,
			AckedByInstallID:  defaultDesktopInstallID,
			ReceivedAssetIDs:  received,
			VerifiedChecksums: true,
			AckedAt:           time.Now().UTC(),
		})
		if err != nil {
			return CloudLifecycleResult{}, err
		}
		ack = &ackResponse
		status, _ = cloudGetJSON[model.ExecutionPackageStatusResponse](ctx, client, baseURL+"/v1/execution-packages/"+url.PathEscape(uploadResponse.ExchangePackageID)+"/status", s.runtime.CloudExchangeToken, build.OrgID)
	}
	return CloudLifecycleResult{
		State:     state,
		Build:     &build,
		Init:      initResponse,
		Upload:    uploadResponse,
		Status:    status,
		Result:    resultPackage,
		Ack:       ack,
		CloudBase: baseURL,
	}, nil
}

func (s *Service) pollCloudStatus(ctx context.Context, client *http.Client, baseURL string, orgID string, exchangePackageID string, request CloudLifecycleRequest) (model.ExecutionPackageStatusResponse, error) {
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
		status, err := cloudGetJSON[model.ExecutionPackageStatusResponse](ctx, client, baseURL+"/v1/execution-packages/"+url.PathEscape(exchangePackageID)+"/status", s.runtime.CloudExchangeToken, orgID)
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

func buildClientExecutionPackageFromState(state *orchestrator.CascadeState, orgID string, now time.Time) (ClientExecutionPackageBuild, error) {
	if state == nil {
		return ClientExecutionPackageBuild{}, errors.New("cascade state is nil")
	}
	if state.ProjectContext == nil || state.WorkflowGraph == nil || state.ScriptDocument == nil || state.ExecutableScriptBundle == nil {
		return ClientExecutionPackageBuild{}, errors.New("project must have context, workflow graph, script document, and executable bundle")
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
			"producer":  "desktop_product_run",
			"auto_flow": true,
		},
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
	req.Header.Set("Authorization", "Bearer "+token)
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
	req.Header.Set("Authorization", "Bearer "+token)
	if orgID != "" {
		req.Header.Set(cascadeOrgIDHeader, orgID)
	}
	return cloudDoJSON[T](client, req)
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
		return zero, fmt.Errorf("%s %s returned %d: %s", req.Method, req.URL.String(), resp.StatusCode, redactBridgeError(string(data)))
	}
	if !json.Valid(data) {
		return zero, fmt.Errorf("%s %s returned non-JSON response: %s", req.Method, req.URL.String(), redactBridgeError(responseSnippet(data)))
	}
	var wrapped struct {
		OK    bool            `json:"ok"`
		Data  json.RawMessage `json:"data,omitempty"`
		Error string          `json:"error,omitempty"`
	}
	if err := json.Unmarshal(data, &wrapped); err == nil && (wrapped.OK || wrapped.Error != "" || len(wrapped.Data) > 0) {
		if !wrapped.OK {
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
