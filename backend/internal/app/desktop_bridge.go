package app

import (
	"context"
	"encoding/json"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
	"cascade-demoops/backend/internal/store"
)

type BridgeResponse struct {
	OK    bool            `json:"ok"`
	Error string          `json:"error,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
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
	return bridgeValue(NewRuntimeConfigView(b.service.RuntimeConfig()), nil)
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

func bridgeValue(value any, err error) BridgeResponse {
	if err != nil {
		return BridgeResponse{OK: false, Error: err.Error()}
	}
	data, marshalErr := json.Marshal(value)
	if marshalErr != nil {
		return BridgeResponse{OK: false, Error: marshalErr.Error()}
	}
	return BridgeResponse{OK: true, Data: data}
}
