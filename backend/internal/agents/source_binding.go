package agents

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
)

type ProductSourceBindingAgent struct{}

func NewProductSourceBindingAgent() *ProductSourceBindingAgent { return &ProductSourceBindingAgent{} }

func (a *ProductSourceBindingAgent) Assess(project *model.ProjectContext, code []model.CodeUnderstandingSnapshot, pages []model.PageUnderstandingSnapshot, now time.Time) (*model.ProductSourceBindingAssessment, []model.CodeUnderstandingSnapshot, error) {
	return AssessProductSourceBinding(project, code, pages, now)
}

func productIdentitySignalsFromCodeCandidates(candidates []codeCandidateFile) []model.ProductIdentitySignal {
	signals := []model.ProductIdentitySignal{}
	for _, candidate := range candidates {
		name := strings.ToLower(candidate.name)
		switch name {
		case "package.json":
			data, err := os.ReadFile(candidate.path)
			if err != nil || len(data) > 256*1024 {
				continue
			}
			var pkg struct {
				Name       string `json:"name"`
				Homepage   string `json:"homepage"`
				Repository any    `json:"repository"`
			}
			if json.Unmarshal(data, &pkg) != nil {
				continue
			}
			signals = appendIdentitySignal(signals, "product_name", "medium", normalizeProductName(pkg.Name), nil)
			signals = appendIdentitySignal(signals, "deployment_origin", "strong", normalizeOrigin(pkg.Homepage), nil)
			signals = appendIdentitySignal(signals, "repository", "strong", normalizeRepositoryIdentity(repositoryURLFromPackageValue(pkg.Repository)), nil)
		case "manifest.json", "site.webmanifest", "manifest.webmanifest":
			data, err := os.ReadFile(candidate.path)
			if err != nil || len(data) > 256*1024 {
				continue
			}
			var manifest struct {
				Name      string `json:"name"`
				ShortName string `json:"short_name"`
				Scope     string `json:"scope"`
				StartURL  string `json:"start_url"`
			}
			if json.Unmarshal(data, &manifest) != nil {
				continue
			}
			signals = appendIdentitySignal(signals, "product_name", "medium", normalizeProductName(firstNonEmpty(manifest.Name, manifest.ShortName)), nil)
			signals = appendIdentitySignal(signals, "deployment_origin", "strong", normalizeOrigin(firstNonEmpty(manifest.Scope, manifest.StartURL)), nil)
		case "cname":
			data, err := os.ReadFile(candidate.path)
			if err == nil && len(data) <= 4096 {
				signals = appendIdentitySignal(signals, "deployment_origin", "strong", normalizeOrigin(strings.TrimSpace(string(data))), nil)
			}
		}
	}
	return uniqueIdentitySignals(signals)
}

func productIdentitySignalsForPage(rawURL, title string, evidence []model.EvidenceRef) []model.ProductIdentitySignal {
	signals := []model.ProductIdentitySignal{}
	origin := normalizeOrigin(rawURL)
	signals = appendIdentitySignal(signals, "deployment_origin", "strong", origin, evidence)
	if parsed, err := url.Parse(origin); err == nil {
		host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
		if parts := strings.Split(host, "."); len(parts) > 0 {
			signals = appendIdentitySignal(signals, "product_name", "medium", normalizeProductName(parts[0]), evidence)
		}
	}
	if strings.TrimSpace(title) != "" && title != "产品入口" && title != "页面截图" {
		signals = appendIdentitySignal(signals, "product_name", "medium", normalizeProductName(title), evidence)
	}
	return uniqueIdentitySignals(signals)
}

func productIdentitySignalsForPageInput(input model.WebpageScreenshotInput, evidence []model.EvidenceRef) []model.ProductIdentitySignal {
	signals := productIdentitySignalsForPage(input.URL, input.Title, evidence)
	for key, kind := range map[string]string{
		"canonical_url":    "deployment_origin",
		"og_url":           "deployment_origin",
		"repository_url":   "repository",
		"application_name": "product_name",
		"site_name":        "product_name",
	} {
		value, _ := input.Metadata[key].(string)
		normalized := normalizeProductName(value)
		strength := "medium"
		if kind == "deployment_origin" {
			normalized = normalizeOrigin(value)
			strength = "strong"
		} else if kind == "repository" {
			normalized = normalizeRepositoryIdentity(value)
			strength = "strong"
		}
		signals = appendIdentitySignal(signals, kind, strength, normalized, evidence)
	}
	return uniqueIdentitySignals(signals)
}

