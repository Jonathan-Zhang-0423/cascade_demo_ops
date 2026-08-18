# Server 侧 Browser Agent、执行与本地视频编辑系统架构 v2

> 日期：2026-07-17
> 文档状态：Server 侧 v2 权威架构，正在实施
> 文档范围：只描述 Server 侧任务边界、系统需求、对外接口和开发计划
> 架构效力：Server 侧新功能、协议演进和代码重构均以本文为准
> 说明：本文不规定 APP 侧的内部架构、Agent 实现、源码分析方式或工作安排；尚未迁移的 v1 代码和协议仅作为兼容基线

## 0. 新路线说明

本文描述Server侧下一阶段的新路线。如果与此前“Server只接收完整脚本并执行，失败后将任务退回”的设计冲突，以本文为准。

Server侧的新定位是：

```text
接收外部业务动作证据和探索任务
        +
Server Browser Agent探索真实部署页面
        ↓
Server完成双证据融合、冲突检查和执行草案编译
        ↓
接收对已生成草案的外部授权
        ↓
Playwright确定性执行 + Outcome Verifier验证
        ↓
低风险运行时问题由Server受控修复并继续
        ↓
录屏、素材目录、本地轻量编辑、渲染和结果交付
```

外部业务证据如何产生不属于本文范围。Server只定义能够接收、验证和消费的数据接口。

---

## 1. Server侧目标

Server侧需要建设以下能力：

1. 创建和管理Browser Agent探索会话。
2. 在真实部署页面中发现组件实例、可交互目标和状态变化。
3. 接收外部业务动作证据，并与浏览器运行时证据进行融合。
4. 按字段权威和硬门槛处理双证据冲突。
5. 从融合结果确定性生成执行草案和说明材料。
6. 校验草案授权和Hash后执行。
7. 使用Playwright完成确定性动作、录屏、截图和Trace。
8. 使用Outcome Verifier执行结构化结果验证。
9. 使用Repair Policy约束Server侧运行时小修复。
10. selector、frame、wait等低风险问题修复成功后继续执行，不默认退回上游。
11. 生成结构化素材目录和默认编辑计划。
12. 接入一个基于现有数据模型的本地轻量视频编辑器。
13. 支持预览渲染、最终渲染、结果打包、下载和ACK。

### 1.1 非目标

Server侧本阶段不建设：

- 可以改变业务目的和业务步骤的自由Browser Agent；
- 真实生产环境中的无约束在线探索；
- 允许模型绕过SandboxPolicy的执行链路；
- 仅凭LLM置信度执行高风险操作的系统；
- 完整专业版视频剪辑软件；
- 通过剪辑隐藏业务执行失败的机制；
- APP侧的源码读取、业务学习和内部Agent编排。

---

## 2. Server侧系统边界

```text
┌────────────────────── Server控制面 ──────────────────────┐
│ Exploration Session API                                  │
│ Evidence Intake API                                      │
│ Status / Cancel / Authorization / Result / ACK            │
│ Durable Job State                                        │
└───────────────────────────┬──────────────────────────────┘
                            ▼
┌──────────────────── Browser Agent探索面 ──────────────────┐
│ Playwright Browser Context                                │
│ Observation Builder                                      │
│ Interactive Element Registry                             │
│ Stagehand Target Resolver                                │
│ Safe Action Policy                                       │
│ Transition Recorder                                      │
└───────────────────────────┬──────────────────────────────┘
                            ▼
┌──────────────────── 证据融合与草案生成 ──────────────────┐
│ Evidence Normalizer                                      │
│ Version Alignment Checker                                │
│ Interaction Fusion Engine                               │
│ Conflict Classifier                                      │
│ Confidence Evaluator                                     │
│ Execution Draft Compiler                                 │
└───────────────────────────┬──────────────────────────────┘
                            ▼
┌──────────────────── 正式执行与受控修复 ──────────────────┐
│ Authorization Verifier                                   │
│ Playwright Executor                                      │
│ Outcome Verifier                                         │
│ Runtime Observation                                      │
│ Repair Policy Guard                                      │
│ Runtime Repair Patch                                     │
│ Recorder / Trace / Failure Diagnostic                    │
└───────────────────────────┬──────────────────────────────┘
                            ▼
┌──────────────────── 素材与本地视频编辑 ──────────────────┐
│ AssetTimelineCatalog                                     │
│ DemoEditPlan                                             │
│ EditorSession                                            │
│ Preview Renderer                                         │
│ FFmpeg Final Renderer                                    │
│ RecordingResultPackage / Delivery / ACK                  │
└──────────────────────────────────────────────────────────┘
```

### 2.1 Server侧持有的数据

Server侧可持有：

- 探索会话、任务和状态元数据；
- 外部业务证据的结构化摘要和Hash；
- 脱敏页面Observation；
- 元素候选、冲突和融合结果；
- 执行草案、授权记录和Hash；
- Runtime Repair Patch；
- Step Result和Validation Result；
- 加密的截图、Trace、录屏和诊断产物；
- AssetTimelineCatalog、DemoEditPlan和EditorSession；
- 预览视频、最终视频和交付状态。

### 2.2 Server侧不应持久化的数据

默认不持久化：

- 完整客户源码；
- 明文密码、Token和API Key；
- Cookie和Authorization Header；
- localStorage/sessionStorage原始内容；
- 未脱敏完整HTML；
- 未经授权的客户页面数据；
- 已超出保留期限的敏感截图、Trace和录屏。

---

## 3. Server侧对外输入

本节只定义Server接口需要消费的数据，不规定这些数据在外部系统中如何生成。

### 3.1 ExplorationRequest

用于创建Server侧探索会话：

