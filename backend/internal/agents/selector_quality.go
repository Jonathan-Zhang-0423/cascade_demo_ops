package agents

import (
	"regexp"
	"strings"

	"cascade-demoops/backend/internal/model"
)

var whitespacePattern = regexp.MustCompile(`\s+`)

func selectorQualityScore(selector string) int {
	value := normalizeSelector(selector)
	if value == "" {
		return 0
	}
	if selectorLooksGeneric(value) {
		return 5
	}
	score := 20
	switch {
	case containsAny(value, "data-testid", "data-test", "data-cy", "data-qa", "data-automation"):
		score = 100
	case containsAny(value, "getbyrole", "[role=", "aria-label", "aria-labelledby"):
		score = 90
	case containsAny(value, "has-text", "text=", "button:", "a:"):
		score = 82
	case containsAny(value, "placeholder=", "name=", "label=", "input[", "textarea[", "select["):
		score = 76
	case strings.HasPrefix(value, "#") && !containsAny(value, "#root", "#app", "#main"):
		score = 68
	case containsAny(value, "[href=", "[type=", "[title="):
		score = 64
	case containsAny(value, "."):
		score = 42
	}
	if containsAny(value, "nth-child", "nth-of-type", " > div", " div ") {
		score -= 18
	}
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

func selectorUsableForBusinessAction(selector string) bool {
	return selectorQualityScore(selector) >= 60 && !selectorLooksGeneric(selector) && !selectorLooksLikeChromeControl(selector) && !selectorLooksReadOnlySurface(selector)
}

func selectorUsableForBlockingAssertion(selector string) bool {
	return selectorQualityScore(selector) >= 50 && !selectorLooksGeneric(selector)
}

func selectorLooksGeneric(selector string) bool {
	value := normalizeSelector(selector)
	if value == "" {
		return false
	}
	switch value {
	case "html", "body", "main", "section", "article", "header", "footer", "nav", "aside", "div", "form", "button", "input", "textarea", "select", "#root", "#app", "#main", "[id='root']", "[id=\"root\"]", "[id='app']", "[id=\"app\"]":
		return true
	}
	if strings.HasPrefix(value, "body ") || strings.HasPrefix(value, "main ") {
		return !containsAny(value, "data-testid", "data-test", "data-cy", "data-qa", "role=", "aria-label", "has-text", "text=", "name=", "placeholder=")
	}
	return false
}

func selectorLooksLikeChromeControl(selector string) bool {
	value := normalizeSelector(selector)
	if value == "" {
		return false
	}
	if !containsAny(value,
		"sidebar", "side-bar", "toggle", "collapse", "expand", "hamburger",
		"menu", "nav", "navigation", "breadcrumb", "header", "footer",
		"theme", "avatar", "profile", "account-menu", "dropdown", "drawer",
		"layout", "shell", "chrome",
		"侧边栏", "菜单", "导航", "面包屑", "页头", "页脚", "主题", "头像", "个人资料", "账户", "抽屉", "折叠", "展开",
	) {
		return false
	}
	return !containsAny(value,
		"create", "new", "project", "invite", "submit", "save", "generate",
		"build", "upload", "search", "send", "run", "start", "confirm",
	)
}

func actionLooksLikeChromeControl(label string, selector string) bool {
	joined := normalizeSelector(label + " " + selector)
	if joined == "" {
		return false
	}
	if !containsAny(joined,
		"sidebar", "side bar", "toggle", "collapse", "expand", "hamburger",
		"menu", "nav", "navigation", "breadcrumb", "header", "footer",
		"theme", "avatar", "profile", "account menu", "dropdown", "drawer",
		"layout", "shell", "chrome",
		"侧边栏", "菜单", "导航", "面包屑", "页头", "页脚", "主题", "头像", "个人资料", "账户", "抽屉", "折叠", "展开",
	) {
		return false
	}
	return !containsAny(joined,
		"create", "new", "project", "invite", "submit", "save", "generate",
		"build", "upload", "search", "send", "run", "start", "confirm",
	)
}

func actionLooksUnsafeOrOffIntent(label string, selector string) bool {
	joined := normalizeIntentText(label + " " + selector)
	if joined == "" {
		return false
	}
	if containsAnyNormalized(joined,
		"cancel", "取消", "delete", "删除", "remove", "移除", "stop", "停止",
		"bulk delete", "批量删除", "regenerate cancel", "取消重新生成",
	) {
		return true
	}
	if containsAnyNormalized(joined, "rename", "重命名") &&
		containsAnyNormalized(joined, "confirm", "确认", "submit", "提交") {
		return true
	}
	return false
}

func actionAllowedForIntentEvidence(label string, selector string) bool {
	return actionAllowedForIntentEvidenceForIntent(label, selector, "")
}

func actionAllowedForIntentEvidenceForIntent(label string, selector string, intentText string) bool {
	if actionLooksLikeChromeControl(label, selector) || actionLooksUnsafeOrOffIntent(label, selector) {
		return false
	}
	intent := normalizeIntentText(intentText)
	joined := normalizeIntentText(label + " " + selector)
	if intent == "" || joined == "" {
		return true
	}
	switch {
	case containsAnyNormalized(joined, "regenerate", "重新生成"):
		return containsAnyNormalized(intent, "regenerate", "重新生成")
	case containsAnyNormalized(joined, "revise", "修订", "修改计划", "改计划"):
		return containsAnyNormalized(intent, "revise", "修订", "修改计划", "改计划")
	case containsAnyNormalized(joined, "polish", "润色"):
		return containsAnyNormalized(intent, "polish", "润色")
	case containsAnyNormalized(joined, "approve", "审批", "批准"):
		return containsAnyNormalized(intent, "approve", "审批", "批准", "验收")
	}
	return true
}

func projectIntelligenceIntentText(intelligence *model.ProjectIntelligencePack) string {
	if intelligence == nil || intelligence.DemoIntent == nil {
		return ""
	}
	parts := []string{intelligence.DemoIntent.Objective, intelligence.DemoIntent.TargetAudience}
	parts = append(parts, intelligence.DemoIntent.ForbiddenTopics...)
	for _, goal := range intelligence.DemoIntent.Goals {
		parts = append(parts, goal.Label, goal.Kind, goal.PreferredAction, goal.TargetPageHint, goal.SuccessState)
		parts = append(parts, goal.TargetKeywords...)
	}
	return strings.Join(parts, " ")
}

func projectIntentText(project *model.ProjectContext, intelligence *model.ProjectIntelligencePack) string {
	if project != nil {
		if text := strings.TrimSpace(project.ProductDescription); text != "" {
			return text
		}
	}
	return projectIntelligenceIntentText(intelligence)
}

func businessActionNeedsExecutableSelector(action model.GraphActionType) bool {
	switch action {
	case model.GraphActionClick, model.GraphActionFill, model.GraphActionSelect, model.GraphActionUpload:
		return true
	default:
		return false
	}
}

func approvedKeyboardKeys(parameters map[string]any) []string {
	raw, ok := parameters["keys"]
	if !ok {
		return nil
	}
	keys := []string{}
	switch value := raw.(type) {
	case string:
		keys = strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' || r == ' ' })
	case []string:
		keys = append(keys, value...)
	case []any:
		for _, item := range value {
			text, ok := item.(string)
			if !ok {
				return nil
			}
			keys = append(keys, text)
		}
	default:
		return nil
	}
	if len(keys) == 0 || len(keys) > 8 {
		return nil
	}
	allowed := map[string]bool{"ArrowLeft": true, "ArrowRight": true, "ArrowDown": true, "ArrowUp": true}
	for index := range keys {
		keys[index] = strings.TrimSpace(keys[index])
		if !allowed[keys[index]] {
			return nil
		}
	}
	return keys
}

func normalizeSelector(selector string) string {
	value := strings.TrimSpace(strings.ToLower(selector))
	value = strings.Trim(value, "`")
	value = whitespacePattern.ReplaceAllString(value, " ")
	return value
}

func bestSelectorCandidate(candidates []model.SelectorCandidate) string {
	bestValue := ""
	bestScore := -1
	for _, candidate := range candidates {
		score := selectorQualityScore(candidate.Value)
		if candidate.StabilityScore > 0 {
			score += int(candidate.StabilityScore * 10)
		}
		if candidate.Confidence > 0 {
			score += int(candidate.Confidence * 5)
		}
		if score > bestScore {
			bestScore = score
			bestValue = candidate.Value
		}
	}
	if selectorLooksGeneric(bestValue) {
		return ""
	}
	return bestValue
}

func bestSelectorValue(values ...string) string {
	bestValue := ""
	bestScore := -1
	for _, value := range values {
		score := selectorQualityScore(value)
		if score > bestScore {
			bestScore = score
			bestValue = value
		}
	}
	if selectorLooksGeneric(bestValue) {
		return ""
	}
	return bestValue
}
