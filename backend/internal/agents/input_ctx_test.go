package agents

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
)

func TestInputContextRedactsCredentialsEmbeddedInRequirements(t *testing.T) {
	input := orchestrator.UserInput{
		Mode:               model.AppModeDesktop,
		ProductURL:         "https://cascadeai.cn",
		ProductDescription: "演示登录，账号yikai.xu@cascadeai.co密码000000，然后新建项目",
		RequirementDocuments: []model.RequirementDocumentInput{{
			ID:    "req_credentials",
			Kind:  "text",
			Title: "账号yikai.xu@cascadeai.co",
			Body:  "password: 000000; email: yikai.xu@cascadeai.co",
		}},
		TargetAudience: "运营",
	}

	project, err := NewInputContextAgent().BuildProjectContext(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(project)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	for _, forbidden := range []string{"000000", "yikai.xu@cascadeai.co"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("project context leaked credential %q: %s", forbidden, text)
		}
	}
	for _, expected := range []string{"secret_ref:local-dev/demo_username", "secret_ref:local-dev/demo_password"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected redacted credential ref %q in project context: %s", expected, text)
		}
	}
}
