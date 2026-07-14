package agents

import (
	"context"
	"testing"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/llm"
	"cascade-demoops/backend/internal/model"
)

func TestMultimodalUnderstandingAcceptsArrayLLMOutput(t *testing.T) {
	project := &model.ProjectContext{
		ID:                 "project_mm_array",
		SchemaVersion:      model.ProjectContextSchemaVersion,
		Mode:               model.AppModeDesktop,
		ProductURL:         "https://app.example.com",
		ProductDescription: "展示从项目理解到自动录制的完整流程",
		TargetAudience:     "中国产品运营团队",
	}
	brief := &model.RequirementBrief{
		ID:            "brief_mm_array",
		ProjectID:     project.ID,
		SchemaVersion: model.MultimodalUnderstandingReportSchemaVersion,
		Objective:     "生成产品实战演示",
	}
	report, err := NewMultimodalUnderstandingAgentWithLLM(arrayMultimodalLLM{}).BuildUnderstanding(
		context.Background(),
		project,
		brief,
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.FeatureHypotheses) < 2 {
		t.Fatalf("expected array LLM output to add feature, got %d", len(report.FeatureHypotheses))
	}
	if len(report.WorkflowCandidates) < 2 {
		t.Fatalf("expected array LLM output to add workflow, got %d", len(report.WorkflowCandidates))
	}
	if report.Confidence != 0.82 {
		t.Fatalf("expected confidence from array output, got %f", report.Confidence)
	}
}

type arrayMultimodalLLM struct{}

func (a arrayMultimodalLLM) GenerateJSON(ctx context.Context, task config.ModelTask, req llm.JSONRequest, target any) (*llm.CallTrace, error) {
	content := `[
		{"name":"自动录制能力","user_value":"用户只输入需求、项目路径和网址即可自动录制","key_actions":["读取代码","生成脚本"],"confidence":0.82},
		{"name":"产品实战主流程","kind":"workflow","use_case":"launch","estimated_steps":5,"value_score":0.9,"feasibility":0.8,"risk_notes":["需要确认登录态"]}
	]`
	if err := llm.DecodeJSONContent(content, target); err != nil {
		return nil, err
	}
	return &llm.CallTrace{
		Provider:       config.ModelProviderKimi,
		Model:          "kimi-k2.7-code",
		Task:           task,
		AdapterVersion: config.ModelAdapterVersion,
	}, nil
}

func (a arrayMultimodalLLM) GenerateText(ctx context.Context, task config.ModelTask, req llm.TextRequest) (string, *llm.CallTrace, error) {
	return "", nil, nil
}

func (a arrayMultimodalLLM) GenerateMultimodal(ctx context.Context, task config.ModelTask, req llm.MultimodalRequest, target any) (*llm.CallTrace, error) {
	return a.GenerateJSON(ctx, task, llm.JSONRequest{System: req.System, User: req.User}, target)
}
