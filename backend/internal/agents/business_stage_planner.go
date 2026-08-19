package agents

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"cascade-demoops/backend/internal/model"
)

type BusinessStagePlannerAgent struct{}

func NewBusinessStagePlannerAgent() *BusinessStagePlannerAgent {
	return &BusinessStagePlannerAgent{}
}

func (a *BusinessStagePlannerAgent) PlanBusinessStages(
	ctx context.Context,
	project *model.ProjectContext,
	brief *model.RequirementBrief,
	report *model.MultimodalUnderstandingReport,
	productMap *model.ProductMap,
	intelligence *model.ProjectIntelligencePack,
	verifiedPlan *model.VerifiedInteractionPlan,
) (*model.BusinessStagePlan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if project == nil {
		return nil, errors.New("project context is required")
	}
	if intelligence == nil {
		intelligence = project.ProjectIntelligence
	}
	intentText := businessStageIntentText(project, brief, report, intelligence)
	now := time.Now().UTC()
	routeHints := businessRouteHints(project, productMap, intelligence)
	source := businessTargetSource{
		project:      project,
		brief:        brief,
		report:       report,
		intelligence: intelligence,
		verifiedPlan: verifiedPlan,
	}
	builder := &businessStagePlanBuilder{
		project:    project,
		intentText: intentText,
		routeHints: routeHints,
		source:     source,
		now:        now,
	}

	if builder.needsSessionSetup() {
		builder.addStage(stageSpec{
			id:             "session_setup",
			kind:           model.BusinessStageKindSessionSetup,
			title:          "登录并建立演示会话",
			objective:      "使用本地授权的演示账号完成登录，进入可演示的工作台状态。",
			actionType:     string(model.GraphActionFill),
			actionLabel:    "登录演示账号",
			successState:   "登录完成，页面进入工作台或目标业务页面。",
			routeState:     model.BusinessRouteStateUnauthenticated,
			entryRoute:     routeHints.login,
			expectedRoute:  routeHints.workspace,
			durationMS:     durationMSForIntentKeywords(intentText, "login", "signin", "sign in", "登录", "登陆", "登入"),
			keywords:       []string{"login", "signin", "sign in", "email", "password", "登录", "邮箱", "密码"},
			capture:        []string{"登录页表单", "登录后工作台"},
			nonDestructive: true,
		})
	}

	// Project names are user-provided values. Never infer them from the
	// normalized intent graph because that graph also contains action kinds,
	// selector aliases, and other generated metadata that may follow a phrase
	// such as "new project".
	projectName := intentProjectName(businessStageExplicitRequirementText(project, brief, report))
	wantsNewProject := containsAnyNormalized(intentText, "新建项目", "创建项目", "新增项目", "new project", "create project") || projectName != ""
	if wantsNewProject {
		builder.addStage(stageSpec{
			id:            "new_project_entry",
			kind:          model.BusinessStageKindBusinessAction,
			title:         "进入新建项目流程",
			objective:     "在工作台中找到并进入新建项目流程。",
			actionType:    string(model.GraphActionClick),
			actionLabel:   "点击新建项目入口",
			successState:  "新建项目表单、弹窗或创建流程可见。",
			routeState:    model.BusinessRouteStateWorkspace,
			entryRoute:    routeHints.workspace,
			expectedRoute: firstNonEmpty(routeHints.creation, routeHints.workspace),
			durationMS:    durationMSForIntentKeywords(intentText, "新建项目", "创建项目", "新增项目", "new project", "create project"),
			keywords:      []string{"新建项目", "创建项目", "新增项目", "new project", "create project", "project"},
			capture:       []string{"工作台项目入口", "新建项目流程"},
		})
		if projectName != "" {
			builder.addStage(stageSpec{
				id:            "project_name_input",
				kind:          model.BusinessStageKindBusinessInput,
				title:         "填写项目名称",
				objective:     "把演示项目名称填写为“" + projectName + "”。",
				actionType:    string(model.GraphActionFill),
				actionLabel:   "填写项目名称",
				inputSemantic: "project_name",
				inputValue:    projectName,
				successState:  "项目名称已填写为“" + projectName + "”。",
				routeState:    model.BusinessRouteStateCreationFlow,
				entryRoute:    firstNonEmpty(routeHints.creation, routeHints.workspace),
				expectedRoute: firstNonEmpty(routeHints.creation, routeHints.workspace),
				durationMS:    durationMSForIntentKeywords(intentText, "项目名称", "项目名", "project name", projectName),
				keywords:      []string{"项目名称", "项目名", "project name", "name", projectName},
				capture:       []string{"项目名称输入框", "已填写的项目名称"},
			})
		}
	}

	wantsDirectBuildMode := containsAnyNormalized(intentText, "实际构建", "直接构建", "直接生成", "direct build", "build directly")
	if containsAnyNormalized(intentText, "构建模式", "build mode", "builder mode") || wantsDirectBuildMode {
		modeTitle := "选择构建模式"
		modeObjective := "在项目创建流程中选择构建模式。"
		modeSuccess := "构建模式已被选中，后续可以启动 agent 构建。"
		modeKeywords := []string{"构建模式", "build mode", "builder mode", "构建", "mode"}
		if wantsDirectBuildMode {
			modeTitle = "切换为直接构建模式"
			modeObjective = "关闭仅规划模式，让项目提交后直接启动 agent 生成可运行代码。"
			modeSuccess = "仅规划模式已关闭，项目将以直接构建模式启动。"
			modeKeywords = append(modeKeywords, "计划", "规划", "plan", "direct build", "实际构建", "直接构建")
		}
		builder.addStage(stageSpec{
			id:            "select_build_mode",
			kind:          model.BusinessStageKindModeSelection,
			title:         modeTitle,
			objective:     modeObjective,
			actionType:    string(model.GraphActionClick),
			actionLabel:   "选择构建模式",
			inputSemantic: "build_mode",
			successState:  modeSuccess,
			routeState:    model.BusinessRouteStateCreationFlow,
			entryRoute:    firstNonEmpty(routeHints.creation, routeHints.workspace),
			expectedRoute: firstNonEmpty(routeHints.creation, routeHints.workspace),
			durationMS:    durationMSForIntentKeywords(intentText, "构建模式", "build mode", "builder mode"),
			keywords:      modeKeywords,
			capture:       []string{"构建模式选项", "已选择构建模式"},
		})
	}

	wantsBuild := containsAnyNormalized(intentText, "agent", "智能体", "实际构建", "开始构建", "启动构建", "run build", "start build", "生成", "构建")
	if wantsBuild {
		builder.addStage(stageSpec{
			id:            "start_agent_build",
			kind:          model.BusinessStageKindBusinessSubmit,
			title:         "启动 agent 实际构建",
			objective:     "提交项目创建信息并启动 agent 进入实际构建过程。",
			actionType:    string(model.GraphActionClick),
			actionLabel:   "启动 agent 构建",
			successState:  "agent 构建过程开始，页面出现构建进度、日志或项目详情。",
			routeState:    model.BusinessRouteStateProjectDetail,
			entryRoute:    firstNonEmpty(routeHints.creation, routeHints.workspace),
			expectedRoute: firstNonEmpty(routeHints.buildRunning, routeHints.projectDetail),
			durationMS:    durationMSForIntentKeywords(intentText, "启动", "开始", "提交", "启动构建", "开始构建", "run build", "start build", "submit", "run", "start"),
			keywords:      []string{"agent", "智能体", "开始构建", "启动构建", "实际构建", "生成", "构建", "build", "run", "start", "generate"},
			capture:       []string{"启动构建按钮", "构建开始状态"},
		})
	}

	waitMS := requiredObservationDurationMS(intentText)
	wantsObservation := containsAnyNormalized(intentText, "等待", "观察", "看实际发生", "看发生了什么", "实际构建演示", "构建演示", "progress", "log", "observe")
	if waitMS > 0 || wantsObservation {
		builder.addStage(stageSpec{
			id:             "observe_agent_progress",
			kind:           model.BusinessStageKindObserveProgress,
			title:          observationStageTitle(waitMS),
			objective:      observationStageObjective(waitMS),
			actionType:     string(model.GraphActionWait),
			actionLabel:    "观察构建进度",
			successState:   "构建进度、日志、预览或项目状态持续可见。",
			routeState:     model.BusinessRouteStateBuildRunning,
			entryRoute:     firstNonEmpty(routeHints.buildRunning, routeHints.projectDetail, routeHints.workspace),
			expectedRoute:  firstNonEmpty(routeHints.buildRunning, routeHints.projectDetail, routeHints.workspace),
			durationMS:     waitMS,
			keywords:       []string{"构建进度", "构建日志", "agent", "智能体", "progress", "log", "preview", "build"},
			capture:        []string{"构建过程", "构建日志或预览变化"},
			nonDestructive: true,
		})
	}

	wantsCompletion := wantsBuildCompletion(intentText)

	if len(builder.stages) == 0 && intentIsObservationOnly(intentText) {
		builder.addStage(stageSpec{
			id:             "observation_only",
			kind:           model.BusinessStageKindFinalObserve,
			title:          "仅观察产品首页",
			objective:      "按用户要求只观察首页，不生成真实业务动作。",
			actionType:     string(model.GraphActionInspect),
			actionLabel:    "观察首页",
			successState:   "首页保持可观察。",
			routeState:     model.BusinessRouteStateWorkspace,
			entryRoute:     firstNonEmpty(routeHints.workspace, routePathFromCandidate(project.ProductURL), "/"),
			expectedRoute:  firstNonEmpty(routeHints.workspace, routePathFromCandidate(project.ProductURL), "/"),
			durationMS:     durationMSForIntentKeywords(intentText, "首页", "观察", "home", "homepage", "inspect"),
			keywords:       []string{"首页", "观察", "home", "homepage", "inspect"},
			capture:        []string{"首页截图"},
			nonDestructive: true,
		})
	}

	if len(builder.stages) == 0 {
		builder.addStage(stageSpec{
			id:            "primary_business_action",
			kind:          model.BusinessStageKindBusinessAction,
			title:         "执行核心业务动作",
			objective:     firstNonEmpty(intentText, "围绕用户需求执行核心业务动作。"),
			actionType:    string(model.GraphActionClick),
			actionLabel:   "核心业务动作",
			successState:  "目标业务状态可见。",
			routeState:    model.BusinessRouteStateWorkspace,
			entryRoute:    firstNonEmpty(routeHints.workspace, "/"),
			expectedRoute: firstNonEmpty(routeHints.workspace, "/"),
			durationMS:    durationMSForIntentKeywords(intentText, intentKeywordsForText(intentText)...),
			keywords:      intentKeywordsForText(intentText),
			capture:       []string{"核心业务控件", "业务结果状态"},
		})
	}

	finalObjective := "停留在最终业务页面，截图并让观众看清楚当前结果。"
	finalSuccessState := "最终业务状态保持可观察。"
	finalKeywords := []string{"结果", "状态", "预览", "详情", "result", "preview", "detail"}
	if wantsCompletion {
		finalObjective = "轮询等待明确的 Agent 构建完成结果，完成后停留在最终业务页面并截图。"
		finalSuccessState = "页面出现由产品代码证据绑定的 Agent 构建完成结果，而不是仅有加载状态或构建中状态。"
		finalKeywords = append(finalKeywords, "构建完成", "全部步骤完成", "编写完成", "build complete", "build_complete", "all complete", "all steps", "build-result", "completed")
	}
	builder.addStage(stageSpec{
		id:             "final_observe",
		kind:           model.BusinessStageKindFinalObserve,
		title:          "收束并观察最终状态",
		objective:      finalObjective,
		actionType:     string(model.GraphActionInspect),
		actionLabel:    "观察最终状态",
		successState:   finalSuccessState,
		routeState:     builder.finalRouteState(),
		entryRoute:     builder.finalEntryRoute(),
		expectedRoute:  builder.finalEntryRoute(),
		durationMS:     durationMSForIntentKeywords(intentText, "最终", "收束", "结果", "状态", "预览", "详情", "final", "result", "preview", "detail"),
		keywords:       finalKeywords,
		capture:        []string{"最终状态截图"},
		nonDestructive: true,
	})

	if wantsPlayableKeyboardVerification(intentText) {
		playableName := firstNonEmpty(projectName, "游戏")
		builder.addStage(stageSpec{
			id:             "playable_preview",
			kind:           model.BusinessStageKindFinalObserve,
			title:          "打开并核验" + playableName + "试玩界面",
			objective:      "确认最终预览中真实显示" + playableName + "棋盘、得分和键盘操作说明。",
			actionType:     string(model.GraphActionInspect),
			actionLabel:    "核验可试玩预览",
			successState:   playableName + "棋盘、得分和方向/旋转操作说明均可见。",
			routeState:     builder.finalRouteState(),
			entryRoute:     builder.finalEntryRoute(),
			expectedRoute:  builder.finalEntryRoute(),
			durationMS:     5000,
			keywords:       []string{"俄罗斯方块", "棋盘", "得分", "操作说明", "预览", "tetris", "board", "score", "controls", "preview"},
			capture:        []string{playableName + "棋盘", "得分", "键盘操作说明"},
			nonDestructive: true,
		})
		builder.addStage(stageSpec{
			id:            "verify_playable_controls",
			kind:          model.BusinessStageKindFinalObserve,
			title:         "用键盘实际试玩" + playableName,
			objective:     "依次按左、右、下和旋转键，核验方块位置或形状确实发生画面变化。",
			actionType:    string(model.GraphActionPress),
			actionLabel:   "按方向键试玩",
			successState:  "按键后棋盘画面发生变化，证明游戏可由键盘实际操作。",
			routeState:    builder.finalRouteState(),
			entryRoute:    builder.finalEntryRoute(),
			expectedRoute: builder.finalEntryRoute(),
			durationMS:    6000,
			keywords:      []string{"按左", "按右", "按下", "旋转", "方向键", "键盘", "试玩", "位置", "形状", "ArrowLeft", "ArrowRight", "ArrowDown", "ArrowUp"},
			capture:       []string{"按键前棋盘", "按键后棋盘变化"},
			parameters: map[string]string{
				"keys":               "ArrowLeft,ArrowRight,ArrowDown,ArrowUp",
				"inter_key_delay_ms": "350",
				"focus_preview":      "true",
			},
			nonDestructive: true,
		})
	}

	return builder.plan(), nil
}

