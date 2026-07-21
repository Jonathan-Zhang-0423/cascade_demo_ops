package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"cascade-demoops/backend/internal/llm"
	"cascade-demoops/backend/internal/model"
)

// ProjectInvestigationToolSuite owns the read-only tools used to investigate a
// local repository. It deliberately exposes grep-style and structured-read
// behavior instead of arbitrary shell execution so App-side planning can become
// tool-driven without expanding the unsafe command surface.
type ProjectInvestigationToolSuite struct {
	llm llm.Client
}

func NewProjectInvestigationToolSuite(client llm.Client) *ProjectInvestigationToolSuite {
	return &ProjectInvestigationToolSuite{llm: client}
}

type ProjectInvestigationRequest struct {
	Root       string
	Candidates []codeCandidateFile
	Budget     model.CodeReadBudget
	Project    *model.ProjectContext
	Brief      *model.RequirementBrief
}

type ProjectInvestigationResult struct {
	SelectedCandidates []codeCandidateFile
	Trace              *model.CodeInvestigationTrace
}

func (s *ProjectInvestigationToolSuite) InvestigateLocalPath(ctx context.Context, root string, budget model.CodeReadBudget, project *model.ProjectContext, brief *model.RequirementBrief) (ProjectInvestigationResult, error) {
	candidates, err := collectCodeCandidates(ctx, root, budget.MaxFileBytes, project, brief)
	if err != nil {
		return ProjectInvestigationResult{}, err
	}
	return s.Investigate(ctx, ProjectInvestigationRequest{
		Root:       filepath.Clean(root),
		Candidates: candidates,
		Budget:     budget,
		Project:    project,
		Brief:      brief,
	})
}

func collectCodeCandidates(ctx context.Context, root string, maxFileBytes int64, project *model.ProjectContext, brief *model.RequirementBrief) ([]codeCandidateFile, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, nil
	}
	root = filepath.Clean(root)
	if rels, ok := gitDiscoveredRelativeFiles(ctx, root); ok {
		candidates := codeCandidatesFromRelativePaths(root, rels, maxFileBytes, project, brief)
		if len(candidates) > 0 {
			return candidates, nil
		}
	}
	if rels, ok := rgDiscoveredRelativeFiles(ctx, root); ok {
		candidates := codeCandidatesFromRelativePaths(root, rels, maxFileBytes, project, brief)
		if len(candidates) > 0 {
			return candidates, nil
		}
	}
	return walkCodeCandidates(ctx, root, maxFileBytes, project, brief)
}

