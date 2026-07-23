//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
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
	winClassName       = "CascadeDemoOpsNativeWindow"
	winTitle           = "Cascade DemoOps Desktop"
	nativeDefaultOrgID = "org_desktop"

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
	idCopyPreview       = 1047
	idExportPackage     = 1048
	idClearDraft        = 1049
	idStatusBar         = 1050
	idRecentPackage     = 1051
	idOpenRecentPackage = 1052
	idRecentLabel       = 1053
	idViewReview        = 1054
	idApprovePackage    = 1055
	idImportPackage     = 1056
	idOpenPreviewFile   = 1057
	idUploadApproved    = 1058
	idQueryServerStatus = 1059
	idFetchServerResult = 1060
	idAckServerResult   = 1061
	idDownloadArtifacts = 1062
	idOpenDeliverables  = 1063
	idOpenPrimaryAsset  = 1064

	bnClicked    = 0
	enChange     = 0x0300
	cbnSelChange = 1
	accelCommand = 1
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
	procCreateAcceleratorTbl = user32.NewProc("CreateAcceleratorTableW")
	procDestroyAccelerator   = user32.NewProc("DestroyAcceleratorTable")
	procTranslateAccelerator = user32.NewProc("TranslateAcceleratorW")
	procShowWindow           = user32.NewProc("ShowWindow")
	procTranslateMessage     = user32.NewProc("TranslateMessage")
	procUpdateWindow         = user32.NewProc("UpdateWindow")
	procMessageBoxW          = user32.NewProc("MessageBoxW")
	procEnableWindow         = user32.NewProc("EnableWindow")
	procOpenClipboard        = user32.NewProc("OpenClipboard")
	procEmptyClipboard       = user32.NewProc("EmptyClipboard")
	procSetClipboardData     = user32.NewProc("SetClipboardData")
	procCloseClipboard       = user32.NewProc("CloseClipboard")
	procGetModuleHandleW     = kernel32.NewProc("GetModuleHandleW")
	procGlobalAlloc          = kernel32.NewProc("GlobalAlloc")
	procGlobalLock           = kernel32.NewProc("GlobalLock")
	procGlobalUnlock         = kernel32.NewProc("GlobalUnlock")
	procGlobalFree           = kernel32.NewProc("GlobalFree")
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
	accelTable       syscall.Handle
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
	healthServer     syscall.Handle
	healthResult     syscall.Handle
	healthArtifacts  syscall.Handle
	healthAck        syscall.Handle
	previewContent   syscall.Handle
	viewMarkdownBtn  syscall.Handle
	viewStageBtn     syscall.Handle
	viewOutlineBtn   syscall.Handle
	viewBundleBtn    syscall.Handle
	viewReviewBtn    syscall.Handle
	copyPreviewBtn   syscall.Handle
	openPreviewBtn   syscall.Handle
	recentLabel      syscall.Handle
	recentPackage    syscall.Handle
	openRecentBtn    syscall.Handle
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
	importPackageBtn syscall.Handle
	openOutputBtn    syscall.Handle
	exportBtn        syscall.Handle
	approveBtn       syscall.Handle
	uploadBtn        syscall.Handle
	queryStatusBtn   syscall.Handle
	fetchResultBtn   syscall.Handle
	ackResultBtn     syscall.Handle
	downloadBtn      syscall.Handle
	openAssetsBtn    syscall.Handle
	openPrimaryBtn   syscall.Handle
	clearDraftBtn    syscall.Handle
	openLogBtn       syscall.Handle
	statusList       syscall.Handle
	statusBar        syscall.Handle
	markdown         nativeField
	stageJSON        nativeField
	outlineJSON      nativeField

	mu                   sync.Mutex
	generating           bool
	uploading            bool
	queryingStatus       bool
	fetchingResult       bool
	ackingResult         bool
	downloadingArtifacts bool
	lastResult           *nativeGenerateResult
	pendingResult        *nativeGenerateResult
	pendingError         string
	pendingUpload        *nativeUploadResult
	pendingServerStatus  *nativeServerStatusResult
	pendingServerResult  *nativeServerResultFetchResult
	pendingServerAck     *nativeServerAckResult
	pendingDownloads     *nativeArtifactDownloadBatch
	recentPackages       []nativeRecentPackage
	currentPreview       string
	suppressDraftSave    bool
}

type nativeField struct {
	Label syscall.Handle
	Edit  syscall.Handle
}