type stageSpec struct {
	id             string
	kind           model.BusinessStageKind
	title          string
	objective      string
	actionType     string
	actionLabel    string
	inputSemantic  string
	inputValue     string
	successState   string
	routeState     model.BusinessRouteState
	entryRoute     string
	expectedRoute  string
	durationMS     int
	keywords       []string
	capture        []string
	parameters     map[string]string
	nonDestructive bool
}

type intentDurationHint struct {
	ValueMS    int
	Context    string
	CenterRune int
	Maximum    bool
	FinalFilm  bool
}

var intentDurationPattern = regexp.MustCompile(`(?i)(\d+)\s*(毫秒|ms|秒|s|sec|secs|second|seconds|分钟|mins|minutes|min|m)`)

const maxDurationKeywordDistanceRunes = 16

func durationMSForIntentKeywords(intentText string, keywords ...string) int {
	normalized := normalizeIntentText(intentText)
	hints := durationHintsFromIntent(intentText)
	if len(hints) == 0 {
		return 0
	}
	if len(keywords) == 0 {
		best := 0
		for _, hint := range hints {
			best = maxInt(best, hint.ValueMS)
		}
		return best
	}
	best := 0
	bestDistance := 0
	hasFinalFilmPipeline := durationIntentHasFinalFilmPipeline(normalized)
	for _, hint := range hints {
		if hint.Maximum || hint.FinalFilm || (hasFinalFilmPipeline && hint.ValueMS >= 60*1000) {
			continue
		}
		distance, ok := nearestKeywordDistance(normalized, hint.CenterRune, keywords)
		if !ok || distance > maxDurationKeywordDistanceRunes {
			continue
		}
		if best == 0 || distance < bestDistance || (distance == bestDistance && hint.ValueMS > best) {
			best = hint.ValueMS
			bestDistance = distance
		}
	}
	return best
}

