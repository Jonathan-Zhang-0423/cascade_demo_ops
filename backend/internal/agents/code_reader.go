package agents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/credentialstore"
	"cascade-demoops/backend/internal/llm"
	"cascade-demoops/backend/internal/model"
)

type CodeReaderAgent struct {
	MaxFiles           int
	MaxFileBytes       int64
	CacheRoot          string
	Budget             model.CodeReadBudget
	InvestigationTools *ProjectInvestigationToolSuite
	llm                llm.Client
}

const maxCodeReadTotalFileLimit = 80
const gitSnapshotTimeout = 45 * time.Second

type gitRepositorySnapshotMetadata struct {
	URL          string
	RepositoryID string
	Branch       string
	CommitSHA    string
	SafeLabel    string
}

func NewCodeReaderAgent() *CodeReaderAgent {
	budget := defaultCodeReadBudget()
	return &CodeReaderAgent{MaxFiles: budget.TotalFileLimit, MaxFileBytes: budget.MaxFileBytes, Budget: budget, InvestigationTools: NewProjectInvestigationToolSuite(nil)}
}

func NewCodeReaderAgentWithLLM(client llm.Client, cacheRoots ...string) *CodeReaderAgent {
	budget := defaultCodeReadBudget()
	cacheRoot := ""
	if len(cacheRoots) > 0 {
		cacheRoot = strings.TrimSpace(cacheRoots[0])
	}
	return &CodeReaderAgent{MaxFiles: budget.TotalFileLimit, MaxFileBytes: budget.MaxFileBytes, CacheRoot: cacheRoot, Budget: budget, InvestigationTools: NewProjectInvestigationToolSuite(client), llm: client}
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
			ID:                  firstNonEmpty(input.ID, fmt.Sprintf("code_snapshot_%d", i+1)),
			ProjectID:           project.ID,
			SchemaVersion:       model.MultimodalUnderstandingReportSchemaVersion,
			RepositoryID:        input.RepositoryID,
			URI:                 input.URI,
			Branch:              input.Branch,
			CommitSHA:           input.CommitSHA,
			Summary:             "代码结构摘要已生成；仅保存 hash、路由、组件、选择器和数据模型摘要。",
			CreatedAt:           time.Now().UTC(),
			SourceRefHashSHA256: digestCodeSourceReference(input),
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
			if err := a.scanLocalPath(ctx, input.LocalPath, &snapshot, project, brief); err != nil {
				snapshot.Summary = "代码路径暂不可扫描，已保留输入摘要和 evidence ref。"
			}
		} else if strings.TrimSpace(input.URI) != "" && isGitRepositoryInput(input) {
			if err := a.scanGitRepository(ctx, input, &snapshot, project, brief); err != nil {
				snapshot.Summary = "GitHub 仓库暂不可读取，已保留仓库 URL 摘要和 evidence ref。"
				snapshot.EvidenceRefs = append(snapshot.EvidenceRefs, model.EvidenceRef{
					ID:         "ev_git_repo_degraded_" + shortHash(input.URI),
					Kind:       model.EvidenceKindSourceCode,
					Summary:    "GitHub 仓库读取降级：" + sanitizeGitFetchError(err),
					FieldPath:  "inputs.code.uri",
					Confidence: 0.35,
				})
			}
		}
		if snapshot.SourceDigestSHA256 == "" {
			snapshot.SourceDigestSHA256 = digestSnapshotIdentity(input)
		}
		snapshot.ProductIdentitySignals = uniqueIdentitySignals(append(snapshot.ProductIdentitySignals, productIdentitySignalsFromCodeInput(input)...))
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

func productIdentitySignalsFromCodeInput(input model.CodeInput) []model.ProductIdentitySignal {
	signals := []model.ProductIdentitySignal{}
	if repository := normalizeRepositoryIdentity(input.URI); repository != "" {
		signals = appendIdentitySignal(signals, "repository", "strong", repository, input.EvidenceRefs)
		if parsed, err := url.Parse(strings.TrimSuffix(input.URI, ".git")); err == nil {
			signals = appendIdentitySignal(signals, "product_name", "medium", normalizeProductName(filepath.Base(parsed.Path)), input.EvidenceRefs)
		}
	}
	if input.LocalPath != "" {
		signals = appendIdentitySignal(signals, "product_name", "medium", normalizeProductName(filepath.Base(filepath.Clean(input.LocalPath))), input.EvidenceRefs)
	}
	return uniqueIdentitySignals(signals)
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

func (o *codeReaderLLMOutput) UnmarshalJSON(data []byte) error {
	type alias codeReaderLLMOutput
	var single alias
	if err := json.Unmarshal(data, &single); err == nil {
		*o = codeReaderLLMOutput(single)
		o.normalize()
		return nil
	}
	var items []alias
	if err := json.Unmarshal(data, &items); err != nil {
		return err
	}
	merged := codeReaderLLMOutput{}
	for _, item := range items {
		merged.merge(codeReaderLLMOutput(item))
	}
	merged.normalize()
	*o = merged
	return nil
}

func (a *CodeReaderAgent) scanGitRepository(ctx context.Context, input model.CodeInput, snapshot *model.CodeUnderstandingSnapshot, project *model.ProjectContext, brief *model.RequirementBrief) error {
	localPath, repoMeta, err := prepareReadOnlyGitRepositorySnapshot(ctx, a.CacheRoot, input.URI, input.Branch, input.CommitSHA)
	if err != nil {
		return err
	}
	snapshot.URI = repoMeta.URL
	snapshot.RepositoryID = firstNonEmpty(snapshot.RepositoryID, repoMeta.RepositoryID)
	snapshot.Branch = firstNonEmpty(snapshot.Branch, repoMeta.Branch)
	snapshot.CommitSHA = firstNonEmpty(snapshot.CommitSHA, repoMeta.CommitSHA)
	snapshot.EvidenceRefs = append(snapshot.EvidenceRefs, model.EvidenceRef{
		ID:         "ev_git_repo_snapshot_" + shortHash(repoMeta.URL+repoMeta.CommitSHA),
		Kind:       model.EvidenceKindRepoSnapshot,
		Summary:    "GitHub 仓库只读快照：" + repoMeta.SafeLabel,
		FieldPath:  "inputs.code.uri",
		Confidence: 0.72,
	})
	if err := a.scanLocalPath(ctx, localPath, snapshot, project, brief); err != nil {
		return err
	}
	if snapshot.InvestigationTrace != nil {
		snapshot.InvestigationTrace.Summary = "GitHub 仓库只读快照完成；" + snapshot.InvestigationTrace.Summary
	}
	return nil
}

func isGitRepositoryInput(input model.CodeInput) bool {
	kind := strings.ToLower(strings.TrimSpace(input.Kind))
	uri := strings.TrimSpace(input.URI)
	if uri == "" {
		return false
	}
	return strings.Contains(kind, "git") || strings.Contains(kind, "repository") || isSupportedGitRepositoryURL(uri)
}

func prepareReadOnlyGitRepositorySnapshot(ctx context.Context, cacheRoot string, rawURL string, branch string, commitSHA string) (string, gitRepositorySnapshotMetadata, error) {
	normalizedURL, safeLabel, err := normalizeSupportedGitRepositoryURL(rawURL)
	if err != nil {
		return "", gitRepositorySnapshotMetadata{}, err
	}
	repoID := "github_" + shortHash(strings.Join([]string{
		normalizedURL,
		strings.TrimSpace(branch),
		strings.TrimSpace(commitSHA),
	}, "#"))
	if strings.TrimSpace(cacheRoot) == "" {
		cacheRoot = filepath.Join(os.TempDir(), "cascade-demoops-cache")
	}
	root := filepath.Join(cacheRoot, "github", repoID)
	if err := os.MkdirAll(filepath.Dir(root), 0o700); err != nil {
		return "", gitRepositorySnapshotMetadata{}, err
	}
	_, statErr := os.Stat(filepath.Join(root, ".git"))
	if statErr != nil {
		if err := cloneGitRepositorySnapshot(ctx, normalizedURL, branch, root); err != nil {
			_ = os.RemoveAll(root)
			return "", gitRepositorySnapshotMetadata{}, err
		}
	} else if err := refreshGitRepositorySnapshot(ctx, root, branch); err != nil {
		return "", gitRepositorySnapshotMetadata{}, err
	}
	if strings.TrimSpace(commitSHA) != "" {
		if err := gitCommand(ctx, root, "checkout", "--detach", commitSHA); err != nil {
			return "", gitRepositorySnapshotMetadata{}, err
		}
	} else if strings.TrimSpace(branch) != "" {
		if err := gitCommand(ctx, root, "checkout", branch); err != nil {
			_ = gitCommand(ctx, root, "checkout", "origin/"+branch)
		}
	}
	resolvedCommit, _ := gitCommandOutput(ctx, root, "rev-parse", "HEAD")
	resolvedBranch := strings.TrimSpace(branch)
	if resolvedBranch == "" {
		resolvedBranch, _ = gitCommandOutput(ctx, root, "branch", "--show-current")
	}
	return root, gitRepositorySnapshotMetadata{
		URL:          normalizedURL,
		RepositoryID: repoID,
		Branch:       strings.TrimSpace(resolvedBranch),
		CommitSHA:    strings.TrimSpace(resolvedCommit),
		SafeLabel:    safeLabel,
	}, nil
}

func cloneGitRepositorySnapshot(ctx context.Context, repoURL string, branch string, destination string) error {
	args := []string{"clone", "--depth", "1", "--filter=blob:none"}
	if strings.TrimSpace(branch) != "" {
		args = append(args, "--branch", strings.TrimSpace(branch))
	}
	args = append(args, repoURL, destination)
	return gitCommand(ctx, "", args...)
}

func refreshGitRepositorySnapshot(ctx context.Context, root string, branch string) error {
	args := []string{"fetch", "--depth", "1", "origin"}
	if strings.TrimSpace(branch) != "" {
		args = append(args, strings.TrimSpace(branch))
	}
	return gitCommand(ctx, root, args...)
}

func isSupportedGitRepositoryURL(value string) bool {
	_, _, err := normalizeSupportedGitRepositoryURL(value)
	return err == nil
}

func normalizeSupportedGitRepositoryURL(value string) (string, string, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return "", "", fmt.Errorf("git repository URL is required")
	}
	if strings.HasPrefix(raw, "git@github.com:") {
		path := strings.TrimSuffix(strings.TrimPrefix(raw, "git@github.com:"), ".git")
		if !safeGitHubRepoPath(path) {
			return "", "", fmt.Errorf("unsupported GitHub repository path")
		}
		return "https://github.com/" + path + ".git", "github.com/" + path, nil
	}
	if strings.HasPrefix(raw, "file://") {
		return raw, "local git fixture", nil
	}
	if !strings.HasPrefix(strings.ToLower(raw), "https://github.com/") {
		return "", "", fmt.Errorf("only HTTPS github.com repository URLs are supported")
	}
	path := strings.TrimPrefix(raw, "https://github.com/")
	path = strings.TrimSuffix(path, ".git")
	path = strings.Trim(path, "/")
	if !safeGitHubRepoPath(path) {
		return "", "", fmt.Errorf("unsupported GitHub repository path")
	}
	return "https://github.com/" + path + ".git", "github.com/" + path, nil
}

func safeGitHubRepoPath(path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	allowed := regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	return allowed.MatchString(parts[0]) && allowed.MatchString(parts[1])
}

func gitCommand(ctx context.Context, dir string, args ...string) error {
	_, err := gitCommandOutput(ctx, dir, args...)
	return err
}

func gitCommandOutput(ctx context.Context, dir string, args ...string) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", fmt.Errorf("git executable unavailable")
	}
	runCtx, cancel := context.WithTimeout(ctx, gitSnapshotTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if token, tokenErr := credentialstore.ReadGitHubToken(); tokenErr == nil && token != "" {
		cmd.Env = append(cmd.Env, "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.https://github.com/.extraheader", "GIT_CONFIG_VALUE_0=Authorization: Bearer "+token)
	}
	output, err := cmd.CombinedOutput()
	if runCtx.Err() != nil {
		return "", runCtx.Err()
	}
	if err != nil {
		return "", fmt.Errorf("git %s failed: %s", safeGitCommand(args), sanitizeGitFetchError(fmt.Errorf("%s", string(output))))
	}
	return strings.TrimSpace(string(output)), nil
}

func safeGitCommand(args []string) string {
	if len(args) == 0 {
		return ""
	}
	safe := append([]string{}, args...)
	for i, arg := range safe {
		if strings.Contains(arg, "://") || strings.Contains(arg, "@github.com:") {
			safe[i] = "<repo-url>"
		}
	}
	if len(safe) > 4 {
		safe = append(safe[:4], "...")
	}
	return strings.Join(safe, " ")
}

func sanitizeGitFetchError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	message = regexp.MustCompile(`https://[^@\s]+@github\.com/`).ReplaceAllString(message, "https://<redacted>@github.com/")
	message = regexp.MustCompile(`(?i)(token|password|authorization)[=:]\S+`).ReplaceAllString(message, "$1=<redacted>")
	if len([]rune(message)) > 180 {
		runes := []rune(message)
		message = string(runes[:179]) + "…"
	}
	return message
}

func (o *codeReaderLLMOutput) merge(item codeReaderLLMOutput) {
	if o.Summary == "" {
		o.Summary = item.Summary
	}
	o.Frameworks = append(o.Frameworks, item.Frameworks...)
	o.HeroComponents = append(o.HeroComponents, item.HeroComponents...)
	o.RouteNames = append(o.RouteNames, item.RouteNames...)
	o.SelectorNotes = append(o.SelectorNotes, item.SelectorNotes...)
	o.SensitiveFields = append(o.SensitiveFields, item.SensitiveFields...)
	if item.Confidence > o.Confidence {
		o.Confidence = item.Confidence
	}
}

func (o *codeReaderLLMOutput) normalize() {
	o.Frameworks = flexibleStringSlice(uniqueStrings([]string(o.Frameworks)))
	o.HeroComponents = flexibleStringSlice(uniqueStrings([]string(o.HeroComponents)))
	o.SelectorNotes = flexibleStringSlice(uniqueStrings([]string(o.SelectorNotes)))
	o.SensitiveFields = flexibleStringSlice(uniqueStrings([]string(o.SensitiveFields)))
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

type codeCandidateFile struct {
	path  string
	rel   string
	name  string
	size  int64
	score int
}

func (a *CodeReaderAgent) scanLocalPath(ctx context.Context, root string, snapshot *model.CodeUnderstandingSnapshot, project *model.ProjectContext, brief *model.RequirementBrief) error {
	root = filepath.Clean(root)
	budget := a.effectiveBudget()
	snapshot.ReadBudget = &budget
	investigationResult, err := a.investigationToolSuite().InvestigateLocalPath(ctx, root, budget, project, brief)
	if err != nil {
		return err
	}
	snapshot.InvestigationTrace = investigationResult.Trace
	if err := readStructuredCodeSnapshotFromCandidates(ctx, snapshot, investigationResult.SelectedCandidates, budget); err != nil {
		return err
	}
	snapshot.ProductIdentitySignals = productIdentitySignalsFromCodeCandidates(investigationResult.SelectedCandidates)
	normalizeCodeSnapshotForIntent(snapshot, project, brief)
	return nil
}

func readStructuredCodeSnapshotFromCandidates(ctx context.Context, snapshot *model.CodeUnderstandingSnapshot, candidates []codeCandidateFile, budget model.CodeReadBudget) error {
	if snapshot == nil {
		return nil
	}
	pathDigests := make([]model.PathDigest, 0)
	contentDigests := make([]string, 0)
	fileCount := 0
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := os.ReadFile(candidate.path)
		if err != nil {
			continue
		}
		name := candidate.name
		rel := candidate.rel
		pathHash := hashString(filepath.ToSlash(rel))
		contentHash := hashBytes(data)
		kind := codeKindForFile(name)
		language := languageForFile(name)
		fileCount++
		if snapshot.InvestigationTrace != nil {
			snapshot.InvestigationTrace.TotalBytesRead += int64(len(data))
		}
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
		inspectFilesystemRoutes(rel, pathHash, snapshot)
		inspectRoutes(text, pathHash, snapshot)
		inspectSelectors(text, pathHash, snapshot)
		inspectSemanticControls(text, pathHash, snapshot)
		inspectComponents(name, text, pathHash, snapshot)
		inspectAPIs(text, pathHash, snapshot)
		inspectDataModels(text, pathHash, snapshot)
		inspectSensitiveFields(text, snapshot)
	}
	sort.Strings(contentDigests)
	snapshot.FileCount = fileCount
	snapshot.PathDigests = pathDigests
	snapshot.SourceDigestSHA256 = hashString(strings.Join(contentDigests, "\n"))
	if snapshot.InvestigationTrace != nil {
		snapshot.InvestigationTrace.TotalFilesSelected = fileCount
		snapshot.InvestigationTrace.CompletedAt = time.Now().UTC()
		snapshot.InvestigationTrace.Summary = fmt.Sprintf("工具化调查完成：发现 %d 个候选文件，grep 搜索 %d 个文件，最终结构化读取 %d 个文件。", snapshot.InvestigationTrace.TotalFilesDiscovered, snapshot.InvestigationTrace.TotalFilesSearched, fileCount)
		call := model.CodeInvestigationToolCall{
			ID:                "tool_read_structured_files_" + shortHash(snapshot.ID),
			Tool:              "read_structured_files",
			Purpose:           "只读取已由 repo index / grep_text 选中的需求相关文件，并提取路由、组件、selector、API、数据模型摘要。",
			InputSummary:      fmt.Sprintf("selected_files=%d max_file_bytes=%d", len(candidates), budget.MaxFileBytes),
			OutputSummary:     fmt.Sprintf("structured_files=%d routes=%d components=%d selectors=%d apis=%d", fileCount, len(snapshot.Routes), len(snapshot.Components), len(snapshot.Selectors), len(snapshot.APIEndpoints)),
			SelectedFileCount: fileCount,
			PathHashes:        pathHashesForCandidates(candidates, fileCount),
			Confidence:        0.78,
		}
		annotateInvestigationToolCall(&call)
		snapshot.InvestigationTrace.ToolCalls = append(snapshot.InvestigationTrace.ToolCalls, call)
		snapshot.InvestigationQuality = codeInvestigationQualityForSnapshot(snapshot)
	}
	return nil
}

func defaultCodeReadBudget() model.CodeReadBudget {
	return model.CodeReadBudget{
		Mode:                   "intent_driven_progressive",
		RepoIndexFileLimit:     24,
		DrilldownRounds:        4,
		FilesPerRound:          6,
		TotalFileLimit:         32,
		MaxFileBytes:           128 * 1024,
		ToolSearchFileLimit:    120,
		ToolSearchBytesPerFile: 48 * 1024,
		ToolSearchResultLimit:  18,
	}
}

func (a *CodeReaderAgent) effectiveBudget() model.CodeReadBudget {
	budget := a.Budget
	defaults := defaultCodeReadBudget()
	if strings.TrimSpace(budget.Mode) == "" {
		budget.Mode = defaults.Mode
	}
	if budget.RepoIndexFileLimit <= 0 {
		budget.RepoIndexFileLimit = defaults.RepoIndexFileLimit
	}
	if budget.DrilldownRounds <= 0 {
		budget.DrilldownRounds = defaults.DrilldownRounds
	}
	if budget.FilesPerRound <= 0 {
		budget.FilesPerRound = defaults.FilesPerRound
	}
	if budget.TotalFileLimit <= 0 {
		budget.TotalFileLimit = firstPositiveInt(a.MaxFiles, defaults.TotalFileLimit)
	}
	if budget.TotalFileLimit > maxCodeReadTotalFileLimit {
		budget.TotalFileLimit = maxCodeReadTotalFileLimit
	}
	if budget.MaxFileBytes <= 0 {
		budget.MaxFileBytes = firstPositiveInt64(a.MaxFileBytes, defaults.MaxFileBytes)
	}
	if budget.ToolSearchFileLimit <= 0 {
		budget.ToolSearchFileLimit = defaults.ToolSearchFileLimit
	}
	if budget.ToolSearchBytesPerFile <= 0 {
		budget.ToolSearchBytesPerFile = defaults.ToolSearchBytesPerFile
	}
	if budget.ToolSearchResultLimit <= 0 {
		budget.ToolSearchResultLimit = defaults.ToolSearchResultLimit
	}
	a.MaxFileBytes = budget.MaxFileBytes
	return budget
}

func (a *CodeReaderAgent) investigationToolSuite() *ProjectInvestigationToolSuite {
	if a == nil {
		return NewProjectInvestigationToolSuite(nil)
	}
	if a.InvestigationTools != nil {
		if a.InvestigationTools.llm == nil && a.llm != nil {
			a.InvestigationTools.llm = a.llm
		}
		return a.InvestigationTools
	}
	a.InvestigationTools = NewProjectInvestigationToolSuite(a.llm)
	return a.InvestigationTools
}

type codeInvestigationQuery struct {
	questionID       string
	purpose          string
	query            string
	terms            []string
	expectedEvidence []string
	plannedFrom      string
	suggestedTool    string
	parentTool       string
	commandKind      string
	dependsOnToolID  string
}

type codeInvestigationToolPolicy struct {
	grepText           bool
	followImports      bool
	findReferences     bool
	findAPIHandlers    bool
	readWindow         bool
	listRelatedFiles   bool
	inspectFileOutline bool
	shellRun           bool
	expectedEvidence   []string
}

type codeSearchMatch struct {
	candidate           codeCandidateFile
	score               int
	terms               []string
	snippets            []model.CodeSnippetRef
	snippetObservations []codeSnippetObservation
}

type codeSearchResult struct {
	matches   []codeSearchMatch
	searched  int
	bytesRead int64
}

type codeSnippetObservation struct {
	ID           string
	PathHash     string
	LineStart    int
	LineEnd      int
	MatchedTerms []string
	SignalKinds  []string
	Preview      string
}

type relatedFileObservation struct {
	ID       string
	DirHash  string
	FileName string
	Kind     string
	PathHash string
	Score    int
	Reason   string
}

type fileOutlineObservation struct {
	ID             string
	FileName       string
	Kind           string
	PathHash       string
	DirHash        string
	SymbolNames    []string
	RoutePaths     []string
	APIPaths       []string
	SelectorHints  []string
	DataModelNames []string
	SignalKinds    []string
	Reason         string
}

type importFollowScan struct {
	sourceFileCount int
	importCount     int
	bytesRead       int64
	aliasRuleCount  int
	aliasRuleHashes []string
}

type importAliasRule struct {
	Prefix       string
	TargetPrefix string
}

type importAliasScan struct {
	configFileCount int
	ruleCount       int
	bytesRead       int64
}

type referenceSearchScan struct {
	seedFileCount int
	termCount     int
	searched      int
	matched       int
	bytesRead     int64
	terms         []string
}

type apiHandlerSearchScan struct {
	seedFileCount int
	pathCount     int
	searched      int
	matched       int
	bytesRead     int64
	apiPaths      []string
}

type codeInvestigationEvidenceReview struct {
	fileCount      int
	routeCount     int
	componentCount int
	selectorCount  int
	apiCount       int
	backendCount   int
	dataModelCount int
	styleCount     int
	gaps           []string
	pathHashes     []string
}

type codeInvestigationLLMOutput struct {
	Summary    string                      `json:"summary"`
	Queries    []codeInvestigationPlanItem `json:"queries"`
	Confidence float64                     `json:"confidence"`
}

type codeInvestigationPlanItem struct {
	Purpose          string              `json:"purpose"`
	Terms            flexibleStringSlice `json:"terms"`
	Query            string              `json:"query"`
	Tool             string              `json:"tool"`
	CommandKind      string              `json:"command_kind"`
	Command          string              `json:"command"`
	ExpectedEvidence flexibleStringSlice `json:"expected_evidence"`
}

type codeInvestigationNextActionsLLMOutput struct {
	Summary     string                               `json:"summary"`
	NextActions []codeInvestigationNextActionLLMItem `json:"next_actions"`
	Actions     []codeInvestigationNextActionLLMItem `json:"actions"`
	Confidence  float64                              `json:"confidence"`
}

type codeInvestigationNextActionLLMItem struct {
	Tool             string              `json:"tool"`
	Reason           string              `json:"reason"`
	QueryTerms       flexibleStringSlice `json:"query_terms"`
	Terms            flexibleStringSlice `json:"terms"`
	CommandKind      string              `json:"command_kind"`
	Command          string              `json:"command"`
	ExpectedEvidence flexibleStringSlice `json:"expected_evidence"`
}

func (o *codeInvestigationNextActionsLLMOutput) normalize() {
	if len(o.NextActions) == 0 && len(o.Actions) > 0 {
		o.NextActions = o.Actions
	}
	normalized := make([]codeInvestigationNextActionLLMItem, 0, len(o.NextActions))
	for _, action := range o.NextActions {
		terms := sanitizeInvestigationTerms(append(stringSlice(action.QueryTerms), stringSlice(action.Terms)...))
		expected := sanitizeExpectedEvidenceKinds(stringSlice(action.ExpectedEvidence))
		tool := sanitizeInvestigationToolName(action.Tool)
		if tool == "" {
			tool = toolForExpectedEvidence(expected)
		}
		action.CommandKind = sanitizeShellRunCommandKind(action.CommandKind, action.Command)
		if tool == "shell_run" && action.CommandKind == "" {
			action.CommandKind = shellRunCommandKindForTerms(terms)
		}
		if investigationToolRequiresTerms(tool, action.CommandKind) && len(terms) == 0 {
			continue
		}
		action.Tool = tool
		action.QueryTerms = flexibleStringSlice(terms)
		action.Terms = nil
		action.ExpectedEvidence = flexibleStringSlice(expected)
		normalized = append(normalized, action)
	}
	o.NextActions = normalized
}

