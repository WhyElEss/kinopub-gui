import { useEffect, useRef, useState } from "react";
import { ArrowUpCircle, Check, FolderOpen, FolderPlus, RefreshCw, Server, Trash2, TriangleAlert } from "lucide-react";
import { api, type FFmpegStatus, type Settings } from "../api";
import { useApp } from "../store";
import { useI18n } from "../i18n";
import { Field, Spinner, Toggle } from "../components/ui";
import { DirPicker } from "../components/DirPicker";
import { InstallFFmpeg } from "../components/InstallFFmpeg";
import { KinopubLogin } from "../components/KinopubLogin";
import {
  DEFAULT_DIR_TEMPLATE,
  DEFAULT_MOVIE_DIR_TEMPLATE,
  DEFAULT_MOVIE_NAME_TEMPLATE,
  DEFAULT_NAME_TEMPLATE,
  OutputTemplates,
  SAMPLE_MOVIE,
  SAMPLE_SERIES,
} from "../components/OutputTemplates";

type SaveState = "idle" | "saving" | "saved" | "error";

export function SettingsPage() {
  const { settings, ffmpeg, setSettingsLocal, toast } = useApp();
  const { t } = useI18n();
  const [form, setForm] = useState<Settings>(settings);
  const [saveState, setSaveState] = useState<SaveState>("idle");
  const [pickOutput, setPickOutput] = useState(false);
  const [pickMovieOutput, setPickMovieOutput] = useState(false);
  const [pickWork, setPickWork] = useState(false);
  const [pickLib, setPickLib] = useState(false);

  // Settings persist automatically on every edit (debounced) — no Save button.
  // dirty gates the resync effect so an SSE echo can't clobber in-progress edits;
  // editSeq lets an in-flight save notice a newer edit landed while it was on the
  // wire and skip settling stale state; formRef feeds the latest value to both the
  // edit handler and the unmount flush.
  const dirty = useRef(false);
  const editSeq = useRef(0);
  const saveTimer = useRef<number | undefined>(undefined);
  const formRef = useRef(form);
  formRef.current = form;

  // Resync from the store only when there's no pending edit (B4), so an SSE
  // reconnect/blip — or the echo of our own save — can't overwrite what the user
  // is editing.
  useEffect(() => {
    if (!dirty.current) setForm(settings);
  }, [settings]);

  // Let the transient "Saved" tick fade back to idle so it doesn't linger.
  useEffect(() => {
    if (saveState !== "saved") return;
    const id = window.setTimeout(() => setSaveState("idle"), 1800);
    return () => window.clearTimeout(id);
  }, [saveState]);

  const persist = async (payload: Settings, seq: number) => {
    setSaveState("saving");
    try {
      const saved = await api.saveSettings(payload);
      // Only settle when this is still the latest edit; otherwise a newer save is
      // already queued and will publish the newer value.
      if (editSeq.current === seq) {
        dirty.current = false;
        setSettingsLocal(saved);
        setSaveState("saved");
      }
    } catch (e: any) {
      setSaveState("error");
      toast(e.message || t("Save failed"), "error");
    }
  };

  const commit = (next: Settings) => {
    setForm(next);
    dirty.current = true;
    setSaveState("saving");
    const seq = ++editSeq.current;
    if (saveTimer.current) window.clearTimeout(saveTimer.current);
    saveTimer.current = window.setTimeout(() => void persist(next, seq), 600);
  };

  const set = <K extends keyof Settings>(k: K, v: Settings[K]) => commit({ ...formRef.current, [k]: v });

  // A template pair changes together (typing in one, or hitting Reset, updates
  // both), so it has to land in a single commit — two set() calls would both
  // build on the pre-edit snapshot and the first change would be lost.
  const setTemplates = (dirKey: keyof Settings, dir: string, nameKey: keyof Settings, name: string) =>
    commit({ ...formRef.current, [dirKey]: dir, [nameKey]: name });

  // Flush a still-pending edit if the user leaves the page before the debounce
  // fires, so nothing is silently dropped.
  useEffect(() => {
    return () => {
      if (saveTimer.current) window.clearTimeout(saveTimer.current);
      if (dirty.current) void api.saveSettings(formRef.current);
    };
  }, []);

  const libDirs = form.libraryDirs || [];

  return (
    <div className="mx-auto max-w-3xl space-y-5">
      <header className="flex items-start justify-between gap-3">
        <div>
          <h1 className="text-2xl font-bold text-slate-100">{t("Settings")}</h1>
          <p className="mt-1 text-sm text-slate-400">{t("Changes are saved automatically.")}</p>
        </div>
        <SaveStatus state={saveState} />
      </header>

      <KinopubLogin />

      <div className="card space-y-4 p-5">
        <div className="space-y-4">
          <div>
            <h3 className="text-sm font-semibold text-slate-200">{t("Default file layout")}</h3>
            <p className="mt-0.5 text-xs text-slate-500">
              {t("Folder and file name for every new download. Each download can override them.")}
            </p>
          </div>

          <div className="space-y-3">
            <p className="text-xs font-medium uppercase tracking-wide text-slate-400">{t("TV series")}</p>
            <Field label={t("Output folder")}>
              <button className="input flex items-center gap-2 text-left" onClick={() => setPickOutput(true)} type="button">
                <FolderOpen className="h-4 w-4 shrink-0 text-gold-400" />
                <span className="truncate font-mono text-xs">{form.outputPath || t("Choose…")}</span>
              </button>
            </Field>
            <OutputTemplates
              dirTemplate={form.dirTemplate}
              nameTemplate={form.nameTemplate}
              onChange={(dir, name) => setTemplates("dirTemplate", dir, "nameTemplate", name)}
              outputPath={form.outputPath}
              values={SAMPLE_SERIES}
              container={form.container}
              defaults={{ dir: DEFAULT_DIR_TEMPLATE, name: DEFAULT_NAME_TEMPLATE }}
            />
          </div>

          <div className="space-y-3 border-t border-white/[0.06] pt-4">
            <p className="text-xs font-medium uppercase tracking-wide text-slate-400">{t("Films")}</p>
            <Field label={t("Output folder")}>
              <button className="input flex items-center gap-2 text-left" onClick={() => setPickMovieOutput(true)} type="button">
                <FolderOpen className="h-4 w-4 shrink-0 text-gold-400" />
                <span className="truncate font-mono text-xs">{form.movieOutputPath || form.outputPath || t("Choose…")}</span>
              </button>
              {/* Empty means "wherever series go", so say which folder that is
                  instead of showing a blank field. */}
              {!form.movieOutputPath ? (
                <p className="mt-1 text-[11px] text-slate-500">{t("Same folder as series — pick another to split them.")}</p>
              ) : (
                <button
                  type="button"
                  className="mt-1 text-[11px] text-slate-400 hover:text-slate-200"
                  onClick={() => set("movieOutputPath", "")}
                >
                  {t("Use the series folder")}
                </button>
              )}
            </Field>
            <OutputTemplates
              dirTemplate={form.movieDirTemplate}
              nameTemplate={form.movieNameTemplate}
              onChange={(dir, name) => setTemplates("movieDirTemplate", dir, "movieNameTemplate", name)}
              outputPath={form.movieOutputPath || form.outputPath}
              values={SAMPLE_MOVIE}
              container={form.container}
              defaults={{ dir: DEFAULT_MOVIE_DIR_TEMPLATE, name: DEFAULT_MOVIE_NAME_TEMPLATE }}
            />
          </div>
        </div>

        <div className="space-y-3 border-t border-white/[0.06] pt-4">
          <p className="text-xs font-medium uppercase tracking-wide text-slate-400">{t("Work folder")}</p>
          <Field
            label={t("Downloading files are kept here")}
            hint={t(
              "Keeps segments and half-written files out of your media library. Best on the same disk as the output folders — then the finished file is moved instantly instead of copied.",
            )}
          >
            <button className="input flex items-center gap-2 text-left" onClick={() => setPickWork(true)} type="button">
              <FolderOpen className="h-4 w-4 shrink-0 text-gold-400" />
              <span className="truncate font-mono text-xs">
                {form.workDir || t("Next to the finished file")}
              </span>
            </button>
            {form.workDir && (
              <button
                type="button"
                className="mt-1 text-[11px] text-slate-400 hover:text-slate-200"
                onClick={() => set("workDir", "")}
              >
                {t("Keep them next to the finished file")}
              </button>
            )}
          </Field>
        </div>

        <div className="grid gap-4 sm:grid-cols-2">
          <Field label={t("Default quality")}>
            <select className="input" value={form.quality} onChange={(e) => set("quality", e.target.value)}>
              <option value="">{t("Auto (optimal)")}</option>
              <option value="max">{t("Maximum")}</option>
              <option value="2160p">2160p · 4K</option>
              <option value="1080p">1080p</option>
              <option value="720p">720p</option>
              <option value="480p">480p</option>
              <option value="360p">360p</option>
            </select>
          </Field>
          <Field label={t("Container")}>
            <select className="input" value={form.container} onChange={(e) => set("container", e.target.value)}>
              <option value="mkv">MKV</option>
              <option value="mp4">MP4</option>
            </select>
          </Field>
          <Field
            label={t("Episodes at once within a download")}
            hint={t("How many episodes of a series download together. A film has one, so this does nothing for films.")}
          >
            <input type="number" min={1} max={16} className="input" value={form.concurrency} onChange={(e) => set("concurrency", e.target.value === "" ? 1 : Math.max(1, Number(e.target.value)))} />
          </Field>
          <Field label={t("Retries")}>
            <input type="number" min={0} className="input" value={form.retries} onChange={(e) => set("retries", e.target.value === "" ? 5 : Math.max(0, Number(e.target.value)))} />
          </Field>
          <Field label={t("Min interval (ms)")}>
            <input type="number" min={0} max={60000} className="input" value={form.minIntervalMs} onChange={(e) => set("minIntervalMs", e.target.value === "" ? 0 : Math.max(0, Number(e.target.value)))} />
          </Field>
          <Field label={t("Proxy")}>
            <input className="input" placeholder="socks5://127.0.0.1:1080" value={form.proxy} onChange={(e) => set("proxy", e.target.value)} />
          </Field>
          <Field label={t("Downloads at once")}>
            <input
              type="number"
              min={0}
              max={16}
              className="input"
              value={form.maxActiveJobs}
              onChange={(e) => set("maxActiveJobs", e.target.value === "" ? 0 : Math.max(0, Number(e.target.value)))}
            />
            <p className="mt-1 text-xs text-slate-500">
              {t("0 = no limit. When set, extra downloads wait in a queue you can reorder.")}
            </p>
          </Field>
          <Field
            label={t("Check followed series every (min)")}
            hint={t("How often kino.pub is asked whether a followed series has new episodes. 15 minutes to 24 hours.")}
          >
            <input
              type="number"
              min={15}
              max={1440}
              className="input"
              value={form.watchIntervalMinutes}
              onChange={(e) =>
                set("watchIntervalMinutes", e.target.value === "" ? 180 : Math.max(0, Number(e.target.value)))
              }
            />
          </Field>
        </div>

        <p className="text-xs text-slate-500">
          {t(
            "Quality, container and folders are fixed when a download is queued. Episodes at once, retries, throttle and proxy are re-read when it starts, so a change reaches downloads already waiting.",
          )}
        </p>

        <Toggle label={t("No chunked download by default")} hint={t("Stream everything through ffmpeg")} checked={form.noChunked} onChange={(v) => set("noChunked", v)} />
      </div>

      <div className="card space-y-3 p-5">
        <div className="flex items-center justify-between">
          <div>
            <h2 className="text-sm font-semibold text-slate-200">{t("Extra library folders")}</h2>
            <p className="text-xs text-slate-500">{t("Scanned in addition to the output folder.")}</p>
          </div>
          <button className="btn-ghost px-3 py-2" onClick={() => setPickLib(true)}>
            <FolderPlus className="h-4 w-4" /> {t("Add")}
          </button>
        </div>
        {libDirs.length === 0 ? (
          <p className="text-sm text-slate-500">{t("None added.")}</p>
        ) : (
          <div className="space-y-1.5">
            {libDirs.map((d) => (
              <div key={d} className="flex items-center gap-2 rounded-lg border border-white/[0.06] bg-ink-900/40 px-3 py-2">
                <FolderOpen className="h-4 w-4 shrink-0 text-gold-400/80" />
                <span className="min-w-0 flex-1 truncate font-mono text-xs text-slate-300">{d}</span>
                <button
                  className="rounded-md p-1 text-slate-500 hover:bg-white/[0.06] hover:text-ember-400"
                  onClick={() => set("libraryDirs", libDirs.filter((x) => x !== d))}
                >
                  <Trash2 className="h-3.5 w-3.5" />
                </button>
              </div>
            ))}
          </div>
        )}
      </div>

      <FFmpegInfo ffmpeg={ffmpeg} />

      <UpdateCard />

      <DirPicker open={pickOutput} initial={form.outputPath} onClose={() => setPickOutput(false)} onSelect={(p) => set("outputPath", p)} />
      <DirPicker
        open={pickWork}
        initial={form.workDir || form.outputPath}
        onClose={() => setPickWork(false)}
        onSelect={(p) => set("workDir", p)}
      />
      <DirPicker
        open={pickMovieOutput}
        initial={form.movieOutputPath || form.outputPath}
        onClose={() => setPickMovieOutput(false)}
        onSelect={(p) => set("movieOutputPath", p)}
      />
      <DirPicker
        open={pickLib}
        initial={form.outputPath}
        onClose={() => setPickLib(false)}
        onSelect={(p) => set("libraryDirs", [...new Set([...libDirs, p])])}
      />
    </div>
  );
}

