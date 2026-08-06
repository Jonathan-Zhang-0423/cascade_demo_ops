# Seedance 2.0 视频生成内部协议

> 文档状态：Server 内部 Provider 协议  
> 整理日期：2026-08-05  
> 对 App 可见性：不可见；App 不传模型 ID 或厂商参数

## 1. 定位

本文只定义 Seedance 2.0 的 Server 内部能力和请求编译边界。不得将本文中的模型 ID、参数、素材 role 或任务字段写入 App 公共协议。

Server 当前固定模型 ID：

```text
doubao-seedance-2-0-260128
```

模型 ID 由 Server 配置管理，App 只表达 `presentation_video_candidate` 逻辑意图。

## 2. 当前运行路径

现有 Director 路径的已验证请求参数为：

```text
resolution=1080p
ratio=16:9
duration=5
generate_audio=false
return_last_frame=true
watermark=false
reference_count<=4
```

该路径已经存在并可能参与另一窗口的验收。本轮新增的 `Seedance20GeneratedShotCompiler` 只是未注册路由的 dry-run 旁路编译器，不替换、不调用也不修改现有 `seedanceProviderRequest`。

## 3. 当前项目 Capability Profile

| 项目 | 当前项目边界 |
| --- | --- |
| Profile | `demoops.seedance_2_0_generated_shot.v1` |
| 输出时长 | 4～15 秒，推荐 5 秒 |
| 请求分辨率 | 1080p |
| 请求比例 | 当前旁路 Profile 仅开放 16:9 |
| 通用参考素材总数 | 最多 4 |
| 参考图片 | 最多 4，稳定格式 PNG/JPEG |
| 参考视频 | 最多 3，稳定格式 MP4/MOV |
| 参考音频 | 当前不开放 |
| 首帧模式 | 当前旁路 Profile 不开放 |
| 首尾帧模式 | 当前旁路 Profile 不开放 |
| 仅尾帧模式 | 当前不开放 |
| 生成音频 | 当前关闭 |
| 厂商原生输出 | MP4，技术资料标注 24fps |
| 编辑器输入 | 必须先规范化为 MP4/H.264/yuv420p/1920x1080/CFR30 |

这是当前 Server 已验证能力，不等于厂商理论最大能力。厂商资料中出现的其他比例、4K、音频、帧角色或更多参考素材，不经独立测试、Profile 升版和调用前门禁不得开放。

## 4. Dry-run 编译规则

公共 `GeneratedShotIntent` 编译为 Seedance 请求时：

- `model` 由 Server 固定为 `doubao-seedance-2-0-260128`；
- `resolution` 固定为 `1080p`；
- `ratio` 当前固定兼容 `16:9`；
- `duration` 使用已校验的 4～15 秒整数；
- PNG/JPEG 编译为 `image_url`；
- MP4/MOV 编译为 `video_url`；
- 当前通用参考模式不写入 H3 风格的 `reference_image` / `reference_video` role；
- `generate_audio=false`、`watermark=false`；
- 编译结果必须带 `dry_run_only=true`，不能直接调用 Ark Client。

## 5. 与 H3 的明确区别

- Seedance 请求发送到 Ark `/contents/generations/tasks`，H3 使用 MiniMax V2 接口；
- Seedance 当前项目输出为 1080p，H3 当前请求固定 2K；
- Seedance 当前旁路 Profile 只开放通用参考模式，H3 当前 Profile 独立开放首帧和首尾帧模式；
- Seedance 技术资料标注原生 24fps，H3 公共资料不提供可配置 FPS；
- 两者的请求体和原始响应不能互换；
- 两者只允许在 Server 的 normalized candidate 层汇合。

禁止将一个 Provider 的请求校验结果复用于另一个 Provider，也禁止将一方输出交给另一方二次生成。

## 6. 安全边界

Seedance 只生成非权威展示素材：

- `non_authoritative=true`；
- `presentation_only=true`；
- `include_in_demo=false`，直到显式审核和选择；
- 不绑定业务 `source_step_id`；
- 不生成或替换产品 UI、按钮、文字、数字、表格、状态或业务结果；
- 生成失败时继续使用真实录屏和确定性编辑计划交付。

## 7. 当前实现位置与状态

- 现有运行请求：`backend/internal/executor/director_adapter.go`
- Ark Client：`backend/internal/media/ark_client.go`
- 未注册 dry-run Profile：`backend/internal/media/generated_shot_profiles.go`
- 详细厂商参数基线：[ark-model-parameters.md](ark-model-parameters.md)
- Ark 运行准备：[ark-media-integration.md](ark-media-integration.md)

当前 dry-run Profile 不接 App、Director、Executor、comparison 或 fallback 路由，不会改变现有验收请求。

Seedance 与 H3 未来规范化产物的共同字段见 [Server 生成视频统一候选产物内部协议](generated-video-candidate-internal-protocol.md)。当前 Seedance 运行链路尚未切换到该协议；在补齐独立的下载、双探测和规范化证据前，不得直接构造统一候选对象。
