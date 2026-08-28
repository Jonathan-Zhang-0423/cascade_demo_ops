# 企业宣传片链路实施记录 v1

本轮已完成开发计划的第一批实现，保持 Browser Agent/OutcomeVerifier 事实链一次执行，并通过只读 artifact 接入创作链。

## 已实现

- `backend/internal/model/creative_source_bundle.go`
  - 定义并校验 `CreativeSourceBundle`。
  - 必须包含 source digest、必要事实、不可变且非敏感素材引用和事实基线降级策略。
- `backend/internal/model/creative_shot_intent.go`
  - 定义镜头语言角色、Provider 候选、fallback 策略和 presentation-only 约束。
- `backend/internal/creative/source_bundle.go`
  - 从现有 `AssetTimelineCatalog` 生成一次性事实包，不重新执行事实验证。
- `backend/internal/creative/adapter.go`
  - 将新契约适配到现有 `PresentationGenerationIntent`。
  - 编译确定性的 `DemoEditPlan`，保留原始素材和脚本顺序。
- `backend/internal/media/provider_adapter.go`
  - 增加有序兼容 Provider 选择。
  - 执行 `CreativeShotIntent.allowed_providers` allowlist。
  - Provider 已创建 task 时停止跨 Provider 重试，必须按 task ID 恢复，避免重复计费。
- `backend/internal/finalfilm/generated_track.go`
  - FinalFilm 候选生成阶段支持“调用前失败才切换 Provider”的 H3/Seedance 互补路径。
  - 保留既有状态机、Store、revision、审核和 patch apply 边界。
- `backend/internal/creative/quality.go`
  - 增加硬门禁、软评分和降级报告。
  - Provider 候选缺失默认保留事实基线，不阻断基础交付。
- `backend/internal/model/final_film_job.go` / `backend/internal/finalfilm/output_validator.go`
  - FinalFilm 输出验证增加 `delivery_status`、`quality_tier`、`degradations` 和 `blocking_failures` 字段。

## 验证

已通过：

```text
go test ./internal/model ./internal/creative ./internal/media
go build ./internal/finalfilm
```

FinalFilm 测试二进制在当前 Windows Application Control 策略下无法启动，因此使用 `go build` 完成编译级验证；现有未相关的工作树修改未被重置或覆盖。

## 尚待下一批

- 将质量报告持久化到 FinalFilm Review Package/Delivery Manifest；
- 将 Editor Compiler 的镜头原语进一步接入 Renderer 的实际时间线编译；
- 增加单 Provider、双 Provider fallback 和确定性基线三组完整 A/B 验收；
- 在具备明确授权和凭据的环境中执行真实 H3/Seedance 端到端验收。
