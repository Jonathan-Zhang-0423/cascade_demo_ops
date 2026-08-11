import { useEffect, useMemo, useState, type FormEvent, type MouseEvent } from "react";
import type { GraphNode, RepositoryInput, SandboxPolicy } from "../../src/types/workflowGraph";
import { agentPipelineItems, codeSummaryFromWorkspace, updateWorkspaceInputs } from "./agentPipeline";
import { createBridgeClient, userInputFromWorkspace } from "./bridge";
import type {
	ApprovalChecklistState,
	AssistantSessionView,
	ModelDiagnosticResult,
	NavSection,
	ProjectWorkspaceView,
	ProjectSummaryView,
	ProjectWorkstationView,
	RuntimeHealthView,
	RuntimeLogEntry,
	ScenarioID,
	WorkspaceStage,
} from "./domain";
import { createProjectDraftWorkspace, createWorkspace, initialChecklist } from "./mockWorkspace";
import { scenarioTemplates } from "./scenarios";
import { VideoEditor } from "./VideoEditor";
import { AssistantConversationPanel, AssistantWidget } from "./AssistantWidget";
import {
  canUploadExecutionPackage,
	beginGraphRevision,
	executionServerBlockedReason,
  lifecycleStagesFromWorkspace,
  lifecycleStatusLabel,
  lifecycleStatusTone,
  packageApprovalBlockedReasons,
  projectJourney,
  projectNextAction,
  projectStatusLabels,
  recommendedWorkstation,
  resetApprovalChecklistForRepair,
  sandboxProfileLabel,
  sandboxRiskLevel,
  sandboxRiskMessage,
  shouldResumeCloudRun,
  updateGraphNode,
  workflowStageLabels,
} from "./workflow";

const workflowStages = Object.entries(workflowStageLabels).map(([id, label]) => ({ id: id as WorkspaceStage, label }));

const dailyAgentHeadlines = [
  "Let’s make the product explain itself.",
  "Turn the happy path into a good story.",
  "Your product has a point. Let’s make it visible.",
  "A useful demo starts with one honest sentence.",
  "Let’s give your best workflow a proper entrance.",
  "Less screen tour, more point made.",
  "Find the shortest path to the “aha.”",
  "Make the clicks earn their screen time.",
  "Today’s agenda: one clear story, zero wandering.",
  "Let’s turn product behavior into customer proof.",
  "The product is ready. Let’s make the story catch up.",
  "Give us the outcome; we’ll choreograph the clicks.",
  "Good demos don’t tour. They reveal.",
  "Let’s make the useful parts impossible to miss.",
  "A clean demo is product truth with good timing.",
  "Show the work, skip the wandering.",
  "Let’s put the “why” between the clicks.",
  "Start with the customer win; the route comes next.",
  "Your workflow, edited for attention.",
  "Make every click move the story.",
  "Let’s turn the product path into a proof path.",
] as const;

function getDailyAgentHeadline(date = new Date()) {
  const localDay = Math.floor(Date.UTC(date.getFullYear(), date.getMonth(), date.getDate()) / 86_400_000);
  return dailyAgentHeadlines[localDay % dailyAgentHeadlines.length] ?? dailyAgentHeadlines[0];
}

type ProjectActionPrompt = {
  kind: "archive" | "delete";
  project: ProjectSummaryView;
};

