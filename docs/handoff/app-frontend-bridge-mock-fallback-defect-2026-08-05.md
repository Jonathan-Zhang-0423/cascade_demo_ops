# App 前端缺陷报告：配置界面静默降级为 mock，从不写入后端

日期：2026-08-05
报告人：万明达（Validation Agent）
主送：孟洋
抄送：徐一凯
严重程度：**阻断级** —— 真实 App→Server 联调无法通过 UI 完成
状态：**根因已定位，修复已在本机验证通过**

## 1. 现象

在 `http://127.0.0.1:3000` 的 App 界面中：

- 通过"手动填写配置"表单填入产品地址和本地源码目录后，右侧"实时工作台"的配置面板**不更新**，仍显示示例占位值（`https://example.com`、`sample-project`）。
- 通过左侧对话框发送配置补充信息（如"产品地址是 http://127.0.0.1:5100/app，请更新配置"）**无任何响应**，界面只回显消息文本，不生成新的 configuration 提案。
- 点击"确认并生成方案"按钮**无反应**。
- dev bridge（`127.0.0.1:4317`）的请求日志中**完全没有来自前端的请求**——不是请求失败，是一次都没发出。

## 2. 根因

`frontend/web/src/bridge.ts:611-616`：

```ts
export function createBridgeClient(): DesktopBridgeClient {
  if (import.meta.env.VITE_CASCADE_BRIDGE === "local") {
    return createLocalBridgeClient(import.meta.env.VITE_CASCADE_BRIDGE_URL || defaultLocalBridgeURL);
  }
  return createMockBridgeClient();
}
```

`VITE_CASCADE_BRIDGE=local` 配置在**仓库根目录**的 `cascade_demo_ops/.env` 中，但 Vite 的 dev server 工作目录是 `frontend/web/`，**只加载该目录下的 `.env*` 文件**，不会读取仓库根目录的 `.env`。

`frontend/web/` 下不存在任何 `.env` 文件，因此 `import.meta.env.VITE_CASCADE_BRIDGE` 为 `undefined`，条件判断失败，前端**静默回落到 `createMockBridgeClient()`**（`bridge.ts:1094`）。

mock 实现的所有方法只操作浏览器内存中的 `Map`，从不发起 HTTP 请求。例如 `saveProjectInputs`（`bridge.ts:1287-1293`）：

```ts
async saveProjectInputs(projectID, inputs) {
  const project = projects.get(projectID);
  if (!project) return { ok: false, error: "未找到项目" };
  const next = { ...project, productURL: inputs.product_urls?.[0]?.url ?? project.productURL, inputBundle: inputs };
  projects.set(projectID, next);
  return ok(next);          // 无 requestLocal 调用，无 HTTP
}
```

对照 `createLocalBridgeClient` 中的真实实现（`bridge.ts:809-817`）：

```ts
async saveProjectInputs(projectID, inputs) {
  const result = await requestLocal<LocalProjectContext>(baseURL, `/v1/desktop/projects/${encodeURIComponent(projectID)}/inputs`, {
    method: "POST",
    body: JSON.stringify(inputs),
  });
  ...
}
```

因此界面上一切操作看起来"成功"（mock 返回 `ok()`），但数据从未离开浏览器。这解释了为什么配置面板不更新、聊天无响应、且后端日志零请求。

## 3. 影响

1. **真实 App→Server 联调无法通过 UI 完成。** 用户无法通过界面填写真实产品地址和源码目录，也无法触发执行包生成。测试端 A 本轮只能绕过 UI、直接调用 dev bridge REST API 才创建出真实项目（见《真实 App 包验收阻塞报告》第 9 节复现步骤）。
2. **失败模式是静默的。** 没有报错、没有警告、没有控制台输出，界面表现为"操作生效了但数据没变"，极易被误判为后端问题或环境问题。测试端 A 初期确实误判为网络或 dev server 版本问题，排查耗时约 1 小时。
3. **`VITE_CASCADE_BRIDGE_URL` 为空值时的默认行为叠加了风险。** `defaultLocalBridgeURL = ""`（`bridge.ts:609`）意味着即使走对了 local 分支，请求也是相对路径，依赖 `vite.config.ts` 的 proxy 转发到 `127.0.0.1:4317`。该 proxy 配置存在且正确，但这条链路上任一环节断裂都会以类似的静默方式失败。

## 4. 已验证的修复

在 `frontend/web/` 目录下创建 `.env.local`：

```env
VITE_CASCADE_BRIDGE=local
VITE_CASCADE_BRIDGE_URL=
```

重启 Vite dev server 后实测：前端立即开始向 dev bridge 发起真实请求（`POST /v1/desktop/assistant/sessions/.../turns`、`GET /v1/desktop/assistant/sessions/.../events` 等均返回 200），修复前该日志为零请求。

## 5. 建议的正式修复方向

上述 `.env.local` 只是本机验证手段，`.env.local` 按惯例不入版本库。建议 App 侧选择其一做正式修复：

1. **在 `frontend/web/` 下提交一个 `.env.development`**，写入 `VITE_CASCADE_BRIDGE=local`，使本地开发默认走真实 bridge。
2. **在 `vite.config.ts` 中通过 `envDir` 指向仓库根目录**，让 Vite 读取现有的根 `.env`：
   ```ts
   export default defineConfig({ envDir: "../..", /* ... */ })
   ```
   这样根目录 `.env` 里已有的 `VITE_CASCADE_BRIDGE=local` 立即生效，无需新增文件。
3. **消除静默降级**：让 `createBridgeClient()` 在回落到 mock 时打印显式警告（如 `console.warn("[bridge] VITE_CASCADE_BRIDGE 未设置为 local，已回落到 mock 客户端，所有操作不会写入后端")`），或在开发构建下直接抛错。当前的静默回落是本次排查成本的主要来源。

建议同时采纳方案 2（或 1）与方案 3：前者修复默认行为，后者保证同类问题下次能被立刻发现。

## 6. 复现步骤

```bash
# 1. 确认 frontend/web/ 下无 .env 文件
ls frontend/web/.env*        # 预期：无匹配

# 2. 启动 dev bridge 与前端
cd cascade_demo_ops && pnpm dev:bridge
cd cascade_demo_ops/frontend/web && npx vite --host 127.0.0.1 --port 3000 --strictPort

# 3. 浏览器打开 127.0.0.1:3000，在"手动填写配置"中填入任意产品地址并保存

# 4. 观察 dev bridge 日志
#    预期（缺陷表现）：无任何 dev_bridge request 记录
#    修复后：出现 POST /v1/desktop/projects/<id>/inputs 等真实请求
```

## 7. 与验收阻塞的关系

本缺陷与《真实 App 包验收阻塞报告》（`docs/handoff/real-app-package-acceptance-blocked-2026-08-05.md`）中记录的执行包确信度门禁阻塞是**两个独立问题**：

- 本缺陷阻止了通过 UI 完成配置输入，绕过 UI 调 API 后即可正常创建项目并生成执行包。
- 确信度门禁阻塞发生在执行包生成之后，根因是 outline 生成器未给 `click` 步骤写入 `non_destructive: true`，与前端无关。

两者都定位在 App / Outline 生成侧，建议一并处理。