// SaveStatus is the small live indicator that replaces the old Save button:
// it shows the auto-save is in flight, just landed, or failed. Idle shows
// nothing so the header stays quiet once everything is persisted.
function SaveStatus({ state }: { state: SaveState }) {
  const { t } = useI18n();
  if (state === "saving") {
    return (
      <span className="flex shrink-0 items-center gap-1.5 text-xs font-medium text-slate-400">
        <Spinner className="h-3.5 w-3.5" /> {t("Saving…")}
      </span>
    );
  }
  if (state === "saved") {
    return (
      <span className="flex shrink-0 items-center gap-1.5 text-xs font-medium text-emerald-400">
        <Check className="h-3.5 w-3.5" /> {t("Saved")}
      </span>
    );
  }
  if (state === "error") {
    return (
      <span className="flex shrink-0 items-center gap-1.5 text-xs font-medium text-ember-400">
        <TriangleAlert className="h-3.5 w-3.5" /> {t("Save failed")}
      </span>
    );
  }
  return null;
}

function UpdateCard() {
  const { update, refreshUpdate, version, toast } = useApp();
  const { t } = useI18n();
  const [checking, setChecking] = useState(false);
  const [applying, setApplying] = useState(false);

  const check = async () => {
    setChecking(true);
    await refreshUpdate(true);
    setChecking(false);
  };

  const apply = async () => {
    setApplying(true);
    try {
      const r = await api.applyUpdate();
      toast(
        t("Updating to {v} — the app will restart and this tab will reconnect.", { v: r.version }),
        "success",
      );
      // The server re-execs on the same port; the SSE connection reconnects
      // automatically, so we keep the spinner until that happens.
    } catch (e: any) {
      toast(e.message || t("Update failed"), "error");
      setApplying(false);
    }
  };

  return (
    <div className="card p-5">
      <h2 className="mb-3 flex items-center gap-2 text-sm font-semibold text-slate-200">
        <ArrowUpCircle className="h-4 w-4 text-gold-400" /> {t("Software update")}
      </h2>
      <div className="space-y-3 text-sm">
        <div className="flex items-center justify-between gap-3">
          <span className="text-slate-400">{t("Current version")}</span>
          <span className="font-mono text-xs text-slate-300">{version || "—"}</span>
        </div>

        {update?.updateAvailable && (
          <div className="space-y-3 rounded-lg border border-gold-500/25 bg-gold-500/[0.06] p-3">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <span className="font-medium text-gold-200">
                {t("New version {v} available", { v: update.latest || "" })}
              </span>
              {update.releaseUrl && (
                <a
                  href={update.releaseUrl}
                  target="_blank"
                  rel="noreferrer"
                  className="text-xs text-slate-400 underline hover:text-slate-200"
                >
                  {t("Release notes")}
                </a>
              )}
            </div>
            <button className="btn-primary" onClick={apply} disabled={applying}>
              {applying ? <Spinner className="h-4 w-4" /> : <ArrowUpCircle className="h-4 w-4" />}
              {applying ? t("Updating…") : t("Update & restart")}
            </button>
          </div>
        )}

        <div className="flex items-center justify-between gap-3">
          <span className="min-w-0 flex-1 truncate text-xs text-slate-500">
            {update?.updateAvailable
              ? ""
              : update?.note
                ? update.note
                : t("You're on the latest version.")}
          </span>
          <button className="btn-ghost shrink-0 px-3 py-1.5 text-xs" onClick={check} disabled={checking}>
            {checking ? <Spinner className="h-3.5 w-3.5" /> : <RefreshCw className="h-3.5 w-3.5" />}{" "}
            {t("Check for updates")}
          </button>
        </div>
      </div>
    </div>
  );
}

