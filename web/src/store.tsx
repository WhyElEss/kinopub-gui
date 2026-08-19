import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import {
  api,
  type FFmpegStatus,
  type JobView,
  type KPStatus,
  type KPUser,
  type Settings,
  type Snapshot,
  type UpdateStatus,
  type WatchView,
} from "./api";
import { applyTheme, normalizeTheme } from "./theme";
import {
  DEFAULT_DIR_TEMPLATE,
  DEFAULT_MOVIE_DIR_TEMPLATE,
  DEFAULT_MOVIE_NAME_TEMPLATE,
  DEFAULT_NAME_TEMPLATE,
} from "./components/OutputTemplates";

export interface Toast {
  id: number;
  kind: "info" | "success" | "error";
  message: string;
}

interface AppContextValue {
  connected: boolean;
  version: string;
  jobs: JobView[];
  // Series the app follows, re-checking kino.pub for episodes that aired since.
  watches: WatchView[];
  kpauth: KPStatus;
  kpUser: KPUser | null;
  kpUserError: boolean;
  ffmpeg: FFmpegStatus;
  // Whether the SERVER can open a file in a desktop app. Combined with
  // pageIsLocal below to decide whether "Open" is worth offering at all.
  canOpenFiles: boolean;
  settings: Settings;
  settingsLoaded: boolean;
  update: UpdateStatus | null;
  refreshUpdate: (force?: boolean) => Promise<void>;
  ffmpegInstall: { supported: boolean; source?: string };
  // Which job cards have their episode list open, by job id. Held here because
  // the card is remounted whenever a job moves between the active and finished
  // lists, and the page itself unmounts on navigation — both would otherwise
  // discard a list the user opened. Undefined = untouched, follow the default.
  epsOpen: Record<string, boolean>;
  toggleEps: (jobId: string, open: boolean) => void;
  setSettingsLocal: (s: Settings) => void;
  // Persists just the theme; the switcher does not own the rest of the form.
  saveTheme: (theme: string) => Promise<void>;
  setKpAuthLocal: (a: KPStatus) => void;
  refresh: () => void;
  toasts: Toast[];
  toast: (message: string, kind?: Toast["kind"]) => void;
  dismissToast: (id: number) => void;
}

const AppContext = createContext<AppContextValue | null>(null);

const emptyKpAuth: KPStatus = { loggedIn: false, pending: false };
const emptyFFmpeg: FFmpegStatus = { ffmpegFound: false, ffprobeFound: false };
const emptySettings: Settings = {
  outputPath: "",
  movieOutputPath: "",
  workDir: "",
  dirTemplate: DEFAULT_DIR_TEMPLATE,
  nameTemplate: DEFAULT_NAME_TEMPLATE,
  movieDirTemplate: DEFAULT_MOVIE_DIR_TEMPLATE,
  movieNameTemplate: DEFAULT_MOVIE_NAME_TEMPLATE,
  quality: "1080p",
  container: "mkv",
  concurrency: 2,
  retries: 5,
  minIntervalMs: 0,
  proxy: "",
  verbosity: "normal",
  noChunked: false,
  theme: "auto",
  libraryDirs: null,
  maxActiveJobs: 0,
  watchIntervalMinutes: 180,
};

function sortJobs(jobs: JobView[]): JobView[] {
  return [...jobs].sort(
    (a, b) => new Date(b.createdAt).getTime() - new Date(a.createdAt).getTime(),
  );
}