```json
{
  "schema_version": "demoops.exploration_request.v2-draft",
  "exploration_session_id": "explore_001",
  "idempotency_key": "project_001:goal_001:build_20260717",
  "project_id": "project_001",
  "business_goals": [
    {
      "intent_goal_id": "goal_create_project",
      "title": "创建一个演示项目",
      "required": true
    }
  ],
  "target_environment": {
    "base_url": "https://staging.example.com",
    "allowed_domains": ["staging.example.com"],
    "environment_name": "staging",
    "expected_deployment_version": "build-20260717",
    "frontend_build_id": "web-20260717.3"
  },
  "source_alignment": {
    "commit_sha": "<commit>",
    "code_snapshot_digest": "sha256:<digest>",
    "frontend_asset_digest": "sha256:<digest>"
  },
  "credential_grant_refs": ["grant_demo_user"],
  "browser": {
    "engine": "chromium",
    "locale": "zh-CN",
    "timezone": "Asia/Shanghai",
    "viewport": {"width": 1440, "height": 900}
  },
  "exploration_policy": {
    "mode": "safe_interactive",
    "allow_navigation": true,
    "allow_open_non_destructive_dialog": true,
    "allow_test_data_input": false,
    "allow_form_submission": false,
    "allow_destructive_actions": false,
    "allow_download": false,
    "allow_upload": false,
    "max_actions": 100,
    "max_runtime_sec": 300
  }
}
```

Server校验要求：

- `exploration_session_id`和`idempotency_key`非空；
- `base_url`属于`allowed_domains`；
- 浏览器和资源参数受Server上限约束；
- 凭据只接受grant/ref，不接受普通JSON明文；
- 探索策略不能扩大Server硬安全策略；
- 禁止危险操作时，模型或脚本均不能绕过。

### 3.2 ExternalBusinessEvidenceReport

Server需要接收结构化业务动作证据。字段名称可在联调中调整，但Server融合最少需要：

```json
{
  "schema_version": "demoops.external_business_evidence_report.v2-draft",
  "exploration_session_id": "explore_001",
  "report_revision": 3,
  "status": "ready",
  "source_alignment": {
    "commit_sha": "<commit>",
    "code_snapshot_digest": "sha256:<digest>",
    "frontend_asset_digest": "sha256:<digest>"
  },
  "actions": [
    {
      "semantic_action_id": "workspace.create_project",
      "intent_goal_id": "goal_create_project",
      "title": "创建项目",
      "business_purpose": "创建一个新的项目记录",
      "action_type": "click",
      "route_ref": "/workspace",
      "component_ref": "WorkspaceCreateButton",
      "runtime_hints": {
        "roles": ["button"],
        "names": ["创建项目", "新建项目"],
        "test_ids": ["create-project"],
        "selector_hints": ["[data-testid='create-project']"]
      },
      "preconditions": ["user_can_create_project"],
      "side_effects": ["project_record_created"],
      "risk_level": "write_low",
      "outcome_contract": {
        "expected_outcome": "创建项目表单打开",
        "validations": [
          {
            "id": "project_dialog_visible",
            "kind": "element_visible",
            "target": {"test_id": "create-project-dialog"},
            "required": true,
            "timeout_ms": 5000
          }
        ]
      },
      "evidence_summary": {
        "semantic_evidence_strength": "strong",
        "evidence_refs": ["external_ev_001", "external_ev_002"]
      }
    }
  ]
}
```

Server不要求接收完整源码，只要求能将业务语义、版本和证据引用稳定关联。

### 3.3 ExecutionAuthorization

Server完成融合并生成草案后，需要接收与草案Hash绑定的执行授权：

```json
{
  "schema_version": "demoops.execution_authorization.v2-draft",
  "authorization_id": "auth_001",
  "draft_id": "draft_001",
  "draft_hash_sha256": "sha256:<draft>",
  "approved_at": "2026-07-17T10:00:00Z",
  "reviewed_semantic_action_ids": ["workspace.create_project"],
  "runtime_repair_policy_hash_sha256": "sha256:<policy>",
  "signature": {
    "algorithm": "ed25519",
    "key_id": "approved_caller_key_001",
    "value": "<signature>"
  }
}
```

Server只执行与`draft_hash_sha256`和Repair Policy Hash完全一致的草案。

---

## 4. Server侧Browser Agent

### 4.1 Browser Agent职责

Browser Agent负责真实页面事实：

- 打开和登录目标环境；
- 探索允许域名内的页面；
- 识别可交互组件实例；
- 提取role、accessible name、testid、稳定属性和frame；
- 形成selector候选；
- 检查候选唯一性、可见性和可操作性；
- 在探索策略允许时执行安全状态转换；
- 记录动作前后页面状态；
- 执行结构化Validation；
- 形成脱敏的浏览器证据报告；
- 对不确定目标输出歧义，不强行选择。

Browser Agent不负责：

- 定义新的业务动作；
- 修改业务副作用；
- 修改输入数据；
- 修改业务成功标准；
- 扩大域名和凭据权限；
- 执行未获探索策略授权的危险动作。

### 4.2 内部模块

建议拆分：

```text
video-worker/src/browser-agent/
    contracts.ts
    exploration-runner.ts
    observation-builder.ts
    interactive-element-registry.ts
    deterministic-target-resolver.ts
    stagehand-target-resolver.ts
    safe-action-policy.ts
    transition-recorder.ts
    outcome-verifier.ts
    evidence-builder.ts
```

### 4.3 Observation Builder

Observation建议包括：

```json
{
  "observation_id": "browser_obs_001",
  "url_without_query": "https://staging.example.com/workspace",
  "page_title": "工作台",
  "page_fingerprint": "sha256:<digest>",
  "accessibility_snapshot_ref": "artifact://a11y/001",
  "interactive_elements": [
    {
      "element_ref": "element_001",
      "role": "button",
      "name": "创建项目",
      "test_id": "project-create-button",
      "frame": "main",
      "visible": true,
      "enabled": true,
      "match_count": 1
    }
  ],
  "screenshot_ref": "artifact://screenshot/001",
  "redaction_applied": true
}
```

禁止进入普通Observation：

- Cookie；
- Authorization Header；
- Storage原值；
- 密码；
- 输入框原值；
- 完整HTML；
- 未授权页面文本。

### 4.4 目标解析顺序

建议确定性Resolver优先，Stagehand作为语义识别和候选排序能力：

```text
稳定testid
    ↓
role + accessible name
    ↓
label
    ↓
稳定属性组合
    ↓
结构化CSS
    ↓
Stagehand候选
    ↓
视觉兜底（后续阶段）
```

禁止优先使用绝对坐标。

### 4.5 Stagehand接入模式

Stagehand应封装在接口之后：

```typescript
interface IntelligentTargetResolver {
  propose(input: ResolverInput): Promise<TargetCandidate[]>;
}
```

第一阶段：

```text
shadow / suggest only
```

只保存候选，不自动执行。完成真实样本评估后再开放低风险动作。

---

## 5. Server浏览器证据输出

