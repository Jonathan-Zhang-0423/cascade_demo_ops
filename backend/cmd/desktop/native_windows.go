//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"cascade-demoops/backend/internal/app"
	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
)

const (
	winClassName = "CascadeDemoOpsNativeWindow"
	winTitle     = "Cascade DemoOps Desktop"

	idProductURL        = 1001
	idLocalRepoPath     = 1002
	idGitRepoURL        = 1003
	idDemoUsername      = 1004
	idDemoPassword      = 1005
	idRequirement       = 1006
	idGenerateButton    = 1007
	idSaveButton        = 1008
	idStatusList        = 1009
	idMarkdown          = 1010
	idStageJSON         = 1011
	idOutlineJSON       = 1012
	idHeaderTitle       = 1013
	idHeaderMeta        = 1014
	idEngineStatus      = 1015
	idWorkflowStatus    = 1016
	idPreviewTitle      = 1017
	idInputGroup        = 1018
	idInputHint         = 1019
	idSourceHint        = 1020
	idCredentialHint    = 1021
	idLifecycleGroup    = 1022
	idLifecycleHint     = 1023
	idPreviewGroup      = 1024
	idPreviewHint       = 1025
	idArtifactStatus    = 1026
	idPreviewSummary    = 1027
	idPreviewContent    = 1028
	idViewMarkdown      = 1029
	idViewStageJSON     = 1030
	idViewOutline       = 1031
	idViewBundle        = 1032
	idBrowseRepo        = 1033
	idOpenOutput        = 1034
	idOpenLog           = 1035
	idImportRequirement = 1036
	idMenuExit          = 1037
	idHealthRuntime     = 1038
	idHealthStages      = 1039
	idHealthBundle      = 1040
	idHealthValidation  = 1041
	idPhaseInput        = 1042
	idPhaseUnderstand   = 1043
	idPhasePackage      = 1044
	idPhaseReview       = 1045
	idInputReadiness    = 1046

	bnClicked = 0
	enChange  = 0x0300
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	ole32    = syscall.NewLazyDLL("ole32.dll")
	comdlg32 = syscall.NewLazyDLL("comdlg32.dll")

	procRegisterClassExW     = user32.NewProc("RegisterClassExW")
	procCreateWindowExW      = user32.NewProc("CreateWindowExW")
	procDefWindowProcW       = user32.NewProc("DefWindowProcW")
	procDispatchMessageW     = user32.NewProc("DispatchMessageW")
	procGetClientRect        = user32.NewProc("GetClientRect")
	procGetMessageW          = user32.NewProc("GetMessageW")
	procGetWindowTextW       = user32.NewProc("GetWindowTextW")
	procGetWindowTextLenW    = user32.NewProc("GetWindowTextLengthW")
	procLoadCursorW          = user32.NewProc("LoadCursorW")
	procMoveWindow           = user32.NewProc("MoveWindow")
	procPostMessageW         = user32.NewProc("PostMessageW")
	procPostQuitMessage      = user32.NewProc("PostQuitMessage")
	procSendMessageW         = user32.NewProc("SendMessageW")
	procSetWindowTextW       = user32.NewProc("SetWindowTextW")
	procCreateMenu           = user32.NewProc("CreateMenu")
	procCreatePopupMenu      = user32.NewProc("CreatePopupMenu")
	procAppendMenuW          = user32.NewProc("AppendMenuW")
	procSetMenu              = user32.NewProc("SetMenu")
	procDrawMenuBar          = user32.NewProc("DrawMenuBar")
	procDestroyMenu          = user32.NewProc("DestroyMenu")
	procEnableMenuItem       = user32.NewProc("EnableMenuItem")
	procShowWindow           = user32.NewProc("ShowWindow")
	procTranslateMessage     = user32.NewProc("TranslateMessage")
	procUpdateWindow         = user32.NewProc("UpdateWindow")
	procMessageBoxW          = user32.NewProc("MessageBoxW")
	procEnableWindow         = user32.NewProc("EnableWindow")
	procGetModuleHandleW     = kernel32.NewProc("GetModuleHandleW")
	procGetStockObject       = gdi32.NewProc("GetStockObject")
	procCreateFontW          = gdi32.NewProc("CreateFontW")
	procCreateSolidBrush     = gdi32.NewProc("CreateSolidBrush")
	procDeleteObject         = gdi32.NewProc("DeleteObject")
	procSetBkColor           = gdi32.NewProc("SetBkColor")
	procSetTextColor         = gdi32.NewProc("SetTextColor")
	procSHBrowseForFolderW   = shell32.NewProc("SHBrowseForFolderW")
	procSHGetPathFromIDListW = shell32.NewProc("SHGetPathFromIDListW")
	procShellExecuteW        = shell32.NewProc("ShellExecuteW")
	procCoTaskMemFree        = ole32.NewProc("CoTaskMemFree")
	procGetOpenFileNameW     = comdlg32.NewProc("GetOpenFileNameW")
)

type nativeApp struct {
	runtimeConfig config.AppRuntimeConfig
	service       *app.Service
	logger        *desktopLogger

	hwnd             syscall.Handle
	font             uintptr
	titleFont        uintptr
	monoFont         uintptr
	bgBrush          uintptr
	panelBrush       uintptr
	fieldBrush       uintptr
	readonlyBrush    uintptr
	darkBrush        uintptr
	mainMenu         syscall.Handle
	headerTitle      syscall.Handle
	headerMeta       syscall.Handle
	engineStatus     syscall.Handle
	workflowState    syscall.Handle
	previewTitle     syscall.Handle
	inputGroup       syscall.Handle
	inputHint        syscall.Handle
	sourceHint       syscall.Handle
	credentialHint   syscall.Handle
	lifecycleGroup   syscall.Handle
	lifecycleHint    syscall.Handle
	phaseInput       syscall.Handle
	phaseUnderstand  syscall.Handle
	phasePackage     syscall.Handle
	phaseReview      syscall.Handle
	previewGroup     syscall.Handle
	previewHint      syscall.Handle
	artifactStatus   syscall.Handle
	previewSummary   syscall.Handle
	healthRuntime    syscall.Handle
	healthStages     syscall.Handle
	healthBundle     syscall.Handle
	healthValidation syscall.Handle
	previewContent   syscall.Handle
	viewMarkdownBtn  syscall.Handle
	viewStageBtn     syscall.Handle
	viewOutlineBtn   syscall.Handle
	viewBundleBtn    syscall.Handle
	productURL       nativeField
	localRepoPath    nativeField
	gitRepoURL       nativeField
	demoUsername     nativeField
	demoPassword     nativeField
	requirement      nativeField
	inputReadiness   syscall.Handle
	generateBtn      syscall.Handle
	saveBtn          syscall.Handle
	browseRepoBtn    syscall.Handle
	importReqBtn     syscall.Handle
	openOutputBtn    syscall.Handle
	openLogBtn       syscall.Handle
	statusList       syscall.Handle
	markdown         nativeField
	stageJSON        nativeField
	outlineJSON      nativeField

	mu            sync.Mutex
	generating    bool
	lastResult    *nativeGenerateResult
	pendingResult *nativeGenerateResult
	pendingError  string
}

type nativeField struct {
	Label syscall.Handle
	Edit  syscall.Handle
}

type nativeGenerateResult struct {
	ProjectID         string `json:"project_id"`
	Markdown          string `json:"markdown"`
	StageJSON         string `json:"stage_json"`
	OutlineJSON       string `json:"outline_json"`
	BundleJSON        string `json:"bundle_json"`
	OutputDirectory   string `json:"output_directory"`
	Summary           string `json:"summary"`
	Runtime           string `json:"runtime"`
	StageCount        int    `json:"stage_count"`
	OutlineStageCount int    `json:"outline_stage_count"`
	BundleHashSuffix  string `json:"bundle_hash_suffix"`
	HealthRuntime     string `json:"health_runtime"`
	HealthStages      string `json:"health_stages"`
	HealthBundle      string `json:"health_bundle"`
	HealthValidation  string `json:"health_validation"`
}

