import { useEffect, useMemo, useState } from "react";
import clsx from "clsx";
import {
  Check,
  ChevronDown,
  ChevronRight,
  Download,
  Eye,
  FolderOpen,
  HardDrive,
  Loader2,
  Mic2,
  Play,
  Rss,
  Star,
} from "lucide-react";
import {
  api,
  type DiscoverDetail,
  type DiscoverItem,
  type StartRequest,
  imgURL,
} from "../api";
import { buildAudioSpecs } from "../audio";
import { useApp } from "../store";
import { useI18n } from "../i18n";
import { Field, Modal, PosterImage } from "./ui";
import { Ratings } from "./Ratings";
import { Player } from "./Player";
import { DirPicker } from "./DirPicker";
import { OutputTemplates, previewPath, type TemplateValues } from "./OutputTemplates";

const QUALITIES = ["", "max", "2160p", "1080p", "720p", "480p", "360p"];

// Last download voiceover choice, remembered across titles/seasons so the same
// dub is pre-selected next time. Stored as normalized dub names (see normDub).
const AUDIO_PREF_KEY = "kp.download.audioPref";

function epKey(season: number, episode: number) {
  return `S${season}E${episode}`;
}

// normDub strips the leading "NN. " index that kino.pub prepends per title (it
// differs between titles/seasons) so a dub like "02. Дубляж. Невафильм (RUS)"
// matches the same studio elsewhere. Used to carry the chosen voiceover forward.
function normDub(label: string): string {
  return label.replace(/^\s*\d+\.\s*/, "").trim().toLowerCase();
}

function readAudioPref(): string[] {
  try {
    const arr = JSON.parse(localStorage.getItem(AUDIO_PREF_KEY) || "[]");
    return Array.isArray(arr) ? arr.filter((x) => typeof x === "string") : [];
  } catch {
    return [];
  }
}

function writeAudioPref(dubs: string[]) {
  try {
    if (dubs.length) localStorage.setItem(AUDIO_PREF_KEY, JSON.stringify(dubs));
    else localStorage.removeItem(AUDIO_PREF_KEY);
  } catch {
    /* storage unavailable — preference just won't persist */
  }
}

