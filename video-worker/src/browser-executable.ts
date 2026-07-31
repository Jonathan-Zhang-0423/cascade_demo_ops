import { existsSync } from "node:fs";

export function configuredBrowserExecutable(): string | undefined {
  const configured = process.env.CASCADE_BROWSER_EXECUTABLE_PATH?.trim();
  if (configured) return configured;

  // Windows desktop installations already depend on WebView2. Prefer the
  // managed Edge binary so local page scanning needs no separate browser install.
  if (process.platform === "win32") {
    const candidates = [
      process.env.PROGRAMFILES_X86 && `${process.env.PROGRAMFILES_X86}\\Microsoft\\Edge\\Application\\msedge.exe`,
      process.env.PROGRAMFILES && `${process.env.PROGRAMFILES}\\Microsoft\\Edge\\Application\\msedge.exe`,
      process.env.LOCALAPPDATA && `${process.env.LOCALAPPDATA}\\Microsoft\\Edge\\Application\\msedge.exe`,
    ];
    return candidates.find((candidate): candidate is string => Boolean(candidate && existsSync(candidate)));
  }
  if (process.platform === "darwin") {
    const candidates = [
      "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
      "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
    ];
    return candidates.find((candidate) => existsSync(candidate));
  }
  return undefined;
}
