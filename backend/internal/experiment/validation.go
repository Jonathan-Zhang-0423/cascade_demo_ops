package experiment

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

var targetPromptForbiddenTerms = []string{
	"demoops", "seedance", "ffmpeg", "h3", "视觉轮询", "审计", "selector", "data-testid", "browser worker", "harness",
}

func ValidateCreateRequest(request CreateRunRequest) error {
	if strings.TrimSpace(request.DefinitionRef) == "" || strings.TrimSpace(request.CredentialRef) == "" || strings.TrimSpace(request.AuthorizationRef) == "" || len(strings.TrimSpace(request.IdempotencyKey)) < 8 {
		return errors.New("definition_ref, credential_ref, authorization_ref, and idempotency_key are required")
	}
	parsed, err := url.Parse(strings.TrimSpace(request.TargetURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("target_url must be an http(s) URL without credentials or fragment")
	}
	if strings.Contains(request.DefinitionRef, "..") || strings.ContainsAny(request.DefinitionRef, `/\\`) {
		return errors.New("definition_ref must be a single registered identifier")
	}
	return nil
}

func ValidateDefinition(value Definition) error {
	if value.SchemaVersion != DefinitionSchemaVersion || strings.TrimSpace(value.DefinitionID) == "" || value.WorkflowTemplateID != WorkflowTemplateAsyncProductDemo {
		return errors.New("unsupported experiment definition identity or workflow template")
	}
	if len(value.RunSet) != 2 || value.RunSet[0] != "main" || value.RunSet[1] != "recovery" || value.RecoveryInjectionPhase != "once_effect_committed" {
		return errors.New("experiment definition must declare main and recovery legs with committed once-effect recovery")
	}
	if value.MainTargetDurationMS.Min != 100_000 || value.MainTargetDurationMS.Max != 110_000 {
		return errors.New("experiment main target duration must be 100-110 seconds")
	}
	budget := value.AuthorizationBudget
	if budget.TargetSubmissions != 2 || budget.FinalFilmJobs != 1 || budget.ProviderCalls != 8 || budget.VisualCallsPerRun != 12 {
		return errors.New("experiment authorization budget is not frozen")
	}
	if strings.TrimSpace(value.MainProjectName) == "" || strings.TrimSpace(value.RecoveryProjectName) == "" || value.MainProjectName == value.RecoveryProjectName {
		return errors.New("main and recovery project names must be distinct")
	}
	for _, ref := range []string{value.ProductSpecRef, value.ObservationPlanRef, value.InteractionPlanRef} {
		if !safeRelativeJSONRef(ref) {
			return errors.New("experiment artifact refs must be sibling JSON files")
		}
	}
	return nil
}

func ValidateProductSpec(value ProductSpec) error {
	if value.SchemaVersion != ProductSpecSchemaVersion || strings.TrimSpace(value.SpecID) == "" || strings.TrimSpace(value.Title) == "" || len([]rune(strings.TrimSpace(value.Objective))) < 10 {
		return errors.New("product spec identity, title, and objective are required")
	}
	if len(value.Requirements) < 4 || len(value.InteractionRequirements) < 2 || len(value.ObservableAcceptance) < 4 || len(value.ForbiddenOutcomes) == 0 {
		return errors.New("product spec lacks functional, interaction, observable, or forbidden outcome coverage")
	}
	if strings.TrimSpace(value.VisualDirection.Theme) == "" || len(value.VisualDirection.Palette) < 2 || strings.TrimSpace(value.VisualDirection.Motion) == "" {
		return errors.New("product spec visual direction is incomplete")
	}
	seen := map[string]bool{}
	for _, requirement := range append(append([]ProductRequirement{}, value.Requirements...), value.InteractionRequirements...) {
		if strings.TrimSpace(requirement.ID) == "" || seen[requirement.ID] || strings.TrimSpace(requirement.Statement) == "" || (requirement.Priority != "must" && requirement.Priority != "should") {
			return errors.New("product spec requirements contain an invalid or duplicate item")
		}
		seen[requirement.ID] = true
	}
	for _, criterion := range value.ObservableAcceptance {
		if strings.TrimSpace(criterion.ID) == "" || seen[criterion.ID] || strings.TrimSpace(criterion.Statement) == "" || len(criterion.EvidenceKinds) == 0 {
			return errors.New("product spec acceptance criteria contain an invalid or duplicate item")
		}
		seen[criterion.ID] = true
	}
	return nil
}

func ValidateObservationPlan(value ObservationPlan) error {
	if value.SchemaVersion != ObservationPlanSchemaVersion || strings.TrimSpace(value.PlanID) == "" || value.InitialPhase != "request_submitted" || value.TerminalPhase != "preview_ready" {
		return errors.New("observation plan identity or terminal phases are invalid")
	}
	if value.HeartbeatMS < 30_000 || value.HeartbeatMS > 90_000 || value.WarnAfterMS < 60_000 || value.DeferAfterMS <= value.WarnAfterMS || value.DeferAfterMS > 1_800_000 || value.MaxVisualCalls < 1 || value.MaxVisualCalls > 24 || value.RequiredTerminalChannels < 2 {
		return errors.New("observation plan polling, timeout, or terminal-channel budget is invalid")
	}
	if len(value.Transitions) < 4 {
		return errors.New("observation plan requires a complete transition graph")
	}
	for _, transition := range value.Transitions {
		if !validObservationPhase(transition.From) || !validObservationPhase(transition.To) || len(transition.EvidenceKinds) == 0 {
			return errors.New("observation plan contains an invalid transition")
		}
	}
	return nil
}

func ValidateInteractionPlan(value InteractionPlan) error {
	if value.SchemaVersion != InteractionPlanSchemaVersion || strings.TrimSpace(value.PlanID) == "" || len(value.Steps) < 2 {
		return errors.New("interaction evidence plan identity and steps are required")
	}
	if value.SurfaceKind != "dom" && value.SurfaceKind != "iframe" && value.SurfaceKind != "canvas" && value.SurfaceKind != "runtime_discovered" {
		return errors.New("interaction evidence plan surface_kind is invalid")
	}
	seen := map[string]bool{}
	for _, step := range value.Steps {
		if strings.TrimSpace(step.StepID) == "" || seen[step.StepID] || strings.TrimSpace(step.SemanticIntent) == "" || len(step.ExpectedChanges) == 0 || len(step.EvidenceSlots) == 0 || len(step.ProofRequirements) == 0 {
			return errors.New("interaction evidence step is incomplete or duplicated")
		}
		if step.ReplayPolicy != ReplayObserveOnly && step.ReplayPolicy != ReplayIdempotentWrite && step.ReplayPolicy != ReplayOnceEffect {
			return errors.New("interaction evidence step has an invalid replay policy")
		}
		for _, proof := range step.ProofRequirements {
			if !validProofRequirement(proof) {
				return errors.New("interaction evidence step has an invalid proof requirement")
			}
		}
		seen[step.StepID] = true
	}
	return nil
}

func validProofRequirement(value ProofRequirement) bool {
	switch value.Kind {
	case "all_evidence_slots", "region_changed", "numeric_increase":
		return value.MinCount == 0 && value.Modality == "" && value.MinSimilarity == 0
	case "distinct_actions", "state_variants":
		return value.MinCount >= 1 && value.MinCount <= 12
	case "approximate_state_restore":
		return value.MinSimilarity >= 0.5 && value.MinSimilarity <= 1
	case "input_modality":
		return value.Modality == "keyboard" || value.Modality == "pointer" || value.Modality == "touch"
	default:
		return false
	}
}

func ValidateRun(value Run) error {
	if value.SchemaVersion != RunSchemaVersion || strings.TrimSpace(value.RunID) == "" || value.WorkflowTemplate != WorkflowTemplateAsyncProductDemo || value.Revision < 1 || value.CreatedAt.IsZero() || value.UpdatedAt.IsZero() {
		return errors.New("experiment run identity, workflow, revision, and timestamps are required")
	}
	if !validRunState(value.State) || len(value.Legs) != 2 || value.Legs[0].Kind != "main" || value.Legs[1].Kind != "recovery" {
		return errors.New("experiment run state or legs are invalid")
	}
	if value.ProviderCallsUsed < 0 || value.ProviderCallsUsed > value.Budget.ProviderCalls {
		return errors.New("experiment provider-call budget exceeded")
	}
	for _, leg := range value.Legs {
		if leg.VisualCallsUsed > value.Budget.VisualCallsPerRun || leg.TargetSubmissions > 1 || !validRunState(leg.State) {
			return errors.New("experiment leg exceeded visual or once-effect budget")
		}
	}
	if value.State == RunStateWaitingInput && value.Waiting == nil {
		return errors.New("waiting_input run requires waiting details")
	}
	return nil
}

func CompileBuildPrompt(value ProductSpec, projectName string) (string, error) {
	if err := ValidateProductSpec(value); err != nil {
		return "", err
	}
	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		return "", errors.New("project name is required")
	}
	lines := []string{
		fmt.Sprintf("请直接构建并完成一款名为《%s》的可运行网页产品。", projectName),
		"产品目标：" + strings.TrimSpace(value.Objective),
		"目标受众：" + strings.TrimSpace(value.Audience),
		"核心功能：",
	}
	for _, requirement := range value.Requirements {
		lines = append(lines, "- "+strings.TrimSpace(requirement.Statement))
	}
	lines = append(lines, "交互要求：")
	for _, requirement := range value.InteractionRequirements {
		lines = append(lines, "- "+strings.TrimSpace(requirement.Statement))
	}
	lines = append(lines,
		"视觉方向："+strings.TrimSpace(value.VisualDirection.Theme)+"；配色："+strings.Join(value.VisualDirection.Palette, "、")+"；动效："+strings.TrimSpace(value.VisualDirection.Motion)+"。",
	)
	if len(value.ResponsiveRequirements) > 0 {
		lines = append(lines, "响应式与可用性：")
		for _, requirement := range value.ResponsiveRequirements {
			lines = append(lines, "- "+strings.TrimSpace(requirement))
		}
	}
	lines = append(lines, "可观察验收：")
	for _, criterion := range value.ObservableAcceptance {
		if criterion.Required {
			lines = append(lines, "- "+strings.TrimSpace(criterion.Statement))
		}
	}
	for _, outcome := range value.ForbiddenOutcomes {
		if !containsPromptForbiddenTerm(outcome) {
			lines = append(lines, "不得出现："+strings.TrimSpace(outcome))
		}
	}
	lines = append(lines, "不要只输出计划、分析、说明或静态占位页；请直接实现并在当前预览中提供完整可交互结果。")
	prompt := strings.Join(lines, "\n")
	if containsPromptForbiddenTerm(prompt) {
		return "", errors.New("compiled target prompt contains an internal harness term")
	}
	if len([]rune(prompt)) > 6000 {
		return "", errors.New("compiled target prompt exceeds the allowed size")
	}
	return prompt, nil
}

