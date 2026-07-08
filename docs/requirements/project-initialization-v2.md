项目初始化执行文档 V2.0：Cascade DemoOps Engine

1. 核心架构原则（基于PRD重述）

· 核心资产：系统不生成“死视频”，而是生成 Demo Workflow Graph（JSON结构化可执行图谱）。视频、截图、文档均为该Graph的渲染产物。
· 编排大脑：使用 LangGraph（Go实现） 作为Orchestrator，严格按照 “探索→生成图谱→人工审批→执行→排练→资产生成” 的状态机流转。
· 双端与Sidecar：后端Go负责状态机和Agent逻辑；前端TS（React）负责交互；独立的 Node/TS Worker 负责浏览器操控（Playwright）和视频渲染（Remotion）。
· 人机协作：必须在“生成图谱”后引入 Human-in-the-loop（人工审批），支持用户修改Graph节点后再继续。

2. 目录结构（必须严格生成）

```text
.
├── backend/
│   ├── main.go                 # 入口（支持 --mode=web/desktop）
│   ├── internal/
│   │   ├── orchestrator/       # LangGraph 主流程（MVP的6步状态机）
│   │   │   └── cascade_flow.go
│   │   ├── agents/             # 对应PRD的6组Agent（此处为逻辑模块）
│   │   │   ├── input_ctx.go    # 输入与上下文Agent组
│   │   │   ├── product_map.go  # 产品理解Agent组
│   │   │   ├── graph_builder.go# Workflow Graph生成Agent组
│   │   │   ├── qa_executor.go  # 执行与QA Agent组（含Rehearse）
│   │   │   └── asset_gen.go    # 资产生成Agent组（视频/截图/docs）
│   │   ├── executor/           # 确定性执行服务（接口定义）
│   │   │   └── interface.go    # 定义 Record() 和 Render()
│   │   ├── driver/             # 双端驱动（Cloud/Local）
│   │   │   ├── local.go        # 调用 Node Sidecar
│   │   │   └── cloud.go        # 调用云端 K8s Worker
│   │   ├── model/              # 核心数据结构（重点修改）
│   │   │   ├── project.go      # ProjectContext (URL/账号/代码库/受众)
│   │   │   └── workflow_graph.go # ★核心资产★ Demo Workflow Graph
│   │   └── store/              # 状态存储 (Badger/SQLite/Redis)
│   └── go.mod
├── frontend/                   # React + TS (Wails/Web通用)
│   ├── src/
│   │   ├── components/         # 包含 Graph 可视化编辑器（审批节点用）
│   │   └── types/              # 与 backend/model 严格同步
├── video-worker/               # Node Sidecar (PRD中的确定性服务)
│   ├── src/
│   │   ├── recorder.ts         # Playwright 执行 Graph 中的 browser steps
│   │   ├── renderer.ts         # Remotion 合成 60秒视频 + Zoom/Callout
│   │   └── index.ts            # 监听 Go 的 Stdio 指令
└── .env.example
```

3. 核心数据结构定义（Codex必须生成此代码）

在 backend/internal/model/workflow_graph.go 中定义护城河资产：

```go
// DemoWorkflowGraph 是可执行的产品演示知识图谱
type DemoWorkflowGraph struct {
    ID          string              `json:"id"`
    Version     int                 `json:"version"`
    EntryPoint  string              `json:"entry_point"` // 起始URL
    Nodes       []*GraphNode        `json:"nodes"`
    Edges       []*GraphEdge        `json:"edges"`
    Assets      *AssetManifest      `json:"assets"`      // 待生成的资产清单
}

type GraphNode struct {
    ID          string              `json:"id"`
    Action      string              `json:"action"`       // click/fill/upload/navigate
    Selector    string              `json:"selector"`     // CSS/XPath
    InputData   string              `json:"input_data"`   // 测试数据
    ExpectedOutcome string          `json:"expected_outcome"` // 预期页面标题/元素
    IsScreenshot bool               `json:"is_screenshot"`// 是否需要截图
    HasZoom      bool               `json:"has_zoom"`     // 是否放大特写
    RetryPolicy  int                `json:"retry_policy"` // 失败重试次数
}
```

