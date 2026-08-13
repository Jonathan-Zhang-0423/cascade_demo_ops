import { describe, expect, it } from "vitest";
import viteConfig from "../vite.config";

describe("vite demo server config", () => {
  it("pins the frontend demo to port 3000", () => {
    expect(viteConfig.envDir).toBe("../..");
    expect(viteConfig.server?.host).toBe("127.0.0.1");
    expect(viteConfig.server?.port).toBe(3000);
    expect(viteConfig.server?.strictPort).toBe(true);
    expect(viteConfig.server?.proxy?.["/v1/desktop"]).toMatchObject({
      target: "http://127.0.0.1:4317",
      changeOrigin: true,
    });
    expect(viteConfig.server?.proxy?.["/v1/editor"]).toMatchObject({
      target: "http://127.0.0.1:4317",
      changeOrigin: true,
    });
  });
});
