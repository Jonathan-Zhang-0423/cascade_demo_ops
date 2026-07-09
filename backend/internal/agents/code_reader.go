package agents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/llm"
	"cascade-demoops/backend/internal/model"
)

type CodeReaderAgent struct {
	MaxFiles     int
	MaxFileBytes int64
	llm          llm.Client
}

func NewCodeReaderAgent() *CodeReaderAgent {
	return &CodeReaderAgent{MaxFiles: 600, MaxFileBytes: 256 * 1024}
}

func NewCodeReaderAgentWithLLM(client llm.Client) *CodeReaderAgent {
	return &CodeReaderAgent{MaxFiles: 600, MaxFileBytes: 256 * 1024, llm: client}
}

func (a *CodeReaderAgent) ReadCode(ctx context.Context, project *model.ProjectContext, brief *model.RequirementBrief) ([]model.CodeUnderstandingSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if project == nil || project.Inputs == nil {
		return nil, nil
	}
	snapshots := make([]model.CodeUnderstandingSnapshot, 0, len(project.Inputs.Code))
	for i, input := range project.Inputs.Code {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		snapshot := model.CodeUnderstandingSnapshot{
			ID:            firstNonEmpty(input.ID, fmt.Sprintf("code_snapshot_%d", i+1)),
			ProjectID:     project.ID,
			SchemaVersion: model.MultimodalUnderstandingReportSchemaVersion,
			RepositoryID:  input.RepositoryID,
			URI:           input.URI,
			Branch:        input.Branch,
			CommitSHA:     input.CommitSHA,
			Summary:       "代码结构摘要已生成；仅保存 hash、路由、组件、选择器和数据模型摘要。",
			CreatedAt:     time.Now().UTC(),
			EvidenceRefs: []model.EvidenceRef{{
				ID:         "ev_code_" + firstNonEmpty(input.ID, fmt.Sprintf("%d", i+1)),
				Kind:       model.EvidenceKindSourceCode,
				Summary:    "代码结构摘要",
				FieldPath:  "inputs.code",
				Confidence: 0.78,
			}},
		}
		if strings.TrimSpace(input.Language) != "" {
			snapshot.Languages = append(snapshot.Languages, input.Language)
		}
		if strings.TrimSpace(input.Framework) != "" {
			snapshot.Frameworks = append(snapshot.Frameworks, input.Framework)
		}
		for _, entrypoint := range input.Entrypoints {
			snapshot.EntrypointHashes = append(snapshot.EntrypointHashes, hashString(entrypoint))
		}
		if input.LocalPath != "" {
			if err := a.scanLocalPath(ctx, input.LocalPath, &snapshot); err != nil {
				snapshot.Summary = "代码路径暂不可扫描，已保留输入摘要和 evidence ref。"
			}
		}
		if snapshot.SourceDigestSHA256 == "" {
			snapshot.SourceDigestSHA256 = digestSnapshotIdentity(input)
		}
		snapshot.Languages = uniqueStrings(snapshot.Languages)
		snapshot.Frameworks = uniqueStrings(snapshot.Frameworks)
		snapshot.EntrypointHashes = uniqueStrings(snapshot.EntrypointHashes)
		trace, err := a.enhanceSnapshotWithLLM(ctx, project, brief, &snapshot)
		if err != nil && !llm.IsDeterministicFallback(err) {
			return nil, err
		}
		if trace != nil {
			snapshot.EvidenceRefs = append(snapshot.EvidenceRefs, model.EvidenceRef{
				ID:         "ev_model_code_" + shortHash(snapshot.ID+trace.Label()),
				Kind:       model.EvidenceKindSourceCode,
				Summary:    "CodeReaderAgent 模型路由：" + trace.Label(),
				FieldPath:  "model_trace.code_reader",
				Confidence: 0.68,
			})
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

type codeReaderLLMOutput struct {
	Summary        string              `json:"summary"`
	Frameworks     flexibleStringSlice `json:"frameworks"`
	HeroComponents flexibleStringSlice `json:"hero_components"`
	RouteNames     []struct {
		Path string `json:"path"`
		Name string `json:"name"`
	} `json:"route_names"`
	SelectorNotes   flexibleStringSlice `json:"selector_notes"`
	SensitiveFields flexibleStringSlice `json:"sensitive_fields"`
	Confidence      float64             `json:"confidence"`
}

func (a *CodeReaderAgent) enhanceSnapshotWithLLM(ctx context.Context, project *model.ProjectContext, brief *model.RequirementBrief, snapshot *model.CodeUnderstandingSnapshot) (*llm.CallTrace, error) {
	if a.llm == nil || snapshot == nil || snapshot.FileCount == 0 {
		return nil, nil
	}
	payload := map[string]any{
		"target_audience": project.TargetAudience,
		"objective":       "",
		"file_count":      snapshot.FileCount,
		"languages":       snapshot.Languages,
		"frameworks":      snapshot.Frameworks,
		"routes":          limitRoutes(snapshot.Routes, 40),
		"components":      limitComponents(snapshot.Components, 40),
		"selectors":       limitSelectors(snapshot.Selectors, 40),
		"api_endpoints":   limitAPIs(snapshot.APIEndpoints, 40),
		"data_models":     limitDataModels(snapshot.DataModels, 30),
		"sensitive_names": snapshot.SensitiveFields,
		"source_digest":   snapshot.SourceDigestSHA256,
	}
	if brief != nil {
		payload["objective"] = brief.Objective
		payload["must_show"] = brief.MustShow
	}
	data, _ := json.Marshal(payload)
	var output codeReaderLLMOutput
	trace, err := a.llm.GenerateJSON(ctx, config.ModelTaskCodeReading, llm.JSONRequest{
		System:       "你是 Cascade DemoOps 的代码阅读 agent。你只能基于已脱敏的代码结构摘要判断产品架构、路由、组件和稳定选择器，不能要求或输出完整源码。",
		User:         string(data),
		SchemaName:   "CodeUnderstandingPatch",
		ResponseHint: "返回字段：summary, frameworks, hero_components, route_names[{path,name}], selector_notes, sensitive_fields, confidence。",
		MaxTokens:    1400,
		Temperature:  0.15,
	}, &output)
	if err != nil {
		return trace, err
	}
	if output.Summary != "" {
		snapshot.Summary = output.Summary + "（仅基于结构摘要，不包含完整源码。）"
	}
	snapshot.Frameworks = uniqueStrings(append(snapshot.Frameworks, stringSlice(output.Frameworks)...))
	for _, routeName := range output.RouteNames {
		for i := range snapshot.Routes {
			if snapshot.Routes[i].Path == routeName.Path && routeName.Name != "" {
				snapshot.Routes[i].Name = routeName.Name
				snapshot.Routes[i].Confidence = maxFloat(snapshot.Routes[i].Confidence, output.Confidence)
			}
		}
	}
	for _, componentName := range stringSlice(output.HeroComponents) {
		for i := range snapshot.Components {
			if strings.EqualFold(snapshot.Components[i].Name, componentName) {
				snapshot.Components[i].ActionLabels = uniqueStrings(append(snapshot.Components[i].ActionLabels, "hero_candidate"))
				snapshot.Components[i].Confidence = maxFloat(snapshot.Components[i].Confidence, output.Confidence)
			}
		}
	}
	for _, sensitive := range stringSlice(output.SensitiveFields) {
		snapshot.SensitiveFields = append(snapshot.SensitiveFields, model.SensitiveFieldFinding{Name: sensitive, Kind: "llm_sensitive_field", Reason: "GLM 基于结构摘要判断该字段需要打码或避开。"})
	}
	return trace, nil
}

func (a *CodeReaderAgent) scanLocalPath(ctx context.Context, root string, snapshot *model.CodeUnderstandingSnapshot) error {
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return nil
	}
	root = filepath.Clean(root)
	pathDigests := make([]model.PathDigest, 0)
	contentDigests := make([]string, 0)
	fileCount := 0
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() {
			if shouldSkipDir(name) && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if fileCount >= a.MaxFiles || !isScannableFile(name) {
			return nil
		}
		fileInfo, err := entry.Info()
		if err != nil || fileInfo.Size() > a.MaxFileBytes {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = name
		}
		pathHash := hashString(filepath.ToSlash(rel))
		contentHash := hashBytes(data)
		kind := codeKindForFile(name)
		language := languageForFile(name)
		fileCount++
		pathDigests = append(pathDigests, model.PathDigest{
			PathHashSHA256: pathHash,
			ContentSHA256:  contentHash,
			Kind:           kind,
			Language:       language,
			Entrypoint:     isEntrypointFile(name, rel),
		})
		contentDigests = append(contentDigests, pathHash+":"+contentHash)
		if language != "" {
			snapshot.Languages = append(snapshot.Languages, language)
		}
		if isEntrypointFile(name, rel) {
			snapshot.EntrypointHashes = append(snapshot.EntrypointHashes, pathHash)
		}
		text := string(data)
		inspectFrameworks(name, text, snapshot)
		inspectRoutes(text, pathHash, snapshot)
		inspectSelectors(text, pathHash, snapshot)
		inspectComponents(name, text, pathHash, snapshot)
		inspectAPIs(text, pathHash, snapshot)
		inspectDataModels(text, pathHash, snapshot)
		inspectSensitiveFields(text, snapshot)
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(contentDigests)
	snapshot.FileCount = fileCount
	snapshot.PathDigests = pathDigests
	snapshot.SourceDigestSHA256 = hashString(strings.Join(contentDigests, "\n"))
	return nil
}

func shouldSkipDir(name string) bool {
	switch strings.ToLower(name) {
	case ".git", "node_modules", "dist", "build", ".next", "coverage", "vendor", "tmp", ".turbo":
		return true
	default:
		return false
	}
}

func isScannableFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".go", ".ts", ".tsx", ".js", ".jsx", ".vue", ".svelte", ".json", ".md", ".sql", ".py", ".rb", ".php", ".rs", ".cs", ".java", ".kt":
		return true
	default:
		return strings.EqualFold(name, "go.mod") || strings.EqualFold(name, "package.json")
	}
}

func languageForFile(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".go":
		return "go"
	case ".ts":
		return "typescript"
	case ".tsx":
		return "tsx"
	case ".js", ".jsx":
		return "javascript"
	case ".vue":
		return "vue"
	case ".svelte":
		return "svelte"
	case ".py":
		return "python"
	case ".sql":
		return "sql"
	default:
		return ""
	}
}