func (o *codeInvestigationLLMOutput) UnmarshalJSON(data []byte) error {
	type alias codeInvestigationLLMOutput
	var single alias
	if err := json.Unmarshal(data, &single); err == nil {
		*o = codeInvestigationLLMOutput(single)
		o.normalize()
		return nil
	}
	var items []alias
	if err := json.Unmarshal(data, &items); err != nil {
		return err
	}
	merged := codeInvestigationLLMOutput{}
	for _, item := range items {
		if merged.Summary == "" {
			merged.Summary = item.Summary
		}
		merged.Queries = append(merged.Queries, item.Queries...)
		if item.Confidence > merged.Confidence {
			merged.Confidence = item.Confidence
		}
	}
	*o = merged
	o.normalize()
	return nil
}

func (o *codeInvestigationLLMOutput) normalize() {
	normalized := make([]codeInvestigationPlanItem, 0, len(o.Queries))
	for _, query := range o.Queries {
		terms := sanitizeInvestigationTerms(append(stringSlice(query.Terms), splitQueryTerms(query.Query)...))
		tool := sanitizeInvestigationToolName(query.Tool)
		if tool == "" {
			tool = "grep_text"
		}
		query.CommandKind = sanitizeShellRunCommandKind(query.CommandKind, query.Command)
		if tool == "shell_run" && query.CommandKind == "" {
			query.CommandKind = shellRunCommandKindForTerms(terms)
		}
		if investigationToolRequiresTerms(tool, query.CommandKind) && len(terms) == 0 {
			continue
		}
		query.Tool = tool
		query.Terms = flexibleStringSlice(terms)
		query.Query = strings.Join(terms, " OR ")
		normalized = append(normalized, query)
	}
	o.Queries = normalized
}

func (s *ProjectInvestigationToolSuite) Investigate(ctx context.Context, request ProjectInvestigationRequest) (ProjectInvestigationResult, error) {
	root := request.Root
	candidates := request.Candidates
	budget := request.Budget
	project := request.Project
	brief := request.Brief
	trace := &model.CodeInvestigationTrace{
		ID:                   "code_investigation_" + shortHash(root+"|"+budget.Mode+"|"+strings.Join(codeIntentTextParts(project, brief), "|")),
		Mode:                 "tool_driven_intent_drilldown",
		TotalFilesDiscovered: len(candidates),
		CreatedAt:            time.Now().UTC(),
	}
	trace.Questions = buildCodeInvestigationQuestions(project, brief, budget)
	if len(candidates) == 0 {
		finalizeInvestigationQuestions(trace)
		trace.CompletedAt = time.Now().UTC()
		trace.Summary = "未发现可调查的代码文件。"
		return ProjectInvestigationResult{Trace: trace}, nil
	}
	trace.ToolCalls = append(trace.ToolCalls, s.shellMetadataToolCall(ctx, root, candidates))
	selected := []codeCandidateFile{}
	selectedKeys := map[string]bool{}
	candidateIndex := codeCandidateIndex(candidates)
	importAliasRules, importAliasScan, err := importAliasRulesFromCandidates(ctx, candidates, budget)
	if err != nil {
		return ProjectInvestigationResult{SelectedCandidates: selected, Trace: trace}, err
	}
	trace.TotalBytesRead += importAliasScan.bytesRead
	add := func(candidate codeCandidateFile) bool {
		if len(selected) >= budget.TotalFileLimit || selectedKeys[candidate.rel] {
			return false
		}
		selectedKeys[candidate.rel] = true
		selected = append(selected, candidate)
		return true
	}

	start := time.Now()
	repoIndexSelected := []codeCandidateFile{}
	for _, candidate := range candidates {
		if len(repoIndexSelected) >= budget.RepoIndexFileLimit || len(selected) >= budget.TotalFileLimit {
			break
		}
		if isRepoIndexCandidate(candidate.rel, candidate.name) && add(candidate) {
			repoIndexSelected = append(repoIndexSelected, candidate)
		}
	}
	trace.ToolCalls = append(trace.ToolCalls, model.CodeInvestigationToolCall{
		ID:                "tool_repo_index_" + shortHash(root),
		Tool:              "repo_index",
		Purpose:           "列出 manifest、入口、路由目录和配置文件，建立项目调查的第一层地图。",
		InputSummary:      fmt.Sprintf("candidate_files=%d repo_index_limit=%d", len(candidates), budget.RepoIndexFileLimit),
		OutputSummary:     fmt.Sprintf("selected_index_files=%d", len(repoIndexSelected)),
		MatchedFileCount:  len(repoIndexSelected),
		SelectedFileCount: len(repoIndexSelected),
		PathHashes:        pathHashesForCandidates(repoIndexSelected, len(repoIndexSelected)),
		Confidence:        0.76,
		ElapsedMS:         time.Since(start).Milliseconds(),
	})

	queries, planningCall := s.planCodeInvestigationQueries(ctx, candidates, budget, project, brief, trace.Questions)
	trace.ToolCalls = append(trace.ToolCalls, planningCall)
	executedQueries := map[string]bool{}
	snippetObservationCache := map[string][]codeSnippetObservation{}
	relatedFileObservationCache := map[string][]relatedFileObservation{}
	fileOutlineObservationCache := map[string][]fileOutlineObservation{}
	maxSearchRounds := maxInt(1, budget.DrilldownRounds)
	maxAdaptiveRounds := maxSearchRounds + 2
	for i := 0; i < len(queries) && i < maxAdaptiveRounds; i++ {
		query := queries[i]
		if err := ctx.Err(); err != nil {
			return ProjectInvestigationResult{SelectedCandidates: selected, Trace: trace}, err
		}
		if len(selected) >= budget.TotalFileLimit {
			break
		}
		queryKey := codeInvestigationQueryExecutionKey(query)
		if executedQueries[queryKey] {
			continue
		}
		if codeInvestigationQuestionAnswered(trace, query.questionID) && shouldSkipAnsweredInvestigationQuery(query) {
			continue
		}
		executedQueries[queryKey] = true
		toolPolicy := toolPolicyForInvestigationQuery(query)
		if s != nil && s.llm != nil && query.plannedFrom == "initial_plan" && sanitizeInvestigationToolName(query.suggestedTool) == "grep_text" {
			toolPolicy.followImports = false
			toolPolicy.findReferences = false
			toolPolicy.findAPIHandlers = false
		}
		cachedSnippetObservations := snippetObservationCache[query.questionID]
		grepBudget, ok := budgetForRemainingToolSearch(budget, trace.TotalFilesSearched)
		if !ok && investigationToolPolicyRequiresSearchBudget(toolPolicy) {
			break
		}
		if !ok {
			grepBudget = budget
			grepBudget.ToolSearchFileLimit = 0
		}
		selectedThisRound := []codeCandidateFile{}
		snippetsThisRound := []model.CodeSnippetRef{}
		snippetObservationsThisRound := []codeSnippetObservation{}
		grepCallID := ""
		if toolPolicy.grepText {
			start = time.Now()
			search, err := searchCodeCandidatesForQuery(ctx, candidates, selectedKeys, query, grepBudget)
			if err != nil {
				return ProjectInvestigationResult{SelectedCandidates: selected, Trace: trace}, err
			}
			trace.TotalFilesSearched += search.searched
			trace.TotalBytesRead += search.bytesRead
			for _, match := range search.matches {
				if len(selectedThisRound) >= budget.FilesPerRound || len(selected) >= budget.TotalFileLimit {
					break
				}
				if add(match.candidate) {
					selectedThisRound = append(selectedThisRound, match.candidate)
					snippetsThisRound = append(snippetsThisRound, match.snippets...)
					snippetObservationsThisRound = append(snippetObservationsThisRound, match.snippetObservations...)
				}
			}
			grepCallID = fmt.Sprintf("tool_grep_text_%d_%s", i+1, shortHash(query.query))
			trace.ToolCalls = append(trace.ToolCalls, model.CodeInvestigationToolCall{
				ID:                grepCallID,
				Tool:              "grep_text",
				Purpose:           query.purpose,
				Query:             query.query,
				InputSummary:      fmt.Sprintf("terms=%s search_file_limit=%d bytes_per_file=%d", strings.Join(query.terms, ","), grepBudget.ToolSearchFileLimit, grepBudget.ToolSearchBytesPerFile),
				OutputSummary:     fmt.Sprintf("searched=%d matched=%d selected=%d", search.searched, len(search.matches), len(selectedThisRound)),
				MatchedFileCount:  len(search.matches),
				SelectedFileCount: len(selectedThisRound),
				PathHashes:        pathHashesForCandidates(selectedThisRound, len(selectedThisRound)),
				SnippetRefs:       limitCodeSnippetRefs(snippetsThisRound, 12),
				Metadata:          investigationQueryMetadataWithToolPolicy(query, toolPolicy),
				Confidence:        confidenceForSearchCall(len(search.matches), len(selectedThisRound)),
				ElapsedMS:         time.Since(start).Milliseconds(),
			})
		}
		shellRunCallID := ""
		shellRunThisRound := []codeCandidateFile{}
		if toolPolicy.shellRun {
			shellRunSelected, shellRunScan, shellRunCall := runReadOnlyShellInvestigationTool(ctx, i+1, root, candidates, selectedKeys, query, grepBudget)
			trace.TotalFilesSearched += shellRunScan.searched
			trace.TotalBytesRead += shellRunScan.bytesRead
			if shellRunCall.ID != "" {
				shellRunCallID = shellRunCall.ID
				trace.ToolCalls = append(trace.ToolCalls, shellRunCall)
			}
			for _, candidate := range shellRunSelected {
				if len(shellRunThisRound) >= minInt(3, budget.FilesPerRound) || len(selected) >= budget.TotalFileLimit {
					break
				}
				if add(candidate) {
					shellRunThisRound = append(shellRunThisRound, candidate)
				}
			}
		}
		snippetCallID := ""
		if len(snippetsThisRound) > 0 {
			snippetCallID = fmt.Sprintf("tool_read_evidence_snippets_%d_%s", i+1, shortHash(query.query))
			trace.ToolCalls = append(trace.ToolCalls, evidenceSnippetToolCall(snippetCallID, query, snippetsThisRound))
		}
		readWindowCallID := ""
		if toolPolicy.readWindow {
			windowObservations, windowRefs, windowCall := readEvidenceWindowsFromSnippetObservations(ctx, i+1, query, candidates, append(cachedSnippetObservations, snippetObservationsThisRound...), budget)
			if windowCall.ID != "" {
				readWindowCallID = windowCall.ID
				trace.ToolCalls = append(trace.ToolCalls, windowCall)
			}
			snippetsThisRound = append(snippetsThisRound, windowRefs...)
			snippetObservationsThisRound = append(snippetObservationsThisRound, windowObservations...)
		}
		relatedFileObservationsThisRound := []relatedFileObservation{}
		listRelatedCallID := ""
		if toolPolicy.listRelatedFiles {
			seedCandidates := append([]codeCandidateFile{}, selectedThisRound...)
			seedCandidates = append(seedCandidates, candidateSeedsFromSnippetObservations(snippetObservationCache[query.questionID], candidates)...)
			var listCall model.CodeInvestigationToolCall
			relatedFileObservationsThisRound, listCall = listRelatedFilesForSelectedCandidates(i+1, query, candidates, seedCandidates, budget)
			if listCall.ID != "" {
				listRelatedCallID = listCall.ID
				trace.ToolCalls = append(trace.ToolCalls, listCall)
			}
		}
		if len(snippetObservationsThisRound) > 0 {
			snippetObservationCache[query.questionID] = appendSnippetObservationCache(snippetObservationCache[query.questionID], snippetObservationsThisRound, 12)
		}
		if len(relatedFileObservationsThisRound) > 0 {
			relatedFileObservationCache[query.questionID] = appendRelatedFileObservationCache(relatedFileObservationCache[query.questionID], relatedFileObservationsThisRound, 20)
		}
		fileOutlineObservationsThisRound := []fileOutlineObservation{}
		inspectFileOutlineCallID := ""
		if toolPolicy.inspectFileOutline {
			seedCandidates := append([]codeCandidateFile{}, selectedThisRound...)
			seedCandidates = append(seedCandidates, candidateSeedsFromSnippetObservations(snippetObservationCache[query.questionID], candidates)...)
			seedCandidates = append(seedCandidates, candidateSeedsFromRelatedFileObservations(relatedFileObservationCache[query.questionID], candidates)...)
			var outlineBytesRead int64
			var outlineCall model.CodeInvestigationToolCall
			fileOutlineObservationsThisRound, outlineBytesRead, outlineCall = inspectFileOutlinesForSelectedCandidates(ctx, i+1, query, candidates, seedCandidates, budget)
			trace.TotalBytesRead += outlineBytesRead
			if outlineCall.ID != "" {
				inspectFileOutlineCallID = outlineCall.ID
				trace.ToolCalls = append(trace.ToolCalls, outlineCall)
			}
		}
		if len(fileOutlineObservationsThisRound) > 0 {
			fileOutlineObservationCache[query.questionID] = appendFileOutlineObservationCache(fileOutlineObservationCache[query.questionID], fileOutlineObservationsThisRound, 20)
		}
		followedImports := []codeCandidateFile{}
		importScan := importFollowScan{
			aliasRuleCount:  len(importAliasRules),
			aliasRuleHashes: importAliasRuleHashes(importAliasRules, 8),
		}
		if toolPolicy.followImports {
			followedImports, importScan, err = followImportsFromCandidates(ctx, selectedThisRound, candidateIndex, selectedKeys, budget, importAliasRules)
			if err != nil {
				return ProjectInvestigationResult{SelectedCandidates: selected, Trace: trace}, err
			}
			trace.TotalBytesRead += importScan.bytesRead
		}
		followedThisRound := []codeCandidateFile{}
		for _, candidate := range followedImports {
			if len(followedThisRound) >= minInt(4, budget.FilesPerRound) || len(selected) >= budget.TotalFileLimit {
				break
			}
			if add(candidate) {
				followedThisRound = append(followedThisRound, candidate)
			}
		}
		followCallID := ""
		if len(followedThisRound) > 0 || importScan.importCount > 0 {
			followCallID = fmt.Sprintf("tool_follow_imports_%d_%s", i+1, shortHash(query.query))
			trace.ToolCalls = append(trace.ToolCalls, followImportsToolCall(followCallID, query, importScan, followedThisRound))
		}
		referenceSources := append(append(append([]codeCandidateFile{}, selectedThisRound...), shellRunThisRound...), followedThisRound...)
		referenceBudget, ok := budgetForRemainingToolSearch(budget, trace.TotalFilesSearched)
		referencedFiles := []codeCandidateFile{}
		referenceScan := referenceSearchScan{}
		if ok && toolPolicy.findReferences {
			referencedFiles, referenceScan, err = findReferencesFromCandidates(ctx, referenceSources, candidates, selectedKeys, referenceBudget)
		}
		if err != nil {
			return ProjectInvestigationResult{SelectedCandidates: selected, Trace: trace}, err
		}
		trace.TotalFilesSearched += referenceScan.searched
		trace.TotalBytesRead += referenceScan.bytesRead
		referencedThisRound := []codeCandidateFile{}
		for _, candidate := range referencedFiles {
			if len(referencedThisRound) >= minInt(3, budget.FilesPerRound) || len(selected) >= budget.TotalFileLimit {
				break
			}
			if add(candidate) {
				referencedThisRound = append(referencedThisRound, candidate)
			}
		}
		referenceCallID := ""
		if len(referencedThisRound) > 0 || referenceScan.termCount > 0 {
			referenceCallID = fmt.Sprintf("tool_find_references_%d_%s", i+1, shortHash(query.query))
			trace.ToolCalls = append(trace.ToolCalls, findReferencesToolCall(referenceCallID, query, referenceScan, referencedThisRound))
		}
		apiSources := append(append(append(append([]codeCandidateFile{}, selectedThisRound...), shellRunThisRound...), followedThisRound...), referencedThisRound...)
		apiBudget, ok := budgetForRemainingToolSearch(budget, trace.TotalFilesSearched)
		apiHandlerFiles := []codeCandidateFile{}
		apiHandlerScan := apiHandlerSearchScan{}
		if ok && toolPolicy.findAPIHandlers {
			apiHandlerFiles, apiHandlerScan, err = findAPIHandlersFromCandidates(ctx, apiSources, candidates, selectedKeys, apiBudget)
		}
		if err != nil {
			return ProjectInvestigationResult{SelectedCandidates: selected, Trace: trace}, err
		}
		trace.TotalFilesSearched += apiHandlerScan.searched
		trace.TotalBytesRead += apiHandlerScan.bytesRead
		apiHandlersThisRound := []codeCandidateFile{}
		for _, candidate := range apiHandlerFiles {
			if len(apiHandlersThisRound) >= minInt(3, budget.FilesPerRound) || len(selected) >= budget.TotalFileLimit {
				break
			}
			if add(candidate) {
				apiHandlersThisRound = append(apiHandlersThisRound, candidate)
			}
		}
		apiHandlerCallID := ""
		if len(apiHandlersThisRound) > 0 || apiHandlerScan.pathCount > 0 {
			apiHandlerCallID = fmt.Sprintf("tool_find_api_handlers_%d_%s", i+1, shortHash(query.query))
			trace.ToolCalls = append(trace.ToolCalls, findAPIHandlersToolCall(apiHandlerCallID, query, apiHandlerScan, apiHandlersThisRound))
		}
		reviewCandidates := append(append(append(append(append([]codeCandidateFile{}, selectedThisRound...), shellRunThisRound...), followedThisRound...), referencedThisRound...), apiHandlersThisRound...)
		review, err := reviewInvestigationEvidence(ctx, reviewCandidates, project, brief)
		if err != nil {
			return ProjectInvestigationResult{SelectedCandidates: selected, Trace: trace}, err
		}
		review.gaps = evidenceGapsForExpected(review, query.expectedEvidence)
		reviewCall := evidenceReviewToolCall(i+1, query, review)
		trace.ToolCalls = append(trace.ToolCalls, reviewCall)
		nextActions, nextActionPlanningCall := s.planNextActionsFromEvidenceObservation(ctx, query, review, trace, budget, snippetObservationsThisRound, relatedFileObservationsThisRound, fileOutlineObservationsThisRound, reviewCall.ID)
		nextActionPlanningCallID := ""
		if nextActionPlanningCall.ID != "" {
			nextActionPlanningCallID = nextActionPlanningCall.ID
			trace.ToolCalls = append(trace.ToolCalls, nextActionPlanningCall)
		}
		applyEvidenceReviewToQuestion(trace, query, review, nextActions, nonEmptyToolCallIDs(grepCallID, shellRunCallID, snippetCallID, readWindowCallID, listRelatedCallID, inspectFileOutlineCallID, followCallID, referenceCallID, apiHandlerCallID, reviewCall.ID, nextActionPlanningCallID)...)
		beforeFollowUp := len(queries)
		queries = appendNextActionInvestigationQueries(queries, trace, query, review, nextActions, lastNonEmptyString(nextActionPlanningCallID, reviewCall.ID), executedQueries, maxAdaptiveRounds)
		if len(queries) == beforeFollowUp && (s == nil || s.llm == nil) {
			if next, ok := adaptiveQueryFromEvidenceReview(query, review); ok && len(queries) < maxAdaptiveRounds {
				nextKey := strings.Join(next.terms, "|")
				if !executedQueries[nextKey] && !codeInvestigationQueryExists(queries, nextKey) {
					queries = append(queries, next)
				}
			}
		}
	}

	if len(selected) < budget.TotalFileLimit && shouldRunFocusedFallback(trace, selected) {
		start = time.Now()
		fallbackSelected := []codeCandidateFile{}
		for _, candidate := range candidates {
			if len(fallbackSelected) >= minInt(6, budget.FilesPerRound) || len(selected) >= budget.TotalFileLimit {
				break
			}
			if selectedKeys[candidate.rel] || !focusedFallbackCandidate(candidate) {
				continue
			}
			if add(candidate) {
				fallbackSelected = append(fallbackSelected, candidate)
			}
		}
		if len(fallbackSelected) > 0 {
			trace.ToolCalls = append(trace.ToolCalls, model.CodeInvestigationToolCall{
				ID:                "tool_focused_fallback_" + shortHash(root),
				Tool:              "focused_fallback",
				Purpose:           "当 grep 命中不足时，只补充少量入口、路由或高置信产品文件，不使用全局 selector 池凑数。",
				InputSummary:      fmt.Sprintf("remaining_budget=%d", budget.TotalFileLimit-len(selected)+len(fallbackSelected)),
				OutputSummary:     fmt.Sprintf("selected=%d", len(fallbackSelected)),
				MatchedFileCount:  len(fallbackSelected),
				SelectedFileCount: len(fallbackSelected),
				PathHashes:        pathHashesForCandidates(fallbackSelected, len(fallbackSelected)),
				Confidence:        0.58,
				ElapsedMS:         time.Since(start).Milliseconds(),
			})
		}
	}
	trace.TotalFilesSelected = len(selected)
	annotateInvestigationTraceToolCalls(trace)
	finalizeInvestigationQuestions(trace)
	trace.CompletedAt = time.Now().UTC()
	trace.Summary = fmt.Sprintf("工具化调查选择 %d/%d 个文件用于结构化读取；%s", len(selected), len(candidates), investigationQuestionProgressSummary(trace))
	return ProjectInvestigationResult{SelectedCandidates: selected, Trace: trace}, nil
}

func (s *ProjectInvestigationToolSuite) planCodeInvestigationQueries(ctx context.Context, candidates []codeCandidateFile, budget model.CodeReadBudget, project *model.ProjectContext, brief *model.RequirementBrief, questions []model.CodeInvestigationQuestion) ([]codeInvestigationQuery, model.CodeInvestigationToolCall) {
	start := time.Now()
	fallback := buildCodeInvestigationQueries(project, brief, budget, questions)
	call := model.CodeInvestigationToolCall{
		ID:           "tool_plan_investigation_" + shortHash(strings.Join(codeIntentTextParts(project, brief), "|")),
		Tool:         "plan_investigation",
		Purpose:      "根据需求调查问题、精简仓库地图和 tool_cards 决定首轮最小调查工具；不读取完整源码，不执行任意 shell。",
		InputSummary: fmt.Sprintf("candidate_files=%d investigation_questions=%d deterministic_queries=%d", len(candidates), len(questions), len(fallback)),
		Confidence:   0.58,
	}
	if s == nil || s.llm == nil {
		call.OutputSummary = fmt.Sprintf("LLM 不可用，使用确定性查询计划 %d 条。", len(fallback))
		call.SelectedFileCount = len(fallback)
		call.FallbackReason = "llm_not_configured"
		call.ElapsedMS = time.Since(start).Milliseconds()
		return fallback, call
	}
	payload := map[string]any{
		"intent":                  strings.Join(codeIntentTextParts(project, brief), "\n"),
		"investigation_questions": compactInvestigationQuestionsForPlanner(questions),
		"budget":                  budget,
		"repo_map":                compactRepoMapForInvestigation(candidates, 120),
		"tool_cards":              projectInvestigationToolCards(),
		"allowed_initial_tools":   []string{"grep_text", "shell_run"},
		"instructions":            []string{"围绕 investigation_questions 返回 2-5 个初始调查计划；优先选择最小必要工具。", "默认使用 grep_text；如果需要先像 CLI 助手一样看文件名/文件清单，可使用 shell_run，但只能设置 command_kind=git_ls_files、rg_files 或 rg_search_summary。", "terms 必须是业务语义、路由、组件、API、状态、文件名或样式关键词；git_ls_files/rg_files 可在必要时少量或不带 terms。", "不要输出任意 shell 命令、绝对路径、源码片段、token、cookie、Authorization。", "不要把 /aigc、/.well-known、/v1/execution-packages、app-installations 等控制面路径作为产品证据。"},
	}
	data, _ := json.Marshal(payload)
	var output codeInvestigationLLMOutput
	modelTrace, err := s.llm.GenerateJSON(ctx, config.ModelTaskCodeReading, llm.JSONRequest{
		System:       "你是 Cascade DemoOps 的代码调查 planner。你的任务像 Codex 一样先决定应该调用哪个本地安全工具，再逐步定位少量需求相关文件。你不能要求任意 shell，不能要求完整源码，不能输出敏感数据。",
		User:         string(data),
		SchemaName:   "CodeInvestigationPlan",
		ResponseHint: "返回字段：summary, queries[{purpose, tool, terms[], query, command_kind, expected_evidence[]}], confidence。tool 只能是 grep_text 或 shell_run；shell_run 只能用 command_kind=git_ls_files/rg_files/rg_search_summary。",
		MaxTokens:    1200,
		Temperature:  0.12,
	}, &output)
	call.ElapsedMS = time.Since(start).Milliseconds()
	if modelTrace != nil {
		call.EvidenceRefs = append(call.EvidenceRefs, model.EvidenceRef{
			ID:         "ev_code_investigation_planner_" + shortHash(modelTrace.Label()),
			Kind:       model.EvidenceKindSourceCode,
			Summary:    "CodeReaderAgent 调查计划模型路由：" + modelTrace.Label(),
			FieldPath:  "code_snapshot.investigation_trace.tool_calls.plan_investigation",
			Confidence: 0.62,
		})
	}
	if err != nil {
		call.OutputSummary = fmt.Sprintf("模型调查计划不可用，使用确定性查询计划 %d 条。", len(fallback))
		call.SelectedFileCount = len(fallback)
		call.FallbackReason = llmFallbackReason(err, modelTrace)
		return fallback, call
	}
	plannedQueries := codeInvestigationQueriesFromLLM(output, budget, questions)
	if len(plannedQueries) == 0 {
		call.OutputSummary = fmt.Sprintf("模型未返回有效查询，使用确定性查询计划 %d 条。", len(fallback))
		call.SelectedFileCount = len(fallback)
		call.FallbackReason = "empty_llm_plan"
		return fallback, call
	}
	queries := append(requirementCriticalInvestigationQueries(fallback), plannedQueries...)
	queries = append(queries, fallback...)
	queries = dedupeCodeInvestigationQueries(queries)
	if budget.DrilldownRounds > 0 && len(queries) > budget.DrilldownRounds {
		queries = queries[:budget.DrilldownRounds]
	}
	call.Query = querySummaryForInvestigation(queries)
	call.OutputSummary = fmt.Sprintf("模型生成调查查询 %d 条：%s", len(queries), output.Summary)
	call.SelectedFileCount = len(queries)
	call.Confidence = maxFloat(0.68, output.Confidence)
	return queries, call
}