type nativeInput struct {
	ProductURL         string
	LocalRepoPath      string
	GitRepoURL         string
	DemoUsername       string
	DemoPassword       string
	ProductDescription string
}

var currentNativeApp *nativeApp

func supportsNativeDesktopUI() bool {
	return true
}

func runNativeDesktopUI(runtimeConfig config.AppRuntimeConfig, service *app.Service, logger *desktopLogger) error {
	app := &nativeApp{runtimeConfig: runtimeConfig, service: service, logger: logger}
	currentNativeApp = app
	return app.run()
}

func (a *nativeApp) run() error {
	instance, _, _ := procGetModuleHandleW.Call(0)
	className := utf16Ptr(winClassName)
	wndProc := syscall.NewCallback(nativeWndProc)
	wc := wndclassex{
		CbSize:        uint32(unsafe.Sizeof(wndclassex{})),
		Style:         0,
		LpfnWndProc:   wndProc,
		HInstance:     instance,
		HCursor:       loadArrowCursor(),
		HbrBackground: getStockObject(whiteBrush),
		LpszClassName: uintptr(unsafe.Pointer(className)),
	}
	if ret, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); ret == 0 {
		return fmt.Errorf("register native window class: %w", err)
	}
	hwnd, _, err := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(utf16Ptr(winTitle))),
		uintptr(wsOverlappedWindow|wsVisible),
		cwUseDefault,
		cwUseDefault,
		1280,
		820,
		0,
		0,
		instance,
		0,
	)
	if hwnd == 0 {
		return fmt.Errorf("create native desktop window: %w", err)
	}
	a.hwnd = syscall.Handle(hwnd)
	procShowWindow.Call(hwnd, swShow)
	procUpdateWindow.Call(hwnd)

	var msg msg
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
	return nil
}

func nativeWndProc(hwnd uintptr, msgID uint32, wParam uintptr, lParam uintptr) uintptr {
	app := currentNativeApp
	switch msgID {
	case wmCreate:
		if app != nil {
			app.hwnd = syscall.Handle(hwnd)
			app.createControls()
			app.layout()
			app.addStatus("本地原生应用已启动。不会打开浏览器或 WebView。")
			app.addStatus("填写产品 URL、需求和可用代码来源后，点击生成三合一包。")
			app.addStatus("本地项目路径与 GitHub 仓库 URL 都是可选代码来源，可以同时提供。")
		}
		return 0
	case wmSize:
		if app != nil {
			app.layout()
		}
		return 0
	case wmCommand:
		id := int(wParam & 0xffff)
		code := int((wParam >> 16) & 0xffff)
		if app != nil {
			switch {
			case code == enChange && app.isInputField(id):
				app.updateInputReadiness()
			case code == bnClicked || code == 0:
				app.handleCommand(id)
			}
		}
		return 0
	case wmAppGenerationDone:
		if app != nil {
			app.finishGenerate()
		}
		return 0
	case wmAppGenerationFailed:
		if app != nil {
			app.finishGenerateError()
		}
		return 0
	case wmDestroy:
		if app != nil {
			app.disposeUIResources()
		}
		procPostQuitMessage.Call(0)
		return 0
	case wmCtlColorStatic, wmCtlColorEdit, wmCtlColorListBox:
		if app != nil {
			return app.controlColor(msgID, wParam, lParam)
		}
	}
	ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msgID), wParam, lParam)
	return ret
}

func (a *nativeApp) createControls() {
	a.createUIResources()
	a.createMenu()
	a.headerTitle = createChild(a.hwnd, "STATIC", "Cascade DemoOps Native Workbench", wsChild|wsVisible, idHeaderTitle)
	a.headerMeta = createChild(a.hwnd, "STATIC", "需求驱动小步读代码 · 生成三合一审批包", wsChild|wsVisible, idHeaderMeta)
	a.engineStatus = createChild(a.hwnd, "STATIC", a.engineStatusText(), wsChild|wsVisible|wsBorder, idEngineStatus)
	a.workflowState = createChild(a.hwnd, "STATIC", "准备生成", wsChild|wsVisible|wsBorder, idWorkflowStatus)
	a.inputGroup = createGroupBox(a.hwnd, "输入材料", idInputGroup)
	a.inputHint = createChild(a.hwnd, "STATIC", "按演示需求渐进读取相关代码。", wsChild|wsVisible, idInputHint)
	a.sourceHint = createChild(a.hwnd, "STATIC", "本地路径和 GitHub URL 可同时提供。", wsChild|wsVisible, idSourceHint)
	a.credentialHint = createChild(a.hwnd, "STATIC", "账号密码仅本机临时使用。", wsChild|wsVisible, idCredentialHint)
	a.lifecycleGroup = createGroupBox(a.hwnd, "本地生成生命周期", idLifecycleGroup)
	a.lifecycleHint = createChild(a.hwnd, "STATIC", "审计本地理解、代码 drilldown、生成和保存。", wsChild|wsVisible, idLifecycleHint)
	a.phaseInput = createChild(a.hwnd, "STATIC", "1 输入材料", wsChild|wsVisible|wsBorder, idPhaseInput)
	a.phaseUnderstand = createChild(a.hwnd, "STATIC", "2 项目理解", wsChild|wsVisible|wsBorder, idPhaseUnderstand)
	a.phasePackage = createChild(a.hwnd, "STATIC", "3 生成包", wsChild|wsVisible|wsBorder, idPhasePackage)
	a.phaseReview = createChild(a.hwnd, "STATIC", "4 审核保存", wsChild|wsVisible|wsBorder, idPhaseReview)
	a.previewGroup = createGroupBox(a.hwnd, "审批材料", idPreviewGroup)
	a.previewTitle = createChild(a.hwnd, "STATIC", "审核三合一包；服务器 Agent 在边界内自适应执行。", wsChild|wsVisible, idPreviewTitle)
	a.previewHint = createChild(a.hwnd, "STATIC", "Markdown 面向审批，JSON/Outline 面向执行。", wsChild|wsVisible, idPreviewHint)
	a.artifactStatus = createChild(a.hwnd, "STATIC", "输出目录：尚未生成", wsChild|wsVisible, idArtifactStatus)
	a.healthRuntime = createChild(a.hwnd, "STATIC", "Runtime: --", wsChild|wsVisible|wsBorder, idHealthRuntime)
	a.healthStages = createChild(a.hwnd, "STATIC", "Stages: --", wsChild|wsVisible|wsBorder, idHealthStages)
	a.healthBundle = createChild(a.hwnd, "STATIC", "Bundle: --", wsChild|wsVisible|wsBorder, idHealthBundle)
	a.healthValidation = createChild(a.hwnd, "STATIC", "Validation: pending", wsChild|wsVisible|wsBorder, idHealthValidation)
	a.previewSummary = createChild(a.hwnd, "EDIT", "等待生成结果。", wsChild|wsVisible|wsBorder|wsVScroll|esMultiline|esAutoVScroll|esReadOnly, idPreviewSummary)
	a.viewMarkdownBtn = createChild(a.hwnd, "BUTTON", "Markdown", wsChild|wsVisible|bsPushButton, idViewMarkdown)
	a.viewStageBtn = createChild(a.hwnd, "BUTTON", "Stage JSON", wsChild|wsVisible|bsPushButton, idViewStageJSON)
	a.viewOutlineBtn = createChild(a.hwnd, "BUTTON", "Outline", wsChild|wsVisible|bsPushButton, idViewOutline)
	a.viewBundleBtn = createChild(a.hwnd, "BUTTON", "Full Bundle", wsChild|wsVisible|bsPushButton, idViewBundle)
	a.previewContent = createChild(a.hwnd, "EDIT", "", wsChild|wsVisible|wsBorder|wsVScroll|wsHScroll|esMultiline|esAutoVScroll|esAutoHScroll|esReadOnly, idPreviewContent)
	a.productURL = a.labelAndEdit("产品 URL", idProductURL, "https://cascadeai.cn", false, false)
	a.localRepoPath = a.labelAndEdit("本地项目路径（可选）", idLocalRepoPath, "", false, false)
	a.gitRepoURL = a.labelAndEdit("GitHub 仓库 URL（可选）", idGitRepoURL, "", false, false)
	a.demoUsername = a.labelAndEdit("演示账号（可选）", idDemoUsername, "", false, false)
	a.demoPassword = a.labelAndEdit("演示密码（可选）", idDemoPassword, "", true, false)
	a.requirement = a.labelAndEdit("需求文档 / 需求文本", idRequirement, "", false, true)
	a.inputReadiness = createChild(a.hwnd, "STATIC", "", wsChild|wsVisible|wsBorder, idInputReadiness)
	a.generateBtn = createChild(a.hwnd, "BUTTON", "生成三合一执行包", wsChild|wsVisible|bsPushButton, idGenerateButton)
	a.saveBtn = createChild(a.hwnd, "BUTTON", "保存三合一包", wsChild|wsVisible|bsPushButton, idSaveButton)
	a.browseRepoBtn = createChild(a.hwnd, "BUTTON", "选择文件夹", wsChild|wsVisible|bsPushButton, idBrowseRepo)
	a.importReqBtn = createChild(a.hwnd, "BUTTON", "导入文档", wsChild|wsVisible|bsPushButton, idImportRequirement)
	a.openOutputBtn = createChild(a.hwnd, "BUTTON", "打开输出目录", wsChild|wsVisible|bsPushButton, idOpenOutput)
	a.openLogBtn = createChild(a.hwnd, "BUTTON", "打开诊断日志", wsChild|wsVisible|bsPushButton, idOpenLog)
	a.statusList = createChild(a.hwnd, "LISTBOX", "", wsChild|wsVisible|wsBorder|wsVScroll|lbsNotify, idStatusList)
	a.applyDefaultFont()
	a.updateInputReadiness()
	a.setPhaseText("1 输入材料", "2 项目理解", "3 生成包", "4 审核保存")
	a.updateActionState(false, false)
}

