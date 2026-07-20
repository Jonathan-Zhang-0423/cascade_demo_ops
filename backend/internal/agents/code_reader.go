package agents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	MaxFiles           int
	MaxFileBytes       int64
	Budget             model.CodeReadBudget
	InvestigationTools *ProjectInvestigationToolSuite
	llm                llm.Client
}

func NewCodeReaderAgent() *CodeReaderAgent {
	budget := defaultCodeReadBudget()
	return &CodeReaderAgent{MaxFiles: budget.TotalFileLimit, MaxFileBytes: budget.MaxFileBytes, Budget: budget, InvestigationTools: NewProjectInvestigationToolSuite(nil)}
}

func NewCodeReaderAgentWithLLM(client llm.Client) *CodeReaderAgent {
	budget := defaultCodeReadBudget()
	return &CodeReaderAgent{MaxFiles: budget.TotalFileLimit, MaxFileBytes: budget.MaxFileBytes, Budget: budget, InvestigationTools: NewProjectInvestigationToolSuite(client), llm: client}
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
			if err := a.scanLocalPath(ctx, input.LocalPath, &snapshot, project, brief); err != nil {
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
		inspectRoutes(text, pathHash, snapshot)
		inspectSelectors(text, pathHash, snapshot)
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
		snapshot.InvestigationTrace.ToolCalls = append(snapshot.InvestigationTrace.ToolCalls, model.CodeInvestigationToolCall{
			ID:                "tool_read_structured_files_" + shortHash(snapshot.ID),
			Tool:              "read_structured_files",
			Purpose:           "只读取已由 repo index / grep_text 选中的需求相关文件，并提取路由、组件、selector、API、数据模型摘要。",
			InputSummary:      fmt.Sprintf("selected_files=%d max_file_bytes=%d", len(candidates), budget.MaxFileBytes),
			OutputSummary:     fmt.Sprintf("structured_files=%d routes=%d components=%d selectors=%d apis=%d", fileCount, len(snapshot.Routes), len(snapshot.Components), len(snapshot.Selectors), len(snapshot.APIEndpoints)),
			SelectedFileCount: fileCount,
			PathHashes:        pathHashesForCandidates(candidates, fileCount),
			Confidence:        0.78,
		})
	}
	return nil
}