func requirementCriticalInvestigationQueries(queries []codeInvestigationQuery) []codeInvestigationQuery {
	out := []codeInvestigationQuery{}
	for _, wanted := range []string{"question_followup_requirement", "question_result_anchors", "question_build_completion", "question_playable_result"} {
		for _, query := range queries {
			if query.questionID == wanted {
				out = append(out, query)
				break
			}
		}
	}
	return out
}

func llmFallbackReason(err error, trace *llm.CallTrace) string {
	if trace != nil {
		if trace.FallbackReason != "" {
			return trace.FallbackReason
		}
		if trace.ErrorClass != "" {
			return trace.ErrorClass
		}
	}
	if err == nil {
		return ""
	}
	if llm.IsDeterministicFallback(err) {
		return "deterministic_fallback"
	}
	return "planner_error"
}

func codeInvestigationQueriesFromLLM(output codeInvestigationLLMOutput, budget model.CodeReadBudget, questions []model.CodeInvestigationQuestion) []codeInvestigationQuery {
	queries := []codeInvestigationQuery{}
	for _, item := range output.Queries {
		terms := sanitizeInvestigationTerms(stringSlice(item.Terms))
		tool := sanitizeInvestigationToolName(item.Tool)
		if tool == "" {
			tool = "grep_text"
		}
		commandKind := sanitizeShellRunCommandKind(item.CommandKind, item.Command)
		if tool == "shell_run" && commandKind == "" {
			commandKind = shellRunCommandKindForTerms(terms)
		}
		if investigationToolRequiresTerms(tool, commandKind) && len(terms) == 0 {
			continue
		}
		question := closestInvestigationQuestion(terms, questions)
		expected := sanitizeExpectedEvidenceKinds(stringSlice(item.ExpectedEvidence))
		if len(expected) == 0 {
			expected = question.ExpectedEvidence
		}
		queryText := strings.Join(terms, " OR ")
		if queryText == "" {
			queryText = commandKind
		}
		queries = append(queries, codeInvestigationQuery{
			questionID:       question.ID,
			purpose:          firstNonEmpty(item.Purpose, "按模型规划的业务语义检索相关代码。"),
			query:            queryText,
			terms:            terms,
			expectedEvidence: expected,
			plannedFrom:      "initial_plan",
			suggestedTool:    tool,
			commandKind:      commandKind,
		})
	}
	if budget.DrilldownRounds > 0 && len(queries) > budget.DrilldownRounds {
		queries = queries[:budget.DrilldownRounds]
	}
	return queries
}

func dedupeCodeInvestigationQueries(values []codeInvestigationQuery) []codeInvestigationQuery {
	seen := map[string]bool{}
	out := make([]codeInvestigationQuery, 0, len(values))
	for _, value := range values {
		terms := sanitizeInvestigationTerms(value.terms)
		tool := sanitizeInvestigationToolName(value.suggestedTool)
		if tool == "" {
			tool = "grep_text"
		}
		commandKind := sanitizeShellRunCommandKind(value.commandKind, "")
		if tool == "shell_run" && commandKind == "" {
			commandKind = shellRunCommandKindForTerms(terms)
		}
		if investigationToolRequiresTerms(tool, commandKind) && len(terms) == 0 {
			continue
		}
		value.suggestedTool = tool
		value.commandKind = commandKind
		key := codeInvestigationQueryExecutionKey(value)
		if seen[key] {
			continue
		}
		seen[key] = true
		value.terms = terms
		if len(terms) > 0 {
			value.query = strings.Join(terms, " OR ")
		} else if value.query == "" {
			value.query = commandKind
		}
		out = append(out, value)
	}
	return out
}

func buildCodeInvestigationQuestions(project *model.ProjectContext, brief *model.RequirementBrief, budget model.CodeReadBudget) []model.CodeInvestigationQuestion {
	intentText := strings.Join(codeIntentTextParts(project, brief), " ")
	intentTerms := intentKeywordsForText(intentText)
	questions := []model.CodeInvestigationQuestion{}
	add := func(id string, label string, question string, terms []string, expected []string) {
		terms = sanitizeInvestigationTerms(terms)
		expected = uniqueStrings(expected)
		if id == "" || question == "" || len(terms) == 0 {
			return
		}
		questions = append(questions, model.CodeInvestigationQuestion{
			ID:               id,
			Question:         question,
			IntentLabel:      label,
			ExpectedEvidence: expected,
			QueryTerms:       terms,
			Status:           "open",
		})
	}
	if requirement := intentFollowUpRequirement(strings.Join(codeIntentTextParts(project, brief), "\n")); requirement != "" {
		add(
			"question_followup_requirement",
			"项目页补充构建需求",
			"项目创建后用于填写并提交完整补充需求的输入框、发送按钮和项目详情路由分别由哪些组件与稳定 selector 实现？",
			[]string{"input-chat", "button-send-chat", "ChatInputArea", "chat-panel", "project/:id", "补充需求", "完整需求"},
			[]string{"route", "component_or_selector", "style_or_state"},
		)
	}
	resultAnchorTerms := []string{}
	if wantsBuildCompletion(intentText) {
		resultAnchorTerms = append(resultAnchorTerms, "build-result-card")
	}
	if wantsPlayableKeyboardVerification(intentText) {
		resultAnchorTerms = append(resultAnchorTerms, "preview-iframe")
	}
	if len(resultAnchorTerms) > 0 {
		add(
			"question_result_anchors",
			"最终结果代码锚点",
			"哪些精确的稳定 selector 可以证明构建完成并定位可玩预览？",
			resultAnchorTerms,
			[]string{"component_or_selector"},
		)
	}
	if wantsBuildCompletion(intentText) {
		add(
			"question_build_completion",
			"Agent 构建完成结果",
			"Agent 全部步骤完成时，哪个结果组件、稳定 selector 和状态分支可以确定性证明构建完成？",
			[]string{"build-result-card", "build result", "build_complete", "all_complete", "all complete", "全部步骤完成", "构建完成", "completed"},
			[]string{"component_or_selector", "style_or_state"},
		)
	}
	if wantsPlayableKeyboardVerification(intentText) {
		add(
			"question_playable_result",
			"可玩预览与键盘结果",
			"最终预览容器、iframe/canvas、得分和键盘事件由哪些组件与稳定 selector 实现？",
			[]string{"preview-iframe", "preview panel", "iframe", "canvas", "tetris", "score", "ArrowLeft", "ArrowDown"},
			[]string{"component_or_selector", "style_or_state"},
		)
	}
	if containsAnyNormalized(intentText, "登录", "登陆", "login", "signin", "邮箱", "密码") {
		add(
			"question_session_setup",
			"登录与会话建立",
			"登录入口、认证表单、会话进入工作台的代码路径在哪里？",
			[]string{"登录", "登陆", "login", "signin", "sign", "email", "password", "auth", "session", "workspace", "dashboard"},
			[]string{"route", "component_or_selector", "api_or_data_model"},
		)
	}
	projectName := intentProjectName(intentText)
	if containsAnyNormalized(intentText, "新建项目", "创建项目", "新增项目", "project") || projectName != "" {
		question := "新建项目流程、项目名称输入和创建接口分别由哪些文件实现？"
		if projectName != "" {
			question = fmt.Sprintf("新建项目流程、项目名称输入、用户指定名称“%s”的填充语义和创建接口分别由哪些文件实现？", projectName)
		}
		add(
			"question_project_creation",
			"新建项目",
			question,
			append([]string{"新建项目", "创建项目", "新增项目", "new project", "create project", "project name", "new", "create", "project", "projects", "项目", "项目名称"}, projectName),
			[]string{"route", "component_or_selector", "api_or_data_model"},
		)
	}
	if containsAnyNormalized(intentText, "构建模式", "build mode", "agent", "构建", "生成") {
		add(
			"question_build_mode_agent",
			"构建模式与 Agent 执行",
			"构建模式选择、启动 agent、进度/日志/项目详情状态由哪些组件和后端能力支撑？",
			[]string{"构建模式", "build mode", "builder mode", "mode", "agent", "开始构建", "启动构建", "generate", "build", "builder", "progress", "log"},
			[]string{"route", "component_or_selector", "api_or_data_model", "style_or_state"},
		)
	}
	if containsAnyNormalized(intentText, "样式", "style", "视觉", "页面", "组件") {
		add(
			"question_relevant_style_state",
			"需求相关样式与状态",
			"需求相关页面的样式、loading/progress 状态和交互反馈在哪里定义？",
			[]string{"style", "className", "css", "tailwind", "state", "loading", "progress"},
			[]string{"component_or_selector", "style_or_state"},
		)
	}
	if len(questions) == 0 {
		add(
			"question_product_entry",
			"产品入口与核心流程",
			"产品入口、路由、核心组件和主要业务接口在哪里？",
			append(intentTerms, "route", "router", "page", "component", "button", "form", "project", "dashboard"),
			[]string{"route", "component_or_selector", "api_or_data_model"},
		)
	}
	limit := budget.DrilldownRounds + 2
	if limit <= 0 {
		limit = 4
	}
	if len(questions) > limit {
		questions = questions[:limit]
	}
	return questions
}

func compactInvestigationQuestionsForPlanner(questions []model.CodeInvestigationQuestion) []map[string]any {
	out := make([]map[string]any, 0, len(questions))
	for _, question := range questions {
		out = append(out, map[string]any{
			"id":                question.ID,
			"question":          question.Question,
			"intent_label":      question.IntentLabel,
			"expected_evidence": question.ExpectedEvidence,
			"query_terms":       question.QueryTerms,
		})
	}
	return out
}

func closestInvestigationQuestion(terms []string, questions []model.CodeInvestigationQuestion) model.CodeInvestigationQuestion {
	best := model.CodeInvestigationQuestion{}
	bestScore := 0
	for _, question := range questions {
		score := keywordMatchScore(terms, strings.Join(question.QueryTerms, " "), question.Question, question.IntentLabel)
		if score > bestScore {
			bestScore = score
			best = question
		}
	}
	if best.ID == "" {
		return model.CodeInvestigationQuestion{
			ID:               "question_ad_hoc",
			ExpectedEvidence: []string{"route", "component_or_selector", "api_or_data_model"},
		}
	}
	return best
}

func investigationQueryMetadata(query codeInvestigationQuery) map[string]any {
	metadata := map[string]any{}
	if query.questionID != "" {
		metadata["question_id"] = query.questionID
	}
	if len(query.expectedEvidence) > 0 {
		metadata["expected_evidence"] = query.expectedEvidence
	}
	if query.plannedFrom != "" {
		metadata["planned_from"] = query.plannedFrom
	}
	if query.suggestedTool != "" {
		metadata["suggested_tool"] = query.suggestedTool
	}
	if query.commandKind != "" {
		metadata["command_kind"] = query.commandKind
	}
	if query.dependsOnToolID != "" {
		metadata["depends_on_tool_call_id"] = query.dependsOnToolID
	}
	if len(metadata) == 0 {
		return nil
	}
	return metadata
}

func investigationQueryMetadataWithToolPolicy(query codeInvestigationQuery, policy codeInvestigationToolPolicy) map[string]any {
	metadata := investigationQueryMetadata(query)
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["tool_policy"] = []string{}
	tools := []string{}
	if policy.grepText {
		tools = append(tools, "grep_text")
	}
	if policy.followImports {
		tools = append(tools, "follow_imports")
	}
	if policy.findReferences {
		tools = append(tools, "find_references")
	}
	if policy.findAPIHandlers {
		tools = append(tools, "find_api_handlers")
	}
	if policy.readWindow {
		tools = append(tools, "read_window")
	}
	if policy.listRelatedFiles {
		tools = append(tools, "list_related_files")
	}
	if policy.inspectFileOutline {
		tools = append(tools, "inspect_file_outline")
	}
	if policy.shellRun {
		tools = append(tools, "shell_run")
	}
	metadata["tool_policy"] = tools
	return metadata
}

func toolPolicyForInvestigationQuery(query codeInvestigationQuery) codeInvestigationToolPolicy {
	policy := codeInvestigationToolPolicy{
		grepText:         true,
		followImports:    true,
		findReferences:   true,
		findAPIHandlers:  false,
		expectedEvidence: uniqueStrings(query.expectedEvidence),
	}
	suggested := sanitizeInvestigationToolName(query.suggestedTool)
	switch suggested {
	case "inspect_file_outline":
		policy.grepText = false
		policy.followImports = false
		policy.findReferences = false
		policy.findAPIHandlers = false
		policy.readWindow = false
		policy.listRelatedFiles = false
		policy.inspectFileOutline = true
		return policy
	case "list_related_files":
		policy.grepText = false
		policy.followImports = false
		policy.findReferences = false
		policy.findAPIHandlers = false
		policy.readWindow = false
		policy.listRelatedFiles = true
		return policy
	case "read_window":
		policy.grepText = false
		policy.followImports = false
		policy.findReferences = false
		policy.findAPIHandlers = false
		policy.readWindow = true
		return policy
	case "follow_imports":
		policy.followImports = true
		policy.findReferences = false
		policy.findAPIHandlers = false
		return policy
	case "find_references":
		policy.grepText = false
		policy.followImports = false
		policy.findReferences = true
		policy.findAPIHandlers = false
		return policy
	case "find_api_handlers":
		policy.grepText = query.plannedFrom == "next_actions" && sanitizeInvestigationToolName(query.parentTool) != "shell_run"
		policy.followImports = false
		policy.findReferences = true
		policy.findAPIHandlers = true
		return policy
	case "shell_run":
		policy.grepText = false
		policy.followImports = false
		policy.findReferences = false
		policy.findAPIHandlers = false
		policy.readWindow = false
		policy.listRelatedFiles = false
		policy.inspectFileOutline = false
		policy.shellRun = true
		return policy
	case "grep_text":
		if expectedOnlyStyleOrRoute(query.expectedEvidence) {
			policy.findAPIHandlers = false
			return policy
		}
	}
	if expectedIncludesEvidence(query.expectedEvidence, "style_or_state") && !expectedIncludesEvidence(query.expectedEvidence, "api_or_data_model") {
		policy.findAPIHandlers = false
		return policy
	}
	if expectedIncludesEvidence(query.expectedEvidence, "api_or_data_model") {
		policy.findAPIHandlers = true
	}
	return policy
}

func expectedIncludesEvidence(expected []string, value string) bool {
	for _, item := range expected {
		if item == value {
			return true
		}
	}
	return false
}

func expectedOnlyStyleOrRoute(expected []string) bool {
	if len(expected) == 0 {
		return false
	}
	for _, item := range expected {
		switch item {
		case "style_or_state", "route", "component_or_selector":
			continue
		default:
			return false
		}
	}
	return true
}

func compactRepoMapForInvestigation(candidates []codeCandidateFile, limit int) map[string]any {
	manifest := []string{}
	entrypoints := []string{}
	routeLike := []string{}
	componentLike := []string{}
	apiLike := []string{}
	styleLike := []string{}
	directories := map[string]int{}
	for _, candidate := range candidates {
		rel := filepath.ToSlash(candidate.rel)
		lower := strings.ToLower(rel)
		dir := firstPathSegment(rel)
		if dir != "" {
			directories[dir]++
		}
		switch {
		case strings.EqualFold(candidate.name, "package.json") || strings.EqualFold(candidate.name, "go.mod"):
			manifest = append(manifest, rel)
		case isEntrypointFile(candidate.name, candidate.rel):
			entrypoints = append(entrypoints, rel)
		case pathHasAnySegment(lower, "routes", "router", "pages", "app"):
			routeLike = append(routeLike, rel)
		case pathHasAnySegment(lower, "components", "features", "views"):
			componentLike = append(componentLike, rel)
		case pathHasAnySegment(lower, "api", "server", "backend", "internal", "cmd", "pkg"):
			apiLike = append(apiLike, rel)
		case containsAny(lower, "style", ".css", ".scss", ".sass", ".less", "tailwind"):
			styleLike = append(styleLike, rel)
		}
	}
	return map[string]any{
		"file_count":      len(candidates),
		"directories":     topDirectoryCounts(directories, 24),
		"manifests":       limitStrings(uniqueStrings(manifest), 16),
		"entrypoints":     limitStrings(uniqueStrings(entrypoints), 24),
		"route_like":      limitStrings(uniqueStrings(routeLike), limit),
		"component_like":  limitStrings(uniqueStrings(componentLike), limit),
		"api_like":        limitStrings(uniqueStrings(apiLike), limit),
		"style_like":      limitStrings(uniqueStrings(styleLike), 40),
		"shell_metadata":  manifestMetadataFromCandidates(candidates),
		"omitted_details": "repo_map contains relative paths only; no source content or absolute local paths.",
	}
}

func projectInvestigationToolCards() []map[string]any {
	return []map[string]any{
		{
			"tool":             "shell_metadata",
			"phase":            "startup",
			"when_to_use":      "建立仓库技术栈、manifest、依赖和 git 文件数量的只读背景。",
			"input_contract":   "自动运行；planner 不需要传命令。",
			"output_contract":  "manifest/package manager/dependency names/git tracked count；不返回 scripts 命令值、源码或绝对路径。",
			"persistent_trace": "safe metadata only",
			"cost":             "very_low",
		},
		{
			"tool":             "grep_text",
			"phase":            "initial_and_followup",
			"when_to_use":      "用业务关键词、route、组件名、selector、API path 或状态词定位第一批候选文件。",
			"input_contract":   "query_terms[2-8]；禁止控制面路径、shell 语法、secret/token/cookie。",
			"output_contract":  "path hashes, snippet refs, transient redacted snippet observations",
			"persistent_trace": "hashes and snippet line refs only",
			"cost":             "bounded_by_tool_search_budget",
		},
		{
			"tool":             "shell_run",
			"phase":            "followup",
			"when_to_use":      "需要像 CLI 助手一样做只读仓库探测时使用；仅支持 git/rg 类 discovery 和匹配摘要。",
			"input_contract":   "command_kind=git_ls_files|rg_files|rg_search_summary，query_terms[0-8]；禁止传任意 shell 命令、路径写入、网络命令或 secret。若模型给出 command 字符串，后端只会映射到安全 command_kind。",
			"output_contract":  "command_kind、文件计数、扩展名分布、path hashes；不返回源码行、完整相对路径、绝对路径或 shell stdout。",
			"persistent_trace": "safe command category, counts and hashes only",
			"cost":             "low_bounded_read_only_shell",
		},
		{
			"tool":             "list_related_files",
			"phase":            "followup",
			"when_to_use":      "已有命中文件后，先看同目录/父目录附近有哪些 route/component/API/client/style 文件。",
			"input_contract":   "query_terms from current business target or matched file name.",
			"output_contract":  "file_name, kind, dir_hash, path_hash, score, reason；不读取源码，不返回路径。",
			"persistent_trace": "hashes and counts only",
			"cost":             "very_low",
		},
		{
			"tool":             "inspect_file_outline",
			"phase":            "followup",
			"when_to_use":      "已有候选文件名但不确定其作用时，读取少量候选文件的结构轮廓。",
			"input_contract":   "query_terms naming a matched file/component/API client/route symbol.",
			"output_contract":  "file_name, symbol_names, route_paths, api_paths, selector_hints, data_model_names；不输出源码。",
			"persistent_trace": "hashes and aggregate counts only",
			"cost":             "low_bounded_file_reads",
		},
		{
			"tool":             "read_window",
			"phase":            "followup",
			"when_to_use":      "需要比 grep snippet 更近的局部上下文来判断下一步符号或业务状态。",
			"input_contract":   "query_terms tied to previous snippet refs; no arbitrary path.",
			"output_contract":  "transient redacted local window observations plus snippet hashes.",
			"persistent_trace": "hashes and line refs only",
			"cost":             "low_bounded_windows",
		},
		{
			"tool":             "follow_imports",
			"phase":            "followup",
			"when_to_use":      "命中文件直接 import 了相关组件、hook、client 或样式模块时沿 import 小步追踪。",
			"input_contract":   "uses currently selected files only.",
			"output_contract":  "selected imported file hashes; no source text.",
			"persistent_trace": "hashes and import counts only",
			"cost":             "low",
		},
		{
			"tool":             "find_references",
			"phase":            "followup",
			"when_to_use":      "已有组件名、函数名、selector 或 API symbol，需要找父级 route/调用方。",
			"input_contract":   "symbol terms extracted from selected files.",
			"output_contract":  "referencing file hashes; no source text.",
			"persistent_trace": "hashes and term hashes only",
			"cost":             "medium_bounded_search",
		},
		{
			"tool":             "find_api_handlers",
			"phase":            "followup",
			"when_to_use":      "前端代码暴露 /api path 或 mutation，需要追踪后端 handler/schema/model。",
			"input_contract":   "api_or_data_model expected evidence and API paths from selected files.",
			"output_contract":  "backend handler/model file hashes; no source text.",
			"persistent_trace": "hashes and API path hashes only",
			"cost":             "medium_bounded_search",
		},
	}
}

func firstPathSegment(rel string) string {
	parts := strings.Split(strings.Trim(filepath.ToSlash(rel), "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func topDirectoryCounts(values map[string]int, limit int) []map[string]any {
	type item struct {
		name  string
		count int
	}
	items := make([]item, 0, len(values))
	for name, count := range values {
		items = append(items, item{name: name, count: count})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].count == items[j].count {
			return items[i].name < items[j].name
		}
		return items[i].count > items[j].count
	})
	out := make([]map[string]any, 0, minInt(len(items), limit))
	for _, item := range items {
		if len(out) >= limit {
			break
		}
		out = append(out, map[string]any{"name": item.name, "count": item.count})
	}
	return out
}

func querySummaryForInvestigation(queries []codeInvestigationQuery) string {
	parts := make([]string, 0, len(queries))
	for _, query := range queries {
		parts = append(parts, query.query)
	}
	return strings.Join(parts, " | ")
}

func splitQueryTerms(query string) []string {
	return strings.FieldsFunc(query, func(r rune) bool {
		return r == '|' || r == ',' || r == ';' || r == '\n' || r == '\r' || r == '，' || r == '；'
	})
}

func sanitizeInvestigationTerms(terms []string) []string {
	out := []string{}
	for _, term := range terms {
		term = strings.TrimSpace(term)
		if term == "" || len([]rune(term)) > 40 {
			continue
		}
		lower := strings.ToLower(term)
		if strings.HasPrefix(lower, "-") || containsAny(lower, "&&", "||", ";", "`", "$(", "powershell", "cmd.exe", "bash ", "sh ") {
			continue
		}
		if pathHasForbiddenPrefix(lower, []string{"/aigc", "/.well-known", "/v1/execution-packages", "/v1/result-packages", "/v1/app-installations", "/execution-packages", "/result-packages", "/app-installations"}) {
			continue
		}
		if containsAny(lower, "authorization", "cookie", "bearer ", "cascade-exchange") {
			continue
		}
		out = append(out, term)
	}
	return limitStrings(uniqueStrings(out), 8)
}

func buildCodeInvestigationQueries(project *model.ProjectContext, brief *model.RequirementBrief, budget model.CodeReadBudget, questions []model.CodeInvestigationQuestion) []codeInvestigationQuery {
	if len(questions) > 0 {
		queries := make([]codeInvestigationQuery, 0, len(questions))
		for _, question := range questions {
			terms := sanitizeInvestigationTerms(question.QueryTerms)
			if len(terms) == 0 {
				continue
			}
			queries = append(queries, codeInvestigationQuery{
				questionID:       question.ID,
				purpose:          question.Question,
				query:            strings.Join(terms, " OR "),
				terms:            terms,
				expectedEvidence: question.ExpectedEvidence,
			})
		}
		if len(queries) > 0 {
			if budget.DrilldownRounds > 0 && len(queries) > budget.DrilldownRounds {
				return queries[:budget.DrilldownRounds]
			}
			return queries
		}
	}
	intentText := strings.Join(codeIntentTextParts(project, brief), " ")
	intentTerms := intentKeywordsForText(intentText)
	queries := []codeInvestigationQuery{}
	add := func(purpose string, terms []string) {
		terms = uniqueStrings(terms)
		if len(terms) == 0 {
			return
		}
		queries = append(queries, codeInvestigationQuery{
			purpose:          purpose,
			query:            strings.Join(terms, " OR "),
			terms:            terms,
			expectedEvidence: []string{"route", "component_or_selector", "api_or_data_model"},
		})
	}
	if len(intentTerms) > 0 {
		add("按用户演示需求关键词检索 route/component/API/selector 代码证据。", intentTerms)
	}
	if containsAnyNormalized(intentText, "登录", "登陆", "login", "signin", "邮箱", "密码") {
		add("定位登录入口、账号表单和认证状态代码。", []string{"登录", "登陆", "login", "signin", "email", "password", "auth"})
	}
	projectName := intentProjectName(intentText)
	if containsAnyNormalized(intentText, "新建项目", "创建项目", "新增项目", "project") || projectName != "" {
		add("定位新建项目流程、项目名称输入和创建 API。", append([]string{"新建项目", "创建项目", "新增项目", "new project", "create project", "project name", "项目名称"}, projectName))
	}
	if containsAnyNormalized(intentText, "构建模式", "build mode", "agent", "构建", "生成") {
		add("定位构建模式、agent 启动、进度日志和项目详情页面。", []string{"构建模式", "build mode", "agent", "开始构建", "启动构建", "generate", "build", "progress", "log"})
	}
	if containsAnyNormalized(intentText, "样式", "style", "视觉", "页面", "组件") {
		add("定位需求相关页面组件的样式和状态定义。", []string{"style", "className", "css", "theme", "state", "loading", "progress"})
	}
	if len(queries) == 0 {
		add("需求文本较少，检索产品入口、路由和核心组件线索。", []string{"route", "router", "page", "component", "button", "form", "project", "dashboard"})
	}
	if budget.DrilldownRounds > 0 && len(queries) > budget.DrilldownRounds {
		queries = queries[:budget.DrilldownRounds]
	}
	return queries
}