func (a *nativeApp) handleCommand(id int) {
	switch id {
	case idGenerateButton:
		a.startGenerate()
	case idSaveButton:
		a.saveLastResult()
	case idBrowseRepo:
		a.chooseLocalRepoPath()
	case idImportRequirement:
		a.importRequirementDocument()
	case idOpenOutput:
		a.openLastOutputDirectory()
	case idOpenLog:
		a.openDiagnosticLog()
	case idViewMarkdown:
		a.showPreview("markdown")
	case idViewStageJSON:
		a.showPreview("stage")
	case idViewOutline:
		a.showPreview("outline")
	case idViewBundle:
		a.showPreview("bundle")
	case idMenuExit:
		procPostMessageW.Call(uintptr(a.hwnd), wmClose, 0, 0)
	}
}

func (a *nativeApp) createMenu() {
	mainMenu := createMenu()
	fileMenu := createPopupMenu()
	viewMenu := createPopupMenu()
	helpMenu := createPopupMenu()
	appendMenuItem(fileMenu, idImportRequirement, "导入需求文档...")
	appendMenuItem(fileMenu, idBrowseRepo, "选择本地项目文件夹...")
	appendMenuSeparator(fileMenu)
	appendMenuItem(fileMenu, idGenerateButton, "生成三合一执行包")
	appendMenuItem(fileMenu, idSaveButton, "保存三合一包")
	appendMenuSeparator(fileMenu)
	appendMenuItem(fileMenu, idOpenOutput, "打开输出目录")
	appendMenuItem(fileMenu, idOpenLog, "打开诊断日志")
	appendMenuSeparator(fileMenu)
	appendMenuItem(fileMenu, idMenuExit, "退出")
	appendMenuItem(viewMenu, idViewMarkdown, "预览 Markdown")
	appendMenuItem(viewMenu, idViewStageJSON, "预览 Stage JSON")
	appendMenuItem(viewMenu, idViewOutline, "预览 Script Outline")
	appendMenuItem(viewMenu, idViewBundle, "预览 Full Bundle")
	appendMenuItem(helpMenu, idOpenLog, "诊断日志")
	appendSubMenu(mainMenu, fileMenu, "文件")
	appendSubMenu(mainMenu, viewMenu, "视图")
	appendSubMenu(mainMenu, helpMenu, "帮助")
	procSetMenu.Call(uintptr(a.hwnd), uintptr(mainMenu))
	procDrawMenuBar.Call(uintptr(a.hwnd))
	a.mainMenu = syscall.Handle(mainMenu)
}

func (a *nativeApp) createUIResources() {
	a.font = createFont("Segoe UI", -15, 400)
	a.titleFont = createFont("Segoe UI Semibold", -21, 600)
	a.monoFont = createFont("Cascadia Mono", -14, 400)
	if a.font == 0 {
		a.font = getStockObject(defaultGUIFont)
	}
	if a.titleFont == 0 {
		a.titleFont = a.font
	}
	if a.monoFont == 0 {
		a.monoFont = a.font
	}
	a.bgBrush = createSolidBrush(colorRef(246, 247, 249))
	a.panelBrush = createSolidBrush(colorRef(255, 255, 255))
	a.fieldBrush = createSolidBrush(colorRef(255, 255, 255))
	a.readonlyBrush = createSolidBrush(colorRef(250, 251, 253))
	a.darkBrush = createSolidBrush(colorRef(22, 27, 34))
}

func (a *nativeApp) labelAndEdit(label string, id int, value string, password bool, multiline bool) nativeField {
	labelHandle := createChild(a.hwnd, "STATIC", label, wsChild|wsVisible, 0)
	style := uint32(wsChild | wsVisible | wsBorder | esAutoHScroll)
	if multiline {
		style = wsChild | wsVisible | wsBorder | wsVScroll | esMultiline | esWantReturn | esAutoVScroll
	}
	if password {
		style |= esPassword
	}
	handle := createChild(a.hwnd, "EDIT", value, style, id)
	return nativeField{Label: labelHandle, Edit: handle}
}