```json
{
  "schema_version": "demoops.server_browser_evidence_report.v2-draft",
  "exploration_session_id": "explore_001",
  "report_revision": 4,
  "status": "ready",
  "runtime_alignment": {
    "detected_deployment_version": "build-20260717",
    "frontend_build_id": "web-20260717.3",
    "page_fingerprint": "sha256:<digest>"
  },
  "observations": [
    {
      "observation_id": "browser_obs_001",
      "intent_goal_id": "goal_create_project",
      "route": "/workspace",
      "interactive_candidates": [
        {
          "candidate_id": "candidate_001",
          "role": "button",
          "name": "创建项目",
          "test_id": "project-create-button",
          "selector_candidates": [
            "[data-testid='project-create-button']",
            "role=button[name='创建项目']"
          ],
          "frame": "main",
          "match_count": 1,
          "visible": true,
          "enabled": true,
          "stable_across_refresh": true,
          "evidence_refs": ["browser_ev_a11y_001", "browser_ev_screen_001"]
        }
      ],
      "safe_transition_evidence": {
        "executed": true,
        "candidate_id": "candidate_001",
        "validation_results": [
          {
            "validation_id": "project_dialog_visible",
            "passed": true
          }
        ]
      }
    }
  ]
}
```

Server必须区分：

- 只发现元素；
- 元素可执行；
- 已安全执行；
- 已验证结果。

元素存在不等于业务动作已经验证。

---

## 6. Server侧双证据融合

### 6.1 融合职责

Server侧`InteractionFusionEngine`负责：

- 校验两类证据属于同一探索会话；
- 校验代码快照与部署版本；
- 按`intent_goal_id`和`semantic_action_id`关联动作；
- 检查action type；
- 关联route/component和页面实例；
- 比较业务成功标准和运行时验证结果；
- 分类冲突；
- 计算分项置信度；
- 输出`MergedInteractionGraph`；
- 阻止不满足门槛的动作进入自动执行草案。

### 6.2 字段权威

Server融合时使用以下裁决表：

| 信息 | 融合中的权威 | Server行为 |
|---|---|---|
| 用户授权、安全策略 | 授权包 | 只能收紧，不能扩大 |
| 业务动作含义 | 外部业务证据 | 不由Browser Agent覆盖 |
| 前置条件、副作用 | 外部业务证据 | 页面外观不能替代 |
| action type和输入 | 外部业务证据/授权包 | 默认锁定 |
| 当前页面是否存在目标 | Server浏览器证据 | 以真实页面为准 |
| role/name/testid/frame | Server浏览器证据 | 以当前组件实例为准 |
| 业务成功标准 | 外部业务证据/授权包 | Server不能降低 |
| 成功标准是否满足 | Outcome Verifier | 以真实执行为准 |
| selector/frame/wait | Server运行时证据 | 可生成受控技术方案 |

### 6.3 四道硬门槛

进入执行草案前必须通过：

1. **版本门**：代码证据与部署版本一致或已得到显式豁免。
2. **语义门**：业务语义完整，页面候选不与其冲突。
3. **目标门**：候选唯一、可操作、不属于禁止目标。
4. **结果门**：关键动作包含required Validation，且证据强度满足策略。

硬门槛失败时，模型置信度不能绕过。

### 6.4 融合状态

```text
verified
partially_verified
external_only
server_only
ambiguous
conflict
version_skew
unavailable
```

默认只有`verified`动作可以自动进入执行草案。

### 6.5 冲突处理

#### 定位漂移

业务语义和Validation一致，但页面selector/testid变化：

```text
RUNTIME_TARGET_DRIFT
```

融合采用Server运行时目标，并保留原提示和漂移证据。

#### 页面能力缺失

外部证据声明能力存在，但当前环境或账号不可用：

```text
RUNTIME_CAPABILITY_UNAVAILABLE
```

Server不寻找其他业务动作替代。

#### 业务语义冲突

页面候选与外部业务语义不一致：

```text
SEMANTIC_TARGET_CONFLICT
```

拒绝该候选。

#### Action type冲突

```text
ACTION_TYPE_CONFLICT
```

不得由Server自动修改为另一种业务操作。

#### 版本冲突

```text
SOURCE_DEPLOYMENT_VERSION_MISMATCH
```

阻止融合，等待外部重新同步或显式确认。

#### 结果冲突

动作执行没有抛错，但required Validation失败：

```text
OUTCOME_VERIFICATION_FAILED
```

不得标记成功。

---

## 7. 置信度计算

### 7.1 原则

不直接采用Stagehand或LLM返回的单一置信度。Server保存以下分项：

```json
{
  "version_alignment": 1.0,
  "business_semantic": 0.96,
  "runtime_target": 0.95,
  "target_uniqueness": 1.0,
  "selector_stability": 0.92,
  "outcome_contract": 0.97,
  "runtime_outcome_evidence": 1.0,
  "overall": 0.92
}
```

### 7.2 分项来源

`business_semantic`来自外部业务证据强度，例如：

- 动作语义是否明确；
- 是否包含前置条件；
- 是否包含副作用；
- 是否提供组件/路由线索；
- 是否有稳定证据引用；
- 是否只有一条明确业务路径。

`runtime_target`来自Server浏览器证据：

- role/name/testid匹配；
- route和组件区域；
- 元素可见、enabled、无阻挡；
- frame明确；
- 页面刷新后仍稳定。

`selector_stability`初期按以下等级计算：

```text
稳定testid
  > role + accessible name
  > label
  > 稳定属性
  > CSS结构
  > 文本
  > DOM路径
  > 坐标
```

`outcome_contract`依据：

- 是否有结构化Validation；
- 是否有required Validation；
- 是否覆盖关键业务结果；
- 是否有业务证据支持。

`runtime_outcome_evidence`依据：

- 是否真实执行Validation；
- 是否观察动作前后状态；
- 是否在同一版本、账号和上下文验证；
- 是否多次执行一致。

### 7.3 总分

初期采用最弱环节原则：

```text
overall = min(
  business_semantic,
  runtime_target,
  selector_stability,
  outcome_contract,
  runtime_outcome_evidence
)
```

建议初始阈值：

