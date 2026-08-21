# DemoOps 当前全工作链路代码审计

> 审计日期：2026-08-21
> 审计基线：`feat/automated-final-film-director@ed25587`
> 文档状态：模块化重构的事实基线，不是目标架构
> 排除项：`wip/paused-2048-harness-20260821` 中暂停的实验改动

## 1. 结论

DemoOps 已具备代码理解、页面理解、证据融合、计划审批、受限浏览器执行、直连传输、录屏、Editor、自动导演和最终审核包等能力。当前问题不是缺少单点能力，而是这些能力由一条固定长链和多套局部状态机连接：同一个任务同时存在 `CascadeState`、前端 `ProjectWorkspaceView`、Direct `JobStatus`、Editor Session 和 `FinalFilmJob` 五种生命周期解释。

这导致四类系统性风险：

1. 阶段完成、结果可下载、Editor 可物化、最终成片可终审的判断不共享同一事实源。
2. 固定工作流为了兼容具体任务不断增加分支，使任务语义进入代码理解、计划器和 Browser Runtime 内核。
3. Worker、Gateway 和 App 都能表达等待、失败和恢复，但没有统一的重放语义和责任归属。
4. 安全控制数量很多，却分散为字段校验、HTTP 门禁、运行时限制和 UI 条件，难以回答“为什么被阻断、谁负责解除、是否能安全恢复”。

重构应保留已经验证的安全与执行能力，用统一生命周期内核和传输无关执行端口组合它们；不应并行重写整个产品。

## 2. 当前真实链路

```mermaid
flowchart LR
    A["用户输入与本地项目"] --> B["CascadeFlow 固定理解链"]
    B --> C["WorkflowGraph / ScriptBundle"]
    C --> D["人工审批与包绑定"]
    D --> E["Direct Gateway / Worker"]
    E --> F["Browser Agent + Outcome Verifier"]
    F --> G["RecordingResultPackage"]
    G --> H["本地下载与 Editor 物化"]
    H --> I["FinalFilm 独立 Job"]
    I --> J["H3 / Seedance / FFmpeg"]
    J --> K["审核包与最终终审"]
```

| 链路阶段 | 当前主要实现 | 主要输出 | 当前门禁或状态源 |
| --- | --- | --- | --- |
| 输入与项目建立 | `internal/app.Service` | `ProjectContext`、`CascadeState` | 本地输入完整性、源码绑定 |
| 代码和页面理解 | `internal/agents` | Code/Page Snapshot、Project Intelligence | 读取预算、证据充分性、source binding |
| 业务阶段与图生成 | Business Stage Planner、Graph Builder | `BusinessStagePlan`、`DemoWorkflowGraph` | 业务证据、交互可执行性、缺失证据 |
| 脚本封装与审批 | Script Packager、Cloud Lifecycle | Browser Agent Outline、Client Execution Package | approval digest、package confidence、staleness |
| 直连上传与租约 | App Direct Transport、Direct Gateway | Lease、Job、Credential Receipt | 安装身份、租约、加密消息、防重放 |
| 浏览器执行与录制 | Browser Worker、Stage Orchestrator | StepResult、StageEventLog、录屏、Trace | Policy Guard、Target Contract、Outcome Verifier |
| 结果返回与校验 | Gateway 和 App Result Validator | `RecordingResultPackage` | 包/运行绑定、Artifact 内容、checksum、ACK |
| Editor 物化 | Editor Service | Asset Catalog、Editor Session | 必需素材可读、来源与 revision 绑定 |
| 最终成片 | `internal/finalfilm` | Director Plan、候选、EDL、Final MP4 | Provider 授权/预算、质量门、Final Review |

## 3. 生命周期事实源盘点

| 层 | 状态对象 | 优点 | 当前问题 |
| --- | --- | --- | --- |
| Orchestrator | `CascadeState.Status`、`CurrentNode` | 能表达本地理解与人工审批 | 固定节点顺序；聚合了所有大型中间结果 |
| Desktop/App | `DesktopCloudRunState` | 可跨重启保存远端任务和下载状态 | 传输、业务结果、修复和审核字段混在一个对象中 |
| Frontend | `ProjectWorkspaceView`、`WorkspaceStage` | 对用户友好 | 大量函数再次推导后端状态，可能产生不同结论 |
| Direct Gateway | `DirectJobStatus`、Lease | 有明确认证、租约和传输状态 | stage 是自由字符串；等待原因和业务阶段未形成统一协议 |
| Browser Runtime | StageEventLog、Checkpoint | 已具备事件和恢复基础 | 事件大小无统一预算；阶段级状态不能投影整个工作流 |
| Editor | Editor Session revision | 素材和编辑计划绑定明确 | 是否可物化仍由 App 另行推导 |
| FinalFilm | `FinalFilmJobState`、revision、events | 独立 CAS 状态机较成熟 | 使用另一套顶层状态；不能被主流程统一监控 |

### 3.1 重复和歧义

- `completed` 可能表示浏览器执行完成、结果下载完成、Editor 可用、FinalFilm 渲染完成或最终人工接受。
- `awaiting_*` 同时用于凭据、候选审核、最终审核和业务输入，调用方无法统一处理。
- 前端通过状态组合推导下一工作台，而不是消费一个权威的 Gate Decision。
- 凭据租约、Artifact 下载和业务完成曾经互相覆盖；当前分支已有局部修正，但模型仍没有统一的“传输完成不等于业务完成”约束。