export function TitleDetail({
  id,
  onClose,
  onPick,
  onStarted,
}: {
  id: string;
  onClose: () => void;
  onPick: (item: DiscoverItem) => void;
  onStarted: () => void;
}) {
  const { settings, ffmpeg, toast, watches } = useApp();
  const { t } = useI18n();

  const [detail, setDetail] = useState<DiscoverDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [similar, setSimilar] = useState<DiscoverItem[]>([]);

  const [quality, setQuality] = useState(settings.quality);
  // Selected озвучка labels. Empty set → keep every track.
  const [audioSel, setAudioSel] = useState<Set<string>>(new Set());
  // True when a remembered voiceover existed but isn't available here, so the
  // user is prompted to pick another.
  const [audioPrefMissing, setAudioPrefMissing] = useState(false);
  // Episode keys already downloaded for this title (key → resolution label).
  const [downloaded, setDownloaded] = useState<Map<string, string>>(new Map());
  // Selected episode keys (serials). null until detail loads.
  const [epSel, setEpSel] = useState<Set<string> | null>(null);
  const [openSeasons, setOpenSeasons] = useState<Set<number>>(new Set());
  const [starting, setStarting] = useState(false);
  const [following, setFollowing] = useState(false);
  // Destination for this download: folder plus the two path templates, seeded
  // from the saved defaults (the movie pair for films, the series pair
  // otherwise) once the detail — and with it the item's type — is known.
  const [outputPath, setOutputPath] = useState(settings.outputPath);
  const [dirTmpl, setDirTmpl] = useState(settings.dirTemplate);
  const [nameTmpl, setNameTmpl] = useState(settings.nameTemplate);
  const [showDest, setShowDest] = useState(false);
  const [pickDir, setPickDir] = useState(false);
  // When set, the in-app player is open for this title (a serial episode, or the
  // whole title for a movie when season/episode are undefined).
  const [playing, setPlaying] = useState<{ season?: number; episode?: number } | null>(null);

  useEffect(() => {
    let alive = true;
    setLoading(true);
    setError("");
    setDetail(null);
    setEpSel(null);
    setAudioSel(new Set());
    setAudioPrefMissing(false);
    setDownloaded(new Map());
    api
      .discoverItem(id)
      .then((d) => {
        if (!alive) return;
        setDetail(d);
        setQuality(settings.quality);
        // A film and a series want different layouts, so which default pair
        // applies is only known now that the type is loaded.
        const serial = !!(d.seasons && d.seasons.length);
        setOutputPath(serial ? settings.outputPath : settings.movieOutputPath || settings.outputPath);
        setDirTmpl(serial ? settings.dirTemplate : settings.movieDirTemplate);
        setNameTmpl(serial ? settings.nameTemplate : settings.movieNameTemplate);
        const keys = (d.seasons || []).flatMap((s) => s.episodes.map((e) => epKey(e.season, e.episode)));
        setEpSel(new Set(keys));
        setOpenSeasons(new Set((d.seasons || []).map((s) => s.number)));
        // Default to ALL voiceovers selected (shown highlighted). Carry the last
        // chosen voiceover forward when it's available: pre-select only those; if
        // a remembered choice exists but isn't here, keep all selected and flag it.
        const allLabels = new Set(d.audios.map((a) => a.label));
        const prefs = readAudioPref();
        if (prefs.length && d.audios.length) {
          const matched = d.audios.filter((a) => prefs.includes(normDub(a.label)));
          if (matched.length) {
            setAudioSel(new Set(matched.map((a) => a.label)));
            setAudioPrefMissing(false);
          } else {
            setAudioSel(allLabels);
            setAudioPrefMissing(true);
          }
        } else {
          setAudioSel(allLabels);
        }
      })
      .catch((e) => alive && setError(e.message || "Failed to load"))
      .finally(() => alive && setLoading(false));
    api
      .discoverSimilar(id)
      .then((r) => alive && setSimilar(r.items || []))
      .catch(() => {});
    api
      .libraryDownloaded(id)
      .then((r) => alive && setDownloaded(new Map((r.episodes || []).map((e) => [e.key, e.resolution || ""]))))
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, [id]);

  const allEpKeys = useMemo(
    () => (detail?.seasons || []).flatMap((s) => s.episodes.map((e) => epKey(e.season, e.episode))),
    [detail],
  );

  const toggleAudio = (label: string) =>
    setAudioSel((cur) => {
      const next = new Set(cur);
      next.has(label) ? next.delete(label) : next.add(label);
      return next;
    });

  const allAudioOn = !!detail && detail.audios.length > 0 && audioSel.size === detail.audios.length;
  const toggleAllAudio = () =>
    setAudioSel(() => (allAudioOn ? new Set() : new Set((detail?.audios || []).map((a) => a.label))));

  const toggleEpisode = (key: string) =>
    setEpSel((cur) => {
      const next = new Set(cur ?? []);
      next.has(key) ? next.delete(key) : next.add(key);
      return next;
    });

  const toggleSeason = (season: number) =>
    setEpSel((cur) => {
      const next = new Set(cur ?? []);
      const eps = detail?.seasons?.find((s) => s.number === season)?.episodes ?? [];
      const allOn = eps.length > 0 && eps.every((e) => next.has(epKey(e.season, e.episode)));
      eps.forEach((e) => (allOn ? next.delete(epKey(e.season, e.episode)) : next.add(epKey(e.season, e.episode))));
      return next;
    });

  const toggleSeasonOpen = (season: number) =>
    setOpenSeasons((cur) => {
      const next = new Set(cur);
      next.has(season) ? next.delete(season) : next.add(season);
      return next;
    });

  const isSerial = !!detail?.seasons && detail.seasons.length > 0;
  // The standing "keep downloading this one" instruction for this title, if any.
  const followed = watches.find((w) => w.id === detail?.id);
  const selectedCount = isSerial ? epSel?.size ?? 0 : 1;

  // Values the path preview substitutes. The first selected episode (or 1×1 for
  // a film) stands in for the whole set, so the user sees a real file name.
  const templateValues: TemplateValues = useMemo(() => {
    const firstKey = [...(epSel ?? [])].sort()[0];
    const first = (detail?.seasons || [])
      .flatMap((s) => s.episodes)
      .find((e) => epKey(e.season, e.episode) === firstKey);
    return {
      title: detail?.fullTitle || detail?.title || "",
      ru: detail?.title || "",
      original: detail?.originalTitle || "",
      year: detail?.year || 0,
      id: detail?.id || "",
      season: first?.season ?? 1,
      episode: first?.episode ?? 1,
      epTitle: first?.title || "",
      quality: quality || detail?.qualities?.[0] || "1080p",
    };
  }, [detail, epSel, quality]);

  // The download request for this title as currently configured. Shared by the
  // Download button and by Follow, so a followed series is downloaded with
  // exactly the settings shown here. Returns null (after a toast) when the form
  // is not ready.
  const buildRequest = (): Partial<StartRequest> | null => {
    if (!detail) return null;
    if (!ffmpeg.ffmpegFound) {
      toast(t("ffmpeg not found — install it to download"), "error");
      return null;
    }
    const chosenAudios = detail.audios.filter((a) => audioSel.has(a.label));
    if (detail.audios.length > 0 && chosenAudios.length === 0) {
      toast(t("Select at least one voiceover"), "error");
      return null;
    }
    // When every track is selected, send no filter (keep all). Otherwise build
    // exact per-track rules that match the chosen variants and nothing else
    // (see buildAudioSpecs).
    const audioSpecs = buildAudioSpecs(detail.audios, chosenAudios);
    // Remember this voiceover choice for the next title/season.
    writeAudioPref(chosenAudios.map((a) => normDub(a.label)));
    const seedTitles = Object.fromEntries(
      (detail.seasons || []).flatMap((s) => s.episodes.map((e) => [epKey(e.season, e.episode), e.title])),
    );
    return {
      url: detail.itemUrl,
      outputPath,
      dirTemplate: dirTmpl,
      nameTemplate: nameTmpl,
      quality,
      container: settings.container,
      concurrency: settings.concurrency,
      retries: settings.retries,
      minIntervalMs: settings.minIntervalMs,
      proxy: settings.proxy,
      seasons: "",
      episodes: "",
      audio: "",
      audioSpecs,
      audioMenu: false,
      force: false,
      noChunked: settings.noChunked,
      dryRun: false,
      ffmpegArgs: "",
      ffmpegPath: "",
      userAgent: "",
      verbosity: settings.verbosity,
      seedTitle: detail.title,
      seedPoster: detail.poster,
      seedTitles,
    };
  };

  const start = async () => {
    if (!detail) return;
    if (isSerial && (!epSel || epSel.size === 0)) {
      toast(t("Select at least one episode"), "error");
      return;
    }
    const req = buildRequest();
    if (!req) return;
    setStarting(true);
    try {
      await api.startJob({ ...req, episodeKeys: isSerial && epSel ? [...epSel] : undefined });
      toast(t("Download started"), "success");
      onStarted();
    } catch (e: any) {
      toast(e.message || t("Failed to start"), "error");
    } finally {
      setStarting(false);
    }
  };

  // Seasons a follow covers: the ones the current selection touches, or every
  // season — present and future — when the selection spans all of them, which is
  // what an airing show needs once it rolls over into the next season.
  const followSeasons = (): number[] => {
    const all = (detail?.seasons || []).map((s) => s.number);
    const picked = new Set(
      (detail?.seasons || [])
        .filter((s) => s.episodes.some((e) => epSel?.has(epKey(e.season, e.episode))))
        .map((s) => s.number),
    );
    if (picked.size === 0 || picked.size === all.length) return [];
    return [...picked];
  };

  const toggleFollow = async () => {
    if (!detail) return;
    setFollowing(true);
    try {
      if (followed) {
        await api.unfollowSeries(followed.id);
        toast(t("No longer following {title}", { title: detail.title }), "info");
        return;
      }
      const req = buildRequest();
      if (!req) return;
      // The server checks straight away, so the answer already says what it
      // queued — following a series that is behind starts downloading now.
      const w = await api.followSeries({ ...req, watchSeasons: followSeasons() });
      const queued = w.lastQueued?.length ?? 0;
      toast(
        queued > 0
          ? t("Following — {n} episodes queued", { n: queued })
          : t("Following — new episodes will be downloaded automatically"),
        "success",
      );
      if (queued > 0) onStarted();
    } catch (e: any) {
      toast(e.message || "Error", "error");
    } finally {
      setFollowing(false);
    }
  };

  return (
    <>
    <Modal open onClose={onClose} wide title={detail?.title || (loading ? t("Loading…") : t("Title"))}>
      {loading ? (
        <div className="flex items-center justify-center py-16 text-slate-400">
          <Loader2 className="h-6 w-6 animate-spin" />
        </div>
      ) : error ? (
        <p className="py-8 text-center text-sm text-ember-400">{error}</p>
      ) : detail ? (
        <div className="space-y-5">
          <div className="flex flex-col gap-4 sm:flex-row">
            <PosterImage url={detail.poster} alt={detail.title} className="h-48 w-32 shrink-0 rounded-xl" />
            <div className="min-w-0 flex-1 space-y-2">
              {detail.originalTitle && <p className="-mt-1 text-sm text-slate-400">{detail.originalTitle}</p>}
              <div className="flex flex-wrap items-center gap-2 text-xs text-slate-400">
                {detail.year > 0 && <span className="chip">{detail.year}</span>}
                {detail.isSerial && <span className="chip">{t("Series")}</span>}
                {detail.durationMin ? <span className="chip">{detail.durationMin} {t("min")}</span> : null}
                <Ratings item={detail} className="ml-1" />
              </div>
              {detail.genres && detail.genres.length > 0 && (
                <p className="text-xs font-medium text-emerald-300/90">{detail.genres.join(", ")}</p>
              )}
              {detail.director && <MetaRow label={t("Director")} value={detail.director} />}
              {detail.cast && <MetaRow label={t("Cast")} value={detail.cast} />}
              {detail.countries && detail.countries.length > 0 && (
                <MetaRow label={t("Country")} value={detail.countries.join(", ")} />
              )}
              {detail.plot && <p className="max-h-32 overflow-y-auto text-sm leading-relaxed text-slate-300">{detail.plot}</p>}
            </div>
          </div>

          {/* Озвучки */}
          <div>
            <h3 className="mb-2 flex items-center gap-2 text-sm font-semibold text-slate-200">
              <Mic2 className="h-4 w-4 text-accent-400" /> {t("Voiceover")}
              <span className="text-xs font-normal text-slate-500">
                {allAudioOn
                  ? t("(all selected)")
                  : audioSel.size === 0
                    ? t("(none)")
                    : t("({n} selected)", { n: audioSel.size })}
              </span>
              {detail.audios.length > 1 && (
                <button
                  onClick={toggleAllAudio}
                  className="ml-auto text-xs font-normal text-accent-300 hover:text-accent-200"
                >
                  {allAudioOn ? t("Deselect all") : t("Select all")}
                </button>
              )}
            </h3>
            {audioPrefMissing && detail.audios.length > 0 && (
              <p className="mb-2 text-xs text-accent-300/90">
                {t("Your last voiceover isn't available here — pick another.")}
              </p>
            )}
            {detail.audios.length === 0 ? (
              <p className="text-xs text-slate-500">{t("Voiceover list appears after sign-in / for available titles.")}</p>
            ) : (
              <div className="flex flex-wrap gap-2">
                {detail.audios.map((a) => {
                  const on = audioSel.has(a.label);
                  return (
                    <button
                      key={a.label}
                      onClick={() => toggleAudio(a.label)}
                      className={`chip transition ${
                        on
                          ? "border-accent-500/40 bg-accent-500/[0.14] text-accent-200"
                          : "text-slate-300 hover:bg-white/[0.06]"
                      }`}
                    >
                      {on && <Check className="h-3 w-3" />}
                      {a.label}
                    </button>
                  );
                })}
              </div>
            )}
          </div>

          {/* Episodes (serials) */}
          {isSerial && epSel && (
            <div>
              <div className="mb-2 flex items-center justify-between">
                <h3 className="text-sm font-semibold text-slate-200">
                  {t("Episodes")}{" "}
                  <span className="text-xs font-normal text-slate-500">
                    {t("{n} of {m} selected", { n: epSel.size, m: allEpKeys.length })}
                  </span>
                </h3>
                <div className="flex gap-2 text-xs">
                  {downloaded.size > 0 && (
                    <button
                      className="text-slate-400 hover:text-accent-300"
                      title={t("Select only episodes not yet downloaded")}
                      onClick={() => setEpSel(new Set(allEpKeys.filter((k) => !downloaded.has(k))))}
                    >
                      {t("Only missing")}
                    </button>
                  )}
                  <button className="text-slate-400 hover:text-accent-300" onClick={() => setEpSel(new Set(allEpKeys))}>
                    {t("Select all")}
                  </button>
                  <button className="text-slate-400 hover:text-accent-300" onClick={() => setEpSel(new Set())}>
                    {t("Deselect all")}
                  </button>
                </div>
              </div>
              <div className="max-h-64 space-y-1.5 overflow-y-auto pr-1">
                {detail.seasons!.map((s) => {
                  const open = openSeasons.has(s.number);
                  const total = s.episodes.length;
                  const watched = s.episodes.filter((e) => e.watched).length;
                  const dled = s.episodes.filter((e) => downloaded.has(epKey(e.season, e.episode))).length;
                  const sel = s.episodes.filter((e) => epSel.has(epKey(e.season, e.episode))).length;
                  const allSel = total > 0 && sel === total;
                  const someSel = sel > 0 && !allSel;
                  return (
                    <div key={s.number} className="rounded-lg border border-white/[0.06] bg-ink-900/40">
                      <div className="flex items-center gap-2.5 px-3 py-2">
                        <button
                          onClick={() => toggleSeason(s.number)}
                          title={t("Toggle season")}
                          className={`grid h-4 w-4 shrink-0 place-items-center rounded border transition ${
                            allSel
                              ? "border-accent-500 bg-accent-500"
                              : someSel
                                ? "border-accent-500 bg-accent-500/30"
                                : "border-white/25 hover:border-white/40"
                          }`}
                        >
                          {allSel && <Check className="h-3 w-3 text-ink-950" strokeWidth={3} />}
                          {someSel && <span className="h-[2px] w-2 rounded bg-accent-300" />}
                        </button>
                        <button
                          onClick={() => toggleSeasonOpen(s.number)}
                          className="flex flex-1 items-center gap-1.5 text-left text-sm font-medium text-slate-200"
                        >
                          {open ? <ChevronDown className="h-4 w-4 text-slate-400" /> : <ChevronRight className="h-4 w-4 text-slate-400" />}
                          {t("Season {n}", { n: s.number })}
                        </button>
                        <span className="flex items-center gap-2 text-xs text-slate-500">
                          {dled > 0 && (
                            <span className="inline-flex items-center gap-0.5 text-accent-400/70" title={t("Downloaded")}>
                              <HardDrive className="h-3 w-3" /> {dled}
                            </span>
                          )}
                          {watched > 0 && (
                            <span className="inline-flex items-center gap-0.5 text-emerald-500/70" title={t("Watched")}>
                              <Eye className="h-3 w-3" /> {watched}
                            </span>
                          )}
                          <span className={allSel ? "text-accent-300" : ""}>
                            {sel}/{total}
                          </span>
                        </span>
                      </div>
                      {open && (
                        <div className="border-t border-white/[0.05]">
                          {s.episodes.map((e) => {
                            const key = epKey(e.season, e.episode);
                            const on = epSel.has(key);
                            const dl = downloaded.has(key);
                            const dlRes = downloaded.get(key);
                            return (
                              <div
                                key={key}
                                className={`group flex items-center text-sm transition ${
                                  on ? "bg-accent-500/[0.10]" : "hover:bg-white/[0.03]"
                                }`}
                              >
                                <button
                                  onClick={() => toggleEpisode(key)}
                                  title={e.watched ? t("Watched") : undefined}
                                  className="flex flex-1 items-center gap-2.5 px-3 py-1.5 text-left"
                                >
                                  <span
                                    className={`grid h-6 w-7 shrink-0 place-items-center rounded text-xs font-semibold ${
                                      on ? "bg-accent-500/25 text-accent-200" : "bg-white/[0.05] text-slate-400"
                                    }`}
                                  >
                                    {e.episode}
                                  </span>
                                  <span className={`flex-1 truncate ${e.watched ? "text-slate-500" : on ? "text-slate-100" : "text-slate-400"}`}>
                                    {e.title}
                                  </span>
                                  {dl && (
                                    <span
                                      className="shrink-0 text-accent-400/80"
                                      title={dlRes ? t("Downloaded · {res}", { res: dlRes }) : t("Downloaded")}
                                    >
                                      <HardDrive className="h-3.5 w-3.5" />
                                    </span>
                                  )}
                                  {e.watched && <Eye className="h-3.5 w-3.5 shrink-0 text-emerald-500/70" />}
                                  {on && <Check className="h-3.5 w-3.5 shrink-0 text-accent-300" />}
                                </button>
                                <button
                                  onClick={() => setPlaying({ season: e.season, episode: e.episode })}
                                  title={t("Watch")}
                                  className="mr-2 shrink-0 rounded-md p-1.5 text-accent-400/80 transition hover:bg-accent-500/15 hover:text-accent-200"
                                >
                                  <Play className="h-4 w-4" />
                                </button>
                              </div>
                            );
                          })}
                        </div>
                      )}
                    </div>
                  );
                })}
              </div>
            </div>
          )}

          {/* Destination: folder + path templates for THIS download */}
          <div className="space-y-3 border-t border-white/[0.05] pt-4">
            <button
              type="button"
              className="flex items-center gap-2 text-sm font-semibold text-slate-200"
              onClick={() => setShowDest((v) => !v)}
            >
              {showDest ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
              {t("Where to save")}
              {!showDest && (
                <span className="truncate font-mono text-[11px] font-normal text-slate-500">
                  {previewPath(outputPath, dirTmpl, nameTmpl, templateValues, settings.container === "mp4" ? "mp4" : "mkv")}
                </span>
              )}
            </button>
            {showDest && (
              <div className="space-y-3">
                <Field label={t("Output folder")}>
                  <button className="input flex items-center gap-2 text-left" onClick={() => setPickDir(true)} type="button">
                    <FolderOpen className="h-4 w-4 shrink-0 text-accent-400" />
                    <span className="truncate font-mono text-xs">{outputPath || t("Choose…")}</span>
                  </button>
                </Field>
                <OutputTemplates
                  dirTemplate={dirTmpl}
                  nameTemplate={nameTmpl}
                  onChange={(dir, name) => {
                    setDirTmpl(dir);
                    setNameTmpl(name);
                  }}
                  outputPath={outputPath}
                  values={templateValues}
                  container={settings.container}
                  defaults={{
                    dir: isSerial ? settings.dirTemplate : settings.movieDirTemplate,
                    name: isSerial ? settings.nameTemplate : settings.movieNameTemplate,
                  }}
                />
              </div>
            )}
          </div>

          {/* Download bar */}
          <div className="flex flex-wrap items-center gap-3 border-t border-white/[0.05] pt-4">
            <select className="input w-auto" value={quality} onChange={(e) => setQuality(e.target.value)}>
              {["", "max", ...(detail.qualities?.length ? detail.qualities : QUALITIES.filter((q) => q && q !== "max"))].map((q) => (
                <option key={q} value={q}>
                  {q === "" ? t("Auto (optimal)") : q === "max" ? t("Maximum") : q}
                </option>
              ))}
            </select>
            <button className="btn-primary" onClick={start} disabled={starting || (isSerial && selectedCount === 0)}>
              {starting ? <Loader2 className="h-4 w-4 animate-spin" /> : <Download className="h-4 w-4" />}
              {isSerial ? t("Download ({n})", { n: selectedCount }) : t("Download")}
            </button>
            {isSerial && (
              <button
                className={clsx("btn-ghost", followed && "text-accent-300")}
                onClick={toggleFollow}
                disabled={following}
                title={
                  followed
                    ? t("Stop downloading new episodes of this series automatically")
                    : t("Download new episodes of the selected seasons as they appear on kino.pub")
                }
              >
                {following ? <Loader2 className="h-4 w-4 animate-spin" /> : <Rss className="h-4 w-4" />}
                {followed ? t("Following") : t("Follow")}
              </button>
            )}
            {!isSerial && (
              <button
                className="inline-flex items-center gap-2 rounded-xl bg-emerald-500/90 px-4 py-2 text-sm font-semibold text-ink-950 transition hover:bg-emerald-400"
                onClick={() => setPlaying({})}
              >
                <Play className="h-4 w-4" /> {t("Watch")}
              </button>
            )}
            {!ffmpeg.ffmpegFound && (
              <span className="text-xs text-ember-400">{t("ffmpeg not detected — required to download")}</span>
            )}
          </div>

          {/* Similar */}
          {similar.length > 0 && (
            <div className="border-t border-white/[0.05] pt-4">
              <h3 className="mb-2 text-sm font-semibold text-slate-200">{t("Similar")}</h3>
              <div className="flex gap-3 overflow-x-auto pb-1">
                {similar.slice(0, 12).map((it) => (
                  <button
                    key={it.id}
                    onClick={() => onPick(it)}
                    className="w-24 shrink-0 text-left"
                    title={it.title}
                  >
                    <img
                      src={imgURL(it.poster)}
                      alt={it.title}
                      loading="lazy"
                      className="h-32 w-24 rounded-lg object-cover"
                      onError={(e) => ((e.currentTarget as HTMLImageElement).style.visibility = "hidden")}
                    />
                    <p className="mt-1 truncate text-xs text-slate-400">{it.title}</p>
                  </button>
                ))}
              </div>
            </div>
          )}
        </div>
      ) : null}
    </Modal>
    {playing && detail && (
      <Player
        key={`${detail.id}-${playing.season ?? ""}-${playing.episode ?? ""}`}
        id={detail.id}
        season={playing.season}
        episode={playing.episode}
        title={detail.title}
        episodes={(detail.seasons || []).flatMap((s) =>
          s.episodes.map((e) => ({ season: e.season, episode: e.episode, title: e.title })),
        )}
        onChangeEpisode={(season, episode) => setPlaying({ season, episode })}
        onClose={() => setPlaying(null)}
      />
    )}
    <DirPicker
      open={pickDir}
      initial={outputPath}
      onClose={() => setPickDir(false)}
      onSelect={(p) => setOutputPath(p)}
    />
    </>
  );
}

// MetaRow renders a labelled metadata line (Director / Cast / Country).
function MetaRow({ label, value }: { label: string; value: string }) {
  return (
    <p className="text-xs leading-relaxed text-slate-400">
      <span className="font-semibold text-slate-300">{label}: </span>
      {value}
    </p>
  );
}