func durationIntentHasFinalFilmPipeline(text string) bool {
	return containsAnyNormalized(text, "ffmpeg") && containsAnyNormalized(text,
		"最终 mp4", "最终mp4", "最终成片", "合成为最终成片", "合成最终成片", "final mp4", "final film",
	)
}

func requiredObservationDurationMS(intentText string) int {
	return durationMSForIntentKeywords(intentText, "等待", "观察", "看实际发生", "看发生了什么", "实际构建演示", "构建演示", "progress", "log", "observe", "wait")
}

func durationHintsFromIntent(intentText string) []intentDurationHint {
	normalized := normalizeIntentText(intentText)
	if normalized == "" {
		return nil
	}
	matches := intentDurationPattern.FindAllStringSubmatchIndex(normalized, -1)
	if len(matches) == 0 {
		return nil
	}
	runes := []rune(normalized)
	out := []intentDurationHint{}
	for _, match := range matches {
		if len(match) < 6 {
			continue
		}
		if durationMatchEmbeddedInIdentifier(normalized, match) {
			continue
		}
		value, err := strconv.Atoi(normalized[match[2]:match[3]])
		if err != nil || value <= 0 {
			continue
		}
		unit := normalized[match[4]:match[5]]
		durationMS := durationValueToMS(value, unit)
		if durationMS <= 0 {
			continue
		}
		startRune := runeCount(normalized[:match[0]])
		endRune := runeCount(normalized[:match[1]])
		windowStart := maxInt(0, startRune-18)
		windowEnd := minInt(len(runes), endRune+18)
		out = append(out, intentDurationHint{
			ValueMS:    durationMS,
			Context:    string(runes[windowStart:windowEnd]),
			CenterRune: (startRune + endRune) / 2,
			Maximum:    durationHintIsMaximum(runes, startRune, endRune),
			FinalFilm:  durationHintIsFinalFilm(runes, startRune, endRune),
		})
	}
	return out
}

func durationMatchEmbeddedInIdentifier(text string, match []int) bool {
	if len(match) < 6 || match[0] < 0 || match[1] < 0 {
		return true
	}
	if match[0] > 0 {
		previous, _ := utf8.DecodeLastRuneInString(text[:match[0]])
		if isASCIIIdentifierRune(previous) {
			return true
		}
	}
	unit := strings.ToLower(text[match[4]:match[5]])
	if unit != "毫秒" && unit != "秒" && unit != "分钟" && match[1] < len(text) {
		next, _ := utf8.DecodeRuneInString(text[match[1]:])
		if isASCIIIdentifierRune(next) {
			return true
		}
	}
	return false
}