| 条件 | Server行为 |
|---|---|
| 硬门槛通过且`overall >= 0.90` | 可进入草案 |
| `0.75 <= overall < 0.90` | 标记需补充证据/确认 |
| `overall < 0.75` | 不进入草案 |
| 存在硬冲突 | 直接阻止 |
| 自动运行时修复 | `overall >= 0.95`、候选唯一、复验通过 |

这些阈值只是工程初始值，必须通过联调样本校准。

---

## 8. MergedInteractionGraph

```json
{
  "schema_version": "demoops.merged_interaction_graph.v2-draft",
  "merge_id": "merge_001",
  "exploration_session_id": "explore_001",
  "external_report_revision": 3,
  "server_report_revision": 4,
  "version_alignment": {
    "status": "matched",
    "deployment_version": "build-20260717"
  },
  "actions": [
    {
      "semantic_action_id": "workspace.create_project",
      "intent_goal_id": "goal_create_project",
      "alignment_status": "verified",
      "business_definition": {
        "title": "创建项目",
        "action_type": "click",
        "preconditions": ["user_can_create_project"],
        "side_effects": ["project_record_created"],
        "risk_level": "write_low"
      },
      "runtime_target": {
        "route": "/workspace",
        "role": "button",
        "name": "创建项目",
        "test_id": "project-create-button",
        "selector_candidates": [
          "[data-testid='project-create-button']",
          "role=button[name='创建项目']"
        ],
        "frame": "main"
      },
      "outcome_contract": {
        "expected_outcome": "创建项目表单打开",
        "validations": [
          {
            "id": "project_dialog_visible",
            "kind": "element_visible",
            "target": {"test_id": "create-project-dialog"},
            "required": true
          }
        ]
      },
      "confidence": {
        "business_semantic": 0.96,
        "runtime_target": 0.95,
        "selector_stability": 0.92,
        "outcome_contract": 0.97,
        "runtime_outcome_evidence": 1.0,
        "overall": 0.92
      },
      "evidence_refs": [
        "external_ev_001",
        "browser_ev_a11y_001",
        "browser_ev_screen_001"
      ]
    }
  ],
  "conflicts": [],
  "blocking_findings": []
}
```

---

## 9. Server侧执行草案编译

Server侧从`MergedInteractionGraph`确定性编译：

1. `plan_json`：机器执行与审计主源；
2. `playwright_script`：从Plan确定性生成的受限执行产物；
3. `approval_markdown`：展示步骤、证据、风险和自动修复权限。

附加引用：

- `merged_interaction_graph_ref`；
- `exploration_evidence_manifest_ref`；
- `runtime_repair_policy`。

```json
{
  "schema_version": "demoops.execution_draft_bundle.v2-draft",
  "draft_id": "draft_001",
  "exploration_session_id": "explore_001",
  "merge_id": "merge_001",
  "status": "awaiting_approval",
  "compiler": {
    "name": "server_execution_draft_compiler",
    "version": "2.0.0-draft"
  },
  "plan_json": {},
  "playwright_script": {
    "mime_type": "text/typescript",
    "sha256": "sha256:<script>",
    "inline_source": "<restricted generated source>"
  },
  "approval_markdown": {
    "sha256": "sha256:<markdown>",
    "inline_markdown": "<approval document>"
  },
  "runtime_repair_policy": {},
  "reproducibility": {
    "merged_graph_hash_sha256": "sha256:<merged>",
    "plan_hash_sha256": "sha256:<plan>",
    "script_hash_sha256": "sha256:<script>",
    "markdown_hash_sha256": "sha256:<markdown>",
    "draft_hash_sha256": "sha256:<draft>"
  }
}
```

Server编译要求：

- 相同Merged Graph和编译器版本得到相同结果；
- 只编译允许状态的动作；
- 所有node绑定`semantic_action_id`；
- required动作必须有Validation；
- 不将Stagehand自由文本直接写成可执行代码；
- TS只能使用受限上下文API；
- 三件套和证据通过Hash绑定。

---

## 10. 正式执行与Outcome Verifier

### 10.1 Playwright职责

- 启动隔离浏览器；
- 执行goto/click/fill/select/wait/assert；
- 使用Playwright actionability；
- 处理frame、弹窗和导航；
- 执行网络白名单；
- 录屏、截图和Trace；
- 返回动作执行结果。

### 10.2 Outcome Verifier职责

第一版支持：

```text
url_matches
element_visible
element_hidden
text_contains
attribute_equals
value_equals
element_count
page_title_contains
```

Validation结果：

```json
{
  "validation_id": "project_dialog_visible",
  "kind": "element_visible",
  "required": true,
  "passed": true,
  "expected": "test_id=create-project-dialog visible",
  "observed": "visible=true, match_count=1",
  "duration_ms": 183,
  "evidence_refs": ["screenshot_step_003"]
}
```

必须改正当前代码中将`expected_outcome`直接当作`observed_state`的行为。真实Observed State必须来自页面检查结果。

---

## 11. Server侧运行时小修复

### 11.1 自动修复范围

在已授权Repair Policy内允许：

- 已验证selector alternative；
- CSS切换为testid；
- CSS切换为role/name；
- 同`semantic_action_id`的目标重新定位；
- frame修正；
- DOM层级变化；
- 有上限的timeout调整；
- 有上限的wait condition调整；
- 已授权的普通遮罩或非业务弹窗处理。

### 11.2 禁止自动修复

- 修改`semantic_action_id`；
- 修改业务步骤；
- 修改action type；
- 修改输入值、input_ref或secret_ref；
- 修改URL或扩大域名；
- 插入、删除、重排、跳过required步骤；
- 修改expected outcome；
- 修改Validation；
- 替换为不同业务功能；
- 修改高风险目标；
- 使用`server_only`、`ambiguous`或`conflict`动作。

### 11.3 Runtime Repair Policy

```json
{
  "schema_version": "demoops.runtime_repair_policy.v2-draft",
  "mode": "auto_apply_low_risk",
  "editable_fields": [
    "action.target.selector",
    "action.target.selector_alternatives",
    "action.target.test_id",
    "action.target.role",
    "action.target.text",
    "action.target.label",
    "action.target.frame",
    "action.timeout_ms",
    "action.wait_until"
  ],
  "immutable_fields": [
    "node_id",
    "semantic_action_id",
    "action.type",
    "action.value",
    "action.input_ref",
    "action.secret_ref",
    "page_target.url",
    "expected_outcome",
    "validations",
    "blocking"
  ],
  "same_node_only": true,
  "same_semantic_action_only": true,
  "same_action_type_only": true,
  "same_domain_only": true,
  "max_repair_attempts_per_step": 2,
  "max_total_repair_attempts": 5,
  "max_runtime_ms_per_repair": 30000,
  "min_auto_apply_confidence": 0.95,
  "require_unique_candidate": true,
  "require_outcome_reverification": true
}
```

