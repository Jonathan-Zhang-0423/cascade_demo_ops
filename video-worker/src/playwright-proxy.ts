type PlaywrightProxy = {
  server: string;
  bypass?: string;
  username?: string;
  password?: string;
};

export function launchOptionsWithProxy<T extends Record<string, unknown>>(options: T): T & { proxy?: PlaywrightProxy } {
  const proxy = playwrightProxyFromEnv();
  if (!proxy) {
    return options;
  }
  return { ...options, proxy };
}

function playwrightProxyFromEnv(): PlaywrightProxy | undefined {
  const server = normalizeProxyServer(
    firstEnvValue(
      "CASCADE_BROWSER_PROXY_SERVER",
      "CASCADE_PLAYWRIGHT_PROXY_SERVER",
      "HTTPS_PROXY",
      "HTTP_PROXY",
      "https_proxy",
      "http_proxy",
    ),
  );
  if (!server) {
    return undefined;
  }
  const proxy: PlaywrightProxy = { server };
  const bypass = firstEnvValue("CASCADE_BROWSER_PROXY_BYPASS", "NO_PROXY", "no_proxy");
  if (bypass) {
    proxy.bypass = bypass;
  }
  const username = firstEnvValue("CASCADE_BROWSER_PROXY_USERNAME", "CASCADE_PLAYWRIGHT_PROXY_USERNAME");
  const password = firstEnvValue("CASCADE_BROWSER_PROXY_PASSWORD", "CASCADE_PLAYWRIGHT_PROXY_PASSWORD");
  if (username) {
    proxy.username = username;
  }
  if (password) {
    proxy.password = password;
  }
  return proxy;
}

function normalizeProxyServer(value: string | undefined): string | undefined {
  const trimmed = value?.trim();
  if (!trimmed) {
    return undefined;
  }
  if (/^[a-z][a-z0-9+.-]*:\/\//i.test(trimmed)) {
    return trimmed;
  }
  return `http://${trimmed}`;
}

function firstEnvValue(...keys: string[]): string | undefined {
  for (const key of keys) {
    const value = process.env[key]?.trim();
    if (value) {
      return value;
    }
  }
  return undefined;
}
