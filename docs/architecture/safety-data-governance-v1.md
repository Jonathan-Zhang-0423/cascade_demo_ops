# DemoOps 安全数据与门禁治理规范 v1

> 状态：目标规范
> 适用范围：Desktop/App、控制面、Gateway、Worker、Browser Runtime、Artifact Store、Editor、FinalFilm 和模型 Provider
> 原则：分级策略可调，硬安全底线不可关闭

## 1. 目标

本规范回答四个问题：数据是什么、可以去哪里、谁能处理、何时必须阻断。安全控制不再分散为无法解释的布尔字段，而由数据等级、执行策略、Gate Decision 和审计事件共同表达。

## 2. 数据等级

| 等级 | 名称 | 示例 | 默认处理原则 |
| --- | --- | --- | --- |
| D0 | `public_config` | 公开模型名、分辨率、模块版本、非敏感模板 | 可持久化和进入普通日志 |
| D1 | `operational_metadata` | run ID、状态、耗时、错误码、预算使用、结构化审计 | 可持久化；不得夹带业务正文或 secret |
| D2 | `customer_business_data` | 用户需求、业务步骤、代码结构摘要、页面语义摘要、字幕 | 目的限定、最小化、按项目隔离 |
| D3 | `sensitive_artifact` | 源码窗口、截图、DOM/a11y 摘要、Trace、录屏、原始音视频 | 仅 Artifact 引用传递；访问与留存受控 |
| D4 | `credential_secret` | 密码、Cookie、API Key、Token、私钥、Storage 原值 | 只允许本地 vault 或短期内存；禁止成为 Artifact |

数据等级由生产者声明、接收端验证。接收端只能提高等级，不能自行降低。混合数据采用其中最高等级。

## 3. 驻留与访问矩阵

| 位置 | D0 | D1 | D2 | D3 | D4 |
| --- | --- | --- | --- | --- | --- |
| Desktop 持久状态 | 允许 | 允许 | 项目范围内允许 | 仅受管 Artifact descriptor | 仅 opaque ref |
| 本地凭据库 | 不适用 | 最小元数据 | 禁止 | 禁止 | 允许，用户可撤销 |
| 控制面数据库 | 允许 | 允许 | 摘要和引用 | descriptor，不保存 blob | 禁止 |
| Gateway spool | 允许 | Job 范围内 | 已批准包的加密载荷 | 加密 Artifact 临时存储 | 禁止落盘 |
| Worker 内存 | 允许 | 允许 | 任务所需最小集合 | 任务所需 | 短期、按 scope、终止即销毁 |
| Browser Trace/日志 | 允许 | 允许 | 脱敏摘要 | 明确授权后允许 | 禁止 |
| 模型 Provider | 公开配置 | 调用元数据 | 仅 outbound manifest 声明字段 | 仅经授权的媒体/截帧 | 禁止 |
| Review Package | 允许 | 允许 | 导演计划和字幕 | 明确列入 manifest 的素材 | 禁止 |

## 4. 不可关闭的硬底线

以下规则对开发、内测和生产环境均生效：

1. D4 只能以 opaque ref 存在于计划、Job、事件和 Artifact descriptor 中；真实值仅在本地 vault、加密传输内存和活动 Worker 内存中短暂出现。
2. 认证表单处理期间不得开启包含源码或输入值的 Trace；凭据使用结束后清零 broker 和任务内存。
3. 文件读写必须解析到声明的 project、workspace 或 artifact root；拒绝路径穿越、任意 host mount 和未声明输出目录。
4. 网络权限、allowed domains/routes、Provider 和操作集合只能逐层收窄，不允许下游模块扩大。
5. destructive 或 `once_effect` 操作必须绑定授权、幂等键和恢复策略；状态不确定时禁止自动重放。
6. 跨进程、跨机器、上传下载和 Provider 边界必须验证身份、用途、大小、媒体类型及完整性。沿用现有 SHA-256 实现，不引入新的摘要体系。
7. Provider 调用必须先持久化 admission、预算、attempt 上限和外部 task ID；恢复只能 poll/resume 已有任务。
8. 日志、错误、事件和模型 prompt 禁止包含 Cookie、Authorization、Storage 原值、密码、API Key、私钥和完整未脱敏 HTML。
9. 大型截图、DOM、Trace、录屏和媒体只通过 Artifact 引用传递。单个 Execution Event 的规范化 JSON 不得超过 512 KiB。
10. 所有授权、阻断、敏感数据读取、Provider 消费、Artifact 发布/删除和最终终审必须产生结构化审计事件。

## 5. 环境策略档位