func (a *nativeApp) layout() {
	if a == nil || a.hwnd == 0 {
		return
	}
	rect := clientRect(a.hwnd)
	width := int(rect.Right - rect.Left)
	height := int(rect.Bottom - rect.Top)
	margin := 18
	headerH := 88
	leftW := 440
	gap := 18
	leftTop := margin + headerH
	x := margin + 14
	y := leftTop + 34
	rowH := 30
	leftInnerW := leftW - 28
	inputH := 524
	statusTop := leftTop + inputH + 12
	statusH := maxInt(150, height-statusTop-margin)

	moveControl(a.headerTitle, margin, margin, 460, 24)
	moveControl(a.headerMeta, margin, margin+30, 760, 20)
	moveControl(a.engineStatus, maxInt(margin+760, width-570), margin, 262, 38)
	moveControl(a.workflowState, maxInt(margin+1036, width-292), margin, 274, 38)

	moveControl(a.inputGroup, margin, leftTop, leftW, inputH)
	moveControl(a.inputHint, x, y, leftInnerW, 18)
	y += 26
	a.layoutField(a.productURL, x, y, leftInnerW, rowH)
	y += 54
	a.layoutField(a.localRepoPath, x, y, leftInnerW-118, rowH)
	moveControl(a.browseRepoBtn, x+leftInnerW-108, y+20, 108, rowH)
	y += 54
	a.layoutField(a.gitRepoURL, x, y, leftInnerW, rowH)
	y += 54
	moveControl(a.sourceHint, x, y, leftInnerW, 34)
	y += 42
	a.layoutField(a.demoUsername, x, y, leftInnerW, rowH)
	y += 54
	a.layoutField(a.demoPassword, x, y, leftInnerW, rowH)
	y += 54
	moveControl(a.credentialHint, x, y, leftInnerW, 18)
	y += 28
	a.layoutFieldHeight(a.requirement, x, y, leftInnerW, 104)
	moveControl(a.importReqBtn, x+leftInnerW-108, y, 108, 24)
	y += 128
	moveControl(a.inputReadiness, x, y, leftInnerW, 34)
	y += 48
	moveControl(a.generateBtn, x, y, 186, 34)
	moveControl(a.saveBtn, x+202, y, 190, 34)
	moveControl(a.openOutputBtn, x, y+42, leftInnerW, 32)

	moveControl(a.lifecycleGroup, margin, statusTop, leftW, statusH)
	moveControl(a.lifecycleHint, x, statusTop+26, leftInnerW, 18)
	moveControl(a.openLogBtn, x+leftInnerW-128, statusTop+20, 128, 28)
	phaseTop := statusTop + 52
	phaseGap := 6
	phaseW := maxInt(86, (leftInnerW-phaseGap*3)/4)
	moveControl(a.phaseInput, x, phaseTop, phaseW, 28)
	moveControl(a.phaseUnderstand, x+phaseW+phaseGap, phaseTop, phaseW, 28)
	moveControl(a.phasePackage, x+(phaseW+phaseGap)*2, phaseTop, phaseW, 28)
	moveControl(a.phaseReview, x+(phaseW+phaseGap)*3, phaseTop, phaseW, 28)
	moveControl(a.statusList, x, phaseTop+40, leftInnerW, maxInt(86, statusH-94))

	rightX := margin + leftW + gap
	rightW := maxInt(500, width-rightX-margin)
	rightTop := leftTop
	rightH := maxInt(300, height-rightTop-margin)
	moveControl(a.previewGroup, rightX, rightTop, rightW, rightH)
	moveControl(a.previewTitle, rightX+14, rightTop+28, rightW-28, 18)
	moveControl(a.previewHint, rightX+14, rightTop+50, rightW-28, 18)
	moveControl(a.artifactStatus, rightX+14, rightTop+72, rightW-28, 18)
	healthTop := rightTop + 96
	healthGap := 8
	healthW := maxInt(92, (rightW-28-healthGap*3)/4)
	moveControl(a.healthRuntime, rightX+14, healthTop, healthW, 30)
	moveControl(a.healthStages, rightX+14+(healthW+healthGap), healthTop, healthW, 30)
	moveControl(a.healthBundle, rightX+14+(healthW+healthGap)*2, healthTop, healthW, 30)
	moveControl(a.healthValidation, rightX+14+(healthW+healthGap)*3, healthTop, healthW, 30)
	summaryTop := healthTop + 42
	summaryH := 78
	moveControl(a.previewSummary, rightX+14, summaryTop, rightW-28, summaryH)
	tabTop := summaryTop + summaryH + 12
	buttonW := maxInt(92, (rightW-28-healthGap*3)/4)
	moveControl(a.viewMarkdownBtn, rightX+14, tabTop, buttonW, 30)
	moveControl(a.viewStageBtn, rightX+14+buttonW+healthGap, tabTop, buttonW, 30)
	moveControl(a.viewOutlineBtn, rightX+14+(buttonW+healthGap)*2, tabTop, buttonW, 30)
	moveControl(a.viewBundleBtn, rightX+14+(buttonW+healthGap)*3, tabTop, buttonW, 30)
	moveControl(a.previewContent, rightX+14, tabTop+40, rightW-28, maxInt(160, rightH-(tabTop-rightTop)-54))
}

func (a *nativeApp) layoutField(field nativeField, x int, y int, w int, h int) {
	a.layoutFieldHeight(field, x, y, w, h)
}

func (a *nativeApp) layoutFieldHeight(field nativeField, x int, y int, w int, h int) {
	moveControl(field.Label, x, y, w, 18)
	moveControl(field.Edit, x, y+20, w, h)
}

func (a *nativeApp) startGenerate() {
	a.mu.Lock()
	if a.generating {
		a.mu.Unlock()
		return
	}
	a.generating = true
	a.mu.Unlock()
	setWindowText(a.generateBtn, "生成中...")
	setWindowText(a.workflowState, "正在生成三合一包")
	setWindowText(a.artifactStatus, "输出目录：生成完成后显示")
	a.setHealthText("Runtime: generating", "Stages: --", "Bundle: --", "Validation: pending")
	a.setPhaseText("1 输入完成", "2 理解中", "3 生成中", "4 待审核")
	a.updateActionState(true, false)
	a.addStatus("开始本地项目理解与三合一包生成。")
	input := nativeInput{
		ProductURL:         getWindowText(a.productURL.Edit),
		LocalRepoPath:      getWindowText(a.localRepoPath.Edit),
		GitRepoURL:         getWindowText(a.gitRepoURL.Edit),
		DemoUsername:       getWindowText(a.demoUsername.Edit),
		DemoPassword:       getWindowText(a.demoPassword.Edit),
		ProductDescription: getWindowText(a.requirement.Edit),
	}
	go a.generate(input)
}

func (a *nativeApp) generate(input nativeInput) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if strings.TrimSpace(input.ProductURL) == "" || strings.TrimSpace(input.ProductDescription) == "" {
		a.postError("产品 URL 和需求文本是必填项。")
		return
	}
	state, err := a.service.GenerateExecutionPackage(ctx, orchestrator.UserInput{
		Mode:               model.AppModeDesktop,
		ProductURL:         strings.TrimSpace(input.ProductURL),
		LocalRepoPath:      strings.TrimSpace(input.LocalRepoPath),
		GitRepoURL:         strings.TrimSpace(input.GitRepoURL),
		ProductDescription: strings.TrimSpace(input.ProductDescription),
		TargetAudience:     "产品与运营团队",
		DemoUsername:       strings.TrimSpace(input.DemoUsername),
		DemoPassword:       input.DemoPassword,
		ForbiddenPages:     []string{"/billing", "/settings/api-keys", "/aigc", "/.well-known", "/v1"},
		ForbiddenData:      []string{"password", "token", "cookie", "authorization", ".env"},
	})
	if err != nil {
		a.postError(err.Error())
		return
	}
	if state == nil || state.ExecutableScriptBundle == nil {
		a.postError("生成完成但没有得到三合一执行包。")
		return
	}
	result, err := a.nativeResultFromState(state)
	if err != nil {
		a.postError(err.Error())
		return
	}
	a.mu.Lock()
	a.pendingResult = result
	a.mu.Unlock()
	procPostMessageW.Call(uintptr(a.hwnd), wmAppGenerationDone, 0, 0)
}