### 11.4 Runtime Repair Patch

原始草案不可变：

```text
Approved Draft + Runtime Repair Patch = Effective Execution Plan
```

```json
{
  "schema_version": "demoops.runtime_repair_patch.v2-draft",
  "patch_id": "runtime_patch_001",
  "execution_id": "execution_001",
  "base_draft_hash_sha256": "sha256:<draft>",
  "base_plan_hash_sha256": "sha256:<plan>",
  "node_id": "node_create_project",
  "semantic_action_id": "workspace.create_project",
  "repair_kind": "selector_strategy",
  "operations": [
    {
      "op": "replace",
      "field": "action.target",
      "old_value": {"selector": "button.create"},
      "new_value": {
        "role": "button",
        "name": "创建项目",
        "test_id": "project-create-button"
      }
    }
  ],
  "policy_decision": {
    "within_authorized_scope": true,
    "risk_level": "low",
    "confidence": 0.97
  },
  "outcome_reverification": {
    "required": true,
    "passed": true,
    "validation_ids": ["project_dialog_visible"]
  },
  "patch_hash_sha256": "sha256:<patch>"
}
```

修复成功后步骤标记`passed_repaired`并继续；复验失败则回退候选或停止。

---

## 12. Server侧本地轻量视频编辑器

### 12.1 产品定位

编辑器类似“轻量版剪映”，只负责对已执行、已验证的录制素材进行简单编排。

编辑器不修改：

- Browser执行计划；
- 业务步骤；
- Validation结果；
- Runtime Repair记录；
- 原始录屏和Trace事实。

### 12.2 复用现有代码

当前已有：

- `AssetTimelineCatalog`；
- `DemoEditPlan`；
- DemoEditPlan Validator；
- Director Patch；
- FFmpeg trim/concat；
- 字幕烧录；
- RecordingResultPackage。

编辑器应直接以`DemoEditPlan`为保存格式，不创建第二套时间线真相。

### 12.3 MVP功能

- 素材列表；
- 视频播放器；
- 单视频轨道；
- 字幕轨道；
- 业务步骤标记轨道；
- 按步骤自动切分镜头；
- 调整片段起止时间；
- 删除非required等待片段；
- 修改字幕和字幕时间；
- 保存草稿；
- 撤销和重做；
- 低清预览；
- 最终MP4渲染；
- 发布前EditPlan校验。

第一版暂不开放：

- 多层专业视频轨道；
- 复杂关键帧；
- 专业调色；
- 自动抠图；
- 完整特效市场；
- 当前FFmpeg合成器尚未真实支持的效果。

当前渲染器真实稳定支持的重点是：

```text
trim
concat
caption
```

编辑器必须从Server获取`render_capabilities`，不能展示无法渲染的功能。

### 12.4 EditorSession

```json
{
  "schema_version": "demoops.editor_session.v1-draft",
  "editor_session_id": "editor_001",
  "source_execution_id": "execution_001",
  "source_result_id": "result_001",
  "catalog_id": "catalog_001",
  "base_plan_hash_sha256": "sha256:<edit-plan>",
  "plan_revision": 4,
  "status": "draft",
  "demo_edit_plan": {},
  "validation": {
    "valid": true,
    "errors": [],
    "warnings": []
  },
  "preview_ref": null,
  "updated_at": "2026-07-17T10:00:00Z"
}
```

`PATCH plan`使用`base_revision`防止覆盖：

```json
{
  "base_revision": 4,
  "operations": [
    {
      "op": "replace",
      "path": "/shots/0/source_time_range_ms",
      "value": [1200, 6800]
    }
  ]
}
```

### 12.5 编辑器接口

```text
POST  /v2/editor-sessions
GET   /v2/editor-sessions/:id
PATCH /v2/editor-sessions/:id/plan
POST  /v2/editor-sessions/:id/validate
POST  /v2/editor-sessions/:id/preview
GET   /v2/editor-sessions/:id/preview/status
POST  /v2/editor-sessions/:id/publish
GET   /v2/editor-sessions/:id/render/status
```

媒体读取接口必须支持HTTP Range。

---

## 13. Server侧API建议

### 探索与证据

```text
POST /v2/exploration-sessions
PUT  /v2/exploration-sessions/:id/external-business-evidence
GET  /v2/exploration-sessions/:id/status
GET  /v2/exploration-sessions/:id/browser-evidence
POST /v2/exploration-sessions/:id/merge
GET  /v2/exploration-sessions/:id/merge-result
```

### 草案和执行

```text
POST /v2/execution-drafts
GET  /v2/execution-drafts/:id
POST /v2/execution-drafts/:id/authorize
POST /v2/executions
GET  /v2/executions/:id/status
POST /v2/executions/:id/cancel
GET  /v2/executions/:id/result
POST /v2/executions/:id/result/ack
```

### 编辑和渲染

```text
POST  /v2/editor-sessions
GET   /v2/editor-sessions/:id
PATCH /v2/editor-sessions/:id/plan
POST  /v2/editor-sessions/:id/validate
POST  /v2/editor-sessions/:id/preview
POST  /v2/editor-sessions/:id/publish
```

大文件不通过控制面JSON内联，使用加密artifact descriptor、checksum、size、recipient key和retention策略。

---

## 14. Server状态机

### 14.1 探索状态

```text
created
waiting_for_external_evidence
server_exploring
ready_to_merge
merging
merged
blocked_by_conflict
cancelled
expired
```

### 14.2 执行状态

```text
awaiting_approval
authorized
queued
preparing_worker
running_script
verifying_outcome
observing_failure
planning_repair
validating_repair_policy
executing_repair
verifying_repair_outcome
recording_completed
awaiting_edit
preview_rendering
final_rendering
completed
completed_with_repair
failed
cancelled
```

### 14.3 步骤状态

```text
passed_original
passed_repaired
failed_execution
failed_outcome_verification
repair_rejected_by_policy
repair_requires_approval
repair_attempts_exhausted
runtime_business_conflict
```

---

