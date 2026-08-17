# MiniMax H3 最终成片 Harness

## 结论

MiniMax H3 不应直接把整条产品演示视频“重生成一遍”。产品 UI、文字、数字和操作结果属于事实轨，必须继续来自 Browser Agent 真实录屏/截图和确定性 Renderer。H3 最适合生成展示轨片头、片尾、章节转场和抽象 B-roll；经审核后再与事实轨做确定性合成。

推荐链路：

```text
DirectorEditSuggestion / DemoEditPlan
  -> 提取 presentation-only GeneratedShotIntent
  -> H3-Context-IR（可选，增强但不改变意图）
  -> MiniMax-H3 异步生成 4-15 秒候选
  -> 有界轮询 / 可恢复 task_id
  -> 立即下载供应商时效 URL
  -> ffprobe(original)
  -> FFmpeg: MP4/H.264/yuv420p/1920x1080/CFR30
  -> ffprobe(normalized) + SHA-256
  -> 确定性结构审核
  -> 人工内容审核与显式选择
  -> Editor approval + CandidateAssetEditPlanPatch
  -> 现有 Renderer 合成最终成片
```

## 为什么这样做

- H3 能统一理解文本、图片、视频和音频，支持原生立体声、4-15 秒、768P/2K 和多模态参考，适合单个高质量展示镜头。
- H3 输出属于生成内容，不能证明真实产品状态；整片 video-to-video 会带来 UI 文字变形、数字漂移、操作语义变化和不可审计剪辑。
- 最终时间线、字幕、真实 UI 镜头顺序和业务事实必须由 Server/Renderer 控制，视频 Provider 只返回候选素材。

官方资料：

- [MiniMax H3 发布与能力说明](https://www.minimax.io/blog/minimax-h3)
- [H3 视频生成指南](https://platform.minimax.io/docs/guides/video-generation)
- [创建 H3 视频任务](https://platform.minimax.io/docs/api-reference/video-generation-v2-create)
- [创建 H3-Context-IR 任务](https://platform.minimax.io/docs/api-reference/video-generation-v2-h3-context-ir)
- [查询 H3 任务](https://platform.minimax.io/docs/api-reference/video-generation-v2-query)

## 当前实现

核心实现：

- `backend/internal/media/minimax_h3_client.go`：H3 V2、Context-IR、任务查询和区域 Base URL 处理。
- `backend/internal/media/minimax_h3_harness.go`：生成、恢复、下载、规范化、候选转换和结构审核。
- `backend/internal/media/minimax_h3_harness_config.go`：显式视频路由下的安全凭据选择。
- `backend/cmd/minimaxh3harness/main.go`：operator 真实调用入口。

运行：

```powershell
pnpm harness:minimax-h3 -- -intent-id intro_001 -purpose intro -duration 4 -resolution 768P
```

恢复已创建任务，不产生第二次生成调用：

```powershell
pnpm harness:minimax-h3 -- -task-id <task_id> -intent-id intro_001 -context-ir=false
```

必要配置：

```dotenv
CASCADE_VIDEO_PROVIDER=minimax
CASCADE_VIDEO_MODEL=minimax-h3
MINIMAX_API_KEY=...
MINIMAX_BASE_URL=https://api.minimaxi.com/v1
```

也可使用隔离的 `MINIMAX_H3_API_KEY`、`MINIMAX_H3_BASE_URL`。Harness 会保留配置中的区域主机，并在拼接 H3 `/v2` 路径前剥离单独的 `/v1` 或 `/v2` 后缀。

## App 接入与真实验收状态

正式 FinalFilm workflow 已完成以下接线：

1. Director 受限输出由 Server 编译并持久化为 `GeneratedShotIntent`，模型不能选择 Provider、改写事实轨或指定业务步骤。
2. H3 Adapter 只有在作业完成 baseline、Director 计划落库、用户显式授权且 admission/幂等/费用配置全部通过后才可调用 Provider。
3. normalized candidate 必须依次经过人工内容审核、人工选择、独立 Editor approval 和显式补丁应用；任何步骤都不能自动跳过。
4. Renderer 只做确定性 trim/concat/normalize；最终输出再次验证 SHA-256、ffprobe 媒体 Profile、FFmpeg 无 fallback、无 skipped operation 和需求满足报告。
5. H3 不可用、生成失败、审核拒绝或最终合成验收失败时，均回退已经完成的事实轨 baseline。

`backend/internal/finalfilm/real_h3_e2e_test.go` 提供环境门控的真实素材 E2E。它读取已经持久化的 `harness_result.json`，不会创建第二个 Provider 任务；只有 operator 完成人工逐帧审核并显式设置 `CASCADE_REAL_H3_E2E_APPROVED=true` 后，才会把该真实候选回放进完整 FinalFilm 状态机，并通过真实 Node Worker、FFmpeg 和 ffprobe 生成最终视频。普通单元测试和 CI 默认跳过该用例，因而不会产生费用。

仍需继续建设的是生产级分布式 admission/幂等/usage 对账，以及黑帧、冻结帧、响度和 OCR 等更深层质量门禁；这些不影响当前“真实 H3 候选 + 人工审核 + 确定性最终合成”的闭环可执行性。
