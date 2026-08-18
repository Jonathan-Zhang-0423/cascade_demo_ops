package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
)

func TestMediaDeliveryPolicyHTTPExposesDefaultsAndRecordsAcknowledgement(t *testing.T) {
	server := newTestDevHTTPServer(t)
	state, err := server.service.CreateProject(context.Background(), orchestrator.UserInput{
		ProjectID: "project-media-policy", Mode: model.AppModeDesktop, ProductURL: "https://product.example", TargetAudience: "产品团队",
	})
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/desktop/projects/" + state.ProjectID + "/media-delivery-policy"
	request := httptest.NewRequest(http.MethodGet, path, nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "standard_30d") || !strings.Contains(response.Body.String(), "auto_by_product_style") {
		t.Fatalf("unexpected default policy: status=%d body=%s", response.Code, response.Body.String())
	}

	preferences := model.DefaultMediaDeliveryPreferences()
	preferences.TOSRetention.ClientDisclosureAcknowledged = true
	body, err := json.Marshal(preferences)
	if err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodPut, path, strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), `"client_feedback_required":true`) {
		t.Fatalf("unexpected acknowledged policy: status=%d body=%s", response.Code, response.Body.String())
	}

	project, err := server.service.LoadProject(context.Background(), state.ProjectID)
	if err != nil || project.ProjectContext == nil || project.ProjectContext.Inputs == nil || project.ProjectContext.Inputs.MediaDeliveryPreferences == nil || !project.ProjectContext.Inputs.MediaDeliveryPreferences.TOSRetention.ClientDisclosureAcknowledged {
		t.Fatalf("policy acknowledgement was not persisted: project=%+v err=%v", project, err)
	}
}

func TestProjectContextSummaryCarriesEffectiveMediaDeliveryPolicy(t *testing.T) {
	project := &model.ProjectContext{
		ID: "project-media-summary", Mode: model.AppModeDesktop, ProductURL: "https://product.example", TargetAudience: "产品团队",
		Inputs: &model.ProjectInputBundle{},
	}
	summary := projectContextSummaryForPackage(project, &orchestrator.CascadeState{}, "")
	if summary.MediaDeliveryPreferences == nil {
		t.Fatal("media delivery preferences were omitted from the package summary")
	}
	if summary.MediaDeliveryPreferences.Narration.Mode != model.MediaNarrationModeAutoByProductStyle || summary.MediaDeliveryPreferences.TOSRetention.RetentionDays != 30 {
		t.Fatalf("unexpected package policy: %+v", summary.MediaDeliveryPreferences)
	}
}
