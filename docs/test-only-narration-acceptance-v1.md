# Server 测试专用配音验收约定

## 目的

本约定只用于验证 Server 视频编辑器的旁白导入、时间轴对齐、原声压低、混音、双规格输出和媒体质量门禁。它不是产品默认行为，不修改 App 协议，也不改变正式生产成片规则。

## 启用方式

只有一次性验收运行显式设置以下开关时才启用：

```powershell
$env:CASCADE_ACCEPTANCE_REQUIRE_NARRATION="1"
```

测试进程退出后该变量失效。不得把它写入 `.env.example`、服务启动脚本、正式部署配置或 App 执行包。

## 测试输入与处理

1. Server 使用 Agent Plan 专属 TTS URL 生成短旁白候选。
2. 候选必须完成 Base64 聚合、FFprobe 校验和 SHA-256 审计。
3. 测试运行将候选明确登记为本轮 `tts_confirmed` 测试素材，仅供本轮编辑器验收；不修改原始 App 包，也不写入长期项目编辑计划。
4. 编辑器按旁白时间轴混音，可同时验证原声压低、音量、字幕时间轴和镜头切换。
5. 2K 与 1080p 两个输出都必须包含有效音频流，并在 manifest 中记录旁白素材哈希、模型、采样率、声道和采用状态。

## 通过条件

- `real_call_made=true`、`provider_status=succeeded`；
- `download_verified=true`、`ffprobe_verified=true`；
- 旁白素材来源为本轮测试确认素材，不是未审核候选；
- 两个规格均存在音频流，音频时长覆盖声明的输出时间范围；
- 编辑器输出、审计记录和双规格 checksum 全部通过。

## 禁止事项

- 未设置测试开关时，不能因为本约定阻塞无旁白视频；
- 不能把测试旁白状态复制到正式 App 包或长期 `DemoEditPlan`；
- 不能用 fixture、mock 或 waiver 宣称正式 App-to-Server 联调成功；
- TTS 成功不代表业务步骤、浏览器证据或最终视频验收成功。