4. LangGraph 状态机流转（MVP 核心逻辑）

在 orchestrator/cascade_flow.go 中，必须定义以下严格的线性状态节点（与MVP流程一致）：

1. InputCtx：读取用户输入的 URL、Demo账号、代码库（可选）、产品描述、目标受众，存入 ProjectContext。
2. ProductExplore：驱动 Node Worker 中的 Playwright 沙盒，自动登录并探索页面，生成 ProductMap（页面节点与功能列表）。
3. GraphGenerate：LLM 基于 ProductMap 和受众（如“投资人”或“新手用户”）生成 DemoWorkflowGraph（JSON），并自动构造干净测试数据（如填充表单）。
4. HumanApprove：（关键差异点） 系统挂起等待。前端展示 Graph 的流程图（Mermaid/React Flow），用户可增删节点或调整焦点。审批通过后继续。
5. ExecuteRehearse：在浏览器中真实执行 Graph。对比每一步的 ExpectedOutcome，失败则利用 LLM 修复 Selector 并重试（对应 PRD 的 Rehearse）。
6. AssetGenerate：Rehearse 成功率达到 90% 后，将 Graph + 录屏素材发送至 Node Worker。调用 Remotion 生成 60秒 Demo Video；调用截图模块生成 Screenshot Pack；调用 Docs 模板生成 Step-by-step 用户手册。

5. 双端适配与输入差异（PRD要求的代码库/URL处理）

· Web 模式：输入为 Git Repo URL + 测试环境 URL。Go后端负责克隆代码到临时目录，分析 README/package.json 以理解技术栈。
· 桌面（Wails）模式：输入为用户本地的代码仓库绝对路径 + 本地或 Staging 环境 URL。Go直接通过 filepath.Walk 扫描源码，且录屏直连本地 localhost，实现“数据不出本地”的安全卖点。

6. 针对 MVP 输出的具体执行指令

生成 backend/internal/agents/asset_gen.go 时，必须包含以下硬编码逻辑（供后续优化）：

· 60秒视频：时长严格控制在 58s~62s，包含 3 处转场（Dissolve）和 2 处 Zoom 特写（根据 Graph 中的 HasZoom 标记）。
· Docs 生成：将 Graph 中的每一步 Action + Selector 翻译为自然语言操作说明，并插入对应步骤的截图占位符。

7. 技术实现细节（SDK与调用方式）

· Node Sidecar 通信协议：在 video-worker/index.ts 中封装 JSON-RPC 2.0 over Stdio。Go 通过 exec.Command 启动子进程，并发送 {"method":"record","params":{...}} 指令，Node 完成后返回 {"result":"video_path"}。
· 前端 Graph 编辑器：在 frontend 中集成 reactflow，将后端吐出的 JSON Graph 渲染为可拖拽节点图，支持用户点选节点修改 HasZoom 或 IsScreenshot 布尔值。

8. 环境变量与运行指令（生成 .env.example）

```env
APP_MODE=desktop   # 或 web
NODE_WORKER_PATH=../video-worker/dist/index.js
LLM_API_KEY=xxx    # 用于生成 Graph 和修复 Selector
DEMO_USER=xxx      # 测试环境账号（加密存储）
DEMO_PASS=xxx
```

---

🚀 给 Codex 的最终指令

请先构建数据层（Model）和状态机骨架（Orchestrator），不要立刻实现全部 Agent 逻辑。完成后输出 internal/model/workflow_graph.go 和 orchestrator/cascade_flow.go 的完整代码，并等待我确认 Graph 结构后再填充 Agent 细节。

这份文档已经将我们的技术选型（Go+TS+LangGraph）与你的产品愿景（DemoOps、Workflow Graph、6组Agent）深度咬合。只要 Codex 严格按此生成，代码仓库的骨架就会完全符合“DemoOps 引擎”的演进方向。😊