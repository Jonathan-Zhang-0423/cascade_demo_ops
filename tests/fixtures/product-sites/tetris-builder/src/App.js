const routes = {
  login: "/login",
  workspace: "/app",
  project: "/project/demo-tetris",
};

function renderLogin(root) {
  root.innerHTML = `
    <main class="auth-page" data-route="/login" data-page="login">
      <section class="auth-panel">
        <p class="eyebrow">Cascade Demo Fixture</p>
        <h1>登录到项目构建工作台</h1>
        <label>演示账号<input data-testid="login-email" aria-label="演示账号" value="demo@example.com" /></label>
        <label>演示密码<input data-testid="login-password" aria-label="演示密码" type="password" value="fixture-password" /></label>
        <button data-testid="login-submit">登录并进入工作台</button>
      </section>
    </main>`;
  root.querySelector("[data-testid='login-submit']").addEventListener("click", () => navigate(routes.workspace));
}

function renderWorkspace(root) {
  root.innerHTML = `
    <main class="workspace" data-route="/app" data-page="workspace">
      <header class="topbar">
        <div><p class="eyebrow">工作台</p><h1>AI 应用项目</h1></div>
        <button data-testid="new-project" class="primary">新建项目</button>
      </header>
      <section class="creation-flow">
        <h2>创建新项目</h2>
        <label>项目名称<input data-testid="project-name-input" aria-label="项目名称" value="俄罗斯方块" placeholder="例如：俄罗斯方块" /></label>
        <fieldset>
          <legend>构建模式</legend>
          <button data-testid="build-mode" class="selected">构建模式</button>
          <button data-testid="prototype-mode">原型模式</button>
        </fieldset>
        <button data-testid="start-build" class="primary">启动 agent 实际构建</button>
      </section>
    </main>`;
  root.querySelector("[data-testid='start-build']").addEventListener("click", () => navigate(routes.project));
}

function renderProject(root) {
  root.innerHTML = `
    <main class="project-page" data-route="/project/:id" data-page="project-build">
      <header class="project-hero">
        <div><p class="eyebrow">项目详情</p><h1>俄罗斯方块</h1></div>
        <span data-testid="project-route-badge">/project/demo-tetris</span>
      </header>
      <section class="agent-panel">
        <h2>Agent 构建中</h2>
        <p data-testid="build-mode-summary">当前模式：构建模式</p>
        <ol data-testid="agent-build-log">
          <li>Agent 正在解析需求：俄罗斯方块核心玩法、计分、方块旋转。</li>
          <li>Agent 正在生成页面组件和样式。</li>
          <li>Agent 正在连接预览环境并执行构建检查。</li>
          <li>Agent 正在输出可交互的俄罗斯方块演示页面。</li>
        </ol>
      </section>
    </main>`;
}

function navigate(nextRoute) {
  history.pushState({}, "", nextRoute);
  render();
}

function render() {
  const root = document.body;
  if (location.pathname.startsWith("/project/")) {
    renderProject(root);
    return;
  }
  if (location.pathname === routes.workspace) {
    renderWorkspace(root);
    return;
  }
  renderLogin(root);
}

window.addEventListener("popstate", render);
render();