func codeIntentTextParts(project *model.ProjectContext, brief *model.RequirementBrief) []string {
	parts := []string{}
	if project != nil {
		parts = append(parts, project.ProductDescription, project.TargetAudience, project.Name)
		parts = append(parts, project.MustShow...)
		if project.Inputs != nil {
			parts = append(parts, project.Inputs.RawUserPrompt)
			for _, doc := range project.Inputs.RequirementDocuments {
				parts = append(parts, doc.Title, doc.Body)
			}
		}
	}
	if brief != nil {
		parts = append(parts, brief.Scenario, brief.Objective, brief.PrimaryOutcome, brief.TargetAudience)
		parts = append(parts, brief.MustShow...)
	}
	return parts
}

func searchCodeCandidatesForQuery(ctx context.Context, candidates []codeCandidateFile, selectedKeys map[string]bool, query codeInvestigationQuery, budget model.CodeReadBudget) (codeSearchResult, error) {
	result := codeSearchResult{}
	for _, candidate := range prioritizeCodeCandidatesForQuery(candidates, query) {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if selectedKeys[candidate.rel] || !candidateWorthToolSearch(candidate, query) {
			continue
		}
		if result.searched >= budget.ToolSearchFileLimit {
			break
		}
		score := keywordMatchScore(query.terms, filepath.ToSlash(candidate.rel), candidate.name) * 30
		matchedTerms := matchedTermsForText(query.terms, filepath.ToSlash(candidate.rel)+" "+candidate.name)
		snippetRefs := []model.CodeSnippetRef{}
		snippetObservations := []codeSnippetObservation{}
		if candidate.size <= budget.ToolSearchBytesPerFile {
			data, err := os.ReadFile(candidate.path)
			if err == nil {
				result.searched++
				result.bytesRead += int64(len(data))
				text := string(data)
				contentScore := keywordMatchScore(query.terms, text)
				if contentScore > 0 {
					score += contentScore*50 + minInt(len(data)/1024, 20)
					matchedTerms = uniqueStrings(append(matchedTerms, matchedTermsForText(query.terms, text)...))
					snippetRefs, snippetObservations = snippetEvidenceForQuery(candidate, data, query)
				}
			}
		} else if score > 0 {
			result.searched++
		}
		if score <= 0 {
			continue
		}
		if containsAny(strings.ToLower(filepath.ToSlash(candidate.rel)), "/fixtures/", "/mocks/", "/reports/", "/fonts/") {
			score -= 100
		}
		if score <= 0 {
			continue
		}
		result.matches = append(result.matches, codeSearchMatch{candidate: candidate, score: score + candidate.score/4, terms: matchedTerms, snippets: snippetRefs, snippetObservations: snippetObservations})
	}
	sort.SliceStable(result.matches, func(i, j int) bool {
		if result.matches[i].score == result.matches[j].score {
			return filepath.ToSlash(result.matches[i].candidate.rel) < filepath.ToSlash(result.matches[j].candidate.rel)
		}
		return result.matches[i].score > result.matches[j].score
	})
	result.matches = limitCodeSearchMatchesForQuery(result.matches, query.terms, budget.ToolSearchResultLimit)
	return result, nil
}

func prioritizeCodeCandidatesForQuery(candidates []codeCandidateFile, query codeInvestigationQuery) []codeCandidateFile {
	ordered := append([]codeCandidateFile(nil), candidates...)
	pathTerms := codeSearchPathTerms(query.terms)
	sort.SliceStable(ordered, func(i, j int) bool {
		left := keywordMatchScore(pathTerms, filepath.ToSlash(ordered[i].rel), ordered[i].name)*1000 + ordered[i].score
		right := keywordMatchScore(pathTerms, filepath.ToSlash(ordered[j].rel), ordered[j].name)*1000 + ordered[j].score
		if left == right {
			return filepath.ToSlash(ordered[i].rel) < filepath.ToSlash(ordered[j].rel)
		}
		return left > right
	})
	return ordered
}

func codeSearchPathTerms(terms []string) []string {
	out := append([]string(nil), terms...)
	replacer := strings.NewReplacer("-", " ", "_", " ", ".", " ", "/", " ", "\\", " ")
	for _, term := range terms {
		switch strings.ToLower(strings.TrimSpace(term)) {
		case "build-result-card":
			out = append(out, "plan", "chat", "agent")
		case "preview-iframe":
			out = append(out, "preview", "panel")
		}
		for _, part := range strings.Fields(replacer.Replace(term)) {
			part = strings.TrimSpace(part)
			if len(part) >= 4 {
				out = append(out, part)
			}
		}
	}
	return uniqueStrings(out)
}

func limitCodeSearchMatchesForQuery(matches []codeSearchMatch, terms []string, limit int) []codeSearchMatch {
	if limit <= 0 || len(matches) <= limit {
		return matches
	}
	selected := make([]codeSearchMatch, 0, limit)
	selectedPaths := map[string]bool{}
	for _, term := range terms {
		for _, match := range matches {
			if !containsStringFold(match.terms, term) || selectedPaths[match.candidate.rel] {
				continue
			}
			selected = append(selected, match)
			selectedPaths[match.candidate.rel] = true
			break
		}
		if len(selected) >= limit {
			return selected
		}
	}
	for _, match := range matches {
		if selectedPaths[match.candidate.rel] {
			continue
		}
		selected = append(selected, match)
		selectedPaths[match.candidate.rel] = true
		if len(selected) >= limit {
			break
		}
	}
	return selected
}

func containsStringFold(values []string, wanted string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(wanted)) {
			return true
		}
	}
	return false
}

func budgetForRemainingToolSearch(budget model.CodeReadBudget, searched int) (model.CodeReadBudget, bool) {
	if budget.ToolSearchFileLimit <= 0 {
		return budget, true
	}
	remaining := budget.ToolSearchFileLimit - searched
	if remaining <= 0 {
		return budget, false
	}
	if remaining < budget.ToolSearchFileLimit {
		budget.ToolSearchFileLimit = remaining
	}
	return budget, true
}

func derivedToolSearchLimit(limit int, divisor int, floor int) int {
	if limit <= 0 {
		return floor
	}
	if divisor <= 0 {
		divisor = 1
	}
	scaled := limit / divisor
	if scaled <= 0 {
		scaled = 1
	}
	if limit < floor {
		return limit
	}
	return maxInt(floor, scaled)
}

func codeCandidateIndex(candidates []codeCandidateFile) map[string]codeCandidateFile {
	index := map[string]codeCandidateFile{}
	for _, candidate := range candidates {
		rel := filepath.ToSlash(candidate.rel)
		lower := strings.ToLower(rel)
		index[lower] = candidate
		ext := strings.ToLower(filepath.Ext(rel))
		if ext != "" {
			index[strings.TrimSuffix(lower, ext)] = candidate
		}
		dir, file := filepath.Split(lower)
		if strings.HasPrefix(file, "index.") {
			index[strings.TrimSuffix(strings.TrimSuffix(dir, "/"), "\\")] = candidate
		}
	}
	return index
}

func followImportsFromCandidates(ctx context.Context, sources []codeCandidateFile, index map[string]codeCandidateFile, selectedKeys map[string]bool, budget model.CodeReadBudget, aliasRules []importAliasRule) ([]codeCandidateFile, importFollowScan, error) {
	scan := importFollowScan{
		aliasRuleCount:  len(aliasRules),
		aliasRuleHashes: importAliasRuleHashes(aliasRules, 8),
	}
	if len(sources) == 0 || len(index) == 0 {
		return nil, scan, nil
	}
	out := []codeCandidateFile{}
	seen := map[string]bool{}
	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			return out, scan, err
		}
		if source.size > budget.ToolSearchBytesPerFile {
			continue
		}
		data, err := os.ReadFile(source.path)
		if err != nil {
			continue
		}
		scan.sourceFileCount++
		scan.bytesRead += int64(len(data))
		imports := relativeImportSpecs(string(data), aliasRules)
		scan.importCount += len(imports)
		for _, spec := range imports {
			if len(out) >= minInt(8, maxInt(2, budget.FilesPerRound*2)) {
				return out, scan, nil
			}
			candidate, ok := resolveImportCandidate(source, spec, index, aliasRules)
			if !ok || selectedKeys[candidate.rel] || seen[candidate.rel] || shouldSkipCodeFileRel(candidate.rel, candidate.name) {
				continue
			}
			seen[candidate.rel] = true
			out = append(out, candidate)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score == out[j].score {
			return filepath.ToSlash(out[i].rel) < filepath.ToSlash(out[j].rel)
		}
		return out[i].score > out[j].score
	})
	return out, scan, nil
}

func importAliasRulesFromCandidates(ctx context.Context, candidates []codeCandidateFile, budget model.CodeReadBudget) ([]importAliasRule, importAliasScan, error) {
	scan := importAliasScan{}
	rules := []importAliasRule{}
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return rules, scan, err
		}
		if !candidateWorthAliasConfig(candidate) {
			continue
		}
		if scan.configFileCount >= 8 {
			break
		}
		if candidate.size > budget.ToolSearchBytesPerFile {
			continue
		}
		data, err := os.ReadFile(candidate.path)
		if err != nil {
			continue
		}
		scan.configFileCount++
		scan.bytesRead += int64(len(data))
		configRules := importAliasRulesFromJSONConfig(candidate.rel, data)
		rules = append(rules, configRules...)
	}
	rules = dedupeImportAliasRules(rules)
	scan.ruleCount = len(rules)
	return rules, scan, nil
}

func candidateWorthAliasConfig(candidate codeCandidateFile) bool {
	name := strings.ToLower(candidate.name)
	return name == "tsconfig.json" || name == "jsconfig.json" ||
		(strings.HasPrefix(name, "tsconfig.") && strings.HasSuffix(name, ".json")) ||
		(strings.HasPrefix(name, "jsconfig.") && strings.HasSuffix(name, ".json"))
}

func importAliasRulesFromJSONConfig(configRel string, data []byte) []importAliasRule {
	type tsConfig struct {
		CompilerOptions struct {
			BaseURL string              `json:"baseUrl"`
			Paths   map[string][]string `json:"paths"`
		} `json:"compilerOptions"`
	}
	var parsed tsConfig
	if err := json.Unmarshal(cleanJSONConfig(data), &parsed); err != nil {
		return nil
	}
	configDir := filepath.ToSlash(filepath.Dir(filepath.ToSlash(configRel)))
	if configDir == "." {
		configDir = ""
	}
	baseURL := strings.Trim(filepath.ToSlash(parsed.CompilerOptions.BaseURL), "/")
	rules := []importAliasRule{}
	for alias, targets := range parsed.CompilerOptions.Paths {
		for _, target := range targets {
			if rule, ok := importAliasRuleFromConfig(alias, target, configDir, baseURL); ok {
				rules = append(rules, rule)
			}
		}
	}
	return rules
}

func cleanJSONConfig(data []byte) []byte {
	text := string(data)
	lineComment := regexp.MustCompile(`(?m)//.*$`)
	blockComment := regexp.MustCompile(`(?s)/\*.*?\*/`)
	text = lineComment.ReplaceAllString(text, "")
	text = blockComment.ReplaceAllString(text, "")
	trailingComma := regexp.MustCompile(`,\s*([}\]])`)
	text = trailingComma.ReplaceAllString(text, "$1")
	return []byte(text)
}

func importAliasRuleFromConfig(alias string, target string, configDir string, baseURL string) (importAliasRule, bool) {
	alias = filepath.ToSlash(strings.TrimSpace(alias))
	target = filepath.ToSlash(strings.TrimSpace(target))
	if alias == "" || target == "" || strings.Contains(alias, "\x00") || strings.Contains(target, "\x00") {
		return importAliasRule{}, false
	}
	aliasPrefix := strings.TrimSuffix(alias, "*")
	targetPrefix := strings.TrimSuffix(target, "*")
	if aliasPrefix == "" || targetPrefix == "" || strings.Contains(aliasPrefix, "://") || strings.Contains(targetPrefix, "://") {
		return importAliasRule{}, false
	}
	if strings.HasPrefix(targetPrefix, "/") || strings.Contains(targetPrefix, ":") {
		return importAliasRule{}, false
	}
	baseParts := []string{}
	if configDir != "" {
		baseParts = append(baseParts, configDir)
	}
	if baseURL != "" && baseURL != "." {
		baseParts = append(baseParts, baseURL)
	}
	baseParts = append(baseParts, targetPrefix)
	targetRel := filepath.ToSlash(filepath.Clean(filepath.Join(baseParts...)))
	targetRel = strings.Trim(targetRel, "/")
	if targetRel == "." || targetRel == "" || strings.HasPrefix(targetRel, "../") || strings.Contains(targetRel, "/../") {
		return importAliasRule{}, false
	}
	lowerTarget := strings.ToLower(targetRel)
	if pathHasAnySegment(lowerTarget, "node_modules", "dist", "build", "out", ".next", "coverage", "vendor") {
		return importAliasRule{}, false
	}
	lowerAlias := strings.ToLower(aliasPrefix)
	if pathHasForbiddenPrefix(lowerAlias, []string{"/aigc", "/.well-known", "/v1/", "../"}) {
		return importAliasRule{}, false
	}
	return importAliasRule{Prefix: aliasPrefix, TargetPrefix: targetRel}, true
}

func dedupeImportAliasRules(values []importAliasRule) []importAliasRule {
	seen := map[string]bool{}
	out := []importAliasRule{}
	for _, value := range values {
		key := value.Prefix + "=>" + value.TargetPrefix
		if value.Prefix == "" || value.TargetPrefix == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if len(out[i].Prefix) == len(out[j].Prefix) {
			return out[i].Prefix < out[j].Prefix
		}
		return len(out[i].Prefix) > len(out[j].Prefix)
	})
	return out
}

func importAliasRuleHashes(rules []importAliasRule, limit int) []string {
	values := []string{}
	for _, rule := range rules {
		values = append(values, rule.Prefix+"=>"+rule.TargetPrefix)
	}
	return hashStringsForInvestigationTerms(values, limit)
}

func relativeImportSpecs(text string, aliasRules []importAliasRule) []string {
	specs := []string{}
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?m)\bimport\s+(?:[^'"]+\s+from\s+)?['"]([^'"]+)['"]`),
		regexp.MustCompile(`(?m)\bexport\s+[^'"]+\s+from\s+['"]([^'"]+)['"]`),
		regexp.MustCompile(`(?m)\brequire\(\s*['"]([^'"]+)['"]\s*\)`),
	}
	for _, pattern := range patterns {
		for _, match := range pattern.FindAllStringSubmatch(text, -1) {
			if len(match) < 2 {
				continue
			}
			spec := strings.TrimSpace(match[1])
			if importSpecAllowedForFollow(spec, aliasRules) {
				specs = append(specs, spec)
			}
		}
	}
	return limitStrings(uniqueStrings(specs), 24)
}

func importSpecAllowedForFollow(spec string, aliasRules []importAliasRule) bool {
	if spec == "" || strings.Contains(spec, "\x00") || strings.Contains(spec, "://") {
		return false
	}
	lower := strings.ToLower(spec)
	if strings.HasPrefix(lower, ".") || strings.HasPrefix(lower, "@/") || strings.HasPrefix(lower, "src/") {
		return !pathHasForbiddenPrefix(lower, []string{"/aigc", "/.well-known", "/v1/", "../..", "/node_modules", "/dist", "/build"})
	}
	for _, rule := range aliasRules {
		if rule.Prefix != "" && strings.HasPrefix(spec, rule.Prefix) {
			return !pathHasForbiddenPrefix(lower, []string{"/aigc", "/.well-known", "/v1/", "../..", "/node_modules", "/dist", "/build"})
		}
	}
	return false
}

func resolveImportCandidate(source codeCandidateFile, spec string, index map[string]codeCandidateFile, aliasRules []importAliasRule) (codeCandidateFile, bool) {
	candidates := importResolutionKeys(source, spec, aliasRules)
	for _, key := range candidates {
		if candidate, ok := index[strings.ToLower(filepath.ToSlash(key))]; ok {
			return candidate, true
		}
	}
	return codeCandidateFile{}, false
}

func importResolutionKeys(source codeCandidateFile, spec string, aliasRules []importAliasRule) []string {
	spec = filepath.ToSlash(strings.TrimSpace(spec))
	keys := []string{}
	addBase := func(base string) {
		base = strings.Trim(filepath.ToSlash(base), "/")
		if base == "" {
			return
		}
		keys = append(keys, base)
		for _, ext := range []string{".tsx", ".ts", ".jsx", ".js", ".vue", ".svelte", ".go", ".json", ".css", ".scss"} {
			keys = append(keys, base+ext)
		}
		for _, ext := range []string{".tsx", ".ts", ".jsx", ".js", ".vue", ".svelte"} {
			keys = append(keys, filepath.ToSlash(filepath.Join(base, "index"+ext)))
		}
	}
	switch {
	case strings.HasPrefix(spec, "."):
		base := filepath.Clean(filepath.Join(filepath.Dir(filepath.ToSlash(source.rel)), spec))
		addBase(base)
	case strings.HasPrefix(spec, "@/"):
		addBase("src/" + strings.TrimPrefix(spec, "@/"))
	case strings.HasPrefix(spec, "src/"):
		addBase(spec)
	}
	for _, rule := range aliasRules {
		if rule.Prefix == "" || !strings.HasPrefix(spec, rule.Prefix) {
			continue
		}
		remainder := strings.TrimPrefix(spec, rule.Prefix)
		addBase(filepath.ToSlash(filepath.Join(rule.TargetPrefix, remainder)))
	}
	return uniqueStrings(keys)
}

func followImportsToolCall(id string, query codeInvestigationQuery, scan importFollowScan, selected []codeCandidateFile) model.CodeInvestigationToolCall {
	return model.CodeInvestigationToolCall{
		ID:                id,
		Tool:              "follow_imports",
		Purpose:           "从 grep 命中文件小步跟随相对 import / src alias 到直接相关组件、hook、API 模块；不执行代码，不展开第三方包。",
		Query:             query.query,
		InputSummary:      fmt.Sprintf("source_files=%d imports_seen=%d", scan.sourceFileCount, scan.importCount),
		OutputSummary:     fmt.Sprintf("followed_import_files=%d", len(selected)),
		MatchedFileCount:  scan.importCount,
		SelectedFileCount: len(selected),
		PathHashes:        pathHashesForCandidates(selected, len(selected)),
		Metadata: map[string]any{
			"question_id":       query.questionID,
			"source_file_count": scan.sourceFileCount,
			"import_count":      scan.importCount,
			"alias_rule_count":  scan.aliasRuleCount,
			"alias_rule_hashes": scan.aliasRuleHashes,
			"policy":            "relative_imports_src_alias_and_configured_tsconfig_paths_only",
		},
		Confidence: 0.7,
	}
}

func findReferencesFromCandidates(ctx context.Context, seeds []codeCandidateFile, candidates []codeCandidateFile, selectedKeys map[string]bool, budget model.CodeReadBudget) ([]codeCandidateFile, referenceSearchScan, error) {
	scan := referenceSearchScan{}
	if len(seeds) == 0 || len(candidates) == 0 {
		return nil, scan, nil
	}
	terms := []string{}
	seedKeys := map[string]bool{}
	for _, seed := range seeds {
		if err := ctx.Err(); err != nil {
			return nil, scan, err
		}
		seedKeys[seed.rel] = true
		if seed.size > budget.ToolSearchBytesPerFile {
			continue
		}
		data, err := os.ReadFile(seed.path)
		if err != nil {
			continue
		}
		scan.seedFileCount++
		scan.bytesRead += int64(len(data))
		terms = append(terms, referenceTermsFromCandidate(seed, string(data))...)
	}
	terms = sanitizeInvestigationTerms(terms)
	terms = filterReferenceSearchTerms(terms)
	scan.terms = limitStrings(terms, 8)
	scan.termCount = len(scan.terms)
	if len(scan.terms) == 0 {
		return nil, scan, nil
	}
	type referenceMatch struct {
		candidate codeCandidateFile
		score     int
	}
	matches := []referenceMatch{}
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, scan, err
		}
		if selectedKeys[candidate.rel] || seedKeys[candidate.rel] || !candidateWorthReferenceSearch(candidate) {
			continue
		}
		if scan.searched >= derivedToolSearchLimit(budget.ToolSearchFileLimit, 2, 20) {
			break
		}
		score := keywordMatchScore(scan.terms, filepath.ToSlash(candidate.rel), candidate.name) * 10
		if candidate.size <= budget.ToolSearchBytesPerFile {
			data, err := os.ReadFile(candidate.path)
			if err == nil {
				scan.searched++
				scan.bytesRead += int64(len(data))
				contentScore := keywordMatchScore(scan.terms, string(data))
				if contentScore > 0 {
					score += contentScore*60 + candidate.score/5
				}
			}
		} else if score > 0 {
			scan.searched++
		}
		if score <= 0 {
			continue
		}
		scan.matched++
		matches = append(matches, referenceMatch{candidate: candidate, score: score})
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score == matches[j].score {
			return filepath.ToSlash(matches[i].candidate.rel) < filepath.ToSlash(matches[j].candidate.rel)
		}
		return matches[i].score > matches[j].score
	})
	out := []codeCandidateFile{}
	for _, match := range matches {
		if len(out) >= minInt(6, maxInt(2, budget.FilesPerRound*2)) {
			break
		}
		out = append(out, match.candidate)
	}
	return out, scan, nil
}

func referenceTermsFromCandidate(candidate codeCandidateFile, text string) []string {
	terms := []string{}
	base := strings.TrimSuffix(candidate.name, filepath.Ext(candidate.name))
	if referenceTermAllowed(base) {
		terms = append(terms, base)
	}
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`\b(?:export\s+)?(?:function|class|interface|type|const|let|var)\s+([A-Za-z_][A-Za-z0-9_]*)`),
		regexp.MustCompile(`\bdata-testid=["']([^"']+)["']`),
	}
	for _, pattern := range patterns {
		for _, match := range pattern.FindAllStringSubmatch(text, -1) {
			if len(match) < 2 {
				continue
			}
			if referenceTermAllowed(match[1]) {
				terms = append(terms, match[1])
			}
		}
	}
	return limitStrings(uniqueStrings(terms), 12)
}

func filterReferenceSearchTerms(terms []string) []string {
	out := []string{}
	for _, term := range terms {
		if referenceTermAllowed(term) {
			out = append(out, term)
		}
	}
	return uniqueStrings(out)
}

func referenceTermAllowed(term string) bool {
	term = strings.TrimSpace(term)
	if len([]rune(term)) < 4 || len([]rune(term)) > 40 {
		return false
	}
	lower := strings.ToLower(term)
	if containsAny(lower, "password", "secret", "token", "cookie", "authorization") {
		return false
	}
	switch lower {
	case "index", "page", "layout", "props", "state", "data", "item", "items", "button", "input", "form", "default", "string", "number":
		return false
	}
	return regexp.MustCompile(`^[A-Za-z0-9_:-]+$`).MatchString(term)
}

func candidateWorthReferenceSearch(candidate codeCandidateFile) bool {
	lower := strings.ToLower(filepath.ToSlash(candidate.rel))
	if shouldSkipCodeFileRel(candidate.rel, candidate.name) || strings.EqualFold(candidate.name, "package.json") || strings.EqualFold(candidate.name, "go.mod") {
		return false
	}
	return pathHasAnySegment(lower, "src", "app", "pages", "routes", "router", "components", "features", "server", "backend", "api", "internal", "cmd", "pkg")
}

func findReferencesToolCall(id string, query codeInvestigationQuery, scan referenceSearchScan, selected []codeCandidateFile) model.CodeInvestigationToolCall {
	return model.CodeInvestigationToolCall{
		ID:                id,
		Tool:              "find_references",
		Purpose:           "从已确认组件/API/selector 提取少量符号词，反向查找使用点、父级 route 或调用方；不输出源码文本，不扩大到全仓读取。",
		Query:             query.query,
		InputSummary:      fmt.Sprintf("seed_files=%d reference_terms=%d", scan.seedFileCount, scan.termCount),
		OutputSummary:     fmt.Sprintf("searched=%d matched=%d selected=%d", scan.searched, scan.matched, len(selected)),
		MatchedFileCount:  scan.matched,
		SelectedFileCount: len(selected),
		PathHashes:        pathHashesForCandidates(selected, len(selected)),
		Metadata: map[string]any{
			"question_id":           query.questionID,
			"seed_file_count":       scan.seedFileCount,
			"reference_term_hashes": hashStringsForInvestigationTerms(scan.terms, 8),
			"search_policy":         "symbol_and_selector_terms_only",
			"selection_policy":      "bounded_parent_usage_lookup",
		},
		Confidence: 0.68,
	}
}

func hashStringsForInvestigationTerms(values []string, limit int) []string {
	values = limitStrings(uniqueStrings(values), limit)
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, hashString(value))
	}
	return out
}