func walkCodeCandidates(ctx context.Context, root string, maxFileBytes int64, project *model.ProjectContext, brief *model.RequirementBrief) ([]codeCandidateFile, error) {
	candidates := []codeCandidateFile{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
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
		if !isScannableFile(name) {
			return nil
		}
		fileInfo, err := entry.Info()
		if err != nil || (maxFileBytes > 0 && fileInfo.Size() > maxFileBytes) {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = name
		}
		if shouldSkipCodeFileRel(rel, name) {
			return nil
		}
		candidates = append(candidates, codeCandidateFile{
			path:  path,
			rel:   rel,
			name:  name,
			size:  fileInfo.Size(),
			score: codeFilePriority(rel, name, project, brief),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortCodeCandidates(candidates)
	return candidates, nil
}

func gitDiscoveredRelativeFiles(ctx context.Context, root string) ([]string, bool) {
	if strings.TrimSpace(root) == "" {
		return nil, false
	}
	if _, err := exec.LookPath("git"); err != nil {
		return nil, false
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "git", "-C", root, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	output, err := cmd.Output()
	if cmdCtx.Err() != nil || err != nil {
		return nil, false
	}
	values := []string{}
	for _, raw := range strings.Split(string(output), "\x00") {
		rel := strings.TrimSpace(raw)
		if rel == "" {
			continue
		}
		values = append(values, rel)
	}
	values = uniqueStrings(values)
	if len(values) == 0 {
		return nil, false
	}
	return values, true
}

func rgDiscoveredRelativeFiles(ctx context.Context, root string) ([]string, bool) {
	if strings.TrimSpace(root) == "" {
		return nil, false
	}
	if _, err := exec.LookPath("rg"); err != nil {
		return nil, false
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "rg", "--files", "-0")
	cmd.Dir = root
	output, err := cmd.Output()
	if cmdCtx.Err() != nil || err != nil {
		return nil, false
	}
	values := []string{}
	for _, raw := range strings.Split(string(output), "\x00") {
		rel := strings.TrimSpace(raw)
		if rel == "" {
			continue
		}
		values = append(values, rel)
	}
	values = uniqueStrings(values)
	if len(values) == 0 {
		return nil, false
	}
	return values, true
}

func codeCandidatesFromRelativePaths(root string, rels []string, maxFileBytes int64, project *model.ProjectContext, brief *model.RequirementBrief) []codeCandidateFile {
	candidates := []codeCandidateFile{}
	seen := map[string]bool{}
	for _, rel := range rels {
		normalized, ok := safeRelativeCodePath(rel)
		if !ok || seen[normalized] {
			continue
		}
		seen[normalized] = true
		path := filepath.Join(root, filepath.FromSlash(normalized))
		fileInfo, err := os.Stat(path)
		if err != nil || fileInfo.IsDir() || (maxFileBytes > 0 && fileInfo.Size() > maxFileBytes) {
			continue
		}
		name := filepath.Base(normalized)
		if !isScannableFile(name) || shouldSkipCodeFileRel(normalized, name) {
			continue
		}
		candidates = append(candidates, codeCandidateFile{
			path:  path,
			rel:   normalized,
			name:  name,
			size:  fileInfo.Size(),
			score: codeFilePriority(normalized, name, project, brief),
		})
	}
	sortCodeCandidates(candidates)
	return candidates
}

func safeRelativeCodePath(rel string) (string, bool) {
	rel = strings.TrimSpace(filepath.ToSlash(rel))
	if rel == "" || filepath.IsAbs(rel) || strings.Contains(rel, "\x00") {
		return "", false
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, ":") {
		return "", false
	}
	return clean, true
}

func sortCodeCandidates(candidates []codeCandidateFile) {
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return filepath.ToSlash(candidates[i].rel) < filepath.ToSlash(candidates[j].rel)
		}
		return candidates[i].score > candidates[j].score
	})
}

func (s *ProjectInvestigationToolSuite) shellMetadataToolCall(ctx context.Context, root string, candidates []codeCandidateFile) model.CodeInvestigationToolCall {
	start := time.Now()
	metadata := manifestMetadataFromCandidates(candidates)
	gitCount, gitFallback := gitTrackedFileCount(ctx, root)
	if gitCount > 0 {
		metadata["git_tracked_file_count"] = gitCount
	}
	fallback := ""
	if gitFallback != "" {
		fallback = gitFallback
	}
	manifests := intFromMetadata(metadata, "manifest_count")
	scripts := stringSliceFromMetadata(metadata, "script_names")
	managers := stringSliceFromMetadata(metadata, "package_managers")
	return model.CodeInvestigationToolCall{
		ID:             "tool_shell_metadata_" + shortHash(root),
		Tool:           "shell_metadata",
		Purpose:        "受限项目元数据探测：只读取 manifest/git 摘要，不执行 package scripts，不返回源码、脚本命令值或绝对路径。",
		InputSummary:   "allowed=manifest_summary,git_ls_files_count disallowed=arbitrary_shell,package_scripts,network",
		OutputSummary:  "manifests=" + intToString(manifests) + " managers=" + strings.Join(managers, ",") + " scripts=" + strings.Join(limitStrings(scripts, 8), ","),
		Metadata:       metadata,
		Confidence:     0.72,
		ElapsedMS:      time.Since(start).Milliseconds(),
		FallbackReason: fallback,
	}
}

type shellRunInvestigationScan struct {
	commandKind    string
	searched       int
	matched        int
	bytesRead      int64
	usedExternal   bool
	fallbackReason string
	extensionStats []map[string]any
}

func runReadOnlyShellInvestigationTool(ctx context.Context, round int, root string, candidates []codeCandidateFile, selectedKeys map[string]bool, query codeInvestigationQuery, budget model.CodeReadBudget) ([]codeCandidateFile, shellRunInvestigationScan, model.CodeInvestigationToolCall) {
	start := time.Now()
	kind := sanitizeShellRunCommandKind(query.commandKind, "")
	if kind == "" {
		kind = shellRunCommandKindForTerms(query.terms)
	}
	scan := shellRunInvestigationScan{commandKind: kind}
	selected := []codeCandidateFile{}
	callID := fmt.Sprintf("tool_shell_run_%d_%s", round, shortHash(kind+"|"+query.query))
	if kind == "" || len(candidates) == 0 {
		return selected, scan, model.CodeInvestigationToolCall{
			ID:            callID,
			Tool:          "shell_run",
			Purpose:       "只读 shell_run 被拒绝或没有候选文件；不执行任意 shell，不持久化 stdout。",
			Query:         query.query,
			InputSummary:  "command_kind=unavailable",
			OutputSummary: "matched=0 selected=0",
			Metadata:      shellRunMetadata(query, scan),
			Confidence:    0.45,
			ElapsedMS:     time.Since(start).Milliseconds(),
		}
	}
	switch kind {
	case "git_ls_files", "rg_files":
		rels, usedExternal, fallback := readOnlyShellFileList(ctx, root, kind, candidates)
		scan.usedExternal = usedExternal
		scan.fallbackReason = fallback
		scan.searched = len(rels)
		matched := candidatesMatchingShellFileList(candidates, rels, selectedKeys, query.terms)
		scan.matched = len(matched)
		selected = limitShellRunSelectedCandidates(matched, budget)
	case "rg_search_summary":
		rels, usedExternal, fallback := rgFilesWithMatches(ctx, root, query.terms)
		scan.usedExternal = usedExternal
		scan.fallbackReason = fallback
		if usedExternal {
			scan.searched = len(rels)
			matched := candidatesMatchingShellFileList(candidates, rels, selectedKeys, nil)
			scan.matched = len(matched)
			selected = limitShellRunSelectedCandidates(matched, budget)
		} else {
			search, err := searchCodeCandidatesForQuery(ctx, candidates, selectedKeys, query, budget)
			if err == nil {
				scan.searched = search.searched
				scan.bytesRead = search.bytesRead
				scan.matched = len(search.matches)
				for _, match := range search.matches {
					selected = append(selected, match.candidate)
				}
				selected = limitShellRunSelectedCandidates(selected, budget)
			}
		}
	}
	scan.extensionStats = extensionStatsForCandidates(selected, 8)
	pathHashes := pathHashesForCandidates(selected, len(selected))
	return selected, scan, model.CodeInvestigationToolCall{
		ID:                callID,
		Tool:              "shell_run",
		Purpose:           "安全只读 shell_run 语义工具：仅允许 git/rg discovery 或文件匹配摘要；不执行任意命令、不返回源码行或路径。",
		Query:             query.query,
		InputSummary:      fmt.Sprintf("command_kind=%s query_terms=%d allowlist=git_ls_files,rg_files,rg_search_summary", kind, len(query.terms)),
		OutputSummary:     fmt.Sprintf("command_kind=%s matched=%d selected=%d external=%t", kind, scan.matched, len(selected), scan.usedExternal),
		MatchedFileCount:  scan.matched,
		SelectedFileCount: len(selected),
		PathHashes:        pathHashes,
		Metadata:          shellRunMetadata(query, scan),
		Confidence:        confidenceForSearchCall(scan.matched, len(selected)),
		ElapsedMS:         time.Since(start).Milliseconds(),
		FallbackReason:    scan.fallbackReason,
	}
}

func readOnlyShellFileList(ctx context.Context, root string, kind string, candidates []codeCandidateFile) ([]string, bool, string) {
	switch kind {
	case "git_ls_files":
		if rels, ok := gitDiscoveredRelativeFiles(ctx, root); ok {
			return rels, true, ""
		}
		return candidateRelativePaths(candidates), false, "git_ls_files_unavailable"
	case "rg_files":
		if rels, ok := rgDiscoveredRelativeFiles(ctx, root); ok {
			return rels, true, ""
		}
		return candidateRelativePaths(candidates), false, "rg_files_unavailable"
	default:
		return nil, false, "unsupported_command_kind"
	}
}

func rgFilesWithMatches(ctx context.Context, root string, terms []string) ([]string, bool, string) {
	terms = sanitizeInvestigationTerms(terms)
	if strings.TrimSpace(root) == "" || len(terms) == 0 {
		return nil, false, "missing_root_or_terms"
	}
	if _, err := exec.LookPath("rg"); err != nil {
		return nil, false, "rg_unavailable"
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()
	args := []string{"--files-with-matches", "--fixed-strings"}
	for _, glob := range []string{"!.git/**", "!node_modules/**", "!dist/**", "!build/**", "!.next/**", "!.cache/**", "!reports/**", "!fixtures/**", "!mocks/**", "!fonts/**"} {
		args = append(args, "--glob", glob)
	}
	for _, term := range terms {
		args = append(args, "-e", term)
	}
	args = append(args, ".")
	cmd := exec.CommandContext(cmdCtx, "rg", args...)
	cmd.Dir = root
	output, err := cmd.Output()
	if cmdCtx.Err() != nil {
		return nil, false, "rg_timeout"
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return []string{}, true, ""
		}
		return nil, false, "rg_search_unavailable"
	}
	values := []string{}
	for _, raw := range strings.Split(string(output), "\n") {
		rel := strings.TrimSpace(raw)
		if rel == "" {
			continue
		}
		values = append(values, rel)
	}
	return uniqueStrings(values), true, ""
}

func candidatesMatchingShellFileList(candidates []codeCandidateFile, rels []string, selectedKeys map[string]bool, terms []string) []codeCandidateFile {
	if len(rels) == 0 {
		return nil
	}
	allowedRels := map[string]bool{}
	for _, rel := range rels {
		normalized, ok := safeRelativeCodePath(rel)
		if !ok {
			continue
		}
		allowedRels[strings.ToLower(filepath.ToSlash(normalized))] = true
	}
	ranked := []codeCandidateFile{}
	for _, candidate := range candidates {
		rel := filepath.ToSlash(candidate.rel)
		lower := strings.ToLower(rel)
		if len(allowedRels) > 0 && !allowedRels[lower] {
			continue
		}
		if len(terms) > 0 && keywordMatchScore(terms, rel, candidate.name) == 0 {
			continue
		}
		if shouldSkipCodeFileRel(candidate.rel, candidate.name) {
			continue
		}
		ranked = append(ranked, candidate)
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		left := candidateShellRunScore(ranked[i], terms)
		right := candidateShellRunScore(ranked[j], terms)
		if left == right {
			return filepath.ToSlash(ranked[i].rel) < filepath.ToSlash(ranked[j].rel)
		}
		return left > right
	})
	return ranked
}