func codeKindForFile(name string) string {
	lower := strings.ToLower(name)
	switch {
	case lower == "package.json" || lower == "go.mod":
		return "manifest"
	case strings.HasSuffix(lower, ".md"):
		return "docs"
	case strings.Contains(lower, "route") || strings.Contains(lower, "router"):
		return "route"
	case strings.Contains(lower, "schema") || strings.Contains(lower, "model"):
		return "data_model"
	default:
		return "source"
	}
}

func isEntrypointFile(name string, rel string) bool {
	lower := strings.ToLower(filepath.ToSlash(rel))
	return strings.EqualFold(name, "main.go") ||
		strings.EqualFold(name, "package.json") ||
		strings.Contains(lower, "/app.") ||
		strings.Contains(lower, "/main.") ||
		strings.Contains(lower, "/index.") ||
		strings.Contains(lower, "/routes/")
}

func inspectFrameworks(name string, text string, snapshot *model.CodeUnderstandingSnapshot) {
	lowerName := strings.ToLower(name)
	lower := strings.ToLower(text)
	if lowerName == "go.mod" {
		snapshot.Frameworks = append(snapshot.Frameworks, "go")
	}
	for _, candidate := range []string{"react", "next", "vite", "vue", "svelte", "wails", "gin", "fiber", "echo", "playwright", "remotion"} {
		if strings.Contains(lower, candidate) {
			snapshot.Frameworks = append(snapshot.Frameworks, candidate)
		}
	}
}

