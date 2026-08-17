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

## 生产接入剩余项

Operator harness 已验证真实生成闭环，但正式 App 自动化还需要：

1. 将 Director 输出中的展示镜头意图持久化为 `GeneratedShotIntent`。
2. 使用持久化/分布式 admission、幂等键、配额和成本对账替代 operator 显式授权。
3. 接入内容审核 UI、候选选择和 Editor approval。
4. 只把审核通过的 normalized artifact 编译成候选补丁，再由现有 Renderer 生成最终成片。
5. H3 失败始终降级到不含生成候选的确定性成片，不影响真实录屏交付。
