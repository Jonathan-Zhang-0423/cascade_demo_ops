# 视频风格模板与成片制作草案 v1

## 目标

本方案让 Server 本地编辑器在不改写真实业务事实的前提下，把已录制的素材组织成可预览、可导出的成片。它复用既有唯一执行链：

```text
EditorSession
  -> AssetTimelineCatalog
  -> DemoEditPlan
  -> Validator
  -> FFmpeg Renderer
```

模板和参考视频分析只生成可审阅的建议，不能绕过 `DemoEditPlan`、校验器或最终的 FFmpeg 渲染器。

## 四种对象

| 对象 | Schema | 作用 | 是否可直接改变成片 |
| --- | --- | --- | --- |
| 风格模板 | `demoops.video_style_template.v1` | 平台预置的展示配方：画幅、节奏、字幕密度、源音量、镜头槽位和允许操作 | 否 |
| 风格画像 | `demoops.video_style_profile.v1` | 模板或参考视频分析得到的、可解释的制作参数 | 否 |
| 制作草案 | `demoops.video_style_draft.v1` | 在指定 `revision` 上生成的候选 `DemoEditPlan` 和输出参数 | 否，必须显式应用 |
| 正式编辑计划 | `demoops.demo_edit_plan.v1` | 唯一可进入校验、预览和最终导出的计划 | 是 |

## 当前内置模板

- `concise_product_demo`：简洁产品演示，16:9，适合先说明价值再演示操作。
- `fast_feature_demo`：快节奏功能亮点，16:9，适合短 Demo。
- `guided_product_tutorial`：步骤讲解教程，16:9，适合培训和交付说明。
- `vertical_product_short`：竖屏产品短视频，9:16，适合移动端传播。

模板使用语义槽位（开场、已验证的业务步骤、已验证的结果、收尾），而不是指向某个客户页面、URL 或按钮。这样模板可复用，但不会把客户业务逻辑写死到 Server。

## 草案与确认流程

```text
用户选择模板 + 输入制作提示 + 可选参考视频
  -> Server 创建风格画像和制作草案
  -> Worker 校验候选 DemoEditPlan
  -> 前端展示画幅、音频、警告和未实现效果
  -> 用户显式确认 apply
  -> 写入新的 EditorSession revision
  -> 生成 720p 预览或 1080p 最终导出
```

草案生成时必须携带当前 `expected_revision`。应用时再次校验版本号和计划；任一不一致均拒绝，防止旧草案覆盖新编辑。草案不会修改原始素材、`source_artifact_id`、`source_step_id`、源时间范围、必需业务步骤顺序或 App 侧的业务事实。

## 参考视频边界

参考视频必须通过专用路径登记为：

```text
kind=style_reference_video
asset_role=style_reference_only
include_in_demo=false
metadata.presentation_only=true
metadata.rights_confirmation_required=true
```

因此它不会自动加入时间线、不会成为 `DemoEditShot`，也不会进入最终成片。用户必须确认拥有使用权后才能请求风格分析。系统只允许提取并使用可解释的参数，例如画幅、节奏、镜头停留、字幕密度、颜色方向和建议音量；禁止复制参考视频的画面、人物、Logo、音乐、文案或具体镜头。

当前版本登记参考视频后，画像状态为 `pending_analysis`：仍先套用用户选择的平台模板，不能假装已经完成参考风格复刻。

## 真实渲染范围

当前确定性 Renderer 已实际处理：画幅、已实现的裁剪/拼接、字幕、音频和静态展示素材。模板中的颜色方向、节奏标签、转场风格会写入计划和草案，但复杂运镜、复杂动画和参考视频风格的全像素复现尚未被渲染为画面效果。前端必须显示该警告；未实现效果不能被宣传为已交付。

## 后续参考视频分析链

第一版不把原始 MP4 直接交给通用大模型。推荐可审计链路：

```text
参考视频
  -> FFmpeg / ffprobe：抽帧、音轨、画幅、帧率、时长
  -> PySceneDetect：镜头边界、镜头长度和节奏统计
  -> LAS：转写、词/句时间戳、置信度和字幕密度
  -> 豆包：基于抽帧、转写和客观统计输出结构化 StyleProfile
  -> 规则校验：范围、版权边界、Renderer 能力
  -> 制作草案：仍需用户确认后才写入 DemoEditPlan
```

豆包模型只输出结构化解释和建议，不直接剪辑 MP4、不直接生成 FFmpeg 命令，也不能改变业务步骤。`doubao-seed-2-0-mini-260428` 是否支持原始视频输入尚未在当前工程中确认，因此不作为第一版的直接视频解析前提。

## API

```text
GET  /v1/editor/style-templates
POST /v1/editor/sessions/{session_id}/style-references/assets
POST /v1/editor/sessions/{session_id}/style-references/uploads
POST /v1/editor/sessions/{session_id}/style-drafts
GET  /v1/editor/sessions/{session_id}/style-drafts/{draft_id}
POST /v1/editor/sessions/{session_id}/style-drafts/{draft_id}/apply
```

普通 `/assets` 与 `/uploads` 用于可能进入编辑时间线的素材；参考视频必须使用 `style-references` 专用接口，避免类型混淆。

## 成熟方案的借鉴边界

- Remotion：借鉴组件化、参数化预览思路；本项目使用 `PresentationComposition` 作为只读预览中间表示。
- Creatomate：借鉴模板槽位和变量替换思路；不调用其 SaaS 渲染服务，不将客户素材或业务证据交给外部编辑链路。
- PySceneDetect：借鉴镜头检测能力，作为后续风格统计组件。
- FFmpeg：继续作为唯一最终渲染器，确保本地、可复现和可审计。
