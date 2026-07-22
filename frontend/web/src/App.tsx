import { useEffect, useMemo, useState } from "react";
import type { GraphNode, SandboxPolicy } from "../../src/types/workflowGraph";
import { agentPipelineItems, codeInvestigationQuestionsFromWorkspace, codeSummaryFromWorkspace, updateWorkspaceInputs } from "./agentPipeline";
import { createBridgeClient } from "./bridge";
import type {
	ApprovalChecklistState,
	ModelDiagnosticResult,
	NavSection,
	ProjectWorkspaceView,
	RuntimeHealthView,
	RuntimeLogEntry,
	ScenarioID,
	WorkspaceStage,
} from "./domain";
import { createWorkspace, initialChecklist } from "./mockWorkspace";
import { scenarioTemplates } from "./scenarios";
import { VideoEditor } from "./VideoEditor";
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

export function App() {
	const bridge = useMemo(() => createBridgeClient(), []);
	const [activeNav, setActiveNav] = useState<NavSection>("projects");
	const [editorNavigationOpen, setEditorNavigationOpen] = useState(false);
	const [workspace, setWorkspace] = useState<ProjectWorkspaceView>(() => createWorkspace("product_demo"));
  const [demoCredentials, setDemoCredentials] = useState({ username: "", password: "" });
  const [runtimeHealth, setRuntimeHealth] = useState<RuntimeHealthView | undefined>();
  const [modelDiagnostics, setModelDiagnostics] = useState<ModelDiagnosticResult[]>([]);
  const [isRunningDiagnostics, setIsRunningDiagnostics] = useState(false);
  const [diagnosticsError, setDiagnosticsError] = useState("");
  const [checklist, setChecklist] = useState<ApprovalChecklistState>(initialChecklist);
  const [selectedNodeID, setSelectedNodeID] = useState(workspace.planReview.graph.nodes[0]?.id ?? "");
  const [isGeneratingPackage, setIsGeneratingPackage] = useState(false);
  const [isRunningProduct, setIsRunningProduct] = useState(false);

  const selectedNode = workspace.planReview.graph.nodes.find((node) => node.id === selectedNodeID) ?? workspace.planReview.graph.nodes[0];
  const blockedReasons = packageApprovalBlockedReasons(workspace.packagePreview, checklist, workspace.sourceConnections);
  const canUpload = canUploadExecutionPackage(workspace.packagePreview, checklist, workspace.sourceConnections);
  const navItems = useMemo(() => buildNavItems(workspace), [workspace]);

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
    const result = await bridge.createProject(scenarioID);
    if (result.ok && result.data) {
      setWorkspace(result.data);
      setSelectedNodeID(result.data.planReview.graph.nodes[0]?.id ?? "");
      setChecklist(initialChecklist);
      setDemoCredentials({ username: "", password: "" });
      setActiveNav("projects");
    }
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

  async function runProductLifecycle() {
    setIsRunningProduct(true);
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
        setActiveNav(nextWorkspace.cloudRun.status === "succeeded" ? "assets" : "execution_packages");
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

  async function simulateCloudSuccess() {
    const result = await bridge.pollCloudRun(workspace);
    if (result.ok && result.data) {
      setWorkspace(result.data);
      setActiveNav("assets");
    }
  }

  async function simulateCloudFailure() {
    const result = await bridge.simulateCloudFailure(workspace);
    if (result.ok && result.data) {
      setWorkspace(result.data);
      setActiveNav("execution_packages");
    }
  }

  async function repairFailedScript() {
    const result = await bridge.repairFailedScript(workspace);
    if (result.ok && result.data) {
      setWorkspace(result.data);
      setChecklist((current) => resetApprovalChecklistForRepair(current));
      setActiveNav("execution_packages");
    }
  }

  async function approveAssets() {
    const result = await bridge.acknowledgeResult(workspace);
    if (result.ok && result.data) {
      setWorkspace(result.data);
    }
  }

  return (
    <div className={activeNav === "editor" ? "app-shell editor-route" : "app-shell"}>
      {activeNav === "editor" ? <button type="button" className="editor-navigation-toggle" aria-label={editorNavigationOpen ? "收起工作台导航" : "展开工作台导航"} aria-expanded={editorNavigationOpen} onClick={() => setEditorNavigationOpen((current) => !current)}>☰</button> : null}
      {activeNav === "editor" && editorNavigationOpen ? <button type="button" className="editor-navigation-scrim" aria-label="关闭导航菜单" onClick={() => setEditorNavigationOpen(false)} /> : null}
      <aside className={activeNav === "editor" ? `sidebar editor-navigation-drawer ${editorNavigationOpen ? "open" : ""}` : "sidebar"} aria-label="主导航">
        <div className="brand-block">
          <div className="brand-mark">C</div>
          <div>
            <strong>Cascade</strong>
            <span>DemoOps</span>
          </div>
        </div>
        {activeNav === "editor" ? <button type="button" className="editor-navigation-close" aria-label="关闭导航菜单" title="关闭导航菜单" onClick={() => setEditorNavigationOpen(false)}>×</button> : null}
        <nav className="nav-list">
          {navItems.map((item) => (
            <button
              key={item.id}
              type="button"
              className={activeNav === item.id ? "nav-item active" : "nav-item"}
              onClick={() => {
                setActiveNav(item.id);
                setEditorNavigationOpen(false);
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

      <main className={activeNav === "editor" ? "workspace editor-workspace-mode" : "workspace"}>
        {activeNav !== "editor" ? <ProjectHeader workspace={workspace} isGeneratingPackage={isGeneratingPackage || isRunningProduct} onBuildPackage={runProductLifecycle} /> : null}
        <div className={activeNav === "editor" ? "workspace-grid editor-wide" : "workspace-grid"}>
          <section className={activeNav === "editor" ? "main-panel editor-main-panel" : "main-panel"} aria-label="项目工作台">
            {activeNav === "new_demo" ? <ScenarioPicker activeID={workspace.scenarioID} onCreate={createScenario} /> : null}
            {activeNav === "projects" ? (
              <ProjectFlow
                workspace={workspace}
                demoCredentials={demoCredentials}
                selectedNodeID={selectedNode?.id ?? ""}
                onSelectNode={setSelectedNodeID}
                onPatchNode={patchNode}
                onStageChange={(stage) => patchWorkspace({ stage })}
                onDemoCredentialsChange={setDemoCredentials}
                onWorkspaceChange={setWorkspace}
                onApproveAssets={approveAssets}
              />
            ) : null}
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
            {activeNav === "assets" ? <AssetReview workspace={workspace} onApprove={approveAssets} /> : null}
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
      </main>
    </div>
  );
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
  const localRepoPath = workspace.inputBundle.repositories?.find((repo) => repo.local_path)?.local_path ?? "";
  const gitRepoURL = workspace.inputBundle.repositories?.find((repo) => repo.url)?.url ?? "";
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
            <span>本地项目根目录</span>
            <input value={localRepoPath} onChange={(event) => patchInputs({ localRepoPath: event.currentTarget.value })} placeholder="C:\\Users\\you\\Desktop\\your-project" />
          </label>
          <label className="field-row">
            <span>GitHub 仓库 URL</span>
            <input value={gitRepoURL} onChange={(event) => patchInputs({ gitRepoURL: event.currentTarget.value })} placeholder="https://github.com/org/repo" />
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
        <div className="input-note">本地项目根目录和 GitHub 仓库 URL 都是可选代码来源，可以单独填写也可以同时填写；演示账号密码只作为本地登录预扫描的瞬时凭据，不进入执行包、审批文档或云端 payload。代码读取只生成结构摘要和 hash，不上传完整源码。</div>
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

function UnderstandingStagePanel({ workspace }: { workspace: ProjectWorkspaceView }) {
  return (
    <div className="section-stack">
      <MetricsRow workspace={workspace} />
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
  const businessStages = intelligence.business_stage_plan?.stages ?? [];
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
        <Fact
          label="代码调查"
          value={
            readiness?.code_investigation_summary ||
            (readiness?.code_investigation_tool_driven
              ? `${formatOverreadRisk(readiness.code_investigation_overread_risk)} · 专用工具 ${readiness.code_investigation_specialized_tool_call_count ?? 0} 次`
              : "待评估")
          }
        />
        <Fact
          label="调查缺口"
          value={
            readiness?.code_investigation_gaps?.length
              ? readiness.code_investigation_gaps.slice(0, 3).join("、")
              : readiness?.code_investigation_open_question_count
                ? `${readiness.code_investigation_open_question_count} 个未解问题`
                : "无阻塞缺口"
          }
        />
        <Fact label="需求目标" value={`${intentGoals.length} 个`} />
        <Fact label="页面验证" value={verifiedPlan ? `${verifiedPlan.business_action_count ?? 0} 个业务动作 · ${verifiedPlan.verification_mode ?? "待识别"}` : "未完成"} />
        <Fact label="缺失证据" value={missingEvidence?.blocking ? "阻塞脚本生成" : missingEvidence ? "有提示" : "无阻塞"} />
        <Fact label="Source Digest" value={intelligence.source_digest_sha256 ?? "待生成"} />
      </div>
      {businessStages.length > 0 ? (
        <div className="runtime-log-list">
          {businessStages.slice(0, 8).map((stage) => (
            <div key={stage.id} className={`runtime-log-row ${stage.uncertainties?.some((item) => item.blocking) ? "warning" : "info"}`}>
              <span>{stage.kind}</span>
              <strong>{stage.title || stage.objective || stage.id}</strong>
              <small>
                {[
                  stage.route_state,
                  stage.entry_route,
                  stage.expected_route_after_action,
                  `${stage.duration_ms ?? 0}ms`,
                  `${stage.targets?.length ?? 0} targets`,
                ].filter(Boolean).join(" · ")}
              </small>
            </div>
          ))}
        </div>
      ) : null}
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
  const questions = codeInvestigationQuestionsFromWorkspace(workspace);
  const repositories = workspace.inputBundle.repositories ?? [];
  const hasLocalRepo = repositories.some((repo) => Boolean(repo.local_path));
  const hasGitRepo = repositories.some((repo) => Boolean(repo.url));
  const hasCodeSource = hasLocalRepo || hasGitRepo;
  const sourceMeta = hasLocalRepo && hasGitRepo ? "CodeReaderAgent · 本地 + GitHub" : hasGitRepo ? "CodeReaderAgent · GitHub" : hasLocalRepo ? "CodeReaderAgent · 本地" : "未提供代码来源";
  return (
    <section className="table-section">
      <SectionTitle title="代码阅读摘要" meta={sourceMeta} />
      <div className="settings-grid">
        <Fact label="扫描文件" value={summary.fileCount > 0 ? `${summary.fileCount} 个` : hasCodeSource ? "代码来源不可读或无可扫描文件" : "未提供"} />
        <Fact label="框架线索" value={summary.frameworks.length > 0 ? summary.frameworks.join("、") : "待识别"} />
        <Fact label="路由/组件" value={`${summary.routes} 个路由 / ${summary.components} 个组件`} />
        <Fact label="Selector" value={`${summary.selectors} 个稳定选择器候选`} />
        <Fact
          label="调查工具"
          value={summary.toolCalls > 0 ? `${summary.toolCalls} 次调用 / 专用 ${summary.specializedToolCalls} 次 / shell ${summary.shellRunToolCalls} 次` : "待生成"}
        />
        <Fact
          label="检索范围"
          value={
            summary.searchedFiles > 0 || summary.selectedFiles > 0
              ? `grep ${summary.searchedFiles} 文件 / 读取 ${summary.selectedFiles} 文件 / 选中率 ${formatPercent(summary.selectedFileRatio)}`
              : "待生成"
          }
        />
        <Fact label="调查问题" value={summary.investigationQuestions > 0 ? `${summary.investigationQuestions} 个问题 / ${summary.openInvestigationQuestions} 个待补证据` : "待生成"} />
        <Fact label="结构化读取" value={summary.selectedFiles > 0 ? `${summary.selectedFiles} 个文件 · ${summary.investigationMode || "intent drilldown"}` : "待生成"} />
        <Fact label="工具链" value={formatToolBreakdown(summary.toolBreakdown)} />
        <Fact
          label="调查质量"
          value={
            summary.investigationQualitySummary ||
            (summary.toolCalls > 0 ? `${formatOverreadRisk(summary.investigationOverreadRisk)} · 置信度 ${formatQualityConfidence(summary.investigationQualityConfidence)}` : "待生成")
          }
        />
        <Fact label="未解缺口" value={summary.remainingInvestigationGaps.length ? summary.remainingInvestigationGaps.slice(0, 3).join("、") : "无阻塞缺口"} />
        <Fact label="Source Digest" value={summary.sourceDigest || "待生成"} />
        <Fact label="读取策略" value={summary.degraded ? "已降级使用需求/页面材料" : summary.investigationSourceTextPolicy || "只读扫描结构摘要，不保存完整源码"} />
      </div>
      {questions.length > 0 ? (
        <div className="runtime-log-list">
          {questions.map((question) => (
            <div key={question.id} className={`runtime-log-row ${question.status === "answered" ? "success" : question.remainingGaps.length ? "warning" : "info"}`}>
              <span>{question.status}</span>
              <strong>{question.label}</strong>
              <small>
                {[question.evidenceSummary, question.remainingGaps.length ? `缺口：${question.remainingGaps.slice(0, 2).join("、")}` : "", formatQuestionTools(question.toolCalls)]
                  .filter(Boolean)
                  .join(" · ")}
              </small>
              {question.nextActions.length > 0 ? <em>{formatQuestionNextActions(question.nextActions)}</em> : null}
            </div>
          ))}
        </div>
      ) : null}
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
  const capabilities = runtimeHealth?.appCapabilities;
  const recordingBoundaryLabel = capabilities?.serverRecordingRequired && !capabilities.localRecordingExecution
    ? "Server Browser Agent 执行"
    : capabilities?.localRecordingExecution
      ? "本地录制启用"
      : "未声明";
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
        <Fact label="录制执行边界" value={recordingBoundaryLabel} />
        <Fact label="视频编辑器" value={capabilities?.videoEditor ? "已启用" : "待检查"} />
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
  const pendingAssets = workspace.assets.filter((asset) => asset.status === "generated").length;
  const executionBadge =
    workspace.cloudRun.status === "failed"
      ? "诊断"
      : workspace.executableScriptBundle
        ? "待审"
        : undefined;
  return [
    { id: "projects", label: "项目", badge: workflowStageLabels[workspace.stage] },
    { id: "new_demo", label: "新建演示" },
    { id: "execution_packages", label: "执行包", ...(executionBadge ? { badge: executionBadge } : {}) },
    { id: "assets", label: "成品资产", ...(pendingAssets > 0 ? { badge: `${pendingAssets}` } : {}) },
    { id: "editor", label: "视频编辑" },
    { id: "settings", label: "设置" },
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
    github_repo: "GitHub 代码",
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

function formatToolBreakdown(counts: Record<string, number>): string {
  const items = Object.entries(counts).filter(([, count]) => count > 0);
  if (items.length === 0) {
    return "待生成";
  }
  return items
    .sort((left, right) => {
      if (right[1] === left[1]) {
        return left[0].localeCompare(right[0]);
      }
      return right[1] - left[1];
    })
    .slice(0, 5)
    .map(([tool, count]) => `${tool}×${count}`)
    .join(" / ");
}

function formatPercent(value: number | undefined): string {
  if (typeof value !== "number" || !Number.isFinite(value) || value <= 0) {
    return "0%";
  }
  return `${Math.round(value * 100)}%`;
}

function formatQualityConfidence(value: number | undefined): string {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    return "待评估";
  }
  return formatPercent(value);
}

function formatOverreadRisk(risk: string | undefined): string {
  const labels: Record<string, string> = {
    high: "过读风险高",
    medium: "过读风险中",
    low: "过读风险低",
    unknown: "过读风险待评估",
  };
  return labels[risk ?? ""] ?? "过读风险待评估";
}

function formatQuestionTools(
  tools: Array<{ tool: string; selectedFiles: number; snippetWindows: number; pathHashCount: number; readPolicy?: string; sourceTextPolicy?: string }>,
): string {
  if (tools.length === 0) {
    return "";
  }
  return tools
    .slice(0, 5)
    .map((tool) => {
      const details = [
        tool.selectedFiles > 0 ? `${tool.selectedFiles} 文件` : "",
        tool.snippetWindows > 0 ? `${tool.snippetWindows} 窗口` : "",
        tool.pathHashCount > 0 ? `${tool.pathHashCount} hash` : "",
        tool.readPolicy ? tool.readPolicy : "",
        tool.sourceTextPolicy ? tool.sourceTextPolicy : "",
      ].filter(Boolean);
      return details.length > 0 ? `${tool.tool}(${details.join("/")})` : tool.tool;
    })
    .join(" -> ");
}

function formatQuestionNextActions(actions: Array<{ tool: string; reason?: string; query_terms?: string[]; expected_evidence?: string[] }>): string {
  return actions
    .slice(0, 3)
    .map((action) => {
      const terms = (action.query_terms ?? []).slice(0, 4).join("、");
      const evidence = (action.expected_evidence ?? []).slice(0, 3).join("、");
      return [action.tool, terms ? `检索：${terms}` : "", evidence ? `补证：${evidence}` : "", action.reason ?? ""].filter(Boolean).join(" · ");
    })
    .join(" -> ");
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
