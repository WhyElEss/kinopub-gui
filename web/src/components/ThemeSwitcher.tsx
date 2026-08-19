import clsx from "clsx";
import { Monitor, Moon, Sun } from "lucide-react";
import { useApp } from "../store";
import { useI18n } from "../i18n";
import { THEMES, type Theme } from "../theme";

const ICONS: Record<Theme, typeof Sun> = { auto: Monitor, light: Sun, dark: Moon };
const TITLES: Record<Theme, string> = {
  auto: "Follow the system",
  light: "Day",
  dark: "Night",
};

export function ThemeSwitcher() {
  const { settings, setSettingsLocal, saveTheme } = useApp();
  const { t } = useI18n();
  const current: Theme = THEMES.includes(settings.theme as Theme) ? (settings.theme as Theme) : "auto";

  return (
    <div className="inline-flex items-center rounded-full border border-white/[0.08] bg-white/[0.02] p-0.5">
      {THEMES.map((id) => {
        const Icon = ICONS[id];
        return (
          <button
            key={id}
            onClick={() => {
              // Applied immediately and saved in the background: the setting is
              // shared by every device that opens this server.
              setSettingsLocal({ ...settings, theme: id });
              void saveTheme(id);
            }}
            title={t(TITLES[id])}
            aria-label={t(TITLES[id])}
            aria-pressed={current === id}
            className={clsx(
              "rounded-full p-1.5 transition",
              current === id ? "bg-accent-500 text-ink-950" : "text-slate-400 hover:text-slate-200",
            )}
          >
            <Icon className="h-3.5 w-3.5" />
          </button>
        );
      })}
    </div>
  );
}