func (a *nativeApp) nativeResultFromState(state *orchestrator.CascadeState) (*nativeGenerateResult, error) {
	bundle := state.ExecutableScriptBundle
	stageJSON, err := marshalPretty(bundle.StageApprovalPlan)
	if err != nil {
		return nil, err
	}
	outlineJSON, err := marshalPretty(bundle.ScriptOutline)
	if err != nil {
		return nil, err
	}
	bundleJSON, err := marshalPretty(bundle)
	if err != nil {
		return nil, err
	}
	markdown := strings.TrimSpace(state.ScriptMarkdown)
	if markdown == "" {
		markdown = bundle.ApprovalMarkdown.InlineMarkdown
	}
	return &nativeGenerateResult{
		ProjectID:         state.ProjectID,
		Markdown:          markdown,
		StageJSON:         stageJSON,
		OutlineJSON:       outlineJSON,
		BundleJSON:        bundleJSON,
		OutputDirectory:   filepath.Join(a.runtimeConfig.ArtifactRoot, state.ProjectID),
		Summary:           nativeResultSummary(state, bundle, markdown, stageJSON, outlineJSON, bundleJSON),
		Runtime:           bundle.ScriptManifest.Runtime,
		StageCount:        stageApprovalStageCount(bundle),
		OutlineStageCount: outlineStageCount(bundle),
		BundleHashSuffix:  shortHash(bundle.Reproducibility.BundleHashSHA256),
		HealthRuntime:     "Runtime: " + firstNonEmptyNative(bundle.ScriptManifest.Runtime, "unknown"),
		HealthStages:      fmt.Sprintf("Stages: %d / %d", stageApprovalStageCount(bundle), outlineStageCount(bundle)),
		HealthBundle:      "Bundle: " + byteSizeLabel(len(bundleJSON)),
		HealthValidation:  validationHealthLabel(bundle.Validation),
	}, nil
}

func (a *nativeApp) finishGenerate() {
	a.mu.Lock()
	result := a.pendingResult
	a.pendingResult = nil
	a.generating = false
	a.lastResult = result
	a.mu.Unlock()
	if result == nil {
		a.finishGenerateErrorMessage("生成完成但没有得到可展示结果。")
		return
	}
	setWindowText(a.generateBtn, "生成三合一执行包")
	setWindowText(a.workflowState, "审批包已就绪")
	setWindowText(a.artifactStatus, "输出目录："+result.OutputDirectory)
	setWindowText(a.previewSummary, result.Summary)
	setWindowText(a.previewContent, result.Markdown)
	a.setHealthText(result.HealthRuntime, result.HealthStages, result.HealthBundle, result.HealthValidation)
	a.setPhaseText("1 输入完成", "2 理解完成", "3 生成完成", "4 可审核")
	a.updateActionState(false, true)
	a.addStatus("三合一执行包已生成，可审核或保存。project_id=" + result.ProjectID)
}

func (a *nativeApp) finishGenerateError() {
	a.mu.Lock()
	message := a.pendingError
	a.pendingError = ""
	a.mu.Unlock()
	a.finishGenerateErrorMessage(message)
}

func (a *nativeApp) finishGenerateErrorMessage(message string) {
	a.mu.Lock()
	a.generating = false
	a.mu.Unlock()
	setWindowText(a.generateBtn, "生成三合一执行包")
	setWindowText(a.workflowState, "生成失败")
	a.setHealthText("Runtime: --", "Stages: --", "Bundle: --", "Validation: failed")
	a.setPhaseText("1 输入完成", "2/3 失败", "3 未就绪", "4 不可审核")
	a.updateActionState(false, a.lastResult != nil)
	a.addStatus("生成失败：" + message)
	messageBox("Cascade DemoOps", message, true)
}

func (a *nativeApp) saveLastResult() {
	a.mu.Lock()
	result := a.lastResult
	a.mu.Unlock()
	if result == nil {
		a.addStatus("还没有可保存的执行包。")
		return
	}
	dir := result.OutputDirectory
	if strings.TrimSpace(dir) == "" {
		dir = filepath.Join(a.runtimeConfig.ArtifactRoot, result.ProjectID)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		a.addStatus("保存失败：" + err.Error())
		return
	}
	files := map[string]string{
		"approval_markdown.md":         result.Markdown,
		"stage_approval_plan.json":     result.StageJSON,
		"script_outline.json":          result.OutlineJSON,
		"client_execution_bundle.json": result.BundleJSON,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content+"\n"), 0o644); err != nil {
			a.addStatus("保存失败：" + err.Error())
			return
		}
	}
	a.addStatus("已保存到 " + dir)
	setWindowText(a.workflowState, "已保存三合一包")
	setWindowText(a.artifactStatus, "已保存："+dir)
	a.setPhaseText("1 输入完成", "2 理解完成", "3 生成完成", "4 已保存")
	setEnabled(a.openOutputBtn, true)
	messageBox("Cascade DemoOps", "三合一执行包已保存到：\n"+dir, false)
}

func (a *nativeApp) chooseLocalRepoPath() {
	path, err := browseForFolder(a.hwnd, "选择本地项目文件夹")
	if err != nil {
		a.addStatus("选择文件夹失败：" + err.Error())
		return
	}
	if strings.TrimSpace(path) == "" {
		return
	}
	setWindowText(a.localRepoPath.Edit, path)
	a.addStatus("已选择本地项目路径：" + path)
}

func (a *nativeApp) importRequirementDocument() {
	path, err := openTextFileDialog(a.hwnd, "导入需求文档")
	if err != nil {
		a.addStatus("导入需求文档失败：" + err.Error())
		messageBox("Cascade DemoOps", "导入需求文档失败：\n"+err.Error(), true)
		return
	}
	if strings.TrimSpace(path) == "" {
		return
	}
	content, err := readRequirementDocument(path)
	if err != nil {
		a.addStatus("读取需求文档失败：" + err.Error())
		messageBox("Cascade DemoOps", "读取需求文档失败：\n"+err.Error(), true)
		return
	}
	setWindowText(a.requirement.Edit, content)
	a.updateInputReadiness()
	a.addStatus(fmt.Sprintf("已导入需求文档：%s（%s）", path, byteSizeLabel(len(content))))
}

func (a *nativeApp) openLastOutputDirectory() {
	a.mu.Lock()
	result := a.lastResult
	a.mu.Unlock()
	if result == nil || strings.TrimSpace(result.OutputDirectory) == "" {
		a.addStatus("还没有可打开的输出目录。")
		return
	}
	if err := openFolder(result.OutputDirectory); err != nil {
		a.addStatus("打开输出目录失败：" + err.Error())
		messageBox("Cascade DemoOps", "打开输出目录失败：\n"+err.Error(), true)
		return
	}
	a.addStatus("已打开输出目录：" + result.OutputDirectory)
}

func (a *nativeApp) openDiagnosticLog() {
	if a.logger == nil || strings.TrimSpace(a.logger.Path()) == "" {
		a.addStatus("诊断日志路径不可用。")
		return
	}
	if err := openFile(a.logger.Path()); err != nil {
		a.addStatus("打开诊断日志失败：" + err.Error())
		messageBox("Cascade DemoOps", "打开诊断日志失败：\n"+err.Error(), true)
		return
	}
	a.addStatus("已打开诊断日志：" + a.logger.Path())
}

func (a *nativeApp) showPreview(kind string) {
	a.mu.Lock()
	result := a.lastResult
	a.mu.Unlock()
	if result == nil {
		return
	}
	switch kind {
	case "stage":
		setWindowText(a.previewContent, result.StageJSON)
		setWindowText(a.workflowState, "预览 Stage JSON")
	case "outline":
		setWindowText(a.previewContent, result.OutlineJSON)
		setWindowText(a.workflowState, "预览 Script Outline")
	case "bundle":
		setWindowText(a.previewContent, result.BundleJSON)
		setWindowText(a.workflowState, "预览 Full Bundle")
	default:
		setWindowText(a.previewContent, result.Markdown)
		setWindowText(a.workflowState, "预览 Markdown")
	}
}

func (a *nativeApp) setPreviewButtonsEnabled(enabled bool) {
	setEnabled(a.viewMarkdownBtn, enabled)
	setEnabled(a.viewStageBtn, enabled)
	setEnabled(a.viewOutlineBtn, enabled)
	setEnabled(a.viewBundleBtn, enabled)
}