func AssessProductSourceBinding(project *model.ProjectContext, code []model.CodeUnderstandingSnapshot, pages []model.PageUnderstandingSnapshot, now time.Time) (*model.ProductSourceBindingAssessment, []model.CodeUnderstandingSnapshot, error) {
	assessment := &model.ProductSourceBindingAssessment{
		SchemaVersion:   model.ProductSourceBindingAssessmentSchemaVersion,
		AssessedAt:      now,
		InputHashSHA256: sourceBindingInputHash(project),
	}
	for _, page := range pages {
		assessment.ProductSignals = append(assessment.ProductSignals, page.ProductIdentitySignals...)
	}
	assessment.ProductSignals = uniqueIdentitySignals(assessment.ProductSignals)

	if len(code) == 0 || len(pages) == 0 {
		assessment.Status = model.ProductSourceBindingNotApplicable
		if len(code) == 0 {
			assessment.EffectiveMode = model.ProductSourceModePageOnly
		} else {
			assessment.EffectiveMode = model.ProductSourceModeMixed
		}
		assessment.AssessmentHash = sourceBindingAssessmentHash(assessment)
		return assessment, code, nil
	}

	matchedCode := make([]model.CodeUnderstandingSnapshot, 0, len(code))
	hasMismatch := false
	for _, snapshot := range code {
		item := assessSourceBindingItem(snapshot, assessment.ProductSignals)
		assessment.Sources = append(assessment.Sources, item)
		if item.Status == model.ProductSourceBindingMatched {
			matchedCode = append(matchedCode, snapshot)
		}
		if item.Status == model.ProductSourceBindingMismatched {
			hasMismatch = true
		}
	}
	assessment.AssessmentHash = sourceBindingAssessmentHash(assessment)
	if hasMismatch {
		assessment.Status = model.ProductSourceBindingMismatched
		assessment.EffectiveMode = model.ProductSourceModeBlocked
		if project != nil && project.SourceBinding != nil && project.SourceBinding.Decision == "continue_page_only" && project.SourceBinding.AssessmentHash == assessment.AssessmentHash {
			assessment.EffectiveMode = model.ProductSourceModePageOnly
			assessment.Decision = "continue_page_only"
			return assessment, nil, nil
		}
		return assessment, nil, &model.ProductSourceMismatchError{Assessment: assessment}
	}
	if len(matchedCode) > 0 {
		assessment.Status = model.ProductSourceBindingMatched
		assessment.EffectiveMode = model.ProductSourceModeMixed
		return assessment, matchedCode, nil
	}
	// The service validates the submitted assessment hash against the persisted
	// assessment before starting this fresh read. Code evidence selection is
	// intentionally model-guided and may produce a different content digest on
	// the rerun, so the new assessment hash is not a stable confirmation token.
	// A detected identity mismatch is still handled and blocked above.
	if project != nil && project.SourceBinding != nil && project.SourceBinding.Decision == "confirm_mixed" && project.SourceBinding.AssessmentHash != "" {
		assessment.Status = model.ProductSourceBindingConfirmed
		assessment.EffectiveMode = model.ProductSourceModeMixed
		assessment.Decision = "confirm_mixed"
		return assessment, append([]model.CodeUnderstandingSnapshot{}, code...), nil
	}
	assessment.Status = model.ProductSourceBindingUnverified
	assessment.EffectiveMode = model.ProductSourceModePageOnly
	return assessment, nil, nil
}