## 15. 错误码

```text
SOURCE_DEPLOYMENT_VERSION_MISMATCH
EXTERNAL_BUSINESS_EVIDENCE_MISSING
SERVER_BROWSER_EVIDENCE_MISSING
SEMANTIC_ACTION_ID_MISSING
SEMANTIC_TARGET_CONFLICT
ACTION_TYPE_CONFLICT
AMBIGUOUS_SEMANTIC_TARGET
RUNTIME_TARGET_DRIFT
RUNTIME_CAPABILITY_UNAVAILABLE
OUTCOME_CONTRACT_MISSING
OUTCOME_VERIFICATION_FAILED
REPAIR_POLICY_REJECTED
REPAIR_CONFIDENCE_TOO_LOW
REPAIR_CANDIDATE_NOT_UNIQUE
REPAIR_ATTEMPTS_EXHAUSTED
DRAFT_HASH_MISMATCH
EXECUTION_AUTHORIZATION_INVALID
EDIT_PLAN_REVISION_CONFLICT
EDIT_PLAN_VALIDATION_FAILED
PREVIEW_RENDER_FAILED
FINAL_RENDER_FAILED
```

错误响应：

```json
{
  "code": "AMBIGUOUS_SEMANTIC_TARGET",
  "message": "当前页面有多个候选，无法安全选择。",
  "retryable": false,
  "stage": "merging",
  "semantic_action_id": "workspace.create_project",
  "evidence_refs": ["browser_ev_a11y_001"],
  "suggested_action": "补充业务证据或确认目标候选。"
}
```

---

## 16. 版本、幂等与审计

所有写接口支持：

```text
schema_version
request_id
idempotency_key
created_at
producer
producer_version
content_hash_sha256
```

一致性要求：

1. `exploration_session_id`贯穿证据、融合、草案和执行。
2. 证据报告使用递增`report_revision`。
3. Fusion记录使用的双方报告revision。
4. Draft绑定MergedInteractionGraph Hash。
5. Authorization绑定Draft和Repair Policy Hash。
6. Runtime Patch绑定原Draft和Plan Hash。
7. EditorSession绑定Execution Result、Catalog和EditPlan revision。
8. 所有Agent候选、Policy决策和Validation结果进入审计链。

---

## 17. 现有代码保留与重构

### 17.1 直接保留

- Playwright执行和录制；
- `browser-recorder.ts`；
- `script-runner.ts`；
- SandboxPolicy和allowed domains；
- Graph、Plan、TS和Hash校验；
- Failure Diagnostic和Repair Request；
- RecordingResultPackage；
- `AssetTimelineCatalog`；
- `DemoEditPlan`；
- Director Patch校验机制；
- FFmpeg渲染和媒体回验。

### 17.2 需要重构

`interaction-verifier.ts`拆为：

```text
observation-builder
interactive-element-registry
deterministic-target-resolver
outcome-verifier
```

`renderer.ts`逐步拆为：

```text
timeline-catalog.ts
edit-plan.ts
edit-plan-validator.ts
ffmpeg-compositor.ts
media-probe.ts
render-manifest.ts
```

### 17.3 暂时退出核心链路

- Ark媒体生成；
- 候选生成视频；
- 自动Candidate Asset Patch；
- 用户不可见的自动Director二次渲染；
- 当前无法真实烧录的复杂编辑操作。

建议通过feature flag保留，不立即删除。

---

## 18. Server侧开发计划

### 阶段一：探索和融合底座

交付：

- ExplorationSession模型和状态机；
- External Evidence Intake；
- Browser Agent安全探索；
- Observation Builder；
- ServerBrowserEvidenceReport；
- Version Alignment Checker；
- Fusion Engine第一版；
- 冲突分类和分项置信度。

验收：

- 同一session的双证据可关联；
- 版本不一致时阻止融合；
- 页面目标存在歧义时不强行选择；
- Observation不包含Cookie、Storage和输入原值。

### 阶段二：草案、验证和运行时修复

交付：

- MergedInteractionGraph；
- ExecutionDraft Compiler；
- Draft Hash和Authorization校验；
- Outcome Verifier白名单；
- Runtime Repair Policy Guard；
- Runtime Repair Patch；
- Stagehand shadow模式。

验收：

- verified动作可确定性生成执行草案；
- required Validation失败不能标记成功；
- selector低风险修复成功后继续执行；
- 修复不改变原草案和业务字段；
- 所有修复可审计和回放。

### 阶段三：本地轻量视频编辑器

交付：

- EditorSession；
- 素材区、视频预览和单轨时间线；
- trim、caption和草稿保存；
- 撤销/重做；
- Preview Render；
- Final Render；
- 编辑计划校验和revision控制。

验收：

- 执行结果可生成默认时间线；
- 可以裁剪等待片段和修改字幕；
- required步骤顺序不被改变；
- 预览和最终视频绑定同一EditPlan revision；
- 最终视频可追溯到执行结果和素材Hash。

### 阶段四：受约束分镜设计与多 Provider 分镜头实现

目标：在不让生成模型取得业务事实、最终分镜或编辑器控制权的前提下，提高分镜质量，并把 Seedance 2.5 与 MiniMax-H3 纳入同一套非权威展示镜头实现层。

固定流程：

```text
App 业务要求和视频要求
    -> Server 校验并编译为 StoryboardConstraintSet
    -> Browser Agent 真实执行、录屏、截图和结果验证
    -> video-worker 基于真实素材生成确定性分镜草稿
    -> 可选规划模型生成 DirectorEditSuggestion
    -> Server 将建议转换为字段受限的 DirectorEditPlanPatch
    -> 重新校验 App 约束、事实素材、步骤覆盖和媒体能力边界
    -> Server/Renderer 产出最终可执行 DemoEditPlan
    -> Seedance 2.5 / MiniMax-H3 实现被批准的非事实展示镜头
    -> Provider 输出规范化、内容审核、A/B 选择或 fallback
    -> Renderer 校验并合成最终视频
```

App 约束不是末尾补做的验收项，而是从分镜草稿开始贯穿整个流程的 Server 硬约束。至少包括：

- Stage 和 required 业务步骤顺序；
- 必须展示、禁止展示和重点展示的内容；
- 目标总时长、阶段时长和输出画布；
- 真实 `source_artifact_id`、`source_step_id` 和录屏时间范围；
- 字幕、特写、截图、标注和验证结果要求；
- 事实轨与展示轨边界；
- 生成参考素材数量、类型、时长和帧模式限制；
- 任何超出当前 Server Capability Profile 的生成请求必须在 Provider 调用前拒绝。

