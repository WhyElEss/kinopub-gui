// Theme handling. The choice is a server-side setting, so every device that
// opens this install agrees — but it arrives with the SSE snapshot, i.e. after
// the first paint. The last applied value is therefore mirrored in
// localStorage and re-applied before React mounts, so a reload does not flash
// the wrong theme.

export const THEMES = ["auto", "light", "dark"] as const;
export type Theme = (typeof THEMES)[number];

const KEY = "kinopub.theme";

export function normalizeTheme(v: unknown): Theme {
  return THEMES.includes(v as Theme) ? (v as Theme) : "auto";
}

// applyTheme stamps the root element. "auto" removes the attribute so the CSS
// falls back to prefers-color-scheme, which is what makes the day/night switch
// happen on its own.
export function applyTheme(theme: Theme) {
  const root = document.documentElement;
  if (theme === "auto") root.removeAttribute("data-theme");
  else root.setAttribute("data-theme", theme);
  try {
    localStorage.setItem(KEY, theme);
  } catch {
    /* private mode — the theme just won't survive a reload */
  }
}

// applyStoredTheme runs before the first render, from the cached value.
export function applyStoredTheme() {
  let stored: string | null = null;
  try {
    stored = localStorage.getItem(KEY);
  } catch {
    /* ignore */
  }
  applyTheme(normalizeTheme(stored));
}