func candidateShellRunScore(candidate codeCandidateFile, terms []string) int {
	score := candidate.score
	score += keywordMatchScore(terms, filepath.ToSlash(candidate.rel), candidate.name) * 60
	if isRepoIndexCandidate(candidate.rel, candidate.name) {
		score += 8
	}
	return score
}

func limitShellRunSelectedCandidates(candidates []codeCandidateFile, budget model.CodeReadBudget) []codeCandidateFile {
	limit := minInt(6, maxInt(1, budget.FilesPerRound))
	if len(candidates) <= limit {
		return candidates
	}
	return candidates[:limit]
}

func candidateRelativePaths(candidates []codeCandidateFile) []string {
	out := []string{}
	for _, candidate := range candidates {
		out = append(out, filepath.ToSlash(candidate.rel))
	}
	return uniqueStrings(out)
}

func shellRunMetadata(query codeInvestigationQuery, scan shellRunInvestigationScan) map[string]any {
	metadata := map[string]any{
		"question_id":           query.questionID,
		"planned_from":          query.plannedFrom,
		"depends_on_tool_call":  query.dependsOnToolID,
		"command_kind":          scan.commandKind,
		"execution_policy":      "read_only_allowlist_no_stdout_persistence",
		"used_external_command": scan.usedExternal,
		"searched":              scan.searched,
		"matched":               scan.matched,
		"bytes_read":            scan.bytesRead,
		"term_hashes":           hashStringSlice(query.terms, 8),
		"extension_stats":       scan.extensionStats,
	}
	if scan.fallbackReason != "" {
		metadata["fallback_reason"] = scan.fallbackReason
	}
	return metadata
}

