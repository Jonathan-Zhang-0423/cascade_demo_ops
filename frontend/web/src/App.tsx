import { useEffect, useMemo, useState, type FormEvent, type MouseEvent } from "react";
import type { GraphNode, RepositoryInput, SandboxPolicy } from "../../src/types/workflowGraph";
import { agentPipelineItems, codeSummaryFromWorkspace, updateWorkspaceInputs } from "./agentPipeline";
import { createBridgeClient, userInputFromWorkspace, type ProjectCreationInput } from "./bridge";
import type {
	ApprovalChecklistState,
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
import { createWorkspace, initialChecklist } from "./mockWorkspace";
import { scenarioTemplates } from "./scenarios";
import { VideoEditor } from "./VideoEditor";
import { AssistantConversationPanel, AssistantWidget } from "./AssistantWidget";
import {
  canUploadExecutionPackage,
  lifecycleStagesFromWorkspace,
  lifecycleStatusLabel,
  lifecycleStatusTone,
  packageApprovalBlockedReasons,
  projectStatusLabels,
  resetApprovalChecklistForRepair,
  sandboxProfileLabel,
  sandboxRiskLevel,
  sandboxRiskMessage,
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
	const [workspace, setWorkspace] = useState<ProjectWorkspaceView>(() => createWorkspace("product_demo"));
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
  const [modelDiagnostics, setModelDiagnostics] = useState<ModelDiagnosticResult[]>([]);
  const [isRunningDiagnostics, setIsRunningDiagnostics] = useState(false);
  const [diagnosticsError, setDiagnosticsError] = useState("");
  const [checklist, setChecklist] = useState<ApprovalChecklistState>(initialChecklist);
  const [selectedNodeID, setSelectedNodeID] = useState(workspace.planReview.graph.nodes[0]?.id ?? "");
  const [isGeneratingPackage, setIsGeneratingPackage] = useState(false);
  const [isRunningProduct, setIsRunningProduct] = useState(false);
  const [isCreatingProject, setIsCreatingProject] = useState(false);
  const [agentPrompt, setAgentPrompt] = useState("");
  const [isPromptConfirmationPending, setIsPromptConfirmationPending] = useState(false);

  const selectedNode = workspace.planReview.graph.nodes.find((node) => node.id === selectedNodeID) ?? workspace.planReview.graph.nodes[0];
  const blockedReasons = packageApprovalBlockedReasons(workspace.packagePreview, checklist, workspace.sourceConnections);
  const canUpload = canUploadExecutionPackage(workspace.packagePreview, checklist, workspace.sourceConnections);
  const navItems = useMemo(() => buildNavItems(workspace), [workspace]);
  const isProjectAgentWorkspace = activeNav === "project_library" && selectedProjectID !== undefined;

  useEffect(() => {
    let mounted = true;
    bridge.runtimeHealth().then((result) => {
      if (mounted && result.ok && result.data) {
        setRuntimeHealth(result.data);
      }
    });
    return () => {
      mounted = false;
    };
  }, [bridge]);

  useEffect(() => {
    void refreshProjects();
  }, [bridge]);


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


  async function openProject(projectID: string) {
    const result = await bridge.loadProject(projectID);
    if (!result.ok || !result.data) {
      setProjectsError(result.error ?? "项目加载失败");
      return;
    }
    setWorkspace(result.data);
    setSelectedProjectID(result.data.id);
    setSelectedNodeID(result.data.planReview.graph.nodes[0]?.id ?? "");
    setChecklist(initialChecklist);
    setDemoCredentials({ username: "", password: "" });
    setProjectsError("");
    setRepositoryMessage("");
    setRepositoryError("");
  }

  function patchWorkspace(patch: Partial<ProjectWorkspaceView>) {
    setWorkspace((current) => ({ ...current, ...patch }));
  }

  function patchNode(nodeID: string, patch: Partial<GraphNode>) {
    setWorkspace((current) => ({
      ...current,
      planReview: {
        ...current.planReview,
        graph: updateGraphNode(current.planReview.graph, nodeID, patch),
      },
    }));
  }

  function bridgeRunOptions() {
    const username = demoCredentials.username.trim();
    const password = demoCredentials.password;
    if (!username && !password) {
      return undefined;
    }
    return {
      demoCredentials: {
        ...(username ? { username } : {}),
        ...(password ? { password } : {}),
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

  async function buildPackagePreview() {
    setIsGeneratingPackage(true);
    setActiveNav("execution_packages");
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
    setWorkspace((current) => appendRuntimeLog(current, {
      level: "info",
      message: "开始生成执行包",
      detail: "正在调用本地 Dev Bridge：需求读取 -> 代码 drilldown -> 项目理解 -> Stage JSON -> Browser Agent 大纲。",
    }));
    setWorkspace((current) => {
      const { lastError: _lastError, ...cloudRun } = current.cloudRun;
      return {
        ...current,
        stage: "package_approval",
        cloudRun: {
          ...cloudRun,
          currentStep: "本地 Agent 正在生成执行包",
        },
      };
    });
    await pollRuntimeEvents();
    const poller = window.setInterval(() => {
      void pollRuntimeEvents();
    }, 1500);
    try {
      const result = await bridge.buildExecutionPackagePreview(workspace, bridgeRunOptions());
      window.clearInterval(poller);
      await pollRuntimeEvents();
      if (result.ok && result.data) {
        setWorkspace((current) => appendRuntimeLog(mergeWorkspaceRuntimeLogs(result.data!, current), {
          level: "success",
          message: "执行包生成完成",
          detail: `耗时 ${Math.round((Date.now() - startedAt.getTime()) / 1000)} 秒，已进入执行包审批。`,
        }));
        setActiveNav("execution_packages");
      } else {
        const message = result.error ?? "执行包生成失败";
        setWorkspace((current) => appendRuntimeLog({
          ...current,
          cloudRun: {
            ...current.cloudRun,
            lastError: message,
            currentStep: message,
          },
        }, {
          level: "error",
          message: "执行包生成失败",
          detail: message,
        }));
        setActiveNav("execution_packages");
      }
    } finally {
      window.clearInterval(poller);
      setIsGeneratingPackage(false);
    }
  }

  async function runProductLifecycle(keepAgentWorkspace = false) {
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
    setChecklist({
      userApprovedPlan: true,
      ipAllowlistAcknowledged: true,
      sourceSummaryOnlyAcknowledged: true,
      credentialGrantAcknowledged: true,
      redactionsReviewed: true,
    });
    setWorkspace((current) => appendRuntimeLog({
      ...current,
      stage: "cloud_run",
      status: "cloud_running",
      cloudRun: {
        ...current.cloudRun,
        status: "running",
        stage: "local_generated",
        message: "本地理解与脚本生成已启动。",
        currentStep: "本地 Agent 正在读取需求、项目目录和产品地址",
        progress: 5,
      },
    }, {
      level: "info",
      message: "开始产品实战自动流程",
      detail: "先完成本地理解和三合一执行包生成；脚本就绪后再单独连接服务器上传和轮询。",
    }));
    await pollRuntimeEvents();
    const poller = window.setInterval(() => {
      void pollRuntimeEvents();
    }, 1500);
    try {
      const result = await bridge.runProductLifecycle({
        ...workspace,
        packagePreview: { ...workspace.packagePreview, ipAllowlistAcknowledged: true },
      }, bridgeRunOptions());
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
        setActiveNav(keepAgentWorkspace || nextWorkspace.cloudRun.status === "succeeded" ? "project_library" : "execution_packages");
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

  async function uploadPackage() {
    if (!canUpload) {
      return;
    }
    const result = await bridge.approveAndUploadPackage({
      ...workspace,
      packagePreview: { ...workspace.packagePreview, ipAllowlistAcknowledged: true },
    });
    if (result.ok && result.data) {
      setWorkspace(result.data);
      setActiveNav("execution_packages");
    }
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
    const result = await bridge.acknowledgeResult(workspace);
    if (result.ok && result.data) {
      setWorkspace(result.data);
      void refreshProjects();
    }
  }

  function submitAgentPrompt(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const prompt = agentPrompt.trim();
    if (!prompt) {
      return;
    }
    setIsPromptConfirmationPending(true);
  }

  async function createProjectFromPrompt() {
    const prompt = agentPrompt.trim();
    if (!prompt) return;
    setIsCreatingProject(true);
    const seed = updateWorkspaceInputs(createWorkspace(workspace.scenarioID), {
      productURL: workspace.productURL,
      targetAudience: workspace.targetAudience,
      rawUserPrompt: prompt,
    });
    const seededWorkspace = {
      ...seed,
      inputBundle: { ...seed.inputBundle, repositories: workspace.inputBundle.repositories ?? [] },
    };
    const input: ProjectCreationInput = {
      userInput: userInputFromWorkspace(seededWorkspace, bridgeRunOptions()),
      scenarioID: workspace.scenarioID,
    };
    const result = await bridge.createProject(input);
    if (result.ok && result.data) {
      setWorkspace(result.data);
      setSelectedProjectID(result.data.id);
      setSelectedNodeID(result.data.planReview.graph.nodes[0]?.id ?? "");
      setChecklist(initialChecklist);
      setDemoCredentials({ username: "", password: "" });
      setAgentPrompt("");
      setIsPromptConfirmationPending(false);
      void refreshProjects();
      setActiveNav("project_library");
    }
    setIsCreatingProject(false);
  }

  async function continueCurrentProject() {
    const prompt = agentPrompt.trim();
    if (!prompt) return;
    setIsCreatingProject(true);
    const next = updateWorkspaceInputs(workspace, { rawUserPrompt: prompt });
    const result = await bridge.saveProjectInputs(workspace.id, next.inputBundle);
    if (result.ok && result.data) {
      setWorkspace(result.data);
      setSelectedProjectID(result.data.id);
      setAgentPrompt("");
      setIsPromptConfirmationPending(false);
      void refreshProjects();
      setActiveNav("project_library");
    }
    setIsCreatingProject(false);
  }

  function cancelPromptConfirmation() {
    setIsPromptConfirmationPending(false);
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
          blockedReasons={blockedReasons}
          checklist={checklist}
          canUpload={canUpload}
          isGeneratingPackage={isGeneratingPackage || isRunningProduct}
          selectedNodeID={selectedNodeID}
          onBack={() => { setSelectedProjectID(undefined); void refreshProjects(); }}
          onChecklistChange={setChecklist}
          onSelectNode={setSelectedNodeID}
          onPatchNode={patchNode}
          onUpload={() => runProductLifecycle(true)}
          onCloudSuccess={() => simulateCloudSuccess(true)}
          onCloudFailure={() => simulateCloudFailure(true)}
          onRepairScript={() => repairFailedScript(true)}
          onApproveAssets={approveAssets}
          onReplaceWebpage={() => document.querySelector<HTMLTextAreaElement>(".project-agent-conversation textarea")?.focus()}
          onReplaceSource={() => { setSelectedProjectID(undefined); setActiveNav("repositories"); }}
          onContinuePageOnly={async () => {
            const binding = workspace.sourceBinding;
            if (!binding) return;
            const result = await bridge.continueWithWebpageEvidence(workspace.id, binding.assessment_hash, `page-only-${binding.assessment_hash}`);
            if (result.ok && result.data) setWorkspace(result.data);
          }}
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
            isGeneratingPackage={isGeneratingPackage || isRunningProduct || isCreatingProject}
            isPromptConfirmationPending={isPromptConfirmationPending}
            canContinueCurrentProject={bridge.mode === "mock" || projectSummaries.some((project) => project.id === workspace.id)}
            onPromptChange={setAgentPrompt}
            onSubmitPrompt={submitAgentPrompt}
            onConfirmCreate={createProjectFromPrompt}
            onConfirmContinue={continueCurrentProject}
            onCancelConfirmation={cancelPromptConfirmation}
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
            {activeNav !== "editor" ? <ProjectHeader workspace={workspace} isGeneratingPackage={isGeneratingPackage || isRunningProduct} onBuildPackage={runProductLifecycle} /> : null}
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
                    onChecklistChange={setChecklist}
                    onUpload={runProductLifecycle}
                    onCloudSuccess={simulateCloudSuccess}
                    onCloudFailure={simulateCloudFailure}
                    onRepairScript={repairFailedScript}
                  />
                ) : null}
                {activeNav === "project_library" || activeNav === "assets" ? <AssetReview workspace={workspace} onApprove={approveAssets} /> : null}
                {activeNav === "editor" ? <VideoEditor /> : null}
                {activeNav === "settings" ? (
                  <SettingsPanel
                    workspace={workspace}
                    diagnostics={modelDiagnostics}
                    diagnosticsError={diagnosticsError}
                    isRunningDiagnostics={isRunningDiagnostics}
                    onRunDiagnostics={runModelDiagnostics}
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
  blockedReasons: string[];
  checklist: ApprovalChecklistState;
  canUpload: boolean;
  isGeneratingPackage: boolean;
  selectedNodeID: string;
  onBack: () => void;
  onChecklistChange: (state: ApprovalChecklistState) => void;
  onSelectNode: (id: string) => void;
  onPatchNode: (id: string, patch: Partial<GraphNode>) => void;
  onUpload: () => void;
  onCloudSuccess: () => void;
  onCloudFailure: () => void;
  onRepairScript: () => void;
  onApproveAssets: () => void;
  onReplaceWebpage: () => void;
  onReplaceSource: () => void;
  onContinuePageOnly: () => void;
};

function ProjectAgentWorkspace({
  bridge,
  workspace,
  runtimeHealth,
  blockedReasons,
  checklist,
  canUpload,
  isGeneratingPackage,
  selectedNodeID,
  onBack,
  onChecklistChange,
  onSelectNode,
  onPatchNode,
  onUpload,
  onCloudSuccess,
  onCloudFailure,
  onRepairScript,
  onApproveAssets,
  onReplaceWebpage,
  onReplaceSource,
  onContinuePageOnly,
}: ProjectAgentWorkspaceProps) {
  const [workstationView, setWorkstationView] = useState<ProjectWorkstationView>("editor");
  const [mobileChatOpen, setMobileChatOpen] = useState(false);
  const selectedNode = workspace.planReview.graph.nodes.find((node) => node.id === selectedNodeID) ?? workspace.planReview.graph.nodes[0];
  const statusLabel = projectStatusLabels[workspace.status];
  const runtimeLabel = runtimeHealth?.localDataConfigured ? "Runtime ready" : runtimeHealth ? "Runtime attention" : "Runtime checking";

  useEffect(() => {
    if (workspace.status === "script_repair_required") setWorkstationView("repair");
    if (workspace.status === "asset_ready") setWorkstationView("assets");
  }, [workspace.status]);

  return (
    <div className="project-agent-layout">
      <aside className={`project-agent-chat ${mobileChatOpen ? "mobile-open" : ""}`} aria-label="Project Cascade Agent">
        <header className="project-agent-chat-header">
          <button type="button" className="project-agent-back" onClick={onBack}>← Projects</button>
          <div className="project-agent-identity">
            <span className="project-agent-mark"><img src="/Cascade_launcher_mark.png" alt="" /></span>
            <div><strong>Cascade Agent</strong><small>{workspace.name}</small></div>
          </div>
          <div className="project-agent-status-row"><span className="status-dot ok" />{runtimeLabel}<span className="project-agent-status-divider" />{statusLabel}</div>
          <button type="button" className="project-agent-mobile-close" onClick={() => setMobileChatOpen(false)}>Close chat</button>
        </header>
        <AssistantConversationPanel bridge={bridge} context={{ surface: "projects", scopeKey: `project_${workspace.id}`, projectID: workspace.id, projectName: workspace.name }} embedded showHeader={false} onWorkstationChange={setWorkstationView} />
      </aside>
      <main className="project-workstation" aria-label="Project workstation">
        <header className="project-workstation-header">
          <div><p className="eyebrow">Live workstation</p><h1>{workstationTitle(workstationView)}</h1><p>{workstationDescription(workstationView)}</p></div>
          <div className="project-workstation-meta"><span>{workspace.productURL || "Product context pending"}</span><StatusPill label={statusLabel} tone={workspace.status === "asset_ready" ? "green" : workspace.status === "script_repair_required" ? "yellow" : "blue"} /></div>
          <button type="button" className="project-agent-chat-toggle" onClick={() => setMobileChatOpen(true)}>Open Cascade chat</button>
        </header>
        <section className={`project-workstation-canvas ${workstationView === "editor" ? "project-workstation-editor-canvas" : ""}`}>
          {workstationView === "overview" ? <ProjectOverviewWorkstation workspace={workspace} /> : null}
          {workstationView === "evidence" ? <UnderstandingStagePanel workspace={workspace} onReplaceWebpage={onReplaceWebpage} onReplaceSource={onReplaceSource} onContinuePageOnly={onContinuePageOnly} /> : null}
          {workstationView === "plan" ? <PlanReviewPanel workspace={workspace} selectedNodeID={selectedNodeID} onSelectNode={onSelectNode} onPatchNode={onPatchNode} /> : null}
          {workstationView === "approval" ? <PackageApproval workspace={workspace} checklist={checklist} blockedReasons={blockedReasons} canUpload={canUpload} isLocalMode={bridge.mode === "local"} onChecklistChange={onChecklistChange} onUpload={onUpload} onCloudSuccess={onCloudSuccess} onCloudFailure={onCloudFailure} onRepairScript={onRepairScript} /> : null}
          {workstationView === "execution" ? <div className="section-stack"><CloudRunPanel workspace={workspace} /><RuntimeLogPanel workspace={workspace} /></div> : null}
          {workstationView === "repair" ? <div className="section-stack"><FailureDiagnosticPanel workspace={workspace} onRepairScript={onRepairScript} /><ScriptRepairPanel workspace={workspace} /></div> : null}
          {workstationView === "assets" ? <AssetReview workspace={workspace} onApprove={onApproveAssets} /> : null}
          {workstationView === "editor" ? <div className="project-editor-workstation"><VideoEditor /></div> : null}
          {isGeneratingPackage ? <div className="project-workstation-progress"><span className="status-dot ok" />Cascade is updating the workstation…</div> : null}
        </section>
        {selectedNode && workstationView === "plan" ? <aside className="project-workstation-drawer" aria-label="Selected plan detail"><strong>{selectedNode.title ?? selectedNode.action}</strong><span>{selectedNode.selector || "Page transition"}</span><small>{selectedNode.expected_outcome}</small></aside> : null}
      </main>
    </div>
  );
}

function ProjectOverviewWorkstation({ workspace }: { workspace: ProjectWorkspaceView }) {
  return (
    <div className="project-overview-workstation">
      <section className="project-overview-hero"><span className="project-overview-kicker">Current objective</span><h2>{workspace.inputBundle.raw_user_prompt || "Shape a clear customer story from this product."}</h2><p>{workspace.targetAudience ? `For ${workspace.targetAudience}.` : "Cascade is ready to turn intent into evidence, a plan, and a reviewable demo."}</p></section>
      <div className="project-overview-grid">
        <section className="project-overview-card"><span>Evidence coverage</span><strong>{workspace.understanding.evidenceRefs.length ? `${Math.round(bestEvidenceConfidence(workspace) * 100)}% confidence` : "Not explored yet"}</strong><small>{workspace.understanding.evidenceRefs.length} evidence references available</small></section>
        <section className="project-overview-card"><span>Workflow</span><strong>{workflowStageLabels[workspace.stage]}</strong><small>{workspace.planReview.graph.nodes.length} planned interaction nodes</small></section>
        <section className="project-overview-card"><span>Outputs</span><strong>{workspace.assets.length ? `${workspace.assets.length} assets` : "No assets yet"}</strong><small>{workspace.cloudRun.currentStep}</small></section>
      </div>
      <section className="project-overview-next"><span>Try asking</span><p>“Show me what Cascade found” · “Turn this into a demo plan” · “What should we do next?”</p></section>
    </div>
  );
}

function workstationTitle(view: ProjectWorkstationView): string {
  const labels: Record<ProjectWorkstationView, string> = { overview: "Project overview", evidence: "Evidence and understanding", plan: "Demo plan", approval: "Execution approval", execution: "Live execution", repair: "Repair workspace", assets: "Generated assets", editor: "Video editor" };
  return labels[view];
}

function workstationDescription(view: ProjectWorkstationView): string {
  const descriptions: Record<ProjectWorkstationView, string> = { overview: "The current state of the demo session.", evidence: "What Cascade knows, where it came from, and what remains uncertain.", plan: "A readable route through the product, ready for review.", approval: "A human checkpoint before sensitive execution.", execution: "Observed progress, runtime events, and remaining work.", repair: "A focused view of the failure and the proposed recovery.", assets: "Review the proof Cascade generated from the approved journey.", editor: "Make precise changes to the generated video." };
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
  isGeneratingPackage,
  onBuildPackage,
}: {
  workspace: ProjectWorkspaceView;
  isGeneratingPackage: boolean;
  onBuildPackage: () => void;
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
      <button type="button" className="primary-action" disabled={isGeneratingPackage} onClick={onBuildPackage}>
        <span className="button-icon">包</span>
        {isGeneratingPackage ? "执行中" : "开始实战流程"}
      </button>
    </header>
  );
}

function AgentHome({
  prompt,
  isGeneratingPackage,
  isPromptConfirmationPending,
  canContinueCurrentProject,
  onPromptChange,
  onSubmitPrompt,
  onConfirmCreate,
  onConfirmContinue,
  onCancelConfirmation,
}: {
  prompt: string;
  isGeneratingPackage: boolean;
  isPromptConfirmationPending: boolean;
  canContinueCurrentProject: boolean;
  onPromptChange: (value: string) => void;
  onSubmitPrompt: (event: FormEvent<HTMLFormElement>) => void;
  onConfirmCreate: () => void;
  onConfirmContinue: () => void;
  onCancelConfirmation: () => void;
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
            placeholder="Tell Cascade what you want your customer to understand..."
            aria-label="Describe your demo"
          />
          <div className="agent-composer-footer">
            <span>Agent will inspect context, propose the path, and keep you in control.</span>
            <button type="submit" className="agent-submit" disabled={isGeneratingPackage || !prompt.trim()}>
              {isGeneratingPackage ? "Working" : "Continue"}
              <span aria-hidden="true">↗</span>
            </button>
          </div>
          {isPromptConfirmationPending ? (
            <div className="agent-confirmation" role="group" aria-label="Choose project destination">
              <div>
                <strong>Where should this go?</strong>
                <span>Keep the chat as the starting point, then choose the session to update.</span>
              </div>
              <div className="agent-confirmation-actions">
                <button type="button" className="agent-submit" disabled={isGeneratingPackage} onClick={onConfirmCreate}>
                  Create new project
                </button>
                <button type="button" className="text-action" disabled={isGeneratingPackage || !canContinueCurrentProject} onClick={onConfirmContinue}>
                  Continue current project
                </button>
                <button type="button" className="text-action muted" disabled={isGeneratingPackage} onClick={onCancelConfirmation}>
                  Cancel
                </button>
              </div>
            </div>
          ) : null}
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

function UnderstandingStagePanel({ workspace, onReplaceWebpage, onReplaceSource, onContinuePageOnly }: { workspace: ProjectWorkspaceView; onReplaceWebpage?: () => void; onReplaceSource?: () => void; onContinuePageOnly?: () => void }) {
  return (
    <div className="section-stack">
      <MetricsRow workspace={workspace} />
      <SourceBindingCard workspace={workspace} {...(onReplaceWebpage ? { onReplaceWebpage } : {})} {...(onReplaceSource ? { onReplaceSource } : {})} {...(onContinuePageOnly ? { onContinuePageOnly } : {})} />
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

function SourceBindingCard({ workspace, onReplaceWebpage, onReplaceSource, onContinuePageOnly }: { workspace: ProjectWorkspaceView; onReplaceWebpage?: () => void; onReplaceSource?: () => void; onContinuePageOnly?: () => void }) {
  const binding = workspace.sourceBinding;
  if (!binding || binding.status === "not_applicable") return null;
  const mismatched = binding.status === "mismatched" && binding.effective_mode === "blocked";
  const message = binding.status === "matched"
    ? "网页与源码身份信号一致，本次允许使用源码路由、组件和 selector 候选。"
    : binding.status === "unverified"
      ? "无法可靠证明网页与源码同源，已自动使用仅网页证据模式。"
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
        {onReplaceWebpage ? <button type="button" className="secondary-action" onClick={onReplaceWebpage}>更换网页</button> : null}
        {onReplaceSource ? <button type="button" className="secondary-action" onClick={onReplaceSource}>更换源码</button> : null}
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
}: {
  workspace: ProjectWorkspaceView;
  selectedNodeID: string;
  onSelectNode: (id: string) => void;
  onPatchNode: (id: string, patch: Partial<GraphNode>) => void;
}) {
  return (
    <div className="section-stack">
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
      <section className="table-section">
        <SectionTitle title="云端录制" meta={cloudStatusLabel(workspace.cloudRun.status)} />
        <div className="settings-grid">
          <Fact label="Upload ID" value={workspace.cloudRun.uploadID ?? "待初始化"} />
          <Fact label="Exchange Package" value={workspace.cloudRun.exchangePackageID ?? "待上传"} />
          <Fact label="Cloud Job" value={workspace.cloudRun.cloudJobID ?? "待创建"} />
          <Fact label="Result Package" value={workspace.cloudRun.resultPackageID ?? "待返回"} />
          <Fact label="当前步骤" value={workspace.cloudRun.currentStep} />
          <Fact label="进度" value={`${workspace.cloudRun.progress}%`} />
          <Fact label="服务器消息" value={workspace.cloudRun.message ?? "等待状态轮询"} />
          <Fact label="Artifact" value={artifactSummaryLabel(workspace)} />
        </div>
      </section>
      <ServerLifecyclePanel workspace={workspace} />
      <SandboxPolicyPanel workspace={workspace} />
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
  return <AssetReview workspace={workspace} onApprove={onApprove} />;
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
  onChecklistChange,
  onUpload,
  onCloudSuccess,
  onCloudFailure,
  onRepairScript,
}: {
  workspace: ProjectWorkspaceView;
  checklist: ApprovalChecklistState;
  blockedReasons: string[];
  canUpload: boolean;
  isLocalMode: boolean;
  onChecklistChange: (state: ApprovalChecklistState) => void;
  onUpload: () => void;
  onCloudSuccess: () => void;
  onCloudFailure: () => void;
  onRepairScript: () => void;
}) {
  function toggle(key: keyof ApprovalChecklistState) {
    onChecklistChange({ ...checklist, [key]: !checklist[key] });
  }
  const bundle = workspace.executableScriptBundle;

  return (
    <div className="section-stack">
      <SectionTitle title="执行包审批" meta={workspace.packagePreview.packageID} />
      <div className="approval-grid">
        <div className="package-facts">
          <Fact label="执行包摘要" value={workspace.packagePreview.packageDigest} />
          <Fact label="流程图摘要" value={workspace.packagePreview.graphDigest} />
          <Fact label="Runtime" value={bundle?.script_manifest.runtime ?? "待生成"} />
          <Fact label="Plan Hash" value={bundle?.reproducibility.plan_hash_sha256 ?? "待生成"} />
          <Fact label="Stage Hash" value={bundle?.reproducibility.stage_plan_hash_sha256 ?? "待生成"} />
          <Fact label="Outline Hash" value={bundle?.reproducibility.outline_hash_sha256 ?? bundle?.reproducibility.script_hash_sha256 ?? "待生成"} />
          <Fact label="Prompt Hash" value={bundle?.reproducibility.prompt_policy_hash_sha256 ?? "待生成"} />
          <Fact label="Bundle Hash" value={bundle?.reproducibility.bundle_hash_sha256 ?? "待生成"} />
          <Fact label="上传模式" value={workspace.packagePreview.sourceSummaryOnly ? "仅结构摘要" : "已阻塞"} />
          <Fact label="加密状态" value={workspace.packagePreview.encrypted ? "已启用" : "缺失"} />
          <Fact label="允许域名" value={workspace.planReview.allowedDomains.join("、")} />
          <Fact label="打码规则" value={workspace.planReview.redactionSelectors.join("、")} />
          <Fact label="模型来源" value={workspace.modelProvenance?.join("；") ?? "待生成"} />
          {bundle?.repair_lineage ? <Fact label="修复来源" value={`${bundle.repair_lineage.source_result_id} / 第 ${bundle.repair_lineage.repair_attempt} 次`} /> : null}
        </div>
        <div className="checklist-panel">
          {(
            [
              ["userApprovedPlan", "已审批执行方案"],
              ["ipAllowlistAcknowledged", "已确认 Cascade 云端执行 IP 白名单"],
              ["sourceSummaryOnlyAcknowledged", "确认不上传完整源码"],
              ["credentialGrantAcknowledged", "已复核凭据授权"],
              ["redactionsReviewed", "已复核打码规则"],
            ] as const
          ).map(([key, label]) => (
            <label key={key} className="check-row">
              <input type="checkbox" checked={checklist[key]} onChange={() => toggle(key)} />
              <span>{label}</span>
            </label>
          ))}
        </div>
      </div>
      <RuntimeLogPanel workspace={workspace} />
      <CloudRunPanel workspace={workspace} />
      <SandboxPolicyPanel workspace={workspace} />
      <ScriptBundleReview workspace={workspace} />
      {workspace.cloudRun.failureDiagnostic ? (
        <FailureDiagnosticPanel workspace={workspace} onRepairScript={onRepairScript} />
      ) : null}
      <section className="table-section">
        <SectionTitle title="凭据授权" meta={`${workspace.packagePreview.credentialGrants.length} 项授权`} />
        <table>
          <thead>
            <tr>
              <th>授权</th>
              <th>用途</th>
              <th>过期时间</th>
              <th>允许域名</th>
            </tr>
          </thead>
          <tbody>
            {workspace.packagePreview.credentialGrants.map((grant) => (
              <tr key={grant.grantID}>
                <td>{credentialKindLabel(grant.kind)}</td>
                <td>{grant.purpose}</td>
                <td>{grant.expiresAt}</td>
                <td>{grant.allowedDomains.join(", ")}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>
      {blockedReasons.length > 0 ? (
        <div className="blocked-list">
          {blockedReasons.map((reason) => (
            <span key={reason}>{reason}</span>
          ))}
        </div>
      ) : null}
      <div className="action-row">
        <button type="button" className="primary-action" disabled={!canUpload} onClick={onUpload}>
          <span className="button-icon">云</span>
          {isLocalMode ? "自动上传并录制" : "上传云端"}
        </button>
        <button type="button" className="secondary-action" disabled={isLocalMode} onClick={onCloudSuccess}>
          <span className="button-icon">成</span>
          {isLocalMode ? "录制后续接入" : "模拟完成"}
        </button>
        <button type="button" className="secondary-action" disabled={isLocalMode} onClick={onCloudFailure}>
          <span className="button-icon">诊</span>
          {isLocalMode ? "诊断后续接入" : "模拟失败诊断"}
        </button>
      </div>
    </div>
  );
}

function ServerLifecyclePanel({ workspace }: { workspace: ProjectWorkspaceView }) {
  const stages = lifecycleStagesFromWorkspace(workspace);
  return (
    <section className="table-section">
      <SectionTitle title="服务器执行生命周期" meta={workspace.cloudRun.stage ?? cloudStatusLabel(workspace.cloudRun.status)} />
      <div className="lifecycle-summary-grid">
        <Fact label="Upload ID" value={workspace.cloudRun.uploadID ?? "待初始化"} />
        <Fact label="Exchange Package" value={workspace.cloudRun.exchangePackageID ?? "待上传"} />
        <Fact label="Cloud Job" value={workspace.cloudRun.cloudJobID ?? "待创建"} />
        <Fact label="Result Package" value={workspace.cloudRun.resultPackageID ?? "待返回"} />
      </div>
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
              <span>{stage.artifactCount} artifact</span>
              {stage.errorCode ? <span>{stage.errorCode}</span> : null}
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

function AssetReview({ workspace, onApprove }: { workspace: ProjectWorkspaceView; onApprove: () => void }) {
  return (
    <div className="asset-layout">
      <section className="video-panel">
        <SectionTitle title="演示视频" meta={workspace.assets[0]?.checksum ?? "待生成"} />
		<div className="video-frame">
          <div className="play-symbol">播放</div>
          <span>{workspace.assets[0]?.title ?? "最终演示视频"}</span>
		</div>
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
        <button type="button" className="primary-action" onClick={onApprove}>
          <span className="button-icon">准</span>
          批准成品
        </button>
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
}: {
  workspace: ProjectWorkspaceView;
  runtimeHealth?: RuntimeHealthView;
  diagnostics: ModelDiagnosticResult[];
  diagnosticsError: string;
  isRunningDiagnostics: boolean;
  onRunDiagnostics: () => void;
}) {
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
  const exchangeLabel = cloudExchangeLabel(runtimeHealth);
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
        <Fact label="云端安全连接" value={exchangeLabel} />
      </div>
      <section className="table-section">
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
      </section>
      <section className="table-section">
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
      </section>
      <section className="table-section">
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
      </section>
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

function buildNavItems(workspace: ProjectWorkspaceView): Array<{ id: NavSection; label: string; badge?: string }> {
  return [
    { id: "projects", label: "Home" },
    { id: "project_library", label: "Projects" },
    { id: "repositories", label: "Repositories" },
    { id: "editor", label: "Editor" },
    { id: "settings", label: "Settings" },
  ];
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

function cloudExchangeLabel(runtimeHealth: RuntimeHealthView | undefined): string {
  const exchange = runtimeHealth?.cloudExchange;
  if (!exchange) {
    return "未配对云端";
  }
  const target = [exchange.baseURLHost, exchange.baseURLPath].filter(Boolean).join("");
  if (exchange.authMode === "dev_token") {
    return `${target || "云端"} · 旧联调令牌兼容`;
  }
  if (exchange.devPlaintext) {
    return `${target || "云端"} · 联调明文通道，仅用于测试`;
  }
  if (exchange.sessionValid) {
    return `${target || "云端"} · 已连接 · 安装密钥已启用`;
  }
  if (exchange.installationPaired) {
    return `${target || "云端"} · 会话已过期，自动刷新中`;
  }
  return target ? `${target} · 未配对云端` : "未配对云端";
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