分镜文件的所有权保持如下：

| 文件 | 生产者 | 权限 |
| --- | --- | --- |
| `storyboard_constraint_set.json` | Server 根据 App 包和当前 Capability Profile 编译 | 分镜全流程硬约束，不允许模型修改 |
| `demo_edit_plan_draft.json` | Server/video-worker 确定性代码 | 受约束的基础分镜草稿 |
| `director_edit_suggestion.json` | 可选规划模型；无模型时由确定性 Adapter 生成 | 仅为建议，不可直接渲染 |
| `director_edit_plan_patch.json` | Server Patch Builder | 只包含白名单展示字段，必须校验 |
| `demo_edit_plan.json` | Server/Renderer 受控合并和裁决 | 唯一可执行分镜，必须通过 Renderer 校验 |
| `candidate_asset_edit_plan_patch.json` | Server 根据已审核生成候选构造 | 默认不自动应用，不得绑定业务步骤 |

规划模型只允许建议：

- 节奏、字幕、特写、转场和展示重点；
- 非事实展示镜头的用途、位置和候选 Prompt；
- 从 App 已授权素材集合中选择参考素材的建议；
- intro、outro、section divider、abstract B-roll 和 brand atmosphere。

规划模型不得修改：

- App 业务意图、Stage 顺序、动作类型和成功标准；
- required 步骤及其真实素材绑定；
- 事实镜头的来源、时间范围和验证结论；
- 敏感信息、安全策略和允许域名；
- Server Capability Profile 和 Provider 调用边界；
- 最终 `DemoEditPlan` revision。

分镜头实现层同时考虑 Seedance 2.5 和 MiniMax-H3：

| 模式 | 编排方式 | 使用条件 |
| --- | --- | --- |
| `normal` | 用户显式选择 Seedance 2.5 或 MiniMax-H3 实现已批准的展示镜头 | 单 Provider，不自动 fallback |
| `comparison` | Seedance 2.5 和 MiniMax-H3 使用同一展示意图及各自合法参数产出 A/B 候选 | 分别授权并由用户显式选择 |
| `fallback` | Seedance 失败后，MiniMax-H3 使用原始受控素材独立实现相同展示意图 | H3 路由、能力校验和质量门禁全部就绪后 |

禁止把 Seedance 输出送入 H3 二次生成，或把 H3 输出送入 Seedance 二次生成。两路 Provider 的请求/响应保持隔离，只在 Server 内部候选协议层汇合。

所有生成候选在进入编辑器前必须完成：

```text
Provider Adapter 归一化任务状态和输出 URL
    -> 下载并保存 original artifact + SHA-256
    -> ffprobe 原始媒体
    -> FFmpeg 转换为编辑器媒体 Profile
    -> ffprobe normalized artifact + 新 SHA-256
    -> 结构审核和内容审核
    -> 显式选择或批准
    -> Renderer 再校验
```

编辑器媒体 Profile 初始固定为：

```text
MP4 / H.264 / yuv420p / 1920x1080 / CFR 30fps
```

生成素材必须保持 `non_authoritative=true`、`presentation_only=true`，不得设置业务 `source_step_id`，不得替换真实 UI、按钮、数字、表格、状态或业务结果。生成失败、格式不兼容、审核失败或用户拒绝候选时，流水线必须继续使用只包含真实素材的确定性分镜交付。

交付拆分：

1. `StoryboardConstraintSet` Schema、App 要求编译器和字段级错误报告；
2. 将当前基础 `demo_edit_plan.json` 拆分为可审计草稿与最终受控 revision；
3. 规划模型 Adapter、结构化建议 Schema、超时/失败回退和零直接生效机制；
4. Patch 白名单、App 约束复验、事实素材锁定和 required-step 覆盖验证；
5. Provider-neutral `GeneratedShotIntent`、Seedance 2.5 Adapter 和独立 H3 Adapter；
6. H3 查询、下载、取消、独立 feature flag、配额、幂等和错误分类；
7. 候选 `ffprobe -> FFmpeg -> ffprobe` MediaNormalizer；
8. `normal`、`comparison`、`fallback` 路由及 A/B 候选关系记录；
9. 内容审核、显式选择、EditorSession 候选入口和最终 Renderer 门禁；
10. 端到端回归：没有显式启用时零 Provider 调用，超能力边界时零 Provider 调用，模型失败不阻塞真实素材交付。

验收：

- 每个最终镜头都能追溯到 App 约束、真实素材或已批准的非权威展示意图；
- required 步骤、顺序、事实素材和真实时间范围不会被规划模型或视频生成模型修改；
- 规划模型输出非法、超时或缺失时，确定性分镜仍可独立完成交付；
- Seedance 2.5 与 H3 原始格式不同也只能通过统一 normalized artifact 进入编辑器；
- `comparison` 能产生可追溯 A/B 候选，但不会自动选择或加入时间线；
- `fallback` 不使用前一模型产物作为后一模型输入；
- H3 未正式接入路由前，任何普通 E2E、真实媒体模式或 MiniMax 通用密钥配置都不会触发 H3；
- 仅尾帧等未开放模式在网络调用前拒绝，Provider HTTP 调用次数为 0；
- 所有生成素材失败时，最终交付仍由真实录屏、截图和确定性编辑计划完成。

当前状态说明：基础 `DemoEditPlan`、确定性 DirectorSuggestion、受限 Director Patch、Seedance 候选登记和生成候选 Renderer 门禁已有部分实现；H3 已具备独立创建/查询/取消删除 Client、默认关闭的 Sidecar 配置工厂、有界轮询、时效 URL 下载、隔离的 `ffprobe -> FFmpeg -> ffprobe` MediaNormalizer，以及未注册路由的单进程 Admission 治理层。MediaNormalizer 已锁定 MP4/H.264/yuv420p/1920×1080/CFR 30fps，并区分 original/normalized artifact 和 SHA-256；Admission 已覆盖显式成本估算、周期配额、并发、超时与 Server 内部幂等，但尚未实现跨进程持久化和实际 usage 对账。H3 仍未注册到 Server 执行路由、尚未连接 EditorSession、尚未经过真实 H3 素材调用验收。`StoryboardConstraintSet`、真正的规划模型、H3 正式路由、A/B 选择和 fallback 编排仍未完成，不能把本阶段目标当作当前运行能力。

