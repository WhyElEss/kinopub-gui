import { useEffect, useRef, useState, type ReactNode } from "react";
import { KeyRound, Loader2, Lock, ShieldCheck } from "lucide-react";
import { api, setUnauthorizedHandler, type AuthMeta } from "../api";
import { useI18n } from "../i18n";
import { LangSwitcher } from "./LangSwitcher";

// The gate around the whole app.
//
// It sits OUTSIDE AppProvider on purpose: the store opens an SSE stream and
// polls, and none of that should run — or retry every 1.5 s against a 401 —
// before anyone is signed in.
//
// Without a password configured on the server this renders its children
// immediately and is otherwise invisible, which is upstream's behaviour and the
// right one for a loopback install.

type Phase = "checking" | "locked" | "open";

export function AuthGate({ children }: { children: ReactNode }) {
  const [phase, setPhase] = useState<Phase>("checking");
  const [meta, setMeta] = useState<AuthMeta | null>(null);
  const [error, setError] = useState("");
  // Set when a session expires under a page that was already in use. The app
  // stays mounted and the form arrives OVER it: a session that timed out during
  // a film, or with a download form half filled in, must not cost either.
  const [reauth, setReauth] = useState(false);

  useEffect(() => {
    api
      .authMeta()
      .then((m) => {
        setMeta(m);
        setPhase(!m.required || m.signedIn ? "open" : "locked");
      })
      .catch(() => {
        // The server is unreachable, which is not the same as being signed out.
        // Showing a login form here would invite typing a password at a server
        // that cannot check it; the app's own "reconnecting" state says more.
        setPhase("open");
      });

    // Every 401 outside the login routes lands here, plus the store's own check
    // when the event stream drops (EventSource cannot report a status code).
    setUnauthorizedHandler(() => {
      setError("");
      setReauth(true);
      api.authMeta().then(setMeta).catch(() => {});
    });
    return () => setUnauthorizedHandler(null);
  }, []);

  if (phase === "checking") {
    return (
      <div className="grid min-h-screen place-items-center bg-ink-950">
        <Loader2 className="h-6 w-6 animate-spin text-slate-500" />
      </div>
    );
  }

  if (phase === "locked") {
    return (
      <LoginScreen
        meta={meta}
        error={error}
        setError={setError}
        onSignedIn={() => {
          setError("");
          // Nothing was mounted behind this, so a reload is the cleanest way
          // in: the store, the event stream and every page start against a
          // session that now exists.
          window.location.reload();
        }}
      />
    );
  }

  return (
    <>
      {children}
      {reauth && (
        <div className="fixed inset-0 z-50 overflow-y-auto bg-ink-950/85 backdrop-blur-sm">
          <LoginScreen
            meta={meta}
            error={error}
            setError={setError}
            resumed
            onSignedIn={() => {
              // No reload: the point of the overlay is that what is behind it
              // survives. The event stream reconnects on its own retry, and
              // nothing is replayed — a PUT sent again unasked is exactly the
              // change nobody made twice.
              setError("");
              setReauth(false);
            }}
          />
        </div>
      )}
    </>
  );
}

function LoginScreen({
  meta,
  error,
  setError,
  onSignedIn,
  resumed,
}: {
  meta: AuthMeta | null;
  error: string;
  setError: (v: string) => void;
  onSignedIn: () => void;
  // Rendered over a page that is still there, rather than as the whole screen.
  resumed?: boolean;
}) {
  const { t } = useI18n();
  const [user, setUser] = useState(meta?.user || "admin");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  // The server says whether a code is wanted; a 401 carrying needsTotp turns it
  // on for a session enrolled since this page loaded.
  const [wantsCode, setWantsCode] = useState(!!meta?.totp);
  const passwordRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (meta?.user) setUser(meta.user);
    if (meta?.totp) setWantsCode(true);
  }, [meta]);

  // No autofocus. On iOS it moves the zoom-on-focus problem to page load, and a
  // form this rarely used is better opened deliberately.

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      await api.login(user, password, code);
      onSignedIn();
    } catch (err: any) {
      setError(err?.message || "Sign-in failed");
      if (err?.needsTotp) setWantsCode(true);
      setPassword("");
      setCode("");
      passwordRef.current?.focus();
    } finally {
      setBusy(false);
    }
  };

  return (
    <div
      className={
        "flex min-h-screen flex-col items-center justify-center px-5 py-10" +
        (resumed ? "" : " bg-ink-950")
      }
    >
      <div className="w-full max-w-sm">
        <div className="mb-6 flex items-center justify-between">
          <span className="flex items-center gap-2 text-sm font-semibold text-slate-200">
            <Lock className="h-4 w-4 text-accent-400" /> kinopub
          </span>
          <LangSwitcher />
        </div>

        <form onSubmit={submit} className="card space-y-4 p-6">
          <div>
            <h1 className="text-base font-semibold text-slate-100">{t("Sign in")}</h1>
            <p className="mt-1 text-xs text-slate-500">
              {resumed
                ? t("Your session ended. Sign in again — this page is still here.")
                : t("This downloader is reachable from the internet. Sign in to continue.")}
            </p>
          </div>

          {/* A real <label> for each field: a placeholder is gone the moment
              anything is typed, and this form is used rarely enough for the
              second-factor field to need one. */}
          <div>
            <label className="label" htmlFor="auth-user">
              {t("Username")}
            </label>
            <input
              id="auth-user"
              className="input login-input"
              autoComplete="username"
              value={user}
              onChange={(e) => setUser(e.target.value)}
            />
          </div>

          <div>
            <label className="label" htmlFor="auth-password">
              {t("Password")}
            </label>
            <input
              id="auth-password"
              ref={passwordRef}
              type="password"
              className="input login-input"
              autoComplete="current-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </div>

          {wantsCode && (
            <div>
              <label className="label" htmlFor="auth-totp">
                {t("Authenticator code")}
              </label>
              <input
                id="auth-totp"
                className="input login-input"
                inputMode="numeric"
                autoComplete="one-time-code"
                maxLength={6}
                placeholder="000000"
                value={code}
                onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
              />
            </div>
          )}

          {error && (
            <p role="alert" className="text-sm text-ember-400">
              {error}
            </p>
          )}

          <button type="submit" className="btn-primary w-full" disabled={busy || !password}>
            {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : <KeyRound className="h-4 w-4" />}
            {t("Sign in")}
          </button>
        </form>

        <p className="mt-4 flex items-center justify-center gap-1.5 text-xs text-slate-600">
          <ShieldCheck className="h-3.5 w-3.5" />
          {t("Sessions live on the server and end when it restarts.")}
        </p>
      </div>
    </div>
  );
}
