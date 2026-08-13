export type CascadeLocale = "en-US" | "zh-CN";

const localeStorageKey = "cascade-locale";

export function getStoredLocale(): CascadeLocale {
  try {
    const stored = window.localStorage.getItem(localeStorageKey);
    if (stored === "en-US" || stored === "zh-CN") return stored;
  } catch {
    // Device language remains the safe default when storage is unavailable.
  }
  return window.navigator.language.toLowerCase().startsWith("zh") ? "zh-CN" : "en-US";
}

export function storeLocale(locale: CascadeLocale) {
  try {
    window.localStorage.setItem(localeStorageKey, locale);
  } catch {
    // The locale still applies for the current session.
  }
}

export function copy(locale: CascadeLocale, english: string, chinese: string) {
  return locale === "zh-CN" ? chinese : english;
}