func defaultCodeReadBudget() model.CodeReadBudget {
	return model.CodeReadBudget{
		Mode:                   "intent_driven_progressive",
		RepoIndexFileLimit:     40,
		DrilldownRounds:        4,
		FilesPerRound:          10,
		TotalFileLimit:         80,
		MaxFileBytes:           160 * 1024,
		ToolSearchFileLimit:    180,
		ToolSearchBytesPerFile: 64 * 1024,
		ToolSearchResultLimit:  25,
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
	if budget.TotalFileLimit > defaults.TotalFileLimit {
		budget.TotalFileLimit = defaults.TotalFileLimit
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
}

type codeSearchMatch struct {
	candidate codeCandidateFile
	score     int
	terms     []string
	snippets  []model.CodeSnippetRef
}

type codeSearchResult struct {
	matches   []codeSearchMatch
	searched  int
	bytesRead int64
}

type importFollowScan struct {
	sourceFileCount int
	importCount     int
	bytesRead       int64
}

type codeInvestigationEvidenceReview struct {
	fileCount      int
	routeCount     int
	componentCount int
	selectorCount  int
	apiCount       int
	dataModelCount int
	styleCount     int
	gaps           []string
	pathHashes     []string
}

type codeInvestigationLLMOutput struct {
	Summary string `json:"summary"`
	Queries []struct {
		Purpose string              `json:"purpose"`
		Terms   flexibleStringSlice `json:"terms"`
		Query   string              `json:"query"`
	} `json:"queries"`
	Confidence float64 `json:"confidence"`
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
	normalized := make([]struct {
		Purpose string              `json:"purpose"`
		Terms   flexibleStringSlice `json:"terms"`
		Query   string              `json:"query"`
	}, 0, len(o.Queries))
	for _, query := range o.Queries {
		terms := sanitizeInvestigationTerms(append(stringSlice(query.Terms), splitQueryTerms(query.Query)...))
		if len(terms) == 0 {
			continue
		}
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
		queryKey := strings.Join(query.terms, "|")
		if executedQueries[queryKey] {
			continue
		}
		executedQueries[queryKey] = true
		start = time.Now()
		search, err := searchCodeCandidatesForQuery(ctx, candidates, selectedKeys, query, budget)
		if err != nil {
			return ProjectInvestigationResult{SelectedCandidates: selected, Trace: trace}, err
		}
		trace.TotalFilesSearched += search.searched
		trace.TotalBytesRead += search.bytesRead
		selectedThisRound := []codeCandidateFile{}
		snippetsThisRound := []model.CodeSnippetRef{}
		for _, match := range search.matches {
			if len(selectedThisRound) >= budget.FilesPerRound || len(selected) >= budget.TotalFileLimit {
				break
			}
			if add(match.candidate) {
				selectedThisRound = append(selectedThisRound, match.candidate)
				snippetsThisRound = append(snippetsThisRound, match.snippets...)
			}
		}
		grepCallID := fmt.Sprintf("tool_grep_text_%d_%s", i+1, shortHash(query.query))
		trace.ToolCalls = append(trace.ToolCalls, model.CodeInvestigationToolCall{
			ID:                grepCallID,
			Tool:              "grep_text",
			Purpose:           query.purpose,
			Query:             query.query,
			InputSummary:      fmt.Sprintf("terms=%s search_file_limit=%d bytes_per_file=%d", strings.Join(query.terms, ","), budget.ToolSearchFileLimit, budget.ToolSearchBytesPerFile),
			OutputSummary:     fmt.Sprintf("searched=%d matched=%d selected=%d", search.searched, len(search.matches), len(selectedThisRound)),
			MatchedFileCount:  len(search.matches),
			SelectedFileCount: len(selectedThisRound),
			PathHashes:        pathHashesForCandidates(selectedThisRound, len(selectedThisRound)),
			SnippetRefs:       limitCodeSnippetRefs(snippetsThisRound, 12),
			Metadata:          investigationQueryMetadata(query),
			Confidence:        confidenceForSearchCall(len(search.matches), len(selectedThisRound)),
			ElapsedMS:         time.Since(start).Milliseconds(),
		})
		snippetCallID := ""
		if len(snippetsThisRound) > 0 {
			snippetCallID = fmt.Sprintf("tool_read_evidence_snippets_%d_%s", i+1, shortHash(query.query))
			trace.ToolCalls = append(trace.ToolCalls, evidenceSnippetToolCall(snippetCallID, query, snippetsThisRound))
		}
		followedImports, importScan, err := followImportsFromCandidates(ctx, selectedThisRound, candidateIndex, selectedKeys, budget)
		if err != nil {
			return ProjectInvestigationResult{SelectedCandidates: selected, Trace: trace}, err
		}
		trace.TotalBytesRead += importScan.bytesRead
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
		reviewCandidates := append(append([]codeCandidateFile{}, selectedThisRound...), followedThisRound...)
		review, err := reviewInvestigationEvidence(ctx, reviewCandidates, project, brief)
		if err != nil {
			return ProjectInvestigationResult{SelectedCandidates: selected, Trace: trace}, err
		}
		review.gaps = evidenceGapsForExpected(review, query.expectedEvidence)
		reviewCall := evidenceReviewToolCall(i+1, query, review)
		trace.ToolCalls = append(trace.ToolCalls, reviewCall)
		applyEvidenceReviewToQuestion(trace, query, review, nonEmptyToolCallIDs(grepCallID, snippetCallID, followCallID, reviewCall.ID)...)
		if next, ok := adaptiveQueryFromEvidenceReview(query, review); ok && len(queries) < budget.DrilldownRounds+2 {
			nextKey := strings.Join(next.terms, "|")
			if !executedQueries[nextKey] && !codeInvestigationQueryExists(queries, nextKey) {
				queries = append(queries, next)
			}
		}
	}

	if len(selected) < budget.TotalFileLimit {
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
		Purpose:      "根据需求调查问题和精简仓库地图决定下一轮 grep_text 查询；不读取完整源码，不执行 shell。",
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
		"instructions":            []string{"围绕 investigation_questions 返回 2-5 个 grep 查询计划。", "terms 必须是业务语义、路由、组件、API、状态或样式关键词。", "不要输出 shell 命令、绝对路径、源码片段、token、cookie、Authorization。", "不要把 /aigc、/.well-known、/v1/execution-packages、app-installations 等控制面路径作为产品证据。"},
	}
	data, _ := json.Marshal(payload)
	var output codeInvestigationLLMOutput
	modelTrace, err := s.llm.GenerateJSON(ctx, config.ModelTaskCodeReading, llm.JSONRequest{
		System:       "你是 Cascade DemoOps 的代码调查 planner。你的任务像 Codex 一样先决定应该 grep 什么，再让本地安全工具读取少量命中文件。你不能要求 shell，不能要求完整源码，不能输出敏感数据。",
		User:         string(data),
		SchemaName:   "CodeInvestigationPlan",
		ResponseHint: "返回字段：summary, queries[{purpose, terms[], query}], confidence。terms 每条 2-8 个，query 可省略。",
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
	queries := codeInvestigationQueriesFromLLM(output, budget, questions)
	if len(queries) == 0 {
		call.OutputSummary = fmt.Sprintf("模型未返回有效查询，使用确定性查询计划 %d 条。", len(fallback))
		call.SelectedFileCount = len(fallback)
		call.FallbackReason = "empty_llm_plan"
		return fallback, call
	}
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
		if len(terms) == 0 {
			continue
		}
		question := closestInvestigationQuestion(terms, questions)
		queries = append(queries, codeInvestigationQuery{
			questionID:       question.ID,
			purpose:          firstNonEmpty(item.Purpose, "按模型规划的业务语义检索相关代码。"),
			query:            strings.Join(terms, " OR "),
			terms:            terms,
			expectedEvidence: question.ExpectedEvidence,
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
		if len(terms) == 0 {
			continue
		}
		key := strings.Join(terms, "|")
		if seen[key] {
			continue
		}
		seen[key] = true
		value.terms = terms
		value.query = strings.Join(terms, " OR ")
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
	if containsAnyNormalized(intentText, "登录", "登陆", "login", "signin", "邮箱", "密码") {
		add(
			"question_session_setup",
			"登录与会话建立",
			"登录入口、认证表单、会话进入工作台的代码路径在哪里？",
			[]string{"登录", "登陆", "login", "signin", "sign", "email", "password", "auth", "session", "workspace", "dashboard"},
			[]string{"route", "component_or_selector", "api_or_data_model"},
		)
	}
	if containsAnyNormalized(intentText, "新建项目", "创建项目", "新增项目", "project", "俄罗斯方块", "tetris") {
		add(
			"question_project_creation",
			"新建项目",
			"新建项目流程、项目名称输入、俄罗斯方块填充语义和创建接口分别由哪些文件实现？",
			[]string{"新建项目", "创建项目", "新增项目", "new project", "create project", "project name", "new", "create", "project", "projects", "项目", "项目名称", "俄罗斯方块", "tetris"},
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
	if len(metadata) == 0 {
		return nil
	}
	return metadata
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
	if containsAnyNormalized(intentText, "新建项目", "创建项目", "新增项目", "project", "俄罗斯方块", "tetris") {
		add("定位新建项目流程、项目名称输入和创建 API。", []string{"新建项目", "创建项目", "新增项目", "new project", "create project", "project name", "项目名称", "俄罗斯方块", "tetris"})
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
	for _, candidate := range candidates {
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
					snippetRefs = snippetRefsForQuery(candidate, data, query)
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
		result.matches = append(result.matches, codeSearchMatch{candidate: candidate, score: score + candidate.score/4, terms: matchedTerms, snippets: snippetRefs})
	}
	sort.SliceStable(result.matches, func(i, j int) bool {
		if result.matches[i].score == result.matches[j].score {
			return filepath.ToSlash(result.matches[i].candidate.rel) < filepath.ToSlash(result.matches[j].candidate.rel)
		}
		return result.matches[i].score > result.matches[j].score
	})
	if budget.ToolSearchResultLimit > 0 && len(result.matches) > budget.ToolSearchResultLimit {
		result.matches = result.matches[:budget.ToolSearchResultLimit]
	}
	return result, nil
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

func followImportsFromCandidates(ctx context.Context, sources []codeCandidateFile, index map[string]codeCandidateFile, selectedKeys map[string]bool, budget model.CodeReadBudget) ([]codeCandidateFile, importFollowScan, error) {
	scan := importFollowScan{}
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
		imports := relativeImportSpecs(string(data))
		scan.importCount += len(imports)
		for _, spec := range imports {
			if len(out) >= minInt(8, maxInt(2, budget.FilesPerRound*2)) {
				return out, scan, nil
			}
			candidate, ok := resolveImportCandidate(source, spec, index)
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

func relativeImportSpecs(text string) []string {
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
			if importSpecAllowedForFollow(spec) {
				specs = append(specs, spec)
			}
		}
	}
	return limitStrings(uniqueStrings(specs), 24)
}

func importSpecAllowedForFollow(spec string) bool {
	if spec == "" || strings.Contains(spec, "\x00") || strings.Contains(spec, "://") {
		return false
	}
	lower := strings.ToLower(spec)
	if strings.HasPrefix(lower, ".") || strings.HasPrefix(lower, "@/") || strings.HasPrefix(lower, "src/") {
		return !pathHasForbiddenPrefix(lower, []string{"/aigc", "/.well-known", "/v1/", "../..", "/node_modules", "/dist", "/build"})
	}
	return false
}

func resolveImportCandidate(source codeCandidateFile, spec string, index map[string]codeCandidateFile) (codeCandidateFile, bool) {
	candidates := importResolutionKeys(source, spec)
	for _, key := range candidates {
		if candidate, ok := index[strings.ToLower(filepath.ToSlash(key))]; ok {
			return candidate, true
		}
	}
	return codeCandidateFile{}, false
}

func importResolutionKeys(source codeCandidateFile, spec string) []string {
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
			"policy":            "relative_imports_and_src_alias_only",
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
		OutputSummary:     fmt.Sprintf("routes=%d components=%d selectors=%d apis=%d models=%d style=%d gaps=%s", review.routeCount, review.componentCount, review.selectorCount, review.apiCount, review.dataModelCount, review.styleCount, strings.Join(review.gaps, ",")),
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

func snippetRefsForQuery(candidate codeCandidateFile, data []byte, query codeInvestigationQuery) []model.CodeSnippetRef {
	if len(data) == 0 || len(query.terms) == 0 {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	pathHash := hashString(filepath.ToSlash(candidate.rel))
	contentHash := hashBytes(data)
	refs := []model.CodeSnippetRef{}
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
		refs = append(refs, model.CodeSnippetRef{
			ID:                    "snippet_" + shortHash(key+"|"+query.query),
			PathHashSHA256:        pathHash,
			ContentSHA256:         contentHash,
			LineStart:             lineStart,
			LineEnd:               lineEnd,
			MatchedTerms:          limitStrings(matched, 6),
			SignalKinds:           signalKindsForSnippet(candidate.name, window),
			RedactedPreviewSHA256: hashString(redactSensitiveInvestigationPreview(window)),
		})
		if len(refs) >= 3 {
			break
		}
	}
	return refs
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

func codeInvestigationQueryExists(values []codeInvestigationQuery, key string) bool {
	for _, value := range values {
		if strings.Join(value.terms, "|") == key {
			return true
		}
	}
	return false
}

func applyEvidenceReviewToQuestion(trace *model.CodeInvestigationTrace, query codeInvestigationQuery, review codeInvestigationEvidenceReview, toolCallIDs ...string) {
	if trace == nil || query.questionID == "" {
		return
	}
	for i := range trace.Questions {
		if trace.Questions[i].ID != query.questionID {
			continue
		}
		trace.Questions[i].ToolCallIDs = uniqueStrings(append(trace.Questions[i].ToolCallIDs, toolCallIDs...))
		trace.Questions[i].RemainingGaps = append([]string{}, review.gaps...)
		trace.Questions[i].EvidenceSummary = fmt.Sprintf("files=%d routes=%d components=%d selectors=%d apis=%d models=%d style=%d", review.fileCount, review.routeCount, review.componentCount, review.selectorCount, review.apiCount, review.dataModelCount, review.styleCount)
		trace.Questions[i].Confidence = evidenceReviewConfidence(review)
		switch {
		case review.fileCount == 0:
			trace.Questions[i].Status = "open"
		case len(review.gaps) == 0:
			trace.Questions[i].Status = "answered"
		default:
			trace.Questions[i].Status = "partial"
		}
		return
	}
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
	}
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
	if strings.EqualFold(name, "package.json") || strings.EqualFold(name, "go.mod") {
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
	routePattern     = regexp.MustCompile(`["'](\/[A-Za-z0-9_\-\/:{}.*?=&%]+)["']`)
	testIDPattern    = regexp.MustCompile(`(?:data-testid=["']|getByTestId\(["'])([A-Za-z0-9_\-:.]+)`)
	componentPattern = regexp.MustCompile(`(?:function|const|class)\s+([A-Z][A-Za-z0-9_]+)`)
	apiPattern       = regexp.MustCompile(`["'](\/api\/[A-Za-z0-9_\-\/:{}.*?=&%]+)["']`)
	dataModelPattern = regexp.MustCompile(`(?:type|interface|struct)\s+([A-Z][A-Za-z0-9_]+)`)
)
