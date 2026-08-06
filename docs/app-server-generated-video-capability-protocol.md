# App ↔ Server 生成展示视频能力协议

> 协议状态：预发布、兼容性基线  
> 整理日期：2026-08-05  
> 当前影响：不修改 `browser-agent-outline-v1`，App 当前执行包无需增加字段

## 1. 协议边界

本协议只定义 App 如何表达“可选的非事实展示视频候选”以及 Server 必须返回的稳定语义。它是 Provider-neutral 公共协议，不包含或公开：

- 模型名称或模型 ID；
- Provider、API Endpoint、API Key；
- 厂商请求体、任务 ID、任务状态或有时效下载 URL；
- 厂商单价、成本预算、周期配额、并发上限或调用超时；
- Provider 内部幂等键；
- 某个 Provider 独有的素材 role、分辨率或任务生命周期。

具体 Provider 能力和参数只存在于 Server 内部协议。公共协议不能把某个 Provider 的理论能力声明为所有 Provider 的共同能力。

## 2. 当前兼容性结论

当前 App → Server 主路径仍使用 `browser-agent-outline-v1`。生成展示视频候选尚未成为该 schema 的必填或可执行字段：

- App 当前无需修改或重新生成执行包；
- 正在进行的 App/Server 验收不依赖生成候选；
- 生成候选失败不能改变正式录屏、截图、分镜或业务验收结果；
- 未发布独立 schema/version 前，App 不得把本协议示例塞入现有 required 结构；
- Server 未显式开放能力时必须视为不可用，不能自动选择 Provider 调用。

## 3. 未来逻辑意图

Server 发布独立 schema/version 后，App 可以提交以下逻辑对象。对象只表达展示目的，不指定实现模型：

```json
{
  "capability": "presentation_video_candidate",
  "purpose": "intro",
  "required": false,
  "reference_asset_refs": [
    "stage_01_viewport_screenshot"
  ],
  "requested_slot": {
    "preferred_duration_sec": 5,
    "aspect_ratio": "16:9"
  },
  "content_policy": {
    "presentation_only": true,
    "may_represent_business_step": false,
    "may_replace_captured_ui": false,
    "requires_explicit_review": true
  },
  "failure_policy": "continue_without_generated_candidate"
}
```

### 3.1 允许的 purpose

- `intro`
- `outro`
- `section_divider`
- `abstract_b_roll`
- `brand_atmosphere`
- `transition`

不得使用生成候选表达登录、表单提交、创建项目、构建成功、数据结果等业务事实。

### 3.2 公共硬约束

- `required` 必须为 `false`；
- `presentation_only` 必须为 `true`；
- `may_represent_business_step` 必须为 `false`；
- `may_replace_captured_ui` 必须为 `false`；
- `requires_explicit_review` 必须为 `true`；
- `failure_policy` 固定为 `continue_without_generated_candidate`；
- `reference_asset_refs` 只能引用 App 已授权、Server 可解析和重新验证的素材 ID；
- App 不提交本地文件路径、任意公网 URL、Base64 媒体或 Provider asset ID；
- App 不要求模型原生 FPS、编码、分辨率或厂商特有帧模式；
- 最终生成素材不能绑定业务 `source_step_id`，不能满足 required 业务步骤。

## 4. Capability Profile

App 只能依赖 Server 返回的公共能力交集或本次意图可用范围。Server 返回内容应采用逻辑字段，例如：

```json
{
  "capability": "presentation_video_candidate",
  "enabled": false,
  "schema_version": "not_published",
  "duration_sec": {
    "min": 4,
    "max": 15,
    "recommended": 5
  },
  "reference_assets": {
    "max_total": 4,
    "accepted_logical_kinds": [
      "image",
      "video"
    ]
  },
  "requires_explicit_review": true,
  "failure_policy": "continue_without_generated_candidate"
}
```

当前 `enabled=false`。示例边界是 Server 公共候选意图的保守上限，不表示任意 Provider 都支持其中所有组合。Server 在选择 Provider 后必须再按对应内部 Capability Profile 校验；没有合法 Provider 时不得调用模型。

## 5. Server 职责

Server 必须：

1. 将 App 素材引用解析为已授权的真实 artifact；
2. 探测真实文件，不能信任 App 声明的格式、大小、时长、尺寸或 FPS；
3. 先执行公共意图校验，再执行 Provider 专属能力校验；
4. 只选择完全支持当前意图的 Provider，不得静默删素材、补素材、改比例或改变模式；
5. 在 Provider 调用前执行成本、配额、并发、超时和幂等治理；
6. 将 Provider 输出保存并规范化为编辑器允许的媒体格式；
7. 在显式内容审核和用户选择前保持 `include_in_demo=false`；
8. 确保生成失败不阻塞真实素材交付。

## 6. 稳定错误语义

未来能力接口应沿用项目统一错误结构，并至少区分：

- `generated_candidate_capability_disabled`：公共能力尚未开放；
- `generated_candidate_intent_invalid`：公共逻辑意图不合法；
- `generated_candidate_no_compatible_provider`：没有 Provider 能合法实现当前意图；
- `generated_candidate_request_invalid`：选定 Provider 的输入超出当前项目 Profile；
- `generated_candidate_unavailable`：Provider、下载或媒体规范化失败；
- `generated_candidate_review_required`：候选已规范化但尚未显式审核或选择。

上述错误都不能把已经成功的 Browser Agent 录制或确定性成片改为失败。

## 7. 内部协议分层

公共协议不得承载 Provider 参数。Server 内部使用独立文档：

- Seedance 2.0：[seedance-2-0-video-generation.md](seedance-2-0-video-generation.md)
- MiniMax-H3：[minimax-h3-video-generation.md](minimax-h3-video-generation.md)
- 多 Provider 编排：[server-browser-agent-execution-editor-architecture-v2.md](server-browser-agent-execution-editor-architecture-v2.md#阶段四受约束分镜设计与多-provider-分镜头实现)

任何内部 Profile 扩大都不能自动扩大 App 公共协议。公共能力变化必须单独升版并提供兼容测试。

