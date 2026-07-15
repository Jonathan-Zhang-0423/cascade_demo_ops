package agents

import (
	"net/url"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
)

func runIntentScopeForProject(project *model.ProjectContext) *model.RunIntentScope {
	if project == nil {
		return nil
	}
	origin := originFromURL(project.ProductURL)
	allowed := []string{}
	if origin != "" {
		allowed = append(allowed, origin)
	}
	if project.AccessPolicy != nil {
		for _, domain := range project.AccessPolicy.AllowedDomains {
			if domain == "" {
				continue
			}
			if strings.Contains(domain, "://") {
				allowed = append(allowed, strings.TrimRight(domain, "/"))
			} else {
				allowed = append(allowed, "https://"+strings.TrimSpace(domain))
			}
		}
	}
	return &model.RunIntentScope{
		ID:                    "run_scope_" + project.ID,
		ProjectID:             project.ID,
		SchemaVersion:         model.ProjectIntelligencePackSchemaVersion,
		ProductOrigin:         origin,
		ProductURL:            project.ProductURL,
		AllowedOrigins:        uniqueStrings(allowed),
		ForbiddenPathPrefixes: defaultControlPlaneForbiddenPaths(),
		ForbiddenSignals:      defaultControlPlaneForbiddenSignals(),
		CreatedAt:             time.Now().UTC(),
	}
}

func defaultControlPlaneForbiddenPaths() []string {
	return []string{
		"/aigc",
		"/.well-known",
		"/v1",
		"/api/execution-packages",
		"/api/result-packages",
		"/execution-packages",
		"/result-packages",
		"/app-installations",
		"/dev/execution-packages",
	}
}

func defaultControlPlaneForbiddenSignals() []string {
	return []string{
		"aigc",
		"well-known",
		"app-installations",
		"execution-packages",
		"result-packages",
		"cascade-exchange",
		"exchange envelope",
		"cloud exchange",
		"authorization bearer",
	}
}

func originFromURL(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func isURLAllowedByRunScope(scope *model.RunIntentScope, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || scope == nil {
		return true
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return true
	}
	origin := parsed.Scheme + "://" + parsed.Host
	if len(scope.AllowedOrigins) > 0 && !stringInFoldedSet(origin, scope.AllowedOrigins) {
		return false
	}
	return !pathHasForbiddenPrefix(parsed.Path, scope.ForbiddenPathPrefixes)
}

func isControlPlaneSignal(scope *model.RunIntentScope, values ...string) bool {
	signals := defaultControlPlaneForbiddenSignals()
	if scope != nil && len(scope.ForbiddenSignals) > 0 {
		signals = scope.ForbiddenSignals
	}
	joined := strings.ToLower(strings.Join(values, " "))
	if joined == "" {
		return false
	}
	for _, signal := range signals {
		if signal != "" && strings.Contains(joined, strings.ToLower(signal)) {
			return true
		}
	}
	return false
}

func scopedProductURL(project *model.ProjectContext, scope *model.RunIntentScope, candidate string) string {
	if isURLAllowedByRunScope(scope, candidate) && !isControlPlaneSignal(scope, candidate) {
		return candidate
	}
	if scope != nil && isURLAllowedByRunScope(scope, scope.ProductURL) {
		return scope.ProductURL
	}
	if project != nil {
		return project.ProductURL
	}
	return ""
}

func pathHasForbiddenPrefix(path string, prefixes []string) bool {
	normalized := "/" + strings.TrimLeft(strings.ToLower(strings.TrimSpace(path)), "/")
	for _, prefix := range prefixes {
		prefix = "/" + strings.TrimLeft(strings.ToLower(strings.TrimSpace(prefix)), "/")
		if prefix == "/" {
			continue
		}
		if normalized == prefix || strings.HasPrefix(normalized, strings.TrimRight(prefix, "/")+"/") {
			return true
		}
	}
	return false
}

func stringInFoldedSet(value string, values []string) bool {
	value = strings.TrimRight(strings.ToLower(strings.TrimSpace(value)), "/")
	for _, item := range values {
		if value == strings.TrimRight(strings.ToLower(strings.TrimSpace(item)), "/") {
			return true
		}
	}
	return false
}
