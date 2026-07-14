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
	return selectorQualityScore(selector) >= 60 && !selectorLooksGeneric(selector)
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

func businessActionNeedsExecutableSelector(action model.GraphActionType) bool {
	switch action {
	case model.GraphActionClick, model.GraphActionFill, model.GraphActionSelect, model.GraphActionUpload:
		return true
	default:
		return false
	}
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