func inspectRoutes(text string, pathHash string, snapshot *model.CodeUnderstandingSnapshot) {
	for _, match := range routePattern.FindAllStringSubmatch(text, 20) {
		if len(match) < 2 {
			continue
		}
		path := match[1]
		if !strings.HasPrefix(path, "/") || strings.Contains(path, " ") || len(path) > 100 {
			continue
		}
		snapshot.Routes = append(snapshot.Routes, model.RouteInsight{
			ID:             safeID("route", path),
			Path:           path,
			SourcePathHash: pathHash,
			Confidence:     0.62,
		})
	}
}

func inspectSelectors(text string, pathHash string, snapshot *model.CodeUnderstandingSnapshot) {
	for _, match := range testIDPattern.FindAllStringSubmatch(text, 30) {
		if len(match) < 2 {
			continue
		}
		value := strings.TrimSpace(match[1])
		if value == "" || len(value) > 80 {
			continue
		}
		selector := "[data-testid='" + value + "']"
		snapshot.Selectors = append(snapshot.Selectors, model.SelectorInsight{
			Kind:               "css",
			Value:              selector,
			FilePathHashSHA256: pathHash,
			StabilityScore:     0.86,
			Confidence:         0.76,
		})
	}
}

func inspectComponents(name string, text string, pathHash string, snapshot *model.CodeUnderstandingSnapshot) {
	for _, match := range componentPattern.FindAllStringSubmatch(text, 12) {
		if len(match) < 2 {
			continue
		}
		componentName := match[1]
		snapshot.Components = append(snapshot.Components, model.ComponentInsight{
			ID:                 safeID("component", componentName),
			Name:               componentName,
			Kind:               "ui_component",
			FilePathHashSHA256: pathHash,
			SelectorHints:      selectorValues(snapshot.Selectors, pathHash),
			Confidence:         0.64,
		})
	}
	if len(snapshot.Components) == 0 && strings.HasSuffix(strings.ToLower(name), ".tsx") {
		snapshot.Components = append(snapshot.Components, model.ComponentInsight{
			ID:                 "component_" + shortHash(pathHash),
			Name:               "UIComponent-" + shortHash(pathHash),
			Kind:               "ui_component",
			FilePathHashSHA256: pathHash,
			SelectorHints:      selectorValues(snapshot.Selectors, pathHash),
			Confidence:         0.45,
		})
	}
}