type LoadedDefinition struct {
	Definition      Definition
	ProductSpec     ProductSpec
	ObservationPlan ObservationPlan
	InteractionPlan InteractionPlan
}

func LoadDefinition(root, ref string) (LoadedDefinition, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	ref = strings.TrimSpace(ref)
	if root == "" || root == "." || ref == "" || strings.Contains(ref, "..") || strings.ContainsAny(ref, `/\\`) {
		return LoadedDefinition{}, errors.New("experiment root and safe definition ref are required")
	}
	directory := filepath.Join(root, ref)
	var loaded LoadedDefinition
	if err := readJSONFile(filepath.Join(directory, "definition.json"), &loaded.Definition); err != nil {
		return LoadedDefinition{}, err
	}
	if err := ValidateDefinition(loaded.Definition); err != nil {
		return LoadedDefinition{}, err
	}
	if err := readJSONFile(filepath.Join(directory, loaded.Definition.ProductSpecRef), &loaded.ProductSpec); err != nil {
		return LoadedDefinition{}, err
	}
	if err := readJSONFile(filepath.Join(directory, loaded.Definition.ObservationPlanRef), &loaded.ObservationPlan); err != nil {
		return LoadedDefinition{}, err
	}
	if err := readJSONFile(filepath.Join(directory, loaded.Definition.InteractionPlanRef), &loaded.InteractionPlan); err != nil {
		return LoadedDefinition{}, err
	}
	if err := ValidateProductSpec(loaded.ProductSpec); err != nil {
		return LoadedDefinition{}, err
	}
	if err := ValidateObservationPlan(loaded.ObservationPlan); err != nil {
		return LoadedDefinition{}, err
	}
	if err := ValidateInteractionPlan(loaded.InteractionPlan); err != nil {
		return LoadedDefinition{}, err
	}
	return loaded, nil
}

func readJSONFile(path string, target any) error {
	payload, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func safeRelativeJSONRef(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && filepath.Ext(value) == ".json" && filepath.Base(value) == value && !strings.Contains(value, "..")
}

func containsPromptForbiddenTerm(value string) bool {
	lower := strings.ToLower(value)
	for _, forbidden := range targetPromptForbiddenTerms {
		if strings.Contains(lower, strings.ToLower(forbidden)) {
			return true
		}
	}
	return false
}

func validObservationPhase(value string) bool {
	switch value {
	case "request_submitted", "plan_ready", "execution_active", "preview_candidate", "preview_ready", "blocked", "terminal_failed", "unknown":
		return true
	default:
		return false
	}
}

func validRunState(value RunState) bool {
	switch value {
	case RunStateCreated, RunStateAdmitted, RunStateQueued, RunStateRunning, RunStateWaitingInput, RunStateWaitingExternal, RunStateSucceeded, RunStateFailed, RunStateCanceled, RunStateExpired:
		return true
	default:
		return false
	}
}
