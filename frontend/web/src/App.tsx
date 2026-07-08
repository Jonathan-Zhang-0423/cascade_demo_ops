import { useMemo, useState } from "react";
import type { GraphNode } from "../../src/types/workflowGraph";
import { createMockBridgeClient } from "./bridge";
import type {
	ApprovalChecklistState,
	NavSection,
	ProjectWorkspaceView,
	ScenarioID,
	WorkspaceStage,
} from "./domain";
import { createWorkspace, initialChecklist } from "./mockWorkspace";
import { scenarioTemplates } from "./scenarios";
import { canUploadExecutionPackage, packageApprovalBlockedReasons, updateGraphNode } from "./workflow";

const navItems: Array<{ id: NavSection; label: string; badge?: string }> = [
  { id: "projects", label: "项目", badge: "1" },
  { id: "new_demo", label: "新建演示" },
  { id: "execution_packages", label: "执行包", badge: "1" },
  { id: "assets", label: "成品资产", badge: "2" },
  { id: "settings", label: "设置" },
];

const workflowStages: Array<{ id: WorkspaceStage; label: string }> = [
  { id: "setup", label: "基础设置" },
  { id: "inputs", label: "输入材料" },
  { id: "understanding", label: "产品理解" },
  { id: "plan_review", label: "方案审批" },
  { id: "package_approval", label: "执行包审批" },
  { id: "cloud_run", label: "云端录制" },
  { id: "result_review", label: "成品验收" },
];

