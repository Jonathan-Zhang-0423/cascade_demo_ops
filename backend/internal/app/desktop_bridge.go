package app

import (
	"context"
	"encoding/json"
	"net/http"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/credentialstore"
	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
	"cascade-demoops/backend/internal/store"
)

type BridgeResponse struct {
	OK        bool             `json:"ok"`
	Error     string           `json:"error,omitempty"`
	ErrorInfo *BridgeErrorInfo `json:"error_info,omitempty"`
	Data      json.RawMessage  `json:"data,omitempty"`
}

// HTTPHandler exposes the same bridge contract to the embedded Wails asset server.
func (b *DesktopBridge) HTTPHandler() http.Handler {
	return NewDevHTTPServer(b.service).Handler()
}

type DesktopBridge struct {
	service *Service
}

func NewDesktopBridge(runtime config.AppRuntimeConfig, states store.StateStore) (*DesktopBridge, error) {
	service, err := NewService(runtime, states)
	if err != nil {
		return nil, err
	}
	return &DesktopBridge{service: service}, nil
}

func (b *DesktopBridge) RuntimeConfig() BridgeResponse {
	ctx := context.Background()
	return bridgeValue(NewRuntimeConfigView(b.service.RuntimeConfig(), b.service.ExchangeIdentityStatus(ctx)), nil)
}

func (b *DesktopBridge) StoreGitHubToken(token string) BridgeResponse {
	err := credentialstore.StoreGitHubToken(token)
	return bridgeValue(map[string]bool{"configured": err == nil}, err)
}

func (b *DesktopBridge) GitHubTokenStatus() BridgeResponse {
	return bridgeValue(map[string]bool{"configured": credentialstore.GitHubTokenConfigured()}, nil)
}

func (b *DesktopBridge) DeleteGitHubToken() BridgeResponse {
	err := credentialstore.DeleteGitHubToken()
	return bridgeValue(map[string]bool{"configured": false}, err)
}

func (b *DesktopBridge) DesktopUpdateStatus() BridgeResponse {
	status, err := desktopUpdateConfiguration(b.service.RuntimeConfig())
	return bridgeValue(status, err)
}

func (b *DesktopBridge) CheckDesktopUpdate() BridgeResponse {
	status, err := CheckDesktopUpdate(context.Background(), b.service.RuntimeConfig())
	return bridgeValue(status, err)
}

func (b *DesktopBridge) ApplyDesktopUpdate() BridgeResponse {
	err := ApplyDesktopUpdate(context.Background(), b.service.RuntimeConfig())
	return bridgeValue(map[string]bool{"started": err == nil}, err)
}

func (b *DesktopBridge) CreateProject(input orchestrator.UserInput) BridgeResponse {
	state, err := b.service.CreateProject(context.Background(), input)
	return bridgeValue(state, err)
}

func (b *DesktopBridge) SaveProjectInput(projectID string, inputs model.ProjectInputBundle) BridgeResponse {
	projectContext, err := b.service.SaveProjectInput(context.Background(), projectID, inputs)
	return bridgeValue(projectContext, err)
}

func (b *DesktopBridge) GetWorkflowGraph(projectID string) BridgeResponse {
	graph, err := b.service.GetWorkflowGraph(context.Background(), projectID)
	return bridgeValue(graph, err)
}

func (b *DesktopBridge) GetUnderstandingReport(projectID string) BridgeResponse {
	report, err := b.service.GetUnderstandingReport(context.Background(), projectID)
	return bridgeValue(report, err)
}

func (b *DesktopBridge) GetExecutionScriptDocument(projectID string) BridgeResponse {
	document, err := b.service.GetExecutionScriptDocument(context.Background(), projectID)
	return bridgeValue(document, err)
}

func (b *DesktopBridge) GetExecutionScriptMarkdown(projectID string) BridgeResponse {
	markdown, artifact, err := b.service.GetExecutionScriptMarkdown(context.Background(), projectID)
	return bridgeValue(map[string]any{"markdown": markdown, "artifact": artifact}, err)
}

func (b *DesktopBridge) GetExecutableScriptBundle(projectID string) BridgeResponse {
	bundle, err := b.service.GetExecutableScriptBundle(context.Background(), projectID)
	return bridgeValue(bundle, err)
}

func (b *DesktopBridge) ApproveWorkflowGraph(projectID string, graph *model.DemoWorkflowGraph) BridgeResponse {
	state, err := b.service.ApproveWorkflowGraph(context.Background(), projectID, graph)
	return bridgeValue(state, err)
}

func (b *DesktopBridge) RunRehearsal(projectID string) BridgeResponse {
	state, err := b.service.RunRehearsal(context.Background(), projectID)
	return bridgeValue(state, err)
}

func (b *DesktopBridge) ArtifactURI(projectID string, fileName string) BridgeResponse {
	return bridgeValue(map[string]string{"uri": b.service.ArtifactURI(projectID, fileName)}, nil)
}

func (b *DesktopBridge) InitExecutionPackage(request model.ExecutionPackageInitRequest) BridgeResponse {
	response, err := b.service.InitExecutionPackage(context.Background(), request)
	return bridgeValue(response, err)
}

func (b *DesktopBridge) UploadExecutionPackage(request model.ExecutionPackageUploadRequest, payload model.ClientExecutionPackage) BridgeResponse {
	response, err := b.service.UploadExecutionPackage(context.Background(), request, payload)
	return bridgeValue(response, err)
}

func (b *DesktopBridge) GetExecutionPackageStatus(orgID string, exchangePackageID string) BridgeResponse {
	response, err := b.service.GetExecutionPackageStatus(context.Background(), orgID, exchangePackageID)
	return bridgeValue(response, err)
}

func (b *DesktopBridge) CompleteExecutionPackageWithResult(orgID string, exchangePackageID string, result model.RecordingResultPackage) BridgeResponse {
	response, err := b.service.CompleteExecutionPackageWithResult(context.Background(), orgID, exchangePackageID, result)
	return bridgeValue(response, err)
}

func (b *DesktopBridge) GetResultPackage(orgID string, resultPackageID string) BridgeResponse {
	response, err := b.service.GetResultPackage(context.Background(), orgID, resultPackageID)
	return bridgeValue(response, err)
}

func (b *DesktopBridge) AcknowledgeResultPackage(orgID string, request model.ResultPackageAckRequest) BridgeResponse {
	response, err := b.service.AcknowledgeResultPackage(context.Background(), orgID, request)
	return bridgeValue(response, err)
}

func (b *DesktopBridge) ReviewResultPackage(orgID string, resultPackageID string, request model.ResultReviewRequest) BridgeResponse {
	response, err := b.service.ReviewResultPackage(context.Background(), orgID, resultPackageID, request)
	return bridgeValue(response, err)
}

func (b *DesktopBridge) RequestResultRevision(orgID string, resultPackageID string, request model.ResultRevisionRequest) BridgeResponse {
	response, err := b.service.RequestResultRevision(context.Background(), orgID, resultPackageID, request)
	return bridgeValue(response, err)
}

func bridgeValue(value any, err error) BridgeResponse {
	if err != nil {
		info := bridgeErrorInfo(err)
		return BridgeResponse{OK: false, Error: info.Message, ErrorInfo: &info}
	}
	data, marshalErr := json.Marshal(value)
	if marshalErr != nil {
		info := bridgeErrorInfo(marshalErr)
		return BridgeResponse{OK: false, Error: info.Message, ErrorInfo: &info}
	}
	return BridgeResponse{OK: true, Data: data}
}
