import { useEffect, useRef, useState } from "react";
import {
  ChevronDown,
  ChevronRight,
  Download,
  Eye,
  FolderOpen,
  KeyRound,
  Link2,
  Loader2,
  ShieldAlert,
} from "lucide-react";
import { api, type PreviewResponse, type RunRequest } from "../api";
import { useApp } from "../store";
import { useI18n, looksLikeTimeout } from "../i18n";
import { Field, Toggle } from "../components/ui";
import { SeriesBrowser } from "../components/SeriesBrowser";
import { DirPicker } from "../components/DirPicker";
import { OutputTemplates, SAMPLE_MOVIE, SAMPLE_SERIES } from "../components/OutputTemplates";
import { InstallFFmpeg } from "../components/InstallFFmpeg";

const QUALITIES = [
  // "" is the engine's "optimal" pick (a bandwidth sweet spot, not the biggest
  // file); "max" is the highest-bandwidth variant on offer.
  { v: "", label: "Auto (optimal)" },
  { v: "max", label: "Maximum" },
  { v: "2160p", label: "2160p · 4K" },
  { v: "1080p", label: "1080p" },
  { v: "720p", label: "720p" },
  { v: "480p", label: "480p" },
  { v: "360p", label: "360p" },
];

export function DownloadPage({ onStarted, onSignIn }: { onStarted: () => void; onSignIn: () => void }) {
  const { settings, settingsLoaded, ffmpeg, kpauth, toast } = useApp();
  const { t } = useI18n();

  const [form, setForm] = useState<RunRequest>(() => ({
    url: "",
    outputPath: settings.outputPath,
    dirTemplate: settings.dirTemplate,
    nameTemplate: settings.nameTemplate,
    quality: settings.quality,
    container: settings.container,
    concurrency: settings.concurrency,
    retries: settings.retries,
    minIntervalMs: settings.minIntervalMs,
    proxy: settings.proxy,
    seasons: "",
    episodes: "",
    audio: "",
    audioMenu: true,
    force: false,
    noChunked: settings.noChunked,
    dryRun: false,
    ffmpegArgs: "",
    ffmpegPath: "",
    userAgent: "",
    verbosity: settings.verbosity,
  }));

  const [advanced, setAdvanced] = useState(false);
  const [preview, setPreview] = useState<PreviewResponse | null>(null);
  // Per-episode selection (episode keys "S{n}E{n}"). null until a preview loads;
  // a preview seeds it with all not-yet-downloaded episodes.
  const [selectedKeys, setSelectedKeys] = useState<Set<string> | null>(null);

  const selectableKeysOf = (p: PreviewResponse): string[] =>
    p.seasons.flatMap((s) => s.episodes.filter((e) => !e.completed).map((e) => e.key));
  const [previewing, setPreviewing] = useState(false);
  const [starting, setStarting] = useState(false);
  const [pickDir, setPickDir] = useState(false);

  // Seed form defaults from settings once the SSE snapshot has loaded (B3).
  // The seeded flag prevents re-seeding on subsequent SSE reconnects.
  const seeded = useRef(false);
  useEffect(() => {
    if (!settingsLoaded || seeded.current) return;
    seeded.current = true;
    setForm((f) => ({
      ...f,
      outputPath: f.outputPath || settings.outputPath,
      dirTemplate: f.dirTemplate || settings.dirTemplate,
      nameTemplate: f.nameTemplate || settings.nameTemplate,
      quality: settings.quality,
      container: settings.container,
      concurrency: settings.concurrency,
      retries: settings.retries,
      minIntervalMs: settings.minIntervalMs,
      proxy: settings.proxy,
      noChunked: settings.noChunked,
      verbosity: settings.verbosity,
    }));
  }, [settingsLoaded, settings]);

  const set = <K extends keyof RunRequest>(k: K, v: RunRequest[K]) =>
    setForm((f) => ({ ...f, [k]: v }));

  // Which saved default pair the form currently mirrors, so the Series/Movie
  // buttons show the active one.
  const isMovieLayout =
    form.dirTemplate === settings.movieDirTemplate && form.nameTemplate === settings.movieNameTemplate;

  // Switching the preset moves the folder too — the whole point of the split is
  // that films and series live in different libraries.
  const applyLayout = (kind: "series" | "movie") =>
    setForm((f) => ({
      ...f,
      outputPath:
        kind === "movie" ? settings.movieOutputPath || settings.outputPath : settings.outputPath,
      dirTemplate: kind === "movie" ? settings.movieDirTemplate : settings.dirTemplate,
      nameTemplate: kind === "movie" ? settings.movieNameTemplate : settings.nameTemplate,
    }));

  const layoutBtn = (active: boolean) =>
    active
      ? "rounded-lg bg-accent-500/15 px-2.5 py-1 font-medium text-accent-300"
      : "rounded-lg px-2.5 py-1 text-slate-400 hover:text-slate-200";

  const toggleEpisode = (key: string) =>
    setSelectedKeys((cur) => {
      const base = new Set(cur ?? []);
      base.has(key) ? base.delete(key) : base.add(key);
      return base;
    });

  const toggleSeason = (season: number) =>
    setSelectedKeys((cur) => {
      const base = new Set(cur ?? []);
      const eps = preview?.seasons.find((s) => s.number === season)?.episodes.filter((e) => !e.completed) ?? [];
      const allOn = eps.length > 0 && eps.every((e) => base.has(e.key));
      eps.forEach((e) => (allOn ? base.delete(e.key) : base.add(e.key)));
      return base;
    });

  const selectAllEpisodes = () => setSelectedKeys(new Set(preview ? selectableKeysOf(preview) : []));
  const deselectAllEpisodes = () => setSelectedKeys(new Set());

  const errorToast = (msg: string, fallback: string) => {
    if (looksLikeTimeout(msg)) {
      toast(t("Request timed out — kino.pub may be unreachable without a VPN. Enable a VPN or set a proxy, then retry."), "error");
    } else {
      toast(msg || fallback, "error");
    }
  };

  const doPreview = async () => {
    if (!form.url.trim()) {
      toast(t("Enter a kino.pub URL first"), "error");
      return;
    }
    setPreviewing(true);
    try {
      const r = await api.preview({ ...form, dryRun: true });
      setPreview(r);
      setSelectedKeys(new Set(selectableKeysOf(r)));
      toast(t('Resolved “{title}” · {n} episodes', { title: r.title, n: r.total }), "success");
    } catch (e: any) {
      errorToast(e.message, t("Preview failed"));
      setPreview(null);
    } finally {
      setPreviewing(false);
    }
  };

  const start = async () => {
    if (!form.url.trim()) {
      toast(t("Enter a kino.pub URL first"), "error");
      return;
    }
    if (!ffmpeg.ffmpegFound) {
      toast(t("ffmpeg not found — install it to download"), "error");
      return;
    }
    setStarting(true);
    try {
      const seedTitles = preview
        ? Object.fromEntries(
            preview.seasons.flatMap((s) => s.episodes.map((e) => [e.key, e.title])),
          )
        : null;
      await api.startJob({
        ...form,
        dryRun: false,
        // Explicit per-episode selection from the browser. Omitted (→ all) when
        // the user downloads without previewing.
        episodeKeys: preview && selectedKeys ? [...selectedKeys] : undefined,
        seedTitle: preview?.title || "",
        seedPoster: preview?.posterUrl || "",
        seedTitles,
      });
      toast(t("Download started"), "success");
      onStarted();
    } catch (e: any) {
      errorToast(e.message, t("Failed to start"));
    } finally {
      setStarting(false);
    }
  };

  return (
    <div className="mx-auto max-w-3xl space-y-5">
      <header>
        <h1 className="text-2xl font-bold text-slate-100">{t("Advanced download")}</h1>
        <p className="mt-1 text-sm text-slate-400">
          {t("Paste a kino.pub link to download it directly. The Catalog is the main way to find titles.")}
        </p>
      </header>

      <div className="card flex items-start gap-3 border-accent-500/20 bg-accent-500/[0.06] p-4 text-sm text-accent-200">
        <ShieldAlert className="mt-0.5 h-4 w-4 shrink-0" />
        <span>
          {t("kino.pub is often unavailable without a VPN. If requests hang or time out, enable a VPN or set a proxy below.")}
        </span>
      </div>

      {!kpauth.loggedIn && (
        <div className="card flex flex-wrap items-center gap-3 border-white/[0.08] p-4 text-sm text-slate-300">
          <KeyRound className="h-4 w-4 shrink-0 text-accent-400" />
          <span className="min-w-0 flex-1">
            {t("Sign in to kino.pub (Settings) to resolve and download titles.")}
          </span>
          <button className="btn-primary px-3 py-2" onClick={onSignIn}>
            {t("Sign in")}
          </button>
        </div>
      )}

      <div className="card space-y-4 p-5">
        <Field label={t("kino.pub link")}>
          <div className="flex gap-2">
            <div className="relative flex-1">
              <Link2 className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-500" />
              <input
                className="input pl-9"
                placeholder="https://kino.pub/item/view/38290"
                value={form.url}
                onChange={(e) => set("url", e.target.value)}
                onKeyDown={(e) => e.key === "Enter" && doPreview()}
              />
            </div>
            <button className="btn-ghost" onClick={doPreview} disabled={previewing}>
              {previewing ? <Loader2 className="h-4 w-4 animate-spin" /> : <Eye className="h-4 w-4" />}
              {t("Preview")}
            </button>
          </div>
        </Field>

        <div className="grid gap-4 sm:grid-cols-2">
          <Field label={t("Quality")}>
            <select className="input" value={form.quality} onChange={(e) => set("quality", e.target.value)}>
              {QUALITIES.map((q) => (
                <option key={q.v} value={q.v}>
                  {q.v === "" || q.v === "max" ? t(q.label) : q.label}
                </option>
              ))}
            </select>
          </Field>
          <Field label={t("Output folder")}>
            <button
              className="input flex items-center gap-2 text-left"
              onClick={() => setPickDir(true)}
              type="button"
            >
              <FolderOpen className="h-4 w-4 shrink-0 text-accent-400" />
              <span className="truncate font-mono text-xs">{form.outputPath || t("Choose…")}</span>
            </button>
          </Field>
        </div>

        <div className="space-y-3 border-t border-white/[0.05] pt-4">
          <div className="flex items-center justify-between gap-3">
            <p className="text-sm font-semibold text-slate-200">{t("Where to save")}</p>
            {/* A direct link doesn't say whether it's a film or a series until a
                preview runs, so the layout preset is picked by hand here. */}
            <div className="flex gap-1 text-xs">
              <button
                type="button"
                className={layoutBtn(!isMovieLayout)}
                onClick={() => applyLayout("series")}
              >
                {t("Serial")}
              </button>
              <button
                type="button"
                className={layoutBtn(isMovieLayout)}
                onClick={() => applyLayout("movie")}
              >
                {t("Movie")}
              </button>
            </div>
          </div>
          <OutputTemplates
            dirTemplate={form.dirTemplate || settings.dirTemplate}
            nameTemplate={form.nameTemplate || settings.nameTemplate}
            onChange={(dir, name) =>
              setForm((f) => ({ ...f, dirTemplate: dir, nameTemplate: name }))
            }
            outputPath={form.outputPath}
            values={isMovieLayout ? SAMPLE_MOVIE : SAMPLE_SERIES}
            container={form.container}
            defaults={
              isMovieLayout
                ? { dir: settings.movieDirTemplate, name: settings.movieNameTemplate }
                : { dir: settings.dirTemplate, name: settings.nameTemplate }
            }
          />
        </div>

        <button
          className="flex items-center gap-1.5 text-sm font-medium text-slate-400 hover:text-slate-200"
          onClick={() => setAdvanced((v) => !v)}
          type="button"
        >
          {advanced ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
          {t("Advanced options")}
        </button>

        {advanced && (
          <div className="animate-fade-in space-y-4 border-t border-white/[0.05] pt-4">
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label={t("Container")}>
                <select className="input" value={form.container} onChange={(e) => set("container", e.target.value)}>
                  <option value="mkv">{t("MKV (best multi-audio)")}</option>
                  <option value="mp4">MP4</option>
                </select>
              </Field>
              <Field
                label={t("Episodes at once within a download")}
                hint={t("How many episodes of a series download together. A film has one, so this does nothing for films.")}
              >
                <input
                  type="number"
                  min={1}
                  max={16}
                  className="input"
                  value={form.concurrency}
                  onChange={(e) => set("concurrency", e.target.value === "" ? 1 : Math.max(1, Number(e.target.value)))}
                />
              </Field>
              <Field label={t("Retries")} hint={t("re-attempts per episode after a network error (timeout, reset, 5xx)")}>
                <input
                  type="number"
                  min={0}
                  className="input"
                  value={form.retries}
                  onChange={(e) => set("retries", e.target.value === "" ? 5 : Math.max(0, Number(e.target.value)))}
                />
              </Field>
              <Field label={t("Min interval (ms)")} hint={t("throttle requests (0–60000)")}>
                <input
                  type="number"
                  min={0}
                  max={60000}
                  className="input"
                  value={form.minIntervalMs}
                  onChange={(e) => set("minIntervalMs", e.target.value === "" ? 0 : Math.max(0, Number(e.target.value)))}
                />
              </Field>
              <Field label={t("Proxy")} hint={t("http / https / socks5")}>
                <input className="input" placeholder="socks5://127.0.0.1:1080" value={form.proxy} onChange={(e) => set("proxy", e.target.value)} />
              </Field>
            </div>

            <div className="grid gap-2 sm:grid-cols-2">
              <Toggle label={t("Force re-download")} hint={t("Ignore completed state")} checked={form.force} onChange={(v) => set("force", v)} />
              <Toggle label={t("No chunked download")} hint={t("Stream everything via ffmpeg")} checked={form.noChunked} onChange={(v) => set("noChunked", v)} />
              <Toggle label={t("Verbose logs")} hint={t("Show debug-level log lines")} checked={form.verbosity === "verbose"} onChange={(v) => set("verbosity", v ? "verbose" : "normal")} />
            </div>

            <Field label={t("Extra ffmpeg args")} hint={t('advanced — e.g. "-c:v libx265 -crf 28"')}>
              <input className="input font-mono text-xs" value={form.ffmpegArgs} onChange={(e) => set("ffmpegArgs", e.target.value)} />
            </Field>
          </div>
        )}

        <div className="flex flex-wrap items-center gap-3 border-t border-white/[0.05] pt-4">
          <button
            className="btn-primary"
            onClick={start}
            disabled={starting || (!!preview && !!selectedKeys && selectedKeys.size === 0)}
          >
            {starting ? <Loader2 className="h-4 w-4 animate-spin" /> : <Download className="h-4 w-4" />}
            {preview && selectedKeys ? t("Start download ({n})", { n: selectedKeys.size }) : t("Start download")}
          </button>
          {!ffmpeg.ffmpegFound && (
            <span className="text-xs text-ember-400">{t("ffmpeg not detected — required to download")}</span>
          )}
        </div>
        {!ffmpeg.ffmpegFound && <InstallFFmpeg className="border-t border-white/[0.05] pt-4" />}
      </div>

      {preview && (
        <div className="card p-5">
          <SeriesBrowser
            preview={preview}
            selectedKeys={selectedKeys ?? new Set()}
            onToggleEpisode={toggleEpisode}
            onToggleSeason={toggleSeason}
            onSelectAll={selectAllEpisodes}
            onDeselectAll={deselectAllEpisodes}
          />
        </div>
      )}

      <DirPicker
        open={pickDir}
        initial={form.outputPath}
        onClose={() => setPickDir(false)}
        onSelect={(p) => set("outputPath", p)}
      />
    </div>
  );
}