func findAPIHandlersFromCandidates(ctx context.Context, seeds []codeCandidateFile, candidates []codeCandidateFile, selectedKeys map[string]bool, budget model.CodeReadBudget) ([]codeCandidateFile, apiHandlerSearchScan, error) {
	scan := apiHandlerSearchScan{}
	if len(seeds) == 0 || len(candidates) == 0 {
		return nil, scan, nil
	}
	apiPaths := []string{}
	seedKeys := map[string]bool{}
	for _, seed := range seeds {
		if err := ctx.Err(); err != nil {
			return nil, scan, err
		}
		seedKeys[seed.rel] = true
		if seed.size > budget.ToolSearchBytesPerFile {
			continue
		}
		data, err := os.ReadFile(seed.path)
		if err != nil {
			continue
		}
		scan.seedFileCount++
		scan.bytesRead += int64(len(data))
		apiPaths = append(apiPaths, apiPathsFromText(string(data))...)
	}
	apiPaths = limitStrings(uniqueStrings(apiPaths), 8)
	scan.apiPaths = apiPaths
	scan.pathCount = len(apiPaths)
	if len(apiPaths) == 0 {
		return nil, scan, nil
	}
	type apiHandlerMatch struct {
		candidate codeCandidateFile
		score     int
	}
	matches := []apiHandlerMatch{}
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, scan, err
		}
		if selectedKeys[candidate.rel] || seedKeys[candidate.rel] || !candidateWorthAPIHandlerSearch(candidate) {
			continue
		}
		if scan.searched >= derivedToolSearchLimit(budget.ToolSearchFileLimit, 3, 16) {
			break
		}
		score := apiHandlerPathScore(apiPaths, filepath.ToSlash(candidate.rel), candidate.name) * 8
		if candidate.size <= budget.ToolSearchBytesPerFile {
			data, err := os.ReadFile(candidate.path)
			if err == nil {
				scan.searched++
				scan.bytesRead += int64(len(data))
				contentScore := apiHandlerContentScore(apiPaths, string(data))
				if contentScore > 0 {
					score += contentScore + candidate.score/5
				}
			}
		} else if score > 0 {
			scan.searched++
		}
		if score <= 0 {
			continue
		}
		scan.matched++
		matches = append(matches, apiHandlerMatch{candidate: candidate, score: score})
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score == matches[j].score {
			return filepath.ToSlash(matches[i].candidate.rel) < filepath.ToSlash(matches[j].candidate.rel)
		}
		return matches[i].score > matches[j].score
	})
	out := []codeCandidateFile{}
	for _, match := range matches {
		if len(out) >= minInt(6, maxInt(2, budget.FilesPerRound*2)) {
			break
		}
		out = append(out, match.candidate)
	}
	return out, scan, nil
}

func apiPathsFromText(text string) []string {
	paths := []string{}
	for _, match := range apiPattern.FindAllStringSubmatch(text, 40) {
		if len(match) < 2 {
			continue
		}
		path := strings.TrimSpace(match[1])
		if apiPathAllowedForCode(path) {
			paths = append(paths, path)
		}
	}
	return uniqueStrings(paths)
}

func candidateWorthAPIHandlerSearch(candidate codeCandidateFile) bool {
	lower := strings.ToLower(filepath.ToSlash(candidate.rel))
	if shouldSkipCodeFileRel(candidate.rel, candidate.name) || strings.EqualFold(candidate.name, "package.json") || strings.EqualFold(candidate.name, "go.mod") {
		return false
	}
	return pathHasAnySegment(lower, "api", "server", "backend", "internal", "cmd", "pkg", "routes", "router", "pages", "app")
}

func apiHandlerPathScore(apiPaths []string, values ...string) int {
	text := strings.ToLower(strings.Join(values, " "))
	score := 0
	for _, path := range apiPaths {
		for _, segment := range apiPathSegments(path) {
			if strings.Contains(text, segment) {
				score++
			}
		}
	}
	return score
}

func apiHandlerContentScore(apiPaths []string, text string) int {
	lower := strings.ToLower(text)
	score := 0
	for _, path := range apiPaths {
		pathLower := strings.ToLower(path)
		if strings.Contains(lower, pathLower) {
			score += 140
			continue
		}
		segments := apiPathSegments(path)
		if len(segments) == 0 {
			continue
		}
		matches := 0
		for _, segment := range segments {
			if strings.Contains(lower, segment) {
				matches++
			}
		}
		if matches >= minInt(2, len(segments)) {
			score += matches * 22
		}
	}
	return score
}

func apiPathSegments(path string) []string {
	path = strings.Trim(strings.ToLower(path), "/")
	if path == "" {
		return nil
	}
	out := []string{}
	for _, segment := range strings.Split(path, "/") {
		segment = strings.Trim(segment, "{}:")
		if segment == "" || segment == "api" || segment == "v1" || segment == "v2" || strings.HasPrefix(segment, "[") {
			continue
		}
		if len(segment) < 3 {
			continue
		}
		out = append(out, segment)
	}
	return uniqueStrings(out)
}

func findAPIHandlersToolCall(id string, query codeInvestigationQuery, scan apiHandlerSearchScan, selected []codeCandidateFile) model.CodeInvestigationToolCall {
	return model.CodeInvestigationToolCall{
		ID:                id,
		Tool:              "find_api_handlers",
		Purpose:           "从已确认前端组件/API 调用中提取允许的产品 API 路径，反查后端 route/handler 文件；不执行服务端代码，不输出 API 路径原文。",
		Query:             query.query,
		InputSummary:      fmt.Sprintf("seed_files=%d api_paths=%d", scan.seedFileCount, scan.pathCount),
		OutputSummary:     fmt.Sprintf("searched=%d matched=%d selected=%d", scan.searched, scan.matched, len(selected)),
		MatchedFileCount:  scan.matched,
		SelectedFileCount: len(selected),
		PathHashes:        pathHashesForCandidates(selected, len(selected)),
		Metadata: map[string]any{
			"question_id":      query.questionID,
			"seed_file_count":  scan.seedFileCount,
			"api_path_hashes":  hashStringsForInvestigationTerms(scan.apiPaths, 8),
			"search_policy":    "product_api_paths_only",
			"selection_policy": "bounded_backend_handler_lookup",
		},
		Confidence: 0.7,
	}
}

func reviewInvestigationEvidence(ctx context.Context, candidates []codeCandidateFile, project *model.ProjectContext, brief *model.RequirementBrief) (codeInvestigationEvidenceReview, error) {
	review := codeInvestigationEvidenceReview{}
	if len(candidates) == 0 {
		review.gaps = []string{"route", "component_or_selector", "api_or_data_model"}
		return review, nil
	}
	snapshot := &model.CodeUnderstandingSnapshot{ID: "evidence_review_" + shortHash(fmt.Sprintf("%d", len(candidates)))}
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return review, err
		}
		data, err := os.ReadFile(candidate.path)
		if err != nil {
			continue
		}
		pathHash := hashString(filepath.ToSlash(candidate.rel))
		text := string(data)
		inspectRoutes(text, pathHash, snapshot)
		inspectSelectors(text, pathHash, snapshot)
		inspectComponents(candidate.name, text, pathHash, snapshot)
		inspectAPIs(text, pathHash, snapshot)
		inspectDataModels(text, pathHash, snapshot)
		if candidateLooksBackendImplementation(candidate) {
			review.backendCount++
		}
		if codeKindForFile(candidate.name) == "style" || containsAny(strings.ToLower(filepath.ToSlash(candidate.rel)), ".css", ".scss", ".sass", ".less", "tailwind") || containsAny(text, "className", "loading", "progress") {
			review.styleCount++
		}
		review.fileCount++
		review.pathHashes = append(review.pathHashes, pathHash)
	}
	normalizeCodeSnapshotForIntent(snapshot, project, brief)
	review.routeCount = len(snapshot.Routes)
	review.componentCount = len(snapshot.Components)
	review.selectorCount = len(snapshot.Selectors)
	review.apiCount = len(snapshot.APIEndpoints)
	review.dataModelCount = len(snapshot.DataModels)
	review.pathHashes = uniqueStrings(review.pathHashes)
	return review, nil
}

func evidenceGapsForExpected(review codeInvestigationEvidenceReview, expected []string) []string {
	if len(expected) == 0 {
		expected = []string{"route", "component_or_selector", "api_or_data_model"}
	}
	gaps := []string{}
	for _, item := range uniqueStrings(expected) {
		switch item {
		case "route":
			if review.routeCount == 0 {
				gaps = append(gaps, "route")
			}
		case "component_or_selector":
			if review.componentCount == 0 && review.selectorCount == 0 {
				gaps = append(gaps, "component_or_selector")
			}
		case "api_or_data_model":
			if review.apiCount == 0 && review.dataModelCount == 0 {
				gaps = append(gaps, "api_or_data_model")
			} else if review.apiCount > 0 && review.dataModelCount == 0 && review.backendCount == 0 {
				gaps = append(gaps, "api_or_data_model")
			}
		case "style_or_state":
			if review.styleCount == 0 {
				gaps = append(gaps, "style_or_state")
			}
		}
	}
	return gaps
}

func evidenceReviewToolCall(round int, query codeInvestigationQuery, review codeInvestigationEvidenceReview) model.CodeInvestigationToolCall {
	metadata := map[string]any{
		"round":            round,
		"question_id":      query.questionID,
		"expected":         query.expectedEvidence,
		"file_count":       review.fileCount,
		"route_count":      review.routeCount,
		"component_count":  review.componentCount,
		"selector_count":   review.selectorCount,
		"api_count":        review.apiCount,
		"backend_count":    review.backendCount,
		"data_model_count": review.dataModelCount,
		"style_count":      review.styleCount,
		"gaps":             review.gaps,
	}
	return model.CodeInvestigationToolCall{
		ID:                fmt.Sprintf("tool_evidence_review_%d_%s", round, shortHash(query.query)),
		Tool:              "evidence_review",
		Purpose:           "读取本轮 grep 命中的少量文件，审查是否已经覆盖 route/component/selector/API/data model 证据；仅输出计数、hash 和缺口。",
		Query:             query.query,
		InputSummary:      fmt.Sprintf("selected_files=%d", review.fileCount),
		OutputSummary:     fmt.Sprintf("routes=%d components=%d selectors=%d apis=%d backend=%d models=%d style=%d gaps=%s", review.routeCount, review.componentCount, review.selectorCount, review.apiCount, review.backendCount, review.dataModelCount, review.styleCount, strings.Join(review.gaps, ",")),
		SelectedFileCount: review.fileCount,
		PathHashes:        limitStrings(review.pathHashes, 12),
		Metadata:          metadata,
		Confidence:        evidenceReviewConfidence(review),
	}
}

func evidenceSnippetToolCall(id string, query codeInvestigationQuery, snippets []model.CodeSnippetRef) model.CodeInvestigationToolCall {
	refs := limitCodeSnippetRefs(snippets, 18)
	pathHashes := []string{}
	signalKinds := []string{}
	for _, ref := range refs {
		pathHashes = append(pathHashes, ref.PathHashSHA256)
		signalKinds = append(signalKinds, ref.SignalKinds...)
	}
	return model.CodeInvestigationToolCall{
		ID:                id,
		Tool:              "read_evidence_snippets",
		Purpose:           "只定位 grep 命中文件中的局部证据窗口；持久化 hash、行号、匹配词和信号类型，不保存源码文本或绝对路径。",
		Query:             query.query,
		InputSummary:      fmt.Sprintf("question_id=%s matched_terms=%s", query.questionID, strings.Join(query.terms, ",")),
		OutputSummary:     fmt.Sprintf("snippet_windows=%d signal_kinds=%s", len(refs), strings.Join(limitStrings(uniqueStrings(signalKinds), 8), ",")),
		SelectedFileCount: len(uniqueStrings(pathHashes)),
		PathHashes:        limitStrings(uniqueStrings(pathHashes), 12),
		SnippetRefs:       refs,
		Metadata:          investigationQueryMetadata(query),
		Confidence:        0.7,
	}
}

func readEvidenceWindowsFromSnippetObservations(ctx context.Context, round int, query codeInvestigationQuery, candidates []codeCandidateFile, snippets []codeSnippetObservation, budget model.CodeReadBudget) ([]codeSnippetObservation, []model.CodeSnippetRef, model.CodeInvestigationToolCall) {
	callID := fmt.Sprintf("tool_read_window_%d_%s", round, shortHash(query.query))
	if len(candidates) == 0 || len(snippets) == 0 {
		return nil, nil, model.CodeInvestigationToolCall{
			ID:            callID,
			Tool:          "read_window",
			Purpose:       "按 planner 请求，只读取上一轮 snippet 附近的小窗口；不接受任意路径，不持久化源码文本。",
			Query:         query.query,
			InputSummary:  "candidate_files=0 snippet_refs=0",
			OutputSummary: "window_observations=0",
			Metadata:      investigationQueryMetadata(query),
			Confidence:    0.5,
		}
	}
	candidateByPathHash := map[string]codeCandidateFile{}
	for _, candidate := range candidates {
		if !shouldSkipCodeFileRel(candidate.rel, candidate.name) {
			candidateByPathHash[hashString(filepath.ToSlash(candidate.rel))] = candidate
		}
	}
	refs := []model.CodeSnippetRef{}
	observations := []codeSnippetObservation{}
	seen := map[string]bool{}
	for _, snippet := range snippets {
		if len(observations) >= minInt(4, maxInt(1, budget.FilesPerRound*2)) {
			break
		}
		candidate, ok := candidateByPathHash[snippet.PathHash]
		if !ok || candidate.size > budget.ToolSearchBytesPerFile {
			continue
		}
		if err := ctx.Err(); err != nil {
			break
		}
		data, err := os.ReadFile(candidate.path)
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		lineStart := maxInt(1, snippet.LineStart-3)
		lineEnd := minInt(len(lines), snippet.LineEnd+3)
		if lineStart > lineEnd {
			continue
		}
		key := fmt.Sprintf("%s:%d:%d", snippet.PathHash, lineStart, lineEnd)
		if seen[key] {
			continue
		}
		seen[key] = true
		window := strings.Join(lines[lineStart-1:lineEnd], "\n")
		redactedPreview := redactSensitiveInvestigationPreview(window)
		signalKinds := signalKindsForSnippet(candidate.name, window)
		matched := uniqueStrings(append(matchedTermsForText(query.terms, window), snippet.MatchedTerms...))
		refID := "window_" + shortHash(key+"|"+query.query)
		refs = append(refs, model.CodeSnippetRef{
			ID:                    refID,
			PathHashSHA256:        snippet.PathHash,
			ContentSHA256:         hashBytes(data),
			LineStart:             lineStart,
			LineEnd:               lineEnd,
			MatchedTerms:          limitStrings(matched, 6),
			SignalKinds:           signalKinds,
			RedactedPreviewSHA256: hashString(redactedPreview),
		})
		observations = append(observations, codeSnippetObservation{
			ID:           refID,
			PathHash:     snippet.PathHash,
			LineStart:    lineStart,
			LineEnd:      lineEnd,
			MatchedTerms: limitStrings(matched, 6),
			SignalKinds:  signalKinds,
			Preview:      redactedPreview,
		})
	}
	pathHashes := []string{}
	for _, ref := range refs {
		pathHashes = append(pathHashes, ref.PathHashSHA256)
	}
	return observations, refs, model.CodeInvestigationToolCall{
		ID:                callID,
		Tool:              "read_window",
		Purpose:           "按 planner 请求，只读取上一轮 snippet 附近的小窗口；不接受任意路径，不持久化源码文本。",
		Query:             query.query,
		InputSummary:      fmt.Sprintf("candidate_files=%d snippet_refs=%d", len(candidates), len(snippets)),
		OutputSummary:     fmt.Sprintf("window_observations=%d", len(observations)),
		SelectedFileCount: len(uniqueStrings(pathHashes)),
		PathHashes:        limitStrings(uniqueStrings(pathHashes), 12),
		SnippetRefs:       limitCodeSnippetRefs(refs, 12),
		Metadata:          investigationQueryMetadata(query),
		Confidence:        confidenceForSearchCall(len(snippets), len(observations)),
	}
}

func listRelatedFilesForSelectedCandidates(round int, query codeInvestigationQuery, candidates []codeCandidateFile, seeds []codeCandidateFile, budget model.CodeReadBudget) ([]relatedFileObservation, model.CodeInvestigationToolCall) {
	callID := fmt.Sprintf("tool_list_related_files_%d_%s", round, shortHash(query.query))
	seedDirs := relatedSeedDirectories(seeds)
	observations := []relatedFileObservation{}
	if len(seedDirs) > 0 {
		seen := map[string]bool{}
		for _, candidate := range candidates {
			if len(observations) >= minInt(20, maxInt(6, budget.FilesPerRound*6)) {
				break
			}
			rel := filepath.ToSlash(candidate.rel)
			if shouldSkipCodeFileRel(candidate.rel, candidate.name) || seen[rel] {
				continue
			}
			reason := relatedFileReason(rel, seedDirs, query.terms)
			if reason == "" {
				continue
			}
			seen[rel] = true
			observations = append(observations, relatedFileObservation{
				ID:       "related_file_" + shortHash(rel+"|"+query.query),
				DirHash:  hashString(filepath.ToSlash(filepath.Dir(rel))),
				FileName: candidate.name,
				Kind:     codeKindForFile(candidate.name),
				PathHash: hashString(rel),
				Score:    candidate.score,
				Reason:   reason,
			})
		}
	}
	pathHashes := []string{}
	for _, observation := range observations {
		pathHashes = append(pathHashes, observation.PathHash)
	}
	return observations, model.CodeInvestigationToolCall{
		ID:                callID,
		Tool:              "list_related_files",
		Purpose:           "按 planner 请求，仅列出上一轮命中文件附近的候选 route/component/API 文件；不读取源码，不返回绝对路径。",
		Query:             query.query,
		InputSummary:      fmt.Sprintf("seed_files=%d candidate_files=%d", len(seeds), len(candidates)),
		OutputSummary:     fmt.Sprintf("related_files=%d", len(observations)),
		MatchedFileCount:  len(observations),
		SelectedFileCount: 0,
		PathHashes:        limitStrings(uniqueStrings(pathHashes), 20),
		Metadata:          investigationQueryMetadata(query),
		Confidence:        confidenceForSearchCall(len(observations), len(observations)),
	}
}

func relatedSeedDirectories(seeds []codeCandidateFile) []string {
	out := []string{}
	for _, seed := range seeds {
		rel := strings.Trim(filepath.ToSlash(seed.rel), "/")
		if rel == "" || shouldSkipCodeFileRel(seed.rel, seed.name) {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(rel))
		if dir == "." || dir == "" {
			continue
		}
		out = append(out, dir)
		parent := filepath.ToSlash(filepath.Dir(dir))
		if parent != "." && parent != "" {
			out = append(out, parent)
		}
	}
	return uniqueStrings(out)
}

func relatedFileReason(rel string, seedDirs []string, terms []string) string {
	lower := strings.ToLower(filepath.ToSlash(rel))
	for _, dir := range seedDirs {
		dir = strings.Trim(strings.ToLower(filepath.ToSlash(dir)), "/")
		if dir != "" && (strings.HasPrefix(lower, dir+"/") || strings.Contains(lower, "/"+dir+"/")) {
			if keywordMatchScore(terms, lower) > 0 {
				return "same_or_parent_directory_and_query_terms"
			}
			return "same_or_parent_directory"
		}
	}
	if keywordMatchScore(terms, lower) > 0 && pathHasAnySegment(lower, "pages", "app", "routes", "components", "features", "api", "server", "backend", "internal") {
		return "query_terms_in_product_path"
	}
	return ""
}

func candidateSeedsFromSnippetObservations(snippets []codeSnippetObservation, candidates []codeCandidateFile) []codeCandidateFile {
	if len(snippets) == 0 {
		return nil
	}
	wanted := map[string]bool{}
	for _, snippet := range snippets {
		if snippet.PathHash != "" {
			wanted[snippet.PathHash] = true
		}
	}
	out := []codeCandidateFile{}
	seen := map[string]bool{}
	for _, candidate := range candidates {
		pathHash := hashString(filepath.ToSlash(candidate.rel))
		if !wanted[pathHash] || seen[candidate.rel] {
			continue
		}
		seen[candidate.rel] = true
		out = append(out, candidate)
	}
	return out
}

func candidateSeedsFromRelatedFileObservations(observations []relatedFileObservation, candidates []codeCandidateFile) []codeCandidateFile {
	if len(observations) == 0 {
		return nil
	}
	wanted := map[string]bool{}
	for _, observation := range observations {
		if observation.PathHash != "" {
			wanted[observation.PathHash] = true
		}
	}
	out := []codeCandidateFile{}
	seen := map[string]bool{}
	for _, candidate := range candidates {
		pathHash := hashString(filepath.ToSlash(candidate.rel))
		if !wanted[pathHash] || seen[candidate.rel] {
			continue
		}
		seen[candidate.rel] = true
		out = append(out, candidate)
	}
	return out
}

func inspectFileOutlinesForSelectedCandidates(ctx context.Context, round int, query codeInvestigationQuery, candidates []codeCandidateFile, seeds []codeCandidateFile, budget model.CodeReadBudget) ([]fileOutlineObservation, int64, model.CodeInvestigationToolCall) {
	start := time.Now()
	callID := fmt.Sprintf("tool_inspect_file_outline_%d_%s", round, shortHash(query.query))
	seedDirs := relatedSeedDirectories(seeds)
	observations := []fileOutlineObservation{}
	readCount := 0
	bytesRead := int64(0)
	seen := map[string]bool{}
	for _, candidate := range seeds {
		if len(observations) >= minInt(20, maxInt(4, budget.FilesPerRound*4)) {
			break
		}
		if err := ctx.Err(); err != nil {
			break
		}
		if candidate.size > budget.ToolSearchBytesPerFile {
			continue
		}
		rel := filepath.ToSlash(candidate.rel)
		if shouldSkipCodeFileRel(candidate.rel, candidate.name) || seen[rel] {
			continue
		}
		reason := relatedFileReason(rel, seedDirs, query.terms)
		if reason == "" && keywordMatchScore(query.terms, rel, candidate.name) == 0 {
			continue
		}
		data, err := os.ReadFile(candidate.path)
		if err != nil {
			continue
		}
		readCount++
		bytesRead += int64(len(data))
		observation := buildFileOutlineObservation(candidate, data, query, reason)
		if observation.ID == "" {
			continue
		}
		seen[rel] = true
		observations = append(observations, observation)
	}
	pathHashes := []string{}
	symbolCount := 0
	routeCount := 0
	apiCount := 0
	selectorCount := 0
	modelCount := 0
	for _, observation := range observations {
		pathHashes = append(pathHashes, observation.PathHash)
		symbolCount += len(observation.SymbolNames)
		routeCount += len(observation.RoutePaths)
		apiCount += len(observation.APIPaths)
		selectorCount += len(observation.SelectorHints)
		modelCount += len(observation.DataModelNames)
	}
	return observations, bytesRead, model.CodeInvestigationToolCall{
		ID:                callID,
		Tool:              "inspect_file_outline",
		Purpose:           "按 planner 请求，只读取少量已命中候选文件的结构轮廓；提取符号名、route、API、selector 和 data model 摘要，不持久化源码文本。",
		Query:             query.query,
		InputSummary:      fmt.Sprintf("seed_files=%d candidate_files=%d", len(seeds), len(candidates)),
		OutputSummary:     fmt.Sprintf("outlines=%d symbols=%d routes=%d apis=%d selectors=%d models=%d", len(observations), symbolCount, routeCount, apiCount, selectorCount, modelCount),
		MatchedFileCount:  readCount,
		SelectedFileCount: len(observations),
		PathHashes:        limitStrings(uniqueStrings(pathHashes), 20),
		Metadata: map[string]any{
			"question_id":          query.questionID,
			"seed_file_count":      len(seeds),
			"candidate_file_count": len(candidates),
			"read_count":           readCount,
			"outline_count":        len(observations),
			"bytes_read":           bytesRead,
			"selected_hashes":      limitStrings(uniqueStrings(pathHashes), 12),
			"signal_policy":        "symbols_routes_apis_selectors_models_only",
		},
		Confidence: confidenceForSearchCall(len(observations), len(observations)),
		ElapsedMS:  time.Since(start).Milliseconds(),
	}
}

