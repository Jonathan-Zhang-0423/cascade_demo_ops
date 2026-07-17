# 执行脚本文档协议 v1

> [!IMPORTANT]
> **Legacy v1 / 当前实现兼容基线。** 本文件用于兼容现有代码、fixtures 和迁移验证，不代表 Server 侧新功能路线。Server 侧 v2 架构以 [server-browser-agent-execution-editor-architecture-v2.md](./server-browser-agent-execution-editor-architecture-v2.md) 为准；迁移完成前，已落地数据结构仍按本文校验。

`ExecutionScriptDocument` 是 App 本地理解后、人工审批前的核心封装产物。它不是自由文本脚本，而是由 `DemoWorkflowGraph` 严格派生的可执行 JSON 规范；Markdown 只用于中文审批预览。

## 产物边界

```text
需求/代码摘要/页面截图
  -> MultimodalUnderstandingReport
  -> ProductMap
  -> DemoWorkflowGraph
  -> ExecutionScriptDocument JSON
  -> Markdown 审批预览
```

- JSON 是唯一机器源，云端录制和执行包只能消费 JSON。
- Markdown 只能由 JSON 渲染生成，不反向解析。
- 本阶段不上传完整源码，不保存 raw secret，不做真实录制和剪辑。

## JSON 必填结构

```text
schema_version = demoops.execution_script_document.v1
workflow_graph_id
graph_version
recording_run_spec
steps[]
safety_policy
reproducibility
approval_checklist
```

每个 `steps[]` 必须绑定一个唯一 `node_id`，且顺序必须等于 graph 的可执行拓扑顺序。每步必须包含：

- 页面/目标：URL、selector 或 selector alternatives。
- 操作：`navigate/click/fill/select/upload/wait/assert/inspect/api_call` 之一。
- 预期：expected outcome、validation specs、是否阻塞。
- 录制：截图、录屏、zoom、callout、mask selectors、duration hint。
- 叙事：中文标题、旁白、字幕、业务价值说明。
- 证据：需求、代码、页面 evidence refs 和 confidence。

## 安全规则

- 禁止出现 raw password、token、private key、API key。
- 凭据只能出现为 `secret_ref`、`input_ref`、credential grant ref。
- 代码理解只允许输出结构摘要、hash、selector、路由、组件名、数据模型名；不输出完整源码或原始文件路径。
- `forbidden_pages`、`forbidden_data`、`redactions` 必须同时进入脚本文档和审批清单。
- `RecordingRunSpec.allowed_domains` 是云端执行的硬边界。

## Markdown 预览结构

Markdown 固定包含：

```text
# 标题
## 摘要
## 安全策略
## 执行步骤
## 审批清单
```

审批清单至少包含：

- 上传前必须完成人工审批。
- 需要确认仅上传代码结构摘要，不上传完整源码。
- 需要复核打码选择器和禁止访问数据。
- 如果存在凭据，必须复核授权范围和过期时间。
- 云端录制前需要确认 Cascade 执行 IP 白名单。

## 最小示例

```json
{
  "schema_version": "demoops.execution_script_document.v1",
  "workflow_graph_id": "graph_1",
  "recording_run_spec": {
    "base_url": "https://app.example.com",
    "allowed_domains": ["app.example.com"],
    "locale": "zh-CN"
  },
  "steps": [
    {
      "node_id": "start",
      "action": { "type": "navigate" },
      "expected_outcome": "产品入口页面加载完成"
    }
  ]
}
```