type nativeGenerateResult struct {
	ProjectID         string `json:"project_id"`
	ReviewText        string `json:"review_text"`
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

type nativeApprovalRecord struct {
	SchemaVersion   string    `json:"schema_version"`
	Decision        string    `json:"decision"`
	ApprovedAt      time.Time `json:"approved_at"`
	ProjectID       string    `json:"project_id"`
	Runtime         string    `json:"runtime,omitempty"`
	StageCount      int       `json:"stage_count,omitempty"`
	OutlineStages   int       `json:"outline_stage_count,omitempty"`
	BundleHash      string    `json:"bundle_hash_sha256_suffix,omitempty"`
	OutputDirectory string    `json:"output_directory"`
	ReviewSurface   string    `json:"review_surface"`
}

type nativeUploadResult struct {
	UploadID          string
	ExchangePackageID string
	CloudJobID        string
	Status            string
	CloudBaseURL      string
	ProjectID         string
	OrgID             string
	OutputDirectory   string
	UploadedAt        time.Time
}

type nativeServerHandoffRecord struct {
	SchemaVersion     string    `json:"schema_version"`
	ProjectID         string    `json:"project_id,omitempty"`
	OrgID             string    `json:"org_id,omitempty"`
	UploadID          string    `json:"upload_id,omitempty"`
	ExchangePackageID string    `json:"exchange_package_id"`
	CloudJobID        string    `json:"cloud_job_id,omitempty"`
	Status            string    `json:"status,omitempty"`
	Stage             string    `json:"stage,omitempty"`
	Message           string    `json:"message,omitempty"`
	ProgressPercent   int       `json:"progress_percent,omitempty"`
	ResultPackageID   string    `json:"result_package_id,omitempty"`
	CloudBaseURL      string    `json:"cloud_base_url,omitempty"`
	OutputDirectory   string    `json:"output_directory,omitempty"`
	UploadedAt        time.Time `json:"uploaded_at"`
	LastStatusAt      time.Time `json:"last_status_at,omitempty"`
	ReviewSurface     string    `json:"review_surface"`
}

type nativeServerStatusResult struct {
	Record nativeServerHandoffRecord
	Status model.ExecutionPackageStatusResponse
}

type nativeServerResultFetchResult struct {
	Record nativeServerHandoffRecord
	Result model.RecordingResultPackage
}

type nativeServerResultRecord struct {
	SchemaVersion     string    `json:"schema_version"`
	ProjectID         string    `json:"project_id,omitempty"`
	OrgID             string    `json:"org_id,omitempty"`
	ExchangePackageID string    `json:"exchange_package_id,omitempty"`
	ResultPackageID   string    `json:"result_package_id"`
	ResultID          string    `json:"result_id,omitempty"`
	Status            string    `json:"status,omitempty"`
	DeliveryStatus    string    `json:"delivery_status,omitempty"`
	AssetCount        int       `json:"asset_count,omitempty"`
	DemoVideoCount    int       `json:"demo_video_count,omitempty"`
	ScreenshotCount   int       `json:"screenshot_count,omitempty"`
	TraceCount        int       `json:"trace_count,omitempty"`
	AckRequired       bool      `json:"ack_required,omitempty"`
	AckedAt           time.Time `json:"acked_at,omitempty"`
	OutputDirectory   string    `json:"output_directory,omitempty"`
	FetchedAt         time.Time `json:"fetched_at"`
}

type nativeServerAckResult struct {
	Record nativeServerHandoffRecord
	Ack    model.ResultPackageAckResponse
}

type nativeServerAckRecord struct {
	SchemaVersion     string    `json:"schema_version"`
	ProjectID         string    `json:"project_id,omitempty"`
	OrgID             string    `json:"org_id,omitempty"`
	ExchangePackageID string    `json:"exchange_package_id,omitempty"`
	ResultPackageID   string    `json:"result_package_id"`
	Status            string    `json:"status,omitempty"`
	DeliveryStatus    string    `json:"delivery_status,omitempty"`
	ReceivedAssetIDs  []string  `json:"received_asset_ids,omitempty"`
	VerifiedChecksums bool      `json:"verified_checksums"`
	AckedAt           time.Time `json:"acked_at,omitempty"`
	AckedByInstallID  string    `json:"acked_by_install_id,omitempty"`
	OutputDirectory   string    `json:"output_directory,omitempty"`
}

type nativeArtifactDownloadBatch struct {
	Record    nativeServerHandoffRecord
	Manifest  nativeArtifactDownloadManifest
	Downloads []app.CloudDeliverableDownloadResult
}

type nativeArtifactDownloadManifest struct {
	SchemaVersion       string                               `json:"schema_version"`
	ProjectID           string                               `json:"project_id,omitempty"`
	OrgID               string                               `json:"org_id,omitempty"`
	ExchangePackageID   string                               `json:"exchange_package_id,omitempty"`
	ResultPackageID     string                               `json:"result_package_id"`
	OutputDirectory     string                               `json:"output_directory,omitempty"`
	DownloadDirectory   string                               `json:"download_directory"`
	DownloadedAt        time.Time                            `json:"downloaded_at"`
	ArtifactCount       int                                  `json:"artifact_count"`
	VerifiedCount       int                                  `json:"verified_count"`
	ChecksumMismatchIDs []string                             `json:"checksum_mismatch_ids,omitempty"`
	Artifacts           []app.CloudDeliverableDownloadResult `json:"artifacts"`
}

type nativeRecentPackage struct {
	ProjectID       string    `json:"project_id"`
	OutputDirectory string    `json:"output_directory"`
	Runtime         string    `json:"runtime,omitempty"`
	StageCount      int       `json:"stage_count,omitempty"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type nativeInput struct {
	ProductURL         string
	LocalRepoPath      string
	GitRepoURL         string
	DemoUsername       string
	DemoPassword       string
	ProductDescription string
}

type nativeInputDraft struct {
	ProductURL         string    `json:"product_url,omitempty"`
	LocalRepoPath      string    `json:"local_repo_path,omitempty"`
	GitRepoURL         string    `json:"git_repo_url,omitempty"`
	ProductDescription string    `json:"product_description,omitempty"`
	UpdatedAt          time.Time `json:"updated_at"`
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
		if a.accelTable != 0 {
			translated, _, _ := procTranslateAccelerator.Call(hwnd, uintptr(a.accelTable), uintptr(unsafe.Pointer(&msg)))
			if translated != 0 {
				continue
			}
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
			app.addStatus("常用快捷键：Ctrl+G 生成，Ctrl+S 保存，Ctrl+Enter 本地审批，Ctrl+1 审核摘要。")
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
			case id == idRecentPackage && code == cbnSelChange:
				app.showSelectedRecentPackageSummary()
			case code == enChange && app.isInputField(id):
				app.updateInputReadiness()
			case code == bnClicked || code == 0 || code == accelCommand:
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
	case wmAppUploadDone:
		if app != nil {
			app.finishUpload()
		}
		return 0
	case wmAppUploadFailed:
		if app != nil {
			app.finishUploadError()
		}
		return 0
	case wmAppServerStatusDone:
		if app != nil {
			app.finishServerStatus()
		}
		return 0
	case wmAppServerStatusFailed:
		if app != nil {
			app.finishServerStatusError()
		}
		return 0
	case wmAppServerResultDone:
		if app != nil {
			app.finishServerResult()
		}
		return 0
	case wmAppServerResultFailed:
		if app != nil {
			app.finishServerResultError()
		}
		return 0
	case wmAppServerAckDone:
		if app != nil {
			app.finishServerAck()
		}
		return 0
	case wmAppServerAckFailed:
		if app != nil {
			app.finishServerAckError()
		}
		return 0
	case wmAppArtifactsDone:
		if app != nil {
			app.finishArtifactDownloads()
		}
		return 0
	case wmAppArtifactsFailed:
		if app != nil {
			app.finishArtifactDownloadsError()
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
	a.createAccelerators()
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
	a.recentLabel = createChild(a.hwnd, "STATIC", "最近三合一包", wsChild|wsVisible, idRecentLabel)
	a.recentPackage = createChild(a.hwnd, "COMBOBOX", "", wsChild|wsVisible|wsBorder|cbsDropDownList|wsVScroll, idRecentPackage)
	a.openRecentBtn = createChild(a.hwnd, "BUTTON", "打开最近包", wsChild|wsVisible|bsPushButton, idOpenRecentPackage)
	a.healthRuntime = createChild(a.hwnd, "STATIC", "Runtime: --", wsChild|wsVisible|wsBorder, idHealthRuntime)
	a.healthStages = createChild(a.hwnd, "STATIC", "Stages: --", wsChild|wsVisible|wsBorder, idHealthStages)
	a.healthBundle = createChild(a.hwnd, "STATIC", "Bundle: --", wsChild|wsVisible|wsBorder, idHealthBundle)
	a.healthValidation = createChild(a.hwnd, "STATIC", "Validation: pending", wsChild|wsVisible|wsBorder, idHealthValidation)
	a.healthServer = createChild(a.hwnd, "STATIC", "Server: not uploaded", wsChild|wsVisible|wsBorder, 0)
	a.healthResult = createChild(a.hwnd, "STATIC", "Result: not fetched", wsChild|wsVisible|wsBorder, 0)
	a.healthArtifacts = createChild(a.hwnd, "STATIC", "Artifacts: not downloaded", wsChild|wsVisible|wsBorder, 0)
	a.healthAck = createChild(a.hwnd, "STATIC", "Ack: pending", wsChild|wsVisible|wsBorder, 0)
	a.previewSummary = createChild(a.hwnd, "EDIT", "等待生成结果。", wsChild|wsVisible|wsBorder|wsVScroll|esMultiline|esAutoVScroll|esReadOnly, idPreviewSummary)
	a.viewReviewBtn = createChild(a.hwnd, "BUTTON", "审核摘要", wsChild|wsVisible|bsPushButton, idViewReview)
	a.viewMarkdownBtn = createChild(a.hwnd, "BUTTON", "Markdown", wsChild|wsVisible|bsPushButton, idViewMarkdown)
	a.viewStageBtn = createChild(a.hwnd, "BUTTON", "Stage JSON", wsChild|wsVisible|bsPushButton, idViewStageJSON)
	a.viewOutlineBtn = createChild(a.hwnd, "BUTTON", "Outline", wsChild|wsVisible|bsPushButton, idViewOutline)
	a.viewBundleBtn = createChild(a.hwnd, "BUTTON", "Full Bundle", wsChild|wsVisible|bsPushButton, idViewBundle)
	a.copyPreviewBtn = createChild(a.hwnd, "BUTTON", "复制", wsChild|wsVisible|bsPushButton, idCopyPreview)
	a.openPreviewBtn = createChild(a.hwnd, "BUTTON", "打开文件", wsChild|wsVisible|bsPushButton, idOpenPreviewFile)
	a.previewContent = createChild(a.hwnd, "EDIT", "", wsChild|wsVisible|wsBorder|wsVScroll|wsHScroll|esMultiline|esAutoVScroll|esAutoHScroll|esReadOnly, idPreviewContent)
	a.productURL = a.labelAndEdit("产品 URL", idProductURL, "https://cascadeai.cn", false, false)
	a.localRepoPath = a.labelAndEdit("本地项目路径（可选）", idLocalRepoPath, "", false, false)
	a.gitRepoURL = a.labelAndEdit("GitHub 仓库 URL（可选）", idGitRepoURL, "", false, false)
	a.demoUsername = a.labelAndEdit("演示账号（可选）", idDemoUsername, "", false, false)
	a.demoPassword = a.labelAndEdit("演示密码（可选）", idDemoPassword, "", true, false)
	a.requirement = a.labelAndEdit("需求文档 / 需求文本", idRequirement, "", false, true)
	a.restoreInputDraft()
	a.loadRecentPackages()
	a.inputReadiness = createChild(a.hwnd, "STATIC", "", wsChild|wsVisible|wsBorder, idInputReadiness)
	a.generateBtn = createChild(a.hwnd, "BUTTON", "生成三合一执行包", wsChild|wsVisible|bsPushButton, idGenerateButton)
	a.saveBtn = createChild(a.hwnd, "BUTTON", "保存三合一包", wsChild|wsVisible|bsPushButton, idSaveButton)
	a.browseRepoBtn = createChild(a.hwnd, "BUTTON", "选择文件夹", wsChild|wsVisible|bsPushButton, idBrowseRepo)
	a.importReqBtn = createChild(a.hwnd, "BUTTON", "导入文档", wsChild|wsVisible|bsPushButton, idImportRequirement)
	a.importPackageBtn = createChild(a.hwnd, "BUTTON", "导入三合一包", wsChild|wsVisible|bsPushButton, idImportPackage)
	a.clearDraftBtn = createChild(a.hwnd, "BUTTON", "清除草稿", wsChild|wsVisible|bsPushButton, idClearDraft)
	a.openOutputBtn = createChild(a.hwnd, "BUTTON", "打开输出目录", wsChild|wsVisible|bsPushButton, idOpenOutput)
	a.exportBtn = createChild(a.hwnd, "BUTTON", "导出到文件夹", wsChild|wsVisible|bsPushButton, idExportPackage)
	a.approveBtn = createChild(a.hwnd, "BUTTON", "本地审批通过", wsChild|wsVisible|bsPushButton, idApprovePackage)
	a.uploadBtn = createChild(a.hwnd, "BUTTON", "上传已审批包", wsChild|wsVisible|bsPushButton, idUploadApproved)
	a.queryStatusBtn = createChild(a.hwnd, "BUTTON", "查询服务器状态", wsChild|wsVisible|bsPushButton, idQueryServerStatus)
	a.fetchResultBtn = createChild(a.hwnd, "BUTTON", "获取结果包", wsChild|wsVisible|bsPushButton, idFetchServerResult)
	a.ackResultBtn = createChild(a.hwnd, "BUTTON", "确认交付 ACK", wsChild|wsVisible|bsPushButton, idAckServerResult)
	a.downloadBtn = createChild(a.hwnd, "BUTTON", "下载产物并校验", wsChild|wsVisible|bsPushButton, idDownloadArtifacts)
	a.openAssetsBtn = createChild(a.hwnd, "BUTTON", "打开产物目录", wsChild|wsVisible|bsPushButton, idOpenDeliverables)
	a.openPrimaryBtn = createChild(a.hwnd, "BUTTON", "打开主产物", wsChild|wsVisible|bsPushButton, idOpenPrimaryAsset)
	a.openLogBtn = createChild(a.hwnd, "BUTTON", "打开诊断日志", wsChild|wsVisible|bsPushButton, idOpenLog)
	a.statusList = createChild(a.hwnd, "LISTBOX", "", wsChild|wsVisible|wsBorder|wsVScroll|lbsNotify, idStatusList)
	a.statusBar = createChild(a.hwnd, "STATIC", a.statusBarText(""), wsChild|wsVisible|wsBorder, idStatusBar)
	a.applyDefaultFont()
	a.refreshRecentPackageList()
	a.updateInputReadiness()
	a.showInputPreflightIfIdle()
	a.setPhaseText("1 输入材料", "2 项目理解", "3 生成包", "4 审核保存")
	a.updateActionState(false, false)
}

func (a *nativeApp) handleCommand(id int) {
	if !a.commandAllowed(id) {
		return
	}
	switch id {
	case idGenerateButton:
		a.startGenerate()
	case idSaveButton:
		a.saveLastResult()
	case idExportPackage:
		a.exportLastResult()
	case idApprovePackage:
		a.approveLastResult()
	case idUploadApproved:
		a.uploadApprovedPackage()
	case idQueryServerStatus:
		a.queryServerStatus()
	case idFetchServerResult:
		a.fetchServerResult()
	case idDownloadArtifacts:
		a.downloadServerArtifacts()
	case idAckServerResult:
		a.ackServerResult()
	case idOpenDeliverables:
		a.openDownloadedArtifactsFolder()
	case idOpenPrimaryAsset:
		a.openPrimaryDownloadedArtifact()
	case idClearDraft:
		a.clearInputDraft()
	case idBrowseRepo:
		a.chooseLocalRepoPath()
	case idImportRequirement:
		a.importRequirementDocument()
	case idImportPackage:
		a.importPackageFolder()
	case idOpenOutput:
		a.openLastOutputDirectory()
	case idOpenRecentPackage:
		a.openSelectedRecentPackage()
	case idOpenLog:
		a.openDiagnosticLog()
	case idViewMarkdown:
		a.showPreview("markdown")
	case idViewReview:
		a.showPreview("review")
	case idViewStageJSON:
		a.showPreview("stage")
	case idViewOutline:
		a.showPreview("outline")
	case idViewBundle:
		a.showPreview("bundle")
	case idCopyPreview:
		a.copyCurrentPreview()
	case idOpenPreviewFile:
		a.openCurrentPreviewFile()
	case idMenuExit:
		procPostMessageW.Call(uintptr(a.hwnd), wmClose, 0, 0)
	}
}

func (a *nativeApp) commandAllowed(id int) bool {
	a.mu.Lock()
	generating := a.generating
	uploading := a.uploading
	queryingStatus := a.queryingStatus
	fetchingResult := a.fetchingResult
	ackingResult := a.ackingResult
	downloadingArtifacts := a.downloadingArtifacts
	hasResult := a.lastResult != nil
	hasRecent := len(a.recentPackages) > 0
	a.mu.Unlock()
	busy := generating || uploading || queryingStatus || fetchingResult || ackingResult || downloadingArtifacts
	switch id {
	case idOpenLog, idMenuExit:
		return true
	case idGenerateButton:
		return !busy && a.inputReady()
	case idBrowseRepo, idImportRequirement, idImportPackage, idClearDraft:
		return !busy
	case idSaveButton, idExportPackage, idApprovePackage, idOpenOutput, idViewReview, idViewMarkdown, idViewStageJSON, idViewOutline, idViewBundle, idCopyPreview, idOpenPreviewFile, idUploadApproved, idQueryServerStatus, idFetchServerResult, idDownloadArtifacts, idAckServerResult, idOpenDeliverables, idOpenPrimaryAsset:
		return !busy && hasResult
	case idOpenRecentPackage:
		return !busy && hasRecent
	default:
		return true
	}
}

func (a *nativeApp) createMenu() {
	mainMenu := createMenu()
	fileMenu := createPopupMenu()
	viewMenu := createPopupMenu()
	helpMenu := createPopupMenu()
	appendMenuItem(fileMenu, idImportRequirement, "导入需求文档...\tCtrl+O")
	appendMenuItem(fileMenu, idImportPackage, "导入三合一包文件夹...\tCtrl+I")
	appendMenuItem(fileMenu, idBrowseRepo, "选择本地项目文件夹...\tCtrl+B")
	appendMenuItem(fileMenu, idClearDraft, "清除输入草稿\tCtrl+R")
	appendMenuSeparator(fileMenu)
	appendMenuItem(fileMenu, idGenerateButton, "生成三合一执行包\tCtrl+G")
	appendMenuItem(fileMenu, idSaveButton, "保存三合一包\tCtrl+S")
	appendMenuItem(fileMenu, idExportPackage, "导出三合一包到文件夹...\tCtrl+E")
	appendMenuItem(fileMenu, idApprovePackage, "本地审批通过\tCtrl+Enter")
	appendMenuItem(fileMenu, idUploadApproved, "上传已审批包到服务器\tCtrl+U")
	appendMenuItem(fileMenu, idQueryServerStatus, "查询服务器状态\tCtrl+Shift+U")
	appendMenuItem(fileMenu, idFetchServerResult, "获取服务器结果包\tCtrl+Alt+R")
	appendMenuItem(fileMenu, idDownloadArtifacts, "下载服务器产物并校验\tCtrl+Alt+D")
	appendMenuItem(fileMenu, idAckServerResult, "确认服务器交付 ACK\tCtrl+Alt+A")
	appendMenuItem(fileMenu, idOpenDeliverables, "打开服务器产物目录\tCtrl+Alt+O")
	appendMenuItem(fileMenu, idOpenPrimaryAsset, "打开主产物\tCtrl+Alt+P")
	appendMenuSeparator(fileMenu)
	appendMenuItem(fileMenu, idOpenOutput, "打开输出目录\tCtrl+Shift+O")
	appendMenuItem(fileMenu, idOpenRecentPackage, "打开最近三合一包\tCtrl+Shift+R")
	appendMenuItem(fileMenu, idOpenLog, "打开诊断日志\tCtrl+L")
	appendMenuSeparator(fileMenu)
	appendMenuItem(fileMenu, idMenuExit, "退出")
	appendMenuItem(viewMenu, idViewReview, "预览审核摘要\tCtrl+1")
	appendMenuItem(viewMenu, idViewMarkdown, "预览 Markdown\tCtrl+2")
	appendMenuItem(viewMenu, idViewStageJSON, "预览 Stage JSON\tCtrl+3")
	appendMenuItem(viewMenu, idViewOutline, "预览 Script Outline\tCtrl+4")
	appendMenuItem(viewMenu, idViewBundle, "预览 Full Bundle\tCtrl+5")
	appendMenuSeparator(viewMenu)
	appendMenuItem(viewMenu, idCopyPreview, "复制当前预览\tCtrl+Shift+C")
	appendMenuItem(viewMenu, idOpenPreviewFile, "打开当前预览文件\tCtrl+Shift+F")
	appendMenuItem(helpMenu, idOpenLog, "诊断日志")
	appendSubMenu(mainMenu, fileMenu, "文件")
	appendSubMenu(mainMenu, viewMenu, "视图")
	appendSubMenu(mainMenu, helpMenu, "帮助")
	procSetMenu.Call(uintptr(a.hwnd), uintptr(mainMenu))
	procDrawMenuBar.Call(uintptr(a.hwnd))
	a.mainMenu = syscall.Handle(mainMenu)
}

func (a *nativeApp) createAccelerators() {
	accels := []accel{
		{FVirt: fVirtKey | fControl, Key: 'O', Cmd: idImportRequirement},
		{FVirt: fVirtKey | fControl, Key: 'I', Cmd: idImportPackage},
		{FVirt: fVirtKey | fControl, Key: 'B', Cmd: idBrowseRepo},
		{FVirt: fVirtKey | fControl, Key: 'R', Cmd: idClearDraft},
		{FVirt: fVirtKey | fControl, Key: 'G', Cmd: idGenerateButton},
		{FVirt: fVirtKey | fControl, Key: 'S', Cmd: idSaveButton},
		{FVirt: fVirtKey | fControl, Key: 'E', Cmd: idExportPackage},
		{FVirt: fVirtKey | fControl, Key: vkReturn, Cmd: idApprovePackage},
		{FVirt: fVirtKey | fControl, Key: 'U', Cmd: idUploadApproved},
		{FVirt: fVirtKey | fControl | fShift, Key: 'U', Cmd: idQueryServerStatus},
		{FVirt: fVirtKey | fAlt, Key: 'R', Cmd: idFetchServerResult},
		{FVirt: fVirtKey | fAlt, Key: 'D', Cmd: idDownloadArtifacts},
		{FVirt: fVirtKey | fAlt, Key: 'A', Cmd: idAckServerResult},
		{FVirt: fVirtKey | fAlt, Key: 'O', Cmd: idOpenDeliverables},
		{FVirt: fVirtKey | fAlt, Key: 'P', Cmd: idOpenPrimaryAsset},
		{FVirt: fVirtKey | fControl | fShift, Key: 'O', Cmd: idOpenOutput},
		{FVirt: fVirtKey | fControl | fShift, Key: 'R', Cmd: idOpenRecentPackage},
		{FVirt: fVirtKey | fControl, Key: 'L', Cmd: idOpenLog},
		{FVirt: fVirtKey | fControl, Key: '1', Cmd: idViewReview},
		{FVirt: fVirtKey | fControl, Key: '2', Cmd: idViewMarkdown},
		{FVirt: fVirtKey | fControl, Key: '3', Cmd: idViewStageJSON},
		{FVirt: fVirtKey | fControl, Key: '4', Cmd: idViewOutline},
		{FVirt: fVirtKey | fControl, Key: '5', Cmd: idViewBundle},
		{FVirt: fVirtKey | fControl | fShift, Key: 'C', Cmd: idCopyPreview},
		{FVirt: fVirtKey | fControl | fShift, Key: 'F', Cmd: idOpenPreviewFile},
	}
	handle, _, _ := procCreateAcceleratorTbl.Call(uintptr(unsafe.Pointer(&accels[0])), uintptr(len(accels)))
	a.accelTable = syscall.Handle(handle)
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
	statusBarH := 26
	contentBottom := maxInt(220, height-margin-statusBarH-8)
	headerH := 88
	leftW := 440
	gap := 18
	leftTop := margin + headerH
	x := margin + 14
	y := leftTop + 34
	rowH := 30
	leftInnerW := leftW - 28
	inputH := 666
	statusTop := leftTop + inputH + 12
	statusH := maxInt(150, contentBottom-statusTop)

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
	moveControl(a.importPackageBtn, x, y+108, 132, 24)
	moveControl(a.clearDraftBtn, x+144, y+108, 108, 24)
	y += 128
	moveControl(a.inputReadiness, x, y, leftInnerW, 34)
	y += 48
	moveControl(a.generateBtn, x, y, 186, 34)
	moveControl(a.saveBtn, x+202, y, 190, 34)
	moveControl(a.exportBtn, x, y+42, 186, 32)
	moveControl(a.openOutputBtn, x+202, y+42, 190, 32)
	moveControl(a.approveBtn, x, y+82, leftInnerW, 32)
	moveControl(a.uploadBtn, x, y+120, 190, 32)
	moveControl(a.queryStatusBtn, x+202, y+120, 190, 32)
	moveControl(a.fetchResultBtn, x, y+158, 190, 32)
	moveControl(a.downloadBtn, x+202, y+158, 190, 32)
	moveControl(a.openAssetsBtn, x, y+196, 190, 32)
	moveControl(a.openPrimaryBtn, x+202, y+196, 190, 32)
	moveControl(a.ackResultBtn, x, y+234, leftInnerW, 32)

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
	rightH := maxInt(300, contentBottom-rightTop)
	moveControl(a.previewGroup, rightX, rightTop, rightW, rightH)
	moveControl(a.previewTitle, rightX+14, rightTop+28, rightW-28, 18)
	moveControl(a.previewHint, rightX+14, rightTop+50, rightW-28, 18)
	moveControl(a.artifactStatus, rightX+14, rightTop+72, rightW-28, 18)
	recentTop := rightTop + 96
	moveControl(a.recentLabel, rightX+14, recentTop+5, 94, 18)
	moveControl(a.recentPackage, rightX+112, recentTop, maxInt(180, rightW-28-112-112), 220)
	moveControl(a.openRecentBtn, rightX+rightW-14-104, recentTop, 104, 28)
	healthTop := rightTop + 134
	healthGap := 8
	healthW := maxInt(92, (rightW-28-healthGap*3)/4)
	moveControl(a.healthRuntime, rightX+14, healthTop, healthW, 30)
	moveControl(a.healthStages, rightX+14+(healthW+healthGap), healthTop, healthW, 30)
	moveControl(a.healthBundle, rightX+14+(healthW+healthGap)*2, healthTop, healthW, 30)
	moveControl(a.healthValidation, rightX+14+(healthW+healthGap)*3, healthTop, healthW, 30)
	serverHealthTop := healthTop + 38
	moveControl(a.healthServer, rightX+14, serverHealthTop, healthW, 30)
	moveControl(a.healthResult, rightX+14+(healthW+healthGap), serverHealthTop, healthW, 30)
	moveControl(a.healthArtifacts, rightX+14+(healthW+healthGap)*2, serverHealthTop, healthW, 30)
	moveControl(a.healthAck, rightX+14+(healthW+healthGap)*3, serverHealthTop, healthW, 30)
	summaryTop := serverHealthTop + 42
	summaryH := 74
	moveControl(a.previewSummary, rightX+14, summaryTop, rightW-28, summaryH)
	tabTop := summaryTop + summaryH + 12
	copyW := 72
	openPreviewW := 88
	buttonW := maxInt(64, (rightW-28-copyW-openPreviewW-healthGap*6)/5)
	moveControl(a.viewReviewBtn, rightX+14, tabTop, buttonW, 30)
	moveControl(a.viewMarkdownBtn, rightX+14+buttonW+healthGap, tabTop, buttonW, 30)
	moveControl(a.viewStageBtn, rightX+14+(buttonW+healthGap)*2, tabTop, buttonW, 30)
	moveControl(a.viewOutlineBtn, rightX+14+(buttonW+healthGap)*3, tabTop, buttonW, 30)
	moveControl(a.viewBundleBtn, rightX+14+(buttonW+healthGap)*4, tabTop, buttonW, 30)
	moveControl(a.copyPreviewBtn, rightX+rightW-14-openPreviewW-healthGap-copyW, tabTop, copyW, 30)
	moveControl(a.openPreviewBtn, rightX+rightW-14-openPreviewW, tabTop, openPreviewW, 30)
	moveControl(a.previewContent, rightX+14, tabTop+40, rightW-28, maxInt(160, rightH-(tabTop-rightTop)-54))
	moveControl(a.statusBar, margin, height-margin-statusBarH, maxInt(300, width-margin*2), statusBarH)
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
	if !a.inputReady() {
		a.mu.Unlock()
		a.updateInputReadiness()
		a.addStatus("产品 URL 和需求文本填写完整后才能生成。")
		return
	}
	a.generating = true
	a.mu.Unlock()
	setWindowText(a.generateBtn, "生成中...")
	setWindowText(a.workflowState, "正在生成三合一包")
	setWindowText(a.artifactStatus, "输出目录：生成完成后显示")
	a.setHealthText("Runtime: generating", "Stages: --", "Bundle: --", "Validation: pending")
	a.setServerHealthText("Server: --", "Result: --", "Artifacts: --", "Ack: --")
	a.setPhaseText("1 输入完成", "2 理解中", "3 生成中", "4 待审核")
	a.updateActionState(true, false)
	a.addStatus("开始本地项目理解与三合一包生成。")
	input := a.currentNativeInput()
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
	reviewText := nativeReviewText(state, bundle)
	return &nativeGenerateResult{
		ProjectID:         state.ProjectID,
		ReviewText:        reviewText,
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
	setWindowText(a.previewContent, result.ReviewText)
	a.setStatusBarOutput(result.OutputDirectory)
	a.currentPreview = "review"
	a.setHealthText(result.HealthRuntime, result.HealthStages, result.HealthBundle, result.HealthValidation)
	a.refreshServerHealth(result.OutputDirectory)
	a.setPhaseText("1 输入完成", "2 理解完成", "3 生成完成", "4 可审核")
	a.updateActionState(false, true)
	a.rememberRecentPackage(result, result.OutputDirectory)
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
	a.setServerHealthText("Server: --", "Result: --", "Artifacts: --", "Ack: --")
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
	if err := writeResultFiles(dir, result); err != nil {
		a.addStatus("保存失败：" + err.Error())
		return
	}
	a.addStatus("已保存到 " + dir)
	setWindowText(a.workflowState, "已保存三合一包")
	setWindowText(a.artifactStatus, "已保存："+dir)
	a.setStatusBarOutput(dir)
	a.setPhaseText("1 输入完成", "2 理解完成", "3 生成完成", "4 已保存")
	a.refreshServerHealth(dir)
	a.rememberRecentPackage(result, dir)
	setEnabled(a.openOutputBtn, true)
	messageBox("Cascade DemoOps", "三合一执行包已保存到：\n"+dir, false)
}

func (a *nativeApp) exportLastResult() {
	a.mu.Lock()
	result := a.lastResult
	a.mu.Unlock()
	if result == nil {
		a.addStatus("还没有可导出的执行包。")
		return
	}
	dir, err := browseForFolder(a.hwnd, "选择三合一包导出文件夹")
	if err != nil {
		a.addStatus("导出文件夹选择失败：" + err.Error())
		return
	}
	if strings.TrimSpace(dir) == "" {
		return
	}
	if resultFilesExist(dir) && !confirmBox(a.hwnd, "Cascade DemoOps", "所选文件夹中已有三合一包文件。\n\n是否覆盖这些审批材料？") {
		a.addStatus("已取消导出，未覆盖现有文件。")
		return
	}
	if err := writeResultFiles(dir, result); err != nil {
		a.addStatus("导出失败：" + err.Error())
		messageBox("Cascade DemoOps", "导出三合一包失败：\n"+err.Error(), true)
		return
	}
	a.addStatus("已导出到 " + dir)
	setWindowText(a.workflowState, "已导出三合一包")
	setWindowText(a.artifactStatus, "已导出："+dir)
	a.setStatusBarOutput(dir)
	a.setPhaseText("1 输入完成", "2 理解完成", "3 生成完成", "4 已导出")
	a.refreshServerHealth(dir)
	a.rememberRecentPackage(result, dir)
	messageBox("Cascade DemoOps", "三合一执行包已导出到：\n"+dir, false)
}

func (a *nativeApp) approveLastResult() {
	a.mu.Lock()
	result := a.lastResult
	a.mu.Unlock()
	if result == nil {
		a.addStatus("还没有可审批的执行包。")
		return
	}
	dir := result.OutputDirectory
	if strings.TrimSpace(dir) == "" {
		dir = filepath.Join(a.runtimeConfig.ArtifactRoot, result.ProjectID)
	}
	if !confirmBox(a.hwnd, "Cascade DemoOps", "确认本地审批通过当前三合一包？\n\n这会保存审批材料并写入 approval_record.json，表示可交给服务器 Browser Agent 执行。") {
		a.addStatus("已取消本地审批。")
		return
	}
	if err := writeResultFiles(dir, result); err != nil {
		a.addStatus("审批前保存失败：" + err.Error())
		messageBox("Cascade DemoOps", "审批前保存三合一包失败：\n"+err.Error(), true)
		return
	}
	record := nativeApprovalRecord{
		SchemaVersion:   "demoops.native_approval_record.v1",
		Decision:        "approved_for_server_browser_agent",
		ApprovedAt:      time.Now().UTC(),
		ProjectID:       strings.TrimSpace(result.ProjectID),
		Runtime:         strings.TrimSpace(result.Runtime),
		StageCount:      result.StageCount,
		OutlineStages:   result.OutlineStageCount,
		BundleHash:      strings.TrimSpace(result.BundleHashSuffix),
		OutputDirectory: strings.TrimSpace(dir),
		ReviewSurface:   "native_win32",
	}
	if err := writeApprovalRecord(dir, record); err != nil {
		a.addStatus("审批记录写入失败：" + err.Error())
		messageBox("Cascade DemoOps", "审批记录写入失败：\n"+err.Error(), true)
		return
	}
	a.rememberRecentPackage(result, dir)
	a.addStatus("本地审批已记录：" + filepath.Join(dir, "approval_record.json"))
	setWindowText(a.workflowState, "本地审批已通过")
	setWindowText(a.artifactStatus, "已审批："+dir)
	setWindowText(a.previewSummary, nativeResultSummaryForDirectory(result, dir))
	a.setStatusBarOutput(dir)
	a.refreshServerHealth(dir)
	a.setPhaseText("1 输入完成", "2 理解完成", "3 生成完成", "4 已审批")
	messageBox("Cascade DemoOps", "本地审批已记录：\n"+filepath.Join(dir, "approval_record.json"), false)
}

func (a *nativeApp) uploadApprovedPackage() {
	a.mu.Lock()
	if a.uploading || a.queryingStatus {
		a.mu.Unlock()
		return
	}
	result := a.lastResult
	a.mu.Unlock()
	if result == nil {
		a.addStatus("还没有可上传的执行包。")
		return
	}
	dir := result.OutputDirectory
	if strings.TrimSpace(dir) == "" {
		dir = filepath.Join(a.runtimeConfig.ArtifactRoot, result.ProjectID)
	}
	if serverUploadReadinessLabel(dir) != "ready for server Browser Agent" {
		message := "上传前需要先保存三合一包并完成本地审批。\n\n当前状态：\n" +
			"Files: " + recentPackageFilesLabel(dir) + "\n" +
			"Approval: " + approvalRecordLabel(dir)
		a.addStatus("上传被阻止：" + strings.ReplaceAll(message, "\n", " "))
		messageBox("Cascade DemoOps", message, true)
		return
	}
	if strings.TrimSpace(result.ProjectID) == "" {
		messageBox("Cascade DemoOps", "当前执行包缺少 project_id，无法从本地状态构建上传包。请重新生成后再上传。", true)
		return
	}
	if strings.TrimSpace(a.runtimeConfig.CloudExchangeBaseURL) == "" {
		messageBox("Cascade DemoOps", "服务器地址未配置。\n\n本地包已经可审批，但上传/录制阶段需要 CASCADE_CLOUD_EXCHANGE_BASE_URL 或安装密钥发现能力。", true)
		return
	}
	if !confirmBox(a.hwnd, "Cascade DemoOps", "确认上传已审批三合一包到服务器？\n\n本地会重新构建结构化 ClientExecutionPackage，并通过 exchange init/upload 提交；不会在此步骤等待完整录制完成。") {
		a.addStatus("已取消上传已审批包。")
		return
	}
	a.mu.Lock()
	a.uploading = true
	a.pendingUpload = nil
	a.pendingError = ""
	a.mu.Unlock()
	setWindowText(a.uploadBtn, "上传中...")
	setWindowText(a.workflowState, "正在上传已审批包")
	a.setPhaseText("1 输入完成", "2 理解完成", "3 已审批", "4 上传中")
	a.updateActionStateFromCurrent()
	a.addStatus("开始上传已审批包到服务器。project_id=" + result.ProjectID)
	go a.uploadApprovedPackageAsync(result.ProjectID)
}

func (a *nativeApp) uploadApprovedPackageAsync(projectID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	build, err := a.service.BuildClientExecutionPackage(ctx, projectID, nativeDefaultOrgID)
	if err != nil {
		a.postUploadError(err.Error())
		return
	}
	initResponse, err := a.service.InitCloudExecutionPackageUpload(ctx, build)
	if err != nil {
		a.postUploadError(err.Error())
		return
	}
	uploadResponse, err := a.service.UploadBuiltCloudExecutionPackage(ctx, initResponse.UploadID, build)
	if err != nil {
		a.postUploadError(err.Error())
		return
	}
	a.mu.Lock()
	a.pendingUpload = &nativeUploadResult{
		UploadID:          initResponse.UploadID,
		ExchangePackageID: uploadResponse.ExchangePackageID,
		CloudJobID:        uploadResponse.CloudJobID,
		Status:            string(uploadResponse.Status),
		CloudBaseURL:      strings.TrimSpace(a.runtimeConfig.CloudExchangeBaseURL),
		ProjectID:         strings.TrimSpace(projectID),
		OrgID:             nativeDefaultOrgID,
		UploadedAt:        time.Now().UTC(),
	}
	a.mu.Unlock()
	procPostMessageW.Call(uintptr(a.hwnd), wmAppUploadDone, 0, 0)
}

func (a *nativeApp) finishUpload() {
	a.mu.Lock()
	result := a.pendingUpload
	a.pendingUpload = nil
	a.uploading = false
	a.mu.Unlock()
	setWindowText(a.uploadBtn, "上传已审批包")
	a.updateActionStateFromCurrent()
	if result == nil {
		a.finishUploadErrorMessage("上传完成但没有得到服务器响应。")
		return
	}
	outputDir := ""
	a.mu.Lock()
	if a.lastResult != nil {
		outputDir = strings.TrimSpace(a.lastResult.OutputDirectory)
		if outputDir == "" && strings.TrimSpace(a.lastResult.ProjectID) != "" {
			outputDir = filepath.Join(a.runtimeConfig.ArtifactRoot, a.lastResult.ProjectID)
		}
	}
	a.mu.Unlock()
	result.OutputDirectory = outputDir
	record := nativeServerHandoffRecord{
		SchemaVersion:     "demoops.native_server_handoff_record.v1",
		ProjectID:         strings.TrimSpace(result.ProjectID),
		OrgID:             firstNonEmptyNative(result.OrgID, nativeDefaultOrgID),
		UploadID:          strings.TrimSpace(result.UploadID),
		ExchangePackageID: strings.TrimSpace(result.ExchangePackageID),
		CloudJobID:        strings.TrimSpace(result.CloudJobID),
		Status:            strings.TrimSpace(result.Status),
		CloudBaseURL:      strings.TrimSpace(result.CloudBaseURL),
		OutputDirectory:   strings.TrimSpace(outputDir),
		UploadedAt:        result.UploadedAt,
		LastStatusAt:      time.Now().UTC(),
		ReviewSurface:     "native_win32",
	}
	if record.UploadedAt.IsZero() {
		record.UploadedAt = time.Now().UTC()
	}
	if strings.TrimSpace(outputDir) != "" {
		if err := writeServerHandoffRecord(outputDir, record); err != nil {
			a.addStatus("服务器交接记录写入失败：" + err.Error())
		} else {
			a.addStatus("服务器交接记录已写入：" + serverHandoffRecordPath(outputDir))
		}
	}
	setWindowText(a.workflowState, "已上传到服务器")
	a.setPhaseText("1 输入完成", "2 理解完成", "3 已审批", "4 已上传")
	lines := []string{
		"服务器上传已完成",
		"Upload ID: " + firstNonEmptyNative(result.UploadID, "unknown"),
		"Exchange Package ID: " + firstNonEmptyNative(result.ExchangePackageID, "unknown"),
		"Cloud Job ID: " + firstNonEmptyNative(result.CloudJobID, "unknown"),
		"Status: " + firstNonEmptyNative(result.Status, "unknown"),
		"Server: " + firstNonEmptyNative(result.CloudBaseURL, a.cloudConnectionLabel()),
		"Handoff record: " + handoffRecordLabel(outputDir),
		"Next: 使用“查询服务器状态”查看 Browser Agent 录制进度。",
	}
	setWindowText(a.previewSummary, strings.Join(lines, "\r\n"))
	if strings.TrimSpace(outputDir) != "" {
		a.refreshServerHealth(outputDir)
	}
	a.addStatus("已上传到服务器：exchange_package_id=" + result.ExchangePackageID)
	messageBox("Cascade DemoOps", "已上传到服务器。\n\nExchange Package ID:\n"+result.ExchangePackageID, false)
}

func (a *nativeApp) finishUploadError() {
	a.mu.Lock()
	message := a.pendingError
	a.pendingError = ""
	a.uploading = false
	a.mu.Unlock()
	a.finishUploadErrorMessage(message)
}

func (a *nativeApp) finishUploadErrorMessage(message string) {
	setWindowText(a.uploadBtn, "上传已审批包")
	setWindowText(a.workflowState, "上传失败")
	a.setPhaseText("1 输入完成", "2 理解完成", "3 已审批", "4 上传失败")
	a.updateActionStateFromCurrent()
	if strings.TrimSpace(message) == "" {
		message = "上传已审批包失败。"
	}
	a.addStatus("上传失败：" + message)
	messageBox("Cascade DemoOps", message, true)
}

func (a *nativeApp) postUploadError(message string) {
	a.mu.Lock()
	a.pendingError = message
	a.mu.Unlock()
	procPostMessageW.Call(uintptr(a.hwnd), wmAppUploadFailed, 0, 0)
}

func (a *nativeApp) queryServerStatus() {
	a.mu.Lock()
	if a.uploading || a.queryingStatus {
		a.mu.Unlock()
		return
	}
	result := a.lastResult
	a.mu.Unlock()
	if result == nil {
		a.addStatus("还没有可查询服务器状态的执行包。")
		return
	}
	dir := strings.TrimSpace(result.OutputDirectory)
	if dir == "" && strings.TrimSpace(result.ProjectID) != "" {
		dir = filepath.Join(a.runtimeConfig.ArtifactRoot, result.ProjectID)
	}
	record, err := readServerHandoffRecord(dir)
	if err != nil {
		message := "当前三合一包还没有服务器交接记录。\n\n请先完成“本地审批通过”和“上传已审批包”，或导入包含 server_handoff_record.json 的包目录。\n\n详情：" + err.Error()
		a.addStatus("服务器状态查询被阻止：" + err.Error())
		messageBox("Cascade DemoOps", message, true)
		return
	}
	if strings.TrimSpace(a.runtimeConfig.CloudExchangeBaseURL) == "" {
		messageBox("Cascade DemoOps", "服务器地址未配置。\n\n已有本地交接记录，但状态查询需要 CASCADE_CLOUD_EXCHANGE_BASE_URL 或安装密钥发现能力。", true)
		return
	}
	a.mu.Lock()
	a.queryingStatus = true
	a.pendingServerStatus = nil
	a.pendingError = ""
	a.mu.Unlock()
	setWindowText(a.queryStatusBtn, "查询中...")
	setWindowText(a.workflowState, "正在查询服务器状态")
	a.setPhaseText("1 输入完成", "2 理解完成", "3 已审批", "4 查询中")
	a.updateActionStateFromCurrent()
	a.addStatus("开始查询服务器状态：exchange_package_id=" + record.ExchangePackageID)
	go a.queryServerStatusAsync(record)
}

func (a *nativeApp) queryServerStatusAsync(record nativeServerHandoffRecord) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	status, err := a.service.GetCloudExecutionPackageStatus(ctx, app.CloudStatusRequest{
		OrgID:             firstNonEmptyNative(record.OrgID, nativeDefaultOrgID),
		ExchangePackageID: strings.TrimSpace(record.ExchangePackageID),
	})
	if err != nil {
		a.postServerStatusError(err.Error())
		return
	}
	a.mu.Lock()
	a.pendingServerStatus = &nativeServerStatusResult{Record: record, Status: status}
	a.mu.Unlock()
	procPostMessageW.Call(uintptr(a.hwnd), wmAppServerStatusDone, 0, 0)
}

func (a *nativeApp) finishServerStatus() {
	a.mu.Lock()
	result := a.pendingServerStatus
	a.pendingServerStatus = nil
	a.queryingStatus = false
	a.mu.Unlock()
	setWindowText(a.queryStatusBtn, "查询服务器状态")
	a.updateActionStateFromCurrent()
	if result == nil {
		a.finishServerStatusErrorMessage("服务器状态查询完成但没有得到响应。")
		return
	}
	record := updateServerHandoffRecordFromStatus(result.Record, result.Status)
	if strings.TrimSpace(record.OutputDirectory) != "" {
		if err := writeServerHandoffRecord(record.OutputDirectory, record); err != nil {
			a.addStatus("服务器状态记录更新失败：" + err.Error())
		}
	}
	statusLabel := firstNonEmptyNative(string(result.Status.Status), "unknown")
	setWindowText(a.workflowState, "服务器状态："+statusLabel)
	a.setPhaseText("1 输入完成", "2 理解完成", "3 已审批", "4 "+compactPath(statusLabel, 14))
	setWindowText(a.previewSummary, serverStatusSummary(record, result.Status))
	if strings.TrimSpace(record.OutputDirectory) != "" {
		setWindowText(a.artifactStatus, "服务器状态已更新："+record.OutputDirectory)
		a.setStatusBarOutput(record.OutputDirectory)
		a.refreshServerHealth(record.OutputDirectory)
	}
	a.addStatus("服务器状态已更新：status=" + statusLabel + " stage=" + firstNonEmptyNative(result.Status.Stage, "unknown"))
}

func (a *nativeApp) finishServerStatusError() {
	a.mu.Lock()
	message := a.pendingError
	a.pendingError = ""
	a.queryingStatus = false
	a.mu.Unlock()
	a.finishServerStatusErrorMessage(message)
}

func (a *nativeApp) finishServerStatusErrorMessage(message string) {
	setWindowText(a.queryStatusBtn, "查询服务器状态")
	setWindowText(a.workflowState, "状态查询失败")
	a.setPhaseText("1 输入完成", "2 理解完成", "3 已审批", "4 查询失败")
	a.updateActionStateFromCurrent()
	if strings.TrimSpace(message) == "" {
		message = "服务器状态查询失败。"
	}
	a.addStatus("服务器状态查询失败：" + message)
	messageBox("Cascade DemoOps", message, true)
}

func (a *nativeApp) postServerStatusError(message string) {
	a.mu.Lock()
	a.pendingError = message
	a.mu.Unlock()
	procPostMessageW.Call(uintptr(a.hwnd), wmAppServerStatusFailed, 0, 0)
}

func (a *nativeApp) fetchServerResult() {
	a.mu.Lock()
	if a.serverBusyLocked() {
		a.mu.Unlock()
		return
	}
	result := a.lastResult
	a.mu.Unlock()
	if result == nil {
		a.addStatus("还没有可获取服务器结果的执行包。")
		return
	}
	dir := nativeResultOutputDir(result, a.runtimeConfig.ArtifactRoot)
	record, err := readServerHandoffRecord(dir)
	if err != nil {
		a.addStatus("获取服务器结果被阻止：" + err.Error())
		messageBox("Cascade DemoOps", "当前三合一包还没有服务器交接记录。\n\n请先上传并查询服务器状态，或导入包含 server_handoff_record.json 的包目录。\n\n详情："+err.Error(), true)
		return
	}
	if strings.TrimSpace(record.ResultPackageID) == "" {
		message := "服务器状态还没有 result_package_id。请稍后先查询服务器状态，确认执行已 completed 或结果已生成。"
		a.addStatus("获取服务器结果被阻止：missing result_package_id")
		messageBox("Cascade DemoOps", message, true)
		return
	}
	if strings.TrimSpace(a.runtimeConfig.CloudExchangeBaseURL) == "" {
		messageBox("Cascade DemoOps", "服务器地址未配置，无法获取结果包。", true)
		return
	}
	a.mu.Lock()
	a.fetchingResult = true
	a.pendingServerResult = nil
	a.pendingError = ""
	a.mu.Unlock()
	setWindowText(a.fetchResultBtn, "获取中...")
	setWindowText(a.workflowState, "正在获取服务器结果")
	a.setPhaseText("1 输入完成", "2 理解完成", "3 已审批", "4 获取结果")
	a.updateActionStateFromCurrent()
	a.addStatus("开始获取服务器结果包：result_package_id=" + record.ResultPackageID)
	go a.fetchServerResultAsync(record)
}

func (a *nativeApp) fetchServerResultAsync(record nativeServerHandoffRecord) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	result, err := a.service.GetCloudResultPackage(ctx, app.CloudResultRequest{
		OrgID:           firstNonEmptyNative(record.OrgID, nativeDefaultOrgID),
		ResultPackageID: strings.TrimSpace(record.ResultPackageID),
	})
	if err != nil {
		a.postServerResultError(err.Error())
		return
	}
	a.mu.Lock()
	a.pendingServerResult = &nativeServerResultFetchResult{Record: record, Result: result}
	a.mu.Unlock()
	procPostMessageW.Call(uintptr(a.hwnd), wmAppServerResultDone, 0, 0)
}

func (a *nativeApp) finishServerResult() {
	a.mu.Lock()
	result := a.pendingServerResult
	a.pendingServerResult = nil
	a.fetchingResult = false
	a.mu.Unlock()
	setWindowText(a.fetchResultBtn, "获取结果包")
	a.updateActionStateFromCurrent()
	if result == nil {
		a.finishServerResultErrorMessage("服务器结果获取完成但没有得到结果包。")
		return
	}
	record := updateServerHandoffRecordFromResult(result.Record, result.Result)
	if strings.TrimSpace(record.OutputDirectory) != "" {
		if err := writeServerHandoffRecord(record.OutputDirectory, record); err != nil {
			a.addStatus("服务器交接记录更新失败：" + err.Error())
		}
		if err := writeServerResultPackage(record.OutputDirectory, result.Result); err != nil {
			a.addStatus("服务器结果包保存失败：" + err.Error())
			messageBox("Cascade DemoOps", "服务器结果包保存失败：\n"+err.Error(), true)
			return
		}
		if err := writeServerResultRecord(record.OutputDirectory, serverResultRecordFromPackage(record, result.Result)); err != nil {
			a.addStatus("服务器结果摘要保存失败：" + err.Error())
		}
	}
	setWindowText(a.workflowState, "服务器结果已获取")
	a.setPhaseText("1 输入完成", "2 理解完成", "3 已审批", "4 结果已取")
	setWindowText(a.previewSummary, serverResultSummary(record, result.Result))
	if strings.TrimSpace(record.OutputDirectory) != "" {
		setWindowText(a.artifactStatus, "服务器结果已保存："+record.OutputDirectory)
		a.setStatusBarOutput(record.OutputDirectory)
		a.refreshServerHealth(record.OutputDirectory)
	}
	a.addStatus("服务器结果包已获取：result_id=" + firstNonEmptyNative(result.Result.ResultID, "unknown"))
}

func (a *nativeApp) finishServerResultError() {
	a.mu.Lock()
	message := a.pendingError
	a.pendingError = ""
	a.fetchingResult = false
	a.mu.Unlock()
	a.finishServerResultErrorMessage(message)
}

func (a *nativeApp) finishServerResultErrorMessage(message string) {
	setWindowText(a.fetchResultBtn, "获取结果包")
	setWindowText(a.workflowState, "结果获取失败")
	a.setPhaseText("1 输入完成", "2 理解完成", "3 已审批", "4 获取失败")
	a.updateActionStateFromCurrent()
	if strings.TrimSpace(message) == "" {
		message = "服务器结果获取失败。"
	}
	a.addStatus("服务器结果获取失败：" + message)
	messageBox("Cascade DemoOps", message, true)
}

func (a *nativeApp) postServerResultError(message string) {
	a.mu.Lock()
	a.pendingError = message
	a.mu.Unlock()
	procPostMessageW.Call(uintptr(a.hwnd), wmAppServerResultFailed, 0, 0)
}

func (a *nativeApp) downloadServerArtifacts() {
	a.mu.Lock()
	if a.serverBusyLocked() {
		a.mu.Unlock()
		return
	}
	result := a.lastResult
	a.mu.Unlock()
	if result == nil {
		a.addStatus("还没有可下载产物的执行包。")
		return
	}
	dir := nativeResultOutputDir(result, a.runtimeConfig.ArtifactRoot)
	record, err := readServerHandoffRecord(dir)
	if err != nil {
		a.addStatus("下载服务器产物被阻止：" + err.Error())
		messageBox("Cascade DemoOps", "当前三合一包还没有服务器交接记录。\n\n请先上传、查询状态并获取结果包。\n\n详情："+err.Error(), true)
		return
	}
	resultPackage, err := readServerResultPackage(dir)
	if err != nil {
		a.addStatus("下载服务器产物被阻止：" + err.Error())
		messageBox("Cascade DemoOps", "请先获取服务器结果包。\n\n详情："+err.Error(), true)
		return
	}
	deliverables := deliverablesForDownload(record, resultPackage)
	if len(deliverables) == 0 {
		message := "结果包没有可下载的 deliverable。请确认服务器状态 result_summary.deliverables 或 result delivery asset_refs 是否已返回下载信息。"
		a.addStatus("下载服务器产物被阻止：no deliverables")
		messageBox("Cascade DemoOps", message, true)
		return
	}
	if strings.TrimSpace(a.runtimeConfig.CloudExchangeBaseURL) == "" {
		messageBox("Cascade DemoOps", "服务器地址未配置，无法下载产物。", true)
		return
	}
	if !confirmBox(a.hwnd, "Cascade DemoOps", fmt.Sprintf("确认下载服务器产物并校验 checksum？\n\n将下载 %d 个 deliverable 到 server_deliverables 文件夹。", len(deliverables))) {
		a.addStatus("已取消下载服务器产物。")
		return
	}
	a.mu.Lock()
	a.downloadingArtifacts = true
	a.pendingDownloads = nil
	a.pendingError = ""
	a.mu.Unlock()
	setWindowText(a.downloadBtn, "下载中...")
	setWindowText(a.workflowState, "正在下载服务器产物")
	a.setPhaseText("1 输入完成", "2 理解完成", "3 已审批", "4 下载中")
	a.updateActionStateFromCurrent()
	a.addStatus(fmt.Sprintf("开始下载服务器产物：count=%d result_package_id=%s", len(deliverables), record.ResultPackageID))
	go a.downloadServerArtifactsAsync(record, deliverables)
}

func (a *nativeApp) downloadServerArtifactsAsync(record nativeServerHandoffRecord, deliverables []model.ExecutionDeliverable) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	downloadDir := serverDeliverablesDir(record.OutputDirectory)
	downloads := make([]app.CloudDeliverableDownloadResult, 0, len(deliverables))
	for _, deliverable := range deliverables {
		download, err := a.service.DownloadCloudResultDeliverable(ctx, app.CloudDeliverableDownloadRequest{
			OrgID:             firstNonEmptyNative(record.OrgID, nativeDefaultOrgID),
			ResultPackageID:   strings.TrimSpace(record.ResultPackageID),
			ExchangePackageID: record.ExchangePackageID,
			Deliverable:       deliverable,
			OutputDirectory:   downloadDir,
		})
		if err != nil {
			a.postArtifactDownloadError(err.Error())
			return
		}
		downloads = append(downloads, download)
	}
	manifest := artifactDownloadManifest(record, downloadDir, downloads)
	a.mu.Lock()
	a.pendingDownloads = &nativeArtifactDownloadBatch{Record: record, Manifest: manifest, Downloads: downloads}
	a.mu.Unlock()
	procPostMessageW.Call(uintptr(a.hwnd), wmAppArtifactsDone, 0, 0)
}

func (a *nativeApp) finishArtifactDownloads() {
	a.mu.Lock()
	batch := a.pendingDownloads
	a.pendingDownloads = nil
	a.downloadingArtifacts = false
	a.mu.Unlock()
	setWindowText(a.downloadBtn, "下载产物并校验")
	a.updateActionStateFromCurrent()
	if batch == nil {
		a.finishArtifactDownloadsErrorMessage("服务器产物下载完成但没有得到下载记录。")
		return
	}
	if err := writeArtifactDownloadManifest(batch.Record.OutputDirectory, batch.Manifest); err != nil {
		a.addStatus("服务器产物下载清单保存失败：" + err.Error())
		messageBox("Cascade DemoOps", "服务器产物下载清单保存失败：\n"+err.Error(), true)
		return
	}
	setWindowText(a.workflowState, "服务器产物已下载")
	a.setPhaseText("1 输入完成", "2 理解完成", "3 已审批", "4 产物已下载")
	setWindowText(a.previewSummary, artifactDownloadSummary(batch.Manifest))
	if strings.TrimSpace(batch.Record.OutputDirectory) != "" {
		setWindowText(a.artifactStatus, "服务器产物已下载："+serverDeliverablesDir(batch.Record.OutputDirectory))
		a.setStatusBarOutput(batch.Record.OutputDirectory)
		a.refreshServerHealth(batch.Record.OutputDirectory)
	}
	a.addStatus(fmt.Sprintf("服务器产物已下载：verified=%d/%d", batch.Manifest.VerifiedCount, batch.Manifest.ArtifactCount))
}

func (a *nativeApp) finishArtifactDownloadsError() {
	a.mu.Lock()
	message := a.pendingError
	a.pendingError = ""
	a.downloadingArtifacts = false
	a.mu.Unlock()
	a.finishArtifactDownloadsErrorMessage(message)
}

func (a *nativeApp) finishArtifactDownloadsErrorMessage(message string) {
	setWindowText(a.downloadBtn, "下载产物并校验")
	setWindowText(a.workflowState, "产物下载失败")
	a.setPhaseText("1 输入完成", "2 理解完成", "3 已审批", "4 下载失败")
	a.updateActionStateFromCurrent()
	if strings.TrimSpace(message) == "" {
		message = "服务器产物下载失败。"
	}
	a.addStatus("服务器产物下载失败：" + message)
	messageBox("Cascade DemoOps", message, true)
}

func (a *nativeApp) postArtifactDownloadError(message string) {
	a.mu.Lock()
	a.pendingError = message
	a.mu.Unlock()
	procPostMessageW.Call(uintptr(a.hwnd), wmAppArtifactsFailed, 0, 0)
}

func (a *nativeApp) openDownloadedArtifactsFolder() {
	a.mu.Lock()
	result := a.lastResult
	a.mu.Unlock()
	if result == nil {
		a.addStatus("还没有可打开的服务器产物目录。")
		return
	}
	dir := nativeResultOutputDir(result, a.runtimeConfig.ArtifactRoot)
	if _, err := readArtifactDownloadManifest(dir); err != nil {
		a.addStatus("打开服务器产物目录被阻止：" + err.Error())
		messageBox("Cascade DemoOps", "请先执行“下载产物并校验”。\n\n详情："+err.Error(), true)
		return
	}
	path := serverDeliverablesDir(dir)
	if err := openFolder(path); err != nil {
		a.addStatus("打开服务器产物目录失败：" + err.Error())
		messageBox("Cascade DemoOps", "打开服务器产物目录失败：\n"+err.Error(), true)
		return
	}
	a.addStatus("已打开服务器产物目录：" + path)
}

func (a *nativeApp) openPrimaryDownloadedArtifact() {
	a.mu.Lock()
	result := a.lastResult
	a.mu.Unlock()
	if result == nil {
		a.addStatus("还没有可打开的主产物。")
		return
	}
	dir := nativeResultOutputDir(result, a.runtimeConfig.ArtifactRoot)
	manifest, err := readArtifactDownloadManifest(dir)
	if err != nil {
		a.addStatus("打开主产物被阻止：" + err.Error())
		messageBox("Cascade DemoOps", "请先执行“下载产物并校验”。\n\n详情："+err.Error(), true)
		return
	}
	artifact, ok := primaryDownloadedArtifact(manifest)
	if !ok {
		a.addStatus("没有找到可打开的主产物。")
		messageBox("Cascade DemoOps", "下载清单中没有找到可打开的主产物。", true)
		return
	}
	if err := openFile(artifact.LocalPath); err != nil {
		a.addStatus("打开主产物失败：" + err.Error())
		messageBox("Cascade DemoOps", "打开主产物失败：\n"+err.Error(), true)
		return
	}
	a.addStatus("已打开主产物：" + artifact.LocalPath)
}

func (a *nativeApp) ackServerResult() {
	a.mu.Lock()
	if a.serverBusyLocked() {
		a.mu.Unlock()
		return
	}
	result := a.lastResult
	a.mu.Unlock()
	if result == nil {
		a.addStatus("还没有可 ACK 的服务器结果。")
		return
	}
	dir := nativeResultOutputDir(result, a.runtimeConfig.ArtifactRoot)
	record, err := readServerHandoffRecord(dir)
	if err != nil {
		a.addStatus("服务器 ACK 被阻止：" + err.Error())
		messageBox("Cascade DemoOps", "当前三合一包还没有服务器交接记录。\n\n请先上传并获取结果包。\n\n详情："+err.Error(), true)
		return
	}
	if strings.TrimSpace(record.ResultPackageID) == "" {
		messageBox("Cascade DemoOps", "缺少 result_package_id。请先查询服务器状态并获取结果包。", true)
		return
	}
	received := []string{}
	verifiedByDownload := false
	if manifest, err := readArtifactDownloadManifest(dir); err == nil && len(manifest.Artifacts) > 0 && len(manifest.ChecksumMismatchIDs) == 0 {
		for _, artifact := range manifest.Artifacts {
			if artifact.ChecksumVerified {
				received = append(received, artifact.ArtifactID)
			}
		}
		verifiedByDownload = len(received) > 0
	} else if resultPackage, err := readServerResultPackage(dir); err == nil {
		received = resultReceivedAssetIDs(resultPackage)
	}
	verificationMode := "当前将按服务器返回的结果包和 checksum 元数据确认交付。建议先执行“下载产物并校验”。"
	if verifiedByDownload {
		verificationMode = fmt.Sprintf("已基于本地下载文件完成 checksum 校验：%d 个 artifact。", len(received))
	}
	message := "确认服务器结果交付 ACK？\n\n" + verificationMode + "\n\nResult Package ID:\n" + record.ResultPackageID
	if !confirmBox(a.hwnd, "Cascade DemoOps", message) {
		a.addStatus("已取消服务器结果 ACK。")
		return
	}
	a.mu.Lock()
	a.ackingResult = true
	a.pendingServerAck = nil
	a.pendingError = ""
	a.mu.Unlock()
	setWindowText(a.ackResultBtn, "ACK 中...")
	setWindowText(a.workflowState, "正在确认服务器交付")
	a.setPhaseText("1 输入完成", "2 理解完成", "3 已审批", "4 ACK 中")
	a.updateActionStateFromCurrent()
	a.addStatus("开始确认服务器交付：result_package_id=" + record.ResultPackageID)
	go a.ackServerResultAsync(record, received)
}

func (a *nativeApp) ackServerResultAsync(record nativeServerHandoffRecord, received []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	ack, err := a.service.AckCloudResultPackage(ctx, app.CloudAckRequest{
		OrgID:             firstNonEmptyNative(record.OrgID, nativeDefaultOrgID),
		ResultPackageID:   strings.TrimSpace(record.ResultPackageID),
		ReceivedAssetIDs:  received,
		VerifiedChecksums: true,
		ExchangePackageID: record.ExchangePackageID,
		UseResultSummary:  len(received) == 0,
	})
	if err != nil {
		a.postServerAckError(err.Error())
		return
	}
	a.mu.Lock()
	a.pendingServerAck = &nativeServerAckResult{Record: record, Ack: ack}
	a.mu.Unlock()
	procPostMessageW.Call(uintptr(a.hwnd), wmAppServerAckDone, 0, 0)
}

func (a *nativeApp) finishServerAck() {
	a.mu.Lock()
	result := a.pendingServerAck
	a.pendingServerAck = nil
	a.ackingResult = false
	a.mu.Unlock()
	setWindowText(a.ackResultBtn, "确认交付 ACK")
	a.updateActionStateFromCurrent()
	if result == nil {
		a.finishServerAckErrorMessage("服务器 ACK 完成但没有得到响应。")
		return
	}
	record := updateServerHandoffRecordFromAck(result.Record, result.Ack)
	if strings.TrimSpace(record.OutputDirectory) != "" {
		if err := writeServerHandoffRecord(record.OutputDirectory, record); err != nil {
			a.addStatus("服务器交接记录更新失败：" + err.Error())
		}
		if err := writeServerAckRecord(record.OutputDirectory, serverAckRecordFromResponse(record, result.Ack)); err != nil {
			a.addStatus("服务器 ACK 记录保存失败：" + err.Error())
		}
	}
	setWindowText(a.workflowState, "服务器交付已确认")
	a.setPhaseText("1 输入完成", "2 理解完成", "3 已审批", "4 已 ACK")
	setWindowText(a.previewSummary, serverAckSummary(record, result.Ack))
	if strings.TrimSpace(record.OutputDirectory) != "" {
		a.refreshServerHealth(record.OutputDirectory)
	}
	a.addStatus("服务器交付 ACK 已完成：result_package_id=" + result.Ack.ResultPackageID)
}

func (a *nativeApp) finishServerAckError() {
	a.mu.Lock()
	message := a.pendingError
	a.pendingError = ""
	a.ackingResult = false
	a.mu.Unlock()
	a.finishServerAckErrorMessage(message)
}

func (a *nativeApp) finishServerAckErrorMessage(message string) {
	setWindowText(a.ackResultBtn, "确认交付 ACK")
	setWindowText(a.workflowState, "ACK 失败")
	a.setPhaseText("1 输入完成", "2 理解完成", "3 已审批", "4 ACK 失败")
	a.updateActionStateFromCurrent()
	if strings.TrimSpace(message) == "" {
		message = "服务器交付 ACK 失败。"
	}
	a.addStatus("服务器交付 ACK 失败：" + message)
	messageBox("Cascade DemoOps", message, true)
}

func (a *nativeApp) postServerAckError(message string) {
	a.mu.Lock()
	a.pendingError = message
	a.mu.Unlock()
	procPostMessageW.Call(uintptr(a.hwnd), wmAppServerAckFailed, 0, 0)
}

func writeResultFiles(dir string, result *nativeGenerateResult) error {
	if strings.TrimSpace(dir) == "" {
		return errors.New("output directory is empty")
	}
	if result == nil {
		return errors.New("result is empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for name, content := range resultFiles(result) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content+"\n"), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func readResultFiles(dir string) (*nativeGenerateResult, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, errors.New("package directory is empty")
	}
	markdown, err := readRequiredTextFile(filepath.Join(dir, "approval_markdown.md"))
	if err != nil {
		return nil, err
	}
	stageJSON, err := readRequiredTextFile(filepath.Join(dir, "stage_approval_plan.json"))
	if err != nil {
		return nil, err
	}
	outlineJSON, err := readRequiredTextFile(filepath.Join(dir, "script_outline.json"))
	if err != nil {
		return nil, err
	}
	bundleJSON, err := readRequiredTextFile(filepath.Join(dir, "client_execution_bundle.json"))
	if err != nil {
		return nil, err
	}
	var bundle model.ExecutableRecordingScriptBundle
	if err := json.Unmarshal([]byte(bundleJSON), &bundle); err != nil {
		return nil, fmt.Errorf("client_execution_bundle.json is invalid: %w", err)
	}
	state := &orchestrator.CascadeState{ProjectID: firstNonEmptyNative(bundle.ProjectID, "imported_package")}
	reviewText := nativeReviewText(state, &bundle)
	result := &nativeGenerateResult{
		ProjectID:         state.ProjectID,
		ReviewText:        reviewText,
		Markdown:          strings.TrimSpace(markdown),
		StageJSON:         strings.TrimSpace(stageJSON),
		OutlineJSON:       strings.TrimSpace(outlineJSON),
		BundleJSON:        strings.TrimSpace(bundleJSON),
		OutputDirectory:   dir,
		Summary:           nativeResultSummary(state, &bundle, markdown, stageJSON, outlineJSON, bundleJSON),
		Runtime:           bundle.ScriptManifest.Runtime,
		StageCount:        stageApprovalStageCount(&bundle),
		OutlineStageCount: outlineStageCount(&bundle),
		BundleHashSuffix:  shortHash(bundle.Reproducibility.BundleHashSHA256),
		HealthRuntime:     "Runtime: " + firstNonEmptyNative(bundle.ScriptManifest.Runtime, "unknown"),
		HealthStages:      fmt.Sprintf("Stages: %d / %d", stageApprovalStageCount(&bundle), outlineStageCount(&bundle)),
		HealthBundle:      "Bundle: " + byteSizeLabel(len(bundleJSON)),
		HealthValidation:  validationHealthLabel(bundle.Validation),
	}
	return result, nil
}

func readRequiredTextFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("missing package file: %s", filepath.Base(path))
		}
		return "", err
	}
	if len(data) > maxPackageImportBytes {
		return "", fmt.Errorf("%s is larger than %s", filepath.Base(path), byteSizeLabel(maxPackageImportBytes))
	}
	if strings.TrimSpace(string(data)) == "" {
		return "", fmt.Errorf("%s is empty", filepath.Base(path))
	}
	return string(data), nil
}

func writeApprovalRecord(dir string, record nativeApprovalRecord) error {
	if strings.TrimSpace(dir) == "" {
		return errors.New("approval output directory is empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "approval_record.json"), append(data, '\n'), 0o644)
}

func writeServerHandoffRecord(dir string, record nativeServerHandoffRecord) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return errors.New("server handoff directory is empty")
	}
	if strings.TrimSpace(record.ExchangePackageID) == "" {
		return errors.New("exchange_package_id is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	record.OutputDirectory = dir
	if strings.TrimSpace(record.SchemaVersion) == "" {
		record.SchemaVersion = "demoops.native_server_handoff_record.v1"
	}
	if strings.TrimSpace(record.OrgID) == "" {
		record.OrgID = nativeDefaultOrgID
	}
	if record.UploadedAt.IsZero() {
		record.UploadedAt = time.Now().UTC()
	}
	if strings.TrimSpace(record.ReviewSurface) == "" {
		record.ReviewSurface = "native_win32"
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(serverHandoffRecordPath(dir), append(data, '\n'), 0o644)
}

func readServerHandoffRecord(dir string) (nativeServerHandoffRecord, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nativeServerHandoffRecord{}, errors.New("package directory is empty")
	}
	data, err := os.ReadFile(serverHandoffRecordPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return nativeServerHandoffRecord{}, errors.New("server_handoff_record.json is missing")
	}
	if err != nil {
		return nativeServerHandoffRecord{}, err
	}
	if len(data) > 128*1024 {
		return nativeServerHandoffRecord{}, errors.New("server_handoff_record.json is unexpectedly large")
	}
	var record nativeServerHandoffRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nativeServerHandoffRecord{}, fmt.Errorf("server_handoff_record.json is invalid: %w", err)
	}
	if strings.TrimSpace(record.ExchangePackageID) == "" {
		return nativeServerHandoffRecord{}, errors.New("server_handoff_record.json is missing exchange_package_id")
	}
	if strings.TrimSpace(record.OutputDirectory) == "" {
		record.OutputDirectory = dir
	}
	if strings.TrimSpace(record.OrgID) == "" {
		record.OrgID = nativeDefaultOrgID
	}
	return record, nil
}

func serverHandoffRecordPath(dir string) string {
	return filepath.Join(strings.TrimSpace(dir), "server_handoff_record.json")
}

func writeServerResultPackage(dir string, result model.RecordingResultPackage) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return errors.New("server result directory is empty")
	}
	if strings.TrimSpace(result.ResultID) == "" && strings.TrimSpace(result.SourcePackageID) == "" {
		return errors.New("recording result package identity is missing")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(serverResultPackagePath(dir), append(data, '\n'), 0o644)
}

func readServerResultPackage(dir string) (model.RecordingResultPackage, error) {
	data, err := os.ReadFile(serverResultPackagePath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return model.RecordingResultPackage{}, errors.New("server_result_package.json is missing")
	}
	if err != nil {
		return model.RecordingResultPackage{}, err
	}
	if len(data) > maxPackageImportBytes {
		return model.RecordingResultPackage{}, errors.New("server_result_package.json is unexpectedly large")
	}
	var result model.RecordingResultPackage
	if err := json.Unmarshal(data, &result); err != nil {
		return model.RecordingResultPackage{}, fmt.Errorf("server_result_package.json is invalid: %w", err)
	}
	return result, nil
}

func writeServerResultRecord(dir string, record nativeServerResultRecord) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return errors.New("server result record directory is empty")
	}
	if strings.TrimSpace(record.ResultPackageID) == "" {
		return errors.New("result_package_id is required")
	}
	if strings.TrimSpace(record.SchemaVersion) == "" {
		record.SchemaVersion = "demoops.native_server_result_record.v1"
	}
	record.OutputDirectory = dir
	if record.FetchedAt.IsZero() {
		record.FetchedAt = time.Now().UTC()
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(serverResultRecordPath(dir), append(data, '\n'), 0o644)
}

func writeServerAckRecord(dir string, record nativeServerAckRecord) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return errors.New("server ack record directory is empty")
	}
	if strings.TrimSpace(record.ResultPackageID) == "" {
		return errors.New("result_package_id is required")
	}
	if strings.TrimSpace(record.SchemaVersion) == "" {
		record.SchemaVersion = "demoops.native_server_ack_record.v1"
	}
	record.OutputDirectory = dir
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(serverAckRecordPath(dir), append(data, '\n'), 0o644)
}

func writeArtifactDownloadManifest(dir string, manifest nativeArtifactDownloadManifest) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return errors.New("artifact download manifest directory is empty")
	}
	if strings.TrimSpace(manifest.SchemaVersion) == "" {
		manifest.SchemaVersion = "demoops.native_artifact_download_manifest.v1"
	}
	manifest.OutputDirectory = dir
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(serverDeliverablesDir(dir), 0o755); err != nil {
		return err
	}
	return os.WriteFile(serverDeliverablesManifestPath(dir), append(data, '\n'), 0o644)
}

func readArtifactDownloadManifest(dir string) (nativeArtifactDownloadManifest, error) {
	data, err := os.ReadFile(serverDeliverablesManifestPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return nativeArtifactDownloadManifest{}, errors.New("server_deliverables/manifest.json is missing")
	}
	if err != nil {
		return nativeArtifactDownloadManifest{}, err
	}
	if len(data) > maxPackageImportBytes {
		return nativeArtifactDownloadManifest{}, errors.New("server_deliverables/manifest.json is unexpectedly large")
	}
	var manifest nativeArtifactDownloadManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nativeArtifactDownloadManifest{}, fmt.Errorf("server_deliverables/manifest.json is invalid: %w", err)
	}
	return manifest, nil
}

func serverResultPackagePath(dir string) string {
	return filepath.Join(strings.TrimSpace(dir), "server_result_package.json")
}

func serverResultRecordPath(dir string) string {
	return filepath.Join(strings.TrimSpace(dir), "server_result_record.json")
}

func serverAckRecordPath(dir string) string {
	return filepath.Join(strings.TrimSpace(dir), "server_result_ack.json")
}

func serverDeliverablesDir(dir string) string {
	return filepath.Join(strings.TrimSpace(dir), "server_deliverables")
}

func serverDeliverablesManifestPath(dir string) string {
	return filepath.Join(serverDeliverablesDir(dir), "manifest.json")
}

func resultFiles(result *nativeGenerateResult) map[string]string {
	if result == nil {
		return map[string]string{}
	}
	return map[string]string{
		"approval_markdown.md":         result.Markdown,
		"stage_approval_plan.json":     result.StageJSON,
		"script_outline.json":          result.OutlineJSON,
		"client_execution_bundle.json": result.BundleJSON,
	}
}

func resultFilesExist(dir string) bool {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return false
	}
	for name := range resultFiles(&nativeGenerateResult{}) {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

func previewFileName(kind string) (string, string) {
	switch kind {
	case "stage":
		return "stage_approval_plan.json", "Stage JSON"
	case "outline":
		return "script_outline.json", "Script Outline"
	case "bundle":
		return "client_execution_bundle.json", "Full Bundle"
	default:
		return "approval_markdown.md", "Markdown"
	}
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

func (a *nativeApp) importPackageFolder() {
	dir, err := browseForFolder(a.hwnd, "选择已有三合一包文件夹")
	if err != nil {
		a.addStatus("导入三合一包失败：" + err.Error())
		messageBox("Cascade DemoOps", "导入三合一包失败：\n"+err.Error(), true)
		return
	}
	if strings.TrimSpace(dir) == "" {
		return
	}
	result, err := readResultFiles(dir)
	if err != nil {
		a.addStatus("导入三合一包失败：" + err.Error())
		messageBox("Cascade DemoOps", "导入三合一包失败：\n"+err.Error(), true)
		return
	}
	a.mu.Lock()
	a.lastResult = result
	a.mu.Unlock()
	setWindowText(a.workflowState, "已导入三合一包")
	setWindowText(a.artifactStatus, "已导入："+dir)
	setWindowText(a.previewSummary, result.Summary)
	setWindowText(a.previewContent, result.ReviewText)
	a.currentPreview = "review"
	a.setHealthText(result.HealthRuntime, result.HealthStages, result.HealthBundle, result.HealthValidation)
	a.setPhaseText("1 可保留", "2 已载入", "3 包已就绪", "4 可审核")
	a.setStatusBarOutput(dir)
	a.refreshServerHealth(dir)
	a.rememberRecentPackage(result, dir)
	a.updateActionState(false, true)
	a.addStatus("已导入三合一包：" + dir)
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

func (a *nativeApp) openSelectedRecentPackage() {
	recent, ok := a.selectedRecentPackage()
	if !ok {
		a.addStatus("还没有可打开的最近三合一包。")
		return
	}
	if err := openFolder(recent.OutputDirectory); err != nil {
		a.addStatus("打开最近三合一包失败：" + err.Error())
		messageBox("Cascade DemoOps", "打开最近三合一包失败：\n"+err.Error(), true)
		return
	}
	a.addStatus("已打开最近三合一包：" + recent.OutputDirectory)
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
	case "review":
		setWindowText(a.previewContent, result.ReviewText)
		setWindowText(a.workflowState, "预览审核摘要")
		a.currentPreview = "review"
	case "stage":
		setWindowText(a.previewContent, result.StageJSON)
		setWindowText(a.workflowState, "预览 Stage JSON")
		a.currentPreview = "stage"
	case "outline":
		setWindowText(a.previewContent, result.OutlineJSON)
		setWindowText(a.workflowState, "预览 Script Outline")
		a.currentPreview = "outline"
	case "bundle":
		setWindowText(a.previewContent, result.BundleJSON)
		setWindowText(a.workflowState, "预览 Full Bundle")
		a.currentPreview = "bundle"
	default:
		setWindowText(a.previewContent, result.Markdown)
		setWindowText(a.workflowState, "预览 Markdown")
		a.currentPreview = "markdown"
	}
}

func (a *nativeApp) copyCurrentPreview() {
	a.mu.Lock()
	result := a.lastResult
	kind := a.currentPreview
	a.mu.Unlock()
	if result == nil {
		a.addStatus("还没有可复制的审批材料。")
		return
	}
	content := result.Markdown
	label := "Markdown"
	switch kind {
	case "review":
		content = result.ReviewText
		label = "审核摘要"
	case "stage":
		content = result.StageJSON
		label = "Stage JSON"
	case "outline":
		content = result.OutlineJSON
		label = "Script Outline"
	case "bundle":
		content = result.BundleJSON
		label = "Full Bundle"
	}
	if strings.TrimSpace(content) == "" {
		a.addStatus("当前预览为空，未复制。")
		return
	}
	if err := setClipboardText(a.hwnd, content); err != nil {
		a.addStatus("复制失败：" + err.Error())
		messageBox("Cascade DemoOps", "复制当前预览失败：\n"+err.Error(), true)
		return
	}
	a.addStatus("已复制当前预览：" + label)
}

func (a *nativeApp) openCurrentPreviewFile() {
	a.mu.Lock()
	result := a.lastResult
	kind := a.currentPreview
	a.mu.Unlock()
	if result == nil {
		a.addStatus("还没有可打开的审批文件。")
		return
	}
	fileName, label := previewFileName(kind)
	if fileName == "" {
		fileName, label = previewFileName("markdown")
	}
	dir := strings.TrimSpace(result.OutputDirectory)
	if dir == "" {
		dir = filepath.Join(a.runtimeConfig.ArtifactRoot, result.ProjectID)
	}
	if err := writeResultFiles(dir, result); err != nil {
		a.addStatus("打开当前文件前保存失败：" + err.Error())
		messageBox("Cascade DemoOps", "打开当前预览文件失败：\n"+err.Error(), true)
		return
	}
	path := filepath.Join(dir, fileName)
	if err := openFile(path); err != nil {
		a.addStatus("打开当前文件失败：" + err.Error())
		messageBox("Cascade DemoOps", "打开当前预览文件失败：\n"+err.Error(), true)
		return
	}
	a.addStatus("已打开当前预览文件：" + label)
}

func (a *nativeApp) setPreviewButtonsEnabled(enabled bool) {
	setEnabled(a.viewMarkdownBtn, enabled)
	setEnabled(a.viewStageBtn, enabled)
	setEnabled(a.viewOutlineBtn, enabled)
	setEnabled(a.viewBundleBtn, enabled)
	setEnabled(a.copyPreviewBtn, enabled)
	setEnabled(a.openPreviewBtn, enabled)
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
	input := a.currentNativeInput()
	productURL := strings.TrimSpace(input.ProductURL)
	localRepo := strings.TrimSpace(input.LocalRepoPath)
	gitRepo := strings.TrimSpace(input.GitRepoURL)
	username := strings.TrimSpace(input.DemoUsername)
	password := input.DemoPassword
	requirement := strings.TrimSpace(input.ProductDescription)
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
	a.showInputPreflightIfIdle()
	if !a.suppressDraftSave {
		a.saveInputDraft()
	}
	a.updateActionStateFromCurrent()
}

func (a *nativeApp) currentNativeInput() nativeInput {
	return nativeInput{
		ProductURL:         getWindowText(a.productURL.Edit),
		LocalRepoPath:      getWindowText(a.localRepoPath.Edit),
		GitRepoURL:         getWindowText(a.gitRepoURL.Edit),
		DemoUsername:       getWindowText(a.demoUsername.Edit),
		DemoPassword:       getWindowText(a.demoPassword.Edit),
		ProductDescription: getWindowText(a.requirement.Edit),
	}
}

func (a *nativeApp) showInputPreflightIfIdle() {
	if a.previewSummary == 0 {
		return
	}
	a.mu.Lock()
	generating := a.generating
	uploading := a.uploading
	hasResult := a.lastResult != nil
	a.mu.Unlock()
	if generating || uploading || hasResult {
		return
	}
	input := a.currentNativeInput()
	setWindowText(a.previewSummary, nativeInputPreflightSummary(input))
	if a.previewContent != 0 {
		setWindowText(a.previewContent, a.nativeInputPreflightDetail(input))
		a.currentPreview = "input_preflight"
	}
}

func (a *nativeApp) clearInputDraft() {
	if !confirmBox(a.hwnd, "Cascade DemoOps", "清除当前输入草稿？\n\n会清空 URL、代码来源、需求文本和窗口中的临时账号密码；不会删除已生成的执行包。") {
		a.addStatus("已取消清除输入草稿。")
		return
	}
	a.suppressDraftSave = true
	setWindowText(a.productURL.Edit, "https://cascadeai.cn")
	setWindowText(a.localRepoPath.Edit, "")
	setWindowText(a.gitRepoURL.Edit, "")
	setWindowText(a.demoUsername.Edit, "")
	setWindowText(a.demoPassword.Edit, "")
	setWindowText(a.requirement.Edit, "")
	a.suppressDraftSave = false
	path := a.inputDraftPath()
	if strings.TrimSpace(path) != "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			a.addStatus("清除输入草稿失败：" + err.Error())
			messageBox("Cascade DemoOps", "清除输入草稿失败：\n"+err.Error(), true)
			a.updateInputReadiness()
			return
		}
	}
	a.suppressDraftSave = true
	a.updateInputReadiness()
	a.suppressDraftSave = false
	a.addStatus("已清除输入草稿。")
}

func (a *nativeApp) restoreInputDraft() {
	draft, err := a.readInputDraft()
	if err != nil {
		if a.logger != nil {
			a.logger.Printf("native: input draft restore skipped: %v", err)
		}
		return
	}
	if strings.TrimSpace(draft.ProductURL) != "" {
		setWindowText(a.productURL.Edit, draft.ProductURL)
	}
	if strings.TrimSpace(draft.LocalRepoPath) != "" {
		setWindowText(a.localRepoPath.Edit, draft.LocalRepoPath)
	}
	if strings.TrimSpace(draft.GitRepoURL) != "" {
		setWindowText(a.gitRepoURL.Edit, draft.GitRepoURL)
	}
	if strings.TrimSpace(draft.ProductDescription) != "" {
		setWindowText(a.requirement.Edit, draft.ProductDescription)
	}
}

func (a *nativeApp) saveInputDraft() {
	if strings.TrimSpace(a.runtimeConfig.DataRoot) == "" {
		return
	}
	draft := nativeInputDraft{
		ProductURL:         strings.TrimSpace(getWindowText(a.productURL.Edit)),
		LocalRepoPath:      strings.TrimSpace(getWindowText(a.localRepoPath.Edit)),
		GitRepoURL:         strings.TrimSpace(getWindowText(a.gitRepoURL.Edit)),
		ProductDescription: strings.TrimSpace(getWindowText(a.requirement.Edit)),
		UpdatedAt:          time.Now().UTC(),
	}
	if strings.TrimSpace(draft.ProductURL+draft.LocalRepoPath+draft.GitRepoURL+draft.ProductDescription) == "" {
		return
	}
	data, err := json.MarshalIndent(draft, "", "  ")
	if err != nil {
		if a.logger != nil {
			a.logger.Printf("native: input draft marshal failed: %v", err)
		}
		return
	}
	path := a.inputDraftPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		if a.logger != nil {
			a.logger.Printf("native: input draft directory failed: %v", err)
		}
		return
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		if a.logger != nil {
			a.logger.Printf("native: input draft save failed: %v", err)
		}
	}
}

func (a *nativeApp) readInputDraft() (nativeInputDraft, error) {
	path := a.inputDraftPath()
	if strings.TrimSpace(path) == "" {
		return nativeInputDraft{}, errors.New("draft path is empty")
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nativeInputDraft{}, nil
	}
	if err != nil {
		return nativeInputDraft{}, err
	}
	var draft nativeInputDraft
	if err := json.Unmarshal(data, &draft); err != nil {
		return nativeInputDraft{}, err
	}
	return draft, nil
}

func (a *nativeApp) inputDraftPath() string {
	if strings.TrimSpace(a.runtimeConfig.DataRoot) == "" {
		return ""
	}
	return filepath.Join(a.runtimeConfig.DataRoot, "desktop-input-draft.json")
}

func (a *nativeApp) rememberRecentPackage(result *nativeGenerateResult, outputDir string) {
	if result == nil || strings.TrimSpace(outputDir) == "" {
		return
	}
	entry := nativeRecentPackage{
		ProjectID:       strings.TrimSpace(result.ProjectID),
		OutputDirectory: strings.TrimSpace(outputDir),
		Runtime:         strings.TrimSpace(result.Runtime),
		StageCount:      result.StageCount,
		UpdatedAt:       time.Now().UTC(),
	}
	a.mu.Lock()
	recent := make([]nativeRecentPackage, 0, len(a.recentPackages)+1)
	recent = append(recent, entry)
	seen := map[string]bool{strings.ToLower(entry.OutputDirectory): true}
	for _, item := range a.recentPackages {
		key := strings.ToLower(strings.TrimSpace(item.OutputDirectory))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		recent = append(recent, item)
		if len(recent) >= maxRecentPackages {
			break
		}
	}
	a.recentPackages = recent
	a.mu.Unlock()
	a.refreshRecentPackageList()
	a.saveRecentPackages()
}

func (a *nativeApp) loadRecentPackages() {
	path := a.recentPackagesPath()
	if strings.TrimSpace(path) == "" {
		return
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		if a.logger != nil {
			a.logger.Printf("native: recent package load failed: %v", err)
		}
		return
	}
	var recent []nativeRecentPackage
	if err := json.Unmarshal(data, &recent); err != nil {
		if a.logger != nil {
			a.logger.Printf("native: recent package parse failed: %v", err)
		}
		return
	}
	a.mu.Lock()
	a.recentPackages = compactRecentPackages(recent)
	a.mu.Unlock()
}

func (a *nativeApp) saveRecentPackages() {
	path := a.recentPackagesPath()
	if strings.TrimSpace(path) == "" {
		return
	}
	a.mu.Lock()
	recent := append([]nativeRecentPackage(nil), a.recentPackages...)
	a.mu.Unlock()
	data, err := json.MarshalIndent(recent, "", "  ")
	if err != nil {
		if a.logger != nil {
			a.logger.Printf("native: recent package marshal failed: %v", err)
		}
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		if a.logger != nil {
			a.logger.Printf("native: recent package directory failed: %v", err)
		}
		return
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil && a.logger != nil {
		a.logger.Printf("native: recent package save failed: %v", err)
	}
}

func (a *nativeApp) refreshRecentPackageList() {
	if a.recentPackage == 0 {
		return
	}
	a.mu.Lock()
	recent := append([]nativeRecentPackage(nil), a.recentPackages...)
	a.mu.Unlock()
	procSendMessageW.Call(uintptr(a.recentPackage), cbResetContent, 0, 0)
	for _, item := range recent {
		label := recentPackageLabel(item)
		procSendMessageW.Call(uintptr(a.recentPackage), cbAddString, 0, uintptr(unsafe.Pointer(utf16Ptr(label))))
	}
	if len(recent) > 0 {
		procSendMessageW.Call(uintptr(a.recentPackage), cbSetCurSel, 0, 0)
	}
	setEnabled(a.openRecentBtn, len(recent) > 0)
	a.enableMenuItem(idOpenRecentPackage, len(recent) > 0)
}

func (a *nativeApp) selectedRecentPackage() (nativeRecentPackage, bool) {
	a.mu.Lock()
	recent := append([]nativeRecentPackage(nil), a.recentPackages...)
	a.mu.Unlock()
	if len(recent) == 0 {
		return nativeRecentPackage{}, false
	}
	index, _, _ := procSendMessageW.Call(uintptr(a.recentPackage), cbGetCurSel, 0, 0)
	if index == cbErr || int(index) < 0 || int(index) >= len(recent) {
		return recent[0], true
	}
	return recent[int(index)], true
}

func (a *nativeApp) showSelectedRecentPackageSummary() {
	if a.previewSummary == 0 {
		return
	}
	a.mu.Lock()
	generating := a.generating
	uploading := a.uploading
	hasResult := a.lastResult != nil
	a.mu.Unlock()
	if generating || uploading {
		return
	}
	recent, ok := a.selectedRecentPackage()
	if !ok {
		return
	}
	lines := []string{
		"最近三合一包",
		"Project ID: " + firstNonEmptyNative(recent.ProjectID, "unknown"),
		"Runtime: " + firstNonEmptyNative(recent.Runtime, "unknown"),
		fmt.Sprintf("Stages: %d", recent.StageCount),
		"Updated: " + nativeTimeLabel(recent.UpdatedAt),
		"Path: " + compactPath(recent.OutputDirectory, 110),
		"Files: " + recentPackageFilesLabel(recent.OutputDirectory),
		"Approval: " + approvalRecordLabel(recent.OutputDirectory),
		"Server handoff: " + serverHandoffLabel(recent.OutputDirectory),
		"Delivery: " + deliveryHealthLine(recent.OutputDirectory),
	}
	setWindowText(a.previewSummary, strings.Join(lines, "\r\n"))
	if !hasResult && a.previewContent != 0 {
		setWindowText(a.previewContent, "选择“打开最近包”会加载该文件夹中的 Markdown、Stage JSON、Script Outline 和 Bundle；选择“打开输出目录”仅打开当前已生成或已导入包。")
		a.currentPreview = "recent_summary"
	}
}

func (a *nativeApp) recentPackagesPath() string {
	if strings.TrimSpace(a.runtimeConfig.DataRoot) == "" {
		return ""
	}
	return filepath.Join(a.runtimeConfig.DataRoot, "desktop-recent-packages.json")
}

func compactRecentPackages(input []nativeRecentPackage) []nativeRecentPackage {
	out := make([]nativeRecentPackage, 0, len(input))
	seen := map[string]bool{}
	for _, item := range input {
		item.OutputDirectory = strings.TrimSpace(item.OutputDirectory)
		if item.OutputDirectory == "" {
			continue
		}
		key := strings.ToLower(item.OutputDirectory)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
		if len(out) >= maxRecentPackages {
			break
		}
	}
	return out
}

func recentPackageLabel(item nativeRecentPackage) string {
	name := strings.TrimSpace(item.ProjectID)
	if name == "" {
		name = filepath.Base(item.OutputDirectory)
	}
	date := ""
	if !item.UpdatedAt.IsZero() {
		date = item.UpdatedAt.Local().Format("01-02 15:04")
	}
	meta := strings.TrimSpace(strings.Join([]string{date, strings.TrimSpace(item.Runtime)}, " "))
	if item.StageCount > 0 {
		meta = strings.TrimSpace(fmt.Sprintf("%s %d stages", meta, item.StageCount))
	}
	path := compactPath(item.OutputDirectory, 56)
	if meta == "" {
		return fmt.Sprintf("%s  -  %s", name, path)
	}
	return fmt.Sprintf("%s  -  %s  -  %s", name, meta, path)
}

func recentPackageFilesLabel(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "missing path"
	}
	required := []string{"approval_markdown.md", "stage_approval_plan.json", "script_outline.json", "client_execution_bundle.json"}
	missing := 0
	for _, name := range required {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			missing++
		}
	}
	if missing == 0 {
		return "ready"
	}
	return fmt.Sprintf("missing %d/%d", missing, len(required))
}

func approvalRecordLabel(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "missing path"
	}
	if _, err := os.Stat(filepath.Join(dir, "approval_record.json")); err == nil {
		return "approved locally"
	}
	return "required before server upload"
}

func serverHandoffLabel(dir string) string {
	record, err := readServerHandoffRecord(dir)
	if err == nil {
		status := firstNonEmptyNative(record.Status, "uploaded")
		stage := strings.TrimSpace(record.Stage)
		if stage != "" {
			status += "/" + stage
		}
		if strings.TrimSpace(record.ExchangePackageID) != "" {
			status += " (" + compactPath(record.ExchangePackageID, 28) + ")"
		}
		return status
	}
	return serverUploadReadinessLabel(dir)
}

func serverHealthLabel(dir string) string {
	record, err := readServerHandoffRecord(dir)
	if err != nil {
		return "Server: not uploaded"
	}
	status := firstNonEmptyNative(record.Status, "uploaded")
	if strings.TrimSpace(record.Stage) != "" {
		status += "/" + record.Stage
	}
	return "Server: " + compactPath(status, 34)
}

func resultHealthLabel(dir string) string {
	result, err := readServerResultPackage(dir)
	if err != nil {
		if record, recordErr := readServerHandoffRecord(dir); recordErr == nil && strings.TrimSpace(record.ResultPackageID) != "" {
			return "Result: ready"
		}
		return "Result: not fetched"
	}
	status := firstNonEmptyNative(string(result.Status), "fetched")
	return "Result: " + compactPath(status, 34)
}

func artifactHealthLabel(dir string) string {
	manifest, err := readArtifactDownloadManifest(dir)
	if err != nil {
		return "Artifacts: not downloaded"
	}
	label := fmt.Sprintf("%d/%d verified", manifest.VerifiedCount, manifest.ArtifactCount)
	if len(manifest.ChecksumMismatchIDs) > 0 {
		label = fmt.Sprintf("%d mismatch", len(manifest.ChecksumMismatchIDs))
	}
	return "Artifacts: " + label
}

func ackHealthLabel(dir string) string {
	data, err := os.ReadFile(serverAckRecordPath(dir))
	if err != nil {
		if result, resultErr := readServerResultPackage(dir); resultErr == nil && !result.Delivery.AckedAt.IsZero() {
			return "Ack: " + nativeTimeLabel(result.Delivery.AckedAt)
		}
		return "Ack: pending"
	}
	var record nativeServerAckRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return "Ack: invalid record"
	}
	status := firstNonEmptyNative(record.DeliveryStatus, record.Status, "acked")
	if !record.AckedAt.IsZero() {
		status += " " + record.AckedAt.Local().Format("15:04")
	}
	return "Ack: " + compactPath(status, 34)
}

func deliveryHealthLine(dir string) string {
	return strings.Join([]string{
		strings.TrimPrefix(serverHealthLabel(dir), "Server: "),
		strings.TrimPrefix(resultHealthLabel(dir), "Result: "),
		strings.TrimPrefix(artifactHealthLabel(dir), "Artifacts: "),
		strings.TrimPrefix(ackHealthLabel(dir), "Ack: "),
	}, " | ")
}

func serverUploadReadinessLabel(dir string) string {
	if recentPackageFilesLabel(dir) != "ready" {
		return "blocked until package files are complete"
	}
	if approvalRecordLabel(dir) != "approved locally" {
		return "waiting for local approval"
	}
	return "ready for server Browser Agent"
}

func handoffRecordLabel(dir string) string {
	if strings.TrimSpace(dir) == "" {
		return "not recorded"
	}
	if _, err := os.Stat(serverHandoffRecordPath(dir)); err == nil {
		return filepath.Base(serverHandoffRecordPath(dir))
	}
	return "not recorded"
}

func nativeTimeLabel(value time.Time) string {
	if value.IsZero() {
		return "unknown"
	}
	return value.Local().Format("2006-01-02 15:04")
}

func (a *nativeApp) updateActionState(generating bool, hasResult bool) {
	a.mu.Lock()
	uploading := a.uploading
	queryingStatus := a.queryingStatus
	fetchingResult := a.fetchingResult
	ackingResult := a.ackingResult
	downloadingArtifacts := a.downloadingArtifacts
	hasRecent := len(a.recentPackages) > 0
	a.mu.Unlock()
	busy := generating || uploading || queryingStatus || fetchingResult || ackingResult || downloadingArtifacts
	generateEnabled := !busy && a.inputReady()
	recentEnabled := !busy && hasRecent
	setEnabled(a.generateBtn, generateEnabled)
	setEnabled(a.saveBtn, !busy && hasResult)
	setEnabled(a.browseRepoBtn, !busy)
	setEnabled(a.importReqBtn, !busy)
	setEnabled(a.importPackageBtn, !busy)
	setEnabled(a.clearDraftBtn, !busy)
	setEnabled(a.exportBtn, !busy && hasResult)
	setEnabled(a.approveBtn, !busy && hasResult)
	setEnabled(a.uploadBtn, !busy && hasResult)
	setEnabled(a.queryStatusBtn, !busy && hasResult)
	setEnabled(a.fetchResultBtn, !busy && hasResult)
	setEnabled(a.downloadBtn, !busy && hasResult)
	setEnabled(a.openAssetsBtn, !busy && hasResult)
	setEnabled(a.openPrimaryBtn, !busy && hasResult)
	setEnabled(a.ackResultBtn, !busy && hasResult)
	setEnabled(a.openOutputBtn, !busy && hasResult)
	setEnabled(a.openRecentBtn, recentEnabled)
	setEnabled(a.openLogBtn, true)
	a.setPreviewButtonsEnabled(!busy && hasResult)
	a.enableMenuItem(idGenerateButton, generateEnabled)
	a.enableMenuItem(idBrowseRepo, !busy)
	a.enableMenuItem(idImportRequirement, !busy)
	a.enableMenuItem(idImportPackage, !busy)
	a.enableMenuItem(idClearDraft, !busy)
	for _, id := range []int{idSaveButton, idExportPackage, idApprovePackage, idUploadApproved, idQueryServerStatus, idFetchServerResult, idDownloadArtifacts, idOpenDeliverables, idOpenPrimaryAsset, idAckServerResult, idOpenOutput, idViewMarkdown, idViewStageJSON, idViewOutline, idViewBundle, idOpenPreviewFile} {
		a.enableMenuItem(id, !busy && hasResult)
	}
	a.enableMenuItem(idCopyPreview, !busy && hasResult)
	a.enableMenuItem(idOpenRecentPackage, recentEnabled)
	a.enableMenuItem(idOpenLog, true)
}

func (a *nativeApp) updateActionStateFromCurrent() {
	a.mu.Lock()
	generating := a.generating
	hasResult := a.lastResult != nil
	a.mu.Unlock()
	a.updateActionState(generating, hasResult)
}

func (a *nativeApp) serverBusyLocked() bool {
	return a.generating || a.uploading || a.queryingStatus || a.fetchingResult || a.ackingResult || a.downloadingArtifacts
}

func nativeResultOutputDir(result *nativeGenerateResult, artifactRoot string) string {
	if result == nil {
		return ""
	}
	dir := strings.TrimSpace(result.OutputDirectory)
	if dir == "" && strings.TrimSpace(result.ProjectID) != "" {
		dir = filepath.Join(artifactRoot, result.ProjectID)
	}
	return dir
}

func (a *nativeApp) inputReady() bool {
	return strings.TrimSpace(getWindowText(a.productURL.Edit)) != "" &&
		strings.TrimSpace(getWindowText(a.requirement.Edit)) != ""
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

func (a *nativeApp) setServerHealthText(server string, result string, artifacts string, ack string) {
	setWindowText(a.healthServer, server)
	setWindowText(a.healthResult, result)
	setWindowText(a.healthArtifacts, artifacts)
	setWindowText(a.healthAck, ack)
}

func (a *nativeApp) refreshServerHealth(dir string) {
	a.setServerHealthText(serverHealthLabel(dir), resultHealthLabel(dir), artifactHealthLabel(dir), ackHealthLabel(dir))
}

func (a *nativeApp) setStatusBarOutput(outputDir string) {
	setWindowText(a.statusBar, a.statusBarText(outputDir))
}

func (a *nativeApp) statusBarText(outputDir string) string {
	parts := []string{
		"Native Win32",
		a.cloudConnectionLabel(),
		"data: " + compactPath(a.runtimeConfig.DataRoot, 42),
	}
	if a.logger != nil && strings.TrimSpace(a.logger.Path()) != "" {
		parts = append(parts, "log: "+compactPath(a.logger.Path(), 42))
	}
	if strings.TrimSpace(outputDir) != "" {
		parts = append(parts, "output: "+compactPath(outputDir, 48))
	}
	return strings.Join(parts, "  |  ")
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
	return fmt.Sprintf("引擎：%s · %s · %s", a.runtimeConfig.Profile, sidecar, a.cloudConnectionLabel())
}

func (a *nativeApp) cloudConnectionLabel() string {
	base := strings.TrimSpace(a.runtimeConfig.CloudExchangeBaseURL)
	if base == "" {
		return "Server: not configured"
	}
	host := base
	path := ""
	if parsed, err := url.Parse(base); err == nil {
		if parsed.Host != "" {
			host = parsed.Host
		}
		path = strings.TrimSpace(parsed.Path)
	}
	label := "Server: " + host
	if path != "" && path != "/" {
		label += path
	}
	if strings.TrimSpace(a.runtimeConfig.CloudExchangeToken) != "" {
		label += " (legacy token compatible)"
	} else {
		label += " (installation session)"
	}
	return label
}

func nativeResultSummary(state *orchestrator.CascadeState, bundle *model.ExecutableRecordingScriptBundle, markdown string, stageJSON string, outlineJSON string, bundleJSON string) string {
	if state == nil || bundle == nil {
		return "生成结果不可用。"
	}
	stageCount := stageApprovalStageCount(bundle)
	outlineCount := outlineStageCount(bundle)
	lines := []string{
		"Project ID: " + state.ProjectID,
		"Runtime: " + firstNonEmptyNative(bundle.ScriptManifest.Runtime, "unknown"),
		fmt.Sprintf("Stages: approval=%d outline=%d (%s)", stageCount, outlineCount, stageCoverageLabel(stageCount, outlineCount)),
		fmt.Sprintf("Payload size: markdown=%s stage=%s outline=%s bundle=%s", byteSizeLabel(len(markdown)), byteSizeLabel(len(stageJSON)), byteSizeLabel(len(outlineJSON)), byteSizeLabel(len(bundleJSON))),
		"Package gate: " + packageSizeGateLabel(len(bundleJSON)),
		"Upload boundary: local approval required; credentials stay as secret_ref only.",
	}
	if suffix := shortHash(bundle.Reproducibility.BundleHashSHA256); suffix != "" {
		lines = append(lines, "Bundle hash: ..."+suffix)
	}
	if bundle.Validation != nil {
		lines = append(lines, fmt.Sprintf("Validation: valid=%t findings=%d", bundle.Validation.Valid, len(bundle.Validation.Findings)))
	}
	return strings.Join(lines, "\r\n")
}

func nativeResultSummaryForDirectory(result *nativeGenerateResult, dir string) string {
	if result == nil {
		return "生成结果不可用。"
	}
	lines := []string{
		"Project ID: " + firstNonEmptyNative(result.ProjectID, "unknown"),
		"Runtime: " + firstNonEmptyNative(result.Runtime, "unknown"),
		fmt.Sprintf("Stages: approval=%d outline=%d (%s)", result.StageCount, result.OutlineStageCount, stageCoverageLabel(result.StageCount, result.OutlineStageCount)),
		fmt.Sprintf("Payload size: markdown=%s stage=%s outline=%s bundle=%s", byteSizeLabel(len(result.Markdown)), byteSizeLabel(len(result.StageJSON)), byteSizeLabel(len(result.OutlineJSON)), byteSizeLabel(len(result.BundleJSON))),
		"Package gate: " + packageSizeGateLabel(len(result.BundleJSON)),
		"Files: " + recentPackageFilesLabel(dir),
		"Approval: " + approvalRecordLabel(dir),
		"Server handoff: " + serverHandoffLabel(dir),
	}
	if suffix := strings.TrimSpace(result.BundleHashSuffix); suffix != "" {
		lines = append(lines, "Bundle hash: ..."+suffix)
	}
	return strings.Join(lines, "\r\n")
}

func nativeInputPreflightSummary(input nativeInput) string {
	productURL := strings.TrimSpace(input.ProductURL)
	requirement := strings.TrimSpace(input.ProductDescription)
	localRepo := strings.TrimSpace(input.LocalRepoPath)
	gitRepo := strings.TrimSpace(input.GitRepoURL)
	username := strings.TrimSpace(input.DemoUsername)
	password := input.DemoPassword
	lines := []string{
		"输入预检",
		"Product URL: " + readyLabel(productURL != ""),
		"Requirement: " + readySizeLabel(requirement),
		"Code sources: " + codeSourceLabel(localRepo, gitRepo),
		"Credentials: " + credentialInputLabel(username, password),
		"Generate gate: " + generateGateLabel(productURL, requirement),
	}
	return strings.Join(lines, "\r\n")
}

func (a *nativeApp) nativeInputPreflightDetail(input nativeInput) string {
	productURL := strings.TrimSpace(input.ProductURL)
	requirement := strings.TrimSpace(input.ProductDescription)
	localRepo := strings.TrimSpace(input.LocalRepoPath)
	gitRepo := strings.TrimSpace(input.GitRepoURL)
	username := strings.TrimSpace(input.DemoUsername)
	password := input.DemoPassword
	lines := []string{
		"本地生成预检",
		"",
		"必填材料",
		"- 产品 URL: " + valueOrMissing(productURL),
		"- 需求文本: " + readySizeLabel(requirement),
		"",
		"可选代码来源",
		"- 本地项目路径: " + valueOrMissing(localRepo),
		"- GitHub 仓库 URL: " + valueOrMissing(gitRepo),
		"- 关系: 本地路径和 GitHub URL 是并列可选输入，可以同时提供；都不填写时会按需求和页面材料降级生成。",
		"",
		"临时凭据",
		"- 状态: " + credentialInputLabel(username, password),
		"- 边界: 密码不会保存到草稿、审批文档、三合一包或云端 payload。",
		"",
		"生成门禁",
		"- " + generateGateLabel(productURL, requirement),
		"- 生成后请先审核 Markdown、Stage JSON 和 Script Outline，再本地审批或交给服务器 Browser Agent。",
		"",
		"服务器执行",
		"- " + a.cloudConnectionLabel(),
		"- 本地生成不依赖服务器；上传/录制阶段需要服务器连接和本地 approval_record.json。",
	}
	return strings.Join(lines, "\r\n")
}

func nativeReviewText(state *orchestrator.CascadeState, bundle *model.ExecutableRecordingScriptBundle) string {
	if state == nil || bundle == nil {
		return "审核摘要不可用。"
	}
	lines := []string{
		"审核摘要",
		"",
		"Project ID: " + state.ProjectID,
		"Runtime: " + firstNonEmptyNative(bundle.ScriptManifest.Runtime, "unknown"),
		fmt.Sprintf("Stage coverage: approval=%d outline=%d", stageApprovalStageCount(bundle), outlineStageCount(bundle)),
		"Bundle hash: ..." + firstNonEmptyNative(shortHash(bundle.Reproducibility.BundleHashSHA256), "missing"),
		"Validation: " + strings.TrimPrefix(validationHealthLabel(bundle.Validation), "Validation: "),
	}
	if bundle.StageApprovalPlan != nil {
		lines = append(lines, "", "Stage Plan")
		for i, stage := range bundle.StageApprovalPlan.Stages {
			if i >= 12 {
				lines = append(lines, fmt.Sprintf("- ...and %d more stages", len(bundle.StageApprovalPlan.Stages)-i))
				break
			}
			title := firstNonEmptyNative(stage.Title, stage.Objective, stage.ID)
			route := firstNonEmptyNative(stage.TargetRoute, stage.EntryRoute, stage.ExpectedRouteAfterAction, stage.TargetURL)
			if route == "" && len(stage.CandidateRoutes) > 0 {
				route = stage.CandidateRoutes[0].Route
			}
			action := string(stage.Interaction.Kind)
			success := strings.TrimSpace(stage.SuccessState)
			line := fmt.Sprintf("- %02d %s", stage.Order, title)
			if stage.StageKind != "" {
				line += " [" + string(stage.StageKind) + "]"
			}
			if route != "" {
				line += " route=" + route
			}
			if action != "" {
				line += " action=" + action
			}
			if success != "" {
				line += " success=" + compactPath(success, 96)
			}
			lines = append(lines, line)
		}
		if len(bundle.StageApprovalPlan.UncertaintyReport) > 0 {
			lines = append(lines, "", "Uncertainty")
			for i, item := range bundle.StageApprovalPlan.UncertaintyReport {
				if i >= 6 {
					lines = append(lines, fmt.Sprintf("- ...and %d more uncertainty items", len(bundle.StageApprovalPlan.UncertaintyReport)-i))
					break
				}
				lines = append(lines, "- "+firstNonEmptyNative(item.Summary, item.SuggestedAction, item.Kind, item.ID))
			}
		}
	}
	if bundle.ScriptOutline != nil {
		lines = append(lines, "", "Browser Agent Boundary")
		if len(bundle.ScriptOutline.AllowedExplorationScope.AllowedOrigins) > 0 {
			lines = append(lines, "- allowed origins: "+strings.Join(bundle.ScriptOutline.AllowedExplorationScope.AllowedOrigins, ", "))
		}
		if len(bundle.ScriptOutline.AllowedExplorationScope.ForbiddenPathPrefixes) > 0 {
			lines = append(lines, "- forbidden paths: "+strings.Join(bundle.ScriptOutline.AllowedExplorationScope.ForbiddenPathPrefixes, ", "))
		}
		if len(bundle.ScriptOutline.ImmutableFields) > 0 {
			lines = append(lines, "- immutable: "+strings.Join(bundle.ScriptOutline.ImmutableFields, ", "))
		}
		if len(bundle.ScriptOutline.ServerEditableFields) > 0 {
			lines = append(lines, "- server editable: "+strings.Join(bundle.ScriptOutline.ServerEditableFields, ", "))
		}
	}
	if bundle.Validation != nil && len(bundle.Validation.Findings) > 0 {
		lines = append(lines, "", "Validation Findings")
		for i, finding := range bundle.Validation.Findings {
			if i >= 8 {
				lines = append(lines, fmt.Sprintf("- ...and %d more findings", len(bundle.Validation.Findings)-i))
				break
			}
			summary := firstNonEmptyNative(finding.Summary, finding.Title, finding.ID)
			lines = append(lines, fmt.Sprintf("- %s %s: %s", finding.Severity, firstNonEmptyNative(finding.Kind, finding.ID), compactPath(summary, 140)))
		}
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

func stageCoverageLabel(stageCount int, outlineCount int) string {
	switch {
	case stageCount > 0 && stageCount == outlineCount:
		return "aligned"
	case stageCount == 0 && outlineCount == 0:
		return "missing"
	default:
		return "mismatch"
	}
}

func packageSizeGateLabel(size int) string {
	switch {
	case size <= 0:
		return "missing bundle"
	case size <= 200*1024:
		return "compact (" + byteSizeLabel(size) + ")"
	case size <= 512*1024:
		return "review size (" + byteSizeLabel(size) + ")"
	default:
		return "too large for normal outline handoff (" + byteSizeLabel(size) + ")"
	}
}

func readyLabel(ok bool) string {
	if ok {
		return "ready"
	}
	return "missing"
}

func readySizeLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "missing"
	}
	return byteSizeLabel(len(value))
}

func codeSourceLabel(localRepo string, gitRepo string) string {
	switch {
	case strings.TrimSpace(localRepo) != "" && strings.TrimSpace(gitRepo) != "":
		return "local + GitHub"
	case strings.TrimSpace(localRepo) != "":
		return "local"
	case strings.TrimSpace(gitRepo) != "":
		return "GitHub"
	default:
		return "optional"
	}
}

func credentialInputLabel(username string, password string) string {
	username = strings.TrimSpace(username)
	switch {
	case username != "" && password != "":
		return "provided locally"
	case username != "" || password != "":
		return "incomplete"
	default:
		return "optional"
	}
}

func generateGateLabel(productURL string, requirement string) string {
	if strings.TrimSpace(productURL) != "" && strings.TrimSpace(requirement) != "" {
		return "ready to generate"
	}
	return "waiting for product URL and requirement"
}

func valueOrMissing(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "(missing)"
	}
	return value
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

func updateServerHandoffRecordFromStatus(record nativeServerHandoffRecord, status model.ExecutionPackageStatusResponse) nativeServerHandoffRecord {
	record.SchemaVersion = firstNonEmptyNative(record.SchemaVersion, "demoops.native_server_handoff_record.v1")
	record.ExchangePackageID = firstNonEmptyNative(status.ExchangePackageID, record.ExchangePackageID)
	record.CloudJobID = firstNonEmptyNative(status.CloudJobID, record.CloudJobID)
	record.Status = firstNonEmptyNative(string(status.Status), record.Status)
	record.Stage = strings.TrimSpace(status.Stage)
	record.Message = strings.TrimSpace(status.Message)
	record.ProgressPercent = status.ProgressPercent
	record.ResultPackageID = strings.TrimSpace(status.ResultPackageID)
	record.LastStatusAt = time.Now().UTC()
	if record.UploadedAt.IsZero() {
		record.UploadedAt = record.LastStatusAt
	}
	if strings.TrimSpace(record.OrgID) == "" {
		record.OrgID = nativeDefaultOrgID
	}
	if strings.TrimSpace(record.ReviewSurface) == "" {
		record.ReviewSurface = "native_win32"
	}
	return record
}

func updateServerHandoffRecordFromResult(record nativeServerHandoffRecord, result model.RecordingResultPackage) nativeServerHandoffRecord {
	record.ResultPackageID = firstNonEmptyNative(record.ResultPackageID, result.ResultID, result.Delivery.ResultPackageRef.ID)
	record.CloudJobID = firstNonEmptyNative(result.CloudJobID, record.CloudJobID)
	record.Status = firstNonEmptyNative(string(result.Status), record.Status)
	record.Stage = firstNonEmptyNative(record.Stage, "result_package_fetched")
	record.Message = "RecordingResultPackage fetched by native desktop"
	record.LastStatusAt = time.Now().UTC()
	if strings.TrimSpace(record.ProjectID) == "" {
		record.ProjectID = strings.TrimSpace(result.SourcePackageID)
	}
	return record
}

func updateServerHandoffRecordFromAck(record nativeServerHandoffRecord, ack model.ResultPackageAckResponse) nativeServerHandoffRecord {
	record.ResultPackageID = firstNonEmptyNative(ack.ResultPackageID, record.ResultPackageID)
	record.Status = firstNonEmptyNative(string(ack.Status), record.Status)
	record.Stage = "result_acknowledged"
	record.Message = "Recording result delivery acknowledged by native desktop"
	record.LastStatusAt = time.Now().UTC()
	return record
}

func serverResultRecordFromPackage(record nativeServerHandoffRecord, result model.RecordingResultPackage) nativeServerResultRecord {
	demoVideos, screenshots, traces := resultAssetCounts(result)
	return nativeServerResultRecord{
		SchemaVersion:     "demoops.native_server_result_record.v1",
		ProjectID:         record.ProjectID,
		OrgID:             firstNonEmptyNative(record.OrgID, nativeDefaultOrgID),
		ExchangePackageID: record.ExchangePackageID,
		ResultPackageID:   firstNonEmptyNative(record.ResultPackageID, result.ResultID, result.Delivery.ResultPackageRef.ID),
		ResultID:          result.ResultID,
		Status:            string(result.Status),
		DeliveryStatus:    resultDeliveryLabel(result),
		AssetCount:        len(result.GeneratedAssets) + len(result.Delivery.AssetRefs),
		DemoVideoCount:    demoVideos,
		ScreenshotCount:   screenshots,
		TraceCount:        traces,
		AckRequired:       result.Delivery.AckRequired,
		AckedAt:           result.Delivery.AckedAt,
		OutputDirectory:   record.OutputDirectory,
		FetchedAt:         time.Now().UTC(),
	}
}

func serverAckRecordFromResponse(record nativeServerHandoffRecord, ack model.ResultPackageAckResponse) nativeServerAckRecord {
	return nativeServerAckRecord{
		SchemaVersion:     "demoops.native_server_ack_record.v1",
		ProjectID:         record.ProjectID,
		OrgID:             firstNonEmptyNative(record.OrgID, nativeDefaultOrgID),
		ExchangePackageID: record.ExchangePackageID,
		ResultPackageID:   ack.ResultPackageID,
		Status:            string(ack.Status),
		DeliveryStatus:    string(ack.DeliveryStatus),
		ReceivedAssetIDs:  append([]string{}, ack.ReceivedAssetIDs...),
		VerifiedChecksums: ack.VerifiedChecksums,
		AckedAt:           ack.AckedAt,
		AckedByInstallID:  ack.AckedByInstallID,
		OutputDirectory:   record.OutputDirectory,
	}
}

func serverStatusSummary(record nativeServerHandoffRecord, status model.ExecutionPackageStatusResponse) string {
	lines := []string{
		"服务器执行状态",
		"Exchange Package ID: " + firstNonEmptyNative(status.ExchangePackageID, record.ExchangePackageID, "unknown"),
		"Cloud Job ID: " + firstNonEmptyNative(status.CloudJobID, record.CloudJobID, "unknown"),
		"Status: " + firstNonEmptyNative(string(status.Status), record.Status, "unknown"),
		"Stage: " + firstNonEmptyNative(status.Stage, record.Stage, "unknown"),
	}
	if status.ProgressPercent > 0 {
		lines = append(lines, fmt.Sprintf("Progress: %d%%", status.ProgressPercent))
	}
	if strings.TrimSpace(status.Message) != "" {
		lines = append(lines, "Message: "+compactPath(status.Message, 180))
	}
	if strings.TrimSpace(status.ResultPackageID) != "" {
		lines = append(lines, "Result Package ID: "+status.ResultPackageID)
	}
	if status.ResultSummary != nil {
		lines = append(lines, fmt.Sprintf("Result: status=%s delivery=%s pass_rate=%.2f assets=%d videos=%d screenshots=%d",
			status.ResultSummary.ResultStatus,
			status.ResultSummary.DeliveryStatus,
			status.ResultSummary.PassRate,
			status.ResultSummary.GeneratedAssetCount,
			status.ResultSummary.DemoVideoCount,
			status.ResultSummary.ScreenshotCount,
		))
		if strings.TrimSpace(status.ResultSummary.PrimaryDemoVideoURI) != "" {
			lines = append(lines, "Primary video: "+compactPath(status.ResultSummary.PrimaryDemoVideoURI, 160))
		}
		if status.ResultSummary.AckRequired {
			lines = append(lines, "Ack: required")
		} else if !status.ResultSummary.AckedAt.IsZero() {
			lines = append(lines, "Ack: "+nativeTimeLabel(status.ResultSummary.AckedAt))
		}
	}
	if status.FailureSummary != nil {
		lines = append(lines, "Failure: "+firstNonEmptyNative(status.FailureSummary.Code, "unknown"))
		if strings.TrimSpace(status.FailureSummary.Message) != "" {
			lines = append(lines, "Failure message: "+compactPath(status.FailureSummary.Message, 180))
		}
		if strings.TrimSpace(status.FailureSummary.FailedStage) != "" {
			lines = append(lines, "Failed stage: "+status.FailureSummary.FailedStage)
		}
		if strings.TrimSpace(status.FailureSummary.CurrentURL) != "" {
			lines = append(lines, "Current URL: "+compactPath(status.FailureSummary.CurrentURL, 160))
		}
		if strings.TrimSpace(status.FailureSummary.PageTitle) != "" {
			lines = append(lines, "Page title: "+compactPath(status.FailureSummary.PageTitle, 120))
		}
		if status.FailureSummary.Retryable {
			lines = append(lines, "Retryable: yes")
		}
	}
	if status.Error != nil {
		lines = append(lines, "Error code: "+firstNonEmptyNative(status.Error.Code, "unknown"))
		if strings.TrimSpace(status.Error.Message) != "" {
			lines = append(lines, "Error message: "+compactPath(status.Error.Message, 180))
		}
	}
	if len(status.StageHistory) > 0 {
		lines = append(lines, "", "Recent Stage History")
		start := maxInt(0, len(status.StageHistory)-6)
		for _, event := range status.StageHistory[start:] {
			line := "- " + firstNonEmptyNative(event.Stage, "unknown")
			if event.Status != "" {
				line += " " + string(event.Status)
			}
			if event.ProgressPercent > 0 {
				line += fmt.Sprintf(" %d%%", event.ProgressPercent)
			}
			if strings.TrimSpace(event.Message) != "" {
				line += ": " + compactPath(event.Message, 120)
			}
			lines = append(lines, line)
		}
	}
	lines = append(lines,
		"",
		"Server: "+firstNonEmptyNative(record.CloudBaseURL, "configured exchange"),
		"Last checked: "+nativeTimeLabel(time.Now()),
		"Handoff record: "+handoffRecordLabel(record.OutputDirectory),
	)
	return strings.Join(lines, "\r\n")
}

func serverResultSummary(record nativeServerHandoffRecord, result model.RecordingResultPackage) string {
	demoVideos, screenshots, traces := resultAssetCounts(result)
	lines := []string{
		"服务器结果包",
		"Result Package ID: " + firstNonEmptyNative(record.ResultPackageID, result.ResultID, result.Delivery.ResultPackageRef.ID, "unknown"),
		"Result ID: " + firstNonEmptyNative(result.ResultID, "unknown"),
		"Source Package ID: " + firstNonEmptyNative(result.SourcePackageID, "unknown"),
		"Cloud Job ID: " + firstNonEmptyNative(result.CloudJobID, record.CloudJobID, "unknown"),
		"Status: " + firstNonEmptyNative(string(result.Status), "unknown"),
		"Delivery: " + resultDeliveryLabel(result),
		fmt.Sprintf("Assets: generated=%d delivery_refs=%d demo_videos=%d screenshots=%d traces=%d", len(result.GeneratedAssets), len(result.Delivery.AssetRefs), demoVideos, screenshots, traces),
		fmt.Sprintf("Verification: pass_rate=%.2f failed_nodes=%d checksums=%d", result.VerificationReport.PassRate, len(result.VerificationReport.FailedNodeIDs), len(result.VerificationReport.OutputChecksums)),
	}
	if result.Delivery.AckRequired {
		lines = append(lines, "Ack: required")
	}
	if !result.Delivery.AckedAt.IsZero() {
		lines = append(lines, "Acked at: "+nativeTimeLabel(result.Delivery.AckedAt))
	}
	if result.FailureDiagnostic != nil {
		lines = append(lines, "Failure: "+firstNonEmptyNative(result.FailureDiagnostic.Error.Code, "unknown"))
		if strings.TrimSpace(result.FailureDiagnostic.Error.Message) != "" {
			lines = append(lines, "Failure message: "+compactPath(result.FailureDiagnostic.Error.Message, 180))
		}
		if strings.TrimSpace(result.FailureDiagnostic.CurrentURL) != "" {
			lines = append(lines, "Current URL: "+compactPath(result.FailureDiagnostic.CurrentURL, 160))
		}
	}
	if len(result.PatchLedger) > 0 {
		lines = append(lines, fmt.Sprintf("Runtime patches: %d", len(result.PatchLedger)))
	}
	lines = append(lines,
		"",
		"Saved files:",
		"- "+serverResultPackagePath(record.OutputDirectory),
		"- "+serverResultRecordPath(record.OutputDirectory),
		"Next: 如结果可接受，可执行“确认交付 ACK”。真实视频/截图二进制下载和本地 hash 校验是下一阶段能力。",
	)
	return strings.Join(lines, "\r\n")
}

func serverAckSummary(record nativeServerHandoffRecord, ack model.ResultPackageAckResponse) string {
	lines := []string{
		"服务器交付 ACK",
		"Result Package ID: " + firstNonEmptyNative(ack.ResultPackageID, record.ResultPackageID, "unknown"),
		"Status: " + firstNonEmptyNative(string(ack.Status), "unknown"),
		"Delivery: " + firstNonEmptyNative(string(ack.DeliveryStatus), "unknown"),
		fmt.Sprintf("Received assets: %d", len(ack.ReceivedAssetIDs)),
		fmt.Sprintf("Verified checksums: %t", ack.VerifiedChecksums),
		"Acked at: " + nativeTimeLabel(ack.AckedAt),
		"Acked by: " + firstNonEmptyNative(ack.AckedByInstallID, "desktop_installation"),
		"",
		"Saved file:",
		"- " + serverAckRecordPath(record.OutputDirectory),
	}
	return strings.Join(lines, "\r\n")
}

func deliverablesForDownload(record nativeServerHandoffRecord, result model.RecordingResultPackage) []model.ExecutionDeliverable {
	out := []model.ExecutionDeliverable{}
	seen := map[string]bool{}
	add := func(item model.ExecutionDeliverable) {
		item.ID = strings.TrimSpace(item.ID)
		if item.ID == "" || seen[item.ID] {
			return
		}
		seen[item.ID] = true
		out = append(out, item)
	}
	statusResultID := strings.TrimSpace(record.ResultPackageID)
	if statusResultID != "" && strings.TrimSpace(record.ExchangePackageID) != "" {
		// Download URLs from the server status response are not persisted in the compact handoff record,
		// so result package asset refs below provide the resumable fallback.
	}
	for _, asset := range result.Delivery.AssetRefs {
		add(model.ExecutionDeliverable{
			ID:          asset.ID,
			Kind:        asset.Kind,
			Role:        asset.Role,
			URI:         asset.URI,
			MimeType:    asset.MimeType,
			SHA256:      asset.SHA256,
			SizeBytes:   asset.SizeBytes,
			Sensitive:   asset.Sensitive,
			DownloadURL: fmt.Sprintf("/v1/dev/result-packages/%s/deliverables/%s", url.PathEscape(record.ResultPackageID), url.PathEscape(asset.ID)),
		})
	}
	for _, asset := range result.GeneratedAssets {
		add(model.ExecutionDeliverable{
			ID:        asset.ID,
			Kind:      asset.Kind,
			URI:       asset.URI,
			MimeType:  asset.MimeType,
			SHA256:    asset.SHA256,
			SizeBytes: asset.SizeBytes,
			Sensitive: asset.Sensitive,
		})
	}
	return out
}

func artifactDownloadManifest(record nativeServerHandoffRecord, downloadDir string, downloads []app.CloudDeliverableDownloadResult) nativeArtifactDownloadManifest {
	verified := 0
	mismatches := []string{}
	for _, download := range downloads {
		if download.ChecksumVerified {
			verified++
			continue
		}
		if strings.TrimSpace(download.ExpectedSHA256) != "" {
			mismatches = append(mismatches, download.ArtifactID)
		}
	}
	return nativeArtifactDownloadManifest{
		SchemaVersion:       "demoops.native_artifact_download_manifest.v1",
		ProjectID:           record.ProjectID,
		OrgID:               firstNonEmptyNative(record.OrgID, nativeDefaultOrgID),
		ExchangePackageID:   record.ExchangePackageID,
		ResultPackageID:     record.ResultPackageID,
		OutputDirectory:     record.OutputDirectory,
		DownloadDirectory:   downloadDir,
		DownloadedAt:        time.Now().UTC(),
		ArtifactCount:       len(downloads),
		VerifiedCount:       verified,
		ChecksumMismatchIDs: mismatches,
		Artifacts:           downloads,
	}
}

func artifactDownloadSummary(manifest nativeArtifactDownloadManifest) string {
	lines := []string{
		"服务器产物下载",
		"Result Package ID: " + firstNonEmptyNative(manifest.ResultPackageID, "unknown"),
		fmt.Sprintf("Artifacts: %d", manifest.ArtifactCount),
		fmt.Sprintf("Checksum verified: %d/%d", manifest.VerifiedCount, manifest.ArtifactCount),
		"Download directory: " + manifest.DownloadDirectory,
	}
	if len(manifest.ChecksumMismatchIDs) > 0 {
		lines = append(lines, "Checksum mismatch: "+strings.Join(manifest.ChecksumMismatchIDs, ", "))
	}
	if len(manifest.Artifacts) > 0 {
		lines = append(lines, "", "Downloaded Artifacts")
		for i, artifact := range manifest.Artifacts {
			if i >= 8 {
				lines = append(lines, fmt.Sprintf("- ...and %d more artifacts", len(manifest.Artifacts)-i))
				break
			}
			status := "unverified"
			if artifact.ChecksumVerified {
				status = "verified"
			} else if strings.TrimSpace(artifact.ExpectedSHA256) == "" {
				status = "no expected checksum"
			}
			lines = append(lines, fmt.Sprintf("- %s [%s] %s %s", firstNonEmptyNative(artifact.ArtifactID, "artifact"), firstNonEmptyNative(artifact.Kind, artifact.Role, "deliverable"), byteSizeLabel(int(artifact.SizeBytes)), status))
		}
	}
	if primary, ok := primaryDownloadedArtifact(manifest); ok {
		lines = append(lines, "Primary artifact: "+primary.LocalPath)
	}
	lines = append(lines, "", "Manifest: "+serverDeliverablesManifestPath(manifest.OutputDirectory), "Next: checksum 全部通过后可执行“确认交付 ACK”。")
	return strings.Join(lines, "\r\n")
}

func primaryDownloadedArtifact(manifest nativeArtifactDownloadManifest) (app.CloudDeliverableDownloadResult, bool) {
	if len(manifest.Artifacts) == 0 {
		return app.CloudDeliverableDownloadResult{}, false
	}
	pick := func(predicate func(app.CloudDeliverableDownloadResult) bool) (app.CloudDeliverableDownloadResult, bool) {
		for _, artifact := range manifest.Artifacts {
			if strings.TrimSpace(artifact.LocalPath) == "" {
				continue
			}
			if predicate(artifact) {
				return artifact, true
			}
		}
		return app.CloudDeliverableDownloadResult{}, false
	}
	if artifact, ok := pick(func(artifact app.CloudDeliverableDownloadResult) bool {
		return strings.EqualFold(artifact.Kind, "demo_video") || strings.EqualFold(artifact.Role, "demo_video")
	}); ok {
		return artifact, true
	}
	if artifact, ok := pick(func(artifact app.CloudDeliverableDownloadResult) bool {
		mimeType := strings.ToLower(strings.TrimSpace(artifact.MimeType))
		return strings.HasPrefix(mimeType, "video/")
	}); ok {
		return artifact, true
	}
	if artifact, ok := pick(func(app.CloudDeliverableDownloadResult) bool { return true }); ok {
		return artifact, true
	}
	return app.CloudDeliverableDownloadResult{}, false
}

func resultDeliveryLabel(result model.RecordingResultPackage) string {
	switch {
	case !result.Delivery.AckedAt.IsZero() || result.Status == model.RecordingResultStatusAcked:
		return string(model.ResultDeliveryStatusAcked)
	case !result.Delivery.DeliveredAt.IsZero():
		return string(model.ResultDeliveryStatusDelivered)
	case result.Delivery.AckRequired || len(result.Delivery.AssetRefs) > 0:
		return string(model.ResultDeliveryStatusReady)
	default:
		return "unknown"
	}
}

func resultAssetCounts(result model.RecordingResultPackage) (int, int, int) {
	demoVideos, screenshots, traces := 0, 0, 0
	for _, asset := range result.GeneratedAssets {
		kind := strings.ToLower(strings.TrimSpace(asset.Kind))
		switch {
		case kind == "demo_video":
			demoVideos++
		case kind == "screenshot":
			screenshots++
		case kind == "trace" || strings.Contains(kind, "trace"):
			traces++
		}
	}
	for _, asset := range result.Delivery.AssetRefs {
		kind := strings.ToLower(strings.TrimSpace(asset.Kind))
		role := strings.ToLower(strings.TrimSpace(asset.Role))
		switch {
		case kind == "demo_video" || role == "demo_video":
			demoVideos++
		case kind == "screenshot" || role == "screenshot":
			screenshots++
		case kind == "trace" || strings.Contains(kind, "trace") || strings.Contains(role, "trace"):
			traces++
		}
	}
	return demoVideos, screenshots, traces
}

func resultReceivedAssetIDs(result model.RecordingResultPackage) []string {
	ids := []string{}
	seen := map[string]bool{}
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}
	for _, asset := range result.Delivery.AssetRefs {
		add(asset.ID)
	}
	for _, asset := range result.GeneratedAssets {
		add(asset.ID)
	}
	return ids
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

func compactPath(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 8 {
		return value[len(value)-limit:]
	}
	head := maxInt(3, limit/3)
	tail := maxInt(4, limit-head-3)
	if head+tail+3 >= len(value) {
		return value
	}
	return value[:head] + "..." + value[len(value)-tail:]
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
		a.recentLabel,
		a.recentPackage,
		a.openRecentBtn,
		a.healthRuntime,
		a.healthStages,
		a.healthBundle,
		a.healthValidation,
		a.healthServer,
		a.healthResult,
		a.healthArtifacts,
		a.healthAck,
		a.previewSummary,
		a.previewContent,
		a.viewMarkdownBtn,
		a.viewStageBtn,
		a.viewOutlineBtn,
		a.viewBundleBtn,
		a.copyPreviewBtn,
		a.openPreviewBtn,
		a.generateBtn,
		a.saveBtn,
		a.browseRepoBtn,
		a.importReqBtn,
		a.importPackageBtn,
		a.clearDraftBtn,
		a.exportBtn,
		a.approveBtn,
		a.uploadBtn,
		a.queryStatusBtn,
		a.fetchResultBtn,
		a.ackResultBtn,
		a.downloadBtn,
		a.openAssetsBtn,
		a.openPrimaryBtn,
		a.openOutputBtn,
		a.openLogBtn,
		a.statusList,
		a.statusBar,
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
		hwnd == a.healthServer || hwnd == a.healthResult ||
		hwnd == a.healthArtifacts || hwnd == a.healthAck ||
		hwnd == a.recentPackage ||
		hwnd == a.phaseInput || hwnd == a.phaseUnderstand ||
		hwnd == a.phasePackage || hwnd == a.phaseReview ||
		hwnd == a.inputReadiness || hwnd == a.statusBar:
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
	if a.accelTable != 0 {
		procDestroyAccelerator.Call(uintptr(a.accelTable))
		a.accelTable = 0
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

func confirmBox(owner syscall.Handle, title string, message string) bool {
	result, _, _ := procMessageBoxW.Call(
		uintptr(owner),
		uintptr(unsafe.Pointer(utf16Ptr(message))),
		uintptr(unsafe.Pointer(utf16Ptr(title))),
		mbYesNo|mbIconQuestion,
	)
	return result == idYes
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

func setClipboardText(owner syscall.Handle, text string) error {
	opened, _, err := procOpenClipboard.Call(uintptr(owner))
	if opened == 0 {
		if err != syscall.Errno(0) {
			return err
		}
		return errors.New("OpenClipboard failed")
	}
	defer procCloseClipboard.Call()
	if ok, _, err := procEmptyClipboard.Call(); ok == 0 {
		if err != syscall.Errno(0) {
			return err
		}
		return errors.New("EmptyClipboard failed")
	}
	encoded := syscall.StringToUTF16(text)
	bytes := uintptr(len(encoded) * 2)
	handle, _, err := procGlobalAlloc.Call(gmemMoveable|gmemZeroInit, bytes)
	if handle == 0 {
		if err != syscall.Errno(0) {
			return err
		}
		return errors.New("GlobalAlloc failed")
	}
	ptr, _, err := procGlobalLock.Call(handle)
	if ptr == 0 {
		procGlobalFree.Call(handle)
		if err != syscall.Errno(0) {
			return err
		}
		return errors.New("GlobalLock failed")
	}
	copy(unsafe.Slice((*uint16)(unsafe.Pointer(ptr)), len(encoded)), encoded)
	procGlobalUnlock.Call(handle)
	if out, _, err := procSetClipboardData.Call(cfUnicodeText, handle); out == 0 {
		procGlobalFree.Call(handle)
		if err != syscall.Errno(0) {
			return err
		}
		return errors.New("SetClipboardData failed")
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

type accel struct {
	FVirt uint8
	_     uint8
	Key   uint16
	Cmd   uint16
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
	maxPackageImportBytes     = 2 * 1024 * 1024
	maxRecentPackages         = 8

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

	bsPushButton    = 0x00000000
	bsGroupBox      = 0x00000007
	cbsDropDownList = 0x0003
	lbsNotify       = 0x0001

	swShow = 5

	wmCreate                = 0x0001
	wmDestroy               = 0x0002
	wmClose                 = 0x0010
	wmSize                  = 0x0005
	wmSetFont               = 0x0030
	wmCommand               = 0x0111
	wmCtlColorEdit          = 0x0133
	wmCtlColorListBox       = 0x0134
	wmCtlColorStatic        = 0x0138
	wmAppGenerationDone     = 0x8001
	wmAppGenerationFailed   = 0x8002
	wmAppUploadDone         = 0x8003
	wmAppUploadFailed       = 0x8004
	wmAppServerStatusDone   = 0x8005
	wmAppServerStatusFailed = 0x8006
	wmAppServerResultDone   = 0x8007
	wmAppServerResultFailed = 0x8008
	wmAppServerAckDone      = 0x8009
	wmAppServerAckFailed    = 0x800A
	wmAppArtifactsDone      = 0x800B
	wmAppArtifactsFailed    = 0x800C

	lbAddString = 0x0180
	lbSetCurSel = 0x0186
	lbErr       = ^uintptr(0)

	cbAddString    = 0x0143
	cbResetContent = 0x014B
	cbGetCurSel    = 0x0147
	cbSetCurSel    = 0x014E
	cbErr          = ^uintptr(0)

	mfString    = 0x00000000
	mfSeparator = 0x00000800
	mfPopup     = 0x00000010
	mfByCommand = 0x00000000
	mfGrayed    = 0x00000001
	mfEnabled   = 0x00000000

	fVirtKey = 0x01
	fAlt     = 0x10
	fShift   = 0x04
	fControl = 0x08

	whiteBrush     = 0
	defaultGUIFont = 17

	mbOK              = 0x00000000
	mbYesNo           = 0x00000004
	mbIconError       = 0x00000010
	mbIconQuestion    = 0x00000020
	mbIconInformation = 0x00000040
	idYes             = 6

	bifReturnOnlyFSDirs = 0x00000001
	bifEditBox          = 0x00000010
	bifNewDialogStyle   = 0x00000040

	ofnReadOnly        = 0x00000001
	ofnOverwritePrompt = 0x00000002
	ofnHideReadOnly    = 0x00000004
	ofnFileMustExist   = 0x00001000
	ofnPathMustExist   = 0x00000800
	ofnExplorer        = 0x00080000

	cfUnicodeText = 13
	gmemMoveable  = 0x0002
	gmemZeroInit  = 0x0040

	defaultCharset       = 1
	outDefaultPrecision  = 0
	clipDefaultPrecision = 0
	cleartypeQuality     = 5
	defaultPitch         = 0
	ffDontCare           = 0
	vkReturn             = 0x0D
)

var errNativeUnavailable = errors.New("native desktop ui is unavailable")