func buildFileOutlineObservation(candidate codeCandidateFile, data []byte, query codeInvestigationQuery, reason string) fileOutlineObservation {
	pathHash := hashString(filepath.ToSlash(candidate.rel))
	dirHash := hashString(filepath.ToSlash(filepath.Dir(filepath.ToSlash(candidate.rel))))
	text := string(data)
	snapshot := &model.CodeUnderstandingSnapshot{}
	inspectRoutes(text, pathHash, snapshot)
	inspectSelectors(text, pathHash, snapshot)
	inspectSemanticControls(text, pathHash, snapshot)
	inspectComponents(candidate.name, text, pathHash, snapshot)
	inspectAPIs(text, pathHash, snapshot)
	inspectDataModels(text, pathHash, snapshot)
	symbolNames := outlineSymbolNames(text)
	componentNames := []string{}
	for _, component := range snapshot.Components {
		componentNames = append(componentNames, component.Name)
	}
	symbolNames = limitStrings(uniqueStrings(append(symbolNames, componentNames...)), 8)
	routePaths := []string{}
	for _, route := range snapshot.Routes {
		routePaths = append(routePaths, route.Path)
	}
	apiPaths := []string{}
	for _, api := range snapshot.APIEndpoints {
		apiPaths = append(apiPaths, api.Path)
	}
	dataModelNames := []string{}
	for _, dataModel := range snapshot.DataModels {
		dataModelNames = append(dataModelNames, dataModel.Name)
	}
	selectors := selectorValues(snapshot.Selectors, pathHash)
	signalKinds := signalKindsForSnippet(candidate.name, text)
	if len(symbolNames) > 0 {
		signalKinds = append(signalKinds, "symbol")
	}
	if len(routePaths) > 0 {
		signalKinds = append(signalKinds, "route")
	}
	if len(apiPaths) > 0 {
		signalKinds = append(signalKinds, "api")
	}
	if len(selectors) > 0 {
		signalKinds = append(signalKinds, "selector")
	}
	if len(dataModelNames) > 0 {
		signalKinds = append(signalKinds, "model")
	}
	if reason == "" {
		reason = relatedFileReason(filepath.ToSlash(candidate.rel), relatedSeedDirectories([]codeCandidateFile{candidate}), query.terms)
	}
	return fileOutlineObservation{
		ID:             "file_outline_" + shortHash(pathHash+"|"+query.query),
		FileName:       candidate.name,
		Kind:           codeKindForFile(candidate.name),
		PathHash:       pathHash,
		DirHash:        dirHash,
		SymbolNames:    limitStrings(uniqueStrings(symbolNames), 8),
		RoutePaths:     limitStrings(uniqueStrings(routePaths), 6),
		APIPaths:       limitStrings(uniqueStrings(apiPaths), 6),
		SelectorHints:  limitStrings(uniqueStrings(selectors), 6),
		DataModelNames: limitStrings(uniqueStrings(dataModelNames), 6),
		SignalKinds:    limitStrings(uniqueStrings(signalKinds), 6),
		Reason:         reason,
	}
}

func outlineSymbolNames(text string) []string {
	names := []string{}
	for _, match := range outlineSymbolPattern.FindAllStringSubmatch(text, 20) {
		if len(match) < 2 {
			continue
		}
		name := strings.TrimSpace(match[1])
		if !referenceTermAllowed(name) {
			continue
		}
		names = append(names, name)
	}
	return uniqueStrings(names)
}

func snippetEvidenceForQuery(candidate codeCandidateFile, data []byte, query codeInvestigationQuery) ([]model.CodeSnippetRef, []codeSnippetObservation) {
	if len(data) == 0 || len(query.terms) == 0 {
		return nil, nil
	}
	lines := strings.Split(string(data), "\n")
	pathHash := hashString(filepath.ToSlash(candidate.rel))
	contentHash := hashBytes(data)
	refs := []model.CodeSnippetRef{}
	observations := []codeSnippetObservation{}
	seen := map[string]bool{}
	for index, line := range lines {
		matched := matchedTermsForText(query.terms, line)
		if len(matched) == 0 {
			continue
		}
		lineStart := maxInt(1, index)
		lineEnd := minInt(len(lines), index+2)
		key := fmt.Sprintf("%s:%d:%d", pathHash, lineStart, lineEnd)
		if seen[key] {
			continue
		}
		seen[key] = true
		window := strings.Join(lines[lineStart-1:lineEnd], "\n")
		redactedPreview := redactSensitiveInvestigationPreview(window)
		signalKinds := signalKindsForSnippet(candidate.name, window)
		snippetID := "snippet_" + shortHash(key+"|"+query.query)
		refs = append(refs, model.CodeSnippetRef{
			ID:                    snippetID,
			PathHashSHA256:        pathHash,
			ContentSHA256:         contentHash,
			LineStart:             lineStart,
			LineEnd:               lineEnd,
			MatchedTerms:          limitStrings(matched, 6),
			SignalKinds:           signalKinds,
			RedactedPreviewSHA256: hashString(redactedPreview),
		})
		observations = append(observations, codeSnippetObservation{
			ID:           snippetID,
			PathHash:     pathHash,
			LineStart:    lineStart,
			LineEnd:      lineEnd,
			MatchedTerms: limitStrings(matched, 6),
			SignalKinds:  signalKinds,
			Preview:      redactedPreview,
		})
		if len(refs) >= 3 {
			break
		}
	}
	return refs, observations
}

func signalKindsForSnippet(name string, text string) []string {
	lower := strings.ToLower(text + " " + name)
	kinds := []string{}
	if containsAny(lower, "route", "router", "path:", "url:", "href=", "navigate", "/api/", "/app", "/dashboard", "/workspace") {
		kinds = append(kinds, "route")
	}
	if containsAny(lower, "data-testid", "aria-label", "<button", "<input", "role=", "selector", "component") {
		kinds = append(kinds, "component_or_selector")
	}
	if containsAny(lower, "/api/", "endpoint", "handler", "mutation", "fetch(", "post(", "get(", "schema", "model") {
		kinds = append(kinds, "api_or_data_model")
	}
	if containsAny(lower, "classname", ".css", ".scss", "tailwind", "loading", "progress", "state") {
		kinds = append(kinds, "style_or_state")
	}
	if len(kinds) == 0 {
		kinds = append(kinds, "text_match")
	}
	return uniqueStrings(kinds)
}