func (a *nativeApp) isInputField(id int) bool {
	switch id {
	case idProductURL, idLocalRepoPath, idGitRepoURL, idDemoUsername, idDemoPassword, idRequirement:
		return true
	default:
		return false
	}
}

func (a *nativeApp) updateInputReadiness() {
	if a.inputReadiness == 0 {
		return
	}
	productURL := strings.TrimSpace(getWindowText(a.productURL.Edit))
	localRepo := strings.TrimSpace(getWindowText(a.localRepoPath.Edit))
	gitRepo := strings.TrimSpace(getWindowText(a.gitRepoURL.Edit))
	username := strings.TrimSpace(getWindowText(a.demoUsername.Edit))
	password := getWindowText(a.demoPassword.Edit)
	requirement := strings.TrimSpace(getWindowText(a.requirement.Edit))
	urlState := "URL: missing"
	if productURL != "" {
		urlState = "URL: ready"
	}
	requirementState := "Requirement: missing"
	if requirement != "" {
		requirementState = "Requirement: " + byteSizeLabel(len(requirement))
	}
	sourceState := "Code: optional"
	switch {
	case localRepo != "" && gitRepo != "":
		sourceState = "Code: local + GitHub"
	case localRepo != "":
		sourceState = "Code: local"
	case gitRepo != "":
		sourceState = "Code: GitHub"
	}
	credentialState := "Credentials: optional"
	switch {
	case username != "" && password != "":
		credentialState = "Credentials: provided locally"
	case username != "" || password != "":
		credentialState = "Credentials: incomplete"
	}
	setWindowText(a.inputReadiness, strings.Join([]string{urlState, requirementState, sourceState, credentialState}, "  |  "))
}

func (a *nativeApp) updateActionState(generating bool, hasResult bool) {
	setEnabled(a.generateBtn, !generating)
	setEnabled(a.saveBtn, !generating && hasResult)
	setEnabled(a.browseRepoBtn, !generating)
	setEnabled(a.importReqBtn, !generating)
	setEnabled(a.openOutputBtn, !generating && hasResult)
	setEnabled(a.openLogBtn, true)
	a.setPreviewButtonsEnabled(!generating && hasResult)
	for _, id := range []int{idGenerateButton, idBrowseRepo, idImportRequirement} {
		a.enableMenuItem(id, !generating)
	}
	for _, id := range []int{idSaveButton, idOpenOutput, idViewMarkdown, idViewStageJSON, idViewOutline, idViewBundle} {
		a.enableMenuItem(id, !generating && hasResult)
	}
	a.enableMenuItem(idOpenLog, true)
}

func (a *nativeApp) setPhaseText(input string, understand string, pack string, review string) {
	setWindowText(a.phaseInput, input)
	setWindowText(a.phaseUnderstand, understand)
	setWindowText(a.phasePackage, pack)
	setWindowText(a.phaseReview, review)
}

func (a *nativeApp) setHealthText(runtime string, stages string, bundle string, validation string) {
	setWindowText(a.healthRuntime, runtime)
	setWindowText(a.healthStages, stages)
	setWindowText(a.healthBundle, bundle)
	setWindowText(a.healthValidation, validation)
}

func (a *nativeApp) enableMenuItem(id int, enabled bool) {
	if a.mainMenu == 0 {
		return
	}
	flag := mfByCommand | mfEnabled
	if !enabled {
		flag = mfByCommand | mfGrayed
	}
	procEnableMenuItem.Call(uintptr(a.mainMenu), uintptr(id), uintptr(flag))
	procDrawMenuBar.Call(uintptr(a.hwnd))
}

func (a *nativeApp) engineStatusText() string {
	sidecar := "sidecar 待检查"
	if a.runtimeConfig.NodeBinaryPath != "" {
		sidecar = "Node/sidecar 已配置"
	}
	return fmt.Sprintf("引擎：%s · %s", a.runtimeConfig.Profile, sidecar)
}

func nativeResultSummary(state *orchestrator.CascadeState, bundle *model.ExecutableRecordingScriptBundle, markdown string, stageJSON string, outlineJSON string, bundleJSON string) string {
	if state == nil || bundle == nil {
		return "生成结果不可用。"
	}
	lines := []string{
		"Project ID: " + state.ProjectID,
		"Runtime: " + firstNonEmptyNative(bundle.ScriptManifest.Runtime, "unknown"),
		fmt.Sprintf("Stages: approval=%d outline=%d", stageApprovalStageCount(bundle), outlineStageCount(bundle)),
		fmt.Sprintf("Payload size: markdown=%s stage=%s outline=%s bundle=%s", byteSizeLabel(len(markdown)), byteSizeLabel(len(stageJSON)), byteSizeLabel(len(outlineJSON)), byteSizeLabel(len(bundleJSON))),
	}
	if suffix := shortHash(bundle.Reproducibility.BundleHashSHA256); suffix != "" {
		lines = append(lines, "Bundle hash: ..."+suffix)
	}
	if bundle.Validation != nil {
		lines = append(lines, fmt.Sprintf("Validation: valid=%t findings=%d", bundle.Validation.Valid, len(bundle.Validation.Findings)))
	}
	return strings.Join(lines, "\r\n")
}

func validationHealthLabel(validation *model.ExecutableScriptValidation) string {
	if validation == nil {
		return "Validation: missing"
	}
	if validation.Valid {
		if len(validation.Findings) == 0 {
			return "Validation: passed"
		}
		return fmt.Sprintf("Validation: passed, %d findings", len(validation.Findings))
	}
	return fmt.Sprintf("Validation: blocked, %d findings", len(validation.Findings))
}

func stageApprovalStageCount(bundle *model.ExecutableRecordingScriptBundle) int {
	if bundle == nil || bundle.StageApprovalPlan == nil {
		return 0
	}
	return len(bundle.StageApprovalPlan.Stages)
}

func outlineStageCount(bundle *model.ExecutableRecordingScriptBundle) int {
	if bundle == nil || bundle.ScriptOutline == nil {
		return 0
	}
	return len(bundle.ScriptOutline.Stages)
}

func byteSizeLabel(size int) string {
	if size < 1024 {
		return fmt.Sprintf("%dB", size)
	}
	if size < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(size)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(size)/(1024*1024))
}

func shortHash(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 12 {
		return value
	}
	return value[len(value)-12:]
}

