# 企业宣传片级视频链路开发计划 v1

状态：已讨论确认，作为后续开发计划；本次只记录方案，不执行代码修改。

目标：在保留真实业务执行事实的前提下，让 H3/Seedance 和现有视频编辑器具备企业宣传片级别的创作能力。设计原则是高密度、低耦合：事实链只执行一次，创作链只消费稳定 artifact，不复制事实验证和生命周期。

## 一、目标结构

```text
现有 Browser Agent / OutcomeVerifier
        -> CreativeSourceBundle
        -> Creative Director
        -> CreativeShotIntent
        -> H3 / Seedance Provider Pool
        -> 一凯现有 FinalFilm / Review / State Machine
        -> Editor Compiler / Renderer
        -> 2K Master + 1080p Delivery + Review Package
```

- 保留 `browser-agent-outline-v1`、Browser Agent、OutcomeVerifier、RecordingResultPackage 和真实录屏链路。
- 真实业务执行只完成一次；执行完成后生成轻量 `CreativeSourceBundle`。
- 不新增独立 Fact Ledger 服务，不重复执行事实验证，不修改一凯负责的 FinalFilm 状态机。
- 模块之间通过 artifact 引用和 hash 通信，不共享内部状态对象。

## 二、CreativeSourceBundle

`CreativeSourceBundle` 是事实链与创作链之间唯一交接 artifact，包含：

- 必要业务事实及其来源 artifact；
- 可使用的真实录屏、截图和音频引用；
- source digest、时间范围和素材角色；
- 不可修改的事实、品牌和敏感内容；
- 允许的创作空间和可降级策略。

下游信任该 bundle，不重新运行 Browser Agent；创作链只能引用，不能修改。

## 三、纳入一凯设计的边界

通过 adapter 纳入一凯已经完成的 FinalFilm 能力：

- Director Plan；
- GeneratedShotIntent；
- Provider attempt 和任务恢复；
- H3/Seedance Harness；
- 候选质量报告、Review Package 和 Renderer 交接。

适配层负责把 `CreativeSourceBundle` 转成现有 FinalFilm 输入，并把结果投影为简单的 planning/generating/reviewing/rendering/completed 状态。不得修改一凯的 Store、revision、Job State Machine 或最终终审逻辑。

一凯的 Adaptive Business Harness 和 Temporal Visual Observer 作为 Browser Agent 的可选增强能力保留，先不作为创作链必经步骤，也不建立第二套业务生命周期。

## 四、H3 / Seedance 互补 Provider Pool

新增内部 Provider Capability Router。每个 `CreativeShotIntent` 声明镜头角色、输入约束、允许的 Provider 和 fallback policy。

- H3：片头、片尾、Hero Shot、连续空间、品牌氛围和复杂多模态参考。
- Seedance：章节转场、短 B-roll、局部视觉补片和短时长参考生成。
- Provider 能力校验或任务创建前失败时，兼容的另一 Provider 可以接管。
- 已创建但状态未知的任务必须先用原 task ID 恢复查询，不能立即重复付费创建。
- 两者均失败时，回退到确定性转场、真实录屏或模板化片头，不阻断基础 MP4。

每个候选必须记录：首选 Provider、实际 Provider、task/request ID、fallback 原因、原始和规范化 artifact、媒体探测结果以及最终采用状态。当前自动跨 Provider fallback 未开放，按本计划后续增量实现。

## 五、视频编辑器升级工作包

视频编辑器升级是重点，不增加业务验证耦合，主要提升真实 UI 素材和生成候选的组合质量。

1. 增加镜头语言原语：`establishing_shot`、`action_focus`、`result_reveal`、`chapter_open`、`hero_transition`、`evidence_hold`、`brand_outro`。
2. 增加节奏编译：镜头加速、结果停留、音乐重拍切点、旁白结束对齐和受控等待压缩。
3. 基于已验证 `target_geometry` 实现真实 UI 的平滑推近、构图、光标轨迹、点击反馈、暗角和安全裁剪。
4. 将旁白、音乐、UI 音效和转场音效作为时间线一等素材，支持 ducking、响度统一和节拍对齐。
5. 增加 `FilmStyleProfile`，统一企业科技、B2B 信任、产品发布等风格的色彩、字幕、转场、镜头密度和音频策略。
6. 原始录屏保持只读；高级镜头效果编译为确定性 `DemoEditPlan`，保留 source artifact、source step 和时间范围引用。

## 六、质量策略

采用“硬门禁 + 软评分 + 降级”，避免过度门禁降低视频产出成功率。

- 硬门禁：不可解码、必要事实镜头缺失、敏感信息泄漏、生成镜头冒充事实、严重时长/编码错误、Renderer 失败。
- 软评分：风格一致性、节奏、转场、轻微冻结、字幕布局和品牌色偏差，用于候选排序和审核提示，不默认阻断。
- 可降级：Provider 候选、旁白、B-roll 或 2K 派生失败时，保留真实素材并输出明确降级报告。

最终聚合结果只需包含 `delivery_status`、`quality_tier`、`degradations` 和 `blocking_failures`。

## 七、开发顺序

1. 定义并测试 `CreativeSourceBundle` 最小 schema。
2. 定义 `CreativeShotIntent` 和 Provider Capability Profile。
3. 通过 adapter 接入一凯现有 FinalFilm/Director/Provider 链路。
4. 实现 H3/Seedance 兼容 fallback 和任务恢复边界。
5. 实现 Editor Compiler 的镜头语言、构图和节奏能力。
6. 接入旁白、音乐和音效时间线。
7. 用同一份事实素材完成确定性基线、单 Provider、双 Provider fallback 三组 A/B 验收。
8. 最后评估是否把 Adaptive Business Harness 提升为复杂页面的可选前置能力。

## 八、明确不做的事情

- 不重启一条完整 Browser Agent 事实链。
- 不让 H3/Seedance 修改业务 Stage、用户输入、成功条件或真实 UI 事实。
- 不复制一凯的 FinalFilm 状态机。
- 不把所有质量指标都设为 blocking。
- 不把 Provider 调用成功等同于候选采用或最终发布。