---

## 19. Server侧测试用例

### 双证据一致

业务证据与页面实例对齐，Server生成草案并成功执行。

### selector漂移

外部提示和真实页面testid不同，但语义与Validation一致；Server记录漂移并采用真实目标。

### 正式执行时DOM再次变化

Server生成Runtime Patch，复验原Validation成功后继续执行，结果为`completed_with_repair`。

### 业务语义冲突

页面候选与业务证据不一致；Server拒绝候选，不生成可自动执行步骤。

### 版本不一致

证据版本与部署build不匹配；Server阻止融合。

### 权限不足

当前账号没有目标功能；Server返回`RUNTIME_CAPABILITY_UNAVAILABLE`。

### Outcome假成功

Playwright点击未报错但Validation失败；步骤必须失败。

### 编辑器

录制成功后创建EditorSession，完成trim和字幕修改，预览和最终渲染成功，业务步骤审计不变。

---

## 20. Server侧当前待确认的接口依赖

以下事项需要通过联调协议确认，但不涉及外部系统内部实现：

1. 外部业务证据的最终字段名和schema版本。
2. `semantic_action_id`的稳定命名和生命周期。
3. commit、build ID和部署版本的关联字段。
4. 外部证据允许传输的摘要粒度。
5. 哪些探索动作可获得授权。
6. 哪些动作必须提供required Validation。
7. `partially_verified`是否允许经显式授权进入草案。
8. Runtime Repair Policy初始允许字段。
9. 自动修复最大次数、时间和置信度阈值。
10. 成功Runtime Patch的回传格式。
11. Observation是否允许发送到外部模型。
12. 探索证据、视频和Trace的保留期限。
13. 编辑器承载形态及媒体访问方式。

---

## 21. Server侧最终任务边界

> Server侧接收版本化的业务动作证据和探索请求，使用Browser Agent在真实部署页面中建立组件实例和运行时行为证据，并在Server内部完成版本对齐、双证据融合、冲突分类、分项置信度计算和执行草案编译。Server只执行经过Hash授权的草案。正式执行中，Playwright负责确定性动作和录制，Outcome Verifier负责真实结果验证；selector、frame和有限wait等低风险漂移可在授权Repair Policy内由Server生成Runtime Patch、复验原Validation并继续执行，不默认退回上游。执行完成后，Server基于现有AssetTimelineCatalog和DemoEditPlan提供本地轻量视频编辑器，完成素材编排、预览、最终渲染和结果交付。Server不负责外部业务证据如何生成，也不修改业务含义、动作类型、输入数据、安全边界或成功标准。

---

## 22. 下一步开发计划：协议允许的运行时修复补全

### 当前状态

Server 已落地 Repair Policy 第一版：它校验提案的 run/bundle/policy hash、同节点边界、允许修复种类、可编辑字段、置信度阈值和次数上限；批准后只修改本次运行的内存 Stage，复验并写入 Patch Ledger，不改写 App 原包或审批 hash。

当前仍存在“协议允许但尚未完整落地”的能力，必须进入后续开发计划。它们都只能修复页面实现漂移，不能修改业务意图、动作类型、输入语义、密钥、Stage 顺序、域名、禁止页面、破坏性标记或成功标准。

### P0：真实修复提案生成与接入

- 为 Validation Agent 接入 `RuntimeRepairProposal` 生产接口；Validation Agent 只能提出提案和证据，不能直接操作浏览器或应用补丁。
- 在 Server 未接到外部 Validation Agent 前，实现受限的确定性提案生成器：仅从已审批的 selector alternatives、页面真实观察和已存在 wait/capture 字段中提出候选。
- 提案必须包含同一 run/node/stage、bundle hash、policy hash、修复前后值、置信度与证据引用；缺一项即停止并打包失败诊断。

验收：真实任务能在不改 App 上传字段的前提下产生可审计提案；无提案的 `repair_allowed` 不得伪造修复成功。

### P1：语义定位器修复闭环

- 扩展 selector alternative：按 role/name、test id、label、已验证 selector 的既定优先级重新观察和排序。
- 对候选目标做唯一性、可见性、目标合同、同域和非破坏性检查；歧义候选必须停止，不得猜选。
- 将修复前定位失败原因、候选摘要、采用原因和复验结果写入 Patch Ledger 与 Stage 事件。

验收：selector/testid 漂移但语义合同一致时，可在同一 Stage 内重新解析、执行和验证；多个候选或目标合同不一致时返回失败诊断。

### P2：等待和截图时机修复补全

- 支持 Stage 级与 interaction 级 wait 条件的受控替换；只允许增补或替换已批准的等待语义，设置每次与总运行时上限。
- 支持 Capture Plan 缺失时的受限补建，以及 `pre_capture_wait_ms`、`hold_after_ms` 的安全调整。
- 以真实页面加载状态、元素可见性和截图证据为依据提出提案；不能因固定 sleep 成功就宣称业务结果通过。

验收：异步渲染导致的截图过早或等待不足可修复并复验；超过时限、缺真实证据或改变业务验证条件时停止。

### P3：frame resolution 修复

- 实现协议中已允许的 `frame_resolution`：在相同页面、相同 Stage、相同动作和相同语义目标范围内重新定位 iframe/frame。
- Worker 在重新定位 frame 后必须重新解析目标、执行原动作并复验原 Outcome；不允许跨域 frame、控制面 frame 或任意注入脚本。

验收：同域 iframe 路径/层级漂移可修复；跨域、歧义、越过允许层级或改变目标合同的 frame 修复必须拒绝。

### P4：修复结果与验收强化

- 将修复后重新观察、重新解析、重试动作和重新验证分别记录为尝试序号递增的 Stage 事件。
- Patch Ledger 增加拒绝原因、应用结果和复验结论的结构化展示；Editor Session 与桌面端 Stage Explorer 显示中文摘要。
- 在允许执行测试二进制的 CI/隔离环境运行真实修复回归：selector、wait、capture timing、frame；同时验证所有越权修改被拒绝。

验收：每次修复可回放、可定位、可解释；最终 Result Packager 只有一个失败诊断聚合出口，成功与失败都保留完整审计链。
