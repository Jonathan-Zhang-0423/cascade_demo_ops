package app

import (
	"context"
	"errors"

	"cascade-demoops/backend/internal/model"
)

// MediaDeliveryPolicyView is safe for the client to display. Credentials,
// provider endpoints, and any signed URLs are deliberately absent.
type MediaDeliveryPolicyView struct {
	Preferences            model.MediaDeliveryPreferences `json:"preferences"`
	ClientFeedbackRequired bool                           `json:"client_feedback_required"`
	RequiredFeedbackFields []string                       `json:"required_feedback_fields,omitempty"`
}

func (s *Service) GetMediaDeliveryPolicy(ctx context.Context, projectID string) (MediaDeliveryPolicyView, error) {
	state, err := s.states.Load(ctx, projectID)
	if err != nil {
		return MediaDeliveryPolicyView{}, err
	}
	if state.ProjectContext == nil {
		return MediaDeliveryPolicyView{}, errors.New("project context is missing")
	}
	return mediaDeliveryPolicyView(state.ProjectContext), nil
}

func (s *Service) SaveMediaDeliveryPolicy(ctx context.Context, projectID string, input model.MediaDeliveryPreferences) (MediaDeliveryPolicyView, error) {
	preferences := model.NormalizeMediaDeliveryPreferences(&input)
	if err := model.ValidateMediaDeliveryPreferences(&preferences); err != nil {
		return MediaDeliveryPolicyView{}, err
	}
	state, err := s.states.Load(ctx, projectID)
	if err != nil {
		return MediaDeliveryPolicyView{}, err
	}
	if state.ProjectContext == nil {
		return MediaDeliveryPolicyView{}, errors.New("project context is missing")
	}
	if state.ProjectContext.Inputs == nil {
		state.ProjectContext.Inputs = &model.ProjectInputBundle{}
	}
	state.ProjectContext.Inputs.MediaDeliveryPreferences = &preferences
	if err := s.states.Save(ctx, state); err != nil {
		return MediaDeliveryPolicyView{}, err
	}
	return mediaDeliveryPolicyView(state.ProjectContext), nil
}

func mediaDeliveryPolicyView(project *model.ProjectContext) MediaDeliveryPolicyView {
	var configured *model.MediaDeliveryPreferences
	if project != nil && project.Inputs != nil {
		configured = project.Inputs.MediaDeliveryPreferences
	}
	preferences := model.NormalizeMediaDeliveryPreferences(configured)
	view := MediaDeliveryPolicyView{Preferences: preferences}
	if !preferences.TOSRetention.ClientDisclosureAcknowledged {
		view.ClientFeedbackRequired = true
		view.RequiredFeedbackFields = []string{"tos_retention.client_disclosure_acknowledged"}
	}
	return view
}