func extensionStatsForCandidates(candidates []codeCandidateFile, limit int) []map[string]any {
	counts := map[string]int{}
	for _, candidate := range candidates {
		ext := strings.ToLower(filepath.Ext(candidate.name))
		if ext == "" {
			ext = "(none)"
		}
		counts[ext]++
	}
	type item struct {
		ext   string
		count int
	}
	items := []item{}
	for ext, count := range counts {
		items = append(items, item{ext: ext, count: count})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].count == items[j].count {
			return items[i].ext < items[j].ext
		}
		return items[i].count > items[j].count
	})
	out := []map[string]any{}
	for _, item := range items {
		if len(out) >= limit {
			break
		}
		out = append(out, map[string]any{"ext": item.ext, "count": item.count})
	}
	return out
}

func hashStringSlice(values []string, limit int) []string {
	out := []string{}
	for _, value := range limitStrings(uniqueStrings(values), limit) {
		out = append(out, hashString(value))
	}
	return out
}

func manifestMetadataFromCandidates(candidates []codeCandidateFile) map[string]any {
	metadata := map[string]any{
		"package_managers": []string{},
		"script_names":     []string{},
		"dependency_names": []string{},
		"manifest_count":   0,
	}
	packageManagers := []string{}
	scriptNames := []string{}
	dependencyNames := []string{}
	manifestCount := 0
	goModuleHashes := []string{}
	configNames := []string{}
	for _, candidate := range candidates {
		name := strings.ToLower(candidate.name)
		switch name {
		case "package.json":
			manifestCount++
			packageManagers = append(packageManagers, "npm")
			scripts, deps := packageJSONMetadata(candidate.path)
			scriptNames = append(scriptNames, scripts...)
			dependencyNames = append(dependencyNames, deps...)
		case "go.mod":
			manifestCount++
			packageManagers = append(packageManagers, "go modules")
			if hash := goModuleHash(candidate.path); hash != "" {
				goModuleHashes = append(goModuleHashes, hash)
			}
		case "wails.json", "vite.config.ts", "vite.config.js", "vite.config.mjs", "next.config.js", "next.config.mjs", "next.config.ts":
			configNames = append(configNames, name)
		}
	}
	metadata["package_managers"] = uniqueStrings(packageManagers)
	metadata["script_names"] = limitStrings(uniqueStrings(scriptNames), 24)
	metadata["dependency_names"] = limitStrings(uniqueStrings(dependencyNames), 40)
	metadata["manifest_count"] = manifestCount
	if len(goModuleHashes) > 0 {
		metadata["go_module_hashes"] = uniqueStrings(goModuleHashes)
	}
	if len(configNames) > 0 {
		metadata["config_files"] = uniqueStrings(configNames)
	}
	return metadata
}