func assessSourceBindingItem(snapshot model.CodeUnderstandingSnapshot, productSignals []model.ProductIdentitySignal) model.ProductSourceBindingItem {
	item := model.ProductSourceBindingItem{
		SourceRefID: "source_" + shortHash(snapshot.ID+snapshot.SourceRefHashSHA256+snapshot.SourceDigestSHA256),
		Status:      model.ProductSourceBindingUnverified,
		Signals:     append([]model.ProductIdentitySignal{}, snapshot.ProductIdentitySignals...),
	}
	productByKind := identitySignalHashesByKind(productSignals)
	sourceByKind := identitySignalHashesByKind(snapshot.ProductIdentitySignals)
	for kind, sourceValues := range sourceByKind {
		if intersectsIdentityHashes(sourceValues, productByKind[kind]) {
			item.MatchedKinds = append(item.MatchedKinds, kind)
		} else if len(productByKind[kind]) > 0 {
			item.ConflictingKinds = append(item.ConflictingKinds, kind)
		}
	}
	sort.Strings(item.MatchedKinds)
	sort.Strings(item.ConflictingKinds)
	if sourceBindingContainsString(item.MatchedKinds, "deployment_origin") || sourceBindingContainsString(item.MatchedKinds, "repository") {
		item.Status = model.ProductSourceBindingMatched
	} else if sourceBindingContainsString(item.ConflictingKinds, "deployment_origin") && sourceBindingContainsString(item.ConflictingKinds, "product_name") {
		item.Status = model.ProductSourceBindingMismatched
	}
	return item
}

func sourceBindingAssessmentHash(value *model.ProductSourceBindingAssessment) string {
	if value == nil {
		return ""
	}
	payload := struct {
		SchemaVersion string                           `json:"schema_version"`
		InputHash     string                           `json:"input_hash"`
		Product       []model.ProductIdentitySignal    `json:"product"`
		Sources       []model.ProductSourceBindingItem `json:"sources"`
	}{value.SchemaVersion, value.InputHashSHA256, value.ProductSignals, value.Sources}
	digest, _ := model.DigestCanonicalJSON(payload)
	return digest
}

func sourceBindingInputHash(project *model.ProjectContext) string {
	if project == nil {
		return hashString("")
	}
	return hashString(strings.TrimSpace(project.ProductURL))
}

func appendIdentitySignal(out []model.ProductIdentitySignal, kind, strength, normalized string, evidence []model.EvidenceRef) []model.ProductIdentitySignal {
	if normalized == "" {
		return out
	}
	return append(out, model.ProductIdentitySignal{Kind: kind, Strength: strength, ValueSHA256: hashString(normalized), EvidenceRefs: evidence})
}

func uniqueIdentitySignals(values []model.ProductIdentitySignal) []model.ProductIdentitySignal {
	out := []model.ProductIdentitySignal{}
	seen := map[string]bool{}
	for _, value := range values {
		key := value.Kind + "|" + value.ValueSHA256
		if value.Kind == "" || value.ValueSHA256 == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind+out[i].ValueSHA256 < out[j].Kind+out[j].ValueSHA256 })
	return out
}

func identitySignalHashesByKind(values []model.ProductIdentitySignal) map[string][]string {
	out := map[string][]string{}
	for _, value := range values {
		out[value.Kind] = append(out[value.Kind], value.ValueSHA256)
	}
	return out
}

func intersectsIdentityHashes(left, right []string) bool {
	set := map[string]bool{}
	for _, value := range left {
		set[value] = true
	}
	for _, value := range right {
		if set[value] {
			return true
		}
	}
	return false
}

func sourceBindingContainsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func normalizeOrigin(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if !strings.Contains(value, "://") {
		value = "https://" + strings.TrimPrefix(value, "//")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	if port := parsed.Port(); port != "" {
		host += ":" + port
	}
	return strings.ToLower(parsed.Scheme) + "://" + host
}

func normalizeProductName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "@")
	if slash := strings.LastIndex(value, "/"); slash >= 0 {
		value = value[slash+1:]
	}
	return strings.Join(strings.FieldsFunc(value, func(r rune) bool { return r == '-' || r == '_' || r == ' ' || r == '.' }), "")
}

func normalizeRepositoryIdentity(value string) string {
	value = strings.TrimSpace(strings.TrimSuffix(value, ".git"))
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	return strings.ToLower(strings.TrimPrefix(parsed.Hostname()+parsed.Path, "www."))
}

func repositoryURLFromPackageValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case map[string]any:
		if raw, ok := typed["url"].(string); ok {
			return raw
		}
	}
	return ""
}

func baseNameOnly(path string) string { return filepath.Base(filepath.Clean(path)) }
