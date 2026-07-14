package agents

import (
	"context"
	"testing"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/llm"
	"cascade-demoops/backend/internal/model"
)

func TestRequirementReaderAcceptsArrayLLMOutput(t *testing.T) {
	project := &model.ProjectContext{
		ID:                 "project_requirement_array",
		SchemaVersion:      model.ProjectContextSchemaVersion,
		Mode:               model.AppModeDesktop,
		ProductURL:         "https://cascadeai.cn",
		ProductDescription: "生成产品实战自动流程演示。",
		TargetAudience:     "产品运营团队",
	}

	brief, err := NewRequirementReaderAgentWithLLM(arrayRequirementLLM{}).ReadRequirements(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	if brief.Scenario != "产品实战自动流程" {
		t.Fatalf("expected scenario from array output, got %q", brief.Scenario)
	}
	if brief.Objective != "演示输入需求、读取代码、生成执行包并上传服务器。" {
		t.Fatalf("expected objective from array output, got %q", brief.Objective)
	}
	if len(brief.MustShow) < 2 {
		t.Fatalf("expected merged must_show values, got %+v", brief.MustShow)
	}
	if len(brief.ForbiddenData) == 0 || brief.ForbiddenData[0] != "API Key" {
		t.Fatalf("expected merged forbidden data, got %+v", brief.ForbiddenData)
	}
	if brief.Confidence != 0.87 {
		t.Fatalf("expected max confidence from array output, got %f", brief.Confidence)
	}
}

type arrayRequirementLLM struct{}

func (a arrayRequirementLLM) GenerateJSON(ctx context.Context, task config.ModelTask, req llm.JSONRequest, target any) (*llm.CallTrace, error) {
	content := `[
		{
			"scenario": "产品实战自动流程",
			"objective": "演示输入需求、读取代码、生成执行包并上传服务器。",
			"primary_outcome": "生成可审批执行包",
			"must_show": ["本地代码理解"],
			"forbidden_data": ["API Key"],
			"use_cases": ["product_demo"],
			"confidence": 0.72
		},
		{
			"must_show": ["服务器生命周期"],
			"forbidden_pages": ["/settings/api-keys"],
			"confidence": 0.87
		}
	]`
	if err := llm.DecodeJSONContent(content, target); err != nil {
		return nil, err
	}
	return &llm.CallTrace{Provider: config.ModelProviderKimi, Model: "kimi-k2.7-code", Task: task, AdapterVersion: config.ModelAdapterVersion}, nil
}

func (a arrayRequirementLLM) GenerateText(ctx context.Context, task config.ModelTask, req llm.TextRequest) (string, *llm.CallTrace, error) {
	return "", nil, nil
}

func (a arrayRequirementLLM) GenerateMultimodal(ctx context.Context, task config.ModelTask, req llm.MultimodalRequest, target any) (*llm.CallTrace, error) {
	return a.GenerateJSON(ctx, task, llm.JSONRequest{System: req.System, User: req.User}, target)
}