func packageJSONMetadata(path string) ([]string, []string) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 256*1024 {
		return nil, nil
	}
	var pkg struct {
		Scripts         map[string]any `json:"scripts"`
		Dependencies    map[string]any `json:"dependencies"`
		DevDependencies map[string]any `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, nil
	}
	scripts := mapKeys(pkg.Scripts)
	deps := append(mapKeys(pkg.Dependencies), mapKeys(pkg.DevDependencies)...)
	return sanitizeManifestNames(scripts), sanitizeManifestNames(deps)
}

func goModuleHash(path string) string {
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 256*1024 {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return hashString(strings.TrimSpace(strings.TrimPrefix(line, "module ")))
		}
	}
	return ""
}

func gitTrackedFileCount(ctx context.Context, root string) (int, string) {
	if strings.TrimSpace(root) == "" {
		return 0, "missing_root"
	}
	if _, err := exec.LookPath("git"); err != nil {
		return 0, "git_unavailable"
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "git", "-C", root, "ls-files")
	output, err := cmd.Output()
	if cmdCtx.Err() != nil {
		return 0, "git_timeout"
	}
	if err != nil {
		return 0, "git_ls_files_unavailable"
	}
	count := 0
	for _, line := range strings.Split(string(output), "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count, ""
}

func mapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sanitizeManifestNames(values []string) []string {
	out := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 80 {
			continue
		}
		lower := strings.ToLower(value)
		if containsAny(lower, "password", "secret", "token", "authorization", "cookie") {
			continue
		}
		out = append(out, value)
	}
	return uniqueStrings(out)
}

func intFromMetadata(metadata map[string]any, key string) int {
	value, _ := metadata[key].(int)
	return value
}

func stringSliceFromMetadata(metadata map[string]any, key string) []string {
	switch value := metadata[key].(type) {
	case []string:
		return value
	default:
		return nil
	}
}

func intToString(value int) string {
	return strconv.Itoa(value)
}
