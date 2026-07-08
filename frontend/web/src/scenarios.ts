import type { ScenarioID, ScenarioTemplate } from "./domain";

export const scenarioTemplates: ScenarioTemplate[] = [
  {
    id: "product_demo",
    name: "产品演示",
    useCases: ["launch", "sales", "user_documentation"],
    objective: "把新功能从发现到业务价值的最短路径讲清楚。",
    targetAudience: "产品、销售、客户教育团队",
    targetDurationSec: 60,
    requiredAssets: ["demo_video", "step_by_step_docs"],
    defaultChecklist: ["价值叙事", "核心流程", "结果证明", "行动引导"],
  },
  {
    id: "ai_customer_service_demo",
    name: "AI 客服演示",
    useCases: ["support", "sales", "onboarding"],
    objective: "演示 AI 客服如何接待、检索知识、解决问题并在必要时转人工。",
    targetAudience: "客服负责人、CS 团队、AI Agent 采购评估方",
    targetDurationSec: 90,
    requiredAssets: ["demo_video", "step_by_step_docs"],
    defaultChecklist: ["客户问题", "知识检索", "问题解决", "转人工边界"],
  },
  {
    id: "internal_onboarding_tutorial",
    name: "企业新人教程",
    useCases: ["onboarding", "user_documentation", "help_center"],
    objective: "为新员工生成可重复使用的核心业务流程培训材料。",
    targetAudience: "新员工、培训负责人、企业运营团队",
    targetDurationSec: 120,
    requiredAssets: ["demo_video", "step_by_step_docs"],
    defaultChecklist: ["角色背景", "必要步骤", "合规提醒", "完成证明"],
  },
];

export function getScenarioTemplate(id: ScenarioID): ScenarioTemplate {
  const template = scenarioTemplates.find((item) => item.id === id);
  if (!template) {
    throw new Error(`未知场景模板: ${id}`);
  }
  return template;
}