export function App() {
	const bridge = useMemo(() => createBridgeClient(), []);
	const [activeNav, setActiveNav] = useState<NavSection>("projects");
	const [workspace, setWorkspace] = useState<ProjectWorkspaceView>(() => createProjectDraftWorkspace("product_demo"));
  const [projectSummaries, setProjectSummaries] = useState<ProjectSummaryView[]>([]);
  const [projectsLoading, setProjectsLoading] = useState(false);
  const [projectsError, setProjectsError] = useState("");
  const [projectsActionMessage, setProjectsActionMessage] = useState("");
  const [projectsActionProjectID, setProjectsActionProjectID] = useState<string>();
  const [projectActionPrompt, setProjectActionPrompt] = useState<ProjectActionPrompt>();
  const [repositoryMessage, setRepositoryMessage] = useState("");
  const [repositoryError, setRepositoryError] = useState("");
  const [selectedProjectID, setSelectedProjectID] = useState<string>();
  const [demoCredentials, setDemoCredentials] = useState({ username: "", password: "" });
  const [runtimeHealth, setRuntimeHealth] = useState<RuntimeHealthView | undefined>();
	const [runtimeHealthResolved, setRuntimeHealthResolved] = useState(false);
  const [modelDiagnostics, setModelDiagnostics] = useState<ModelDiagnosticResult[]>([]);
  const [isRunningDiagnostics, setIsRunningDiagnostics] = useState(false);
  const [diagnosticsError, setDiagnosticsError] = useState("");
  const [checklist, setChecklist] = useState<ApprovalChecklistState>(initialChecklist);
  const [selectedNodeID, setSelectedNodeID] = useState(workspace.planReview.graph.nodes[0]?.id ?? "");
  const [isRunningProduct, setIsRunningProduct] = useState(false);
	const [graphDirty, setGraphDirty] = useState(false);
	const [graphSaveError, setGraphSaveError] = useState("");
  const [agentPrompt, setAgentPrompt] = useState("");
  const [configurationSession, setConfigurationSession] = useState<{ scopeKey: string; initialMessage: string }>();

  const selectedNode = workspace.planReview.graph.nodes.find((node) => node.id === selectedNodeID) ?? workspace.planReview.graph.nodes[0];
	const serverBlockedReason = executionServerBlockedReason(runtimeHealthResolved, runtimeHealth?.browserAgentDirect);
  const blockedReasons = [
    ...packageApprovalBlockedReasons(workspace.packagePreview, checklist, workspace.sourceConnections),
	...(graphDirty ? ["执行图有未保存修改，请先保存并重新生成执行包预览。"] : []),
	...(serverBlockedReason ? [serverBlockedReason] : []),
  ];
  const canUpload = blockedReasons.length === 0 && canUploadExecutionPackage(workspace.packagePreview, checklist, workspace.sourceConnections);
  const navItems = useMemo(() => buildNavItems(runtimeHealth?.appCapabilities?.developerUI === true), [runtimeHealth?.appCapabilities?.developerUI]);
  const isProjectAgentWorkspace = activeNav === "project_library" && selectedProjectID !== undefined;

  useEffect(() => {
    let mounted = true;
    bridge.runtimeHealth().then((result) => {
      if (mounted && result.ok && result.data) {
        setRuntimeHealth(result.data);
      }
		if (mounted) setRuntimeHealthResolved(true);
    });
    return () => {
      mounted = false;
    };
  }, [bridge]);

  useEffect(() => {
    void refreshProjects();
  }, [bridge]);

	useEffect(() => {
		if (workspace.cloudRun.blockingErrorCode !== "reunderstanding_required") return;
		setChecklist((current) => resetApprovalChecklistForRepair(current));
	}, [workspace.cloudRun.blockingErrorCode]);

  useEffect(() => {
    if (!shouldResumeCloudRun(workspace, selectedProjectID)) return;
    let cancelled = false;
    let refreshing = false;
    const refresh = async () => {
      if (refreshing) return;
      refreshing = true;
      const result = await bridge.pollCloudRun(workspace);
      refreshing = false;
      if (cancelled || !result.ok || !result.data) return;
      setWorkspace((current) => current.id === result.data!.id ? mergeWorkspaceRuntimeLogs(result.data!, current) : current);
      if (result.data.cloudRun.status === "succeeded" || result.data.cloudRun.status === "failed") void refreshProjects();
    };
    void refresh();
    const timer = window.setInterval(() => void refresh(), 2500);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [bridge, selectedProjectID, workspace.id, workspace.cloudRun.exchangePackageID, workspace.cloudRun.status]);


  async function refreshProjects() {
    setProjectsLoading(true);
    const result = await bridge.listProjects();
    if (result.ok && result.data) {
      setProjectSummaries(result.data);
      setProjectsError("");
    } else {
      setProjectsError(result.error ?? "项目列表不可用");
    }
    setProjectsLoading(false);
  }


  async function openProject(projectID: string, preserveAssistantSession = false) {
    const result = await bridge.loadProject(projectID);
    if (!result.ok || !result.data) {
      setProjectsError(result.error ?? "项目加载失败");
      return;
    }
    setWorkspace(result.data);
    setSelectedProjectID(result.data.id);
    setSelectedNodeID(result.data.planReview.graph.nodes[0]?.id ?? "");
    setChecklist(initialChecklist);
	setGraphDirty(false);
	setGraphSaveError("");
    setDemoCredentials({ username: "", password: "" });
    if (!preserveAssistantSession) setConfigurationSession(undefined);
    setProjectsError("");
    setRepositoryMessage("");
    setRepositoryError("");
  }

  function patchWorkspace(patch: Partial<ProjectWorkspaceView>) {
    setWorkspace((current) => ({ ...current, ...patch }));
  }

  function patchNode(nodeID: string, patch: Partial<GraphNode>) {
	const revision = beginGraphRevision(workspace.planReview.graph, nodeID, patch, initialChecklist);
    setWorkspace((current) => ({
      ...current,
      planReview: {
        ...current.planReview,
		graph: revision.graph,
      },
    }));
	setGraphDirty(revision.graphDirty);
	setGraphSaveError("");
	setChecklist(revision.checklist);
  }

	async function saveGraphRevision() {
		if (!graphDirty || isRunningProduct) return;
		setIsRunningProduct(true);
		setGraphSaveError("");
		const result = await bridge.reviseWorkflowGraph(workspace, `graph-revision-${workspace.id}-${Date.now()}`);
		if (result.ok && result.data) {
			setWorkspace(result.data);
			setGraphDirty(false);
			setChecklist(initialChecklist);
		} else {
			setGraphSaveError(result.error ?? "执行图保存失败");
		}
		setIsRunningProduct(false);
	}

  async function prepareBridgeRunOptions() {
    const username = demoCredentials.username.trim();
    const password = demoCredentials.password;
    if (!username && !password) {
      return undefined;
    }
	if (!username || !password) {
		throw new Error("测试账号和密码必须同时填写。");
	}
	const credentialRef = `project-${workspace.id.replace(/[^A-Za-z0-9_.-]+/g, "-").slice(0, 80)}`;
	const stored = await bridge.storeDemoCredential(credentialRef, username, password);
	if (!stored.ok || !stored.data?.configured) {
		throw new Error(stored.error ?? "测试账号无法保存到 Windows 凭据库。");
	}
	return {
      demoCredentials: {
        ...(username ? { username } : {}),
        ...(password ? { password } : {}),
		secretRef: stored.data.secretRef,
      },
    };
  }

  async function createScenario(scenarioID: ScenarioID) {
    const seed = createWorkspace(scenarioID);
    const result = await bridge.createProject({ userInput: userInputFromWorkspace(seed), scenarioID });
    if (result.ok && result.data) {
      setWorkspace(result.data);
      setSelectedProjectID(result.data.id);
      setSelectedNodeID(result.data.planReview.graph.nodes[0]?.id ?? "");
      setChecklist(initialChecklist);
      setDemoCredentials({ username: "", password: "" });
      setConfigurationSession(undefined);
      void refreshProjects();
      setActiveNav("project_library");
    }
  }

  function requestArchiveProject(project: ProjectSummaryView) {
    setProjectActionPrompt({ kind: "archive", project });
    setProjectsActionMessage("");
  }

  function requestDeleteProject(project: ProjectSummaryView) {
    setProjectActionPrompt({ kind: "delete", project });
    setProjectsActionMessage("");
  }

  function cancelProjectAction() {
    setProjectActionPrompt(undefined);
  }

  async function confirmProjectAction() {
    if (!projectActionPrompt) {
      return;
    }
    const { kind, project } = projectActionPrompt;
    setProjectsActionProjectID(project.id);
    setProjectsActionMessage("");
    const result = kind === "archive" ? await bridge.archiveProject(project.id) : await bridge.deleteProject(project.id);
    if (result.ok) {
      if (selectedProjectID === project.id) {
        setSelectedProjectID(undefined);
      }
      setProjectSummaries((current) => current.filter((item) => item.id !== project.id));
      setProjectsError("");
      setProjectsActionMessage(`${kind === "archive" ? "Archived" : "Deleted"} "${project.name}".`);
      setProjectActionPrompt(undefined);
      void refreshProjects();
    } else {
      setProjectsError(result.error ?? (kind === "archive" ? "项目归档失败" : "项目删除失败"));
    }
    setProjectsActionProjectID(undefined);
  }

  async function prepareProductLifecycle(keepAgentWorkspace = false) {
    setIsRunningProduct(true);
    if (!keepAgentWorkspace) setActiveNav("execution_packages");
    const startedAt = new Date();
    let lastEventID = "0";
    let pollErrorShown = false;
    const pollProjectID = workspace.id;
    const pollRuntimeEvents = async () => {
      const events = await bridge.executionEvents(pollProjectID, lastEventID);
      if (events.ok && events.data) {
        const numericIDs = events.data.map((entry) => Number(entry.id)).filter((id) => Number.isFinite(id));
        if (numericIDs.length > 0) {
          lastEventID = String(Math.max(Number(lastEventID) || 0, ...numericIDs));
        }
        if (events.data.length > 0) {
          setWorkspace((current) => appendRuntimeLogs(current, events.data ?? []));
        }
      } else if (!pollErrorShown) {
        pollErrorShown = true;
        setWorkspace((current) => appendRuntimeLog(current, {
          level: "warning",
          message: "运行日志轮询不可用",
          detail: events.error ?? "无法读取本地 Dev Bridge 运行事件。",
        }));
      }
    };
    setWorkspace((current) => appendRuntimeLog({
      ...current,
      cloudRun: {
        ...current.cloudRun,
        stage: "local_generated",
        message: "本地理解与脚本生成已启动。",
        currentStep: "本地 Agent 正在读取需求、项目目录和产品地址",
        progress: 5,
      },
    }, {
      level: "info",
      message: "启动本地理解",
      detail: "完成本地理解和三合一执行包生成；不会上传服务器，生成后等待独立人工审批。",
    }));
    await pollRuntimeEvents();
    const poller = window.setInterval(() => {
      void pollRuntimeEvents();
    }, 1500);
	try {
	  const runOptions = await prepareBridgeRunOptions();
      const result = await bridge.runProductLifecycle({
        ...workspace,
        packagePreview: { ...workspace.packagePreview, ipAllowlistAcknowledged: true },
      }, runOptions);
	  setDemoCredentials({ username: "", password: "" });
      window.clearInterval(poller);
      await pollRuntimeEvents();
      if (result.ok && result.data) {
        const nextWorkspace = result.data;
        setWorkspace((current) => appendRuntimeLog(mergeWorkspaceRuntimeLogs(nextWorkspace, current), {
          level: nextWorkspace.cloudRun.status === "failed" ? "warning" : "success",
          message: nextWorkspace.cloudRun.status === "failed" ? "本地脚本已就绪，服务器阶段失败" : "产品实战流程完成",
          detail: `耗时 ${Math.round((Date.now() - startedAt.getTime()) / 1000)} 秒；当前阶段：${nextWorkspace.cloudRun.stage ?? nextWorkspace.cloudRun.status}。`,
        }));
        setSelectedProjectID(nextWorkspace.id);
        void refreshProjects();
        setActiveNav(keepAgentWorkspace ? "project_library" : "execution_packages");
      } else {
        const message = result.error ?? "产品实战自动流程失败";
        setWorkspace((current) => appendRuntimeLog({
          ...current,
          stage: "cloud_run",
          cloudRun: {
            ...current.cloudRun,
            status: "failed",
            lastError: message,
            currentStep: message,
            message,
          },
        }, {
          level: "error",
          message: "产品实战自动流程失败",
          detail: message,
        }));
	  }
	} catch (error) {
	  const message = error instanceof Error ? error.message : "测试账号无法安全保存或运行。";
	  setWorkspace((current) => appendRuntimeLog({ ...current, cloudRun: { ...current.cloudRun, status: "failed", lastError: message, currentStep: message, message } }, { level: "error", message: "产品实战准备失败", detail: message }));
    } finally {
      window.clearInterval(poller);
      setIsRunningProduct(false);
    }
  }

  async function runModelDiagnostics() {
    setIsRunningDiagnostics(true);
    setDiagnosticsError("");
    const result = await bridge.modelDiagnostics();
    if (result.ok && result.data) {
      setModelDiagnostics(result.data);
    } else {
      setDiagnosticsError(result.error ?? "模型诊断失败");
    }
    setIsRunningDiagnostics(false);
  }

  async function configureControlPlane(baseURL: string, accessToken: string) {
    const result = await bridge.configureControlPlane(baseURL, accessToken);
    if (result.ok && result.data) {
      setRuntimeHealth(result.data);
      return "";
    }
	const refreshed = await bridge.runtimeHealth();
	if (refreshed.ok && refreshed.data) setRuntimeHealth(refreshed.data);
    return result.error ?? "执行服务器配置失败";
  }

  async function configurePlanningModel(provider: string, model: string, apiKey: string, proxyURL: string) {
    const result = await bridge.configurePlanningModel(provider, model, apiKey, proxyURL);
    if (result.ok && result.data) {
      setRuntimeHealth(result.data);
      setDiagnosticsError("");
      const verification = await bridge.verifyPlanningModel();
      if (!verification.ok || !verification.data) {
        setDiagnosticsError(verification.error ?? "模型凭据已保存，但实际调用验证失败");
        return "模型凭据已安全保存，但尚未通过实际调用验证；Cascade 会继续使用规则兜底。";
      }
      setModelDiagnostics((current) => [...current.filter((item) => item.task !== "planning"), verification.data!]);
      const planning = verification.data;
      if (!planning?.ok) {
        return `模型凭据已安全保存，但实际调用未通过${planning?.errorClass ? `（${planning.errorClass}）` : ""}；Cascade 会继续使用规则兜底。`;
      }
      return "";
    }
    return result.error ?? "模型连接失败";
  }

  async function deletePlanningModel(provider: string) {
    const result = await bridge.deletePlanningModel(provider);
    if (result.ok && result.data) {
      setRuntimeHealth(result.data);
      return "";
    }
    return result.error ?? "模型凭据删除失败";
  }

  async function uploadPackage(keepAgentWorkspace = false) {
    if (!canUpload) {
      return;
    }
    setIsRunningProduct(true);
    setWorkspace((current) => appendRuntimeLog(current, {
      level: "info",
      message: "审批已确认，开始上传",
       detail: current.packagePreview.approvalSubjectDigest
         ? `当前审批绑定对象 digest ${current.packagePreview.approvalSubjectDigest}。`
         : "当前审批对象 digest 尚未生成，上传将被阻断。",
    }));
    const result = await bridge.approveAndUploadPackage({
      ...workspace,
      packagePreview: { ...workspace.packagePreview, ipAllowlistAcknowledged: true },
    });
    if (result.ok && result.data) {
      setWorkspace((current) => appendRuntimeLog(mergeWorkspaceRuntimeLogs(result.data!, current), {
        level: "success",
        message: "执行包已上传",
        detail: "服务器已接收审批后的执行包，正在进入 BrowserAgent 执行队列。",
      }));
      setSelectedProjectID(result.data.id);
      setActiveNav(keepAgentWorkspace ? "project_library" : "execution_packages");
      void refreshProjects();
    } else {
      const message = result.error ?? "执行包上传失败";
      setWorkspace((current) => appendRuntimeLog({
        ...current,
        cloudRun: { ...current.cloudRun, lastError: message, currentStep: message },
      }, { level: "error", message: "上传未完成", detail: message }));
    }
    setIsRunningProduct(false);
  }

  async function refreshCloudRun() {
    setIsRunningProduct(true);
    const result = await bridge.pollCloudRun(workspace);
    if (result.ok && result.data) {
      setWorkspace(result.data);
      void refreshProjects();
    } else {
      const message = result.error ?? "无法刷新服务器状态";
      setWorkspace((current) => appendRuntimeLog(current, { level: "warning", message: "状态刷新失败", detail: message }));
    }
    setIsRunningProduct(false);
  }

  async function simulateCloudSuccess(keepAgentWorkspace = false) {
    const result = await bridge.pollCloudRun(workspace);
    if (result.ok && result.data) {
      setWorkspace(result.data);
      setSelectedProjectID(result.data.id);
      void refreshProjects();
      setActiveNav("project_library");
    }
  }

  async function simulateCloudFailure(keepAgentWorkspace = false) {
    const result = await bridge.simulateCloudFailure(workspace);
    if (result.ok && result.data) {
      setWorkspace(result.data);
      if (!keepAgentWorkspace) setActiveNav("execution_packages");
    }
  }

  async function repairFailedScript(keepAgentWorkspace = false) {
    const result = await bridge.repairFailedScript(workspace);
    if (result.ok && result.data) {
      setWorkspace(result.data);
      setChecklist((current) => resetApprovalChecklistForRepair(current));
      if (!keepAgentWorkspace) setActiveNav("execution_packages");
    }
  }

  async function approveAssets() {
    setIsRunningProduct(true);
    setWorkspace(clearCloudRunError);
    const result = await bridge.acknowledgeResult(workspace);
    if (result.ok && result.data) {
      setWorkspace((current) => appendRuntimeLog(clearCloudRunError(mergeWorkspaceRuntimeLogs(result.data!, current)), { level: "success", message: "成品下载与校验完成", detail: "全部 deliverable 已通过 SHA-256 校验，服务器 ACK 已发送。" }));
      void refreshProjects();
    } else {
      const message = result.error ?? "成品下载或校验失败";
      setWorkspace((current) => appendRuntimeLog({ ...current, cloudRun: { ...current.cloudRun, lastError: message } }, { level: "error", message: "成品接收未完成", detail: message }));
    }
    setIsRunningProduct(false);
  }

  async function reviewAssets(decision: "approved" | "reedit_requested" | "rerecord_requested", summary?: string) {
    setIsRunningProduct(true);
    setWorkspace(clearCloudRunError);
    const result = await bridge.reviewResult(workspace, decision, summary);
    if (result.ok && result.data) {
      setWorkspace((current) => appendRuntimeLog(clearCloudRunError(mergeWorkspaceRuntimeLogs(result.data!, current)), { level: "success", message: decision === "approved" ? "人工审核已通过" : "返工请求已提交", detail: result.data!.cloudRun.message ?? "服务器已记录本次审核决定。" }));
      void refreshProjects();
    } else {
      const message = result.error ?? "人工审核提交失败";
      setWorkspace((current) => appendRuntimeLog({ ...current, cloudRun: { ...current.cloudRun, lastError: message } }, { level: "error", message: "审核决定未提交", detail: message }));
    }
    setIsRunningProduct(false);
  }

  async function releaseDirectLease() {
    setIsRunningProduct(true);
    const result = await bridge.releaseDirectBrowserAgentLease(workspace);
    if (result.ok && result.data) {
      setWorkspace((current) => appendRuntimeLog(result.data!, { level: "success", message: "安全执行会话已释放", detail: "终态任务和已校验素材仍保留。" }));
      void refreshProjects();
    } else {
      setWorkspace((current) => appendRuntimeLog({ ...current, cloudRun: { ...current.cloudRun, lastError: result.error ?? "直连租约释放失败" } }, { level: "error", message: "直连租约未释放", detail: result.error ?? "未知错误" }));
    }
    setIsRunningProduct(false);
  }

  function submitAgentPrompt(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const prompt = agentPrompt.trim();
    if (!prompt) return;
    const draft = updateWorkspaceInputs(createProjectDraftWorkspace(workspace.scenarioID), { rawUserPrompt: prompt });
    setWorkspace(draft);
    setSelectedProjectID(draft.id);
    setChecklist(initialChecklist);
    setConfigurationSession({ scopeKey: `new_${Date.now()}`, initialMessage: prompt });
    setAgentPrompt("");
    setActiveNav("project_library");
  }

  async function saveRepositoryConnections(repositories: RepositoryInput[]) {
    const nextWorkspace = {
      ...workspace,
      inputBundle: { ...workspace.inputBundle, repositories },
      sourceConnections: workspace.sourceConnections.map((source) => source.kind === "local_repo"
        ? { ...source, status: repositories.length > 0 ? ("ready" as const) : ("needs_attention" as const), detail: repositories.length > 0 ? `${repositories.length} 个代码仓库已连接。` : "尚未连接代码仓库。" }
        : source),
    };
    setWorkspace(nextWorkspace);
    setRepositoryError("");
    const result = await bridge.saveProjectInputs(workspace.id, nextWorkspace.inputBundle);
    if (result.ok && result.data) {
      setWorkspace(result.data);
      setRepositoryMessage("Repository connection saved.");
      void refreshProjects();
      return;
    }
    if (bridge.mode === "local") {
      setRepositoryMessage("Saved to this draft. Create the project to persist it to the local workspace.");
    } else {
      setRepositoryError(result.error ?? "Repository connection could not be saved.");
    }
  }

  function connectRepository(repository: RepositoryInput) {
    const repositories = [...(workspace.inputBundle.repositories ?? [])];
    const existingIndex = repositories.findIndex((item) => repository.url && item.url === repository.url || repository.local_path && item.local_path === repository.local_path || repository.host && item.host === repository.host && item.path === repository.path);
    if (existingIndex >= 0) {
      repositories[existingIndex] = { ...repositories[existingIndex], ...repository };
    } else {
      repositories.push({ ...repository, primary: repositories.length === 0 });
    }
    void saveRepositoryConnections(repositories);
  }

  function disconnectRepository(index: number) {
    const repositories = (workspace.inputBundle.repositories ?? []).filter((_, itemIndex) => itemIndex !== index);
    void saveRepositoryConnections(repositories);
  }

  return (
    <div className={`app-shell ${isProjectAgentWorkspace ? "project-agent-shell" : ""}`}>
      {isProjectAgentWorkspace ? (
        <ProjectAgentWorkspace
          bridge={bridge}
          workspace={workspace}
          {...(runtimeHealth ? { runtimeHealth } : {})}
		  runtimeHealthResolved={runtimeHealthResolved}
          blockedReasons={blockedReasons}
          checklist={checklist}
          canUpload={canUpload}
          isGeneratingPackage={isRunningProduct}
          selectedNodeID={selectedNodeID}
          onBack={() => { setSelectedProjectID(undefined); void refreshProjects(); }}
          onOpenProjectFromAssistant={(projectID) => { void openProject(projectID, true); }}
          onChecklistChange={setChecklist}
          onSelectNode={setSelectedNodeID}
          onPatchNode={patchNode}
		  graphDirty={graphDirty}
		  graphSaveError={graphSaveError}
		  onSaveGraphRevision={saveGraphRevision}
          onUpload={() => uploadPackage(true)}
		  onConfigureControlPlane={configureControlPlane}
          onCloudSuccess={() => simulateCloudSuccess(true)}
          onCloudFailure={() => simulateCloudFailure(true)}
          onRepairScript={() => repairFailedScript(true)}
          onApproveAssets={approveAssets}
          onReviewAssets={reviewAssets}
          onReleaseDirectLease={releaseDirectLease}
          onContinuePageOnly={async () => {
            const binding = workspace.sourceBinding;
            if (!binding) return;
            const result = await bridge.continueWithWebpageEvidence(workspace.id, binding.assessment_hash, `page-only-${binding.assessment_hash}`);
            if (result.ok && result.data) setWorkspace(result.data);
          }}
          {...(configurationSession ? { assistantScopeKey: configurationSession.scopeKey, assistantInitialMessage: configurationSession.initialMessage } : {})}
        />
      ) : <>
      <aside className="sidebar" aria-label="主导航">
        <div className="brand-block">
          <img className="brand-logo" src="/Logo_full_black.png" alt="Cascade" />
        </div>
        <nav className="nav-list">
          {navItems.map((item) => (
            <button
              key={item.id}
              type="button"
              className={activeNav === item.id ? "nav-item active" : "nav-item"}
              onClick={() => {
                setActiveNav(item.id);
                if (item.id === "project_library") {
                  setSelectedProjectID(undefined);
                  void refreshProjects();
                }
              }}
            >
              <span>{item.label}</span>
              {item.badge ? <small>{item.badge}</small> : null}
            </button>
          ))}
        </nav>
        <div className="runtime-strip">
          <span className="status-dot ok" />
          <span>桌面端</span>
          <span>本地库</span>
        </div>
      </aside>

      <main
        className={`workspace ${activeNav === "editor" ? "editor-workspace-mode" : ""} ${
          activeNav === "project_library" || activeNav === "repositories" || activeNav === "assets" ? "surface-library" : activeNav === "settings" ? "surface-settings" : ""
        }`}
      >
        {activeNav === "projects" ? (
          <AgentHome
            prompt={agentPrompt}
            isGeneratingPackage={isRunningProduct}
            onPromptChange={setAgentPrompt}
            onSubmitPrompt={submitAgentPrompt}
          />
        ) : activeNav === "project_library" && !selectedProjectID ? (
          <ProjectsPanel
            projects={projectSummaries}
            isLoading={projectsLoading}
            error={projectsError}
            message={projectsActionMessage}
            activeActionProjectID={projectsActionProjectID}
            actionPrompt={projectActionPrompt}
            onOpen={openProject}
            onArchive={requestArchiveProject}
            onDelete={requestDeleteProject}
            onCancelAction={cancelProjectAction}
            onConfirmAction={confirmProjectAction}
            onRetry={refreshProjects}
            onGoHome={() => setActiveNav("projects")}
          />
        ) : activeNav === "repositories" ? (
          <RepositoriesPanel
            workspace={workspace}
            message={repositoryMessage}
            error={repositoryError}
            onConnect={connectRepository}
            onDisconnect={disconnectRepository}
          />
        ) : (
          <>
            {activeNav !== "editor" ? <ProjectHeader workspace={workspace} /> : null}
            <div className={activeNav === "editor" ? "workspace-grid editor-wide" : "workspace-grid"}>
              <section className={activeNav === "editor" ? "main-panel editor-main-panel" : "main-panel"} aria-label="项目工作台">
                {activeNav === "new_demo" ? <ScenarioPicker activeID={workspace.scenarioID} onCreate={createScenario} /> : null}
                {activeNav === "execution_packages" ? (
                  <PackageApproval
                    workspace={workspace}
                    checklist={checklist}
                    blockedReasons={blockedReasons}
                    canUpload={canUpload}
                    isLocalMode={bridge.mode === "local"}
					{...(runtimeHealth ? { runtimeHealth } : {})}
					runtimeHealthResolved={runtimeHealthResolved}
                    onChecklistChange={setChecklist}
                    onUpload={uploadPackage}
					onConfigureControlPlane={configureControlPlane}
                    onCloudSuccess={simulateCloudSuccess}
                    onCloudFailure={simulateCloudFailure}
                    onRepairScript={repairFailedScript}
                  />
                ) : null}
                {activeNav === "project_library" || activeNav === "assets" ? <AssetReview workspace={workspace} onDownload={approveAssets} onReview={reviewAssets} onReleaseLease={releaseDirectLease} /> : null}
                {activeNav === "editor" ? <VideoEditor /> : null}
                {activeNav === "settings" ? (
                  <SettingsPanel
                    workspace={workspace}
                    diagnostics={modelDiagnostics}
                    diagnosticsError={diagnosticsError}
                    isRunningDiagnostics={isRunningDiagnostics}
                    onRunDiagnostics={runModelDiagnostics}
                    onConfigureControlPlane={configureControlPlane}
                    onConfigurePlanningModel={configurePlanningModel}
                    onDeletePlanningModel={deletePlanningModel}
                    developerUI={runtimeHealth?.appCapabilities?.developerUI === true}
                    {...(runtimeHealth ? { runtimeHealth } : {})}
                  />
                ) : null}
              </section>
              {activeNav !== "editor" ? <Inspector workspace={workspace} selectedNode={selectedNode} blockedReasons={blockedReasons} /> : null}
            </div>
          </>
        )}
        {activeNav === "project_library" ? <AssistantWidget bridge={bridge} context={{ surface: "projects", scopeKey: selectedProjectID ? `project_${selectedProjectID}` : "index", ...(selectedProjectID ? { projectID: selectedProjectID, projectName: workspace.name } : {}) }} onOpenProject={openProject} /> : null}
        {activeNav === "repositories" ? <AssistantWidget bridge={bridge} context={{ surface: "repositories", scopeKey: `workspace_${workspace.id}`, projectID: workspace.id, projectName: workspace.name, repositoryLabel: "Connected repositories" }} /> : null}
      </main>
      </>}
    </div>
  );
}

type ProjectAgentWorkspaceProps = {
  bridge: ReturnType<typeof createBridgeClient>;
  workspace: ProjectWorkspaceView;
  runtimeHealth?: RuntimeHealthView;
	runtimeHealthResolved: boolean;
  blockedReasons: string[];
  checklist: ApprovalChecklistState;
  canUpload: boolean;
  isGeneratingPackage: boolean;
  selectedNodeID: string;
  onBack: () => void;
  onOpenProjectFromAssistant: (projectID: string) => void;
  onChecklistChange: (state: ApprovalChecklistState) => void;
  onSelectNode: (id: string) => void;
  onPatchNode: (id: string, patch: Partial<GraphNode>) => void;
	graphDirty: boolean;
	graphSaveError: string;
	onSaveGraphRevision: () => void;
  onUpload: () => void;
	onConfigureControlPlane: (baseURL: string, accessToken: string) => Promise<string>;
  onCloudSuccess: () => void;
  onCloudFailure: () => void;
  onRepairScript: () => void;
  onApproveAssets: () => void;
  onReviewAssets: (decision: "approved" | "reedit_requested" | "rerecord_requested", summary?: string) => void;
  onReleaseDirectLease: () => void;
  onContinuePageOnly: () => void;
  assistantScopeKey?: string;
  assistantInitialMessage?: string;
};

function ProjectAgentWorkspace({
  bridge,
  workspace,
  runtimeHealth,
	runtimeHealthResolved,
  blockedReasons,
  checklist,
  canUpload,
  isGeneratingPackage,
  selectedNodeID,
  onBack,
  onOpenProjectFromAssistant,
  onChecklistChange,
  onSelectNode,
  onPatchNode,
	graphDirty,
	graphSaveError,
	onSaveGraphRevision,
  onUpload,
	onConfigureControlPlane,
  onCloudSuccess,
  onCloudFailure,
  onRepairScript,
  onApproveAssets,
  onReviewAssets,
  onReleaseDirectLease,
  onContinuePageOnly,
  assistantScopeKey,
  assistantInitialMessage,
}: ProjectAgentWorkspaceProps) {
  const [workstationView, setWorkstationView] = useState<ProjectWorkstationView>(() => recommendedWorkstation(workspace));
  const [mobileChatOpen, setMobileChatOpen] = useState(false);
  const [assistantSession, setAssistantSession] = useState<AssistantSessionView>();
  const [assistantSuggestion, setAssistantSuggestion] = useState<{ id: string; text: string }>();
  const selectedNode = workspace.planReview.graph.nodes.find((node) => node.id === selectedNodeID) ?? workspace.planReview.graph.nodes[0];
  const statusLabel = projectStatusLabels[workspace.status];
  const runtimeLabel = runtimeHealth?.localDataConfigured ? "本地能力就绪" : runtimeHealth ? "本地能力需处理" : "正在检查本地能力";

  useEffect(() => {
    setWorkstationView(recommendedWorkstation(workspace));
  }, [workspace.id, workspace.status, workspace.stage, workspace.cloudRun.status, workspace.cloudRun.exchangePackageID, workspace.cloudRun.resultPackageID, workspace.packagePreview.buildStatus]);

  useEffect(() => {
    setAssistantSession(undefined);
  }, [assistantScopeKey]);

  const nextAction = projectNextAction(workspace);
  const journey = projectJourney(workspace);
	const showWorkstationTask = workstationView === "plan" || nextAction.kind === "complete";

  return (
    <div className="project-agent-layout">
      <aside className={`project-agent-chat ${mobileChatOpen ? "mobile-open" : ""}`} aria-label="Project Cascade Agent">
        <header className="project-agent-chat-header">
          <button type="button" className="project-agent-back" onClick={onBack}>← 返回项目库</button>
          <div className="project-agent-identity">
            <span className="project-agent-mark"><img src="/Cascade_launcher_mark.png" alt="" /></span>
            <div><strong>Cascade Agent</strong><small>{workspace.name}</small></div>
          </div>
          <div className="project-agent-status-row"><span className="status-dot ok" />{runtimeLabel}<span className="project-agent-status-divider" />{statusLabel}</div>
          <button type="button" className="project-agent-mobile-close" onClick={() => setMobileChatOpen(false)}>关闭对话</button>
        </header>
        <AssistantConversationPanel
          bridge={bridge}
          context={{
            surface: "projects",
            scopeKey: assistantScopeKey ?? `project_${workspace.id}`,
            ...(workspace.id.startsWith("draft_") ? {} : { projectID: workspace.id }),
            projectName: workspace.name,
          }}
          {...(assistantInitialMessage ? { initialMessage: assistantInitialMessage } : {})}
          {...(assistantSuggestion ? { suggestedMessage: assistantSuggestion } : {})}
          embedded
          showHeader={false}
          onOpenProject={onOpenProjectFromAssistant}
          onWorkstationChange={setWorkstationView}
          onSessionChange={setAssistantSession}
        />
      </aside>
      <main className="project-workstation" aria-label="Project workstation">
        <header className="project-workstation-header">
          <div><p className="eyebrow">实时工作台</p><h1>{workstationTitle(workstationView)}</h1><p>{workstationDescription(workstationView)}</p></div>
          <div className="project-workstation-meta"><span>{workspace.productURL || "等待补充产品地址"}</span><StatusPill label={statusLabel} tone={workspace.status === "asset_ready" ? "green" : workspace.status === "script_repair_required" ? "yellow" : "blue"} /></div>
          <button type="button" className="project-agent-chat-toggle" onClick={() => setMobileChatOpen(true)}>打开 Cascade 对话</button>
        </header>
        <nav className="project-journey" aria-label="Demo production progress">
          {journey.map((step, index) => (
			<button key={step.id} type="button" className={`project-journey-step ${step.status}`} disabled={step.status === "upcoming"} onClick={() => setWorkstationView(step.workstation)}>
              <span>{step.status === "completed" ? "✓" : index + 1}</span>
              <strong>{step.label}</strong>
            </button>
          ))}
        </nav>
        <section className={`project-workstation-canvas ${workstationView === "editor" ? "project-workstation-editor-canvas" : ""}`}>
		  {showWorkstationTask ? (
            <section className="project-next-action" aria-label="Recommended next action">
              <div><span>下一步</span><strong>{nextAction.title}</strong><p>{nextAction.description}</p></div>
              {nextAction.kind === "review_plan" ? <button type="button" className="primary-action" onClick={() => setWorkstationView("approval")}>进入上传审批</button> : null}
              {nextAction.kind === "repair" ? <button type="button" className="primary-action" disabled={isGeneratingPackage} onClick={onRepairScript}>生成修复包</button> : null}
              {nextAction.kind === "review_result" && !workspace.cloudRun.resultAcknowledged ? <button type="button" className="primary-action" disabled={isGeneratingPackage} onClick={onApproveAssets}>{isGeneratingPackage ? "校验中…" : workspace.cloudRun.resultDownloaded ? "重试服务器 ACK" : "下载、校验并 ACK"}</button> : null}
              {nextAction.kind === "complete" ? <button type="button" className="secondary-action" onClick={() => setWorkstationView("editor")}>在 Editor 中打开</button> : null}
            </section>
          ) : null}
          {workstationView === "overview" ? <ProjectOverviewWorkstation workspace={workspace} {...(assistantSession ? { assistantSession } : {})} onContinueConfiguration={() => setMobileChatOpen(true)} /> : null}
          {workstationView === "evidence" ? <UnderstandingStagePanel workspace={workspace} onSuggestChange={(text) => { setAssistantSuggestion({ id: `${Date.now()}-${text}`, text }); setMobileChatOpen(true); }} onContinuePageOnly={onContinuePageOnly} /> : null}
		  {workstationView === "plan" ? <PlanReviewPanel workspace={workspace} selectedNodeID={selectedNodeID} onSelectNode={onSelectNode} onPatchNode={onPatchNode} graphDirty={graphDirty} graphSaveError={graphSaveError} onSaveGraphRevision={onSaveGraphRevision} /> : null}
		  {workstationView === "approval" ? <PackageApproval workspace={workspace} checklist={checklist} blockedReasons={blockedReasons} canUpload={canUpload} isLocalMode={bridge.mode === "local"} {...(runtimeHealth ? { runtimeHealth } : {})} runtimeHealthResolved={runtimeHealthResolved} onChecklistChange={onChecklistChange} onUpload={onUpload} onConfigureControlPlane={onConfigureControlPlane} onCloudSuccess={onCloudSuccess} onCloudFailure={onCloudFailure} onRepairScript={onRepairScript} /> : null}
          {workstationView === "execution" ? <div className="section-stack"><CloudRunPanel workspace={workspace} /><RuntimeLogPanel workspace={workspace} /></div> : null}
          {workstationView === "repair" ? <div className="section-stack"><FailureDiagnosticPanel workspace={workspace} onRepairScript={onRepairScript} /><ScriptRepairPanel workspace={workspace} /></div> : null}
          {workstationView === "assets" ? <AssetReview workspace={workspace} busy={isGeneratingPackage} onDownload={onApproveAssets} onReview={onReviewAssets} onReleaseLease={onReleaseDirectLease} /> : null}
          {workstationView === "editor" ? <div className="project-editor-workstation"><VideoEditor /></div> : null}
          {isGeneratingPackage ? <div className="project-workstation-progress"><span className="status-dot ok" />Cascade 正在更新工作台…</div> : null}
        </section>
        {selectedNode && workstationView === "plan" ? <aside className="project-workstation-drawer" aria-label="Selected plan detail"><strong>{selectedNode.title ?? selectedNode.action}</strong><span>{selectedNode.selector || "Page transition"}</span><small>{selectedNode.expected_outcome}</small></aside> : null}
      </main>
    </div>
  );
}

function ProjectOverviewWorkstation({ workspace, assistantSession, onContinueConfiguration }: { workspace: ProjectWorkspaceView; assistantSession?: AssistantSessionView; onContinueConfiguration: () => void }) {
  const configuration = assistantSession?.configuration;
  return (
    <div className="project-overview-workstation">
      <section className="project-overview-hero"><span className="project-overview-kicker">当前演示目标</span><h2>{workspace.inputBundle.raw_user_prompt || "告诉 Cascade，你希望客户看懂什么。"}</h2><p>{workspace.targetAudience ? `目标观众：${workspace.targetAudience}` : "Cascade 会把你的意图整理成证据、录制方案和可审核成片。"}</p></section>
      {configuration ? <ConfigurationSummaryCard configuration={configuration} pendingKind={pendingConfigurationAction(assistantSession)} onContinue={onContinueConfiguration} /> : <section className="configuration-summary loading"><div><span>Configuration</span><strong>正在读取项目配置…</strong></div></section>}
      <div className="project-overview-grid">
        <section className="project-overview-card"><span>证据覆盖</span><strong>{workspace.understanding.evidenceRefs.length ? `${Math.round(bestEvidenceConfidence(workspace) * 100)}% 确信度` : "尚未分析"}</strong><small>{workspace.understanding.evidenceRefs.length} 条安全证据引用</small></section>
        <section className="project-overview-card"><span>当前流程</span><strong>{workflowStageLabels[workspace.stage]}</strong><small>{workspace.planReview.graph.nodes.length} 个计划交互节点</small></section>
        <section className="project-overview-card"><span>输出结果</span><strong>{workspace.assets.length ? `${workspace.assets.length} 个素材` : "尚无成品"}</strong><small>{workspace.cloudRun.currentStep}</small></section>
      </div>
      <section className="project-overview-next"><span>你可以直接问</span><p>“还缺什么信息？” · “总结刚才的配置” · “为什么这一步需要确认？”</p></section>
    </div>
  );
}

function ConfigurationSummaryCard({ configuration, pendingKind, onContinue }: { configuration: AssistantSessionView["configuration"]; pendingKind: "patch" | "summary" | "safe_action" | "none"; onContinue: () => void }) {
  const missing = new Set(configuration.missingFields ?? []);
  const sourceLabels = configuration.sources?.map((source) => source.label).filter(Boolean) ?? [];
  const rows = [
    ["项目名称", configuration.projectName, "projectName"],
    ["产品地址", configuration.productURL, "productURL"],
    ["目标受众", configuration.targetAudience, "targetAudience"],
    ["目标时长", configuration.targetDurationSec ? `${configuration.targetDurationSec} 秒` : "", "targetDurationSec"],
    ["必须展示", configuration.mustShow?.join("、"), "mustShow"],
    ["禁止展示", configuration.mustNotShow?.join("、"), "mustNotShow"],
    ["项目来源", sourceLabels.join("、"), "sources"],
    ["演示账号", configuration.credentialRefs?.length ? `${configuration.credentialRefs.length} 个安全引用` : "按需添加", "credentialRefs"],
  ] as const;
  const awaitingPatch = pendingKind === "patch";
  const awaitingSummary = pendingKind === "summary";
  const awaitingSafeAction = pendingKind === "safe_action";
  return <section className={`configuration-summary ${configuration.readiness}`}>
    <header><div><span>Configuration · v{configuration.version}</span><strong>{configuration.confirmed ? "已确认并启动本地分析" : configuration.readiness === "ready" ? "信息完整，等待摘要确认" : "继续补全项目配置"}</strong></div><StatusPill label={configuration.confirmed ? "已确认" : awaitingPatch ? "字段变更待确认" : awaitingSummary ? "摘要待确认" : awaitingSafeAction ? "安全动作待完成" : configuration.readiness === "ready" ? "可确认" : `缺少 ${missing.size} 项`} tone={configuration.confirmed ? "green" : configuration.readiness === "ready" ? "blue" : "yellow"} /></header>
    <p className="configuration-objective">{configuration.objective || "告诉 Cascade 这支演示要让观众理解什么。"}</p>
    <dl>{rows.map(([label, value, field]) => <div key={field} className={missing.has(field) ? "missing" : ""}><dt>{label}</dt><dd>{value || (missing.has(field) ? "待补充" : "未设置")}</dd></div>)}</dl>
    <footer><span>{awaitingPatch ? "左侧有字段变更等待确认；确认前不会写入 configuration。" : awaitingSummary ? `${sourceLabels.length ? `本地来源已连接（${sourceLabels.join("、")}）但尚未读取；` : ""}确认后才会开始本地代码分析并生成草稿，不会上传执行包。` : awaitingSafeAction ? "请在左侧完成安全来源或凭据动作；取消后卡片仍可重试。" : configuration.confirmed ? "Configuration 确认不代表执行审批，上传前仍需独立人审。" : "聊天不会接收密码、token 或本地绝对路径。"}</span>{!configuration.confirmed ? <button type="button" className="secondary-action" onClick={onContinue}>{awaitingPatch ? "查看字段变更" : awaitingSummary ? "确认摘要并开始读取" : "继续配置"}</button> : null}</footer>
  </section>;
}

function pendingConfigurationAction(session: AssistantSessionView | undefined): "patch" | "summary" | "safe_action" | "none" {
  const kinds = session?.messages.flatMap((message) => message.proposals ?? []).filter((proposal) => proposal.status === "available").map((proposal) => proposal.kind) ?? [];
  if (kinds.includes("configuration_patch")) return "patch";
  if (kinds.includes("confirm_configuration") || kinds.includes("start_local_analysis")) return "summary";
  if (kinds.some((kind) => ["select_local_project", "connect_github", "attach_requirement_document", "attach_brand_asset", "store_demo_credential"].includes(kind))) return "safe_action";
  return "none";
}

function workstationTitle(view: ProjectWorkstationView): string {
  const labels: Record<ProjectWorkstationView, string> = { overview: "项目配置", evidence: "证据与产品理解", plan: "演示方案", approval: "执行包审批", execution: "服务器执行进度", repair: "修复工作台", assets: "成品审核", editor: "视频编辑器" };
  return labels[view];
}

function workstationDescription(view: ProjectWorkstationView): string {
  const descriptions: Record<ProjectWorkstationView, string> = { overview: "通过左侧对话补齐配置，右侧实时展示已确认内容。", evidence: "检查 Cascade 使用了哪些证据、来源是否一致，以及还有什么不确定。", plan: "确认业务阶段、录制顺序和成功标准，再进入独立上传审批。", approval: "明确查看服务器将访问、使用和录制的内容。", execution: "自动展示 BrowserAgent、录制、剪辑和渲染进度。", repair: "根据脱敏诊断决定重新剪辑还是重新录制。", assets: "下载校验成片，并提交通过或返工意见。", editor: "对已生成的视频进行精确调整。" };
  return descriptions[view];
}

function appendRuntimeLog(workspace: ProjectWorkspaceView, entry: Omit<RuntimeLogEntry, "id" | "time">): ProjectWorkspaceView {
  const logEntry: RuntimeLogEntry = {
    id: `log_${Date.now()}_${Math.random().toString(16).slice(2)}`,
    time: new Date().toLocaleTimeString("zh-CN", { hour12: false }),
    ...entry,
  };
  return appendRuntimeLogs(workspace, [logEntry]);
}

function appendRuntimeLogs(workspace: ProjectWorkspaceView, entries: RuntimeLogEntry[]): ProjectWorkspaceView {
  if (entries.length === 0) {
    return workspace;
  }
  const seen = new Set<string>();
  const runtimeLogs = [...entries, ...(workspace.runtimeLogs ?? [])].filter((entry) => {
    if (seen.has(entry.id)) {
      return false;
    }
    seen.add(entry.id);
    return true;
  }).slice(0, 50);
  return {
    ...workspace,
    runtimeLogs,
  };
}

function mergeWorkspaceRuntimeLogs(next: ProjectWorkspaceView, current: ProjectWorkspaceView): ProjectWorkspaceView {
  const lastError = next.cloudRun.lastError ?? current.cloudRun.lastError;
  const merged: ProjectWorkspaceView = {
    ...next,
    cloudRun: lastError ? { ...next.cloudRun, lastError } : next.cloudRun,
  };
  const runtimeLogs = current.runtimeLogs ?? next.runtimeLogs;
  return runtimeLogs ? { ...merged, runtimeLogs } : merged;
}

function ProjectHeader({
  workspace,
}: {
  workspace: ProjectWorkspaceView;
}) {
  return (
    <header className="project-header">
      <div>
        <p className="eyebrow">{scenarioLabel(workspace.scenarioID)}</p>
        <h1>{workspace.name}</h1>
        <div className="header-meta">
          <span>{workspace.productURL}</span>
          <span>{workspace.targetAudience}</span>
          <StatusPill label={projectStatusLabels[workspace.status]} tone="blue" />
        </div>
      </div>
    </header>
  );
}

function AgentHome({
  prompt,
  isGeneratingPackage,
  onPromptChange,
  onSubmitPrompt,
}: {
  prompt: string;
  isGeneratingPackage: boolean;
  onPromptChange: (value: string) => void;
  onSubmitPrompt: (event: FormEvent<HTMLFormElement>) => void;
}) {
  const dailyHeadline = getDailyAgentHeadline();
  return (
    <div className="agent-home">
      <section className="agent-hero" aria-label="Cascade Agent">
        <h1>{dailyHeadline}</h1>
        <form className="agent-composer" onSubmit={onSubmitPrompt}>
          <textarea
            value={prompt}
            onChange={(event) => onPromptChange(event.currentTarget.value)}
            rows={4}
            placeholder="例如：为我们的审批产品制作一支面向销售团队的 60 秒演示，重点展示从提交到通过的流程。"
            aria-label="描述你想制作的演示"
          />
          <div className="agent-composer-footer">
            <span>发送后直接创建一段新的项目对话；分析与上传仍需分别确认。</span>
            <button type="submit" className="agent-submit" disabled={isGeneratingPackage || !prompt.trim()}>
              {isGeneratingPackage ? "处理中" : "开始配置"}
              <span aria-hidden="true">↗</span>
            </button>
          </div>
        </form>
      </section>
    </div>
  );
}

function ProjectsPanel({
  projects,
  isLoading,
  error,
  message,
  activeActionProjectID,
  actionPrompt,
  onOpen,
  onArchive,
  onDelete,
  onCancelAction,
  onConfirmAction,
  onRetry,
  onGoHome,
}: {
  projects: ProjectSummaryView[];
  isLoading: boolean;
  error: string;
  message: string;
  activeActionProjectID: string | undefined;
  actionPrompt: ProjectActionPrompt | undefined;
  onOpen: (projectID: string) => void;
  onArchive: (project: ProjectSummaryView) => void;
  onDelete: (project: ProjectSummaryView) => void;
  onCancelAction: () => void;
  onConfirmAction: () => void;
  onRetry: () => void;
  onGoHome: () => void;
}) {
  function stopRowClick(event: MouseEvent<HTMLButtonElement>) {
    event.stopPropagation();
  }

  return (
    <section className="projects-panel" aria-label="Projects">
      <div className="projects-panel-header">
        <div>
          <p className="eyebrow">Demo workspace</p>
          <h1>Projects</h1>
          <p className="projects-panel-intro">Every demo session, its evidence, and the assets Cascade made from it.</p>
        </div>
        <span className="project-count">{projects.length} {projects.length === 1 ? "project" : "projects"}</span>
      </div>
      {message ? <div className="projects-action-message" role="status">{message}</div> : null}
      {actionPrompt ? (
        <div className={actionPrompt.kind === "delete" ? "project-action-confirm danger" : "project-action-confirm"} role="dialog" aria-modal="false" aria-label={`${actionPrompt.kind === "delete" ? "Delete" : "Archive"} project`}>
          <div>
            <strong>{actionPrompt.kind === "delete" ? `Would you like to delete "${actionPrompt.project.name}"?` : `Would you like to archive "${actionPrompt.project.name}"?`}</strong>
            <span>
              {actionPrompt.kind === "delete"
                ? "This removes the local project record from Cascade."
                : "This hides the project from the default Projects list while keeping its local record."}
            </span>
          </div>
          <div className="project-action-confirm-actions">
            <button type="button" className="row-action muted" disabled={activeActionProjectID === actionPrompt.project.id} onClick={onCancelAction}>No</button>
            <button
              type="button"
              className={actionPrompt.kind === "delete" ? "row-action danger" : "row-action"}
              disabled={activeActionProjectID === actionPrompt.project.id}
              onClick={onConfirmAction}
            >
              {activeActionProjectID === actionPrompt.project.id ? "Working..." : "Yes"}
            </button>
          </div>
        </div>
      ) : null}
      {error ? (
        <div className="projects-state error-banner">
          <strong>Projects are unavailable.</strong>
          <span>{error}</span>
          <button type="button" className="row-action" onClick={onRetry}>Retry</button>
        </div>
      ) : isLoading ? (
        <div className="projects-state">Loading projects…</div>
      ) : projects.length === 0 ? (
        <div className="projects-state projects-empty">
          <strong>No demo projects yet.</strong>
          <span>Describe the customer story on Home and Cascade will create the first workspace.</span>
          <button type="button" className="row-action" onClick={onGoHome}>Go to Home</button>
        </div>
      ) : (
        <div className="projects-table-wrap">
          <table className="projects-table">
            <thead>
              <tr>
                <th>Project</th>
                <th>Product</th>
                <th>Stage</th>
                <th>Status</th>
                <th>Assets</th>
                <th aria-label="Actions" />
              </tr>
            </thead>
            <tbody>
              {projects.map((project) => (
                <tr key={project.id} onClick={() => onOpen(project.id)}>
                  <td>
                    <button
                      type="button"
                      className="project-name-link"
                      onClick={(event) => {
                        stopRowClick(event);
                        onOpen(project.id);
                      }}
                    >
                      {project.name}
                    </button>
                    <small>{project.id}</small>
                  </td>
                  <td className="project-url">{project.productURL || "Product URL pending"}</td>
                  <td>{workflowStageLabels[project.stage]}</td>
                  <td><StatusPill label={projectStatusLabels[project.status]} tone={project.status === "asset_ready" ? "green" : project.status === "script_repair_required" ? "yellow" : "blue"} /></td>
                  <td>{project.generatedAssetCount}/{project.assetCount || "—"}</td>
                  <td>
                    <div className="project-row-actions">
                      <button
                        type="button"
                        className="row-action"
                        disabled={activeActionProjectID === project.id}
                        onClick={(event) => {
                          stopRowClick(event);
                          onOpen(project.id);
                        }}
                      >
                        Open
                      </button>
                      <button
                        type="button"
                        className="row-action muted"
                        disabled={activeActionProjectID === project.id}
                        onClick={(event) => {
                          stopRowClick(event);
                          onArchive(project);
                        }}
                      >
                        Archive
                      </button>
                      <button
                        type="button"
                        className="row-action danger"
                        disabled={activeActionProjectID === project.id}
                        onClick={(event) => {
                          stopRowClick(event);
                          onDelete(project);
                        }}
                      >
                        Delete
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

type RepositoryKind = "github" | "local" | "server";

function safeLocalPathLabel(path?: string) {
  const normalized = (path ?? "").replaceAll("\\", "/").replace(/\/+$/, "");
  return normalized.split("/").filter(Boolean).at(-1) ?? "本地项目目录";
}

function RepositoriesPanel({
  workspace,
  message,
  error,
  onConnect,
  onDisconnect,
}: {
  workspace: ProjectWorkspaceView;
  message: string;
  error: string;
  onConnect: (repository: RepositoryInput) => void;
  onDisconnect: (index: number) => void;
}) {
  const [kind, setKind] = useState<RepositoryKind>("github");
  const [form, setForm] = useState({ url: "", branch: "main", localPath: "", host: "", port: "22", path: "", username: "", secretRef: "" });
  const repositories = workspace.inputBundle.repositories ?? [];

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const base: RepositoryInput = { kind, provider: kind, read_only: true };
    if (kind === "github") {
      if (!form.url.trim()) return;
      onConnect({ ...base, url: form.url.trim(), branch: form.branch.trim() || "main" });
    } else if (kind === "local") {
      if (!form.localPath.trim()) return;
      onConnect({ ...base, local_path: form.localPath.trim() });
    } else {
      if (!form.host.trim() || !form.path.trim()) return;
      onConnect({
        ...base,
        host: form.host.trim(),
        port: Number(form.port) || 22,
        path: form.path.trim(),
        ...(form.username.trim() ? { username: form.username.trim() } : {}),
        ...(form.secretRef.trim() ? { secret_ref: form.secretRef.trim() } : {}),
      });
    }
    setForm((current) => ({ ...current, url: "", localPath: "", host: "", path: "", username: "", secretRef: "" }));
  }

  return (
    <section className="repositories-panel" aria-label="Repositories">
      <header className="repositories-header">
        <div>
          <p className="eyebrow">Source connections</p>
          <h1>Repositories</h1>
          <p>Connect read-only source material for Cascade to understand and use in your demo workspace.</p>
        </div>
        <StatusPill label={`${repositories.length} connected`} tone={repositories.length > 0 ? "green" : "neutral"} />
      </header>

      <section className="repository-section">
        <SectionTitle title="Connected repositories" meta="Read-only by default" />
        {repositories.length > 0 ? (
          <div className="repository-list">
            {repositories.map((repository, index) => (
              <div className="repository-row" key={`${repository.kind ?? repository.provider ?? "repository"}-${repository.url ?? repository.local_path ?? repository.host ?? index}`}>
                <div className="repository-icon">{repository.kind === "github" || repository.provider === "github" ? "GH" : repository.kind === "server" || repository.provider === "server" ? "SV" : "LO"}</div>
                <div className="repository-details">
                  <strong>{repository.kind === "github" || repository.provider === "github" ? repository.url : repository.kind === "server" || repository.provider === "server" ? `${repository.username ? `${repository.username}@` : ""}${repository.host}:${repository.path}` : safeLocalPathLabel(repository.local_path)}</strong>
                  <span>{repository.branch ? `Branch ${repository.branch} · ` : ""}{repository.read_only ? "Read-only source" : "Writable source"}</span>
                </div>
                <button type="button" className="text-action muted" onClick={() => onDisconnect(index)}>Disconnect</button>
              </div>
            ))}
          </div>
        ) : (
          <div className="repository-empty">No repositories connected to this workspace yet.</div>
        )}
      </section>

      <section className="repository-section">
        <SectionTitle title="Connect a repository" meta="Choose one source" />
        <div className="repository-kind-tabs" role="tablist" aria-label="Repository source type">
          {(["github", "local", "server"] as RepositoryKind[]).map((item) => (
            <button key={item} type="button" className={kind === item ? "repository-kind active" : "repository-kind"} onClick={() => setKind(item)}>
              {item === "github" ? "GitHub" : item === "local" ? "Local repository" : "Server repository"}
            </button>
          ))}
        </div>
        <form className="repository-form" onSubmit={submit}>
          {kind === "github" ? (
            <>
              <label className="field-row wide"><span>GitHub repository URL</span><input value={form.url} onChange={(event) => setForm({ ...form, url: event.currentTarget.value })} placeholder="https://github.com/org/repository" /></label>
              <label className="field-row"><span>Branch</span><input value={form.branch} onChange={(event) => setForm({ ...form, branch: event.currentTarget.value })} placeholder="main" /></label>
            </>
          ) : kind === "local" ? (
            <label className="field-row wide"><span>Local repository path</span><input value={form.localPath} onChange={(event) => setForm({ ...form, localPath: event.currentTarget.value })} placeholder="/Users/you/Projects/product" /></label>
          ) : (
            <>
              <label className="field-row"><span>Server host</span><input value={form.host} onChange={(event) => setForm({ ...form, host: event.currentTarget.value })} placeholder="staging.example.com" /></label>
              <label className="field-row"><span>SSH port</span><input value={form.port} onChange={(event) => setForm({ ...form, port: event.currentTarget.value })} inputMode="numeric" placeholder="22" /></label>
              <label className="field-row"><span>Username</span><input value={form.username} onChange={(event) => setForm({ ...form, username: event.currentTarget.value })} placeholder="deploy" /></label>
              <label className="field-row"><span>Repository path</span><input value={form.path} onChange={(event) => setForm({ ...form, path: event.currentTarget.value })} placeholder="/srv/product" /></label>
              <label className="field-row wide"><span>Credential reference</span><input value={form.secretRef} onChange={(event) => setForm({ ...form, secretRef: event.currentTarget.value })} placeholder="vault://server-key (optional)" /></label>
            </>
          )}
          <div className="repository-form-footer">
            <span>Connections are stored as read-only references. Secrets are never entered here.</span>
            <button type="submit" className="primary-action">Connect repository</button>
          </div>
        </form>
      </section>
      {message ? <div className="repository-feedback success">{message}</div> : null}
      {error ? <div className="repository-feedback error">{error}</div> : null}
    </section>
  );
}

function ScenarioPicker({ activeID, onCreate }: { activeID: ScenarioID; onCreate: (id: ScenarioID) => void }) {
  return (
    <div className="section-stack">
      <SectionTitle title="场景模板" meta="3 个模板" />
      <div className="template-grid">
        {scenarioTemplates.map((template) => (
          <article key={template.id} className={activeID === template.id ? "template-card selected" : "template-card"}>
            <div className="template-topline">
              <h2>{template.name}</h2>
              <StatusPill label={`${template.targetDurationSec} 秒`} tone="neutral" />
            </div>
            <p>{template.objective}</p>
            <div className="chip-row">
              {template.requiredAssets.map((asset) => (
                <span key={asset} className="chip">
                  {assetKindLabel(asset)}
                </span>
              ))}
            </div>
            <button type="button" className="row-action" onClick={() => onCreate(template.id)}>
              使用模板
            </button>
          </article>
        ))}
      </div>
    </div>
  );
}

function ProjectFlow({
  workspace,
  demoCredentials,
  selectedNodeID,
  onSelectNode,
  onPatchNode,
  onStageChange,
  onDemoCredentialsChange,
  onWorkspaceChange,
  onApproveAssets,
}: {
  workspace: ProjectWorkspaceView;
  demoCredentials: { username: string; password: string };
  selectedNodeID: string;
  onSelectNode: (id: string) => void;
  onPatchNode: (id: string, patch: Partial<GraphNode>) => void;
  onStageChange: (stage: WorkspaceStage) => void;
  onDemoCredentialsChange: (credentials: { username: string; password: string }) => void;
  onWorkspaceChange: (workspace: ProjectWorkspaceView) => void;
  onApproveAssets: () => void;
}) {
  return (
    <div className="section-stack">
      <div className="stage-strip">
        {workflowStages.map((stage) => (
          <button
            key={stage.id}
            type="button"
            className={workspace.stage === stage.id ? "stage active" : "stage"}
            onClick={() => onStageChange(stage.id)}
          >
            {stage.label}
          </button>
        ))}
      </div>
      {workspace.stage === "setup" ? <SetupPanel workspace={workspace} /> : null}
      {workspace.stage === "inputs" ? (
        <InputsPanel
          workspace={workspace}
          demoCredentials={demoCredentials}
          onDemoCredentialsChange={onDemoCredentialsChange}
          onWorkspaceChange={onWorkspaceChange}
        />
      ) : null}
      {workspace.stage === "understanding" ? <UnderstandingStagePanel workspace={workspace} /> : null}
      {workspace.stage === "plan_review" ? (
        <PlanReviewPanel
          workspace={workspace}
          selectedNodeID={selectedNodeID}
          onSelectNode={onSelectNode}
          onPatchNode={onPatchNode}
        />
      ) : null}
      {workspace.stage === "package_approval" ? <PackageStageSummary workspace={workspace} /> : null}
      {workspace.stage === "cloud_run" ? <CloudRunPanel workspace={workspace} /> : null}
      {workspace.stage === "script_repair" ? <ScriptRepairPanel workspace={workspace} /> : null}
      {workspace.stage === "result_review" ? <ResultReviewPanel workspace={workspace} onApprove={onApproveAssets} /> : null}
    </div>
  );
}

function SetupPanel({ workspace }: { workspace: ProjectWorkspaceView }) {
  const template = scenarioTemplates.find((item) => item.id === workspace.scenarioID);
  return (
    <div className="stage-panel-grid">
      <section className="table-section">
        <SectionTitle title="基础设置" meta={scenarioLabel(workspace.scenarioID)} />
        <div className="settings-grid">
          <Fact label="产品地址" value={workspace.productURL} />
          <Fact label="目标受众" value={workspace.targetAudience} />
          <Fact label="目标时长" value={`${workspace.planReview.targetDurationSec} 秒`} />
          <Fact label="输出资产" value={workspace.planReview.outputRequests.map(assetKindLabel).join("、")} />
        </div>
      </section>
      <section className="table-section">
        <SectionTitle title="模板目标" meta="业务可读" />
        <div className="stage-copy">
          <p>{template?.objective ?? workspace.planReview.graph.summary}</p>
          <div className="chip-row">
            {(template?.defaultChecklist ?? []).map((item) => (
              <span key={item} className="chip">{item}</span>
            ))}
          </div>
        </div>
      </section>
    </div>
  );
}

function InputsPanel({
  workspace,
  demoCredentials,
  onDemoCredentialsChange,
  onWorkspaceChange,
}: {
  workspace: ProjectWorkspaceView;
  demoCredentials: { username: string; password: string };
  onDemoCredentialsChange: (credentials: { username: string; password: string }) => void;
  onWorkspaceChange: (workspace: ProjectWorkspaceView) => void;
}) {
  const localRepoPath = workspace.inputBundle.repositories?.[0]?.local_path ?? "";
  const forbiddenData = workspaceInputForbiddenData(workspace);
  function patchInputs(patch: Parameters<typeof updateWorkspaceInputs>[1]) {
    onWorkspaceChange(updateWorkspaceInputs(workspace, patch));
  }
  return (
    <div className="section-stack">
      <section className="table-section">
        <SectionTitle title="输入材料配置" meta="Dev Bridge 路径输入" />
        <div className="input-form-grid">
          <label className="field-row">
            <span>产品 URL</span>
            <input value={workspace.productURL} onChange={(event) => patchInputs({ productURL: event.currentTarget.value })} placeholder="https://app.example.com" />
          </label>
          <label className="field-row">
            <span>项目根目录</span>
            <input value={localRepoPath} onChange={(event) => patchInputs({ localRepoPath: event.currentTarget.value })} placeholder="C:\\Users\\you\\Desktop\\your-project" />
          </label>
          <label className="field-row">
            <span>目标受众</span>
            <input value={workspace.targetAudience} onChange={(event) => patchInputs({ targetAudience: event.currentTarget.value })} placeholder="中国客户的产品和运营团队" />
          </label>
          <label className="field-row">
            <span>演示账号</span>
            <input
              value={demoCredentials.username}
              onChange={(event) => onDemoCredentialsChange({ ...demoCredentials, username: event.currentTarget.value })}
              autoComplete="off"
              placeholder="用于本地登录预扫描"
            />
          </label>
          <label className="field-row">
            <span>演示密码</span>
            <input
              type="password"
              value={demoCredentials.password}
              onChange={(event) => onDemoCredentialsChange({ ...demoCredentials, password: event.currentTarget.value })}
              autoComplete="new-password"
              placeholder="仅传给本地 Dev Bridge"
            />
          </label>
          <label className="field-row wide">
            <span>本次演示需求</span>
            <textarea value={workspace.inputBundle.raw_user_prompt ?? ""} onChange={(event) => patchInputs({ rawUserPrompt: event.currentTarget.value })} rows={4} placeholder="描述本次想展示的功能、受众、必须讲清楚的业务价值。" />
          </label>
          <label className="field-row">
            <span>禁用页面</span>
            <textarea value={workspace.planReview.forbiddenPages.join("\n")} onChange={(event) => patchInputs({ forbiddenPagesText: event.currentTarget.value })} rows={3} />
          </label>
          <label className="field-row">
            <span>禁用数据</span>
            <textarea value={forbiddenData.join("\n")} onChange={(event) => patchInputs({ forbiddenDataText: event.currentTarget.value })} rows={3} />
          </label>
        </div>
        <div className="input-note">当前 Dev Bridge 使用文本路径输入；演示账号密码只作为本地登录预扫描的瞬时凭据，不进入执行包、审批文档或云端 payload。代码读取只生成结构摘要和 hash，不上传完整源码。</div>
      </section>
      <InputsTable workspace={workspace} />
      <CodeSummaryPanel workspace={workspace} />
      <AgentPipelinePanel workspace={workspace} />
      <section className="table-section">
        <SectionTitle title="本地输入边界" meta="App 端处理" />
        <div className="settings-grid">
          <Fact label="代码策略" value="只生成结构摘要和 hash，不上传完整源码" />
          <Fact label="凭据策略" value="仅保存 secret_ref，录制授权自动过期" />
          <Fact label="页面材料" value={`${workspace.inputBundle.webpage_screenshots?.length ?? 0} 张截图 / URL 可访问`} />
          <Fact label="需求材料" value={workspace.inputBundle.raw_user_prompt ? "已读取用户需求" : "待补充"} />
        </div>
      </section>
    </div>
  );
}

function UnderstandingStagePanel({ workspace, onSuggestChange, onContinuePageOnly }: { workspace: ProjectWorkspaceView; onSuggestChange?: (text: string) => void; onContinuePageOnly?: () => void }) {
  return (
    <div className="section-stack">
      <MetricsRow workspace={workspace} />
      <SourceBindingCard workspace={workspace} {...(onSuggestChange ? { onSuggestChange } : {})} {...(onContinuePageOnly ? { onContinuePageOnly } : {})} />
      <UnderstandingPanel workspace={workspace} />
      <ProjectIntelligencePanel workspace={workspace} />
      <CodeSummaryPanel workspace={workspace} />
      <AgentPipelinePanel workspace={workspace} />
      <section className="table-section">
        <SectionTitle title="理解输出" meta="ProductMap / Evidence" />
        <div className="settings-grid">
          <Fact label="Product Map" value={workspace.understanding.productMapID} />
          <Fact label="摘要置信度" value={`${Math.round(bestEvidenceConfidence(workspace) * 100)}%`} />
          <Fact label="敏感提示" value={`${workspace.understanding.sensitiveWarnings.length} 项`} />
          <Fact label="后续用途" value="生成 Stage 审批 JSON、Browser Agent 大纲和证据链" />
        </div>
      </section>
    </div>
  );
}

function SourceBindingCard({ workspace, onSuggestChange, onContinuePageOnly }: { workspace: ProjectWorkspaceView; onSuggestChange?: (text: string) => void; onContinuePageOnly?: () => void }) {
  const binding = workspace.sourceBinding;
  if (!binding || binding.status === "not_applicable") return null;
  const mismatched = binding.status === "mismatched" && binding.effective_mode === "blocked";
  const message = binding.status === "matched"
    ? "源码已经完成只读分析，且网页与源码身份信号一致；本次允许使用源码路由、组件和 selector 候选。"
    : binding.status === "unverified"
      ? "源码已经完成只读分析，但无法可靠证明它与当前网页属于同一产品；代码摘要保留在本机，Browser Agent 大纲已自动排除全部源码 selector 并使用仅网页证据模式。"
      : binding.effective_mode === "page_only"
        ? "源码与网页不匹配；已按用户确认移除全部源码执行证据。"
        : "网页与源码来源不匹配，已在生成 Browser Agent 大纲前停止。";
  return (
    <section className="table-section">
      <SectionTitle title="来源一致性" meta={binding.status} />
      <div className="settings-grid">
        <Fact label="执行证据模式" value={binding.effective_mode === "mixed" ? "网页 + 已匹配源码" : binding.effective_mode === "page_only" ? "仅网页证据" : "已阻断"} />
        <Fact label="来源数量" value={`${binding.sources?.length ?? 0} 个`} />
        <Fact label="评估摘要" value={binding.assessment_hash.slice(0, 16)} />
      </div>
      <div className={mismatched ? "error-banner" : "input-note"}>{message}</div>
      {mismatched ? <div className="section-actions">
        {onSuggestChange ? <button type="button" className="secondary-action" onClick={() => onSuggestChange("我要更换待录制的产品网页，请帮我更新产品地址。")}>更换网页</button> : null}
        {onSuggestChange ? <button type="button" className="secondary-action" onClick={() => onSuggestChange("我要更换项目源码，请让我重新选择本地目录或 GitHub 仓库。")}>更换源码</button> : null}
        {onContinuePageOnly ? <button type="button" className="secondary-action" onClick={onContinuePageOnly}>仅使用网页证据重新分析</button> : null}
      </div> : null}
    </section>
  );
}

function ProjectIntelligencePanel({ workspace }: { workspace: ProjectWorkspaceView }) {
  const intelligence = workspace.projectIntelligence;
  const readiness = workspace.scriptReadiness ?? intelligence?.script_readiness_report;
  if (!intelligence) {
    return (
      <section className="table-section">
        <SectionTitle title="项目理解图谱" meta="待生成" />
        <div className="settings-grid">
          <Fact label="状态" value="生成执行包后展示架构、功能、交互面和可演示路径。" />
          <Fact label="安全边界" value="仅保存结构摘要、hash、selector 和 evidence refs。" />
        </div>
      </section>
    );
  }
  const architecture = intelligence.architecture;
  const traceSteps = workspace.agentGraphTrace?.steps ?? [];
  const intentGoals = intelligence.demo_intent?.goals ?? [];
  const verifiedPlan = intelligence.verified_interaction_plan;
  const missingEvidence = intelligence.missing_evidence_report;
  return (
    <section className="table-section">
      <SectionTitle title="项目理解图谱" meta={workspace.agentGraphTrace?.graph_name ?? "ProjectIntelligenceGraph"} />
      <div className="settings-grid">
        <Fact label="架构摘要" value={architecture?.summary ?? "已生成结构摘要"} />
        <Fact label="框架/语言" value={[...(architecture?.frameworks ?? []), ...(architecture?.languages ?? [])].join("、") || "待识别"} />
        <Fact label="模块/路由" value={`${architecture?.modules?.length ?? 0} 个模块 / ${architecture?.route_tree?.length ?? 0} 个路由`} />
        <Fact label="功能能力" value={`${intelligence.feature_capabilities?.length ?? 0} 个`} />
        <Fact label="交互面" value={`${intelligence.interaction_surfaces?.length ?? 0} 个`} />
        <Fact label="API/数据模型" value={`${intelligence.api_contracts?.length ?? 0} 个 API / ${intelligence.data_models?.length ?? 0} 个模型`} />
        <Fact label="推荐路径" value={readiness?.recommended_scenario_name ?? intelligence.demo_scenario_plans?.[0]?.name ?? "待选择"} />
        <Fact label="脚本可行性" value={readiness?.can_proceed ? "可继续生成脚本" : "需复核输入材料"} />
        <Fact label="Selector 覆盖" value={typeof readiness?.selector_coverage === "number" ? `${Math.round(readiness.selector_coverage * 100)}%` : "待计算"} />
        <Fact label="需求目标" value={`${intentGoals.length} 个`} />
        <Fact label="页面验证" value={verifiedPlan ? `${verifiedPlan.business_action_count ?? 0} 个业务动作 · ${verifiedPlan.verification_mode ?? "待识别"}` : "未完成"} />
        <Fact label="缺失证据" value={missingEvidence?.blocking ? "阻塞脚本生成" : missingEvidence ? "有提示" : "无阻塞"} />
        <Fact label="Source Digest" value={intelligence.source_digest_sha256 ?? "待生成"} />
      </div>
      {intentGoals.length > 0 ? (
        <div className="runtime-log-list">
          {intentGoals.slice(0, 5).map((goal) => {
            const trace = intelligence.feature_trace?.traces?.find((item) => item.intent_goal_id === goal.id);
            const action = verifiedPlan?.actions?.find((item) => item.intent_goal_id === goal.id);
            const missing = missingEvidence?.items?.find((item) => item.intent_goal_id === goal.id);
            return (
              <div key={goal.id} className={`runtime-log-row ${action ? "success" : missing ? "warning" : "info"}`}>
                <span>{goal.business_critical ? "业务目标" : "前置目标"}</span>
                <strong>{goal.label}</strong>
                <small>
                  {action
                    ? `已验证 ${action.selector ?? action.label ?? ""}`
                    : missing
                      ? `${missing.missing_kind} · ${missing.message}`
                      : `候选 ${trace?.selector_evidence?.length ?? 0} 个`}
                </small>
              </div>
            );
          })}
        </div>
      ) : null}
      {missingEvidence?.items?.length ? (
        <div className="error-banner">
          {missingEvidence.summary ?? "缺少页面验证证据"}
          {" · "}
          {missingEvidence.items.slice(0, 2).map((item) => item.suggested_action || item.message).join("；")}
        </div>
      ) : null}
      <div className="chip-row">
        {(readiness?.warnings ?? []).slice(0, 4).map((warning) => (
          <span key={warning.id} className="chip">{warning.summary}</span>
        ))}
        {(readiness?.blockers ?? []).slice(0, 4).map((blocker) => (
          <span key={blocker.id} className="chip">{blocker.summary}</span>
        ))}
      </div>
      {traceSteps.length > 0 ? (
        <div className="runtime-log-list">
          {traceSteps.slice(0, 10).map((step) => (
            <div key={step.id} className={`runtime-log-row ${step.status === "completed" ? "success" : step.status === "failed" ? "error" : "info"}`}>
              <span>{step.tool || step.agent || step.node_id}</span>
              <strong>{step.output_summary ?? step.input_summary ?? step.node_id}</strong>
              <small>{typeof step.elapsed_ms === "number" ? `${step.elapsed_ms}ms` : ""}</small>
            </div>
          ))}
        </div>
      ) : null}
    </section>
  );
}

function CodeSummaryPanel({ workspace }: { workspace: ProjectWorkspaceView }) {
  const summary = codeSummaryFromWorkspace(workspace);
  const repoPath = workspace.inputBundle.repositories?.[0]?.local_path;
  return (
    <section className="table-section">
      <SectionTitle title="代码阅读摘要" meta={repoPath ? "CodeReaderAgent" : "未提供项目根目录"} />
      <div className="settings-grid">
        <Fact label="扫描文件" value={summary.fileCount > 0 ? `${summary.fileCount} 个` : repoPath ? "路径不可读或无可扫描文件" : "未提供"} />
        <Fact label="框架线索" value={summary.frameworks.length > 0 ? summary.frameworks.join("、") : "待识别"} />
        <Fact label="路由/组件" value={`${summary.routes} 个路由 / ${summary.components} 个组件`} />
        <Fact label="Selector" value={`${summary.selectors} 个稳定选择器候选`} />
        <Fact label="Source Digest" value={summary.sourceDigest || "待生成"} />
        <Fact label="读取策略" value={summary.degraded ? "已降级使用需求/页面材料" : "只读扫描结构摘要，不保存完整源码"} />
      </div>
    </section>
  );
}

function AgentPipelinePanel({ workspace }: { workspace: ProjectWorkspaceView }) {
  return (
    <section className="table-section">
      <SectionTitle title="Agent 分工" meta="本地执行包生成链路" />
      <div className="agent-pipeline">
        {agentPipelineItems(workspace).map((item) => (
          <div key={item.id} className="agent-row">
            <StatusPill label={agentStatusLabel(item.status)} tone={item.status === "completed" ? "green" : item.status === "attention" ? "yellow" : "neutral"} />
            <div>
              <strong>{item.name}</strong>
              <span>{item.role}</span>
            </div>
            <small>{item.output}</small>
            <em>{item.detail}</em>
          </div>
        ))}
      </div>
    </section>
  );
}

function PlanReviewPanel({
  workspace,
  selectedNodeID,
  onSelectNode,
  onPatchNode,
	graphDirty = false,
	graphSaveError = "",
	onSaveGraphRevision = () => undefined,
}: {
  workspace: ProjectWorkspaceView;
  selectedNodeID: string;
  onSelectNode: (id: string) => void;
  onPatchNode: (id: string, patch: Partial<GraphNode>) => void;
	graphDirty?: boolean;
	graphSaveError?: string;
	onSaveGraphRevision?: () => void;
}) {
  return (
    <div className="section-stack">
	  {graphDirty ? <div className="notice-card warning"><strong>执行图有未保存修改</strong><p>保存后会重新生成执行包和 digest，原审批将失效。</p><button type="button" className="primary-action" onClick={onSaveGraphRevision}>保存并重新生成预览</button></div> : null}
	  {graphSaveError ? <div className="notice-card error"><strong>执行图保存失败</strong><p>{graphSaveError}</p></div> : null}
      <GraphReviewTable workspace={workspace} selectedNodeID={selectedNodeID} onSelectNode={onSelectNode} onPatchNode={onPatchNode} />
      <section className="table-section">
        <SectionTitle title="录制策略" meta="RecordingRunSpec 预览" />
        <div className="settings-grid">
          <Fact label="允许域名" value={workspace.planReview.allowedDomains.join("、")} />
          <Fact label="禁止页面" value={workspace.planReview.forbiddenPages.join("、")} />
          <Fact label="打码选择器" value={workspace.planReview.redactionSelectors.join("、")} />
          <Fact label="输出请求" value={workspace.planReview.outputRequests.map(assetKindLabel).join("、")} />
        </div>
      </section>
    </div>
  );
}

function PackageStageSummary({ workspace }: { workspace: ProjectWorkspaceView }) {
  return (
    <div className="section-stack">
      <section className="table-section">
        <SectionTitle title="执行包审批入口" meta={workspace.packagePreview.packageID} />
        <div className="settings-grid">
          <Fact label="Runtime" value={workspace.executableScriptBundle?.script_manifest.runtime ?? "待生成"} />
          <Fact label="Plan Hash" value={workspace.executableScriptBundle?.reproducibility.plan_hash_sha256 ?? "待生成"} />
          <Fact label="Outline Hash" value={workspace.executableScriptBundle?.reproducibility.outline_hash_sha256 ?? workspace.executableScriptBundle?.reproducibility.script_hash_sha256 ?? "待生成"} />
          <Fact label="Prompt Hash" value={workspace.executableScriptBundle?.reproducibility.prompt_policy_hash_sha256 ?? "待生成"} />
          <Fact label="Bundle Hash" value={workspace.executableScriptBundle?.reproducibility.bundle_hash_sha256 ?? "待生成"} />
          <Fact label="审批状态" value={projectStatusLabels[workspace.status]} />
        </div>
      </section>
      <ServerLifecyclePanel workspace={workspace} />
    </div>
  );
}

function CloudRunPanel({ workspace }: { workspace: ProjectWorkspaceView }) {
  return (
    <div className="section-stack">
      <section className="execution-progress-hero">
        <div><span>服务器正在生成成片</span><strong>{workspace.cloudRun.currentStep || "等待执行服务器更新"}</strong><p>{workspace.cloudRun.message || "你可以离开当前项目，进度会自动保存并在重新打开后恢复。"}</p></div>
        <div className="execution-progress-value"><strong>{Math.max(0, Math.min(workspace.cloudRun.progress, 100))}%</strong><span>{cloudStatusLabel(workspace.cloudRun.status)}</span></div>
        <div className="progress-track"><span style={{ width: `${Math.max(0, Math.min(workspace.cloudRun.progress, 100))}%` }} /></div>
      </section>
	  {workspace.cloudRun.transport === "browser_agent_direct_v1" ? <section className="table-section">
		<SectionTitle title="Browser Agent 直连" meta={workspace.cloudRun.cloudJobID ? "已分配任务" : "等待上传"} />
		<div className="settings-grid">
		  <Fact label="当前阶段" value={workspace.cloudRun.stage ?? "等待上传"} />
		  <Fact label="短期租约" value={workspace.cloudRun.leaseID ? `…${workspace.cloudRun.leaseID.slice(-12)}` : "尚未申请"} />
		  <Fact label="租约到期" value={workspace.cloudRun.leaseExpiresAt ? formatTimestamp(workspace.cloudRun.leaseExpiresAt) : "—"} />
		  <Fact label="Browser Agent Job" value={workspace.cloudRun.cloudJobID ?? "等待服务器接收"} />
		  <Fact label="返回素材" value={`${workspace.cloudRun.directArtifacts?.length ?? 0} 个`} />
		  <Fact label="等待原因" value={workspace.cloudRun.waitingReason ?? "无"} />
		  <Fact label="阻断代码" value={workspace.cloudRun.blockingErrorCode ?? "无"} />
		  <Fact label="下一步" value={workspace.cloudRun.nextAction ?? "等待服务器状态"} />
		  <Fact label="需要重新审批" value={workspace.cloudRun.requiresReapproval ? "是" : "否"} />
		</div>
	  </section> : null}
	  {workspace.cloudRun.reunderstandingIssues?.length ? <section className="table-section">
		<SectionTitle title="需要重新理解的问题" meta={`${workspace.cloudRun.reunderstandingIssues.length} 项结构化阻断`} />
		<div className="blocked-list">{workspace.cloudRun.reunderstandingIssues.map((issue) => (
		  <span key={`${issue.code}-${issue.stageID ?? "global"}-${issue.nodeID ?? "global"}`}>
			{issue.stageID ? `${issue.stageID} · ` : ""}{issue.summary || issue.code}{issue.suggestion ? `；建议：${issue.suggestion}` : ""}
		  </span>
		))}</div>
	  </section> : null}
      <ServerLifecyclePanel workspace={workspace} />
    </div>
  );
}

function ScriptRepairPanel({ workspace }: { workspace: ProjectWorkspaceView }) {
  const diagnostic = workspace.cloudRun.failureDiagnostic;
  const lineage = workspace.executableScriptBundle?.repair_lineage;
  return (
    <div className="section-stack">
      <section className="table-section">
        <SectionTitle title="脚本修复" meta={diagnostic?.failed_node_id ?? "等待诊断"} />
        <div className="settings-grid">
          <Fact label="失败节点" value={diagnostic?.failed_node_id ?? "无"} />
          <Fact label="错误信息" value={diagnostic?.error.message ?? "暂无错误"} />
          <Fact label="诊断材料" value={`${(diagnostic?.screenshot_refs?.length ?? 0) + (diagnostic?.trace_refs?.length ?? 0)} 个加密 artifact`} />
          <Fact label="修复要求" value="重新生成脚本包，并回到人工审批" />
          <Fact label="来源 Result" value={workspace.cloudRun.repairRequest?.source_result_id ?? lineage?.source_result_id ?? "等待失败结果"} />
          <Fact label="Repair Attempt" value={`${workspace.cloudRun.repairRequest?.repair_attempt ?? lineage?.repair_attempt ?? 0}`} />
        </div>
      </section>
      <ServerLifecyclePanel workspace={workspace} />
    </div>
  );
}

function ResultReviewPanel({ workspace, onApprove }: { workspace: ProjectWorkspaceView; onApprove: () => void }) {
  return <AssetReview workspace={workspace} onDownload={onApprove} onReview={() => undefined} onReleaseLease={() => undefined} />;
}

function MetricsRow({ workspace }: { workspace: ProjectWorkspaceView }) {
  const metrics = [
    ["路由", workspace.understanding.routesDetected],
    ["功能", workspace.understanding.featuresDetected],
    ["组件", workspace.understanding.componentsSummarized],
    ["数据模型", workspace.understanding.dataModelsSummarized],
  ];
  return (
    <div className="metric-row">
      {metrics.map(([label, value]) => (
        <div key={label} className="metric-cell">
          <span>{label}</span>
          <strong>{value}</strong>
        </div>
      ))}
    </div>
  );
}

function InputsTable({ workspace }: { workspace: ProjectWorkspaceView }) {
  return (
    <section className="table-section">
      <SectionTitle title="输入材料" meta={`${workspace.sourceConnections.length} 项连接`} />
      <table>
        <thead>
          <tr>
            <th>来源</th>
            <th>类型</th>
            <th>状态</th>
            <th>说明</th>
          </tr>
        </thead>
        <tbody>
          {workspace.sourceConnections.map((source) => (
            <tr key={source.id}>
              <td>{source.label}</td>
              <td>{sourceKindLabel(source.kind)}</td>
              <td>
                <StatusPill label={sourceStatusLabel(source.status)} tone={source.status === "ready" ? "green" : "yellow"} />
              </td>
              <td>{source.detail}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}

function UnderstandingPanel({ workspace }: { workspace: ProjectWorkspaceView }) {
  return (
    <section className="split-section">
      <div>
        <SectionTitle title="产品理解" meta={workspace.understanding.productMapID} />
        <div className="evidence-list">
          {workspace.understanding.evidenceRefs.map((ref) => (
            <div key={ref.id} className="evidence-row">
              <span>{evidenceKindLabel(ref.kind)}</span>
              <strong>{ref.summary}</strong>
              <small>{Math.round((ref.confidence ?? 0) * 100)}%</small>
            </div>
          ))}
        </div>
      </div>
      <div className="warning-band">
        {workspace.understanding.sensitiveWarnings.map((warning) => (
          <span key={warning}>{warning}</span>
        ))}
      </div>
    </section>
  );
}

function GraphReviewTable({
  workspace,
  selectedNodeID,
  onSelectNode,
  onPatchNode,
}: {
  workspace: ProjectWorkspaceView;
  selectedNodeID: string;
  onSelectNode: (id: string) => void;
  onPatchNode: (id: string, patch: Partial<GraphNode>) => void;
}) {
  return (
    <section className="table-section">
      <SectionTitle title="执行方案" meta={`目标 ${workspace.planReview.targetDurationSec} 秒`} />
      <table>
        <thead>
          <tr>
            <th>步骤</th>
            <th>动作</th>
            <th>预期结果</th>
            <th>截图</th>
            <th>聚焦</th>
            <th>时长</th>
          </tr>
        </thead>
        <tbody>
          {workspace.planReview.graph.nodes.map((node, index) => (
            <tr key={node.id} className={selectedNodeID === node.id ? "selected-row" : ""} onClick={() => onSelectNode(node.id)}>
              <td>{index + 1}</td>
              <td>{node.title ?? node.action}</td>
              <td>{node.expected_outcome}</td>
              <td>
                <input
                  aria-label={`截图 ${node.id}`}
                  type="checkbox"
                  checked={node.is_screenshot}
                  onChange={(event) => onPatchNode(node.id, { is_screenshot: event.currentTarget.checked })}
                />
              </td>
              <td>
                <input
                  aria-label={`聚焦 ${node.id}`}
                  type="checkbox"
                  checked={node.has_zoom}
                  onChange={(event) => onPatchNode(node.id, { has_zoom: event.currentTarget.checked })}
                />
              </td>
              <td>{node.duration_hint_ms ?? 0} 毫秒</td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}

function PackageApproval({
  workspace,
  checklist,
  blockedReasons,
  canUpload,
  isLocalMode,
	runtimeHealth,
	runtimeHealthResolved,
  onChecklistChange,
  onUpload,
	onConfigureControlPlane,
  onCloudSuccess,
  onCloudFailure,
  onRepairScript,
}: {
  workspace: ProjectWorkspaceView;
  checklist: ApprovalChecklistState;
  blockedReasons: string[];
  canUpload: boolean;
  isLocalMode: boolean;
	runtimeHealth?: RuntimeHealthView;
	runtimeHealthResolved: boolean;
  onChecklistChange: (state: ApprovalChecklistState) => void;
  onUpload: () => void;
	onConfigureControlPlane: (baseURL: string, accessToken: string) => Promise<string>;
  onCloudSuccess: () => void;
  onCloudFailure: () => void;
  onRepairScript: () => void;
}) {
  function toggle(key: keyof ApprovalChecklistState) {
    onChecklistChange({ ...checklist, [key]: !checklist[key] });
  }
  const bundle = workspace.executableScriptBundle;
	const stages = [...(bundle?.stage_approval_plan?.stages ?? [])].sort((left, right) => left.order - right.order);
	const direct = runtimeHealth?.browserAgentDirect;
	const connected = direct?.configured === true && direct.tokenConfigured === true && direct.reachable === true;
	const requiredConfirmations = [
		!checklist.userApprovedPlan,
		!checklist.ipAllowlistAcknowledged && !workspace.packagePreview.ipAllowlistAcknowledged,
		!checklist.sourceSummaryOnlyAcknowledged,
		workspace.packagePreview.credentialGrants.length > 0 && !checklist.credentialGrantAcknowledged,
		!checklist.redactionsReviewed,
	].filter(Boolean).length;
	const systemBlockers = [
		...workspace.packagePreview.blockedReasons,
		...(workspace.packagePreview.readiness === "blocked" ? ["执行包证据确信度未达到上传要求，请返回理解阶段补充材料。"] : []),
		...workspace.sourceConnections.filter((source) => source.status === "blocked").map((source) => `${source.label} 仍处于阻塞状态。`),
	];
	const runtimeEvidencePending = workspace.packagePreview.confidenceWarnings?.some((warning) => warning.includes("Browser Agent") && warning.includes("运行时"));
	const readinessLabel = workspace.packagePreview.readiness === "ready" ? (runtimeEvidencePending ? "合同可执行" : "证据充分") : workspace.packagePreview.readiness === "review_required" ? (runtimeEvidencePending ? "合同待复核" : "需要重点复核") : "不可上传";

  return (
	<div className="section-stack approval-workspace">
	  <section className="approval-hero">
		<div><span className="eyebrow">Final review before upload</span><h2>确认服务器将看到和录制的内容</h2><p>这一步只批准当前摘要对应的执行包。任何内容变化都会使本次审批失效。</p></div>
		<div className={`approval-readiness ${workspace.packagePreview.readiness ?? "blocked"}`}><strong>{workspace.packagePreview.confidenceScore == null ? "—" : `${Math.round(workspace.packagePreview.confidenceScore * 100)}%`}</strong><span>{readinessLabel}</span></div>
	  </section>

	  <div className="approval-summary-grid">
		<ApprovalSummaryCard label="访问范围" value={`${workspace.planReview.allowedDomains.length} 个域名`} detail={workspace.planReview.allowedDomains.join("、") || "未声明允许域名"} />
		<ApprovalSummaryCard label="录制方案" value={`${stages.length} 个阶段 · ${workspace.planReview.targetDurationSec} 秒`} detail={workspace.planReview.outputRequests.map(assetKindLabel).join("、") || "演示视频"} />
		<ApprovalSummaryCard label="上传数据" value={workspace.packagePreview.sourceSummaryOnly ? "仅摘要与证据引用" : "上传范围异常"} detail={`精简后 ${workspace.packagePreview.totalBytes == null ? "待统计" : formatBytes(workspace.packagePreview.totalBytes)} · ${workspace.packagePreview.encrypted ? "加密传输" : "未加密"}`} />
		<ApprovalSummaryCard label="隐私保护" value={`${workspace.planReview.redactionSelectors.length} 条打码规则`} detail={`${workspace.planReview.forbiddenPages.length} 个禁止页面 · 不包含完整源码`} />
	  </div>

	  <section className="approval-section">
		<div className="approval-section-heading"><div><span>01</span><h3>录制步骤</h3></div><small>BrowserAgent 可以补全 selector 和等待策略，但不能改变阶段意图。</small></div>
		<div className="approval-stage-list">
		  {stages.length ? stages.map((stage, index) => (
			<article key={stage.id || stage.node_id} className="approval-stage-card">
			  <span>{String(index + 1).padStart(2, "0")}</span>
			  <div><strong>{stage.title || stage.objective || `录制阶段 ${index + 1}`}</strong><p>{stage.business_intent || stage.objective || "按已确认业务意图完成非破坏性交互。"}</p><small>{stage.target_route || stage.entry_route || stage.target_url || "运行时确认页面"} · {stage.success_state || "验证目标状态"}</small></div>
			  <em>{stage.duration_ms ? `${Math.max(1, Math.round(stage.duration_ms / 1000))}s` : "自动"}</em>
			</article>
		  )) : <div className="approval-empty">尚未生成可审批的录制阶段。</div>}
		</div>
	  </section>

	  <section className="approval-section approval-safety-section">
		<div className="approval-section-heading"><div><span>02</span><h3>账号、数据与禁止范围</h3></div><small>凭据只以 opaque ref 注入执行时浏览器上下文。</small></div>
		<div className="approval-safety-grid">
		  <div><span>账号权限</span><strong>{workspace.packagePreview.credentialGrants.length ? `${workspace.packagePreview.credentialGrants.length} 项临时授权` : "不使用演示账号"}</strong><p>{workspace.packagePreview.credentialGrants.map((grant) => `${credentialKindLabel(grant.kind)} · ${grant.purpose}`).join("；") || "本次执行包没有凭据引用。"}</p></div>
		  <div><span>禁止访问</span><strong>{workspace.planReview.forbiddenPages.length ? workspace.planReview.forbiddenPages.join("、") : "无额外页面"}</strong><p>执行端只允许访问上方已批准域名，不得执行破坏性操作。</p></div>
		  <div><span>打码范围</span><strong>{workspace.planReview.redactionSelectors.join("、") || "默认敏感字段策略"}</strong><p>截图、录屏、trace 和失败诊断使用相同脱敏规则。</p></div>
		</div>
	  </section>

	  {!runtimeHealthResolved || !connected ? <ControlPlaneConnectionCard {...(runtimeHealth ? { runtimeHealth } : {})} resolved={runtimeHealthResolved} onConfigure={onConfigureControlPlane} /> : <div className="approval-server-ready"><span className="status-dot ok" /><div><strong>Ubuntu Browser Agent 服务器已连接</strong><small>{browserAgentDirectLabel(runtimeHealth)}</small></div></div>}
	  <div className="approval-summary-grid">
		<ApprovalSummaryCard label="直连协议" value={direct?.reachable ? "健康检查通过" : "不可达"} detail={direct?.protocolVersion ?? "browser-agent-direct-v1"} />
		<ApprovalSummaryCard label="安全传输" value={direct?.tokenConfigured ? "令牌已入系统凭据库" : "未配置令牌"} detail={direct?.cryptoSuite ?? "AES-256-GCM + HKDF-SHA256"} />
		<ApprovalSummaryCard label="包预检" value={workspace.packagePreview.readiness === "ready" ? "已通过" : workspace.packagePreview.readiness === "review_required" ? "需复核" : "未通过"} detail={workspace.packagePreview.approvalSubjectDigest ? "当前 digest 已生成" : "等待生成正式 digest"} />
		<ApprovalSummaryCard label="上传与执行" value={workspace.cloudRun.cloudJobID ? cloudStatusLabel(workspace.cloudRun.status) : "尚未上传"} detail={workspace.cloudRun.cloudJobID ? `Browser Agent job ${workspace.cloudRun.cloudJobID.slice(-12)}` : "审批后才建立短期安全执行会话"} />
	  </div>

	  {workspace.packagePreview.confidenceWarnings?.length ? <div className="notice-card warning"><strong>{runtimeEvidencePending ? "运行时证据待采集" : "需要重点复核"}</strong><p>{workspace.packagePreview.confidenceWarnings.slice(0, 3).join("；")}</p></div> : null}
	  {workspace.cloudRun.blockingErrorCode === "reunderstanding_required" ? <div className="notice-card warning"><strong>原执行包审批已失效</strong><p>Browser Agent 的运行时事实表明当前业务理解需要重新生成。旧 Job 已终止；请根据下方结构化问题调整方案，并对新的 package ID 与 digest 重新审批。</p></div> : null}
	  {workspace.cloudRun.reunderstandingIssues?.length ? <div className="blocked-list">{workspace.cloudRun.reunderstandingIssues.map((issue) => <span key={`${issue.code}-${issue.stageID ?? "global"}-${issue.nodeID ?? "global"}`}>{issue.stageID ? `${issue.stageID} · ` : ""}{issue.summary || issue.code}{issue.nextStep ? `；下一步：${issue.nextStep}` : ""}</span>)}</div> : null}
	  {workspace.cloudRun.lastError ? <div className="error-banner">{workspace.cloudRun.lastError}</div> : null}
	  {systemBlockers.length ? <div className="blocked-list">{[...new Set(systemBlockers)].map((reason) => <span key={reason}>{reason}</span>)}</div> : null}

	  <section className="approval-section approval-confirm-section">
		<div className="approval-section-heading"><div><span>03</span><h3>逐项确认</h3></div><small>{requiredConfirmations ? `还有 ${requiredConfirmations} 项待确认` : "所有必需确认已完成"}</small></div>
		<div className="approval-confirm-list">
		  <ApprovalCheck checked={checklist.userApprovedPlan} label="录制范围与阶段顺序符合我的意图" onChange={() => toggle("userApprovedPlan")} />
		  <ApprovalCheck checked={checklist.sourceSummaryOnlyAcknowledged} label="确认只上传摘要、hash 和已批准证据，不上传完整源码" onChange={() => toggle("sourceSummaryOnlyAcknowledged")} />
		  {workspace.packagePreview.credentialGrants.length ? <ApprovalCheck checked={checklist.credentialGrantAcknowledged} label="已复核临时账号权限、用途和过期范围" onChange={() => toggle("credentialGrantAcknowledged")} /> : null}
		  <ApprovalCheck checked={checklist.redactionsReviewed} label="已复核禁止页面、禁止数据与打码策略" onChange={() => toggle("redactionsReviewed")} />
		  <ApprovalCheck checked={checklist.ipAllowlistAcknowledged || workspace.packagePreview.ipAllowlistAcknowledged} label="已确认目标环境允许 Ubuntu Browser Agent 服务器访问" onChange={() => toggle("ipAllowlistAcknowledged")} />
		</div>
	  </section>

	  <div className="approval-submit-bar">
		<div><strong>{canUpload ? "可以提交到执行服务器" : "完成连接与必要确认后才能上传"}</strong><span>Configuration 确认与这次执行审批保持独立。</span></div>
		<button type="button" className="primary-action" disabled={!canUpload} onClick={onUpload}>审批当前版本并上传</button>
	  </div>

	  {runtimeHealth?.appCapabilities?.developerUI ? <details className="approval-technical-details"><summary>开发者技术详情</summary><div className="section-stack"><RuntimeLogPanel workspace={workspace} /><CloudRunPanel workspace={workspace} /><SandboxPolicyPanel workspace={workspace} /><ScriptBundleReview workspace={workspace} /></div></details> : null}
	  {workspace.cloudRun.failureDiagnostic ? (
		<FailureDiagnosticPanel workspace={workspace} onRepairScript={onRepairScript} />
	  ) : null}
	  {!isLocalMode ? <div className="action-row"><button type="button" className="secondary-action" onClick={onCloudSuccess}>模拟完成</button><button type="button" className="secondary-action" onClick={onCloudFailure}>模拟失败诊断</button></div> : null}
	</div>
  );
}

function ApprovalSummaryCard({ label, value, detail }: { label: string; value: string; detail: string }) {
	return <article className="approval-summary-card"><span>{label}</span><strong>{value}</strong><p>{detail}</p></article>;
}

function ApprovalCheck({ checked, label, onChange }: { checked: boolean; label: string; onChange: () => void }) {
	return <label className={`approval-check ${checked ? "checked" : ""}`}><input type="checkbox" checked={checked} onChange={onChange} /><span>{checked ? "✓" : ""}</span><strong>{label}</strong></label>;
}

function ControlPlaneConnectionCard({ runtimeHealth, resolved, onConfigure }: { runtimeHealth?: RuntimeHealthView; resolved: boolean; onConfigure: (baseURL: string, accessToken: string) => Promise<string> }) {
	const [baseURL, setBaseURL] = useState("");
	const [accessToken, setAccessToken] = useState("");
	const [saving, setSaving] = useState(false);
	const [error, setError] = useState("");
	async function submit(event: FormEvent<HTMLFormElement>) {
		event.preventDefault();
		setSaving(true);
		const nextError = await onConfigure(baseURL.trim(), accessToken);
		setError(nextError);
		if (!nextError) setAccessToken("");
		setSaving(false);
	}
	return <section className="approval-server-card">
		<div><span>{resolved ? runtimeHealth?.browserAgentDirect?.configured ? "配置已保存，协议尚未就绪" : "需要连接" : "正在检查"}</span><strong>{resolved ? "连接 Ubuntu Browser Agent 服务器" : "正在读取执行服务器配置…"}</strong><p>访问令牌只写入操作系统凭据库。当前包审批通过后，App 才建立短期加密执行会话并上传。</p></div>
		<form onSubmit={submit}><input value={baseURL} onChange={(event) => setBaseURL(event.currentTarget.value)} placeholder="https://browser-agent.example:18443" aria-label="Browser Agent 控制地址" autoComplete="url" disabled={!resolved || saving} /><input type="password" value={accessToken} onChange={(event) => setAccessToken(event.currentTarget.value)} placeholder="服务器访问令牌（仅保存到系统凭据库）" aria-label="Browser Agent 访问令牌" autoComplete="off" disabled={!resolved || saving} /><button type="submit" className="primary-action" disabled={!resolved || saving || !baseURL.trim() || !accessToken.trim()}>{saving ? "验证中…" : "保存并验证"}</button></form>
		{error ? <div className="error-banner">{error}</div> : null}
		{runtimeHealth?.browserAgentDirect?.controlURLHost ? <small>当前：{browserAgentDirectLabel(runtimeHealth)}</small> : null}
	</section>;
}

function ServerLifecyclePanel({ workspace }: { workspace: ProjectWorkspaceView }) {
  const stages = lifecycleStagesFromWorkspace(workspace);
  return (
    <section className="table-section">
      <SectionTitle title="服务器执行生命周期" meta={workspace.cloudRun.stage ?? cloudStatusLabel(workspace.cloudRun.status)} />
      <div className="lifecycle-grid">
        {stages.map((stage) => (
          <div key={stage.id} className={`lifecycle-card ${stage.status}`}>
            <div className="lifecycle-card-head">
              <strong>{stage.label}</strong>
              <StatusPill label={lifecycleStatusLabel(stage.status)} tone={lifecycleStatusTone(stage.status)} />
            </div>
            <div className="progress-track">
              <span style={{ width: `${Math.max(0, Math.min(stage.progress, 100))}%` }} />
            </div>
            <p>{stage.summary}</p>
            <div className="lifecycle-meta">
              <span>{stage.time ?? "待执行"}</span>
              {stage.artifactCount ? <span>{stage.artifactCount} 个产物</span> : null}
              {stage.errorCode ? <span>需要处理</span> : null}
            </div>
          </div>
        ))}
      </div>
      {workspace.cloudRun.failureSummary ? <div className="error-banner">{workspace.cloudRun.failureSummary}</div> : null}
    </section>
  );
}

function SandboxPolicyPanel({ workspace }: { workspace: ProjectWorkspaceView }) {
  const policy = workspace.scriptDocument?.recording_run_spec.sandbox_policy ?? workspace.executableScriptBundle?.plan_json.recording_run_spec.sandbox_policy;
  const metadata = workspace.cloudRun.sandboxMetadata ?? workspace.cloudRun.resultPackage?.execution_trace?.sandbox ?? workspace.cloudRun.resultPackage?.audit_trail?.sandbox;
  const risk = sandboxRiskLevel(policy);
  return (
    <section className="table-section">
      <SectionTitle title="沙箱策略" meta={policy ? sandboxProfileLabel(policy.profile) : "待生成"} />
      <div className="sandbox-layout">
        <div className="settings-grid">
          <Fact label="运行模式" value={sandboxProfileLabel(policy?.profile)} />
          <Fact label="隔离方式" value={policy?.isolation_mode ?? "未声明"} />
          <Fact label="网络出口" value={policy?.network_policy.mode ?? "未声明"} />
          <Fact label="Proxy 强制" value={policy?.network_policy.proxy_required ? "是" : "否"} />
          <Fact label="允许域名" value={(policy?.network_policy.allowed_domains ?? workspace.planReview.allowedDomains).join("、")} />
          <Fact label="禁止页面" value={workspace.planReview.forbiddenPages.join("、") || "无"} />
          <Fact label="资源限制" value={resourceLimitLabel(policy)} />
          <Fact label="浏览器上下文" value={policy?.browser_policy.fresh_context_per_run ? "每次运行全新 context" : "未强制"} />
          <Fact label="凭据注入" value={policy?.secret_policy.vault_only ? "仅 vault + ctx.secrets" : "需复核"} />
          <Fact label="Artifact 加密" value={policy?.artifact_policy.encrypt_sensitive_artifacts ? "敏感产物强制加密" : "需复核"} />
          <Fact label="诊断脱敏" value={policy?.diagnostic_policy.redaction_required && policy.diagnostic_policy.encrypt_diagnostics ? "脱敏后加密返回" : "需复核"} />
          <Fact label="Policy Hash" value={policy?.policy_hash_sha256 ?? metadata?.policy_hash_sha256 ?? "待生成"} />
        </div>
        <div className={`sandbox-risk ${risk}`}>
          <StatusPill label={risk === "ok" ? "生产推荐" : "需注意"} tone={risk === "ok" ? "green" : "yellow"} />
          <strong>{sandboxRiskMessage(policy)}</strong>
          <span>Worker: {metadata?.worker_id ?? "等待执行"}</span>
          <span>Container: {metadata?.container_id ?? metadata?.micro_vm_id ?? "等待分配"}</span>
          <span>Runtime: {metadata?.runtime_versions ? Object.entries(metadata.runtime_versions).map(([key, value]) => `${key} ${value}`).join(" / ") : "等待上报"}</span>
        </div>
      </div>
    </section>
  );
}

function RuntimeLogPanel({ workspace }: { workspace: ProjectWorkspaceView }) {
  const logs = workspace.runtimeLogs ?? [];
  const lastError = workspace.cloudRun.lastError;
  if (logs.length === 0 && !lastError) {
    return null;
  }
  return (
    <section className="table-section">
      <SectionTitle title="本地运行日志" meta={lastError ? "存在错误" : `${logs.length} 条`} />
      {lastError ? <div className="error-banner">{lastError}</div> : null}
      <div className="runtime-log-list">
        {logs.map((entry) => (
          <div key={entry.id} className={`runtime-log-row ${entry.level}`}>
            <span>{entry.time}</span>
            <strong>{entry.node ? `${entry.node} · ${entry.message}` : entry.message}</strong>
            <small>
              {entry.detail ?? ""}
              {typeof entry.elapsedMS === "number" ? `${entry.detail ? " · " : ""}${entry.elapsedMS}ms` : ""}
            </small>
          </div>
        ))}
      </div>
    </section>
  );
}

function FailureDiagnosticPanel({ workspace, onRepairScript }: { workspace: ProjectWorkspaceView; onRepairScript: () => void }) {
  const diagnostic = workspace.cloudRun.failureDiagnostic;
  const repairRequest = workspace.cloudRun.repairRequest;
  const lineage = workspace.executableScriptBundle?.repair_lineage;
  if (!diagnostic) {
    return null;
  }
  return (
    <section className="table-section">
      <SectionTitle title="脚本失败诊断" meta={repairRequest?.approval_required ? "修复后必须重新审批" : "待确认"} />
      <div className="settings-grid">
        <Fact label="失败节点" value={diagnostic.failed_node_id} />
        <Fact label="错误信息" value={diagnostic.error.message} />
        <Fact label="当前页面" value={diagnostic.current_url ?? "未知"} />
        <Fact label="打码状态" value={diagnostic.redaction_report.applied && !diagnostic.redaction_report.full_html_included ? "已脱敏" : "需复核"} />
        <Fact label="失败 Result" value={repairRequest?.source_result_id ?? lineage?.source_result_id ?? "待生成"} />
        <Fact label="Cloud Job" value={repairRequest?.cloud_job_id ?? lineage?.source_cloud_job_id ?? diagnostic.cloud_job_id} />
        <Fact label="Base Bundle" value={repairRequest?.failed_bundle_hash_sha256 ?? lineage?.base_bundle_hash_sha256 ?? "待记录"} />
        <Fact label="Repair Attempt" value={`${repairRequest?.repair_attempt ?? lineage?.repair_attempt ?? 0}`} />
      </div>
      <table>
        <thead>
          <tr>
            <th>诊断资产</th>
            <th>类型</th>
            <th>敏感</th>
            <th>校验</th>
          </tr>
        </thead>
        <tbody>
          {[...(diagnostic.screenshot_refs ?? []), ...(diagnostic.trace_refs ?? [])].map((artifact) => (
            <tr key={artifact.id}>
              <td>{artifact.role ?? artifact.id}</td>
              <td>{artifact.kind}</td>
              <td>{artifact.sensitive ? "是" : "否"}</td>
              <td>{artifact.sha256}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <div className="blocked-list">
        {(diagnostic.repair_hints ?? []).map((hint) => (
          <span key={`${hint.kind}_${hint.node_id ?? hint.summary}`}>{hint.summary}</span>
        ))}
      </div>
      <div className="action-row">
        <button type="button" className="primary-action" onClick={onRepairScript}>
          <span className="button-icon">修</span>
          生成修复包
        </button>
      </div>
    </section>
  );
}

function ScriptBundleReview({ workspace }: { workspace: ProjectWorkspaceView }) {
  const bundle = workspace.executableScriptBundle;
  const plan = bundle?.plan_json ?? workspace.scriptDocument;
  const markdown = bundle?.approval_markdown.inline_markdown ?? workspace.scriptMarkdown ?? "执行包生成后展示中文思路文档。";
  const planJSON = plan ? JSON.stringify(plan, null, 2) : "执行包生成后展示 JSON 执行计划。";
  const isOutlineRuntime = bundle?.script_manifest.runtime === "browser-agent-outline-v1";
  const stagePlanJSON = bundle?.stage_approval_plan ? JSON.stringify(bundle.stage_approval_plan, null, 2) : "生成后展示用户审批用 Stage JSON。";
  const outlineJSON = bundle?.script_outline ? JSON.stringify(bundle.script_outline, null, 2) : "生成后展示 Browser Agent 脚本大纲。";
  const promptPolicyJSON = bundle?.agent_prompt_policy ? JSON.stringify(bundle.agent_prompt_policy, null, 2) : "生成后展示可修改/不可修改规则。";
  const agentContractJSON = bundle?.browser_agent_contract ? JSON.stringify(bundle.browser_agent_contract, null, 2) : "生成后展示 Browser Agent 执行/修复合同。";
  const dossierJSON = bundle?.project_understanding_dossier ? JSON.stringify(bundle.project_understanding_dossier, null, 2) : "生成后展示需求相关项目理解证据。";
  const source = bundle?.playwright_script.inline_source ?? "legacy TS 模式下展示受限 TypeScript 脚本；Browser Agent 大纲模式不要求 App 生成完整 TS。";
  const bundleBytes = bundle ? new Blob([JSON.stringify(bundle)]).size : 0;
  const stageCount = bundle?.stage_approval_plan?.stages?.length ?? 0;
  const targetContractCount = bundle?.stage_approval_plan?.stages?.filter((stage) => stage.target_contract?.semantic_id).length ?? 0;
  return (
    <section className="script-review-section">
      <SectionTitle title="脚本包审批内容" meta={isOutlineRuntime ? "Browser Agent 受约束路线图" : bundle?.schema_version ?? "待生成"} />
      {bundle ? (
        <div className="script-review-summary">
          <span>runtime: {bundle.script_manifest.runtime}</span>
          <span>bundle: {formatBytes(bundleBytes)}</span>
          <span>target contract: {targetContractCount}/{stageCount}</span>
          <span>{bundle.browser_agent_contract ? "Browser Agent 合同已绑定" : "缺少 Browser Agent 合同"}</span>
        </div>
      ) : null}
      <div className="script-review-grid">
        <ReviewPanel title="思路文档" meta="默认审批视图" content={markdown} />
        {isOutlineRuntime ? (
          <>
            <ReviewPanel title="Stage JSON" meta={bundle?.stage_approval_plan?.schema_version ?? "未生成"} content={stagePlanJSON} />
            <ReviewPanel title="Browser Agent 大纲" meta={bundle?.script_outline?.runtime ?? "browser-agent-outline-v1"} content={outlineJSON} />
            <ReviewPanel title="Browser Agent 合同" meta={bundle?.browser_agent_contract?.schema_version ?? "未生成"} content={agentContractJSON} />
            <ReviewPanel title="可修改规则" meta={bundle?.agent_prompt_policy?.schema_version ?? "未生成"} content={promptPolicyJSON} />
            <ReviewPanel title="证据链" meta={bundle?.project_understanding_dossier?.schema_version ?? "未生成"} content={dossierJSON} />
          </>
        ) : (
          <>
            <ReviewPanel title="执行计划 JSON" meta={plan?.schema_version ?? "未生成"} content={planJSON} />
            <ReviewPanel title="可执行 TS 脚本" meta={bundle?.script_manifest.entry_function ?? "runCascadeRecording"} content={source} />
          </>
        )}
      </div>
    </section>
  );
}

function ReviewPanel({ title, meta, content }: { title: string; meta: string; content: string }) {
  return (
    <div className="review-panel">
      <div className="review-panel-header">
        <strong>{title}</strong>
        <span>{meta}</span>
      </div>
      <pre>{content}</pre>
    </div>
  );
}

function formatBytes(bytes: number) {
  if (!Number.isFinite(bytes) || bytes <= 0) {
    return "0 B";
  }
  if (bytes < 1024) {
    return `${bytes} B`;
  }
  if (bytes < 1024 * 1024) {
    return `${(bytes / 1024).toFixed(1)} KB`;
  }
  return `${(bytes / 1024 / 1024).toFixed(2)} MB`;
}

function formatTimestamp(value: string): string {
	const parsed = new Date(value);
	return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString("zh-CN", { hour12: false });
}

function AssetReview({
  workspace,
  busy = false,
  showDownloadAction = true,
  onDownload,
  onReleaseLease,
  onReview,
}: {
  workspace: ProjectWorkspaceView;
  busy?: boolean;
  showDownloadAction?: boolean;
  onDownload: () => void;
  onReleaseLease: () => void;
  onReview: (decision: "approved" | "reedit_requested" | "rerecord_requested", summary?: string) => void;
}) {
  const [reviewSummary, setReviewSummary] = useState("");
  const review = workspace.cloudRun.resultReview;
  const videoAsset = workspace.assets.find((asset) => asset.kind === "video");
  const hasResult = Boolean(workspace.cloudRun.resultPackageID || workspace.cloudRun.resultPackage);
  const canReview = hasResult && workspace.cloudRun.resultDownloaded === true && workspace.cloudRun.resultAcknowledged === true;
  const revisionSummaryRequired = reviewSummary.trim().length === 0;
  return (
    <div className="asset-layout">
      <section className="video-panel">
        <SectionTitle title="演示视频" meta={workspace.cloudRun.resultAcknowledged ? "已校验并 ACK" : workspace.cloudRun.resultDownloaded ? "已校验，等待 ACK" : hasResult ? "等待下载" : "待生成"} />
		{workspace.cloudRun.lastError ? <div className="error-banner" role="alert"><strong>操作未完成</strong><span>{workspace.cloudRun.lastError}</span></div> : null}
		{videoAsset?.mediaURL ? <video className="review-video" controls preload="metadata" src={videoAsset.mediaURL}>当前环境无法播放该视频。</video> : <div className="video-frame"><div className="play-symbol">{hasResult ? "待下载" : "生成中"}</div><span>{videoAsset?.title ?? "最终演示视频"}</span></div>}
      </section>
      <section className="docs-panel">
        <SectionTitle title="步骤文档" meta={workspace.assets[1]?.checksum ?? "待生成"} />
        <ol className="docs-preview">
          {workspace.planReview.graph.nodes.map((node) => (
            <li key={node.id}>
              <strong>{node.title ?? node.action}</strong>
              <span>{node.expected_outcome}</span>
            </li>
          ))}
        </ol>
        <div className="result-review-actions">
          {showDownloadAction ? <button type="button" className="primary-action" disabled={busy || !hasResult || workspace.cloudRun.resultAcknowledged} onClick={onDownload}>{busy ? "正在安全接收…" : workspace.cloudRun.resultAcknowledged ? "成品已确认接收，可以播放审核" : workspace.cloudRun.resultDownloaded ? "重试服务器 ACK" : "下载、校验并确认接收"}</button> : workspace.cloudRun.resultAcknowledged ? <div className="input-note">成片已安全下载、校验并完成服务器 ACK，可以播放和提交审核决定。</div> : null}
          <label className="field-row">
            <span>审核意见</span>
            <textarea rows={3} value={reviewSummary} onChange={(event) => setReviewSummary(event.currentTarget.value)} placeholder="可对时间点、字幕、节奏或缺失素材进行说明" />
          </label>
          <div className="action-row">
            <button type="button" className="primary-action" disabled={busy || !canReview} onClick={() => onReview("approved", reviewSummary)}>通过</button>
            <button type="button" className="secondary-action" disabled={busy || !canReview || revisionSummaryRequired} onClick={() => onReview("reedit_requested", reviewSummary)}>需要重新剪辑</button>
            <button type="button" className="secondary-action" disabled={busy || !canReview || revisionSummaryRequired} onClick={() => onReview("rerecord_requested", reviewSummary)}>缺少素材，重新录制</button>
          </div>
          {canReview && revisionSummaryRequired ? <div className="input-note">提交重新剪辑或重新录制前，请填写具体修改意见；直接通过无需填写。</div> : null}
          {workspace.cloudRun.leaseID ? <button type="button" className="secondary-action" disabled={busy || !canReview || workspace.cloudRun.status === "running"} onClick={onReleaseLease}>释放已完成任务的安全执行会话</button> : null}
          {review ? <div className="input-note">已提交：{review.decision === "approved" ? "通过" : review.decision === "reedit_requested" ? "重新剪辑" : "重新录制"}{review.summary ? ` · ${review.summary}` : ""}</div> : null}
        </div>
      </section>
    </div>
  );
}

function SettingsPanel({
  workspace,
  runtimeHealth,
  diagnostics,
  diagnosticsError,
  isRunningDiagnostics,
  onRunDiagnostics,
  onConfigureControlPlane,
  onConfigurePlanningModel,
  onDeletePlanningModel,
  developerUI,
}: {
  workspace: ProjectWorkspaceView;
  runtimeHealth?: RuntimeHealthView;
  diagnostics: ModelDiagnosticResult[];
  diagnosticsError: string;
  isRunningDiagnostics: boolean;
  onRunDiagnostics: () => void;
  onConfigureControlPlane: (baseURL: string, accessToken: string) => Promise<string>;
  onConfigurePlanningModel: (provider: string, model: string, apiKey: string, proxyURL: string) => Promise<string>;
  onDeletePlanningModel: (provider: string) => Promise<string>;
  developerUI: boolean;
}) {
  const [controlPlaneURL, setControlPlaneURL] = useState("");
  const [controlPlaneAccessToken, setControlPlaneAccessToken] = useState("");
  const [controlPlaneError, setControlPlaneError] = useState("");
  const [controlPlaneSaving, setControlPlaneSaving] = useState(false);
  const [modelProvider, setModelProvider] = useState("kimi");
  const [modelName, setModelName] = useState("kimi-k2.7-code");
  const [modelAPIKey, setModelAPIKey] = useState("");
  const [modelProxyURL, setModelProxyURL] = useState("");
  const [modelSettingsError, setModelSettingsError] = useState("");
  const [modelSettingsSaving, setModelSettingsSaving] = useState(false);
  const providerRows = [
    ["GLM", "glm"],
    ["Kimi", "kimi"],
    ["MiniMax", "minimax"],
    ["Seedance", "seedance"],
    ["豆包 / Ark", "doubao"],
    ["DeepSeek", "deepseek"],
  ] as const;
  const routeLabels: Record<string, string> = {
    planning: "计划生成",
    code_reading: "代码阅读",
    multimodal_understanding: "多模态理解",
    video_operation: "视频操作",
  };
  const routeRows = Object.entries(runtimeHealth?.modelTaskRoutes ?? {}).map(([task, route]) => [
    routeLabels[task] ?? task,
    route.provider,
    route.model,
  ]);
  const directLabel = browserAgentDirectLabel(runtimeHealth);
  const planningRoute = runtimeHealth?.modelTaskRoutes.planning;
  const planningProvider = planningRoute ? runtimeHealth?.modelProviders[planningRoute.provider] : undefined;
  const planningDiagnostic = planningRoute
    ? diagnostics.find((item) => item.task === "planning" && item.provider === planningRoute.provider && item.model === planningRoute.model)
    : undefined;
  const planningModelStatus = !planningProvider?.configured
    ? { label: "规则兜底可用", tone: "neutral" as const, detail: "尚未保存真实模型凭据。" }
    : planningDiagnostic?.ok
      ? { label: "真实 LLM 已验证", tone: "green" as const, detail: `${planningRoute?.provider} / ${planningRoute?.model} · ${planningDiagnostic.latencyMS ?? 0}ms` }
      : planningDiagnostic
        ? { label: "实际调用失败，规则兜底中", tone: "yellow" as const, detail: planningDiagnostic.errorClass ?? planningDiagnostic.error ?? "provider_error" }
        : { label: "凭据已保存，等待验证", tone: "yellow" as const, detail: `${planningRoute?.provider ?? modelProvider} / ${planningRoute?.model ?? modelName}` };
  async function saveControlPlane(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setControlPlaneSaving(true);
    const error = await onConfigureControlPlane(controlPlaneURL.trim(), controlPlaneAccessToken);
    setControlPlaneError(error);
    if (!error) {
	  setControlPlaneURL("");
	  setControlPlaneAccessToken("");
	}
    setControlPlaneSaving(false);
  }
  async function savePlanningModel(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setModelSettingsSaving(true);
    const error = await onConfigurePlanningModel(modelProvider, modelName.trim(), modelAPIKey, modelProxyURL.trim());
    setModelSettingsError(error);
    if (!error) setModelAPIKey("");
    setModelSettingsSaving(false);
  }
  async function removePlanningModel() {
    setModelSettingsSaving(true);
    const error = await onDeletePlanningModel(modelProvider);
    setModelSettingsError(error);
    setModelAPIKey("");
    setModelSettingsSaving(false);
  }
  return (
    <div className="section-stack">
      <SectionTitle title="设置" meta="运行时正常" />
      <div className="settings-grid">
        <Fact label="凭据保险箱" value="仅保存本地引用" />
        <Fact label="资产存储" value="本地文件存储" />
        <Fact label="允许域名" value={workspace.planReview.allowedDomains.join(", ")} />
        <Fact label="禁止页面" value={workspace.planReview.forbiddenPages.join(", ")} />
        <Fact label="LLM 模式" value={runtimeHealth?.llmMode ?? "auto"} />
        <Fact label="模型适配版本" value={runtimeHealth?.modelAdapterVersion ?? "domestic-llm-adapter-v1"} />
        <Fact label="Browser Agent 直连" value={directLabel} />
      </div>
      <section className="table-section">
        <SectionTitle title="Ubuntu Browser Agent 服务器" meta={runtimeHealth?.browserAgentDirect?.reachable ? "协议可达" : runtimeHealth?.browserAgentDirect?.configured ? "配置已保存，未连通" : "需要连接"} />
        <form className="control-plane-form" onSubmit={saveControlPlane}>
          <div><strong>{runtimeHealth?.browserAgentDirect?.configured ? `${runtimeHealth.browserAgentDirect.controlURLHost ?? "Browser Agent server"}${runtimeHealth.browserAgentDirect.controlURLPath ?? ""}` : "配置 Browser Agent 直连网关"}</strong><span>使用服务器的 HTTPS 控制地址。令牌只保存到 Windows Credential Manager；执行包审批后才建立短期安全执行会话。此处不接收 SSH 密码。</span></div>
          <input value={controlPlaneURL} onChange={(event) => setControlPlaneURL(event.currentTarget.value)} placeholder="https://browser-agent.example:18443" aria-label="Browser Agent 控制地址" autoComplete="url" />
		  <input type="password" value={controlPlaneAccessToken} onChange={(event) => setControlPlaneAccessToken(event.currentTarget.value)} placeholder="访问令牌（仅保存到系统凭据库）" aria-label="Browser Agent 访问令牌" autoComplete="off" />
          <button type="submit" className="primary-action" disabled={controlPlaneSaving || !controlPlaneURL.trim() || !controlPlaneAccessToken.trim()}>{controlPlaneSaving ? "验证中…" : runtimeHealth?.browserAgentDirect?.reachable ? "更换服务器" : runtimeHealth?.browserAgentDirect?.configured ? "重新验证" : "保存并验证"}</button>
        </form>
        {controlPlaneError ? <div className="error-banner" role="alert">{controlPlaneError}</div> : null}
      </section>
      <section className="table-section">
        <SectionTitle title="Cascade Agent 模型" meta={planningModelStatus.label} />
        <form className="model-connection-form" onSubmit={savePlanningModel}>
          <div><strong>连接真实 LLM</strong><span>API key 只保存到 Windows Credential Manager，不进入项目、聊天、日志或安装包。未配置或临时不可用时，Cascade 会明确使用规则兜底。</span></div>
          <label><span>供应商</span><select value={modelProvider} onChange={(event) => { const provider = event.currentTarget.value; setModelProvider(provider); setModelName(provider === "glm" ? "glm-5.2" : provider === "minimax" ? "minimax-m3" : provider === "deepseek" ? "deepseek-v4-flash" : "kimi-k2.7-code"); }}><option value="kimi">Kimi</option><option value="glm">GLM</option><option value="minimax">MiniMax</option><option value="deepseek">DeepSeek</option></select></label>
          <label><span>模型</span><input required value={modelName} onChange={(event) => setModelName(event.currentTarget.value)} autoComplete="off" /></label>
          <label><span>API Key</span><input required type="password" value={modelAPIKey} onChange={(event) => setModelAPIKey(event.currentTarget.value)} autoComplete="off" placeholder="仅保存到系统凭据库" /></label>
          <label><span>网络代理（可选）</span><input type="url" value={modelProxyURL} onChange={(event) => setModelProxyURL(event.currentTarget.value)} autoComplete="off" placeholder="http://127.0.0.1:7892" /></label>
          <div className="model-connection-actions"><button type="submit" className="primary-action" disabled={modelSettingsSaving || !modelName.trim() || !modelAPIKey}>{modelSettingsSaving ? "保存中…" : "保存并启用"}</button><button type="button" className="secondary-action" disabled={modelSettingsSaving || !runtimeHealth?.modelProviders[modelProvider]?.configured} onClick={() => void removePlanningModel()}>移除凭据</button></div>
        </form>
        <div className="model-verification-status" aria-live="polite">
          <StatusPill label={planningModelStatus.label} tone={planningModelStatus.tone} />
          <span>{planningModelStatus.detail}</span>
          <button type="button" className="secondary-action" onClick={onRunDiagnostics} disabled={isRunningDiagnostics || !planningProvider?.configured}>{isRunningDiagnostics ? "验证中…" : "验证实际调用"}</button>
        </div>
        {modelSettingsError ? <div className="error-banner" role="alert">{modelSettingsError}</div> : null}
        {diagnosticsError ? <div className="error-banner" role="alert">{diagnosticsError}</div> : null}
      </section>
      {developerUI ? <section className="table-section">
        <SectionTitle title="模型供应商凭据" meta="仅显示占位状态" />
        <table>
          <thead>
            <tr>
              <th>供应商</th>
              <th>API Key 变量</th>
              <th>Fallback</th>
              <th>Base URL</th>
              <th>状态</th>
            </tr>
          </thead>
          <tbody>
            {providerRows.map(([name, providerKey]) => {
              const status = runtimeHealth?.modelProviders[providerKey];
              const fallback = status?.apiKeyFallbackEnvs?.join(", ") || "无";
              const source = status?.apiKeySourceEnv ? `使用 ${status.apiKeySourceEnv}` : "待配置";
              return (
                <tr key={providerKey}>
                  <td>{name}</td>
                  <td>{status?.apiKeyEnv ?? providerKey}</td>
                  <td>{fallback}</td>
                  <td>{status?.baseURLConfigured ? "已预设" : "待配置"}</td>
                  <td>
                    <StatusPill label={status?.configured ? source : "待配置"} tone={status?.configured ? "green" : "neutral"} />
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </section> : null}
      {developerUI ? <section className="table-section">
        <SectionTitle title="默认模型路由" meta="可通过环境变量覆盖" />
        <table>
          <thead>
            <tr>
              <th>任务</th>
              <th>供应商</th>
              <th>模型</th>
            </tr>
          </thead>
          <tbody>
            {(routeRows.length > 0 ? routeRows : [["计划生成", "kimi", "kimi-k2.7-code"], ["代码阅读", "glm", "glm-5.2"], ["多模态理解", "minimax", "minimax-m3"], ["视频操作", "seedance", "seedance-2.0"]]).map(([task, provider, modelName]) => (
              <tr key={task}>
                <td>{task}</td>
                <td>{provider}</td>
                <td>{modelName}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </section> : null}
      {developerUI ? <section className="table-section">
        <SectionTitle title="真实模型诊断" meta="只返回脱敏状态" />
        <div className="action-row">
          <button type="button" className="secondary-action" onClick={onRunDiagnostics} disabled={isRunningDiagnostics}>
            <span className="button-icon">测</span>
            {isRunningDiagnostics ? "诊断中" : "运行诊断"}
          </button>
          <small>{diagnosticsError || "用于确认 Kimi/GLM/MiniMax 等真实模型是否可调用。"}</small>
        </div>
        <table>
          <thead>
            <tr>
              <th>任务</th>
              <th>供应商</th>
              <th>模型</th>
              <th>Base</th>
              <th>结果</th>
              <th>错误</th>
            </tr>
          </thead>
          <tbody>
            {(diagnostics.length > 0 ? diagnostics : []).map((item) => (
              <tr key={`${item.provider}-${item.task ?? item.model}`}>
                <td>{routeLabels[item.task ?? ""] ?? item.task ?? "未绑定任务"}</td>
                <td>{item.provider}</td>
                <td>{item.model}</td>
                <td>{[item.baseURLHost, item.baseURLPath].filter(Boolean).join("") || "未配置"}</td>
                <td>
                  <StatusPill
                    label={item.ok ? `可调用 ${item.latencyMS ?? 0}ms` : item.configured ? `失败${item.httpStatus ? ` ${item.httpStatus}` : ""}` : "未配置"}
                    tone={item.ok ? "green" : item.configured ? "yellow" : "neutral"}
                  />
                </td>
                <td>{item.errorClass ?? item.error ?? "无"}</td>
              </tr>
            ))}
            {diagnostics.length === 0 ? (
              <tr>
                <td colSpan={6}>尚未运行诊断。</td>
              </tr>
            ) : null}
          </tbody>
        </table>
      </section> : null}
    </div>
  );
}

function Inspector({
	workspace,
	selectedNode,
	blockedReasons,
}: {
	workspace: ProjectWorkspaceView;
	selectedNode: GraphNode | undefined;
	blockedReasons: string[];
}) {
  return (
    <aside className="inspector" aria-label="检查器">
      <SectionTitle title="检查器" meta={selectedNode?.id ?? workspace.id} />
      {selectedNode ? (
        <div className="inspector-block">
          <Fact label="节点" value={selectedNode.title ?? selectedNode.action} />
          <Fact label="选择器" value={selectedNode.selector || "页面跳转"} />
          <Fact label="预期结果" value={selectedNode.expected_outcome} />
          <Fact label="时长" value={`${selectedNode.duration_hint_ms ?? 0} 毫秒`} />
        </div>
      ) : null}
      <div className="inspector-block">
        <SectionTitle title="安全策略" meta={`${blockedReasons.length} 项阻塞`} />
        <div className="token-list">
          {workspace.planReview.redactionSelectors.map((selector) => (
            <span key={selector}>{selector}</span>
          ))}
        </div>
      </div>
      <div className="inspector-block">
        <SectionTitle title="云端录制" meta={cloudStatusLabel(workspace.cloudRun.status)} />
        <div className="progress-track">
          <span style={{ width: `${workspace.cloudRun.progress}%` }} />
        </div>
        <small>{workspace.cloudRun.currentStep}</small>
      </div>
    </aside>
  );
}

function SectionTitle({ title, meta }: { title: string; meta?: string }) {
  return (
    <div className="section-title">
      <h2>{title}</h2>
      {meta ? <span>{meta}</span> : null}
    </div>
  );
}

function StatusPill({ label, tone }: { label: string; tone: "blue" | "green" | "yellow" | "neutral" }) {
  return <span className={`status-pill ${tone}`}>{label}</span>;
}

function Fact({ label, value }: { label: string; value: string }) {
  return (
    <div className="fact-row">
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

function scenarioLabel(id: ScenarioID): string {
  const labels: Record<ScenarioID, string> = {
    product_demo: "产品演示",
    ai_customer_service_demo: "AI 客服演示",
    internal_onboarding_tutorial: "企业新人教程",
  };
  return labels[id];
}

function buildNavItems(developerUI: boolean): Array<{ id: NavSection; label: string; badge?: string }> {
  return [
    { id: "projects", label: "Home" },
    { id: "project_library", label: "Projects" },
    { id: "editor", label: "Editor" },
    { id: "settings", label: "Settings" },
    ...(developerUI ? [{ id: "repositories" as const, label: "Repositories (Dev)" }] : []),
  ];
}

function clearCloudRunError(workspace: ProjectWorkspaceView): ProjectWorkspaceView {
  const { lastError: _lastError, ...cloudRun } = workspace.cloudRun;
  return { ...workspace, cloudRun };
}

function bestEvidenceConfidence(workspace: ProjectWorkspaceView): number {
  if (workspace.understanding.evidenceRefs.length === 0) {
    return 0;
  }
  return Math.max(...workspace.understanding.evidenceRefs.map((ref) => ref.confidence ?? 0));
}

function sourceKindLabel(kind: string): string {
  const labels: Record<string, string> = {
    product_url: "产品地址",
    local_repo: "本地代码",
    requirement_doc: "需求文档",
    screenshot: "页面截图",
    release_note: "发布说明",
    credential: "演示凭据",
  };
  return labels[kind] ?? kind;
}

function sourceStatusLabel(status: string): string {
  const labels: Record<string, string> = {
    ready: "就绪",
    needs_attention: "需确认",
    processing: "处理中",
    blocked: "已阻塞",
  };
  return labels[status] ?? status;
}

function evidenceKindLabel(kind: string | undefined): string {
  if (!kind) {
    return "证据";
  }
  const labels: Record<string, string> = {
    source_code: "代码摘要",
    requirement_doc: "需求文档",
    webpage_screenshot: "页面截图",
    browser_trace: "浏览轨迹",
    release_note: "发布说明",
  };
  return labels[kind] ?? kind;
}

function cloudStatusLabel(status: string): string {
  const labels: Record<string, string> = {
    not_uploaded: "未上传",
    queued: "排队中",
    running: "录制中",
    succeeded: "已完成",
    failed: "失败",
  };
  return labels[status] ?? status;
}

function browserAgentDirectLabel(runtimeHealth: RuntimeHealthView | undefined): string {
  const direct = runtimeHealth?.browserAgentDirect;
  if (!direct?.configured) return "尚未配置";
  const target = [direct.controlURLHost, direct.controlURLPath].filter(Boolean).join("") || "Browser Agent 服务器";
  if (!direct.tokenConfigured) return `${target} · 访问令牌未配置`;
  if (!direct.reachable) return `${target} · 协议不可达${direct.errorClass ? `（${direct.errorClass}）` : ""}`;
  return `${target} · 直连协议可达`;
}

function assetKindLabel(kind: string): string {
  const labels: Record<string, string> = {
    demo_video: "演示视频",
    step_by_step_docs: "步骤文档",
    screenshot_pack: "截图包",
    video: "视频",
    step_docs: "步骤文档",
  };
  return labels[kind] ?? kind;
}

function credentialKindLabel(kind: string): string {
  const labels: Record<string, string> = {
    demo_account: "演示账号",
    sandbox_account: "沙箱账号",
    oauth: "OAuth 授权",
    temporary_token: "临时令牌",
  };
  return labels[kind] ?? kind;
}

function artifactSummaryLabel(workspace: ProjectWorkspaceView): string {
  const summary = workspace.cloudRun.artifactSummary;
  if (!summary) {
    return "等待服务器返回";
  }
  return `${summary.total} 个 / 加密 ${summary.encrypted} / 敏感 ${summary.sensitive}`;
}

function resourceLimitLabel(workspacePolicy: SandboxPolicy | undefined): string {
  const limits = workspacePolicy?.resource_limits;
  if (!limits) {
    return "未声明";
  }
  return [
    limits.max_runtime_sec ? `${limits.max_runtime_sec}s` : "",
    limits.max_memory_mb ? `${limits.max_memory_mb}MB` : "",
    limits.max_cpu_count ? `${limits.max_cpu_count} CPU` : "",
    limits.max_disk_mb ? `${limits.max_disk_mb}MB 磁盘` : "",
  ].filter(Boolean).join(" / ") || "未声明";
}

function agentStatusLabel(status: string): string {
  const labels: Record<string, string> = {
    pending: "待执行",
    completed: "已完成",
    attention: "需注意",
  };
  return labels[status] ?? status;
}

function workspaceInputForbiddenData(workspace: ProjectWorkspaceView): string[] {
  const metadataValue = workspace.inputBundle.metadata?.forbidden_data;
  if (Array.isArray(metadataValue)) {
    return metadataValue.filter((item): item is string => typeof item === "string");
  }
  return workspace.scriptDocument?.safety_policy.forbidden_data ?? ["客户邮箱", "API Key", "访问令牌"];
}