| 控制项 | 开发 `dev` | 内测 `internal` | 生产 `production` |
| --- | --- | --- | --- |
| 低风险动作人工逐项审批 | 可关闭 | Task Pack 可配置 | 风险策略决定 |
| destructive/once-effect 授权 | 必须 | 必须 | 必须且可追溯 reviewer |
| 浏览器隔离 | 本地 sidecar | per-job container 推荐 | per-job container，企业可 microVM |
| Trace | 默认关闭登录阶段，可手动启用 | 执行阶段启用并脱敏 | 按组织策略、最小化启用 |
| 原始截图/录屏远端留存 | 最长 24 小时 | 默认 7 天 | 默认 24 小时，可按合同延长 |
| D1 审计留存 | 7 天 | 30 天 | 90 天或组织策略 |
| D2 远端摘要留存 | 7 天 | 30 天 | 30 天或组织策略 |
| 最终审核包 | 本地直到用户删除 | 本地直到用户删除；远端 30 天 | 按交付合同，默认 30 天 |
| 深度视觉质检/盲审 | 可跳过并标记 | 自动质检必需 | 自动质检和发布验收策略 |
| Artifact 加密 | 敏感数据必须 | D2/D3 必须 | D2/D3 必须，企业可 customer KMS |

环境策略不得改变硬底线。缩短留存可以直接生效；延长 D2/D3 留存必须产生策略变更审计。

## 6. 数据生命周期

### 6.1 收集

- 每个 Module Manifest 声明所需输入角色和最高数据等级。
- 代码理解默认读取结构和小型证据窗口，不保存完整源码正文。
- 页面验证默认保存脱敏语义、状态指纹和必要截图，不保存完整 HTML 或表单值。

### 6.2 使用

- 每次 D2/D3 访问记录 purpose、module run、artifact ID 和结果，不记录数据正文。
- 模型调用必须生成 outbound manifest：Provider、模型、目的、字段/Artifact ID、数据等级、授权引用和保留声明。
- Director 只能消费事实轨摘要与批准素材，不得读取凭据或任意项目文件。

### 6.3 交付

- Artifact descriptor 明确 logical role、来源、revision、数据等级、是否 required、大小和留存。
- Editor/FinalFilm readiness 仅根据声明为 required 的输入判断；可选诊断素材缺失产生 warning，不得覆盖已完成事实结果。

### 6.4 删除

- 删除必须覆盖 blob、临时副本、部分下载、解密工作区和 Provider 临时引用，并产生 `artifact_deleted` 审计事件。
- 任务终止后立即销毁 D4 和解密载荷；租约状态不得阻止已经到期的敏感 Artifact 清理。
- 本地用户主动删除项目时，先展示受影响 Artifact 和审核包，再执行有界目录删除。

## 7. Gate Decision

统一结论：

- `pass`：允许进入下一状态。
- `warn`：允许继续，但必须呈现风险和审计记录。
- `block`：当前输入或动作违反硬约束，不能继续。
- `defer`：等待外部状态、凭据、用户输入或异步 Provider；不是失败。

每个 Gate Decision 必须包含：

- `gate_id`、`gate_type`、`decision`；
- `owner`: `user | module | platform | provider | operator`；
- `reason_code` 和面向用户的脱敏摘要；
- `evidence_refs` 和可选 `artifact_refs`；
- `remediation`、`next_operation`；
- `evaluated_at` 和可选 `expires_at`；
- 绑定的 workflow/module run/revision。

标准 Gate 目录：

| Gate | 典型判定 | 默认负责人 |
| --- | --- | --- |
| Admission | schema、能力、预算、依赖是否满足 | platform |
| Data Access | 数据等级、purpose、驻留和授权 | platform/user |
| Evidence Readiness | 事实是否足够制定计划 | module/user |
| Plan Approval | once-effect/destructive 动作是否获批 | user |
| Execution Policy | 网络、路由、操作和凭据 scope | platform |
| Outcome | 动作后是否出现独立结果证据 | module |
| Materialization | 必需 Artifact 是否存在且可读 | platform |
| Provider Spend | 调用预算、attempt 和外部任务状态 | provider/platform |
| Media Quality | 解码、时长、画面、音频和事实一致性 | module |
| Final Review | 当前 revision 的审核包是否被接受 | user |

## 8. 审计事件

必须记录：

- 模块准入、状态转换、Gate Decision；
- 敏感 Artifact 读取、发布、下载和删除；
- 授权创建、使用、过期、撤销；
- Provider admission、submit、resume、完成和消费额度；
- once-effect 动作开始、结果确认和不确定状态；
- 用户终审和 revision 请求。

不得记录：

- 每秒重复的定位候选、完整 DOM、完整模型 prompt 或响应；
- raw secret、请求 Authorization、Cookie 或 Storage；
- 大型媒体的 base64/二进制正文。

轮询型观察使用固定窗口聚合：保留首次、状态变化、末次和异常摘要；详细画面进入 D3 Artifact。

## 9. 监控和响应

平台至少暴露：运行状态数量、各 Gate 阻断数量、waiting 时长、恢复次数、once-effect 不确定数、Provider 费用与重试、事件拒绝数、Artifact 删除滞后和 secret redaction 命中。

发现 D4 泄漏或未授权 D3 外发时必须：阻断相关 run、撤销授权、销毁 Worker secret、冻结受影响 Artifact、记录 incident ID，并只向用户返回脱敏影响范围。
