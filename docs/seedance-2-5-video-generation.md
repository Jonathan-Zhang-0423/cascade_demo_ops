# Seedance 2.5 FinalFilm 接入说明

## 官方接口

本仓库按最新方舟示例调用：

```http
POST https://ark.cn-beijing.volces.com/api/v3/contents/generations/tasks
Authorization: Bearer ${SEEDANCE_API_KEY}
Content-Type: application/json
```

模型固定为 `doubao-seedance-2-5-260628`。请求内容使用 `content[]`：文本、`reference_image` 图片和 `reference_video` 视频。官方示例也支持 `reference_audio` 和 `generate_audio=true`，但产品第一版为避免未经审核的旁白、音乐或响度问题，保持 `generate_audio=false` 且不接受音频引用。

## 产品内调用链

1. Director 只产出受控展示意图，不产出任意 Ark JSON。
2. `Seedance25GeneratedShotCompiler` 校验用途、时长、比例和已发布的 HTTPS 引用。
3. 编译器固定模型、role、音频和水印策略。
4. `Seedance25ProviderAdapter` 在检查持久化生成授权后调用 Ark client。
5. Adapter 轮询任务，下载原始视频并计算 SHA-256。
6. FFmpeg 规范化为 Editor 统一规格。
7. 候选经过结构审核、人工内容审核、人工选择和 Editor 批准后才能显式写入隔离时间线。
8. 最终 FFmpeg 合成或验收失败时交付事实轨 baseline。

## 环境配置

```dotenv
SEEDANCE_API_KEY=
SEEDANCE_BASE_URL=https://ark.cn-beijing.volces.com/api/v3
SEEDANCE_MODEL=doubao-seedance-2-5-260628
CASCADE_ARK_MEDIA_MODE=real
CASCADE_SEEDANCE_FINAL_FILM_ENABLED=true
```

H3 可同时作为默认视频路由：

```dotenv
CASCADE_VIDEO_PROVIDER=minimax-h3
CASCADE_VIDEO_MODEL=minimax-h3
```

Seedance FinalFilm 注册不依赖 `CASCADE_VIDEO_PROVIDER=seedance`，避免 H3/Seedance 互斥。API key 与 feature flag 也不构成作业授权；真实生成仍需 UI 中的显式批准记录。

## 当前安全 Profile

- 时长：4–15 秒
- 比例：16:9
- 引用：最多 4 个，其中图片最多 4、视频最多 3
- 引用来源：Provider 可访问的 HTTPS URL
- 音频：关闭
- 水印：关闭
- 输出：必须再经 FFmpeg 规范化和人工审核
- 失败策略：不中断事实轨交付

Seedance 2.0 代码仅保留历史作业读取兼容，不会出现在新建 FinalFilm 作业的 Provider 列表中。

## 可重复真实 harness

```powershell
cd backend
go run ./cmd/seedance25harness `
  -authorize-real-call `
  -duration 4 `
  -output ../artifacts/seedance-2.5-harness/latest
```

命令会创建一个真实计费任务；没有 `-authorize-real-call` 时立即拒绝。若 Provider 已成功但本地下载/FFmpeg 阶段中断，可用任务 ID 恢复而不重复创建：

```powershell
go run ./cmd/seedance25harness `
  -authorize-real-call `
  -task-id <existing-task-id> `
  -output ../artifacts/seedance-2.5-harness/latest
```

审计文件只包含模型、任务 ID、状态、非敏感错误分类和媒体摘要，不包含 API key 或 Authorization header。