func redactSensitiveInvestigationPreview(value string) string {
	value = regexp.MustCompile(`(?i)(password|passwd|token|secret|authorization|cookie)\s*[:=]\s*["']?[^"'\s,;]+`).ReplaceAllString(value, "$1=<redacted>")
	value = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`).ReplaceAllString(value, "<email>")
	if len(value) > 240 {
		value = value[:240]
	}
	return value
}

func limitCodeSnippetRefs(values []model.CodeSnippetRef, limit int) []model.CodeSnippetRef {
	if limit <= 0 || len(values) <= limit {
		return values
	}
	return values[:limit]
}

func nonEmptyToolCallIDs(values ...string) []string {
	out := []string{}
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

func evidenceReviewConfidence(review codeInvestigationEvidenceReview) float64 {
	score := 0.42
	if review.routeCount > 0 {
		score += 0.16
	}
	if review.componentCount > 0 || review.selectorCount > 0 {
		score += 0.18
	}
	if review.apiCount > 0 || review.dataModelCount > 0 {
		score += 0.12
	}
	if review.styleCount > 0 {
		score += 0.06
	}
	if score > 0.9 {
		return 0.9
	}
	return score
}

func adaptiveQueryFromEvidenceReview(previous codeInvestigationQuery, review codeInvestigationEvidenceReview) (codeInvestigationQuery, bool) {
	if len(review.gaps) == 0 {
		return codeInvestigationQuery{}, false
	}
	gapTerms := []string{}
	purposeParts := []string{"根据 evidence_review 缺口继续 drilldown"}
	for _, gap := range review.gaps {
		switch gap {
		case "route":
			gapTerms = append(gapTerms, "route", "router", "page", "workspace", "dashboard")
			purposeParts = append(purposeParts, "route")
		case "component_or_selector":
			gapTerms = append(gapTerms, "data-testid", "aria-label", "button", "form", "input", "component")
			purposeParts = append(purposeParts, "component/selector")
		case "api_or_data_model":
			gapTerms = append(gapTerms, "api", "endpoint", "handler", "mutation", "schema", "model")
			purposeParts = append(purposeParts, "api/data")
		case "style_or_state":
			gapTerms = append(gapTerms, "style", "className", "css", "tailwind", "state", "loading", "progress")
			purposeParts = append(purposeParts, "style/state")
		}
	}
	terms := append(gapTerms, limitStrings(previous.terms, 4)...)
	terms = sanitizeInvestigationTerms(terms)
	if len(terms) == 0 {
		return codeInvestigationQuery{}, false
	}
	return codeInvestigationQuery{
		questionID:       previous.questionID,
		purpose:          strings.Join(purposeParts, " + "),
		query:            strings.Join(terms, " OR "),
		terms:            terms,
		expectedEvidence: previous.expectedEvidence,
	}, true
}

func (s *ProjectInvestigationToolSuite) planNextActionsFromEvidenceObservation(ctx context.Context, query codeInvestigationQuery, review codeInvestigationEvidenceReview, trace *model.CodeInvestigationTrace, budget model.CodeReadBudget, snippetObservations []codeSnippetObservation, relatedFileObservations []relatedFileObservation, fileOutlineObservations []fileOutlineObservation, dependsOnToolCallID string) ([]model.CodeInvestigationNextAction, model.CodeInvestigationToolCall) {
	if len(review.gaps) == 0 && len(relatedFileObservations) == 0 && len(fileOutlineObservations) == 0 {
		return nil, model.CodeInvestigationToolCall{}
	}
	start := time.Now()
	fallback := nextActionsForEvidenceReview(query, review, dependsOnToolCallID)
	call := model.CodeInvestigationToolCall{
		ID:           fmt.Sprintf("tool_plan_next_actions_%s", shortHash(query.query+"|"+strings.Join(review.gaps, ","))),
		Tool:         "plan_next_actions",
		Purpose:      "基于上一轮工具观察和 remaining gaps 规划下一步受限代码调查工具；只输出工具名、查询词和期望证据，不读取源码。",
		Query:        query.query,
		InputSummary: fmt.Sprintf("question_id=%s gaps=%s related_file_observations=%d file_outline_observations=%d remaining_search_budget=%d", query.questionID, strings.Join(review.gaps, ","), len(relatedFileObservations), len(fileOutlineObservations), maxInt(0, budget.ToolSearchFileLimit-trace.TotalFilesSearched)),
		Metadata: map[string]any{
			"question_id":                    query.questionID,
			"gaps":                           review.gaps,
			"related_file_observation_count": len(relatedFileObservations),
			"file_outline_observation_count": len(fileOutlineObservations),
			"allowed_tools":                  []string{"grep_text", "shell_run", "list_related_files", "inspect_file_outline", "read_window", "follow_imports", "find_references", "find_api_handlers", "evidence_review"},
			"depends_on_tool_call_id":        dependsOnToolCallID,
			"remaining_search_budget":        maxInt(0, budget.ToolSearchFileLimit-trace.TotalFilesSearched),
			"previous_query_term_count":      len(query.terms),
			"expected_evidence":              query.expectedEvidence,
			"observation_selected_files":     review.fileCount,
		},
		Confidence: 0.6,
	}
	if s == nil || s.llm == nil {
		call.OutputSummary = fmt.Sprintf("LLM 不可用，使用确定性 next_actions %d 条。", len(fallback))
		call.SelectedFileCount = len(fallback)
		call.FallbackReason = "llm_not_configured"
		call.ElapsedMS = time.Since(start).Milliseconds()
		return fallback, call
	}
	payload := map[string]any{
		"active_question": map[string]any{
			"id":                query.questionID,
			"purpose":           query.purpose,
			"query_terms":       query.terms,
			"expected_evidence": query.expectedEvidence,
		},
		"last_observation": map[string]any{
			"files":       review.fileCount,
			"routes":      review.routeCount,
			"components":  review.componentCount,
			"selectors":   review.selectorCount,
			"apis":        review.apiCount,
			"backend":     review.backendCount,
			"models":      review.dataModelCount,
			"style":       review.styleCount,
			"gaps":        review.gaps,
			"path_hashes": limitStrings(review.pathHashes, 8),
			"confidence":  evidenceReviewConfidence(review),
			"depends_on":  dependsOnToolCallID,
		},
		"recent_tool_observations":  compactRecentInvestigationToolCalls(trace, 8),
		"snippet_observations":      compactSnippetObservationsForPlanner(snippetObservations, 6),
		"related_file_observations": compactRelatedFileObservationsForPlanner(relatedFileObservations, 12),
		"file_outline_observations": compactFileOutlineObservationsForPlanner(fileOutlineObservations, 12),
		"tool_cards":                projectInvestigationToolCards(),
		"budget": map[string]any{
			"remaining_search_files": maxInt(0, budget.ToolSearchFileLimit-trace.TotalFilesSearched),
			"remaining_selected":     maxInt(0, budget.TotalFileLimit-trace.TotalFilesSelected),
			"files_per_round":        budget.FilesPerRound,
		},
		"allowed_tools": []string{"grep_text", "shell_run", "list_related_files", "inspect_file_outline", "read_window", "follow_imports", "find_references", "find_api_handlers"},
		"instructions": []string{
			"像代码助手一样基于 observation 决定下一步最小必要工具。",
			"只返回 1-3 个 next_actions；不要输出任意 shell 命令、绝对路径、源码片段、token、cookie、Authorization。",
			"如果需要 shell_run，只能设置 command_kind=git_ls_files、rg_files 或 rg_search_summary；不要生成命令字符串。",
			"如果需要先了解命中文件附近有哪些 route/component/API 文件，使用 list_related_files；它只列文件名、类型和 hash。",
			"如果需要把已命中的文件读成更像 Codex 的结构化轮廓，使用 inspect_file_outline；它只返回符号名、route/API/selector/data model 摘要，不输出源码。",
			"如果需要更靠近 grep 命中的局部上下文，优先使用 read_window；它只能读取上一轮 snippet 附近的小窗口。",
			"query_terms 必须是业务语义、组件名、路由词、API/handler/model 词或状态词。",
			"不要把 /aigc、/.well-known、/v1/execution-packages、app-installations 等控制面路径作为产品证据。",
		},
	}
	data, _ := json.Marshal(payload)
	var output codeInvestigationNextActionsLLMOutput
	modelTrace, err := s.llm.GenerateJSON(ctx, config.ModelTaskCodeReading, llm.JSONRequest{
		System:       "你是 Cascade DemoOps 的代码调查观察器。你根据上一轮本地工具 observation 选择下一步最小必要工具调用，不读取完整源码，不扩大到无关文件。",
		User:         string(data),
		SchemaName:   "CodeInvestigationNextActions",
		ResponseHint: "返回字段：summary, next_actions[{tool, reason, query_terms[], command_kind, expected_evidence[]}], confidence。tool 只能是 grep_text/shell_run/list_related_files/inspect_file_outline/read_window/follow_imports/find_references/find_api_handlers。",
		MaxTokens:    900,
		Temperature:  0.08,
	}, &output)
	call.ElapsedMS = time.Since(start).Milliseconds()
	if modelTrace != nil {
		call.EvidenceRefs = append(call.EvidenceRefs, model.EvidenceRef{
			ID:         "ev_code_next_action_planner_" + shortHash(modelTrace.Label()),
			Kind:       model.EvidenceKindSourceCode,
			Summary:    "CodeReaderAgent 下一步工具规划模型路由：" + modelTrace.Label(),
			FieldPath:  "code_snapshot.investigation_trace.tool_calls.plan_next_actions",
			Confidence: 0.6,
		})
	}
	if err != nil {
		call.OutputSummary = fmt.Sprintf("模型下一步工具规划不可用，使用确定性 next_actions %d 条。", len(fallback))
		call.SelectedFileCount = len(fallback)
		call.FallbackReason = llmFallbackReason(err, modelTrace)
		return fallback, call
	}
	output.normalize()
	actions := nextActionsFromLLMOutput(output, query, review, dependsOnToolCallID)
	if len(actions) == 0 {
		call.OutputSummary = fmt.Sprintf("模型未返回有效 next_actions，使用确定性 next_actions %d 条。", len(fallback))
		call.SelectedFileCount = len(fallback)
		call.FallbackReason = "empty_llm_next_actions"
		return fallback, call
	}
	call.OutputSummary = fmt.Sprintf("模型基于 observation 规划 next_actions %d 条：%s", len(actions), output.Summary)
	call.SelectedFileCount = len(actions)
	call.Confidence = maxFloat(0.68, output.Confidence)
	return actions, call
}

func appendNextActionInvestigationQueries(queries []codeInvestigationQuery, trace *model.CodeInvestigationTrace, base codeInvestigationQuery, review codeInvestigationEvidenceReview, plannedNextActions []model.CodeInvestigationNextAction, dependsOnToolCallID string, executed map[string]bool, limit int) []codeInvestigationQuery {
	if trace == nil || base.questionID == "" || limit <= 0 || len(queries) >= limit {
		return queries
	}
	question, ok := investigationQuestionForID(trace, base.questionID)
	if ok && question.Status == "answered" && !hasAllowedPostAnswerNextAction(plannedNextActions) {
		return queries
	}
	if !ok {
		question = model.CodeInvestigationQuestion{
			ID:               base.questionID,
			Question:         base.purpose,
			QueryTerms:       base.terms,
			ExpectedEvidence: base.expectedEvidence,
			Status:           "partial",
		}
	}
	if len(question.NextActions) == 0 {
		question.NextActions = plannedNextActions
	}
	if question.Status == "answered" {
		question.NextActions = allowedPostAnswerNextActions(question.NextActions)
	}
	if len(question.NextActions) == 0 {
		question.NextActions = nextActionsForEvidenceReview(base, review, dependsOnToolCallID)
	}
	if len(question.NextActions) == 0 {
		return queries
	}
	for _, next := range codeInvestigationQueriesFromNextActions(question, base) {
		if len(queries) >= limit {
			break
		}
		key := codeInvestigationQueryExecutionKey(next)
		if executed[key] || codeInvestigationQueryExists(queries, key) {
			continue
		}
		queries = append(queries, next)
	}
	return queries
}

func investigationQuestionForID(trace *model.CodeInvestigationTrace, id string) (model.CodeInvestigationQuestion, bool) {
	if trace == nil || strings.TrimSpace(id) == "" {
		return model.CodeInvestigationQuestion{}, false
	}
	for _, question := range trace.Questions {
		if question.ID == id {
			return question, true
		}
	}
	return model.CodeInvestigationQuestion{}, false
}

func codeInvestigationQueriesFromNextActions(question model.CodeInvestigationQuestion, base codeInvestigationQuery) []codeInvestigationQuery {
	out := []codeInvestigationQuery{}
	for _, action := range question.NextActions {
		terms := sanitizeInvestigationTerms(action.QueryTerms)
		expected := uniqueStrings(action.ExpectedEvidence)
		if len(expected) == 0 {
			expected = question.ExpectedEvidence
		}
		if len(expected) == 0 {
			expected = base.expectedEvidence
		}
		tool := sanitizeInvestigationToolName(action.Tool)
		commandKind := sanitizeShellRunCommandKind(action.CommandKind, "")
		if tool == "shell_run" && commandKind == "" {
			commandKind = shellRunCommandKindForTerms(terms)
		}
		if investigationToolRequiresTerms(tool, commandKind) && len(terms) == 0 {
			continue
		}
		purpose := firstNonEmpty(action.Reason, question.Question, base.purpose, "继续补齐项目理解证据。")
		queryText := strings.Join(terms, " OR ")
		if queryText == "" {
			queryText = commandKind
		}
		out = append(out, codeInvestigationQuery{
			questionID:       firstNonEmpty(question.ID, base.questionID),
			purpose:          "根据 next_actions 补证：" + purpose,
			query:            queryText,
			terms:            terms,
			expectedEvidence: expected,
			plannedFrom:      "next_actions",
			suggestedTool:    tool,
			parentTool:       base.suggestedTool,
			commandKind:      commandKind,
			dependsOnToolID:  action.DependsOnToolCallID,
		})
	}
	return dedupeCodeInvestigationQueries(out)
}

func nextActionsFromLLMOutput(output codeInvestigationNextActionsLLMOutput, query codeInvestigationQuery, review codeInvestigationEvidenceReview, dependsOnToolCallID string) []model.CodeInvestigationNextAction {
	out := []model.CodeInvestigationNextAction{}
	for _, item := range output.NextActions {
		terms := sanitizeInvestigationTerms(stringSlice(item.QueryTerms))
		expected := sanitizeExpectedEvidenceKinds(stringSlice(item.ExpectedEvidence))
		if len(expected) == 0 {
			expected = review.gaps
		}
		if len(expected) == 0 {
			expected = query.expectedEvidence
		}
		tool := sanitizeInvestigationToolName(item.Tool)
		if tool == "" {
			tool = toolForExpectedEvidence(expected)
		}
		commandKind := sanitizeShellRunCommandKind(item.CommandKind, item.Command)
		if tool == "shell_run" && commandKind == "" {
			commandKind = shellRunCommandKindForTerms(terms)
		}
		if investigationToolRequiresTerms(tool, commandKind) && len(terms) == 0 {
			continue
		}
		if tool == "" {
			continue
		}
		out = append(out, model.CodeInvestigationNextAction{
			Tool:                tool,
			Reason:              firstNonEmpty(item.Reason, "根据上一轮 observation 继续补齐证据缺口。"),
			QueryTerms:          terms,
			CommandKind:         commandKind,
			ExpectedEvidence:    uniqueStrings(expected),
			DependsOnToolCallID: dependsOnToolCallID,
		})
		if len(out) >= 3 {
			break
		}
	}
	return out
}

func sanitizeInvestigationToolName(value string) string {
	switch strings.TrimSpace(value) {
	case "repo_index", "grep_text", "shell_run", "list_related_files", "inspect_file_outline", "read_window", "follow_imports", "find_references", "find_api_handlers", "evidence_review":
		return strings.TrimSpace(value)
	default:
		return ""
	}
}

func sanitizeShellRunCommandKind(kind string, command string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	switch kind {
	case "git_ls_files", "rg_files", "rg_search_summary":
		return kind
	}
	command = strings.ToLower(strings.TrimSpace(command))
	switch {
	case command == "":
		return ""
	case strings.Contains(command, "git") && strings.Contains(command, "ls-files"):
		return "git_ls_files"
	case strings.Contains(command, "rg") && strings.Contains(command, "--files"):
		return "rg_files"
	case strings.Contains(command, "rg") && (strings.Contains(command, "-l") || strings.Contains(command, "--files-with-matches")):
		return "rg_search_summary"
	default:
		return ""
	}
}

func shellRunCommandKindForTerms(terms []string) string {
	if len(terms) == 0 {
		return "git_ls_files"
	}
	return "rg_search_summary"
}

func investigationToolRequiresTerms(tool string, commandKind string) bool {
	switch tool {
	case "read_window", "list_related_files":
		return false
	case "shell_run":
		return commandKind != "git_ls_files" && commandKind != "rg_files"
	default:
		return true
	}
}

func investigationToolPolicyRequiresSearchBudget(policy codeInvestigationToolPolicy) bool {
	return policy.grepText || policy.shellRun || policy.findReferences || policy.findAPIHandlers
}

func sanitizeExpectedEvidenceKinds(values []string) []string {
	out := []string{}
	for _, value := range values {
		switch strings.TrimSpace(value) {
		case "route", "component_or_selector", "api_or_data_model", "style_or_state":
			out = append(out, strings.TrimSpace(value))
		}
	}
	return uniqueStrings(out)
}

func toolForExpectedEvidence(expected []string) string {
	for _, item := range expected {
		if item == "api_or_data_model" {
			return "find_api_handlers"
		}
	}
	for _, item := range expected {
		if item == "component_or_selector" || item == "route" || item == "style_or_state" {
			return "grep_text"
		}
	}
	return ""
}

func compactRecentInvestigationToolCalls(trace *model.CodeInvestigationTrace, limit int) []map[string]any {
	if trace == nil || limit <= 0 {
		return nil
	}
	start := maxInt(0, len(trace.ToolCalls)-limit)
	out := []map[string]any{}
	for _, call := range trace.ToolCalls[start:] {
		item := map[string]any{
			"id":                  call.ID,
			"tool":                call.Tool,
			"query_hash":          shortHash(call.Query),
			"output_summary":      call.OutputSummary,
			"matched_file_count":  call.MatchedFileCount,
			"selected_file_count": call.SelectedFileCount,
			"confidence":          call.Confidence,
		}
		if call.FallbackReason != "" {
			item["fallback_reason"] = call.FallbackReason
		}
		if len(call.PathHashes) > 0 {
			item["path_hashes"] = limitStrings(call.PathHashes, 6)
		}
		if call.Metadata != nil {
			if questionID, ok := call.Metadata["question_id"]; ok {
				item["question_id"] = questionID
			}
			if gaps, ok := call.Metadata["gaps"]; ok {
				item["gaps"] = gaps
			}
			if toolPolicy, ok := call.Metadata["tool_policy"]; ok {
				item["tool_policy"] = toolPolicy
			}
		}
		out = append(out, item)
	}
	return out
}

func compactSnippetObservationsForPlanner(values []codeSnippetObservation, limit int) []map[string]any {
	if limit <= 0 || len(values) == 0 {
		return nil
	}
	out := []map[string]any{}
	seen := map[string]bool{}
	for _, value := range values {
		if len(out) >= limit {
			break
		}
		if value.ID == "" || seen[value.ID] || strings.TrimSpace(value.Preview) == "" {
			continue
		}
		seen[value.ID] = true
		out = append(out, map[string]any{
			"id":            value.ID,
			"path_hash":     value.PathHash,
			"line_start":    value.LineStart,
			"line_end":      value.LineEnd,
			"matched_terms": limitStrings(value.MatchedTerms, 6),
			"signal_kinds":  limitStrings(value.SignalKinds, 6),
			"preview":       value.Preview,
		})
	}
	return out
}

func compactRelatedFileObservationsForPlanner(values []relatedFileObservation, limit int) []map[string]any {
	if limit <= 0 || len(values) == 0 {
		return nil
	}
	out := []map[string]any{}
	seen := map[string]bool{}
	for _, value := range values {
		if len(out) >= limit {
			break
		}
		if value.ID == "" || seen[value.ID] {
			continue
		}
		seen[value.ID] = true
		out = append(out, map[string]any{
			"id":        value.ID,
			"dir_hash":  value.DirHash,
			"file_name": value.FileName,
			"kind":      value.Kind,
			"path_hash": value.PathHash,
			"score":     value.Score,
			"reason":    value.Reason,
		})
	}
	return out
}

func compactFileOutlineObservationsForPlanner(values []fileOutlineObservation, limit int) []map[string]any {
	if limit <= 0 || len(values) == 0 {
		return nil
	}
	out := []map[string]any{}
	seen := map[string]bool{}
	for _, value := range values {
		if len(out) >= limit {
			break
		}
		if value.ID == "" || seen[value.ID] {
			continue
		}
		seen[value.ID] = true
		out = append(out, map[string]any{
			"id":               value.ID,
			"file_name":        value.FileName,
			"kind":             value.Kind,
			"dir_hash":         value.DirHash,
			"path_hash":        value.PathHash,
			"symbol_names":     limitStrings(value.SymbolNames, 8),
			"route_paths":      limitStrings(value.RoutePaths, 6),
			"api_paths":        limitStrings(value.APIPaths, 6),
			"selector_hints":   limitStrings(value.SelectorHints, 6),
			"data_model_names": limitStrings(value.DataModelNames, 6),
			"signal_kinds":     limitStrings(value.SignalKinds, 6),
			"reason":           value.Reason,
		})
	}
	return out
}

func appendSnippetObservationCache(existing []codeSnippetObservation, next []codeSnippetObservation, limit int) []codeSnippetObservation {
	out := append([]codeSnippetObservation{}, existing...)
	seen := map[string]bool{}
	for _, value := range out {
		seen[value.ID] = true
	}
	for _, value := range next {
		if value.ID == "" || seen[value.ID] {
			continue
		}
		seen[value.ID] = true
		out = append(out, value)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

func appendFileOutlineObservationCache(existing []fileOutlineObservation, next []fileOutlineObservation, limit int) []fileOutlineObservation {
	out := append([]fileOutlineObservation{}, existing...)
	seen := map[string]bool{}
	for _, value := range out {
		seen[value.ID] = true
	}
	for _, value := range next {
		if value.ID == "" || seen[value.ID] {
			continue
		}
		seen[value.ID] = true
		out = append(out, value)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

func appendRelatedFileObservationCache(existing []relatedFileObservation, next []relatedFileObservation, limit int) []relatedFileObservation {
	out := append([]relatedFileObservation{}, existing...)
	seen := map[string]bool{}
	for _, value := range out {
		seen[value.ID] = true
	}
	for _, value := range next {
		if value.ID == "" || seen[value.ID] {
			continue
		}
		seen[value.ID] = true
		out = append(out, value)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

func codeInvestigationQueryExists(values []codeInvestigationQuery, key string) bool {
	for _, value := range values {
		if codeInvestigationQueryExecutionKey(value) == key {
			return true
		}
	}
	return false
}

func codeInvestigationQueryExecutionKey(query codeInvestigationQuery) string {
	return strings.Join([]string{
		query.questionID,
		query.suggestedTool,
		query.commandKind,
		strings.Join(query.terms, "|"),
		strings.Join(query.expectedEvidence, "|"),
	}, "|")
}

func annotateInvestigationTraceToolCalls(trace *model.CodeInvestigationTrace) {
	if trace == nil {
		return
	}
	for i := range trace.ToolCalls {
		annotateInvestigationToolCall(&trace.ToolCalls[i])
	}
}

func annotateInvestigationToolCall(call *model.CodeInvestigationToolCall) {
	if call == nil {
		return
	}
	if call.SelectionReason == "" {
		call.SelectionReason = investigationToolSelectionReason(*call)
	}
	if call.ReadPolicy == "" {
		call.ReadPolicy = investigationToolReadPolicy(call.Tool)
	}
	if call.SourceTextPolicy == "" {
		call.SourceTextPolicy = investigationToolSourceTextPolicy(call.Tool)
	}
}

func investigationToolSelectionReason(call model.CodeInvestigationToolCall) string {
	plannedFrom := metadataStringValue(call.Metadata, "planned_from")
	suggestedTool := metadataStringValue(call.Metadata, "suggested_tool")
	commandKind := metadataStringValue(call.Metadata, "command_kind")
	switch call.Tool {
	case "shell_metadata":
		return "启动阶段自动探测仓库 manifest、依赖名和 git 文件数量，建立只读背景。"
	case "repo_index":
		return "启动阶段选择 manifest、入口和路由/配置文件建立第一层项目地图。"
	case "plan_investigation":
		return "基于需求问题、repo map 和 tool cards 选择首轮最小调查工具。"
	case "grep_text":
		if suggestedTool != "" && suggestedTool != "grep_text" {
			return "兼容旧计划的 grep_text 调用；当前工具策略建议 " + suggestedTool + "。"
		}
		if plannedFrom != "" {
			return "planner 从 " + plannedFrom + " 选择 grep_text，用业务关键词做有界文件搜索。"
		}
		return "planner 选择 grep_text，用业务关键词做有界文件搜索。"
	case "shell_run":
		if commandKind != "" {
			return "planner 选择只读 shell_run/" + commandKind + "，先做仓库 discovery 或匹配摘要。"
		}
		return "planner 选择只读 shell_run，先做仓库 discovery 或匹配摘要。"
	case "read_evidence_snippets":
		return "grep 命中后读取局部证据窗口引用，只持久化 hash、行号和信号类型。"
	case "read_window":
		return "planner 基于上一轮 snippet observation 选择 read_window，读取更近的小窗口补上下文。"
	case "list_related_files":
		return "planner 基于已命中文件选择 list_related_files，先看邻近文件关系再决定下一步。"
	case "inspect_file_outline":
		return "planner 选择 inspect_file_outline，读取少量候选文件的符号/route/API/selector 轮廓。"
	case "follow_imports":
		return "工具策略沿当前命中文件的直接 import 小步追踪相关组件、hook、client 或样式模块。"
	case "find_references":
		return "工具策略从已确认符号反向查找父级 route、调用方或引用文件。"
	case "find_api_handlers":
		return "工具策略从前端 API path/mutation 追踪后端 handler/schema/model。"
	case "evidence_review":
		return "每轮工具后审查 route/component/selector/API/data model 证据覆盖和缺口。"
	case "plan_next_actions":
		return "基于上一轮 observation 和 remaining gaps 规划下一步最小工具调用。"
	case "focused_fallback":
		return "grep 命中不足时只补少量入口/路由/高置信产品文件，不使用全局 selector 池凑数。"
	case "read_structured_files":
		return "最终只结构化读取已被工具选择的需求相关文件，提取摘要和 evidence ref。"
	default:
		if call.Purpose != "" {
			return call.Purpose
		}
		return "执行受限代码调查工具。"
	}
}

func investigationToolReadPolicy(tool string) string {
	switch tool {
	case "shell_metadata":
		return "manifest/git_metadata_only"
	case "repo_index":
		return "repo_map_hashes_only"
	case "plan_investigation", "plan_next_actions":
		return "planner_context_only_no_file_read"
	case "grep_text":
		return "bounded_keyword_search"
	case "shell_run":
		return "allowlisted_read_only_git_rg"
	case "read_evidence_snippets":
		return "bounded_snippet_refs"
	case "read_window":
		return "bounded_local_windows_from_prior_snippets"
	case "list_related_files":
		return "directory_relationships_no_source_read"
	case "inspect_file_outline":
		return "bounded_file_outline_parse"
	case "follow_imports":
		return "direct_import_follow_limited"
	case "find_references":
		return "bounded_reverse_reference_search"
	case "find_api_handlers":
		return "bounded_product_api_handler_search"
	case "evidence_review":
		return "selected_round_files_summary"
	case "focused_fallback":
		return "small_entry_route_fallback"
	case "read_structured_files":
		return "selected_files_structured_parse"
	default:
		return "bounded_read_only_tool"
	}
}

func investigationToolSourceTextPolicy(tool string) string {
	switch tool {
	case "shell_metadata", "repo_index", "list_related_files", "shell_run", "plan_investigation", "plan_next_actions", "focused_fallback":
		return "no_source_text_persisted"
	case "grep_text", "read_evidence_snippets", "read_window":
		return "transient_redacted_observations_hashes_persisted"
	case "inspect_file_outline", "follow_imports", "find_references", "find_api_handlers", "evidence_review", "read_structured_files":
		return "local_parse_summary_hashes_persisted"
	default:
		return "no_raw_source_persisted"
	}
}

func metadataStringValue(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	switch value := metadata[key].(type) {
	case string:
		return value
	case fmt.Stringer:
		return value.String()
	default:
		return ""
	}
}

func codeInvestigationQualityForSnapshot(snapshot *model.CodeUnderstandingSnapshot) *model.CodeInvestigationQualitySummary {
	if snapshot == nil || snapshot.InvestigationTrace == nil {
		return nil
	}
	trace := snapshot.InvestigationTrace
	toolCalls := len(trace.ToolCalls)
	specializedTools := 0
	shellRunTools := 0
	for _, call := range trace.ToolCalls {
		if investigationToolIsSpecialized(call.Tool) {
			specializedTools++
		}
		if call.Tool == "shell_run" {
			shellRunTools++
		}
	}
	answered := 0
	open := 0
	gaps := []string{}
	for _, question := range trace.Questions {
		if question.Status == "answered" {
			answered++
			continue
		}
		open++
		gaps = append(gaps, question.RemainingGaps...)
	}
	discovered := trace.TotalFilesDiscovered
	selected := firstPositiveInt(trace.TotalFilesSelected, snapshot.FileCount)
	searched := trace.TotalFilesSearched
	selectedRatio := safeRatio(selected, discovered)
	searchRatio := safeRatio(searched, discovered)
	overreadRisk := codeInvestigationOverreadRisk(discovered, searched, selected, searchRatio, selectedRatio)
	quality := &model.CodeInvestigationQualitySummary{
		Mode:                     trace.Mode,
		ToolDriven:               toolCalls > 0,
		ToolCallCount:            toolCalls,
		SpecializedToolCallCount: specializedTools,
		ShellRunToolCallCount:    shellRunTools,
		TotalFilesDiscovered:     discovered,
		TotalFilesSearched:       searched,
		TotalFilesSelected:       selected,
		StructuredFileCount:      snapshot.FileCount,
		SelectedFileRatio:        selectedRatio,
		SearchFileRatio:          searchRatio,
		AnsweredQuestionCount:    answered,
		OpenQuestionCount:        open,
		RemainingGaps:            uniqueStrings(gaps),
		SourceTextPolicy:         "no_raw_source_persisted",
		OverreadRisk:             overreadRisk,
		Confidence:               codeInvestigationQualityConfidence(toolCalls, specializedTools, open, overreadRisk),
	}
	quality.Summary = codeInvestigationQualitySummaryText(quality)
	return quality
}

func investigationToolIsSpecialized(tool string) bool {
	switch tool {
	case "shell_run", "read_window", "list_related_files", "inspect_file_outline", "follow_imports", "find_references", "find_api_handlers":
		return true
	default:
		return false
	}
}

func codeInvestigationOverreadRisk(discovered int, searched int, selected int, searchRatio float64, selectedRatio float64) string {
	switch {
	case discovered == 0:
		return "unknown"
	case selected > 80 || (discovered >= 80 && searchRatio > 0.9) || (discovered >= 40 && selected > 30 && selectedRatio > 0.6):
		return "high"
	case selected > 40 || (discovered >= 80 && searchRatio > 0.55) || (discovered >= 40 && selected > 20 && selectedRatio > 0.35):
		return "medium"
	default:
		return "low"
	}
}

func codeInvestigationQualityConfidence(toolCalls int, specializedTools int, openQuestions int, overreadRisk string) float64 {
	confidence := 0.45
	if toolCalls > 0 {
		confidence += 0.16
	}
	if specializedTools > 0 {
		confidence += 0.12
	}
	if openQuestions == 0 {
		confidence += 0.12
	}
	switch overreadRisk {
	case "low":
		confidence += 0.12
	case "medium":
		confidence += 0.04
	case "high":
		confidence -= 0.12
	}
	if confidence < 0 {
		return 0
	}
	if confidence > 0.95 {
		return 0.95
	}
	return confidence
}

func codeInvestigationQualitySummaryText(quality *model.CodeInvestigationQualitySummary) string {
	if quality == nil {
		return ""
	}
	return fmt.Sprintf("工具化调查：%d 次工具调用，%d 次专用工具，结构化读取 %d/%d 文件，过读风险=%s，未解问题=%d。",
		quality.ToolCallCount,
		quality.SpecializedToolCallCount,
		quality.StructuredFileCount,
		quality.TotalFilesDiscovered,
		quality.OverreadRisk,
		quality.OpenQuestionCount,
	)
}

func safeRatio(numerator int, denominator int) float64 {
	if denominator <= 0 || numerator <= 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func applyEvidenceReviewToQuestion(trace *model.CodeInvestigationTrace, query codeInvestigationQuery, review codeInvestigationEvidenceReview, nextActions []model.CodeInvestigationNextAction, toolCallIDs ...string) {
	if trace == nil || query.questionID == "" {
		return
	}
	apply := func(question *model.CodeInvestigationQuestion) {
		question.ToolCallIDs = uniqueStrings(append(question.ToolCallIDs, toolCallIDs...))
		question.RemainingGaps = append([]string{}, review.gaps...)
		question.NextActions = nextActions
		if len(question.NextActions) == 0 {
			question.NextActions = nextActionsForEvidenceReview(query, review, lastNonEmptyString(toolCallIDs...))
		}
		question.EvidenceSummary = fmt.Sprintf("files=%d routes=%d components=%d selectors=%d apis=%d models=%d style=%d", review.fileCount, review.routeCount, review.componentCount, review.selectorCount, review.apiCount, review.dataModelCount, review.styleCount)
		question.Confidence = evidenceReviewConfidence(review)
		switch {
		case review.fileCount == 0:
			question.Status = "open"
		case len(review.gaps) == 0:
			question.Status = "answered"
		default:
			question.Status = "partial"
		}
	}
	for i := range trace.Questions {
		if trace.Questions[i].ID != query.questionID {
			continue
		}
		apply(&trace.Questions[i])
		return
	}
	question := model.CodeInvestigationQuestion{
		ID:               query.questionID,
		Question:         firstNonEmpty(query.purpose, "LLM 规划的补充调查问题"),
		ExpectedEvidence: uniqueStrings(query.expectedEvidence),
		QueryTerms:       uniqueStrings(query.terms),
	}
	apply(&question)
	trace.Questions = append(trace.Questions, question)
}

func finalizeInvestigationQuestions(trace *model.CodeInvestigationTrace) {
	if trace == nil {
		return
	}
	for i := range trace.Questions {
		if trace.Questions[i].Status == "" {
			trace.Questions[i].Status = "open"
		}
		if len(trace.Questions[i].RemainingGaps) == 0 && trace.Questions[i].Status == "open" {
			trace.Questions[i].RemainingGaps = append([]string{}, trace.Questions[i].ExpectedEvidence...)
		}
		if len(trace.Questions[i].NextActions) == 0 && trace.Questions[i].Status != "answered" {
			trace.Questions[i].NextActions = nextActionsForOpenQuestion(trace.Questions[i])
		}
	}
}

func codeInvestigationQuestionAnswered(trace *model.CodeInvestigationTrace, id string) bool {
	if strings.TrimSpace(id) == "" {
		return false
	}
	question, ok := investigationQuestionForID(trace, id)
	return ok && question.Status == "answered"
}

func shouldSkipAnsweredInvestigationQuery(query codeInvestigationQuery) bool {
	if postAnswerInvestigationQueryAllowed(query) {
		return false
	}
	return true
}

func postAnswerInvestigationQueryAllowed(query codeInvestigationQuery) bool {
	tool := sanitizeInvestigationToolName(query.suggestedTool)
	if query.plannedFrom == "initial_plan" && initialObservationToolAllowedAfterAnswer(tool) {
		return true
	}
	if query.plannedFrom == "next_actions" && strings.TrimSpace(query.dependsOnToolID) != "" && postAnswerToolAllowed(tool) {
		return true
	}
	return false
}

func initialObservationToolAllowedAfterAnswer(tool string) bool {
	switch tool {
	case "read_window", "list_related_files", "inspect_file_outline", "follow_imports", "find_references", "find_api_handlers":
		return true
	default:
		return false
	}
}

func hasAllowedPostAnswerNextAction(actions []model.CodeInvestigationNextAction) bool {
	return len(allowedPostAnswerNextActions(actions)) > 0
}

func allowedPostAnswerNextActions(actions []model.CodeInvestigationNextAction) []model.CodeInvestigationNextAction {
	out := []model.CodeInvestigationNextAction{}
	for _, action := range actions {
		tool := sanitizeInvestigationToolName(action.Tool)
		if strings.TrimSpace(action.DependsOnToolCallID) == "" || !postAnswerToolAllowed(tool) {
			continue
		}
		out = append(out, action)
	}
	return out
}

func postAnswerToolAllowed(tool string) bool {
	switch tool {
	case "read_window", "list_related_files", "inspect_file_outline", "follow_imports", "find_references", "find_api_handlers", "grep_text":
		return true
	default:
		return false
	}
}

func allCodeInvestigationQuestionsAnswered(trace *model.CodeInvestigationTrace) bool {
	if trace == nil || len(trace.Questions) == 0 {
		return false
	}
	for _, question := range trace.Questions {
		if question.Status != "answered" {
			return false
		}
	}
	return true
}

func shouldRunFocusedFallback(trace *model.CodeInvestigationTrace, selected []codeCandidateFile) bool {
	if len(selected) == 0 {
		return true
	}
	return !allCodeInvestigationQuestionsAnswered(trace)
}

func nextActionsForEvidenceReview(query codeInvestigationQuery, review codeInvestigationEvidenceReview, dependsOnToolCallID string) []model.CodeInvestigationNextAction {
	if len(review.gaps) == 0 {
		return nil
	}
	return nextActionsForGaps(review.gaps, query.terms, query.expectedEvidence, dependsOnToolCallID)
}

func nextActionsForOpenQuestion(question model.CodeInvestigationQuestion) []model.CodeInvestigationNextAction {
	gaps := question.RemainingGaps
	if len(gaps) == 0 {
		gaps = question.ExpectedEvidence
	}
	return nextActionsForGaps(gaps, question.QueryTerms, question.ExpectedEvidence, "")
}

func nextActionsForGaps(gaps []string, baseTerms []string, expected []string, dependsOnToolCallID string) []model.CodeInvestigationNextAction {
	out := []model.CodeInvestigationNextAction{}
	for _, gap := range uniqueStrings(gaps) {
		action := model.CodeInvestigationNextAction{
			Tool:                toolForEvidenceGap(gap),
			Reason:              reasonForEvidenceGap(gap),
			QueryTerms:          sanitizeInvestigationTerms(append(termsForEvidenceGap(gap), limitStrings(baseTerms, 4)...)),
			ExpectedEvidence:    uniqueStrings([]string{gap}),
			DependsOnToolCallID: dependsOnToolCallID,
		}
		if len(action.QueryTerms) == 0 {
			continue
		}
		if len(expected) > 0 {
			action.ExpectedEvidence = uniqueStrings(append(action.ExpectedEvidence, expected...))
		}
		out = append(out, action)
		if len(out) >= 3 {
			break
		}
	}
	return out
}

func toolForEvidenceGap(gap string) string {
	switch gap {
	case "api_or_data_model":
		return "find_api_handlers"
	case "component_or_selector", "style_or_state", "route":
		return "grep_text"
	default:
		return "grep_text"
	}
}

func reasonForEvidenceGap(gap string) string {
	switch gap {
	case "route":
		return "还缺目标业务页面或状态路由证据，需要继续定位 route/page/router 文件。"
	case "component_or_selector":
		return "还缺可交互组件或语义 selector 证据，需要继续定位按钮、表单、输入框或组件定义。"
	case "api_or_data_model":
		return "还缺后端接口、handler、mutation 或数据模型证据，需要沿前端 API 调用查后端实现。"
	case "style_or_state":
		return "还缺样式、loading/progress 状态或交互反馈证据，需要继续定位样式和状态代码。"
	default:
		return "当前调查问题仍有证据缺口，需要继续受限 grep 和结构化读取。"
	}
}

func termsForEvidenceGap(gap string) []string {
	switch gap {
	case "route":
		return []string{"route", "router", "page", "workspace", "dashboard", "projects"}
	case "component_or_selector":
		return []string{"data-testid", "aria-label", "button", "form", "input", "component"}
	case "api_or_data_model":
		return []string{"api", "endpoint", "handler", "mutation", "schema", "model"}
	case "style_or_state":
		return []string{"style", "className", "css", "tailwind", "state", "loading", "progress"}
	default:
		return []string{"route", "component", "api"}
	}
}

func lastNonEmptyString(values ...string) string {
	for i := len(values) - 1; i >= 0; i-- {
		if strings.TrimSpace(values[i]) != "" {
			return values[i]
		}
	}
	return ""
}

func investigationQuestionProgressSummary(trace *model.CodeInvestigationTrace) string {
	if trace == nil || len(trace.Questions) == 0 {
		return "未生成调查问题"
	}
	answered := 0
	partial := 0
	open := 0
	for _, question := range trace.Questions {
		switch question.Status {
		case "answered":
			answered++
		case "partial":
			partial++
		default:
			open++
		}
	}
	return fmt.Sprintf("调查问题 answered=%d partial=%d open=%d", answered, partial, open)
}

func candidateWorthToolSearch(candidate codeCandidateFile, query codeInvestigationQuery) bool {
	lower := strings.ToLower(filepath.ToSlash(candidate.rel))
	if strings.EqualFold(candidate.name, "package.json") || strings.EqualFold(candidate.name, "go.mod") {
		return false
	}
	if shouldSkipCodeFileRel(candidate.rel, candidate.name) {
		return false
	}
	if keywordMatchScore(query.terms, lower, candidate.name) > 0 {
		return true
	}
	return pathHasAnySegment(lower, "src", "app", "pages", "routes", "router", "components", "features", "server", "backend", "api", "internal", "cmd", "pkg")
}

func focusedFallbackCandidate(candidate codeCandidateFile) bool {
	lower := strings.ToLower(filepath.ToSlash(candidate.rel))
	if shouldSkipCodeFileRel(candidate.rel, candidate.name) {
		return false
	}
	if isRepoIndexCandidate(candidate.rel, candidate.name) {
		return true
	}
	if candidate.score >= 135 && pathHasAnySegment(lower, "routes", "router", "pages", "app", "features") {
		return true
	}
	if candidate.score >= 150 && pathHasAnySegment(lower, "api", "server", "backend", "internal") {
		return true
	}
	return false
}

func candidateLooksBackendImplementation(candidate codeCandidateFile) bool {
	lower := strings.ToLower(filepath.ToSlash(candidate.rel))
	if shouldSkipCodeFileRel(candidate.rel, candidate.name) {
		return false
	}
	if pathHasAnySegment(lower, "api", "server", "backend", "internal", "cmd", "pkg", "handlers", "handler") {
		return true
	}
	name := strings.ToLower(candidate.name)
	return containsAny(name, "handler", "controller", "server")
}

func pathHasAnySegment(rel string, segments ...string) bool {
	normalized := strings.Trim(strings.ToLower(filepath.ToSlash(rel)), "/")
	if normalized == "" {
		return false
	}
	parts := strings.Split(normalized, "/")
	for _, part := range parts {
		for _, segment := range segments {
			segment = strings.Trim(strings.ToLower(segment), "/")
			if segment != "" && part == segment {
				return true
			}
		}
	}
	return false
}

func matchedTermsForText(terms []string, text string) []string {
	lower := strings.ToLower(text)
	out := []string{}
	for _, term := range terms {
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}
		if strings.Contains(lower, strings.ToLower(term)) {
			out = append(out, term)
		}
	}
	return uniqueStrings(out)
}

func confidenceForSearchCall(matched int, selected int) float64 {
	switch {
	case selected > 0:
		return 0.76
	case matched > 0:
		return 0.62
	default:
		return 0.42
	}
}

func pathHashesForCandidates(candidates []codeCandidateFile, limit int) []string {
	if limit <= 0 || limit > len(candidates) {
		limit = len(candidates)
	}
	hashes := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		hashes = append(hashes, hashString(filepath.ToSlash(candidates[i].rel)))
	}
	return uniqueStrings(hashes)
}

func selectCodeCandidatesForIntent(candidates []codeCandidateFile, budget model.CodeReadBudget, project *model.ProjectContext, brief *model.RequirementBrief) []codeCandidateFile {
	if len(candidates) == 0 {
		return nil
	}
	selected := []codeCandidateFile{}
	selectedKeys := map[string]bool{}
	add := func(candidate codeCandidateFile) bool {
		if len(selected) >= budget.TotalFileLimit || selectedKeys[candidate.rel] {
			return false
		}
		selectedKeys[candidate.rel] = true
		selected = append(selected, candidate)
		return true
	}
	for _, candidate := range candidates {
		if len(selected) >= budget.RepoIndexFileLimit || len(selected) >= budget.TotalFileLimit {
			break
		}
		if isRepoIndexCandidate(candidate.rel, candidate.name) {
			add(candidate)
		}
	}
	keywords := codeIntentKeywords(project, brief)
	for round := 0; round < budget.DrilldownRounds && len(selected) < budget.TotalFileLimit; round++ {
		addedThisRound := 0
		for _, candidate := range candidates {
			if addedThisRound >= budget.FilesPerRound || len(selected) >= budget.TotalFileLimit {
				break
			}
			if selectedKeys[candidate.rel] || !candidateRelevantForIntentRound(candidate, keywords, round) {
				continue
			}
			if add(candidate) {
				addedThisRound++
			}
		}
	}
	for _, candidate := range candidates {
		if len(selected) >= budget.TotalFileLimit {
			break
		}
		if selectedKeys[candidate.rel] || candidate.score < 70 {
			continue
		}
		add(candidate)
	}
	return selected
}

func isRepoIndexCandidate(rel string, name string) bool {
	lower := strings.ToLower(filepath.ToSlash(rel))
	if strings.EqualFold(name, "package.json") || strings.EqualFold(name, "go.mod") ||
		strings.EqualFold(name, "manifest.json") || strings.EqualFold(name, "site.webmanifest") ||
		strings.EqualFold(name, "manifest.webmanifest") || strings.EqualFold(name, "CNAME") {
		return true
	}
	if isEntrypointFile(name, rel) {
		return true
	}
	return containsAny(lower,
		"/router", "/routes.", "/route-map", "/route_map",
		"vite.config", "next.config", "tailwind.config", "wails.json",
	)
}

func candidateRelevantForIntentRound(candidate codeCandidateFile, keywords []string, round int) bool {
	lower := strings.ToLower(filepath.ToSlash(candidate.rel))
	name := strings.ToLower(candidate.name)
	switch round {
	case 0:
		return keywordMatchScore(keywords, lower, name) > 0
	case 1:
		return pathHasAnySegment(lower, "features", "components", "pages", "routes", "app") &&
			(keywordMatchScore(keywords, lower, name) > 0 || candidate.score >= 105)
	case 2:
		return pathHasAnySegment(lower, "api", "server", "backend", "internal", "cmd", "pkg") &&
			(keywordMatchScore(keywords, lower, name) > 0 || candidate.score >= 90)
	default:
		return containsAny(lower, "style", ".css", ".scss", ".sass", ".less", "theme", "tailwind") &&
			(keywordMatchScore(keywords, lower, name) > 0 || candidate.score >= 70)
	}
}

func firstPositiveInt(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func firstPositiveInt64(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func shouldSkipDir(name string) bool {
	switch strings.ToLower(name) {
	case ".git", "node_modules", "dist", "build", "out", ".next", "coverage", "vendor", "tmp", ".turbo", ".cache":
		return true
	default:
		return false
	}
}

func shouldSkipCodeFileRel(rel string, name string) bool {
	lower := strings.ToLower(filepath.ToSlash(rel))
	if strings.EqualFold(name, "package.json") || strings.EqualFold(name, "go.mod") {
		return false
	}
	if pathHasAnySegment(lower, ".git", "node_modules", "dist", "build", "out", ".next", "coverage", "vendor", "fixtures", "fixture", "mock", "mocks", "__tests__", "reports", "report", "fonts", "font") {
		return true
	}
	return containsAny(lower,
		"/nix/store", "nix/store", "/.git/", "/node_modules/", "/dist/", "/build/", "/out/", "/.next/",
		"/coverage/", "/vendor/", "/fixtures/", "/fixture/", "/mock/", "/mocks/", "/__tests__/",
		"/reports/", "/report/", "/fonts/", "/font/", "/assets/fonts/",
		".test.", ".spec.", ".stories.", ".generated.", ".snap.",
	)
}

func codeFilePriority(rel string, name string, project *model.ProjectContext, brief *model.RequirementBrief) int {
	lower := strings.ToLower(filepath.ToSlash(rel))
	score := 20
	if strings.EqualFold(name, "package.json") || strings.EqualFold(name, "go.mod") {
		score += 90
	}
	switch {
	case pathHasAnySegment(lower, "src", "app", "pages", "routes", "router", "components", "features"):
		score += 80
	case pathHasAnySegment(lower, "internal", "cmd", "pkg", "server", "backend", "api"):
		score += 60
	}
	if containsAny(lower, ".tsx", ".vue", ".svelte") {
		score += 35
	}
	if containsAny(lower, ".ts", ".js", ".jsx", ".go") {
		score += 25
	}
	if containsAny(lower, "style", "css", "theme", "tailwind") {
		score += 15
	}
	for _, keyword := range codeIntentKeywords(project, brief) {
		if keyword != "" && strings.Contains(lower, strings.ToLower(keyword)) {
			score += 35
		}
	}
	if shouldSkipCodeFileRel(rel, name) {
		score -= 1000
	}
	return score
}

func isScannableFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".go", ".ts", ".tsx", ".js", ".jsx", ".vue", ".svelte", ".json", ".md", ".sql", ".py", ".rb", ".php", ".rs", ".cs", ".java", ".kt", ".css", ".scss", ".sass", ".less":
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
	case ".css", ".scss", ".sass", ".less":
		return "css"
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
	case strings.HasSuffix(lower, ".css") || strings.HasSuffix(lower, ".scss") || strings.HasSuffix(lower, ".sass") || strings.HasSuffix(lower, ".less") || strings.Contains(lower, "tailwind"):
		return "style"
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
		fileBaseLooksLikeEntrypoint(name) ||
		strings.Contains(lower, "/app.") ||
		strings.Contains(lower, "/main.") ||
		strings.Contains(lower, "/index.")
}

func fileBaseLooksLikeEntrypoint(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasPrefix(lower, "app.") ||
		strings.HasPrefix(lower, "main.") ||
		strings.HasPrefix(lower, "index.")
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
		if !browserRouteAllowedForCode(path) {
			continue
		}
		snapshot.Routes = append(snapshot.Routes, model.RouteInsight{
			ID:             safeID("route", path),
			Path:           path,
			SourcePathHash: pathHash,
			EvidenceRefs:   []model.EvidenceRef{codeEvidenceRef("route", pathHash, path)},
			Confidence:     0.62,
		})
	}
}

func inspectFilesystemRoutes(rel string, pathHash string, snapshot *model.CodeUnderstandingSnapshot) {
	for _, path := range filesystemRoutesForRel(rel) {
		if !browserRouteAllowedForCode(path) {
			continue
		}
		snapshot.Routes = append(snapshot.Routes, model.RouteInsight{
			ID:             safeID("route", path),
			Path:           path,
			Name:           filesystemRouteNameFromPath(path),
			SourcePathHash: pathHash,
			EvidenceRefs:   []model.EvidenceRef{codeEvidenceRef("route_path", pathHash, path)},
			Confidence:     0.56,
		})
	}
}

func filesystemRoutesForRel(rel string) []string {
	normalized := strings.Trim(filepath.ToSlash(rel), "/")
	lower := strings.ToLower(normalized)
	if normalized == "" || shouldSkipCodeFileRel(normalized, filepath.Base(normalized)) {
		return nil
	}
	if strings.HasSuffix(lower, ".css") || strings.HasSuffix(lower, ".scss") || strings.HasSuffix(lower, ".sass") || strings.HasSuffix(lower, ".less") {
		return nil
	}
	parts := strings.Split(normalized, "/")
	routes := []string{}
	if idx := routeRootIndex(parts, "app"); idx >= 0 {
		if route, ok := routeFromAppParts(parts[idx+1:]); ok {
			routes = append(routes, route)
		}
	}
	if idx := routeRootIndex(parts, "pages"); idx >= 0 {
		if route, ok := routeFromPagesParts(parts[idx+1:]); ok {
			routes = append(routes, route)
		}
	}
	if idx := routeRootIndex(parts, "routes"); idx >= 0 {
		if route, ok := routeFromRoutesParts(parts[idx+1:]); ok {
			routes = append(routes, route)
		}
	}
	return uniqueStrings(routes)
}

func routeRootIndex(parts []string, root string) int {
	for i, part := range parts {
		if strings.EqualFold(part, root) {
			return i
		}
	}
	return -1
}

func routeFromAppParts(parts []string) (string, bool) {
	if len(parts) == 0 {
		return "", false
	}
	file := strings.ToLower(parts[len(parts)-1])
	base := strings.TrimSuffix(file, filepath.Ext(file))
	if base != "page" && base != "route" && base != "layout" {
		return "", false
	}
	routeParts := routeSegmentsFromParts(parts[:len(parts)-1])
	return buildFilesystemRoute(routeParts)
}

func routeFromPagesParts(parts []string) (string, bool) {
	if len(parts) == 0 {
		return "", false
	}
	file := parts[len(parts)-1]
	ext := filepath.Ext(file)
	if ext == "" {
		return "", false
	}
	base := strings.TrimSuffix(file, ext)
	if strings.EqualFold(base, "_app") || strings.EqualFold(base, "_document") || strings.EqualFold(base, "_error") || strings.EqualFold(base, "api") {
		return "", false
	}
	routeParts := append(routeSegmentsFromParts(parts[:len(parts)-1]), normalizeRouteFileSegment(base))
	return buildFilesystemRoute(routeParts)
}

func routeFromRoutesParts(parts []string) (string, bool) {
	if len(parts) == 0 {
		return "", false
	}
	file := parts[len(parts)-1]
	ext := filepath.Ext(file)
	base := strings.TrimSuffix(file, ext)
	switch {
	case strings.EqualFold(base, "+page"), strings.EqualFold(base, "+layout"):
		return buildFilesystemRoute(routeSegmentsFromParts(parts[:len(parts)-1]))
	case strings.EqualFold(base, "index"):
		return buildFilesystemRoute(routeSegmentsFromParts(parts[:len(parts)-1]))
	default:
		routeParts := append(routeSegmentsFromParts(parts[:len(parts)-1]), normalizeRouteFileSegment(base))
		return buildFilesystemRoute(routeParts)
	}
}

func routeSegmentsFromParts(parts []string) []string {
	out := []string{}
	for _, part := range parts {
		segment := normalizeRouteFileSegment(part)
		if segment == "" {
			continue
		}
		out = append(out, segment)
	}
	return out
}

func normalizeRouteFileSegment(segment string) string {
	segment = strings.TrimSpace(segment)
	if segment == "" || strings.EqualFold(segment, "index") {
		return ""
	}
	if strings.HasPrefix(segment, "(") && strings.HasSuffix(segment, ")") {
		return ""
	}
	if strings.HasPrefix(segment, "@") {
		return ""
	}
	if strings.HasPrefix(segment, "[[...") && strings.HasSuffix(segment, "]]") {
		name := strings.TrimSuffix(strings.TrimPrefix(segment, "[[..."), "]]")
		if name == "" {
			return ""
		}
		return ":" + name + "*"
	}
	if strings.HasPrefix(segment, "[...") && strings.HasSuffix(segment, "]") {
		name := strings.TrimSuffix(strings.TrimPrefix(segment, "[..."), "]")
		if name == "" {
			return ""
		}
		return ":" + name + "*"
	}
	if strings.HasPrefix(segment, "[") && strings.HasSuffix(segment, "]") {
		name := strings.TrimSuffix(strings.TrimPrefix(segment, "["), "]")
		if name == "" {
			return ""
		}
		return ":" + name
	}
	return strings.Trim(segment, "_")
}

func buildFilesystemRoute(parts []string) (string, bool) {
	clean := []string{}
	for _, part := range parts {
		part = strings.Trim(part, "/")
		if part == "" {
			continue
		}
		clean = append(clean, part)
	}
	route := "/" + strings.Join(clean, "/")
	if route == "/" || browserRouteAllowedForCode(route) {
		return route, true
	}
	return "", false
}

func filesystemRouteNameFromPath(path string) string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return "Home"
	}
	parts := strings.Split(trimmed, "/")
	name := parts[len(parts)-1]
	name = strings.TrimPrefix(name, ":")
	name = strings.TrimSuffix(name, "*")
	if name == "" {
		name = trimmed
	}
	return strings.ReplaceAll(name, "-", " ")
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
		confidence := 0.76
		stability := 0.86
		if actionLooksLikeChromeControl(value, selector) || selectorLooksReadOnlySurface(selector) {
			confidence = 0.42
			stability = 0.45
		}
		snapshot.Selectors = append(snapshot.Selectors, model.SelectorInsight{
			Kind:               "css",
			Value:              selector,
			FilePathHashSHA256: pathHash,
			StabilityScore:     stability,
			Confidence:         confidence,
			EvidenceRefs:       []model.EvidenceRef{codeEvidenceRef("selector", pathHash, selector)},
		})
	}
}

func inspectSemanticControls(text string, pathHash string, snapshot *model.CodeUnderstandingSnapshot) {
	add := func(kind string, selector string, label string, confidence float64, stability float64) {
		selector = strings.TrimSpace(selector)
		label = strings.TrimSpace(label)
		if selector == "" || !semanticControlLabelAllowed(label) || selectorLooksGeneric(selector) || selectorLooksReadOnlySurface(selector) {
			return
		}
		if actionLooksLikeChromeControl(label, selector) && !semanticControlLabelLooksBusiness(label) {
			return
		}
		snapshot.Selectors = append(snapshot.Selectors, model.SelectorInsight{
			Kind:               kind,
			Value:              selector,
			FilePathHashSHA256: pathHash,
			StabilityScore:     stability,
			Confidence:         confidence,
			EvidenceRefs:       []model.EvidenceRef{codeEvidenceRef("semantic_selector", pathHash, selector)},
		})
	}
	for _, tagMatch := range semanticControlTagPattern.FindAllStringSubmatch(text, 40) {
		if len(tagMatch) < 2 {
			continue
		}
		tagText := tagMatch[0]
		tag := strings.ToLower(tagMatch[1])
		for _, attrMatch := range semanticAttrPattern.FindAllStringSubmatch(tagText, 8) {
			if len(attrMatch) < 3 {
				continue
			}
			attr := strings.ToLower(attrMatch[1])
			value := cleanSemanticControlLabel(attrMatch[2])
			switch attr {
			case "aria-label":
				add("css", `[aria-label*="`+escapeDoubleQuotedSelectorValue(value)+`"]`, value, 0.68, 0.74)
			case "placeholder":
				if tag == "input" || tag == "textarea" {
					add("css", `[`+attr+`*="`+escapeDoubleQuotedSelectorValue(value)+`"]`, value, 0.66, 0.7)
				}
			case "name":
				if tag == "input" || tag == "textarea" || tag == "select" {
					add("css", tag+`[name="`+escapeDoubleQuotedSelectorValue(value)+`"]`, value, 0.62, 0.66)
				}
			}
		}
	}
	for _, match := range buttonTextPattern.FindAllStringSubmatch(text, 30) {
		if len(match) < 2 {
			continue
		}
		label := cleanSemanticControlLabel(match[1])
		add("text", `button:has-text("`+escapeDoubleQuotedSelectorValue(label)+`")`, label, 0.66, 0.62)
	}
	for _, match := range linkTextPattern.FindAllStringSubmatch(text, 20) {
		if len(match) < 2 {
			continue
		}
		label := cleanSemanticControlLabel(match[1])
		add("text", `a:has-text("`+escapeDoubleQuotedSelectorValue(label)+`")`, label, 0.6, 0.58)
	}
}

func cleanSemanticControlLabel(value string) string {
	value = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(value, " ")
	value = strings.TrimSpace(value)
	value = whitespacePattern.ReplaceAllString(value, " ")
	return value
}

func semanticControlLabelAllowed(label string) bool {
	label = strings.TrimSpace(label)
	if len([]rune(label)) < 2 || len([]rune(label)) > 60 {
		return false
	}
	if strings.ContainsAny(label, "{}<>`") || strings.Contains(label, "://") || looksSensitiveLiteral(label) {
		return false
	}
	lower := strings.ToLower(label)
	if containsAny(lower, "authorization", "cookie", "bearer ", "api key", "private key") {
		return false
	}
	return true
}