export function AppProvider({ children }: { children: ReactNode }) {
  const [connected, setConnected] = useState(false);
  const [version, setVersion] = useState("");
  const [jobs, setJobs] = useState<JobView[]>([]);
  const [watches, setWatches] = useState<WatchView[]>([]);
  const [kpauth, setKpAuth] = useState<KPStatus>(emptyKpAuth);
  const [kpUser, setKpUser] = useState<KPUser | null>(null);
  const [kpUserError, setKpUserError] = useState(false);
  const [ffmpeg, setFFmpeg] = useState<FFmpegStatus>(emptyFFmpeg);
  const [canOpenFiles, setCanOpenFiles] = useState(false);
  const [settings, setSettings] = useState<Settings>(emptySettings);
  const [settingsLoaded, setSettingsLoaded] = useState(false);
  const [update, setUpdate] = useState<UpdateStatus | null>(null);
  const [ffmpegInstall, setFfmpegInstall] = useState<{ supported: boolean; source?: string }>({
    supported: false,
  });
  const [epsOpen, setEpsOpen] = useState<Record<string, boolean>>({});
  const [toasts, setToasts] = useState<Toast[]>([]);
  const toastSeq = useRef(0);
  // The version this tab first loaded with. After a self-update the server
  // re-execs and the SSE reconnects with a snapshot carrying the NEW version,
  // while the tab still runs the old JS/CSS bundle — a mismatch. When we see the
  // version change, reload once so the fresh bundle is served (see applySnapshot).
  const loadedVersion = useRef<string>("");

  const refreshUpdate = (force = false): Promise<void> =>
    api
      .checkUpdate(force)
      .then((u) => setUpdate(u))
      .catch(() => {});

  const toast = (message: string, kind: Toast["kind"] = "info") => {
    const id = ++toastSeq.current;
    setToasts((t) => [...t, { id, kind, message }]);
    window.setTimeout(() => {
      setToasts((t) => t.filter((x) => x.id !== id));
    }, kind === "error" ? 6500 : 4000);
  };
  const dismissToast = (id: number) => setToasts((t) => t.filter((x) => x.id !== id));

  const applySnapshot = (snap: Snapshot) => {
    // Reload the tab when the backend version changes under us (self-update
    // restart): the first snapshot records the baseline, any later change forces
    // a fresh load so we never run the old bundle against the new server. The
    // hash route is preserved, so the user stays on the same page.
    if (snap.version) {
      if (!loadedVersion.current) {
        loadedVersion.current = snap.version;
      } else if (snap.version !== loadedVersion.current) {
        window.location.reload();
        return;
      }
    }
    setVersion(snap.version);
    setJobs(sortJobs(snap.jobs || []));
    setWatches(snap.watches || []);
    setKpAuth(snap.kpauth || emptyKpAuth);
    setFFmpeg(snap.ffmpeg || emptyFFmpeg);
    setCanOpenFiles(!!snap.canOpenFiles);
    setSettings(snap.settings || emptySettings);
    setSettingsLoaded(true);
  };

  const refresh = () => {
    api.state().then(applySnapshot).catch(() => {});
  };

  useEffect(() => {
    let es: EventSource | null = null;
    let stopped = false;
    let retry: number | undefined;

    const connect = () => {
      if (stopped) return;
      es = new EventSource("/api/events");
      es.onopen = () => {
        setConnected(true);
        // Re-check on every (re)connect. After a self-update restart this runs
        // against the fresh process (empty cache), so the banner clears.
        refreshUpdate();
        api
          .deps()
          .then((d) => setFfmpegInstall({ supported: d.installSupported, source: d.source }))
          .catch(() => {});
      };
      es.onerror = () => {
        setConnected(false);
        es?.close();
        retry = window.setTimeout(connect, 1500);
      };
      es.onmessage = (ev) => {
        if (!ev.data) return;
        let parsed: { type: string; data: unknown };
        try {
          parsed = JSON.parse(ev.data);
        } catch {
          return;
        }
        switch (parsed.type) {
          case "snapshot":
            applySnapshot(parsed.data as Snapshot);
            break;
          case "job": {
            const job = parsed.data as JobView;
            setJobs((cur) => {
              const idx = cur.findIndex((j) => j.id === job.id);
              if (idx === -1) return sortJobs([...cur, job]);
              const next = cur.slice();
              next[idx] = job;
              return next;
            });
            break;
          }
          case "watches":
            setWatches((parsed.data as WatchView[]) || []);
            break;
          case "job_removed": {
            const id = (parsed.data as { id: string }).id;
            setJobs((cur) => cur.filter((j) => j.id !== id));
            break;
          }
          case "kpauth":
            setKpAuth(parsed.data as KPStatus);
            break;
          case "ffmpeg":
            setFFmpeg(parsed.data as FFmpegStatus);
            break;
          case "settings":
            setSettings(parsed.data as Settings);
            break;
        }
      };
    };

    connect();
    return () => {
      stopped = true;
      if (retry) window.clearTimeout(retry);
      es?.close();
    };
  }, []);

  // Load the account profile (username + subscription) whenever sign-in state
  // flips to logged-in; clear it on logout. Fed by the SSE kpauth updates.
  //
  // If the kino.pub host is unreachable (e.g. VPN off) the fetch fails: surface
  // that as an explicit error state rather than leaving kpUser null, which the
  // UI would otherwise mistake for "no subscription". Keep retrying so the card
  // self-heals once connectivity returns.
  useEffect(() => {
    if (!kpauth.loggedIn) {
      setKpUser(null);
      setKpUserError(false);
      return;
    }
    let alive = true;
    let timer: number | undefined;
    const load = () => {
      api
        .kpUser()
        .then((u) => {
          if (!alive) return;
          setKpUser(u);
          setKpUserError(false);
        })
        .catch(() => {
          if (!alive) return;
          setKpUserError(true);
          timer = window.setTimeout(load, 15000);
        });
    };
    load();
    return () => {
      alive = false;
      if (timer) window.clearTimeout(timer);
    };
  }, [kpauth.loggedIn]);

  // The theme lives in the settings, so it follows the account to any device;
  // applying it here covers both the SSE snapshot and a local switch.
  useEffect(() => {
    applyTheme(normalizeTheme(settings.theme));
  }, [settings.theme]);

  const saveTheme = async (theme: string) => {
    try {
      // settings is fresh here: the context value is rebuilt whenever it changes.
      await api.saveSettings({ ...settings, theme });
    } catch {
      /* the theme is applied locally either way; the next save retries */
    }
  };

  const toggleEps = (jobId: string, open: boolean) =>
    setEpsOpen((cur) => ({ ...cur, [jobId]: open }));

  const value = useMemo<AppContextValue>(
    () => ({
      connected,
      version,
      canOpenFiles,
      jobs,
      watches,
      kpauth,
      kpUser,
      kpUserError,
      ffmpeg,
      settings,
      settingsLoaded,
      update,
      refreshUpdate,
      ffmpegInstall,
      epsOpen,
      toggleEps,
      setSettingsLocal: setSettings,
      saveTheme,
      setKpAuthLocal: setKpAuth,
      refresh,
      toasts,
      toast,
      dismissToast,
    }),
    [connected, version, canOpenFiles, epsOpen, jobs, watches, kpauth, kpUser, kpUserError, ffmpeg, settings, settingsLoaded, update, ffmpegInstall, toasts],
  );

  return <AppContext.Provider value={value}>{children}</AppContext.Provider>;
}

export function useApp(): AppContextValue {
  const ctx = useContext(AppContext);
  if (!ctx) throw new Error("useApp must be used within AppProvider");
  return ctx;
}
