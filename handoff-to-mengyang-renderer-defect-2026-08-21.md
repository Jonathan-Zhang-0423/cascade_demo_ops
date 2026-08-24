# 渲染器 Still 合成缺陷 Handoff（给孟洋）

**日期**: 2026-08-21
**来源**: 明达端到端验收时发现，质量门禁正确拦截
**归属**: 主线（视频编辑器/渲染器，交接文档明确保留项）
**优先级**: 阻塞——所有依赖 still 合成的 demo 视频无法产出

---

## 一句话结论

Still-shot 合成路径产出的视频时长与编辑计划严重不符：计划 8 秒，实际渲染仅 **0.981 秒**。视频质量门禁正确拒绝交付。

---

## 现象

端到端验收（controlled outline 场景 + protocol acceptance）在浏览器执行、三阶段验证、证据收集、结果打包全部通过后，在最终渲染阶段被质量门禁拦下：

```
browser_agent_render_failed: final_video_quality_gate: rendered_duration_mismatch
```

涉及的全部场景（3 个成功路径子场景 + 协议验收全链路）都在同一处失败，说明是 still 合成路径的系统性缺陷，不是个别场景问题。

---

## 证据数据（实测）

以 `controlled outline success` 场景为例（2 个 stage，录制目标时长 8s）：

**编辑计划**（`demo_edit_plan.json`）：
```json
{
  "target_duration_ms": 8000,
  "shots": [
    {"id": "shot_001_node_open_workspace", "presentation_kind": "still", "output_duration_ms": 4000},
    {"id": "shot_002_node_inspect_status", "presentation_kind": "still", "output_duration_ms": 4000}
  ]
}
```

**实际渲染输出**（`media_normalization_report.json`）：
```json
{
  "output": {
    "format": "mov,mp4,...",
    "duration_sec": 0.981,
    "video_codec": "h264",
    "width": 2560, "height": 1440, "fps": 30
  }
}
```

**需求满足报告**（`requirement_satisfaction_report.json`）正确识别：
```json
{
  "errors": [{
    "code": "rendered_duration_mismatch",
    "message": "Rendered video duration is 0.981s, but the edit plan contributes 8.000s (tolerance 0.400s)."
  }]
}
```

**渲染清单**（`render_manifest.json`）：
```json
{
  "compositor": {
    "status": "rendered",
    "quality_status": "degraded",
    "method": "ffmpeg_trim_concat",
    "source_artifact_id": "artifact_..._node_open_workspace_after"
  }
}
```

注意 `quality_status: "degraded"`——compositor 自己知道输出不达标，但仍然产出了 0.981s 的视频。

---

## 根因分析

Still-shot 合成路径（`video-worker/src/renderer.ts` 的 `composeFinalVideo` → still 分支）对每张静态截图应按 `output_duration_ms`（4s）停留，但实际输出时长（0.981s）说明**停留时长没有生效**——两张 4s 的 still 合计应该 8s，实际只有约 1s，接近 ffmpeg 默认的单帧/极短时长行为。

可疑点（供你排查参考）：
- still 分支的 ffmpeg concat 命令是否把 `-t` / `duration` / `loop` 参数正确传给了图片输入
- `ffmpeg_trim_concat` 方法在 still 模式下是否走了和视频片段相同的拼接逻辑（视频片段用 `source_time_range_ms`，still 用 `output_duration_ms`，两者路径可能不一致）
- 之前成功产出过 still 视频（test_waiver 的 30s demo），当时工作正常——8 月中旬的渲染器改动可能引入了回归

---

## 复现步骤

```bash
cd cascade_demo_ops/backend
go test ./internal/app -run "TestControlledOutlineScenarioPackagesRunCompleteServerPath/success" -v
```

或完整验收：

```bash
go test ./internal/app -run "TestControlledOutlineScenarioPackagesRunCompleteServerPath|TestProtocolBrowserAgentAcceptanceRunsCompleteServerPath" -v
```

所有成功路径场景都会在渲染阶段以 `rendered_duration_mismatch` 失败。

---

## 影响范围

- **所有 still 合成 demo 视频**：只要场景没有 raw_recording（或 recording 未被选为 source），最终视频时长都会被截断
- **协议验收测试**：`TestProtocolBrowserAgentAcceptanceRunsCompleteServerPath` 无法通过（被此缺陷阻塞）
- **Controlled outline e2e**：3 个成功/修复子场景无法通过
- **不影响失败场景**：3 个失败路径子场景（locator_missing / required_validation_failure / wait_timeout）正常工作并返回预期稳定码

---

## 建议修复方向（供参考，未越权修改）

`composeFinalVideo` 的 still 分支需要确保：
1. 每张图片输入用 `-loop 1 -t <output_duration_sec>` 或 concat demuxer 的 `duration` 指令按 `output_duration_ms` 停留
2. 拼接后的总时长 = Σ 各 shot 的 output_duration_ms
3. 修复后 `rendered_duration` 应与 `edit_plan.target_duration_ms` 在 5% 容差内一致

修好后我这边可以直接重跑端到端验收确认闭环。

---

## 附带：两个既有的主线 harness 失败（非本次引入，供参考）

1. `TestNewSeedanceGeneratedShotCandidateRequiresExplicitReview`（executor）：seedance normalized artifact 需要绝对本地路径，纯净拉取状态下就失败
2. 之前还遇到过 `TestDirectServerControlledFixtureUploadOnly` 相关的 `formalAppScreenshotInputs` 悬空引用（已在 main 修复）

---

## 我这边已修的三个细节（供你了解）

1. **会话入口路由**：worker 对初始导航加了路由校验后，场景包的 ProductURL 声明（根路径）会被拦。已把 Server-owned 场景 fixture 的产品入口声明为首个已批准工作区路由（不改 runner 语义、不改 allowed_routes 权限边界）
2. **Selector repair fixture 绑定**：新校验要求 action selector 绑定组件证据候选。已给 stale primary selector 补了正式 provenance（与生产 App 包描述 selector 漂移的方式一致）
3. **Approval digest 刷新**：场景 fixture 改造后 approval subject digest 会失配，已让协议验收路径在改造后刷新（与生产审批路径一致）

这三个修复都在 Server 侧验收 fixture 与 Direct 协议层，没有触碰 Browser Agent 运行时、渲染器或产包规则。