func semanticControlLabelLooksBusiness(label string) bool {
	return containsAnyNormalized(label,
		"create", "new", "project", "submit", "save", "generate", "build", "start", "run", "confirm", "next",
		"新建", "创建", "项目", "提交", "保存", "生成", "构建", "开始", "启动", "确认", "下一步",
	)
}

func escapeDoubleQuotedSelectorValue(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return value
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
			ActionLabels:       componentActionLabels(componentName, selectorValues(snapshot.Selectors, pathHash)),
			EvidenceRefs:       []model.EvidenceRef{codeEvidenceRef("component", pathHash, componentName)},
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
			ActionLabels:       componentActionLabels("UIComponent-"+shortHash(pathHash), selectorValues(snapshot.Selectors, pathHash)),
			EvidenceRefs:       []model.EvidenceRef{codeEvidenceRef("component", pathHash, "UIComponent-"+shortHash(pathHash))},
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
		if !apiPathAllowedForCode(path) {
			continue
		}
		snapshot.APIEndpoints = append(snapshot.APIEndpoints, model.APIEndpointInsight{
			ID:                 safeID("api", path),
			Path:               path,
			FilePathHashSHA256: pathHash,
			EvidenceRefs:       []model.EvidenceRef{codeEvidenceRef("api", pathHash, path)},
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
			EvidenceRefs:         []model.EvidenceRef{codeEvidenceRef("data_model", pathHash, name)},
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

func digestCodeSourceReference(input model.CodeInput) string {
	return hashString(strings.Join([]string{input.Kind, input.URI, input.LocalPath, input.RepositoryID, input.Branch, input.CommitSHA}, "|"))
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

func normalizeCodeSnapshotForIntent(snapshot *model.CodeUnderstandingSnapshot, project *model.ProjectContext, brief *model.RequirementBrief) {
	if snapshot == nil {
		return
	}
	keywords := codeIntentKeywords(project, brief)
	snapshot.Routes = rankedRoutesForIntent(snapshot.Routes, keywords, 80)
	snapshot.Components = rankedComponentsForIntent(snapshot.Components, keywords, 120)
	snapshot.Selectors = rankedSelectorsForIntent(snapshot.Selectors, keywords, 100)
	snapshot.APIEndpoints = rankedAPIsForIntent(snapshot.APIEndpoints, keywords, 80)
	snapshot.DataModels = rankedDataModelsForIntent(snapshot.DataModels, keywords, 80)
}

func codeIntentKeywords(project *model.ProjectContext, brief *model.RequirementBrief) []string {
	parts := []string{}
	if project != nil {
		parts = append(parts, project.ProductDescription, project.TargetAudience, project.Name)
		parts = append(parts, project.MustShow...)
	}
	if brief != nil {
		parts = append(parts, brief.Scenario, brief.Objective, brief.PrimaryOutcome, brief.TargetAudience)
		parts = append(parts, brief.MustShow...)
	}
	keywords := []string{}
	for _, part := range parts {
		keywords = append(keywords, intentKeywordsForText(part)...)
	}
	return uniqueStrings(keywords)
}

func rankedRoutesForIntent(values []model.RouteInsight, keywords []string, limit int) []model.RouteInsight {
	seen := map[string]bool{}
	out := []model.RouteInsight{}
	for _, value := range values {
		if !browserRouteAllowedForCode(value.Path) || seen[value.Path] {
			continue
		}
		seen[value.Path] = true
		value.Confidence = maxFloat(value.Confidence, 0.62)
		out = append(out, value)
	}
	sort.SliceStable(out, func(i, j int) bool {
		left := routeIntentScore(out[i], keywords)
		right := routeIntentScore(out[j], keywords)
		if left == right {
			return out[i].Path < out[j].Path
		}
		return left > right
	})
	return limitRoutes(out, limit)
}

func routeIntentScore(value model.RouteInsight, keywords []string) int {
	score := int(value.Confidence * 100)
	score += keywordMatchScore(keywords, value.Path, value.Name) * 30
	if containsAny(strings.ToLower(value.Path), "project", "build", "generate", "workspace", "dashboard", "app") {
		score += 10
	}
	return score
}

func rankedComponentsForIntent(values []model.ComponentInsight, keywords []string, limit int) []model.ComponentInsight {
	seen := map[string]bool{}
	out := []model.ComponentInsight{}
	for _, value := range values {
		key := firstNonEmpty(value.ID, value.Name, value.FilePathHashSHA256)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	sort.SliceStable(out, func(i, j int) bool {
		left := componentIntentScore(out[i], keywords)
		right := componentIntentScore(out[j], keywords)
		if left == right {
			return out[i].Name < out[j].Name
		}
		return left > right
	})
	return limitComponents(out, limit)
}

func componentIntentScore(value model.ComponentInsight, keywords []string) int {
	score := int(value.Confidence * 100)
	score += keywordMatchScore(keywords, value.Name, strings.Join(value.ActionLabels, " "), strings.Join(value.SelectorHints, " ")) * 30
	if actionLooksLikeChromeControl(value.Name+" "+strings.Join(value.ActionLabels, " "), strings.Join(value.SelectorHints, " ")) {
		score -= 80
	}
	return score
}

func rankedSelectorsForIntent(values []model.SelectorInsight, keywords []string, limit int) []model.SelectorInsight {
	seen := map[string]bool{}
	out := []model.SelectorInsight{}
	for _, value := range values {
		if value.Value == "" || selectorLooksGeneric(value.Value) || seen[value.Value] {
			continue
		}
		if selectorLooksReadOnlySurface(value.Value) {
			continue
		}
		if selectorLooksLikeChromeControl(value.Value) && keywordMatchScore(keywords, value.Value) == 0 {
			continue
		}
		seen[value.Value] = true
		out = append(out, value)
	}
	sort.SliceStable(out, func(i, j int) bool {
		left := selectorIntentScore(out[i], keywords)
		right := selectorIntentScore(out[j], keywords)
		if left == right {
			return out[i].Value < out[j].Value
		}
		return left > right
	})
	return limitSelectors(out, limit)
}

func selectorIntentScore(value model.SelectorInsight, keywords []string) int {
	score := selectorQualityScore(value.Value) + int(value.Confidence*20) + int(value.StabilityScore*20)
	score += keywordMatchScore(keywords, value.Value) * 35
	if selectorLooksReadOnlySurface(value.Value) || selectorLooksLikeChromeControl(value.Value) {
		score -= 90
	}
	return score
}

func rankedAPIsForIntent(values []model.APIEndpointInsight, keywords []string, limit int) []model.APIEndpointInsight {
	seen := map[string]bool{}
	out := []model.APIEndpointInsight{}
	for _, value := range values {
		if !apiPathAllowedForCode(value.Path) || seen[value.Path] {
			continue
		}
		seen[value.Path] = true
		out = append(out, value)
	}
	sort.SliceStable(out, func(i, j int) bool {
		left := int(out[i].Confidence*100) + keywordMatchScore(keywords, out[i].Path)*30
		right := int(out[j].Confidence*100) + keywordMatchScore(keywords, out[j].Path)*30
		if left == right {
			return out[i].Path < out[j].Path
		}
		return left > right
	})
	return limitAPIs(out, limit)
}

func rankedDataModelsForIntent(values []model.DataModelInsight, keywords []string, limit int) []model.DataModelInsight {
	seen := map[string]bool{}
	out := []model.DataModelInsight{}
	for _, value := range values {
		if value.Name == "" || seen[value.Name] {
			continue
		}
		seen[value.Name] = true
		out = append(out, value)
	}
	sort.SliceStable(out, func(i, j int) bool {
		left := int(out[i].Confidence*100) + keywordMatchScore(keywords, out[i].Name, strings.Join(dataFieldNames(out[i].Fields), " "))*30
		right := int(out[j].Confidence*100) + keywordMatchScore(keywords, out[j].Name, strings.Join(dataFieldNames(out[j].Fields), " "))*30
		if left == right {
			return out[i].Name < out[j].Name
		}
		return left > right
	})
	return limitDataModels(out, limit)
}

func browserRouteAllowedForCode(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" || !strings.HasPrefix(path, "/") || strings.Contains(path, " ") || len(path) > 100 {
		return false
	}
	lower := strings.ToLower(path)
	if strings.HasPrefix(lower, "/api/") || lower == "/api" {
		return false
	}
	if pathHasForbiddenPrefix(lower, defaultControlPlaneForbiddenPaths()) {
		return false
	}
	if containsAny(lower, "/nix/store", "/node_modules", "/fonts/", "/font/", ".woff", ".ttf", ".otf", ".png", ".jpg", ".jpeg", ".svg", ".map") {
		return false
	}
	if containsAny(lower, "/usr/", "/var/", "/home/", "/users/") {
		return false
	}
	return true
}

func apiPathAllowedForCode(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" || !strings.HasPrefix(path, "/") || strings.Contains(path, " ") || len(path) > 120 {
		return false
	}
	lower := strings.ToLower(path)
	if pathHasForbiddenPrefix(lower, []string{"/aigc", "/.well-known", "/v1/execution-packages", "/v1/result-packages", "/v1/app-installations", "/dev/execution-packages", "/execution-packages", "/result-packages", "/app-installations"}) {
		return false
	}
	if containsAny(lower, "cascade-exchange", "exchange-packages", "authorization") {
		return false
	}
	return true
}

func componentActionLabels(name string, selectors []string) []string {
	labels := []string{name}
	for _, selector := range selectors {
		labels = append(labels, labelFromSelector(selector))
	}
	return uniqueStrings(labels)
}

func codeEvidenceRef(kind string, pathHash string, value string) model.EvidenceRef {
	return model.EvidenceRef{
		ID:         "ev_code_" + kind + "_" + shortHash(pathHash+"|"+value),
		Kind:       model.EvidenceKindSourceCode,
		Summary:    kind + " evidence from local read-only code scan",
		FieldPath:  "code_snapshot." + kind,
		Confidence: 0.68,
	}
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
	routePattern              = regexp.MustCompile(`["'](\/[A-Za-z0-9_\-\/:{}.*?=&%]+)["']`)
	testIDPattern             = regexp.MustCompile(`(?:data-testid=["']|getByTestId\(["'])([A-Za-z0-9_\-:.]+)`)
	semanticControlTagPattern = regexp.MustCompile(`(?is)<(input|textarea|select|button|a)\b[^>]*>`)
	semanticAttrPattern       = regexp.MustCompile(`(?is)\b(aria-label|placeholder|name)=["']([^"']{1,80})["']`)
	buttonTextPattern         = regexp.MustCompile(`(?is)<button\b[^>]*>([^<>{}]{1,80})</button>`)
	linkTextPattern           = regexp.MustCompile(`(?is)<a\b[^>]*>([^<>{}]{1,80})</a>`)
	outlineSymbolPattern      = regexp.MustCompile(`(?m)\b(?:export\s+)?(?:async\s+)?(?:function|class|const|let|var)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	componentPattern          = regexp.MustCompile(`(?:function|const|class)\s+([A-Z][A-Za-z0-9_]+)`)
	apiPattern                = regexp.MustCompile(`["'](\/api\/[A-Za-z0-9_\-\/:{}.*?=&%]+)["']`)
	dataModelPattern          = regexp.MustCompile(`(?:type|interface|struct)\s+([A-Z][A-Za-z0-9_]+)`)
)