## 4. 固定长链与任务耦合

`CascadeFlow` 固定排列 13 个节点：输入、需求、代码、页面、项目智能、融合理解、产品探索、页面交互验证、图生成、脚本封装、人工审批、排练、素材生成。所有任务都必须经过同一顺序，即使某些任务不需要源码、无需页面写操作，或只需要重新运行导演阶段。

当前已有五类 `ProductArchetype` 和结构信号路由，是可复用基础，但它们只在 Interaction Contract 和脚本阶段提供标记，尚未成为：

- 可独立版本化的 Task Pack；
- 可声明依赖关系的模块 DAG；
- 可独立重试、暂停、恢复和计费的运行单元；
- 可向 UI 投影统一进度与 Gate 的生命周期对象。

代码理解层仍包含针对异步构建、项目创建、构建完成和交互区域的查询词与问题模板；Browser Runtime 也承载定位、视觉、录制、策略、验证、事件和恢复等多种职责。继续在固定流程中增加任务分支，会提高跨站回归成本。

## 5. 数据与信任边界

| 边界 | 通过的数据 | 已有保护 | 需要统一之处 |
| --- | --- | --- | --- |
| 本地项目 → Code Reader | 文件、结构、少量证据窗口 | 读取预算、路径过滤、摘要化证据 | 数据分级、模型外发清单、留存声明 |
| App → Gateway | 批准后的执行包和 credential grant | 安装身份、签名/摘要、加密消息、防重放 | 统一 Execution Request 和 Gate 引用 |
| Gateway → Worker | 包、Job、短期凭据 | Worker token、一次性消费、scope/expiry | 统一 waiting/input/resume 语义 |
| Worker → Artifact | 截图、Trace、录屏、JSONL | job-scoped 路径、Artifact descriptor | 大小预算、数据等级、留存和删除事件 |
| App → 模型 Provider | 结构化 prompt、媒体引用 | Provider admission、显式预算 | 统一 outbound manifest 和审计事件 |
| Result → Editor/FinalFilm | 必需素材、事实轨、质量数据 | revision/source 绑定、内容校验 | 统一 materialization Gate 和 Artifact role |

## 6. 门禁盘点

现有门禁可归为九类：输入准入、源码绑定、证据充分性、人工审批、执行策略、凭据授权、结果真实性、素材物化、视频质量/终审。实现散落在 `model` validator、App service、Gateway、Browser Runtime、FinalFilm 和前端条件中。

主要缺口不是“门禁不够多”，而是：

- 没有统一结论枚举；错误、等待和阻断经常使用字符串消息表达。
- 没有统一责任方，用户无法区分需要补输入、重传凭据、等待外部 Provider，还是代码缺陷。
- 没有统一有效期和恢复入口，导致凭据过期后可能重走已经完成的阶段。
- 审计事件缺少大小和采样规则；高频轮询若保存每次候选详情，可能反过来破坏结果交付。

## 7. 可直接复用的资产

| 能力 | 复用方式 |
| --- | --- |
| Code Reader 的预算读取和证据窗口 | 包装为 Source Intelligence Module，不重写读取引擎 |
| Project Intelligence 与 EvidenceRef | 作为模块输出 Artifact 的事实模型 |
| Interaction Contract、Outcome Observer | 作为 Surface Validation Module 的动作/结果契约 |
| Browser Agent Outline 和 Stage Orchestrator | 作为 Execution Port 的 Browser Adapter |
| Direct 加密传输、防重放、租约和 Artifact 内容校验 | 保留为 Remote HTTP Adapter 的信任边界实现 |
| StageEventLog、Checkpoint 和 replay policy | 升级为统一 Execution Event / Checkpoint 语义 |
| Editor Asset Catalog、DemoEditPlan | 作为执行录制到导演模块的稳定交接面 |
| FinalFilm CAS Store、Provider attempt、质量报告、审核包 | 作为 Director Module 的首个标准端口实现 |
| 五类 Product Archetype | 提升为 Task Pack 的初始分类集合 |
| Director Skills 和 Skill Registry | 作为模块版本及决策解释记录，不进入生命周期内核 |

## 8. 优先级结论

### P0：先统一生命周期和端口

- 建立唯一顶层状态、统一 Gate Decision 和责任方。
- 规定 once-effect、幂等写和只观察三种恢复策略。
- 限制事件正文大小，高频观察仅保留摘要和 Artifact 引用。

### P1：再把能力包装为模块

- 先写 Adapter，不移动成熟实现。
- FinalFilm、Browser Worker 和 Direct Transport 已有清晰边界，应最先接入。
- 前端只消费统一 read model，不再自行拼接多套状态。

### P2：最后按 Task Pack 替换固定长链

- 工作流选择只使用意图、已观察交互结构、证据能力和风险需求。
- hostname、固定路由、业务文案和 selector 只能存在于运行输入或测试 fixture，不能进入通用内核。

## 9. 暂停实验说明

2048 视觉 Harness 的未完成修改已独立保存到 `wip/paused-2048-harness-20260821@1d7d6fb`。该分支证明了长轮询视觉观察、异步构建继续按钮和审计大小需要统一设计，但其代码不属于本审计基线，也不得在架构规范完成前恢复远端任务。
