package agents

import "cascade-demoops/backend/internal/model"

const archetypeProfileVersion = "1.0.0"

// archetypeProfile contains only structural signals and outcome templates.
// Hostnames, route literals, selectors, product names, and business copy are
// deliberately absent so profile selection is stable across sites.
type archetypeProfile struct {
	ID               model.ProductArchetype
	Version          string
	RequiredSignals  []string
	OutcomeTemplates []string
	Score            func(archetypeSignals) int
}

type archetypeSignals struct {
	Actions      map[model.GraphActionType]int
	Progress     int
	FinalObserve int
	VisualChange int
	FrameChange  int
}

var productArchetypeProfiles = []archetypeProfile{
	{ID: model.ProductArchetypeInteractive, Version: archetypeProfileVersion, RequiredSignals: []string{"bounded input action", "runtime region change"}, OutcomeTemplates: []string{"frame_surface_changed", "visual_region_changed"}, Score: func(value archetypeSignals) int {
		return value.Actions[model.GraphActionPress]*300 + value.FrameChange*60 + value.VisualChange*30
	}},
	{ID: model.ProductArchetypeAsyncBuilder, Version: archetypeProfileVersion, RequiredSignals: []string{"submit action", "progress observation", "terminal result"}, OutcomeTemplates: []string{"network_settled", "dom_changed", "url_matches"}, Score: func(value archetypeSignals) int {
		if value.Progress == 0 || value.FinalObserve == 0 {
			return 0
		}
		return value.Progress*45 + value.FinalObserve*25 + (value.Actions[model.GraphActionClick]+value.Actions[model.GraphActionAPICall])*20
	}},
	{ID: model.ProductArchetypeCRUDForm, Version: archetypeProfileVersion, RequiredSignals: []string{"field mutation", "commit action", "result observation"}, OutcomeTemplates: []string{"value_equals", "dom_changed", "aria_changed"}, Score: func(value archetypeSignals) int {
		mutations := value.Actions[model.GraphActionFill] + value.Actions[model.GraphActionSelect]
		if mutations < 2 || value.Actions[model.GraphActionClick] == 0 {
			return 0
		}
		return mutations*35 + value.Actions[model.GraphActionClick]*20 + value.FinalObserve*10
	}},
	{ID: model.ProductArchetypeCanvasEditor, Version: archetypeProfileVersion, RequiredSignals: []string{"multiple direct manipulations", "visual result"}, OutcomeTemplates: []string{"visual_region_changed", "frame_surface_changed"}, Score: func(value archetypeSignals) int {
		if value.Actions[model.GraphActionClick] < 2 || value.FinalObserve == 0 {
			return 0
		}
		return value.Actions[model.GraphActionClick]*22 + value.VisualChange*35 + value.FrameChange*35
	}},
	{ID: model.ProductArchetypeDashboard, Version: archetypeProfileVersion, RequiredSignals: []string{"observation-dominant flow"}, OutcomeTemplates: []string{"dom_changed", "aria_changed", "network_settled"}, Score: func(value archetypeSignals) int {
		observations := value.Actions[model.GraphActionInspect] + value.Actions[model.GraphActionWait]
		mutations := value.Actions[model.GraphActionFill] + value.Actions[model.GraphActionSelect] + value.Actions[model.GraphActionPress]
		if observations == 0 || observations <= mutations {
			return 0
		}
		return observations*24 + value.FinalObserve*15
	}},
}

func routeProductArchetype(steps []model.ScriptStep) model.ProductArchetype {
	signals := archetypeSignals{Actions: map[model.GraphActionType]int{}}
	for _, step := range steps {
		signals.Actions[step.Action.Type]++
		switch step.StageKind {
		case model.BusinessStageKindObserveProgress:
			signals.Progress++
		case model.BusinessStageKindFinalObserve:
			signals.FinalObserve++
		}
		for _, validation := range step.Validations {
			switch validation.Kind {
			case "visual_region_changed":
				signals.VisualChange++
			case "frame_surface_changed":
				signals.FrameChange++
			}
		}
	}
	bestID, bestScore, tied := model.ProductArchetypeUnknown, 0, false
	for _, profile := range productArchetypeProfiles {
		score := profile.Score(signals)
		if score > bestScore {
			bestID, bestScore, tied = profile.ID, score, false
		} else if score > 0 && score == bestScore {
			tied = true
		}
	}
	if tied {
		return model.ProductArchetypeUnknown
	}
	return bestID
}