export function App() {
	const bridge = useMemo(() => createMockBridgeClient(), []);
	const [activeNav, setActiveNav] = useState<NavSection>("projects");
	const [workspace, setWorkspace] = useState<ProjectWorkspaceView>(() => createWorkspace("product_demo"));
  const [checklist, setChecklist] = useState<ApprovalChecklistState>(initialChecklist);
  const [selectedNodeID, setSelectedNodeID] = useState(workspace.planReview.graph.nodes[0]?.id ?? "");

  const selectedNode = workspace.planReview.graph.nodes.find((node) => node.id === selectedNodeID) ?? workspace.planReview.graph.nodes[0];
  const blockedReasons = packageApprovalBlockedReasons(workspace.packagePreview, checklist, workspace.sourceConnections);
  const canUpload = canUploadExecutionPackage(workspace.packagePreview, checklist, workspace.sourceConnections);

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

  async function createScenario(scenarioID: ScenarioID) {
    const result = await bridge.createProject(scenarioID);
    if (result.ok && result.data) {
      setWorkspace(result.data);
      setSelectedNodeID(result.data.planReview.graph.nodes[0]?.id ?? "");
      setChecklist(initialChecklist);
      setActiveNav("projects");
    }
  }

  async function buildPackagePreview() {
    const result = await bridge.buildExecutionPackagePreview(workspace);
    if (result.ok && result.data) {
      setWorkspace(result.data);
      setActiveNav("execution_packages");
    }
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

  async function approveAssets() {
    const result = await bridge.acknowledgeResult(workspace);
    if (result.ok && result.data) {
      setWorkspace(result.data);
    }
  }

  return (
    <div className="app-shell">
      <aside className="sidebar" aria-label="主导航">
        <div className="brand-block">
          <div className="brand-mark">C</div>
          <div>
            <strong>Cascade</strong>
            <span>DemoOps</span>
          </div>
        </div>
        <nav className="nav-list">
          {navItems.map((item) => (
            <button
              key={item.id}
              type="button"
              className={activeNav === item.id ? "nav-item active" : "nav-item"}
              onClick={() => setActiveNav(item.id)}
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

      <main className="workspace">
        <ProjectHeader workspace={workspace} onBuildPackage={buildPackagePreview} />
        <div className="workspace-grid">
          <section className="main-panel" aria-label="项目工作台">
            {activeNav === "new_demo" ? <ScenarioPicker activeID={workspace.scenarioID} onCreate={createScenario} /> : null}
            {activeNav === "projects" ? (
              <ProjectFlow
                workspace={workspace}
                selectedNodeID={selectedNode?.id ?? ""}
                onSelectNode={setSelectedNodeID}
                onPatchNode={patchNode}
                onStageChange={(stage) => patchWorkspace({ stage })}
              />
            ) : null}
            {activeNav === "execution_packages" ? (
              <PackageApproval
                workspace={workspace}
                checklist={checklist}
                blockedReasons={blockedReasons}
                canUpload={canUpload}
                onChecklistChange={setChecklist}
                onUpload={uploadPackage}
                onCloudSuccess={simulateCloudSuccess}
              />
            ) : null}
            {activeNav === "assets" ? <AssetReview workspace={workspace} onApprove={approveAssets} /> : null}
            {activeNav === "settings" ? <SettingsPanel workspace={workspace} /> : null}
          </section>
          <Inspector workspace={workspace} selectedNode={selectedNode} blockedReasons={blockedReasons} />
        </div>
      </main>
    </div>
  );
}

function ProjectHeader({ workspace, onBuildPackage }: { workspace: ProjectWorkspaceView; onBuildPackage: () => void }) {
  return (
    <header className="project-header">
      <div>
        <p className="eyebrow">{scenarioLabel(workspace.scenarioID)}</p>
        <h1>{workspace.name}</h1>
        <div className="header-meta">
          <span>{workspace.productURL}</span>
          <span>{workspace.targetAudience}</span>
          <StatusPill label={projectStatusLabel(workspace.status)} tone="blue" />
        </div>
      </div>
      <button type="button" className="primary-action" onClick={onBuildPackage}>
        <span className="button-icon">包</span>
        生成执行包
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
  selectedNodeID,
  onSelectNode,
  onPatchNode,
  onStageChange,
}: {
  workspace: ProjectWorkspaceView;
  selectedNodeID: string;
  onSelectNode: (id: string) => void;
  onPatchNode: (id: string, patch: Partial<GraphNode>) => void;
  onStageChange: (stage: WorkspaceStage) => void;
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
      <MetricsRow workspace={workspace} />
      <InputsTable workspace={workspace} />
      <UnderstandingPanel workspace={workspace} />
      <GraphReviewTable
        workspace={workspace}
        selectedNodeID={selectedNodeID}
        onSelectNode={onSelectNode}
        onPatchNode={onPatchNode}
      />
    </div>
  );
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
  onChecklistChange,
  onUpload,
  onCloudSuccess,
}: {
  workspace: ProjectWorkspaceView;
  checklist: ApprovalChecklistState;
  blockedReasons: string[];
  canUpload: boolean;
  onChecklistChange: (state: ApprovalChecklistState) => void;
  onUpload: () => void;
  onCloudSuccess: () => void;
}) {
  function toggle(key: keyof ApprovalChecklistState) {
    onChecklistChange({ ...checklist, [key]: !checklist[key] });
  }

  return (
    <div className="section-stack">
      <SectionTitle title="执行包审批" meta={workspace.packagePreview.packageID} />
      <div className="approval-grid">
        <div className="package-facts">
          <Fact label="执行包摘要" value={workspace.packagePreview.packageDigest} />
          <Fact label="流程图摘要" value={workspace.packagePreview.graphDigest} />
          <Fact label="上传模式" value={workspace.packagePreview.sourceSummaryOnly ? "仅结构摘要" : "已阻塞"} />
          <Fact label="加密状态" value={workspace.packagePreview.encrypted ? "已启用" : "缺失"} />
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
          上传云端
        </button>
        <button type="button" className="secondary-action" onClick={onCloudSuccess}>
          <span className="button-icon">成</span>
          模拟完成
        </button>
      </div>
    </div>
  );
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

function SettingsPanel({ workspace }: { workspace: ProjectWorkspaceView }) {
  return (
    <div className="section-stack">
      <SectionTitle title="设置" meta="运行时正常" />
      <div className="settings-grid">
        <Fact label="凭据保险箱" value="仅保存本地引用" />
        <Fact label="资产存储" value="本地文件存储" />
        <Fact label="允许域名" value={workspace.planReview.allowedDomains.join(", ")} />
        <Fact label="禁止页面" value={workspace.planReview.forbiddenPages.join(", ")} />
      </div>
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

function projectStatusLabel(status: ProjectWorkspaceView["status"]): string {
  const labels: Record<ProjectWorkspaceView["status"], string> = {
    draft: "草稿",
    understanding_ready: "理解完成",
    awaiting_approval: "等待审批",
    cloud_running: "云端录制中",
    asset_ready: "成品就绪",
  };
  return labels[status];
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
