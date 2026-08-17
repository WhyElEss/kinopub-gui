import { RotateCcw } from "lucide-react";
import { Field } from "./ui";
import { useI18n } from "../i18n";

// Defaults must stay in sync with internal/services/outputlayout/template.go —
// the server applies these same strings when a request leaves a template empty.
export const DEFAULT_DIR_TEMPLATE = "{title}";
export const DEFAULT_NAME_TEMPLATE = "Season {season:02}/S{season:02}E{episode:02}";
export const DEFAULT_MOVIE_DIR_TEMPLATE = "{ru} ({year})";
export const DEFAULT_MOVIE_NAME_TEMPLATE = "{ru} ({year})";

// Sample values used for the preview line when a real title isn't known yet.
export const SAMPLE_SERIES: TemplateValues = {
  title: "Рик и Морти / Rick and Morty",
  ru: "Рик и Морти",
  original: "Rick and Morty",
  year: 2013,
  id: "12345",
  season: 3,
  episode: 7,
  epTitle: "Пиклик",
  quality: "1080p",
};

export const SAMPLE_MOVIE: TemplateValues = {
  title: "Матрица / The Matrix",
  ru: "Матрица",
  original: "The Matrix",
  year: 1999,
  id: "54321",
  season: 1,
  episode: 1,
  epTitle: "",
  quality: "1080p",
};

export interface TemplateValues {
  title: string;
  ru: string;
  original: string;
  year: number;
  id: string;
  season: number;
  episode: number;
  epTitle: string;
  quality: string;
}

const NUMERIC = new Set(["season", "episode"]);

// expandTemplate mirrors outputlayout.expand for the live preview only. It is
// deliberately forgiving: unknown tokens stay visible so a typo is obvious, and
// no filesystem sanitizing happens here (the server does that for real).
export function expandTemplate(tmpl: string, v: TemplateValues): string {
  return tmpl.replace(/\{([^}]*)\}/g, (whole, expr: string) => {
    const [rawName, pad] = expr.split(":");
    const name = rawName.trim().toLowerCase();
    if (NUMERIC.has(name)) {
      const n = name === "season" ? v.season : v.episode;
      const width = pad && /^0\d$/.test(pad) ? Number(pad) : 0;
      return String(n).padStart(width, "0");
    }
    const strings: Record<string, string> = {
      title: v.title,
      ru: v.ru || v.title,
      original: v.original || v.ru || v.title,
      year: v.year > 0 ? String(v.year) : "",
      id: v.id,
      eptitle: v.epTitle,
      quality: v.quality,
    };
    if (name in strings) return strings[name];
    return whole;
  });
}

// previewPath renders the full path a download would produce, with empty
// components dropped exactly like the server drops them.
export function previewPath(
  outputPath: string,
  dirTmpl: string,
  nameTmpl: string,
  values: TemplateValues,
  ext: string,
): string {
  // Mirrors outputlayout.expand: the TEMPLATE is split into components first, so
  // a title containing "/" stays one folder instead of silently nesting, and
  // each component gets the same character substitution the server applies.
  const parts = [...dirTmpl.split("/"), ...nameTmpl.split("/")]
    .map((p) => expandTemplate(p, values).trim().replace(/[/\\:*?"<>|]/g, "_"))
    .map((p) => p.replace(/^[\s.]+|[\s.]+$/g, ""))
    .filter(Boolean);
  const root = outputPath.replace(/\/+$/, "");
  return [root, ...parts].join("/") + "." + ext;
}

export function OutputTemplates({
  dirTemplate,
  nameTemplate,
  onChange,
  outputPath,
  values,
  container,
  defaults,
}: {
  dirTemplate: string;
  nameTemplate: string;
  onChange: (dir: string, name: string) => void;
  outputPath: string;
  values: TemplateValues;
  container: string;
  defaults: { dir: string; name: string };
}) {
  const { t } = useI18n();
  const isDefault = dirTemplate === defaults.dir && nameTemplate === defaults.name;

  return (
    <div className="space-y-3">
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label={t("Folder")}>
          <input
            className="input font-mono text-xs"
            value={dirTemplate}
            spellCheck={false}
            onChange={(e) => onChange(e.target.value, nameTemplate)}
          />
        </Field>
        <Field label={t("File name")}>
          <input
            className="input font-mono text-xs"
            value={nameTemplate}
            spellCheck={false}
            onChange={(e) => onChange(dirTemplate, e.target.value)}
          />
        </Field>
      </div>

      <div className="rounded-lg bg-white/[0.03] px-3 py-2">
        <p className="text-[11px] uppercase tracking-wide text-slate-500">{t("Will be saved as")}</p>
        <p className="mt-0.5 break-all font-mono text-xs text-gold-300">
          {previewPath(outputPath, dirTemplate, nameTemplate, values, container === "mp4" ? "mp4" : "mkv")}
        </p>
      </div>

      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-slate-500">
        <span>
          {t("Tokens")}: <span className="font-mono">{"{title} {ru} {original} {year} {season:02} {episode:02} {epTitle} {quality}"}</span>
        </span>
        {!isDefault && (
          <button
            type="button"
            className="inline-flex items-center gap-1 text-slate-400 hover:text-slate-200"
            onClick={() => onChange(defaults.dir, defaults.name)}
          >
            <RotateCcw className="h-3 w-3" /> {t("Reset")}
          </button>
        )}
      </div>
    </div>
  );
}
