# 开发反馈

## 目的与边界

本文件汇总当前 Server 对 App 的新增对接要求，以及正式 App 产包已发现的问题。目标是让 App 以现有正式流程生成不可变 `browser-agent-outline-v1` 执行包，并在不传递密钥、密码、Cookie、Token 或本地绝对路径的前提下，使 Server 能够执行、录制、编排并交付视频。

本文件不要求修改 Validation Agent 规则，也不允许 App 为通过 Server 验收而改写已经批准的执行包。

## 一、必须新增的媒体交付策略对接

### 1. Server 接口

App 在任务确认页面调用：

```text
GET /v1/desktop/projects/{project_id}/media-delivery-policy
PUT /v1/desktop/projects/{project_id}/media-delivery-policy
```

`GET` 返回 Server 当前有效的默认策略，包括：

- 默认展示候选 Provider：`seedance`；
- 候选采用范围：仅已通过门禁的非事实展示镜头；业务步骤不能被生成素材替代；
- 默认旁白模式：`auto_by_product_style`；
- 最终交付：同一 `DemoEditPlan` 的 `2560x1440` Master 与 `1920x1080` 分发版；
- TOS 默认策略：`standard_30d`、`all_task_artifacts`；
- `client_feedback_required` 及需要确认的字段。

App 必须展示以下提示，并允许用户确认或调整：

```text
本任务的原始证据、派生素材、模型候选、最终视频和审计信息默认保存 30 天。
```

用户确认默认策略时，调用 `PUT`：

```json
{
  "tos_retention": {
    "mode": "standard_30d",
    "retention_days": 30,
    "scope": "all_task_artifacts",
    "client_disclosure_acknowledged": true
  }
}
```

用户选择其他时长时：

```json
{
  "tos_retention": {
    "mode": "custom",
    "retention_days": 7,
    "scope": "all_task_artifacts",
    "client_disclosure_acknowledged": true
  }
}
```

自定义保留期允许 `1-365` 天。`standard_30d` 必须为 30 天。客户端不得自行修改 Provider、模型版本、输出规格或候选采用边界；这些由 Server 返回并管理。

### 2. 正式包透传

App 必须在正式执行包的 `project_context_summary` 中透传 Server 已确认的：

```text
media_delivery_preferences
```

该字段采用 `demoops.media_delivery_preferences.v1`，至少包含：

```json
{
  "schema_version": "demoops.media_delivery_preferences.v1",
  "narration": {
    "mode": "auto_by_product_style",
    "client_specified": false
  },
  "tos_retention": {
    "mode": "standard_30d",
    "retention_days": 30,
    "scope": "all_task_artifacts",
    "client_disclosure_acknowledged": true
  },
  "output_profiles": [
    {"id": "final_master_2k", "width": 2560, "height": 1440, "format": "mp4_h264_yuv420p_cfr30"},
    {"id": "final_delivery_1080p", "width": 1920, "height": 1080, "format": "mp4_h264_yuv420p_cfr30"}
  ],
  "default_candidate_provider": "seedance",
  "candidate_adoption_policy": "qualified_presentation_auto"
}
```

如果用户明确选择无旁白或定制旁白，`narration.mode` 可以改为 `disabled` 或 `custom`，并设置 `client_specified=true`。用户未明确选择时，不要把旁白需求错误写入网页 `must_show`；应保持 `auto_by_product_style`。

### 3. 安全限制

App 不得在此接口或正式包中发送：

- API Key、AccessKey、SecretKey、密码、Cookie、Token；
- TOS 对象的长期公开 URL、预签名 URL；
- 本地绝对路径、完整源码、浏览器 HTML；
- Provider endpoint、模型私有参数或账单凭据。

用户选择、保留期、输出偏好和旁白模式属于可传递的业务元数据；TOS 实际路径、签名 URL、模型调用、预算计算和凭据均由 Server 管理。

## 二、正式 App 产包必须修复的问题

### 1. 展示要求不能作为网页交互需求

字幕、旁白、镜头样式、时长和画幅属于 `presentation/output constraint`。它们不能进入网页 `must_show` 后要求绑定 DOM selector 或 `node_refs`，否则会错误阻断执行包。

建议：为这类要求建立独立的 `presentation_requirements` 或等价结构，并让 App 置信度算法以“业务 Stage 覆盖 + 编辑计划验收”判断，不以页面控件证据判断。

### 2. 业务步骤必须独立建模

以下动作不得合并到登录或项目命名 Stage：

1. 点击构建；
2. 填写详细需求弹窗；
3. 观察构建进度；
4. 观察并验证构建完成。

每个动作需有语义匹配的 Stage、允许的 selector/evidence，或者明确标记 `runtime_discovery_required` 并给出受限修复边界。特别是“构建完成”必须绑定可观测条件，例如结果页、预览、完成状态或稳定结果标识。

### 3. 动态页面证据不足

App 预扫描需要以受限状态机覆盖：

```text
登录后 -> 工作台 -> 新建项目 -> 输入页或弹窗 -> 提交 -> 进度 -> 结果
```

每次状态转移只执行已经批准的非破坏性操作，并记录 URL、role/name、`data-testid`、截图摘要、时间与业务语义。Server 可以在受限边界内处理运行时漂移，但不会猜测未授权的业务动作或补写 App 包。

### 4. 本地源码与运行页面绑定

本地测试时，App 应记录受控启动进程、端口、工作目录摘要和构建版本摘要的哈希，以形成 `local_runtime_binding`。无法证明时必须维持 `page_only`，不能将代码读取结果自动声明为实际页面来源。

## 三、客户端交互顺序

```text
用户输入业务目标与视频要求
-> App 区分网页业务步骤与展示输出约束
-> App 获取 Server media-delivery-policy
-> 客户端展示 TOS 30 天默认留存与可选项
-> 用户确认或调整
-> App 保存偏好并构建不可变正式包
-> App 预检 readiness=ready
-> App 上传原包
-> Server 执行真实页面、生成证据、模型候选与双规格视频
```

预算确认接口、TOS 生命周期执行器、TTS 真实调用和模型候选自动写入时间线仍由 Server 后续接入。当前媒体偏好接口已经可用，用于让 App 完成展示、确认和正式包透传。

## 四、验收标准

1. `GET media-delivery-policy` 能返回 30 天默认留存、两种输出规格、默认 Seedance 和自动风格旁白。
2. App 展示留存提示后，`PUT media-delivery-policy` 能保存确认或合法自定义天数。
3. 重新生成的正式包完整携带 `media_delivery_preferences`，且不含任何密钥或本地绝对路径。
4. 字幕、旁白和输出规格不再造成页面需求的 `node_refs` 缺失。
5. 每项业务动作都有独立 Stage 与证据，或有明确的受限运行时发现标记。
6. `confidence_summary.requirement_coverage = 1` 且 `confidence_summary.readiness = ready` 后，才允许提交 Server 进行正式端到端验收。