func firstNonEmptyNative(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (a *nativeApp) addStatus(message string) {
	if a.statusList == 0 {
		return
	}
	line := time.Now().Format("15:04:05") + "  " + message
	index, _, _ := procSendMessageW.Call(uintptr(a.statusList), lbAddString, 0, uintptr(unsafe.Pointer(utf16Ptr(line))))
	if index != lbErr {
		procSendMessageW.Call(uintptr(a.statusList), lbSetCurSel, index, 0)
	}
	if a.logger != nil {
		a.logger.Printf("native: %s", message)
	}
}

func (a *nativeApp) postError(message string) {
	a.mu.Lock()
	a.pendingError = message
	a.mu.Unlock()
	procPostMessageW.Call(uintptr(a.hwnd), wmAppGenerationFailed, 0, 0)
}

func marshalPretty(value any) (string, error) {
	if value == nil {
		return "null", nil
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func createChild(parent syscall.Handle, class string, text string, style uint32, id int) syscall.Handle {
	hwnd, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(utf16Ptr(class))),
		uintptr(unsafe.Pointer(utf16Ptr(text))),
		uintptr(style),
		0,
		0,
		10,
		10,
		uintptr(parent),
		uintptr(id),
		0,
		0,
	)
	return syscall.Handle(hwnd)
}

func createGroupBox(parent syscall.Handle, text string, id int) syscall.Handle {
	return createChild(parent, "BUTTON", text, wsChild|wsVisible|bsGroupBox, id)
}

func createMenu() syscall.Handle {
	menu, _, _ := procCreateMenu.Call()
	return syscall.Handle(menu)
}

func createPopupMenu() syscall.Handle {
	menu, _, _ := procCreatePopupMenu.Call()
	return syscall.Handle(menu)
}

func appendMenuItem(menu syscall.Handle, id int, text string) {
	procAppendMenuW.Call(uintptr(menu), mfString, uintptr(id), uintptr(unsafe.Pointer(utf16Ptr(text))))
}

func appendMenuSeparator(menu syscall.Handle) {
	procAppendMenuW.Call(uintptr(menu), mfSeparator, 0, 0)
}

func appendSubMenu(menu syscall.Handle, subMenu syscall.Handle, text string) {
	procAppendMenuW.Call(uintptr(menu), mfPopup, uintptr(subMenu), uintptr(unsafe.Pointer(utf16Ptr(text))))
}

func (a *nativeApp) applyDefaultFont() {
	if a.font == 0 {
		return
	}
	handles := []syscall.Handle{
		a.headerTitle,
		a.headerMeta,
		a.engineStatus,
		a.workflowState,
		a.inputGroup,
		a.inputHint,
		a.sourceHint,
		a.credentialHint,
		a.lifecycleGroup,
		a.lifecycleHint,
		a.phaseInput,
		a.phaseUnderstand,
		a.phasePackage,
		a.phaseReview,
		a.previewGroup,
		a.previewTitle,
		a.previewHint,
		a.artifactStatus,
		a.healthRuntime,
		a.healthStages,
		a.healthBundle,
		a.healthValidation,
		a.previewSummary,
		a.previewContent,
		a.viewMarkdownBtn,
		a.viewStageBtn,
		a.viewOutlineBtn,
		a.viewBundleBtn,
		a.generateBtn,
		a.saveBtn,
		a.browseRepoBtn,
		a.importReqBtn,
		a.openOutputBtn,
		a.openLogBtn,
		a.statusList,
	}
	for _, field := range []nativeField{
		a.productURL,
		a.localRepoPath,
		a.gitRepoURL,
		a.demoUsername,
		a.demoPassword,
		a.requirement,
	} {
		handles = append(handles, field.Label, field.Edit)
	}
	handles = append(handles, a.inputReadiness)
	for _, handle := range handles {
		if handle != 0 {
			procSendMessageW.Call(uintptr(handle), wmSetFont, a.font, 1)
		}
	}
	if a.titleFont != 0 {
		procSendMessageW.Call(uintptr(a.headerTitle), wmSetFont, a.titleFont, 1)
	}
	if a.monoFont != 0 {
		procSendMessageW.Call(uintptr(a.previewSummary), wmSetFont, a.monoFont, 1)
		procSendMessageW.Call(uintptr(a.previewContent), wmSetFont, a.monoFont, 1)
		procSendMessageW.Call(uintptr(a.statusList), wmSetFont, a.monoFont, 1)
	}
}

func (a *nativeApp) controlColor(msgID uint32, wParam uintptr, lParam uintptr) uintptr {
	hwnd := syscall.Handle(lParam)
	hdc := wParam
	textColor := colorRef(33, 37, 41)
	bgColor := colorRef(255, 255, 255)
	brush := a.panelBrush
	switch {
	case hwnd == a.previewSummary || hwnd == a.previewContent ||
		hwnd == a.healthRuntime || hwnd == a.healthStages ||
		hwnd == a.healthBundle || hwnd == a.healthValidation ||
		hwnd == a.phaseInput || hwnd == a.phaseUnderstand ||
		hwnd == a.phasePackage || hwnd == a.phaseReview ||
		hwnd == a.inputReadiness:
		bgColor = colorRef(250, 251, 253)
		brush = a.readonlyBrush
	case hwnd == a.statusList:
		bgColor = colorRef(22, 27, 34)
		textColor = colorRef(222, 226, 230)
		brush = a.darkBrush
	case msgID == wmCtlColorEdit:
		brush = a.fieldBrush
	case hwnd == a.inputGroup || hwnd == a.lifecycleGroup || hwnd == a.previewGroup:
		brush = a.bgBrush
		bgColor = colorRef(246, 247, 249)
	default:
		bgColor = colorRef(246, 247, 249)
		brush = a.bgBrush
	}
	if brush == 0 {
		brush = getStockObject(whiteBrush)
	}
	procSetTextColor.Call(hdc, uintptr(textColor))
	procSetBkColor.Call(hdc, uintptr(bgColor))
	return brush
}

func (a *nativeApp) disposeUIResources() {
	stockFont := getStockObject(defaultGUIFont)
	seen := map[uintptr]bool{}
	for _, object := range []uintptr{a.font, a.titleFont, a.monoFont, a.bgBrush, a.panelBrush, a.fieldBrush, a.readonlyBrush, a.darkBrush} {
		if object == 0 || object == stockFont || seen[object] {
			continue
		}
		seen[object] = true
		procDeleteObject.Call(object)
	}
	if a.mainMenu != 0 {
		procDestroyMenu.Call(uintptr(a.mainMenu))
		a.mainMenu = 0
	}
}

func moveControl(handle syscall.Handle, x int, y int, w int, h int) {
	if handle == 0 {
		return
	}
	procMoveWindow.Call(uintptr(handle), uintptr(x), uintptr(y), uintptr(w), uintptr(h), 1)
}

func setWindowText(handle syscall.Handle, text string) {
	procSetWindowTextW.Call(uintptr(handle), uintptr(unsafe.Pointer(utf16Ptr(text))))
}

func setEnabled(handle syscall.Handle, enabled bool) {
	value := uintptr(0)
	if enabled {
		value = 1
	}
	procEnableWindow.Call(uintptr(handle), value)
}

func getWindowText(handle syscall.Handle) string {
	length, _, _ := procGetWindowTextLenW.Call(uintptr(handle))
	if length == 0 {
		return ""
	}
	buffer := make([]uint16, int(length)+1)
	procGetWindowTextW.Call(uintptr(handle), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	return syscall.UTF16ToString(buffer)
}

func clientRect(hwnd syscall.Handle) rect {
	var out rect
	procGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&out)))
	return out
}

func loadArrowCursor() uintptr {
	ret, _, _ := procLoadCursorW.Call(0, uintptr(32512))
	return ret
}

func getStockObject(id int) uintptr {
	ret, _, _ := procGetStockObject.Call(uintptr(id))
	return ret
}

func messageBox(title string, message string, isError bool) {
	flags := uintptr(mbOK | mbIconInformation)
	if isError {
		flags = mbOK | mbIconError
	}
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(utf16Ptr(message))), uintptr(unsafe.Pointer(utf16Ptr(title))), flags)
}

func browseForFolder(owner syscall.Handle, title string) (string, error) {
	var displayName [maxPath]uint16
	info := browseInfo{
		HwndOwner:      uintptr(owner),
		PidlRoot:       0,
		PszDisplayName: uintptr(unsafe.Pointer(&displayName[0])),
		LpszTitle:      uintptr(unsafe.Pointer(utf16Ptr(title))),
		UlFlags:        bifReturnOnlyFSDirs | bifNewDialogStyle | bifEditBox,
	}
	pidl, _, err := procSHBrowseForFolderW.Call(uintptr(unsafe.Pointer(&info)))
	if pidl == 0 {
		return "", nil
	}
	defer procCoTaskMemFree.Call(pidl)
	var path [maxPath]uint16
	ok, _, _ := procSHGetPathFromIDListW.Call(pidl, uintptr(unsafe.Pointer(&path[0])))
	if ok == 0 {
		if err != syscall.Errno(0) {
			return "", err
		}
		return "", errors.New("folder path is unavailable")
	}
	return syscall.UTF16ToString(path[:]), nil
}

