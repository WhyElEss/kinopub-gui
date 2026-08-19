import { useState } from "react";
import { Pause, Play, RefreshCw, Rss, X } from "lucide-react";
import { api, type WatchView } from "../api";
import { useApp } from "../store";
import { useI18n } from "../i18n";
import { relTime } from "../lib/format";
import { PosterImage } from "./ui";

// WatchList shows the series the app follows. An airing show gets its episodes
// over weeks, so the download that queued it only ever covered what had aired
// that day; each of these rows is a standing instruction to keep going.
export function WatchList() {
  const { watches, toast } = useApp();
  const { t } = useI18n();
  const [busy, setBusy] = useState<string | null>(null);

  if (watches.length === 0) return null;

  const checkAll = async () => {
    setBusy("all");
    try {
      await api.checkAllWatches();
      toast(t("Checking for new episodes…"), "info");
    } catch (e: any) {
      toast(e.message || "Error", "error");
    } finally {
      setBusy(null);
    }
  };

  const check = async (w: WatchView) => {
    setBusy(w.id);
    try {
      const r = await api.checkWatch(w.id);
      const n = r.queued?.length ?? 0;
      toast(n > 0 ? t("{n} new episodes queued", { n }) : t("No new episodes yet"), n > 0 ? "success" : "info");
    } catch (e: any) {
      toast(e.message || "Error", "error");
    } finally {
      setBusy(null);
    }
  };

  const setPaused = async (w: WatchView, paused: boolean) => {
    setBusy(w.id);
    try {
      await api.pauseWatch(w.id, paused);
    } catch (e: any) {
      toast(e.message || "Error", "error");
    } finally {
      setBusy(null);
    }
  };

  const unfollow = async (w: WatchView) => {
    setBusy(w.id);
    try {
      await api.unfollowSeries(w.id);
      toast(t("No longer following {title}", { title: w.title || w.url }), "info");
    } catch (e: any) {
      toast(e.message || "Error", "error");
    } finally {
      setBusy(null);
    }
  };

  return (
    <div className="card p-4">
      <div className="flex items-center justify-between gap-3">
        <h2 className="flex items-center gap-2 text-sm font-semibold text-slate-200">
          <Rss className="h-4 w-4 text-gold-300" />
          {t("Following ({n})", { n: watches.length })}
        </h2>
        <button className="btn-ghost px-3 py-1.5 text-xs" onClick={checkAll} disabled={busy !== null}>
          <RefreshCw className="h-3.5 w-3.5" /> {t("Check now")}
        </button>
      </div>
      <p className="mt-1 text-xs text-slate-500">
        {t("New episodes are downloaded automatically with the settings each series was followed with.")}
      </p>

      <ul className="mt-3 space-y-2">
        {watches.map((w) => (
          <li
            key={w.id}
            className="flex items-center gap-3 rounded-xl border border-white/[0.06] bg-ink-900/40 px-3 py-2"
          >
            <PosterImage
              url={w.posterUrl}
              alt={w.title}
              className="hidden aspect-[2/3] w-8 shrink-0 rounded border border-white/[0.08] sm:block"
            />
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-2">
                <span className="truncate text-sm font-medium text-slate-200">{w.title || w.url}</span>
                <span className="chip shrink-0">
                  {w.seasons && w.seasons.length > 0
                    ? w.seasons.map((n) => `S${n}`).join(", ")
                    : t("all seasons")}
                </span>
                {w.paused && <span className="chip shrink-0 text-slate-400">{t("paused")}</span>}
              </div>
              <p className="mt-0.5 truncate text-xs text-slate-500">
                {w.lastError
                  ? w.lastError
                  : w.lastCheck
                    ? `${t("checked")} ${relTime(w.lastCheck, t)}${
                        w.available ? ` · ${t("{done}/{total} episodes", { done: w.downloaded ?? 0, total: w.available })}` : ""
                      }`
                    : t("not checked yet")}
              </p>
            </div>
            <div className="flex shrink-0 items-center gap-1">
              <button
                className="btn-ghost px-2 py-1.5"
                title={t("Check now")}
                onClick={() => check(w)}
                disabled={busy !== null}
              >
                <RefreshCw className="h-3.5 w-3.5" />
              </button>
              <button
                className="btn-ghost px-2 py-1.5"
                title={w.paused ? t("Resume checks") : t("Pause checks")}
                onClick={() => setPaused(w, !w.paused)}
                disabled={busy !== null}
              >
                {w.paused ? <Play className="h-3.5 w-3.5" /> : <Pause className="h-3.5 w-3.5" />}
              </button>
              <button
                className="btn-ghost px-2 py-1.5 text-ember-400"
                title={t("Stop following")}
                onClick={() => unfollow(w)}
                disabled={busy !== null}
              >
                <X className="h-3.5 w-3.5" />
              </button>
            </div>
          </li>
        ))}
      </ul>
    </div>
  );
}