function FFmpegInfo({ ffmpeg }: { ffmpeg: FFmpegStatus }) {
  const { t } = useI18n();
  return (
    <div className="card p-5">
      <h2 className="mb-3 flex items-center gap-2 text-sm font-semibold text-slate-200">
        <Server className="h-4 w-4 text-gold-400" /> {t("System")}
      </h2>
      <div className="space-y-2 text-sm">
        <Row label="ffmpeg" ok={ffmpeg.ffmpegFound} detail={ffmpeg.ffmpegFound ? ffmpeg.ffmpegVersion || ffmpeg.ffmpegPath : t("not found on PATH")} />
        <Row label="ffprobe" ok={ffmpeg.ffprobeFound} detail={ffmpeg.ffprobeFound ? ffmpeg.ffprobePath || "" : t("not found on PATH")} />
      </div>
      <InstallFFmpeg className="mt-3" />
    </div>
  );
}

function Row({ label, ok, detail }: { label: string; ok: boolean; detail?: string }) {
  return (
    <div className="flex items-center gap-3">
      <span className={`h-2 w-2 rounded-full ${ok ? "bg-emerald-400" : "bg-ember-500"}`} />
      <span className="w-16 font-medium text-slate-300">{label}</span>
      <span className="min-w-0 flex-1 truncate font-mono text-xs text-slate-500">{detail}</span>
    </div>
  );
}