func isASCIIIdentifierRune(value rune) bool {
	return value == '_' || (value >= '0' && value <= '9') || (value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z')
}

func durationHintIsFinalFilm(runes []rune, startRune int, endRune int) bool {
	windowStart := maxInt(0, startRune-20)
	windowEnd := minInt(len(runes), endRune+20)
	context := normalizeIntentText(string(runes[windowStart:windowEnd]))
	return containsAnyNormalized(context,
		"最终成片", "成片时长", "最终输出", "输出 mp4", "输出mp4", "最终 mp4", "最终mp4", "mp4 成片", "mp4成片", "ffmpeg",
		"真实操作演示", "演示时长", "整段演示", "完整演示", "演示视频", "编码演示", "产出一条", "节奏清晰",
		"final film", "final video", "final mp4", "video duration",
	)
}

func durationHintIsMaximum(runes []rune, startRune int, endRune int) bool {
	windowStart := maxInt(0, startRune-14)
	windowEnd := minInt(len(runes), endRune+10)
	context := normalizeIntentText(string(runes[windowStart:windowEnd]))
	return containsAnyNormalized(context,
		"最多", "至多", "不超过", "最大", "上限", "超时", "最长", "max", "maximum", "up to", "timeout", "at most",
	)
}

const maxBuildCompletionWaitMS = 20 * 60 * 1000

func wantsBuildCompletion(intentText string) bool {
	text := normalizeIntentText(intentText)
	return containsAnyNormalized(text,
		"等待 agent 真正", "等待agent真正", "直到 agent", "直到agent", "构建完成", "编写完", "编写完成",
		"全部步骤完成", "所有步骤完成", "明确 build_complete", "build_complete", "build complete", "all_complete",
		"all complete", "all steps complete", "wait until complete", "wait for completion",
	)
}

func completionWaitTimeoutMS(intentText string) int {
	if !wantsBuildCompletion(intentText) {
		return 0
	}
	best := 0
	normalized := normalizeIntentText(intentText)
	keywords := []string{"等待", "直到", "构建完成", "编写完", "完成", "build_complete", "build complete", "all complete", "wait", "timeout"}
	for _, hint := range durationHintsFromIntent(intentText) {
		if !hint.Maximum {
			continue
		}
		if distance, ok := nearestKeywordDistance(normalized, hint.CenterRune, keywords); !ok || distance > maxDurationKeywordDistanceRunes+8 {
			continue
		}
		best = maxInt(best, hint.ValueMS)
	}
	if best <= 0 {
		best = maxBuildCompletionWaitMS
	}
	return minInt(best, maxBuildCompletionWaitMS)
}

func wantsPlayableKeyboardVerification(intentText string) bool {
	text := normalizeIntentText(intentText)
	keyboardRequested := containsAnyNormalized(text,
		"键盘", "方向键", "按左", "按右", "按下", "旋转", "arrowleft", "arrowright", "arrowdown", "arrowup", "keyboard",
	)
	playableResultRequested := containsAnyNormalized(text,
		"实际可玩", "实际试玩", "试玩", "可操作", "俄罗斯方块", "tetris", "棋盘", "方块位置", "方块形状", "playable",
	)
	return keyboardRequested && playableResultRequested
}

func nearestKeywordDistance(normalizedIntent string, centerRune int, keywords []string) (int, bool) {
	best := 0
	found := false
	for _, keyword := range keywords {
		keyword = normalizeIntentText(keyword)
		if keyword == "" {
			continue
		}
		offset := 0
		remaining := normalizedIntent
		for {
			idx := strings.Index(remaining, keyword)
			if idx < 0 {
				break
			}
			startRune := runeCount(normalizedIntent[:offset+idx])
			keywordCenter := startRune + maxInt(1, runeCount(keyword))/2
			distance := absInt(centerRune - keywordCenter)
			if !found || distance < best {
				best = distance
				found = true
			}
			next := idx + len(keyword)
			offset += next
			if next >= len(remaining) {
				break
			}
			remaining = remaining[next:]
		}
	}
	return best, found
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func durationValueToMS(value int, unit string) int {
	unit = strings.ToLower(strings.TrimSpace(unit))
	switch unit {
	case "毫秒", "ms":
		return value
	case "分钟", "min", "mins", "minute", "minutes", "m":
		return value * 60 * 1000
	default:
		return value * 1000
	}
}

func runeCount(value string) int {
	return len([]rune(value))
}

func observationStageTitle(durationMS int) string {
	if durationMS > 0 {
		return fmt.Sprintf("观察业务执行过程 %d 秒", durationMS/1000)
	}
	return "观察业务执行过程"
}

func observationStageObjective(durationMS int) string {
	if durationMS > 0 {
		return fmt.Sprintf("进入目标页面后持续观察业务执行过程，保留不少于 %d 秒的自然等待。", durationMS/1000)
	}
	return "进入目标页面后观察业务执行过程，等待页面出现可解释的进度、日志、预览或结果状态。"
}

type businessStagePlanBuilder struct {
	project    *model.ProjectContext
	intentText string
	routeHints businessRouteSet
	source     businessTargetSource
	now        time.Time
	stages     []model.BusinessStage
}

func (b *businessStagePlanBuilder) needsSessionSetup() bool {
	return b.project.DemoAccount != nil || containsAnyNormalized(b.intentText, "登录", "登陆", "login", "sign in", "signin")
}

func (b *businessStagePlanBuilder) addStage(spec stageSpec) {
	order := len(b.stages) + 1
	stageID := "business_stage_" + spec.id
	targets := b.source.targetsForStage(spec)
	evidence := evidenceRefsForBusinessTargets(targets)
	requirements := b.source.evidenceRequirementsForStage(spec, targets)
	uncertainties := businessStageUncertainties(spec, requirements, evidence)
	entryRoute := firstNonEmpty(spec.entryRoute, "/")
	expectedRoute := firstNonEmpty(spec.expectedRoute, spec.entryRoute, "/")
	if spec.kind == model.BusinessStageKindSessionSetup {
		if observed := authenticationEntryRouteFromTargets(targets); observed != "" {
			entryRoute = observed
		}
		if workspace := authenticatedWorkspaceRouteFromTargets(targets); workspace != "" {
			expectedRoute = workspace
		}
	}
	stage := model.BusinessStage{
		ID:                       stageID,
		Order:                    order,
		Kind:                     spec.kind,
		Title:                    spec.title,
		Objective:                spec.objective,
		UserIntent:               b.intentText,
		RouteState:               spec.routeState,
		EntryRoute:               entryRoute,
		ExpectedRouteAfterAction: expectedRoute,
		DurationMS:               spec.durationMS,
		Action: model.BusinessActionSemantics{
			Type:           spec.actionType,
			Label:          spec.actionLabel,
			InputSemantic:  spec.inputSemantic,
			InputValue:     spec.inputValue,
			SuccessState:   spec.successState,
			WaitConditions: businessStageWaitConditions(spec),
			CapturePoints:  spec.capture,
			Parameters:     spec.parameters,
			NonDestructive: businessStageIsApprovedNonDestructive(spec),
		},
		Targets:              targets,
		EvidenceRequirements: requirements,
		Uncertainties:        uncertainties,
		EvidenceRefs:         uniqueEvidenceRefs(evidence),
		Confidence:           businessStageConfidence(spec, targets, requirements),
	}
	b.stages = append(b.stages, stage)
}

func authenticationEntryRouteFromTargets(targets []model.BusinessTargetCandidate) string {
	for _, target := range targets {
		for _, candidate := range target.Alternatives {
			if candidate.SourceKind != "page_scan" || !strings.EqualFold(strings.TrimSpace(candidate.ObservedPageRole), "authentication") || !strings.EqualFold(strings.TrimSpace(candidate.ObservedFormRole), "authentication") {
				continue
			}
			if route := firstNonEmpty(candidate.ObservedURL, candidate.ObservedRouteTemplate); strings.TrimSpace(route) != "" {
				return route
			}
		}
	}
	return ""
}

func authenticatedWorkspaceRouteFromTargets(targets []model.BusinessTargetCandidate) string {
	for _, target := range targets {
		if target.IsVerified && strings.TrimSpace(target.URL) != "" {
			return target.URL
		}
	}
	return ""
}

func businessStageIsApprovedNonDestructive(spec stageSpec) bool {
	if spec.nonDestructive || spec.kind == model.BusinessStageKindObserveProgress || spec.kind == model.BusinessStageKindFinalObserve || spec.kind == model.BusinessStageKindSessionSetup {
		return true
	}
	allowedAction := map[string]model.GraphActionType{
		"new_project_entry":  model.GraphActionClick,
		"project_name_input": model.GraphActionFill,
		"select_build_mode":  model.GraphActionClick,
		"start_agent_build":  model.GraphActionClick,
	}
	want, ok := allowedAction[spec.id]
	if !ok || model.GraphActionType(spec.actionType) != want {
		return false
	}
	semanticText := strings.Join(append([]string{
		spec.title, spec.objective, spec.actionLabel, spec.inputSemantic,
		spec.inputValue, spec.successState,
	}, spec.keywords...), " ")
	return !containsAnyNormalized(semanticText,
		"delete", "remove", "destroy", "payment", "pay", "billing", "purchase", "refund",
		"permission", "role", "api key", "secret", "token", "删除", "移除", "销毁", "支付",
		"购买", "退款", "账单", "权限", "角色", "密钥", "令牌",
	)
}

func (b *businessStagePlanBuilder) finalRouteState() model.BusinessRouteState {
	for i := len(b.stages) - 1; i >= 0; i-- {
		if b.stages[i].RouteState != "" {
			return b.stages[i].RouteState
		}
	}
	return model.BusinessRouteStateWorkspace
}

func (b *businessStagePlanBuilder) finalEntryRoute() string {
	for i := len(b.stages) - 1; i >= 0; i-- {
		if route := firstNonEmpty(b.stages[i].ExpectedRouteAfterAction, b.stages[i].EntryRoute); route != "" {
			return route
		}
	}
	return firstNonEmpty(b.routeHints.workspace, "/")
}

func (b *businessStagePlanBuilder) plan() *model.BusinessStagePlan {
	coreCount := 0
	uncertainties := []model.StageUncertainty{}
	evidence := []model.EvidenceRef{}
	for _, stage := range b.stages {
		if businessStageKindIsCore(stage.Kind) {
			coreCount++
		}
		uncertainties = append(uncertainties, stage.Uncertainties...)
		evidence = append(evidence, stage.EvidenceRefs...)
	}
	intentID := ""
	if b.source.intelligence != nil && b.source.intelligence.DemoIntent != nil {
		intentID = b.source.intelligence.DemoIntent.ID
	}
	return &model.BusinessStagePlan{
		ID:                     "business_stage_plan_" + b.project.ID,
		ProjectID:              b.project.ID,
		IntentID:               intentID,
		SchemaVersion:          model.ProjectIntelligencePackSchemaVersion,
		Stages:                 b.stages,
		CoreBusinessStageCount: coreCount,
		BlockingUncertainties:  uncertainties,
		EvidenceRefs:           uniqueEvidenceRefs(evidence),
		Confidence:             businessStagePlanConfidence(b.stages),
		CreatedAt:              b.now,
	}
}

type businessTargetSource struct {
	project      *model.ProjectContext
	brief        *model.RequirementBrief
	report       *model.MultimodalUnderstandingReport
	intelligence *model.ProjectIntelligencePack
	verifiedPlan *model.VerifiedInteractionPlan
}

func (s businessTargetSource) targetsForStage(spec stageSpec) []model.BusinessTargetCandidate {
	targets := []model.BusinessTargetCandidate{}
	targets = append(targets, s.targetsFromVerifiedPlan(spec)...)
	targets = append(targets, s.targetsFromCodeSnapshots(spec)...)
	targets = append(targets, s.targetsFromFeatureTrace(spec)...)
	targets = append(targets, s.resultTargetsForStage(spec)...)
	targets = uniqueBusinessTargetCandidates(targets)
	if len(targets) > 5 {
		targets = targets[:5]
	}
	return targets
}

func (s businessTargetSource) targetsFromCodeSnapshots(spec stageSpec) []model.BusinessTargetCandidate {
	preferredTestID := ""
	switch spec.id {
	case "final_observe":
		preferredTestID = "build-result-card"
	case "playable_preview", "verify_playable_controls":
		preferredTestID = "preview-iframe"
	default:
		return nil
	}
	if s.report == nil || len(s.report.CodeSnapshots) == 0 {
		return nil
	}
	selector := "[data-testid='" + preferredTestID + "']"
	out := []model.BusinessTargetCandidate{}
	for _, snapshot := range s.report.CodeSnapshots {
		for _, component := range snapshot.Components {
			for _, hint := range component.SelectorHints {
				if testIDFromSelector(hint) != preferredTestID {
					continue
				}
				out = append(out, model.BusinessTargetCandidate{
					ID:                 "target_code_" + shortHash(spec.id+component.ID+selector),
					Label:              firstNonEmpty(component.Name, spec.actionLabel),
					Kind:               spec.actionType,
					Selector:           selector,
					TestID:             preferredTestID,
					Route:              spec.entryRoute,
					ComponentRef:       firstNonEmpty(component.ID, component.Name),
					SelectorScore:      96,
					Confidence:         maxFloat64(component.Confidence, 0.76),
					IsVerified:         false,
					VerificationStatus: "code_evidence",
					VerificationSource: "local_code_snapshot",
					EvidenceRefs:       component.EvidenceRefs,
				})
				break
			}
		}
		for _, insight := range snapshot.Selectors {
			if testIDFromSelector(insight.Value) != preferredTestID {
				continue
			}
			out = append(out, model.BusinessTargetCandidate{
				ID:                 "target_code_selector_" + shortHash(spec.id+insight.FilePathHashSHA256+selector),
				Label:              spec.actionLabel,
				Kind:               spec.actionType,
				Selector:           selector,
				TestID:             preferredTestID,
				Route:              spec.entryRoute,
				SelectorScore:      int(maxFloat64(insight.StabilityScore*100, 92)),
				Confidence:         maxFloat64(insight.Confidence, 0.76),
				IsVerified:         false,
				VerificationStatus: "code_evidence",
				VerificationSource: "local_code_snapshot",
				EvidenceRefs:       insight.EvidenceRefs,
			})
		}
	}
	return out
}

func (s businessTargetSource) resultTargetsForStage(spec stageSpec) []model.BusinessTargetCandidate {
	if spec.id != "new_project_entry" || s.verifiedPlan == nil {
		return nil
	}
	out := []model.BusinessTargetCandidate{}
	for _, action := range s.verifiedPlan.Actions {
		if action.VerificationStatus != "verified" {
			continue
		}
		candidate := businessTargetFromVerifiedAction(action)
		if !isExplicitNewProjectResultCandidate(candidate) {
			continue
		}
		candidate.ID = "result_" + candidate.ID
		out = append(out, candidate)
	}
	return out
}

func (s businessTargetSource) targetsFromVerifiedPlan(spec stageSpec) []model.BusinessTargetCandidate {
	if s.verifiedPlan == nil {
		return nil
	}
	out := []model.BusinessTargetCandidate{}
	for _, action := range s.verifiedPlan.Actions {
		if spec.kind == model.BusinessStageKindSessionSetup && verifiedLoginOutcomeAction(action) {
			out = append(out, businessTargetFromVerifiedAction(action))
			continue
		}
		if !businessActionMatchesStage(spec, action.Label, action.Kind, action.Selector, action.InputValue, action.ComponentRef) {
			continue
		}
		if spec.kind != model.BusinessStageKindSessionSetup && looksLikeLoginAction(action.Label, action.Selector) {
			continue
		}
		out = append(out, businessTargetFromVerifiedAction(action))
	}
	return out
}

func verifiedLoginOutcomeAction(action model.VerifiedInteractionAction) bool {
	if action.ID != "intent_login_observe" || action.VerificationStatus != "verified" || strings.TrimSpace(action.URL) == "" {
		return false
	}
	for _, ref := range action.EvidenceRefs {
		if ref.Kind == model.EvidenceKindBrowserScan || ref.Kind == model.EvidenceKindBrowserTrace {
			return true
		}
	}
	return false
}

func (s businessTargetSource) targetsFromFeatureTrace(spec stageSpec) []model.BusinessTargetCandidate {
	if s.intelligence == nil || s.intelligence.FeatureTrace == nil {
		return nil
	}
	out := []model.BusinessTargetCandidate{}
	for _, trace := range s.intelligence.FeatureTrace.Traces {
		traceText := strings.Join(append([]string{trace.IntentLabel, trace.IntentGoalID}, trace.MatchedComponents...), " ")
		if !containsAnyNormalized(traceText, spec.keywords...) && !businessStageMatchesTraceKind(spec, trace) {
			continue
		}
		for _, probe := range trace.SelectorEvidence {
			if !businessProbeAllowedForStage(spec, probe) {
				continue
			}
			out = append(out, businessTargetFromProbe(probe))
		}
		if len(trace.SelectorEvidence) == 0 && len(trace.MatchedComponents) > 0 {
			for _, component := range trace.MatchedComponents {
				out = append(out, model.BusinessTargetCandidate{
					ID:           "target_component_" + shortHash(spec.id+component),
					IntentGoalID: trace.IntentGoalID,
					Label:        firstNonEmpty(trace.IntentLabel, spec.actionLabel),
					Kind:         spec.actionType,
					Route:        spec.entryRoute,
					ComponentRef: component,
					Confidence:   maxFloat64(trace.Confidence, 0.55),
					EvidenceRefs: trace.EvidenceRefs,
				})
			}
		}
	}
	return out
}

func (s businessTargetSource) evidenceRequirementsForStage(spec stageSpec, targets []model.BusinessTargetCandidate) []model.EvidenceRequirement {
	routeSatisfied := strings.TrimSpace(spec.entryRoute) != ""
	targetSatisfied := len(targets) > 0 || spec.kind == model.BusinessStageKindObserveProgress || spec.kind == model.BusinessStageKindFinalObserve
	selectorSatisfied := false
	componentSatisfied := false
	for _, target := range targets {
		if selectorUsableForBusinessAction(target.Selector) || len(target.Alternatives) > 0 {
			selectorSatisfied = true
		}
		if target.ComponentRef != "" || len(target.EvidenceRefs) > 0 {
			componentSatisfied = true
		}
	}
	if spec.kind == model.BusinessStageKindObserveProgress || spec.kind == model.BusinessStageKindFinalObserve {
		selectorSatisfied = true
	}
	if spec.kind == model.BusinessStageKindSessionSetup {
		selectorSatisfied = true
		componentSatisfied = targetSatisfied
	}
	return []model.EvidenceRequirement{
		{
			Kind:       "route",
			Required:   true,
			Satisfied:  routeSatisfied,
			Summary:    "阶段必须绑定产品域内的入口路由或状态。",
			FieldPath:  "business_stage_plan.stages[].entry_route",
			Confidence: boolConfidence(routeSatisfied),
		},
		{
			Kind:       "component_or_semantic_target",
			Required:   businessStageKindIsCore(spec.kind),
			Satisfied:  targetSatisfied || componentSatisfied,
			Summary:    "核心业务阶段需要组件证据或清晰的语义目标，供 server browser agent 自适应定位。",
			FieldPath:  "business_stage_plan.stages[].targets",
			Confidence: boolConfidence(targetSatisfied || componentSatisfied),
		},
		{
			Kind:       "selector_candidate",
			Required:   false,
			Satisfied:  selectorSatisfied,
			Summary:    "selector 可由页面扫描或 server browser agent 在语义约束内补全；App 端不因为缺 selector 删除阶段。",
			FieldPath:  "business_stage_plan.stages[].targets[].selector",
			Confidence: boolConfidence(selectorSatisfied),
		},
	}
}

func businessTargetFromVerifiedAction(action model.VerifiedInteractionAction) model.BusinessTargetCandidate {
	testID := testIDFromSelector(action.Selector)
	return model.BusinessTargetCandidate{
		ID:                 firstNonEmpty(action.ID, "target_verified_"+shortHash(action.Label+action.Selector)),
		IntentGoalID:       action.IntentGoalID,
		Label:              action.Label,
		Kind:               action.Kind,
		Selector:           action.Selector,
		TestID:             testID,
		URL:                action.URL,
		RouteRef:           action.RouteRef,
		Route:              routePathFromCandidate(action.URL),
		ComponentRef:       action.ComponentRef,
		SelectorScore:      action.SelectorScore,
		Confidence:         0.72,
		IsVerified:         action.VerificationStatus == "verified",
		VerificationStatus: action.VerificationStatus,
		VerificationSource: action.VerificationSource,
		EvidenceRefs:       action.EvidenceRefs,
		Alternatives:       action.Alternatives,
	}
}

func businessTargetFromProbe(probe model.InteractionProbe) model.BusinessTargetCandidate {
	testID := testIDFromSelector(probe.Selector)
	return model.BusinessTargetCandidate{
		ID:                 firstNonEmpty(probe.ID, "target_probe_"+shortHash(probe.Label+probe.Selector)),
		IntentGoalID:       probe.IntentGoalID,
		Label:              probe.Label,
		Kind:               probe.Kind,
		Selector:           probe.Selector,
		TestID:             testID,
		URL:                probe.URL,
		RouteRef:           probe.RouteRef,
		Route:              routePathFromCandidate(probe.URL),
		ComponentRef:       probe.ComponentRef,
		SelectorScore:      probe.SelectorScore,
		Confidence:         maxFloat64(probe.Score/10, probe.Score),
		IsVerified:         false,
		VerificationStatus: "code_evidence",
		VerificationSource: probe.Source,
		EvidenceRefs:       probe.EvidenceRefs,
		Alternatives:       probe.Alternatives,
	}
}

func testIDFromSelector(selector string) string {
	selector = strings.TrimSpace(selector)
	for _, marker := range []string{"data-testid=\"", "data-testid='"} {
		if index := strings.Index(selector, marker); index >= 0 {
			value := selector[index+len(marker):]
			if end := strings.IndexAny(value, "\"'"); end >= 0 {
				return strings.TrimSpace(value[:end])
			}
		}
	}
	return ""
}

func businessProbeAllowedForStage(spec stageSpec, probe model.InteractionProbe) bool {
	if spec.kind != model.BusinessStageKindSessionSetup && (probe.IsChrome || looksLikeLoginAction(probe.Label, probe.Selector)) {
		return false
	}
	if businessStageKindIsCore(spec.kind) && !probe.IsBusiness {
		return false
	}
	if !businessActionMatchesStage(spec, probe.Label, probe.Kind, probe.Selector, "", probe.ComponentRef) {
		return false
	}
	return true
}

func businessActionMatchesStage(spec stageSpec, label string, kind string, selector string, value string, componentRef string) bool {
	text := strings.Join([]string{label, kind, selector, value, componentRef}, " ")
	labelText := normalizeIntentText(label)
	selectorText := normalizeIntentText(strings.Join([]string{selector, componentRef}, " "))
	if spec.kind == model.BusinessStageKindFinalObserve {
		if spec.id == "final_observe" && containsAnyNormalized(text, "build-result-card", "build result", "build_complete", "all_complete", "构建完成", "全部步骤完成") {
			return true
		}
		if spec.id == "playable_preview" && containsAnyNormalized(text, "preview-iframe", "preview panel", "playable", "tetris", "棋盘", "得分") {
			return true
		}
	}
	wantAction := model.GraphActionType(spec.actionType)
	gotAction := graphActionTypeFromKind(kind, selector)
	if wantAction != "" && gotAction != wantAction {
		return false
	}
	switch spec.id {
	case "new_project_entry":
		if containsAnyNormalized(selectorText, "button-create-project", "create-project-button") && !containsAnyNormalized(selectorText, "button-new-project", "new-project-button") {
			return false
		}
		return containsAnyNormalized(labelText, "new project", "create project", "新建项目", "创建项目", "新增项目") ||
			containsAnyNormalized(selectorText, "button-new-project", "new-project-button", "new-project-entry", "create-project-entry")
	case "project_name_input":
		return containsAnyNormalized(text, "project name", "project-name", "project idea", "project-idea", "project prompt", "project-prompt", "项目名称", "项目名", "项目需求", "需求描述", "idea", "prompt", spec.inputValue)
	case "select_build_mode":
		return containsAnyNormalized(text, "build mode", "build-mode", "builder mode", "mode plan", "mode-plan", "plan mode", "plan-mode", "构建模式", "规划模式", "计划模式")
	case "start_agent_build":
		if containsAnyNormalized(labelText+" "+selectorText, "build mode", "build-mode", "builder mode", "mode-plan", "plan-mode", "构建模式", "规划模式", "计划模式") {
			return false
		}
		return containsAnyNormalized(labelText, "build", "generate", "run", "start", "构建", "生成", "启动", "开始") ||
			containsAnyNormalized(selectorText, "button-create-project", "create-project-button", "start-build", "run-build", "generate-app")
	}
	if containsAnyNormalized(text, spec.keywords...) {
		return true
	}
	if spec.inputValue != "" && containsAnyNormalized(text, spec.inputValue) {
		return true
	}
	switch spec.kind {
	case model.BusinessStageKindBusinessInput:
		return graphActionTypeFromKind(kind, selector) == model.GraphActionFill && containsAnyNormalized(text, "name", "项目", "input", "textarea")
	case model.BusinessStageKindModeSelection:
		return containsAnyNormalized(text, "mode", "构建", "build")
	case model.BusinessStageKindBusinessSubmit:
		return containsAnyNormalized(text, "submit", "start", "run", "generate", "build", "开始", "启动", "生成", "构建")
	case model.BusinessStageKindSessionSetup:
		return looksLikeLoginAction(label, selector) || containsAnyNormalized(text, "email", "password", "login", "signin", "登录", "密码")
	default:
		return false
	}
}

func businessStageMatchesTraceKind(spec stageSpec, trace model.FeatureGoalTrace) bool {
	switch spec.kind {
	case model.BusinessStageKindBusinessInput:
		return containsAnyNormalized(trace.IntentLabel, spec.inputValue, "项目名称", "project name")
	case model.BusinessStageKindModeSelection:
		return containsAnyNormalized(trace.IntentLabel, "构建模式", "build mode")
	case model.BusinessStageKindBusinessSubmit:
		return containsAnyNormalized(trace.IntentLabel, "agent", "构建", "build", "generate")
	default:
		return containsAnyNormalized(trace.IntentLabel, spec.actionLabel, spec.title)
	}
}

func businessStageUncertainties(spec stageSpec, requirements []model.EvidenceRequirement, evidence []model.EvidenceRef) []model.StageUncertainty {
	out := []model.StageUncertainty{}
	for _, req := range requirements {
		if req.Satisfied {
			continue
		}
		out = append(out, model.StageUncertainty{
			ID:              "uncertainty_" + spec.id + "_" + req.Kind,
			StageID:         "business_stage_" + spec.id,
			Kind:            req.Kind,
			Summary:         req.Summary,
			Blocking:        false,
			SuggestedAction: "继续读取与该需求目标相关的 route/component/API 代码，或让 server browser agent 在受约束范围内运行时确认。",
			EvidenceRefs:    evidence,
		})
	}
	return out
}

func businessStageWaitConditions(spec stageSpec) []string {
	switch spec.kind {
	case model.BusinessStageKindSessionSetup:
		return []string{"domcontentloaded", "authenticated_workspace_visible"}
	case model.BusinessStageKindObserveProgress:
		return []string{"domcontentloaded", "progress_or_log_changes_visible"}
	case model.BusinessStageKindFinalObserve:
		return []string{"domcontentloaded", "final_state_visible"}
	default:
		return []string{"domcontentloaded", "target_state_visible"}
	}
}

func businessStageConfidence(spec stageSpec, targets []model.BusinessTargetCandidate, requirements []model.EvidenceRequirement) float64 {
	score := 0.58
	if len(targets) > 0 {
		score += 0.16
	}
	for _, req := range requirements {
		if req.Satisfied {
			score += 0.04
		}
	}
	if spec.kind == model.BusinessStageKindObserveProgress || spec.kind == model.BusinessStageKindFinalObserve {
		score += 0.06
	}
	return minFloat64(score, 0.92)
}

func businessStagePlanConfidence(stages []model.BusinessStage) float64 {
	if len(stages) == 0 {
		return 0
	}
	total := 0.0
	for _, stage := range stages {
		total += stage.Confidence
	}
	return minFloat64(total/float64(len(stages)), 0.94)
}

func businessStageKindIsCore(kind model.BusinessStageKind) bool {
	switch kind {
	case model.BusinessStageKindBusinessAction, model.BusinessStageKindBusinessInput, model.BusinessStageKindModeSelection, model.BusinessStageKindBusinessSubmit:
		return true
	default:
		return false
	}
}

func evidenceRefsForBusinessTargets(targets []model.BusinessTargetCandidate) []model.EvidenceRef {
	refs := []model.EvidenceRef{}
	for _, target := range targets {
		refs = append(refs, target.EvidenceRefs...)
	}
	return uniqueEvidenceRefs(refs)
}

func uniqueBusinessTargetCandidates(targets []model.BusinessTargetCandidate) []model.BusinessTargetCandidate {
	out := []model.BusinessTargetCandidate{}
	seen := map[string]bool{}
	for _, target := range targets {
		key := firstNonEmpty(target.ID, target.Selector, target.ComponentRef, target.Label)
		if key == "" {
			continue
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, target)
	}
	return out
}

type businessRouteSet struct {
	login         string
	workspace     string
	creation      string
	projectDetail string
	buildRunning  string
}

func businessRouteHints(project *model.ProjectContext, productMap *model.ProductMap, intelligence *model.ProjectIntelligencePack) businessRouteSet {
	workspace := firstNonEmpty(
		semanticArchitectureRouteTemplate(intelligence, []string{"workspace", "dashboard", "app", "工作台", "控制台"}, false, false),
		appWorkspaceRouteTemplate(intelligence),
	)
	if workspace == "" || workspace == "/" {
		workspace = firstBusinessRouteFromProductMap(productMap, []string{"app", "dashboard", "workspace", "project"})
	}
	creation := firstNonEmpty(
		semanticArchitectureRouteTemplate(intelligence, []string{"new", "create", "project", "项目", "新建", "创建"}, false, false),
		firstExistingRouteTemplate([]string{"/workspace/projects/new", "/projects/new", "/project/new", "/app/projects/new", "/app"}, intelligence, firstNonEmpty(workspace, "/")),
	)
	projectDetail := firstNonEmpty(
		dynamicProjectRouteTemplate(intelligence, "/project/:id"),
		semanticArchitectureRouteTemplate(intelligence, []string{"project", "detail", "workspace", "项目", "详情"}, true, true),
	)
	buildRunning := firstNonEmpty(
		projectBuildRouteTemplate(intelligence, projectDetail),
		semanticArchitectureRouteTemplate(intelligence, []string{"build", "progress", "log", "preview", "agent", "构建", "进度", "日志", "预览"}, true, false),
	)
	return businessRouteSet{
		login:         userLoginRouteTemplate(intelligence),
		workspace:     firstNonEmpty(workspace, routePathFromCandidate(project.ProductURL), "/"),
		creation:      creation,
		projectDetail: projectDetail,
		buildRunning:  buildRunning,
	}
}

func semanticArchitectureRouteTemplate(intelligence *model.ProjectIntelligencePack, keywords []string, preferDynamic bool, requireDynamic bool) string {
	if intelligence == nil || intelligence.Architecture == nil {
		return ""
	}
	bestRoute := ""
	bestScore := 0
	for _, item := range intelligence.Architecture.RouteTree {
		path := normalizeRouteTemplate(item.Path)
		if !routeCandidateAllowed(path) {
			continue
		}
		dynamic := routeTemplateDynamic(path)
		if requireDynamic && !dynamic {
			continue
		}
		score := semanticArchitectureRouteScore(path, item, keywords)
		if score <= 0 {
			continue
		}
		if dynamic == preferDynamic {
			score++
		}
		if item.AuthRequired {
			score++
		}
		if score > bestScore || (score == bestScore && routeSpecificity(path) > routeSpecificity(bestRoute)) {
			bestRoute = path
			bestScore = score
		}
	}
	return bestRoute
}

func semanticArchitectureRouteScore(path string, item model.ArchitectureRouteNode, keywords []string) int {
	text := normalizeIntentText(strings.Join(append([]string{path, item.Name}, item.ComponentRefs...), " "))
	score := 0
	for _, keyword := range keywords {
		keyword = normalizeIntentText(keyword)
		if keyword == "" {
			continue
		}
		if strings.Contains(text, keyword) {
			score += 2
		}
	}
	if containsAnyNormalized(text, "new", "create", "新建", "创建") && containsAnyNormalized(strings.Join(keywords, " "), "new", "create", "新建", "创建") {
		score += 3
	}
	if routeTemplateDynamic(path) && containsAnyNormalized(strings.Join(keywords, " "), "detail", "build", "progress", "详情", "构建", "进度") {
		score += 2
	}
	if strings.Contains(path, "/build") && containsAnyNormalized(strings.Join(keywords, " "), "build", "progress", "构建", "进度") {
		score += 3
	}
	return score
}

func routeSpecificity(route string) int {
	if route == "" {
		return 0
	}
	score := strings.Count(strings.Trim(route, "/"), "/") + 1
	if routeTemplateDynamic(route) {
		score++
	}
	return score
}

func firstBusinessRouteFromProductMap(productMap *model.ProductMap, keywords []string) string {
	if productMap == nil {
		return ""
	}
	for _, route := range productMap.Routes {
		if route == nil {
			continue
		}
		if containsAnyNormalized(route.Path+" "+route.Name, keywords...) {
			if path := routePathFromCandidate(route.Path); path != "" {
				return path
			}
		}
	}
	for _, page := range productMap.Pages {
		if page == nil {
			continue
		}
		if containsAnyNormalized(page.URL+" "+page.Title, keywords...) {
			if path := routePathFromCandidate(page.URL); path != "" {
				return path
			}
		}
	}
	return ""
}

func businessStageIntentText(project *model.ProjectContext, brief *model.RequirementBrief, report *model.MultimodalUnderstandingReport, intelligence *model.ProjectIntelligencePack) string {
	parts := []string{}
	if project != nil {
		parts = append(parts, project.ProductDescription, project.TargetAudience, strings.Join(project.MustShow, " "), strings.Join(project.MustNotShow, " "))
		if project.Inputs != nil {
			parts = append(parts, project.Inputs.RawUserPrompt)
			for _, doc := range project.Inputs.RequirementDocuments {
				parts = append(parts, doc.Title, doc.Body)
			}
		}
	}
	if brief != nil {
		parts = append(parts, brief.Scenario, brief.Objective, strings.Join(brief.MustShow, " "), strings.Join(brief.MustNotShow, " "))
	}
	if report != nil && report.RequirementBrief != nil {
		parts = append(parts, report.RequirementBrief.Scenario, report.RequirementBrief.Objective)
	}
	if intelligence != nil && intelligence.DemoIntent != nil {
		parts = append(parts, intelligence.DemoIntent.Objective, intelligence.DemoIntent.TargetAudience)
		for _, goal := range intelligence.DemoIntent.Goals {
			parts = append(parts, goal.Label, goal.Kind, goal.PreferredAction, goal.TargetPageHint, goal.SuccessState, strings.Join(goal.TargetKeywords, " "))
		}
	}
	return normalizeIntentText(strings.Join(parts, " "))
}

func businessStageExplicitRequirementText(project *model.ProjectContext, brief *model.RequirementBrief, report *model.MultimodalUnderstandingReport) string {
	parts := []string{}
	if project != nil {
		parts = append(parts, project.ProductDescription, strings.Join(project.MustShow, " "), strings.Join(project.MustNotShow, " "))
		if project.Inputs != nil {
			parts = append(parts, project.Inputs.RawUserPrompt)
			for _, doc := range project.Inputs.RequirementDocuments {
				parts = append(parts, doc.Title, doc.Body)
			}
		}
	}
	if brief != nil {
		parts = append(parts, brief.Scenario, brief.Objective, strings.Join(brief.MustShow, " "), strings.Join(brief.MustNotShow, " "))
	}
	if report != nil && report.RequirementBrief != nil {
		parts = append(parts, report.RequirementBrief.Scenario, report.RequirementBrief.Objective)
	}
	return normalizeIntentText(strings.Join(parts, " "))
}

func intentIsObservationOnly(intentText string) bool {
	if intentText == "" {
		return false
	}
	return containsAnyNormalized(intentText, "只观察", "仅观察", "观察首页", "不执行真实业务动作", "不要执行", "inspect only", "observation only", "homepage only") &&
		!containsAnyNormalized(intentText, "新建项目", "创建项目", "新增项目", "项目名称", "项目名", "构建模式", "开始构建", "启动构建", "实际构建", "new project", "create project", "project name", "build mode", "start build")
}

func boolConfidence(ok bool) float64 {
	if ok {
		return 0.8
	}
	return 0.35
}

func maxFloat64(left float64, right float64) float64 {
	if left > right {
		return left
	}
	return right
}