func inspectAPIs(text string, pathHash string, snapshot *model.CodeUnderstandingSnapshot) {
	for _, match := range apiPattern.FindAllStringSubmatch(text, 20) {
		if len(match) < 2 {
			continue
		}
		path := match[1]
		snapshot.APIEndpoints = append(snapshot.APIEndpoints, model.APIEndpointInsight{
			ID:                 safeID("api", path),
			Path:               path,
			FilePathHashSHA256: pathHash,
			Confidence:         0.58,
		})
	}
}

func inspectDataModels(text string, pathHash string, snapshot *model.CodeUnderstandingSnapshot) {
	for _, match := range dataModelPattern.FindAllStringSubmatch(text, 12) {
		if len(match) < 2 {
			continue
		}
		name := match[1]
		snapshot.DataModels = append(snapshot.DataModels, model.DataModelInsight{
			ID:                   safeID("model", name),
			Name:                 name,
			Kind:                 "type",
			SourcePathHashSHA256: pathHash,
			Confidence:           0.58,
		})
	}
}

func inspectSensitiveFields(text string, snapshot *model.CodeUnderstandingSnapshot) {
	lower := strings.ToLower(text)
	for _, token := range []string{"password", "token", "secret", "api_key", "apikey", "email", "billing"} {
		if strings.Contains(lower, token) {
			snapshot.SensitiveFields = append(snapshot.SensitiveFields, model.SensitiveFieldFinding{
				Name:   token,
				Kind:   "possible_sensitive_field",
				Reason: "代码摘要中检测到可能需要打码或禁用的字段名称。",
			})
		}
	}
}

func selectorValues(selectors []model.SelectorInsight, pathHash string) []string {
	values := []string{}
	for _, selector := range selectors {
		if selector.FilePathHashSHA256 == pathHash {
			values = append(values, selector.Value)
		}
	}
	return uniqueStrings(values)
}

func digestSnapshotIdentity(input model.CodeInput) string {
	return hashString(strings.Join([]string{input.ID, input.Kind, input.URI, input.LocalPath, input.CommitSHA}, "|"))
}

func limitRoutes(values []model.RouteInsight, limit int) []model.RouteInsight {
	if len(values) <= limit {
		return values
	}
	return values[:limit]
}

func limitComponents(values []model.ComponentInsight, limit int) []model.ComponentInsight {
	if len(values) <= limit {
		return values
	}
	return values[:limit]
}

func limitSelectors(values []model.SelectorInsight, limit int) []model.SelectorInsight {
	if len(values) <= limit {
		return values
	}
	return values[:limit]
}

func limitAPIs(values []model.APIEndpointInsight, limit int) []model.APIEndpointInsight {
	if len(values) <= limit {
		return values
	}
	return values[:limit]
}

func limitDataModels(values []model.DataModelInsight, limit int) []model.DataModelInsight {
	if len(values) <= limit {
		return values
	}
	return values[:limit]
}

func maxFloat(left float64, right float64) float64 {
	if right > left {
		return right
	}
	return left
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

var (
	routePattern     = regexp.MustCompile(`["'](\/[A-Za-z0-9_\-\/:{}.*?=&%]+)["']`)
	testIDPattern    = regexp.MustCompile(`(?:data-testid=["']|getByTestId\(["'])([A-Za-z0-9_\-:.]+)`)
	componentPattern = regexp.MustCompile(`(?:function|const|class)\s+([A-Z][A-Za-z0-9_]+)`)
	apiPattern       = regexp.MustCompile(`["'](\/api\/[A-Za-z0-9_\-\/:{}.*?=&%]+)["']`)
	dataModelPattern = regexp.MustCompile(`(?:type|interface|struct)\s+([A-Z][A-Za-z0-9_]+)`)
)