func openTextFileDialog(owner syscall.Handle, title string) (string, error) {
	var fileBuffer [maxLongPath]uint16
	filter := utf16DoubleNull("需求文档 (*.md;*.txt)\x00*.md;*.txt\x00Markdown (*.md)\x00*.md\x00Text (*.txt)\x00*.txt\x00所有文件 (*.*)\x00*.*")
	ofn := openFileName{
		LStructSize: uint32(unsafe.Sizeof(openFileName{})),
		HwndOwner:   uintptr(owner),
		LpstrFilter: uintptr(unsafe.Pointer(&filter[0])),
		LpstrFile:   uintptr(unsafe.Pointer(&fileBuffer[0])),
		NMaxFile:    maxLongPath,
		LpstrTitle:  uintptr(unsafe.Pointer(utf16Ptr(title))),
		Flags:       ofnExplorer | ofnFileMustExist | ofnPathMustExist | ofnHideReadOnly,
		LpstrDefExt: uintptr(unsafe.Pointer(utf16Ptr("md"))),
	}
	ok, _, err := procGetOpenFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
	if ok == 0 {
		if err != syscall.Errno(0) {
			return "", err
		}
		return "", nil
	}
	return syscall.UTF16ToString(fileBuffer[:]), nil
}

func readRequirementDocument(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	limited := io.LimitReader(file, maxRequirementImportBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return "", err
	}
	if len(data) > maxRequirementImportBytes {
		return "", fmt.Errorf("document is larger than %s", byteSizeLabel(maxRequirementImportBytes))
	}
	return strings.TrimSpace(string(data)), nil
}

func openFolder(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("output directory is empty")
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return err
	}
	ret, _, err := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(utf16Ptr("open"))),
		uintptr(unsafe.Pointer(utf16Ptr(path))),
		0,
		0,
		swShow,
	)
	if ret <= 32 {
		if err != syscall.Errno(0) {
			return err
		}
		return fmt.Errorf("ShellExecuteW failed with code %d", ret)
	}
	return nil
}

func openFile(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("file path is empty")
	}
	ret, _, err := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(utf16Ptr("open"))),
		uintptr(unsafe.Pointer(utf16Ptr(path))),
		0,
		0,
		swShow,
	)
	if ret <= 32 {
		if err != syscall.Errno(0) {
			return err
		}
		return fmt.Errorf("ShellExecuteW failed with code %d", ret)
	}
	return nil
}

func utf16Ptr(value string) *uint16 {
	ptr, err := syscall.UTF16PtrFromString(value)
	if err != nil {
		panic(err)
	}
	return ptr
}

func utf16DoubleNull(value string) []uint16 {
	encoded := syscall.StringToUTF16(value)
	if len(encoded) == 0 || encoded[len(encoded)-1] != 0 {
		encoded = append(encoded, 0)
	}
	if len(encoded) == 1 || encoded[len(encoded)-2] != 0 {
		encoded = append(encoded, 0)
	}
	return encoded
}

func maxInt(left int, right int) int {
	if left > right {
		return left
	}
	return right
}

func createFont(face string, height int32, weight int32) uintptr {
	ret, _, _ := procCreateFontW.Call(
		uintptr(height),
		0,
		0,
		0,
		uintptr(weight),
		0,
		0,
		0,
		defaultCharset,
		outDefaultPrecision,
		clipDefaultPrecision,
		cleartypeQuality,
		defaultPitch|ffDontCare,
		uintptr(unsafe.Pointer(utf16Ptr(face))),
	)
	return ret
}

func createSolidBrush(color uint32) uintptr {
	ret, _, _ := procCreateSolidBrush.Call(uintptr(color))
	return ret
}

func colorRef(red byte, green byte, blue byte) uint32 {
	return uint32(red) | uint32(green)<<8 | uint32(blue)<<16
}

type wndclassex struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  uintptr
	LpszClassName uintptr
	HIconSm       uintptr
}

type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

type point struct {
	X int32
	Y int32
}

type rect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

type browseInfo struct {
	HwndOwner      uintptr
	PidlRoot       uintptr
	PszDisplayName uintptr
	LpszTitle      uintptr
	UlFlags        uint32
	Lpfn           uintptr
	LParam         uintptr
	IImage         int32
}

type openFileName struct {
	LStructSize       uint32
	HwndOwner         uintptr
	HInstance         uintptr
	LpstrFilter       uintptr
	LpstrCustomFilter uintptr
	NMaxCustFilter    uint32
	NFilterIndex      uint32
	LpstrFile         uintptr
	NMaxFile          uint32
	LpstrFileTitle    uintptr
	NMaxFileTitle     uint32
	LpstrInitialDir   uintptr
	LpstrTitle        uintptr
	Flags             uint32
	NFileOffset       uint16
	NFileExtension    uint16
	LpstrDefExt       uintptr
	LCustData         uintptr
	LpfnHook          uintptr
	LpTemplateName    uintptr
	PvReserved        uintptr
	DwReserved        uint32
	FlagsEx           uint32
}

const (
	cwUseDefault              = ^uintptr(0x7fffffff)
	maxPath                   = 260
	maxLongPath               = 32768
	maxRequirementImportBytes = 256 * 1024

	wsOverlappedWindow = 0x00cf0000
	wsVisible          = 0x10000000
	wsChild            = 0x40000000
	wsBorder           = 0x00800000
	wsVScroll          = 0x00200000
	wsHScroll          = 0x00100000

	esAutoHScroll = 0x0080
	esAutoVScroll = 0x0040
	esMultiline   = 0x0004
	esPassword    = 0x0020
	esReadOnly    = 0x0800
	esWantReturn  = 0x1000

	bsPushButton = 0x00000000
	bsGroupBox   = 0x00000007
	lbsNotify    = 0x0001

	swShow = 5

	wmCreate              = 0x0001
	wmDestroy             = 0x0002
	wmClose               = 0x0010
	wmSize                = 0x0005
	wmSetFont             = 0x0030
	wmCommand             = 0x0111
	wmCtlColorEdit        = 0x0133
	wmCtlColorListBox     = 0x0134
	wmCtlColorStatic      = 0x0138
	wmAppGenerationDone   = 0x8001
	wmAppGenerationFailed = 0x8002

	lbAddString = 0x0180
	lbSetCurSel = 0x0186
	lbErr       = ^uintptr(0)

	mfString    = 0x00000000
	mfSeparator = 0x00000800
	mfPopup     = 0x00000010
	mfByCommand = 0x00000000
	mfGrayed    = 0x00000001
	mfEnabled   = 0x00000000

	whiteBrush     = 0
	defaultGUIFont = 17

	mbOK              = 0x00000000
	mbIconError       = 0x00000010
	mbIconInformation = 0x00000040

	bifReturnOnlyFSDirs = 0x00000001
	bifEditBox          = 0x00000010
	bifNewDialogStyle   = 0x00000040

	ofnReadOnly        = 0x00000001
	ofnOverwritePrompt = 0x00000002
	ofnHideReadOnly    = 0x00000004
	ofnFileMustExist   = 0x00001000
	ofnPathMustExist   = 0x00000800
	ofnExplorer        = 0x00080000

	defaultCharset       = 1
	outDefaultPrecision  = 0
	clipDefaultPrecision = 0
	cleartypeQuality     = 5
	defaultPitch         = 0
	ffDontCare           = 0
)

var errNativeUnavailable = errors.New("native desktop ui is unavailable")
