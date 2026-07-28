package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
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

// CloudPackagePreflightResult is a no-side-effect Server Intake report for
// the App-generated package. It must pass before upload or Browser execution.
type CloudPackagePreflightResult struct {
	Valid          bool     `json:"valid"`
	Runtime        string   `json:"runtime,omitempty"`
	PackageID      string   `json:"package_id,omitempty"`
	StageCount     int      `json:"stage_count,omitempty"`
	RequiredChecks int      `json:"required_checks,omitempty"`
	AllowedDomains []string `json:"allowed_domains,omitempty"`
	Message        string   `json:"message"`
}

type CloudStatusRequest struct {
	OrgID             string `json:"org_id,omitempty"`
	ExchangePackageID string `json:"exchange_package_id"`
}

// OpenCloudExecutionPackageEventStream keeps control-plane credentials in the
// Go process while the desktop UI consumes the resumable SSE stream locally.
func (s *Service) OpenCloudExecutionPackageEventStream(ctx context.Context, request CloudStatusRequest, lastEventID string) (*http.Response, error) {
	packageID := strings.TrimSpace(request.ExchangePackageID)
	if packageID == "" {
		return nil, errors.New("exchange_package_id is required")
	}
	lastEventID = strings.TrimSpace(lastEventID)
	if len(lastEventID) > 256 || (lastEventID != "" && !strings.HasPrefix(lastEventID, packageID+":")) {
		return nil, errors.New("last event id does not belong to the execution package")
	}
	session, err := s.EnsureExchangeSession(ctx, request.OrgID, "")
	if err != nil {
		return nil, err
	}
	endpoint := session.BaseURL + "/v1/execution-packages/" + url.PathEscape(packageID) + "/events?org_id=" + url.QueryEscape(firstNonEmptyString(request.OrgID, defaultDesktopOrgID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	setExchangeAuthHeader(req, session.SessionToken)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set(cascadeOrgIDHeader, firstNonEmptyString(request.OrgID, defaultDesktopOrgID))
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	response, err := (&http.Client{}).Do(req)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		return nil, fmt.Errorf("GET execution event stream returned %d: %s", response.StatusCode, redactBridgeError(string(body)))
	}
	return response, nil
}

type CloudEditorMaterializationRequest struct {
	OrgID           string `json:"org_id,omitempty"`
	ResultPackageID string `json:"result_package_id"`
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

type CloudResultReviewRequest struct {
	OrgID           string                    `json:"org_id,omitempty"`
	ResultPackageID string                    `json:"result_package_id"`
	Review          model.ResultReviewRequest `json:"review"`
}

type CloudResultRevisionRequest struct {
	OrgID           string                      `json:"org_id,omitempty"`
	ResultPackageID string                      `json:"result_package_id"`
	Revision        model.ResultRevisionRequest `json:"revision"`
}

type CloudDeliverableDownloadRequest struct {
	OrgID             string                     `json:"org_id,omitempty"`
	ResultPackageID   string                     `json:"result_package_id"`
	ExchangePackageID string                     `json:"exchange_package_id,omitempty"`
	Deliverable       model.ExecutionDeliverable `json:"deliverable"`
	OutputDirectory   string                     `json:"output_directory"`
}

type CloudDeliverableDownloadResult struct {
	ArtifactID       string `json:"artifact_id"`
	Kind             string `json:"kind,omitempty"`
	Role             string `json:"role,omitempty"`
	MimeType         string `json:"mime_type,omitempty"`
	DownloadURL      string `json:"download_url,omitempty"`
	LocalPath        string `json:"local_path"`
	SizeBytes        int64  `json:"size_bytes,omitempty"`
	SHA256           string `json:"sha256"`
	ExpectedSHA256   string `json:"expected_sha256,omitempty"`
	ChecksumVerified bool   `json:"checksum_verified"`
	Resumed          bool   `json:"resumed,omitempty"`
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

type ProductRunPrepareResult struct {
	State *orchestrator.CascadeState   `json:"state"`
	Build *ClientExecutionPackageBuild `json:"build,omitempty"`
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
	session, err := s.EnsureExchangeSession(ctx, build.OrgID, build.ProjectID)
	if err != nil {
		return ClientExecutionPackageBuild{}, ExchangeSession{}, err
	}
	build, err = s.signBuildWithExchangeSession(build, session)
	if err != nil {
		return ClientExecutionPackageBuild{}, ExchangeSession{}, err
	}
	return build, session, nil
}

func (s *Service) PrepareProductRun(ctx context.Context, request CloudLifecycleRequest) (ProductRunPrepareResult, error) {
	orgID := firstNonEmptyString(request.OrgID, defaultDesktopOrgID)
	projectID := strings.TrimSpace(request.ProjectID)
	var (
		state *orchestrator.CascadeState
		err   error
	)
	if request.UserInput != nil {
		state, err = s.GenerateExecutionPackage(ctx, *request.UserInput)
		if err != nil {
			return ProductRunPrepareResult{}, err
		}
		projectID = state.ProjectID
	} else if projectID != "" {
		state, err = s.states.Load(ctx, projectID)
		if err != nil {
			return ProductRunPrepareResult{}, err
		}
	} else {
		return ProductRunPrepareResult{}, errors.New("user_input or project_id is required")
	}
	build, err := buildClientExecutionPackageFromState(state, orgID, time.Now().UTC())
	if err != nil {
		return ProductRunPrepareResult{}, err
	}
	return ProductRunPrepareResult{State: compactStateForPrepareResponse(state, &build), Build: &build}, nil
}

func compactStateForPrepareResponse(state *orchestrator.CascadeState, build *ClientExecutionPackageBuild) *orchestrator.CascadeState {
	if state == nil {
		return nil
	}
	out := &orchestrator.CascadeState{
		ProjectID:               state.ProjectID,
		CurrentNode:             state.CurrentNode,
		Status:                  state.Status,
		ProjectContext:          compactProjectContextForPrepareResponse(state.ProjectContext),
		RequirementBrief:        state.RequirementBrief,
		ScriptReadinessReport:   state.ScriptReadinessReport,
		VerifiedInteractionPlan: compactVerifiedInteractionPlanForPrepareResponse(state.VerifiedInteractionPlan),
		MissingEvidenceReport:   compactMissingEvidenceReportForPrepareResponse(state.MissingEvidenceReport),
		Approved:                state.Approved,
		RehearsePassRate:        state.RehearsePassRate,
		Artifacts:               state.Artifacts,
		ErrorMessage:            state.ErrorMessage,
	}
	if build != nil {
		out.WorkflowGraph = build.Package.WorkflowGraph
		if build.Package.ExecutableScriptBundle != nil {
			out.ExecutableScriptBundle = build.Package.ExecutableScriptBundle
			out.ScriptDocument = build.Package.ExecutableScriptBundle.PlanJSON
			out.ScriptMarkdown = build.Package.ExecutableScriptBundle.ApprovalMarkdown.InlineMarkdown
			out.ScriptMarkdownArtifact = build.Package.ExecutableScriptBundle.ApprovalMarkdown.Artifact
			out.ApprovalMarkdownArtifact = build.Package.ExecutableScriptBundle.ApprovalMarkdown.Artifact
		}
	}
	if state.ProjectIntelligence != nil {
		out.ProjectIntelligence = &model.ProjectIntelligencePack{
			ID:                    state.ProjectIntelligence.ID,
			ProjectID:             state.ProjectIntelligence.ProjectID,
			SchemaVersion:         state.ProjectIntelligence.SchemaVersion,
			DemoIntent:            compactDemoIntentForPrepareResponse(state.ProjectIntelligence.DemoIntent),
			RunIntentScope:        state.ProjectIntelligence.RunIntentScope,
			BusinessStagePlan:     compactBusinessStagePlanForPrepareResponse(state.ProjectIntelligence.BusinessStagePlan),
			VerifiedInteraction:   out.VerifiedInteractionPlan,
			MissingEvidenceReport: out.MissingEvidenceReport,
			ScriptReadinessReport: out.ScriptReadinessReport,
			SourceDigestSHA256:    state.ProjectIntelligence.SourceDigestSHA256,
			EvidenceRefs:          compactEvidenceRefsForUpload(state.ProjectIntelligence.EvidenceRefs, 8),
			Confidence:            state.ProjectIntelligence.Confidence,
			CreatedAt:             state.ProjectIntelligence.CreatedAt,
		}
	}
	return out
}

func compactProjectContextForPrepareResponse(project *model.ProjectContext) *model.ProjectContext {
	if project == nil {
		return nil
	}
	out := &model.ProjectContext{
		ID:                 project.ID,
		SchemaVersion:      project.SchemaVersion,
		Mode:               project.Mode,
		Name:               project.Name,
		ProductURL:         project.ProductURL,
		DemoAccount:        project.DemoAccount,
		GitRepoURL:         project.GitRepoURL,
		LocalRepoPath:      project.LocalRepoPath,
		ProductDescription: truncateForUpload(project.ProductDescription, 1000),
		TargetAudience:     project.TargetAudience,
		BrandTone:          project.BrandTone,
		MustShow:           limitStringsForUpload(project.MustShow, 8),
		MustNotShow:        limitStringsForUpload(project.MustNotShow, 8),
		ForbiddenPages:     limitStringsForUpload(project.ForbiddenPages, 16),
		ForbiddenData:      limitStringsForUpload(project.ForbiddenData, 16),
		Goals:              project.Goals,
		AccessPolicy:       project.AccessPolicy,
		SecurityPolicy:     project.SecurityPolicy,
		CreatedAt:          project.CreatedAt,
		UpdatedAt:          project.UpdatedAt,
	}
	if project.Inputs != nil {
		out.Inputs = &model.ProjectInputBundle{
			ProductURLs:          project.Inputs.ProductURLs,
			Repositories:         project.Inputs.Repositories,
			Credentials:          project.Inputs.Credentials,
			Requirements:         project.Inputs.Requirements,
			RawUserPrompt:        truncateForUpload(project.Inputs.RawUserPrompt, 1000),
			RequirementDocuments: compactRequirementDocumentsForPrepareResponse(project.Inputs.RequirementDocuments),
		}
	}
	return out
}

func compactRequirementDocumentsForPrepareResponse(docs []model.RequirementDocumentInput) []model.RequirementDocumentInput {
	if len(docs) == 0 {
		return nil
	}
	if len(docs) > 3 {
		docs = docs[:3]
	}
	out := make([]model.RequirementDocumentInput, len(docs))
	for i, doc := range docs {
		doc.Body = truncateForUpload(doc.Body, 1000)
		doc.EvidenceRefs = compactEvidenceRefsForUpload(doc.EvidenceRefs, 2)
		doc.Metadata = nil
		out[i] = doc
	}
	return out
}

func compactDemoIntentForPrepareResponse(intent *model.DemoIntentSpec) *model.DemoIntentSpec {
	if intent == nil {
		return nil
	}
	out := *intent
	out.Objective = truncateForUpload(out.Objective, 1000)
	out.ForbiddenTopics = limitStringsForUpload(out.ForbiddenTopics, 12)
	out.EvidenceRefs = compactEvidenceRefsForUpload(out.EvidenceRefs, 4)
	if len(out.Goals) > 8 {
		out.Goals = out.Goals[:8]
	}
	for i := range out.Goals {
		out.Goals[i].TargetKeywords = limitStringsForUpload(out.Goals[i].TargetKeywords, 8)
		out.Goals[i].EvidenceRefs = compactEvidenceRefsForUpload(out.Goals[i].EvidenceRefs, 2)
	}
	return &out
}

func compactBusinessStagePlanForPrepareResponse(plan *model.BusinessStagePlan) *model.BusinessStagePlan {
	if plan == nil {
		return nil
	}
	out := *plan
	out.EvidenceRefs = compactEvidenceRefsForUpload(out.EvidenceRefs, 6)
	out.BlockingUncertainties = compactStageUncertaintiesForPrepareResponse(out.BlockingUncertainties, 8)
	if len(out.Stages) > 12 {
		out.Stages = out.Stages[:12]
	}
	for i := range out.Stages {
		stage := &out.Stages[i]
		stage.UserIntent = truncateForUpload(stage.UserIntent, 1000)
		stage.Objective = truncateForUpload(stage.Objective, 240)
		stage.Action.InputValue = RedactSensitiveForPrepareResponse(stage.Action.InputValue)
		stage.Action.WaitConditions = limitStringsForUpload(stage.Action.WaitConditions, 4)
		stage.Action.CapturePoints = limitStringsForUpload(stage.Action.CapturePoints, 4)
		stage.EvidenceRefs = compactEvidenceRefsForUpload(stage.EvidenceRefs, 4)
		stage.Uncertainties = compactStageUncertaintiesForPrepareResponse(stage.Uncertainties, 4)
		if len(stage.Targets) > 4 {
			stage.Targets = stage.Targets[:4]
		}
		for j := range stage.Targets {
			target := &stage.Targets[j]
			target.EvidenceRefs = compactEvidenceRefsForUpload(target.EvidenceRefs, 2)
			target.Alternatives = compactSelectorCandidatesForUpload(target.Alternatives, 2)
		}
		if len(stage.EvidenceRequirements) > 4 {
			stage.EvidenceRequirements = stage.EvidenceRequirements[:4]
		}
		for j := range stage.EvidenceRequirements {
			stage.EvidenceRequirements[j].EvidenceRefs = compactEvidenceRefsForUpload(stage.EvidenceRequirements[j].EvidenceRefs, 2)
		}
	}
	return &out
}

func compactStageUncertaintiesForPrepareResponse(values []model.StageUncertainty, limit int) []model.StageUncertainty {
	if len(values) == 0 || limit <= 0 {
		return nil
	}
	if len(values) > limit {
		values = values[:limit]
	}
	out := make([]model.StageUncertainty, len(values))
	for i, value := range values {
		value.Summary = truncateForUpload(value.Summary, 240)
		value.SuggestedAction = truncateForUpload(value.SuggestedAction, 240)
		value.EvidenceRefs = compactEvidenceRefsForUpload(value.EvidenceRefs, 2)
		out[i] = value
	}
	return out
}

func compactVerifiedInteractionPlanForPrepareResponse(plan *model.VerifiedInteractionPlan) *model.VerifiedInteractionPlan {
	if plan == nil {
		return nil
	}
	out := *plan
	out.EvidenceRefs = compactEvidenceRefsForUpload(out.EvidenceRefs, 6)
	if len(out.Actions) > 12 {
		out.Actions = out.Actions[:12]
	}
	for i := range out.Actions {
		action := &out.Actions[i]
		action.ExpectedOutcome = truncateForUpload(action.ExpectedOutcome, 180)
		action.SuccessState = truncateForUpload(action.SuccessState, 180)
		action.InputValue = RedactSensitiveForPrepareResponse(action.InputValue)
		action.WaitConditions = limitStringsForUpload(action.WaitConditions, 4)
		action.EvidenceRefs = compactEvidenceRefsForUpload(action.EvidenceRefs, 2)
		action.Alternatives = compactSelectorCandidatesForUpload(action.Alternatives, 2)
	}
	return &out
}

func compactMissingEvidenceReportForPrepareResponse(report *model.MissingEvidenceReport) *model.MissingEvidenceReport {
	if report == nil {
		return nil
	}
	out := *report
	out.Summary = truncateForUpload(out.Summary, 500)
	out.EvidenceRefs = compactEvidenceRefsForUpload(out.EvidenceRefs, 4)
	if len(out.Items) > 6 {
		out.Items = out.Items[:6]
	}
	for i := range out.Items {
		item := &out.Items[i]
		item.Message = truncateForUpload(item.Message, 240)
		item.SuggestedAction = truncateForUpload(item.SuggestedAction, 240)
		item.EvidenceRefs = compactEvidenceRefsForUpload(item.EvidenceRefs, 2)
	}
	return &out
}

func RedactSensitiveForPrepareResponse(value string) string {
	if value == "" {
		return ""
	}
	return agents.RedactSensitiveUserText(truncateForUpload(value, 120))
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
	session, err := s.EnsureExchangeSession(ctx, build.OrgID, build.ProjectID)
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
	session, err := s.EnsureExchangeSession(ctx, build.OrgID, build.ProjectID)
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

// PreflightCloudExecutionPackage validates the exact signed payload with the
// Server Intake contract but never uploads, persists, or runs it.
func (s *Service) PreflightCloudExecutionPackage(ctx context.Context, build ClientExecutionPackageBuild) (CloudPackagePreflightResult, error) {
	if err := normalizeClientExecutionPackageForUpload(&build.Package); err != nil {
		return CloudPackagePreflightResult{}, err
	}
	if build.Envelope.EnvelopeID == "" {
		return CloudPackagePreflightResult{}, errors.New("exchange envelope is required for preflight")
	}
	if err := s.exchange.ValidateUpload(ctx, model.ExecutionPackageUploadRequest{
		Envelope: build.Envelope, PayloadRef: build.PayloadRef,
	}, build.Package); err != nil {
		return CloudPackagePreflightResult{}, err
	}
	result := CloudPackagePreflightResult{
		Valid: true, PackageID: build.Package.PackageID, AllowedDomains: append([]string{}, build.Package.RecordingRunSpec.AllowedDomains...),
		Message: "Server Intake 校验通过：尚未上传、尚未启动浏览器、尚未读取任何客户页面。",
	}
	if bundle := build.Package.ExecutableScriptBundle; bundle != nil {
		result.Runtime = bundle.ScriptManifest.Runtime
		if bundle.PlanJSON != nil {
			result.StageCount = len(bundle.PlanJSON.Steps)
			for _, step := range bundle.PlanJSON.Steps {
				for _, validation := range step.Validations {
					if validation.Required {
						result.RequiredChecks++
					}
				}
			}
		}
	}
	return result, nil
}

func (s *Service) GetCloudExecutionPackageStatus(ctx context.Context, request CloudStatusRequest) (model.ExecutionPackageStatusResponse, error) {
	if strings.TrimSpace(request.ExchangePackageID) == "" {
		return model.ExecutionPackageStatusResponse{}, errors.New("exchange_package_id is required")
	}
	session, err := s.EnsureExchangeSession(ctx, request.OrgID, "")
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
	session, err := s.EnsureExchangeSession(ctx, request.OrgID, "")
	if err != nil {
		return model.RecordingResultPackage{}, err
	}
	client := &http.Client{Timeout: 60 * time.Second}
	return cloudGetJSON[model.RecordingResultPackage](ctx, client, session.BaseURL+"/v1/result-packages/"+url.PathEscape(request.ResultPackageID), session.SessionToken, firstNonEmptyString(request.OrgID, defaultDesktopOrgID))
}

func (s *Service) DownloadCloudResultDeliverable(ctx context.Context, request CloudDeliverableDownloadRequest) (CloudDeliverableDownloadResult, error) {
	resultPackageID := strings.TrimSpace(request.ResultPackageID)
	if resultPackageID == "" {
		return CloudDeliverableDownloadResult{}, errors.New("result_package_id is required")
	}
	deliverable := request.Deliverable
	if strings.TrimSpace(deliverable.ID) == "" {
		return CloudDeliverableDownloadResult{}, errors.New("deliverable id is required")
	}
	outputDir := strings.TrimSpace(request.OutputDirectory)
	if outputDir == "" {
		return CloudDeliverableDownloadResult{}, errors.New("output_directory is required")
	}
	session, err := s.EnsureExchangeSession(ctx, request.OrgID, "")
	if err != nil {
		return CloudDeliverableDownloadResult{}, err
	}
	endpoint, err := resolveCloudDeliverableDownloadURL(session.BaseURL, resultPackageID, deliverable)
	if err != nil {
		return CloudDeliverableDownloadResult{}, err
	}
	orgID := firstNonEmptyString(request.OrgID, defaultDesktopOrgID)
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return CloudDeliverableDownloadResult{}, err
	}
	localPath := filepath.Join(outputDir, cloudDeliverableFileName(deliverable))
	expected := strings.ToLower(strings.TrimSpace(deliverable.SHA256))
	if existing, ok := verifiedDownload(localPath, expected, deliverable.SizeBytes); ok {
		return cloudDownloadResult(deliverable, endpoint, localPath, existing, expected, firstNonEmptyString(deliverable.MimeType, ""), false), nil
	}
	partialPath := localPath + ".part"
	client := &http.Client{Timeout: 5 * time.Minute}
	size, sum, mimeType, resumed, err := downloadCloudDeliverableToPartial(ctx, client, endpoint, session.SessionToken, orgID, partialPath)
	if err != nil {
		return CloudDeliverableDownloadResult{}, err
	}
	verified := expected != "" && strings.EqualFold(sum, expected)
	if !verified {
		return cloudDownloadResult(deliverable, endpoint, partialPath, size, expected, mimeType, resumed), nil
	}
	if err := os.Rename(partialPath, localPath); err != nil {
		return CloudDeliverableDownloadResult{}, fmt.Errorf("finalize verified deliverable: %w", err)
	}
	return cloudDownloadResult(deliverable, endpoint, localPath, size, expected, mimeType, resumed), nil
}

func downloadCloudDeliverableToPartial(ctx context.Context, client *http.Client, endpoint, token, orgID, partialPath string) (int64, string, string, bool, error) {
	offset, hasher, err := partialDownloadState(partialPath)
	if err != nil {
		return 0, "", "", false, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, "", "", false, err
	}
	setExchangeAuthHeader(request, token)
	if orgID != "" {
		request.Header.Set(cascadeOrgIDHeader, orgID)
	}
	if offset > 0 {
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, "", "", false, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusPartialContent {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return 0, "", "", false, fmt.Errorf("GET %s returned %d: %s", endpoint, response.StatusCode, redactBridgeError(string(data)))
	}
	resumed := offset > 0 && response.StatusCode == http.StatusPartialContent
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if !resumed {
		offset = 0
		hasher = sha256.New()
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}
	file, err := os.OpenFile(partialPath, flags, 0o600)
	if err != nil {
		return 0, "", "", false, err
	}
	written, copyErr := io.Copy(io.MultiWriter(file, hasher), response.Body)
	closeErr := file.Close()
	if copyErr != nil {
		return 0, "", "", resumed, copyErr
	}
	if closeErr != nil {
		return 0, "", "", resumed, closeErr
	}
	return offset + written, hex.EncodeToString(hasher.Sum(nil)), response.Header.Get("Content-Type"), resumed, nil
}

func partialDownloadState(path string) (int64, hash.Hash, error) {
	hasher := sha256.New()
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, hasher, nil
	}
	if err != nil {
		return 0, nil, err
	}
	size, copyErr := io.Copy(hasher, file)
	closeErr := file.Close()
	if copyErr != nil {
		return 0, nil, copyErr
	}
	if closeErr != nil {
		return 0, nil, closeErr
	}
	return size, hasher, nil
}

func verifiedDownload(path, expected string, expectedSize int64) (int64, bool) {
	if expected == "" {
		return 0, false
	}
	file, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	hasher := sha256.New()
	size, copyErr := io.Copy(hasher, file)
	_ = file.Close()
	if copyErr != nil || expectedSize > 0 && size != expectedSize {
		return 0, false
	}
	return size, strings.EqualFold(hex.EncodeToString(hasher.Sum(nil)), expected)
}

func cloudDownloadResult(deliverable model.ExecutionDeliverable, endpoint, localPath string, size int64, expected, mimeType string, resumed bool) CloudDeliverableDownloadResult {
	sum := ""
	if file, err := os.Open(localPath); err == nil {
		hasher := sha256.New()
		if _, hashErr := io.Copy(hasher, file); hashErr == nil {
			sum = hex.EncodeToString(hasher.Sum(nil))
		}
		_ = file.Close()
	}
	return CloudDeliverableDownloadResult{
		ArtifactID:       deliverable.ID,
		Kind:             deliverable.Kind,
		Role:             deliverable.Role,
		MimeType:         firstNonEmptyString(deliverable.MimeType, mimeType),
		DownloadURL:      endpoint,
		LocalPath:        localPath,
		SizeBytes:        size,
		SHA256:           sum,
		ExpectedSHA256:   expected,
		ChecksumVerified: expected != "" && strings.EqualFold(sum, expected),
		Resumed:          resumed,
	}
}

func (s *Service) GetCloudEditorMaterialization(ctx context.Context, request CloudEditorMaterializationRequest) (EditorSessionMaterialization, error) {
	if strings.TrimSpace(request.ResultPackageID) == "" {
		return EditorSessionMaterialization{}, errors.New("result_package_id is required")
	}
	result, err := s.GetCloudResultPackage(ctx, CloudResultRequest{OrgID: request.OrgID, ResultPackageID: request.ResultPackageID})
	if err != nil {
		return EditorSessionMaterialization{}, err
	}
	return s.GetEditorSessionMaterialization(ctx, result)
}

func (s *Service) AckCloudResultPackage(ctx context.Context, request CloudAckRequest) (model.ResultPackageAckResponse, error) {
	if strings.TrimSpace(request.ResultPackageID) == "" {
		return model.ResultPackageAckResponse{}, errors.New("result_package_id is required")
	}
	session, err := s.EnsureExchangeSession(ctx, request.OrgID, "")
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

func (s *Service) ReviewCloudResultPackage(ctx context.Context, request CloudResultReviewRequest) (model.ResultReviewRecord, error) {
	if strings.TrimSpace(request.ResultPackageID) == "" {
		return model.ResultReviewRecord{}, errors.New("result_package_id is required")
	}
	session, err := s.EnsureExchangeSession(ctx, request.OrgID, "")
	if err != nil {
		return model.ResultReviewRecord{}, err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	return cloudPostJSON[model.ResultReviewRecord](ctx, client, session.BaseURL+"/v1/result-packages/"+url.PathEscape(request.ResultPackageID)+"/reviews", session.SessionToken, firstNonEmptyString(request.OrgID, defaultDesktopOrgID), request.Review)
}

func (s *Service) RequestCloudResultRevision(ctx context.Context, request CloudResultRevisionRequest) (model.ResultRevisionRecord, error) {
	if strings.TrimSpace(request.ResultPackageID) == "" {
		return model.ResultRevisionRecord{}, errors.New("result_package_id is required")
	}
	session, err := s.EnsureExchangeSession(ctx, request.OrgID, "")
	if err != nil {
		return model.ResultRevisionRecord{}, err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	return cloudPostJSON[model.ResultRevisionRecord](ctx, client, session.BaseURL+"/v1/result-packages/"+url.PathEscape(request.ResultPackageID)+"/revisions", session.SessionToken, firstNonEmptyString(request.OrgID, defaultDesktopOrgID), request.Revision)
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
	session, err := s.EnsureExchangeSession(ctx, build.OrgID, build.ProjectID)
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
	return CloudLifecycleResult{
		State:     state,
		Build:     &build,
		Init:      initResponse,
		Upload:    uploadResponse,
		Status:    status,
		Result:    resultPackage,
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
		return "", "", errors.New("DemoOps execution server is not configured; set CASCADE_CLOUD_EXCHANGE_BASE_URL")
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
	graphForPackage, err := cloneWorkflowGraphForPackage(graph)
	if err != nil {
		return ClientExecutionPackageBuild{}, err
	}
	bundleForPackage, err := cloneExecutableBundleForPackage(state.ExecutableScriptBundle)
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
		ProductMapSummary:      productMapSummaryForPackage(project, state.ProductMap),
		WorkflowGraph:          graphForPackage,
		RecordingRunSpec:       runSpec,
		ExecutableScriptBundle: bundleForPackage,
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
	applyBrowserAgentOutlineUploadView(&pkg)
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
	if bundle.StageApprovalPlan != nil {
		stagePlanHash, err := model.DigestCanonicalJSON(bundle.StageApprovalPlan)
		if err != nil {
			return err
		}
		bundle.Reproducibility.StagePlanHashSHA256 = stagePlanHash
	}
	if bundle.ScriptOutline != nil {
		outlineHash, err := model.DigestCanonicalJSON(bundle.ScriptOutline)
		if err != nil {
			return err
		}
		bundle.Reproducibility.OutlineHashSHA256 = outlineHash
	}
	if bundle.AgentPromptPolicy != nil {
		promptHash, err := model.DigestCanonicalJSON(bundle.AgentPromptPolicy)
		if err != nil {
			return err
		}
		bundle.Reproducibility.PromptPolicyHashSHA256 = promptHash
	}
	if bundle.BrowserAgentContract != nil {
		contractHash, err := model.DigestCanonicalJSON(bundle.BrowserAgentContract)
		if err != nil {
			return err
		}
		bundle.Reproducibility.BrowserAgentContractHashSHA256 = contractHash
	}
	if bundle.UnderstandingDossier != nil {
		dossierHash, err := model.DigestCanonicalJSON(bundle.UnderstandingDossier)
		if err != nil {
			return err
		}
		bundle.Reproducibility.UnderstandingDossierHashSHA256 = dossierHash
		if bundle.UnderstandingDossierRef != nil {
			bundle.UnderstandingDossierRef.SHA256 = dossierHash
		}
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

func cloneWorkflowGraphForPackage(graph *model.DemoWorkflowGraph) (*model.DemoWorkflowGraph, error) {
	if graph == nil {
		return nil, nil
	}
	data, err := json.Marshal(graph)
	if err != nil {
		return nil, err
	}
	var out model.DemoWorkflowGraph
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func cloneExecutableBundleForPackage(bundle *model.ExecutableRecordingScriptBundle) (*model.ExecutableRecordingScriptBundle, error) {
	if bundle == nil {
		return nil, nil
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, err
	}
	var out model.ExecutableRecordingScriptBundle
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func applyBrowserAgentOutlineUploadView(pkg *model.ClientExecutionPackage) {
	if pkg == nil || pkg.ExecutableScriptBundle == nil ||
		pkg.ExecutableScriptBundle.ScriptManifest.Runtime != model.ExecutableScriptRuntimeBrowserAgentOutlineV1 {
		return
	}
	bundle := pkg.ExecutableScriptBundle
	bundle.PlaywrightScript = model.ExecutableScriptSource{MimeType: "application/x.browser-agent-outline+json"}
	if bundle.PlanJSON != nil {
		bundle.PlanJSON.WorkflowGraph = nil
		bundle.PlanJSON.EvidenceRefs = compactEvidenceRefsForUpload(bundle.PlanJSON.EvidenceRefs, 8)
		for i := range bundle.PlanJSON.Steps {
			compactScriptStepForUpload(&bundle.PlanJSON.Steps[i])
		}
	}
	compactStageApprovalPlanForUpload(bundle.StageApprovalPlan)
	compactBrowserAgentOutlineForUpload(bundle.ScriptOutline)
	compactBrowserAgentContractForUpload(bundle.BrowserAgentContract)
	bundle.UnderstandingDossier = nil
	pkg.WorkflowGraph = minimalWorkflowGraphForUpload(pkg.WorkflowGraph, bundle)
	pkg.ProductMapSummary = minimalProductMapSummaryForUpload(pkg.ProductMapSummary, bundle)
	pkg.ProductMapSummary.EvidenceRefs = compactEvidenceRefsForUpload(pkg.ProductMapSummary.EvidenceRefs, 4)
	pkg.ProjectContextSummary.Audiences = nil
	pkg.ProjectContextSummary.BrandKit = nil
	pkg.ProjectContextSummary.KnowledgeRefs = nil
	pkg.EvidenceBundle = model.EvidenceBundle{EvidenceRefs: compactEvidenceRefsForUpload(evidenceRefsForUpload(pkg), 16)}
	if pkg.Metadata == nil {
		pkg.Metadata = map[string]any{}
	}
	pkg.Metadata["upload_view"] = "browser_agent_outline_minimal"
	pkg.Metadata["full_project_understanding"] = "local_only"
}

func minimalWorkflowGraphForUpload(graph *model.DemoWorkflowGraph, bundle *model.ExecutableRecordingScriptBundle) *model.DemoWorkflowGraph {
	if graph == nil {
		return nil
	}
	out := &model.DemoWorkflowGraph{
		ID:            graph.ID,
		ProjectID:     graph.ProjectID,
		SchemaVersion: graph.SchemaVersion,
		Version:       graph.Version,
		Status:        graph.Status,
		Name:          graph.Name,
		Summary:       truncateForUpload(graph.Summary, 240),
		EntryPoint:    graph.EntryPoint,
		Nodes:         []*model.GraphNode{},
		Edges:         []*model.GraphEdge{},
		EvidenceRefs:  compactEvidenceRefsForUpload(graph.EvidenceRefs, 4),
		CreatedAt:     graph.CreatedAt,
		UpdatedAt:     graph.UpdatedAt,
	}
	if bundle == nil || bundle.PlanJSON == nil {
		return out
	}
	var previous string
	for _, step := range bundle.PlanJSON.Steps {
		nodeID := firstNonEmptyString(step.NodeID, step.ID)
		out.Nodes = append(out.Nodes, &model.GraphNode{
			ID:              nodeID,
			Action:          string(step.Action.Type),
			ExpectedOutcome: truncateForUpload(step.ExpectedOutcome, 200),
			Type:            model.GraphNodeTypeAction,
			Title:           truncateForUpload(firstNonEmptyString(step.Title, step.NodeID), 120),
			Goal:            truncateForUpload(step.BusinessValue, 200),
			PageRef:         firstNonEmptyString(step.PageTarget.URL, step.Action.Target.URL),
			EvidenceRefs:    compactEvidenceRefsForUpload(step.EvidenceRefs, 3),
			DurationHintMS:  step.Timing.DurationMS,
		})
		if previous != "" && nodeID != "" {
			out.Edges = append(out.Edges, &model.GraphEdge{
				ID:       "edge_" + shortID(previous) + "_" + shortID(nodeID),
				FromNode: previous,
				ToNode:   nodeID,
				Priority: len(out.Edges) + 1,
			})
		}
		previous = nodeID
	}
	return out
}

func minimalProductMapSummaryForUpload(summary model.ProductMapSummary, bundle *model.ExecutableRecordingScriptBundle) model.ProductMapSummary {
	routeRefs := map[string]bool{}
	componentRefs := map[string]bool{}
	if bundle != nil && bundle.StageApprovalPlan != nil {
		for _, stage := range bundle.StageApprovalPlan.Stages {
			addUploadRef(routeRefs, stage.TargetRoute)
			addUploadRef(routeRefs, stage.TargetURL)
			for _, ref := range stage.ComponentRefs {
				addUploadRef(componentRefs, ref)
			}
		}
	}
	out := model.ProductMapSummary{
		ProductMapID: summary.ProductMapID,
		Version:      summary.Version,
		Summary:      summary.Summary,
		EvidenceRefs: limitEvidenceRefs(summary.EvidenceRefs, 16),
	}
	for _, route := range summary.Routes {
		if route == nil || len(out.Routes) >= 16 {
			break
		}
		if uploadRefMatches(routeRefs, route.Path) || uploadRefMatches(routeRefs, route.ID) || uploadComponentOverlap(componentRefs, route.ComponentIDs) {
			copied := *route
			copied.EvidenceRefs = limitEvidenceRefs(copied.EvidenceRefs, 4)
			out.Routes = append(out.Routes, &copied)
		}
	}
	for _, component := range summary.Components {
		if len(out.Components) >= 16 {
			break
		}
		if len(componentRefs) == 0 || componentRefs[component.ID] {
			component.EvidenceRefs = limitEvidenceRefs(component.EvidenceRefs, 4)
			out.Components = append(out.Components, component)
		}
	}
	return out
}

func evidenceRefsForUpload(pkg *model.ClientExecutionPackage) []model.EvidenceRef {
	refs := append([]model.EvidenceRef{}, pkg.EvidenceBundle.EvidenceRefs...)
	bundle := pkg.ExecutableScriptBundle
	if bundle == nil {
		return refs
	}
	if bundle.StageApprovalPlan != nil {
		refs = append(refs, bundle.StageApprovalPlan.EvidenceRefs...)
		for _, stage := range bundle.StageApprovalPlan.Stages {
			refs = append(refs, stage.EvidenceRefs...)
		}
	}
	if bundle.ScriptOutline != nil {
		refs = append(refs, bundle.ScriptOutline.EvidenceRefs...)
		for _, stage := range bundle.ScriptOutline.Stages {
			refs = append(refs, stage.EvidenceRefs...)
		}
	}
	return refs
}

func limitEvidenceRefs(refs []model.EvidenceRef, limit int) []model.EvidenceRef {
	if limit <= 0 || len(refs) == 0 {
		return nil
	}
	out := []model.EvidenceRef{}
	seen := map[string]bool{}
	for _, ref := range refs {
		key := ref.ID + "|" + string(ref.Kind) + "|" + ref.FieldPath + "|" + ref.ArtifactID
		if key == "|||" {
			key = ref.Summary
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, ref)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func compactScriptStepForUpload(step *model.ScriptStep) {
	if step == nil {
		return
	}
	step.BusinessValue = truncateForUpload(step.BusinessValue, 240)
	step.ExpectedOutcome = truncateForUpload(step.ExpectedOutcome, 240)
	step.EvidenceRefs = compactEvidenceRefsForUpload(step.EvidenceRefs, 3)
	step.PageTarget.SelectorAlternatives = compactSelectorCandidatesForUpload(step.PageTarget.SelectorAlternatives, 2)
	step.Action.Target.SelectorAlternatives = compactSelectorCandidatesForUpload(step.Action.Target.SelectorAlternatives, 2)
	step.Action.Target.EvidenceRefs = compactEvidenceRefsForUpload(step.Action.Target.EvidenceRefs, 2)
	if step.TargetContract != nil {
		compactTargetContractForUpload(step.TargetContract)
	}
	for i := range step.Validations {
		compactValidationForUpload(&step.Validations[i])
	}
	if len(step.Validations) > 3 {
		step.Validations = step.Validations[:3]
	}
	step.Capture.MaskSelectors = limitStringsForUpload(step.Capture.MaskSelectors, 3)
	step.Capture.Redactions = limitRedactionsForUpload(step.Capture.Redactions, 3)
	step.Narrative.Voiceover = truncateForUpload(step.Narrative.Voiceover, 240)
	step.Narrative.Caption = truncateForUpload(step.Narrative.Caption, 160)
	step.Narrative.Callout = truncateForUpload(step.Narrative.Callout, 120)
}

func compactStageApprovalPlanForUpload(plan *model.StageApprovalPlan) {
	if plan == nil {
		return
	}
	plan.Summary = truncateForUpload(plan.Summary, 320)
	plan.EvidenceRefs = compactEvidenceRefsForUpload(plan.EvidenceRefs, 8)
	for i := range plan.Stages {
		stage := &plan.Stages[i]
		stage.Objective = truncateForUpload(stage.Objective, 240)
		stage.BusinessIntent = truncateForUpload(stage.BusinessIntent, 240)
		stage.SuccessState = truncateForUpload(stage.SuccessState, 240)
		stage.ComponentRefs = limitStringsForUpload(stage.ComponentRefs, 4)
		stage.APIRefs = limitStringsForUpload(stage.APIRefs, 4)
		stage.StyleRefs = limitStringsForUpload(stage.StyleRefs, 3)
		stage.DataModelRefs = limitStringsForUpload(stage.DataModelRefs, 3)
		stage.WaitConditions = limitStringsForUpload(stage.WaitConditions, 4)
		stage.CapturePoints = limitStringsForUpload(stage.CapturePoints, 4)
		stage.RiskNotes = limitStringsForUpload(stage.RiskNotes, 3)
		stage.EvidenceRefs = compactEvidenceRefsForUpload(stage.EvidenceRefs, 3)
		stage.Interaction.EvidenceRefs = compactEvidenceRefsForUpload(stage.Interaction.EvidenceRefs, 2)
		stage.Interaction.Target.SelectorAlternatives = compactSelectorCandidatesForUpload(stage.Interaction.Target.SelectorAlternatives, 2)
		stage.Interaction.Target.EvidenceRefs = compactEvidenceRefsForUpload(stage.Interaction.Target.EvidenceRefs, 2)
		if stage.TargetContract != nil {
			compactTargetContractForUpload(stage.TargetContract)
		}
		for j := range stage.InputContent {
			stage.InputContent[j].EvidenceRefs = compactEvidenceRefsForUpload(stage.InputContent[j].EvidenceRefs, 1)
			stage.InputContent[j].Value = truncateForUpload(stage.InputContent[j].Value, 120)
		}
	}
	plan.UncertaintyReport = compactStageUncertaintiesForUpload(plan.UncertaintyReport, 4)
	plan.SafetyPolicy.ForbiddenPages = limitStringsForUpload(plan.SafetyPolicy.ForbiddenPages, 12)
	plan.SafetyPolicy.ForbiddenData = limitStringsForUpload(plan.SafetyPolicy.ForbiddenData, 12)
	plan.SafetyPolicy.Redactions.MaskSelectors = limitStringsForUpload(plan.SafetyPolicy.Redactions.MaskSelectors, 6)
}

func compactBrowserAgentOutlineForUpload(outline *model.BrowserAgentScriptOutline) {
	if outline == nil {
		return
	}
	outline.Summary = truncateForUpload(outline.Summary, 320)
	outline.EvidenceRefs = compactEvidenceRefsForUpload(outline.EvidenceRefs, 8)
	outline.AllowedExplorationScope.AllowedOrigins = limitStringsForUpload(outline.AllowedExplorationScope.AllowedOrigins, 4)
	outline.AllowedExplorationScope.AllowedRoutes = limitStringsForUpload(outline.AllowedExplorationScope.AllowedRoutes, 8)
	outline.AllowedExplorationScope.ForbiddenPathPrefixes = limitStringsForUpload(outline.AllowedExplorationScope.ForbiddenPathPrefixes, 16)
	outline.AllowedExplorationScope.ForbiddenKeywords = limitStringsForUpload(outline.AllowedExplorationScope.ForbiddenKeywords, 12)
	outline.ForbiddenActions = limitStringsForUpload(outline.ForbiddenActions, 12)
	outline.ServerEditableFields = limitStringsForUpload(outline.ServerEditableFields, 12)
	outline.ImmutableFields = limitStringsForUpload(outline.ImmutableFields, 12)
	for i := range outline.Stages {
		stage := &outline.Stages[i]
		stage.Objective = truncateForUpload(stage.Objective, 240)
		stage.SuccessState = truncateForUpload(stage.SuccessState, 240)
		stage.WaitConditions = limitStringsForUpload(stage.WaitConditions, 4)
		stage.CapturePoints = limitStringsForUpload(stage.CapturePoints, 4)
		stage.CanModify = limitStringsForUpload(stage.CanModify, 8)
		stage.MustPreserve = limitStringsForUpload(stage.MustPreserve, 8)
		stage.EvidenceRefs = compactEvidenceRefsForUpload(stage.EvidenceRefs, 3)
		if stage.TargetContract != nil {
			compactTargetContractForUpload(stage.TargetContract)
		}
		if len(stage.Components) > 3 {
			stage.Components = stage.Components[:3]
		}
		for j := range stage.Components {
			component := &stage.Components[j]
			component.SelectorAlternatives = compactSelectorCandidatesForUpload(component.SelectorAlternatives, 2)
			component.EvidenceRefs = compactEvidenceRefsForUpload(component.EvidenceRefs, 2)
		}
		if len(stage.Interactions) > 1 {
			stage.Interactions = stage.Interactions[:1]
		}
		for j := range stage.Interactions {
			interaction := &stage.Interactions[j]
			interaction.EvidenceRefs = compactEvidenceRefsForUpload(interaction.EvidenceRefs, 2)
			interaction.WaitConditions = limitStringsForUpload(interaction.WaitConditions, 3)
			interaction.Target.SelectorAlternatives = compactSelectorCandidatesForUpload(interaction.Target.SelectorAlternatives, 2)
			interaction.Target.EvidenceRefs = compactEvidenceRefsForUpload(interaction.Target.EvidenceRefs, 2)
		}
	}
	outline.UncertaintyReport = compactStageUncertaintiesForUpload(outline.UncertaintyReport, 4)
}

func compactBrowserAgentContractForUpload(contract *model.BrowserAgentContract) {
	if contract == nil {
		return
	}
	contract.RepairPolicy.AllowedRepairKinds = limitStringsForUpload(contract.RepairPolicy.AllowedRepairKinds, 8)
	contract.RepairPolicy.EditableFields = limitStringsForUpload(contract.RepairPolicy.EditableFields, 12)
	contract.RepairPolicy.ImmutableFields = limitStringsForUpload(contract.RepairPolicy.ImmutableFields, 12)
	contract.ObservationPolicy.AllowedFields = limitStringsForUpload(contract.ObservationPolicy.AllowedFields, 12)
	contract.ObservationPolicy.MaskSelectors = limitStringsForUpload(contract.ObservationPolicy.MaskSelectors, 8)
}

func compactValidationForUpload(validation *model.ValidationSpec) {
	if validation == nil {
		return
	}
	validation.Assertion = truncateForUpload(validation.Assertion, 180)
	validation.Target.SelectorAlternatives = compactSelectorCandidatesForUpload(validation.Target.SelectorAlternatives, 2)
	validation.Target.EvidenceRefs = compactEvidenceRefsForUpload(validation.Target.EvidenceRefs, 2)
	validation.EvidenceRefs = compactEvidenceRefsForUpload(validation.EvidenceRefs, 2)
}

func compactTargetContractForUpload(contract *model.BrowserAgentTargetContract) {
	if contract == nil {
		return
	}
	contract.Purpose = truncateForUpload(contract.Purpose, 180)
	contract.AllowedRoles = limitStringsForUpload(contract.AllowedRoles, 4)
	contract.AllowedNames = limitStringsForUpload(contract.AllowedNames, 6)
	contract.ForbiddenNames = limitStringsForUpload(contract.ForbiddenNames, 6)
	contract.EvidenceRefs = compactEvidenceRefsForUpload(contract.EvidenceRefs, 2)
}

func compactSelectorCandidatesForUpload(candidates []model.SelectorCandidate, limit int) []model.SelectorCandidate {
	if limit <= 0 || len(candidates) == 0 {
		return nil
	}
	out := make([]model.SelectorCandidate, 0, limit)
	seen := map[string]bool{}
	for _, candidate := range candidates {
		key := candidate.Kind + "|" + candidate.Value
		if key == "|" || seen[key] {
			continue
		}
		seen[key] = true
		candidate.Source = truncateForUpload(candidate.Source, 80)
		candidate.EvidenceRefs = compactEvidenceRefsForUpload(candidate.EvidenceRefs, 1)
		out = append(out, candidate)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func compactEvidenceRefsForUpload(refs []model.EvidenceRef, limit int) []model.EvidenceRef {
	limited := limitEvidenceRefs(refs, limit)
	for i := range limited {
		limited[i].Summary = truncateForUpload(limited[i].Summary, 140)
	}
	return limited
}

func compactStageUncertaintiesForUpload(items []model.StageUncertainty, limit int) []model.StageUncertainty {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	if len(items) > limit {
		items = items[:limit]
	}
	out := make([]model.StageUncertainty, len(items))
	for i, item := range items {
		item.Summary = truncateForUpload(item.Summary, 180)
		item.SuggestedAction = truncateForUpload(item.SuggestedAction, 180)
		item.EvidenceRefs = compactEvidenceRefsForUpload(item.EvidenceRefs, 2)
		out[i] = item
	}
	return out
}

func limitRedactionsForUpload(redactions []model.RedactionSpec, limit int) []model.RedactionSpec {
	if limit <= 0 || len(redactions) == 0 {
		return nil
	}
	if len(redactions) > limit {
		redactions = redactions[:limit]
	}
	out := make([]model.RedactionSpec, len(redactions))
	for i, redaction := range redactions {
		redaction.Reason = truncateForUpload(redaction.Reason, 120)
		out[i] = redaction
	}
	return out
}

func limitStringsForUpload(values []string, limit int) []string {
	if limit <= 0 || len(values) == 0 {
		return nil
	}
	out := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(truncateForUpload(value, 160))
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func truncateForUpload(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len([]rune(value)) <= limit {
		return value
	}
	runes := []rune(value)
	if limit <= 1 {
		return string(runes[:limit])
	}
	return string(runes[:limit-1]) + "…"
}

func addUploadRef(refs map[string]bool, value string) {
	value = strings.TrimSpace(value)
	if value != "" {
		refs[value] = true
	}
}

func uploadRefMatches(refs map[string]bool, value string) bool {
	if len(refs) == 0 {
		return true
	}
	value = strings.TrimSpace(value)
	for ref := range refs {
		if value != "" && (strings.Contains(value, ref) || strings.Contains(ref, value)) {
			return true
		}
	}
	return false
}

func uploadComponentOverlap(refs map[string]bool, values []string) bool {
	for _, value := range values {
		if refs[value] {
			return true
		}
	}
	return false
}

func redactClientExecutionPackageText(pkg *model.ClientExecutionPackage) error {
	if pkg == nil {
		return nil
	}
	redactStringFields(reflect.ValueOf(pkg), map[uintptr]bool{})
	return nil
}

func redactStringFields(value reflect.Value, seen map[uintptr]bool) {
	if !value.IsValid() {
		return
	}
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return
		}
		ptr := value.Pointer()
		if ptr != 0 && seen[ptr] {
			return
		}
		if ptr != 0 {
			seen[ptr] = true
		}
		redactStringFields(value.Elem(), seen)
		return
	}
	if value.Kind() == reflect.Interface {
		if value.IsNil() {
			return
		}
		inner := value.Elem()
		if inner.Kind() == reflect.String {
			redacted := agents.RedactSensitiveUserText(inner.String())
			if redacted != inner.String() && value.CanSet() {
				value.Set(reflect.ValueOf(redacted))
			}
			return
		}
		redactStringFields(inner, seen)
		return
	}
	switch value.Kind() {
	case reflect.String:
		if value.CanSet() {
			value.SetString(agents.RedactSensitiveUserText(value.String()))
		}
	case reflect.Struct:
		if value.Type().PkgPath() == "time" {
			return
		}
		for i := 0; i < value.NumField(); i++ {
			field := value.Field(i)
			if field.CanSet() || field.Kind() == reflect.Pointer || field.Kind() == reflect.Slice || field.Kind() == reflect.Map || field.Kind() == reflect.Struct || field.Kind() == reflect.Interface {
				redactStringFields(field, seen)
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			redactStringFields(value.Index(i), seen)
		}
	case reflect.Map:
		if value.IsNil() {
			return
		}
		for _, key := range value.MapKeys() {
			item := value.MapIndex(key)
			if item.Kind() == reflect.String {
				redacted := agents.RedactSensitiveUserText(item.String())
				if redacted != item.String() {
					value.SetMapIndex(key, reflect.ValueOf(redacted))
				}
				continue
			}
			if item.Kind() == reflect.Interface && !item.IsNil() && item.Elem().Kind() == reflect.String {
				redacted := agents.RedactSensitiveUserText(item.Elem().String())
				if redacted != item.Elem().String() {
					value.SetMapIndex(key, reflect.ValueOf(any(redacted)))
				}
			}
		}
	}
}

var scriptPlanHashLiteralPattern = regexp.MustCompile(`const cascadePlanHash = "([a-f0-9]{64})";`)
var packagePasswordLeakagePattern = regexp.MustCompile(`(?i)(密码|口令)\s*[:：=]\s*[^\s,，。;；)）]{4,}|\b(password|passwd|pwd|passcode)\b\s*[:：=]\s*[^\s,，。;；)）]{4,}`)
var packageAPIKeyLeakagePattern = regexp.MustCompile(`(?i)\bsk-[A-Za-z0-9_-]{16,}\b`)
var packageEnvFileLeakagePattern = regexp.MustCompile(`(?i)([A-Za-z]:)?[\\/][^"'\s]*\.env(?:\.[A-Za-z0-9_-]+)?|\.env(?:\.[A-Za-z0-9_-]+)?\s*[:=]`)

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
	outlineRuntime := pkg.ExecutableScriptBundle.ScriptManifest.Runtime == model.ExecutableScriptRuntimeBrowserAgentOutlineV1
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
	stageKindValidation := preflightBusinessStageKinds(pkg.ExecutableScriptBundle)
	findings = append(findings, stageKindValidation...)
	useLegacyLoginCheck := !bundleHasBusinessStageKinds(pkg.ExecutableScriptBundle)
	repeatedLoginCount := 0
	allowedDomains := pkg.RecordingRunSpec.AllowedDomains
	for _, step := range doc.Steps {
		if useLegacyLoginCheck && isLoginStep(step) {
			repeatedLoginCount++
		}
		if !outlineRuntime && actionRequiresSelector(step.Action.Type) && !actionTargetHasStableHandle(step.Action.Target) {
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
		if !outlineRuntime && len(step.EvidenceRefs) == 0 && actionRequiresSelector(step.Action.Type) {
			findings = append(findings, packagePreflightFinding(
				"evidence_missing_"+shortID(step.NodeID),
				model.FindingSeverityWarning,
				"business action is missing route/component/selector evidence: "+step.NodeID,
				"Bind ProjectIntelligenceGraph route/component/API/selector evidence to the graph node.",
			))
		}
	}
	if outlineRuntime {
		findings = append(findings, preflightBrowserAgentOutline(pkg.ExecutableScriptBundle)...)
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
	if outlineRuntime {
		if data, err := json.Marshal(pkg); err == nil && len(data) > 512*1024 {
			findings = append(findings, packagePreflightFinding(
				"payload_too_large",
				model.FindingSeverityBlocking,
				fmt.Sprintf("browser-agent outline package is too large: %d bytes", len(data)),
				"Upload only markdown, stage JSON, browser-agent outline, contract, hashes, and compact evidence refs.",
			))
		}
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

func preflightBrowserAgentOutline(bundle *model.ExecutableRecordingScriptBundle) []model.AgentFinding {
	findings := []model.AgentFinding{}
	if bundle == nil {
		return findings
	}
	if bundle.StageApprovalPlan == nil {
		findings = append(findings, packagePreflightFinding(
			"stage_approval_plan_missing",
			model.FindingSeverityBlocking,
			"browser agent outline package is missing stage_approval_plan",
			"Regenerate the package so the user can approve stage JSON before upload.",
		))
	}
	if bundle.ScriptOutline == nil {
		findings = append(findings, packagePreflightFinding(
			"script_outline_missing",
			model.FindingSeverityBlocking,
			"browser agent outline package is missing script_outline",
			"Regenerate the package so the server browser agent receives route/component interaction guidance.",
		))
	}
	if bundle.AgentPromptPolicy == nil {
		findings = append(findings, packagePreflightFinding(
			"agent_prompt_policy_missing",
			model.FindingSeverityBlocking,
			"browser agent outline package is missing agent_prompt_policy",
			"Regenerate the package so server-side repairs have explicit editable and immutable boundaries.",
		))
	}
	if bundle.BrowserAgentContract == nil {
		findings = append(findings, packagePreflightFinding(
			"browser_agent_contract_missing",
			model.FindingSeverityBlocking,
			"browser agent outline package is missing browser_agent_contract",
			"Regenerate the package so server-side browser agent repair has explicit policy, observation, and conflict boundaries.",
		))
	}
	if bundle.PlanJSON != nil {
		for _, step := range bundle.PlanJSON.Steps {
			if browserAgentStepNeedsValidation(step) && !browserAgentStepHasRequiredValidation(step) {
				findings = append(findings, packagePreflightFinding(
					"required_validation_missing_"+shortID(step.NodeID),
					model.FindingSeverityBlocking,
					"business step is missing required outcome validation: "+step.NodeID,
					"Add a required validation such as url_matches, element_visible, text_contains, or page_title_contains.",
				))
			}
			if browserAgentStepNeedsValidation(step) && (step.TargetContract == nil || strings.TrimSpace(step.TargetContract.SemanticID) == "") {
				findings = append(findings, packagePreflightFinding(
					"target_contract_missing_"+shortID(step.NodeID),
					model.FindingSeverityBlocking,
					"business step is missing target_contract: "+step.NodeID,
					"Bind each business action to semantic_id, allowed role/name, forbidden names, and component evidence.",
				))
			}
		}
	}
	if bundle.StageApprovalPlan != nil {
		for _, stage := range bundle.StageApprovalPlan.Stages {
			if strings.TrimSpace(stage.Objective) == "" {
				findings = append(findings, packagePreflightFinding(
					"stage_objective_missing_"+shortID(stage.NodeID),
					model.FindingSeverityBlocking,
					"stage approval item is missing objective: "+stage.NodeID,
					"Bind each stage to a user-approved business objective before upload.",
				))
			}
			if stageEvidenceRequired(stage) && !stageApprovalHasEvidence(stage) {
				findings = append(findings, packagePreflightFinding(
					"stage_evidence_missing_"+shortID(stage.NodeID),
					model.FindingSeverityWarning,
					"stage approval item is missing route/component/API evidence: "+stage.NodeID,
					"Keep the stage objective and target_contract; the server browser agent may add runtime evidence inside the approved product scope.",
				))
			}
			if stage.TargetContract == nil || strings.TrimSpace(stage.TargetContract.SemanticID) == "" {
				findings = append(findings, packagePreflightFinding(
					"stage_target_contract_missing_"+shortID(stage.NodeID),
					model.FindingSeverityBlocking,
					"stage approval item is missing target_contract: "+stage.NodeID,
					"Regenerate the stage plan from intent-traced route/component/action evidence.",
				))
			}
		}
		for _, uncertainty := range bundle.StageApprovalPlan.UncertaintyReport {
			if uncertainty.Blocking {
				findings = append(findings, packagePreflightFinding(
					"stage_uncertainty_"+shortID(uncertainty.ID),
					model.FindingSeverityWarning,
					"stage approval plan has runtime uncertainty: "+uncertainty.Summary,
					firstNonEmptyString(uncertainty.SuggestedAction, "Let the server browser agent resolve this inside the approved product scope or return a repair request."),
				))
			}
		}
	}
	if bundle.ScriptOutline != nil {
		for _, stage := range bundle.ScriptOutline.Stages {
			if len(stage.Interactions) == 0 {
				findings = append(findings, packagePreflightFinding(
					"outline_interaction_missing_"+shortID(stage.NodeID),
					model.FindingSeverityBlocking,
					"script outline stage has no interaction guidance: "+stage.NodeID,
					"Add route, component, target role/name/selector candidates, and wait conditions for this stage.",
				))
			}
			if stage.TargetContract == nil || strings.TrimSpace(stage.TargetContract.SemanticID) == "" {
				findings = append(findings, packagePreflightFinding(
					"outline_target_contract_missing_"+shortID(stage.NodeID),
					model.FindingSeverityBlocking,
					"script outline stage is missing target_contract: "+stage.NodeID,
					"Provide semantic_id, allowed role/name, forbidden names, and component evidence for the server browser agent.",
				))
			}
		}
		for _, uncertainty := range bundle.ScriptOutline.UncertaintyReport {
			if uncertainty.Blocking {
				findings = append(findings, packagePreflightFinding(
					"outline_uncertainty_"+shortID(uncertainty.ID),
					model.FindingSeverityWarning,
					"script outline has runtime uncertainty: "+uncertainty.Summary,
					firstNonEmptyString(uncertainty.SuggestedAction, "Let the server browser agent resolve this inside the approved product scope or return a repair request."),
				))
			}
		}
	}
	if bundle.AgentPromptPolicy != nil {
		if strings.TrimSpace(bundle.AgentPromptPolicy.SystemPrompt) == "" || len(bundle.AgentPromptPolicy.ImmutableFields) == 0 || len(bundle.AgentPromptPolicy.EditableFields) == 0 {
			findings = append(findings, packagePreflightFinding(
				"prompt_policy_incomplete",
				model.FindingSeverityBlocking,
				"agent prompt policy is incomplete",
				"Declare system prompt, immutable fields, and editable fields for the server browser agent.",
			))
		}
	}
	return findings
}

func bundleHasBusinessStageKinds(bundle *model.ExecutableRecordingScriptBundle) bool {
	if bundle == nil || bundle.StageApprovalPlan == nil {
		return false
	}
	for _, stage := range bundle.StageApprovalPlan.Stages {
		if stage.StageKind != "" {
			return true
		}
	}
	return false
}

func preflightBusinessStageKinds(bundle *model.ExecutableRecordingScriptBundle) []model.AgentFinding {
	findings := []model.AgentFinding{}
	if bundle == nil || bundle.StageApprovalPlan == nil || !bundleHasBusinessStageKinds(bundle) {
		return findings
	}
	sessionSetupCount := 0
	coreBusinessCount := 0
	hasSubmit := false
	hasPostSubmitTransition := false
	for _, stage := range bundle.StageApprovalPlan.Stages {
		switch stage.StageKind {
		case model.BusinessStageKindSessionSetup:
			sessionSetupCount++
		case model.BusinessStageKindBusinessAction, model.BusinessStageKindBusinessInput, model.BusinessStageKindModeSelection, model.BusinessStageKindBusinessSubmit:
			coreBusinessCount++
		}
		if stage.StageKind != model.BusinessStageKindSessionSetup && stageLooksLikeLoginOrCredential(stage) {
			findings = append(findings, packagePreflightFinding(
				"login_outside_session_"+shortID(stage.NodeID),
				model.FindingSeverityBlocking,
				"non-session business stage contains login or credential semantics: "+stage.NodeID,
				"Keep login only in the session_setup stage; later stages must reuse the authenticated state.",
			))
		}
		if stage.StageKind == model.BusinessStageKindBusinessInput && !stagePreservesBusinessInput(stage) {
			findings = append(findings, packagePreflightFinding(
				"business_input_semantics_missing_"+shortID(stage.NodeID),
				model.FindingSeverityBlocking,
				"business_input stage does not preserve user input semantics: "+stage.NodeID,
				"Keep the user-approved fill content such as project name or feature request in input_content/action value.",
			))
		}
		if stage.StageKind == model.BusinessStageKindBusinessSubmit {
			hasSubmit = true
			if strings.TrimSpace(stage.ExpectedRouteAfterAction) != "" && strings.TrimSpace(stage.ExpectedRouteAfterAction) != strings.TrimSpace(stage.EntryRoute) {
				hasPostSubmitTransition = true
			}
		}
		if stage.StageKind == model.BusinessStageKindObserveProgress {
			if stage.Interaction.Kind == model.GraphActionClick || stage.Interaction.Kind == model.GraphActionFill || stage.Interaction.Kind == model.GraphActionSelect {
				findings = append(findings, packagePreflightFinding(
					"observe_progress_has_business_action_"+shortID(stage.NodeID),
					model.FindingSeverityBlocking,
					"observe_progress stage must not click or fill: "+stage.NodeID,
					"Use wait/inspect/capture only while observing build progress.",
				))
			}
		}
	}
	if sessionSetupCount > 1 {
		findings = append(findings, packagePreflightFinding(
			"duplicate_session_setup",
			model.FindingSeverityBlocking,
			"business stage plan contains repeated session_setup stages",
			"Generate exactly one session_setup stage, then reuse the authenticated session for all business stages.",
		))
	}
	if coreBusinessCount == 0 {
		findings = append(findings, packagePreflightFinding(
			"business_stage_missing",
			model.FindingSeverityBlocking,
			"business stage plan has no core business stage",
			"Add business_action/business_input/mode_selection/business_submit stages derived from the user requirement.",
		))
	}
	if hasSubmit && !hasPostSubmitTransition {
		findings = append(findings, packagePreflightFinding(
			"business_submit_transition_missing",
			model.FindingSeverityBlocking,
			"business_submit stage is missing route/state transition",
			"Set expected_route_after_action to project_detail or build_running so the browser agent knows the post-submit state.",
		))
	}
	return findings
}

func stageLooksLikeLoginOrCredential(stage model.StageApprovalStage) bool {
	values := []string{
		stage.ID,
		stage.NodeID,
		stage.Title,
		stage.Objective,
		stage.BusinessIntent,
		stage.TargetRoute,
		stage.TargetRouteTemplate,
		stage.EntryRoute,
		stage.ExpectedRouteAfterAction,
		stage.Interaction.Target.Label,
		stage.Interaction.Target.Text,
		stage.Interaction.Target.TestID,
		stage.Interaction.Target.Selector,
		stage.Interaction.Value,
		stage.Interaction.InputRef,
		stage.Interaction.SecretRef,
	}
	for _, input := range stage.InputContent {
		values = append(values, input.Kind, input.Label, input.Value, input.InputRef, input.SecretRef)
	}
	text := strings.ToLower(strings.Join(values, " "))
	return strings.Contains(text, "login") ||
		strings.Contains(text, "sign in") ||
		strings.Contains(text, "signin") ||
		strings.Contains(text, "sign up") ||
		strings.Contains(text, "register") ||
		strings.Contains(text, "password") ||
		strings.Contains(text, "demo_password") ||
		strings.Contains(text, "登录") ||
		strings.Contains(text, "登陆") ||
		strings.Contains(text, "注册") ||
		strings.Contains(text, "密码")
}

func stagePreservesBusinessInput(stage model.StageApprovalStage) bool {
	for _, input := range stage.InputContent {
		text := strings.TrimSpace(input.Value + " " + input.InputRef + " " + input.Label)
		if text != "" && !strings.Contains(strings.ToLower(text), "password") && !strings.Contains(text, "密码") {
			return true
		}
	}
	return strings.TrimSpace(stage.Interaction.Value) != ""
}

func stageApprovalHasEvidence(stage model.StageApprovalStage) bool {
	return len(stage.EvidenceRefs) > 0 ||
		len(stage.ComponentRefs) > 0 ||
		len(stage.APIRefs) > 0 ||
		len(stage.StyleRefs) > 0 ||
		len(stage.DataModelRefs) > 0 ||
		len(stage.Interaction.EvidenceRefs) > 0 ||
		len(stage.Interaction.Target.EvidenceRefs) > 0
}

func stageEvidenceRequired(stage model.StageApprovalStage) bool {
	if stage.StageKind == "" {
		return true
	}
	switch stage.StageKind {
	case model.BusinessStageKindBusinessAction, model.BusinessStageKindBusinessInput, model.BusinessStageKindModeSelection, model.BusinessStageKindBusinessSubmit:
		return true
	default:
		return false
	}
}

func browserAgentStepNeedsValidation(step model.ScriptStep) bool {
	switch step.Action.Type {
	case model.GraphActionNavigate, model.GraphActionClick, model.GraphActionFill, model.GraphActionSelect, model.GraphActionUpload, model.GraphActionAPICall:
		return true
	default:
		return false
	}
}

func browserAgentStepHasRequiredValidation(step model.ScriptStep) bool {
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
	values := []string{
		step.NodeID,
		step.Title,
		step.BusinessValue,
		step.ExpectedOutcome,
		step.Action.Target.Selector,
		step.Action.Target.Text,
		step.Action.Target.Label,
		step.Action.Target.TestID,
	}
	if step.Action.Type == model.GraphActionNavigate {
		values = append(values, step.Action.Target.URL)
	}
	text := strings.ToLower(strings.Join(values, " "))
	return strings.Contains(text, "login") ||
		strings.Contains(text, "sign in") ||
		strings.Contains(text, "signin") ||
		strings.Contains(text, "sign up") ||
		strings.Contains(text, "register") ||
		strings.Contains(text, "登录") ||
		strings.Contains(text, "登入") ||
		strings.Contains(text, "注册") ||
		strings.Contains(text, "创建账户")
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
	case packageAPIKeyLeakagePattern.Match(data):
		return "api key"
	case packageEnvFileLeakagePattern.Match(data):
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
	goals := demoGoalsForPackage(project)
	return model.ProjectContextSummary{
		ContextID:         "ctx_" + project.ID,
		SchemaVersion:     model.ProjectContextSchemaVersion,
		Mode:              project.Mode,
		Name:              project.Name,
		ProductURL:        project.ProductURL,
		TargetAudience:    project.TargetAudience,
		Goals:             goals,
		Audiences:         append([]model.AudienceProfile{}, project.Audiences...),
		AccessPolicy:      project.AccessPolicy,
		SecurityPolicy:    project.SecurityPolicy,
		InputFingerprints: inputFingerprints,
	}
}

func productMapSummaryForPackage(project *model.ProjectContext, productMap *model.ProductMap) model.ProductMapSummary {
	if productMap == nil {
		return model.ProductMapSummary{}
	}
	intentText := ""
	if project != nil {
		intentText = project.ProductDescription
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
		Summary:      packageProductMapSummary(intentText, productMap.Summary),
		Pages:        sanitizeProductPagesForPackage(productMap.Pages, intentText),
		Features:     sanitizeFeaturesForPackage(productMap.Features, intentText),
		Routes:       productMap.Routes,
		Components:   components,
		DataModels:   dataModels,
		Roles:        productMap.Roles,
		Workflows:    sanitizeWorkflowsForPackage(productMap.Workflows, intentText),
		EvidenceRefs: append([]model.EvidenceRef{}, productMap.EvidenceRefs...),
	}
}

func demoGoalsForPackage(project *model.ProjectContext) []model.DemoGoal {
	if project == nil || strings.TrimSpace(project.ProductDescription) == "" {
		if project == nil {
			return nil
		}
		return append([]model.DemoGoal{}, project.Goals...)
	}
	return []model.DemoGoal{{
		ID:               "goal_primary",
		UseCase:          model.DemoUseCaseLaunch,
		AudienceID:       "audience_primary",
		ValueProposition: project.ProductDescription,
		SuccessCriteria:  demoSuccessCriteriaFromProject(project),
		Priority:         1,
	}}
}

func demoSuccessCriteriaFromProject(project *model.ProjectContext) []string {
	if project == nil {
		return nil
	}
	values := []string{}
	values = append(values, splitRequirementClauses(project.ProductDescription)...)
	values = append(values, project.MustShow...)
	if len(values) == 0 && strings.TrimSpace(project.ProductDescription) != "" {
		values = append(values, strings.TrimSpace(project.ProductDescription))
	}
	return limitStringsForUpload(uniqueNonEmptyStrings(values), 8)
}

func splitRequirementClauses(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		switch r {
		case '\n', '\r', '，', ',', '；', ';', '。':
			return true
		default:
			return false
		}
	})
	out := []string{}
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func uniqueNonEmptyStrings(values []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func packageProductMapSummary(intentText string, fallback string) string {
	if strings.TrimSpace(intentText) == "" {
		return fallback
	}
	return "需求作用域产品地图：" + strings.TrimSpace(intentText)
}

func sanitizeProductPagesForPackage(pages []*model.ProductPage, intentText string) []*model.ProductPage {
	out := make([]*model.ProductPage, 0, len(pages))
	for _, page := range pages {
		if page == nil {
			continue
		}
		copied := *page
		copied.Actions = filterPackageSummaryStrings(page.Actions, intentText)
		copied.PrimaryActions = filterPackageSummaryActions(page.PrimaryActions, intentText)
		out = append(out, &copied)
	}
	return out
}

func sanitizeFeaturesForPackage(features []*model.Feature, intentText string) []*model.Feature {
	out := make([]*model.Feature, 0, len(features))
	for _, feature := range features {
		if feature == nil || packageSummaryTextOutOfScope(feature.Name+" "+feature.UserValue+" "+feature.BusinessValue, intentText) {
			continue
		}
		out = append(out, feature)
	}
	return out
}

func sanitizeWorkflowsForPackage(workflows []*model.WorkflowCandidate, intentText string) []*model.WorkflowCandidate {
	out := make([]*model.WorkflowCandidate, 0, len(workflows))
	for _, workflow := range workflows {
		if workflow == nil || packageSummaryTextOutOfScope(workflow.Name+" "+string(workflow.UseCase)+" "+strings.Join(workflow.RiskNotes, " "), intentText) {
			continue
		}
		out = append(out, workflow)
	}
	return out
}

func filterPackageSummaryStrings(values []string, intentText string) []string {
	out := []string{}
	for _, value := range values {
		if strings.TrimSpace(value) == "" || packageSummaryTextOutOfScope(value, intentText) {
			continue
		}
		out = append(out, value)
	}
	return out
}

func filterPackageSummaryActions(actions []model.UIActionRef, intentText string) []model.UIActionRef {
	out := []model.UIActionRef{}
	for _, action := range actions {
		if packageSummaryTextOutOfScope(action.Label+" "+action.Selector+" "+action.Kind, intentText) {
			continue
		}
		out = append(out, action)
	}
	return out
}

func packageSummaryTextOutOfScope(value string, intentText string) bool {
	text := strings.ToLower(value)
	intent := strings.ToLower(intentText)
	if text == "" {
		return false
	}
	blockers := []string{"graph can be approved", "rehearsal pass rate", "排练通过率", "可审批", "审批", "approve", "approved", "批准", "regenerate", "重新生成", "revise", "修订", "polish", "润色", "cancel", "取消", "delete", "删除", "remove", "移除", "stop", "停止"}
	for _, blocker := range blockers {
		if strings.Contains(text, strings.ToLower(blocker)) && !strings.Contains(intent, strings.ToLower(blocker)) {
			return true
		}
	}
	return false
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

func resolveCloudDeliverableDownloadURL(baseURL string, resultPackageID string, deliverable model.ExecutionDeliverable) (string, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return "", errors.New("cloud base url is required")
	}
	downloadURL := strings.TrimSpace(deliverable.DownloadURL)
	if downloadURL == "" {
		downloadURL = fmt.Sprintf("/v1/dev/result-packages/%s/deliverables/%s", url.PathEscape(resultPackageID), url.PathEscape(deliverable.ID))
	}
	if parsed, err := url.Parse(downloadURL); err == nil && parsed.IsAbs() {
		return downloadURL, nil
	}
	if !strings.HasPrefix(downloadURL, "/") {
		downloadURL = "/" + downloadURL
	}
	return baseURL + downloadURL, nil
}

func cloudDeliverableFileName(deliverable model.ExecutionDeliverable) string {
	parts := []string{deliverable.Kind, deliverable.Role, deliverable.ID}
	name := strings.Trim(strings.Join(parts, "_"), "_")
	if name == "" {
		name = "deliverable"
	}
	extension := extensionFromMimeType(deliverable.MimeType)
	if extension == "" {
		extension = filepath.Ext(deliverable.URI)
	}
	if extension != "" && !strings.HasSuffix(strings.ToLower(name), strings.ToLower(extension)) {
		name += extension
	}
	replacer := strings.NewReplacer(`\`, "_", `/`, "_", ":", "_", "*", "_", "?", "_", `"`, "_", "<", "_", ">", "_", "|", "_", "\r", "_", "\n", "_")
	return replacer.Replace(name)
}

func extensionFromMimeType(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0])) {
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "application/zip":
		return ".zip"
	case "application/json":
		return ".json"
	default:
		return ""
	}
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
