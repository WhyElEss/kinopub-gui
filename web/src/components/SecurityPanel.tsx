import { useEffect, useMemo, useState } from "react";
import qrcode from "qrcode-generator";
import {
  AlertTriangle,
  CheckCircle2,
  Loader2,
  LogOut,
  ShieldCheck,
  ShieldOff,
} from "lucide-react";
import { api, type TotpEnrolment, type TotpStatus } from "../api";
import { useI18n } from "../i18n";
import { useApp } from "../store";

// Settings → Security: the second factor, enrolled from the page rather than
// from a shell.
//
// The asymmetry with the password is the point. A password must exist BEFORE
// the server is public — set through a UI, a fresh install would offer an
// unprotected setup page on a public hostname and whoever found it first would
// own the box. A second factor has no such window, since only someone already
// signed in can add one. So it can be a button, and lives in the config volume
// the container can write, not in the environment.
//
// Nothing here is rebuilt under someone using it: an open enrolment survives
// anything else on the Settings page redrawing, because its state lives in this
// component and the panel is never remounted by a refresh elsewhere.

// The QR is drawn here rather than served by the Go side, which has no QR
// encoder and should not grow a hand-written one. The library does the part
// worth trusting a library with — Reed-Solomon, masking, format bits — and this
// turns its matrix into one <path>, which is a fraction of the size of one
// <rect> per module.
//
// A wrong QR cannot lock anyone out: enrolment is only completed by a code that
// matches the secret the SERVER holds, so an image encoding something else
// simply fails to enable anything.
const QUIET = 4;

function QrSvg({ text, className }: { text: string; className?: string }) {
  const path = useMemo(() => {
    // 0 = smallest version that fits. 'M' recovers ~15%, the usual choice for a
    // screen where nothing is going to smudge it.
    const qr = qrcode(0, "M");
    qr.addData(text);
    qr.make();
    const size = qr.getModuleCount();
    let d = "";
    for (let r = 0; r < size; r++) {
      for (let c = 0; c < size; c++) {
        if (qr.isDark(r, c)) d += `M${c} ${r}h1v1h-1z`;
      }
    }
    return { d, span: size + QUIET * 2 };
  }, [text]);

  return (
    // Black on white ALWAYS, whatever the page theme is doing. A QR inverted
    // for the dark theme is a QR most scanners will not read.
    <svg
      className={className}
      viewBox={`${-QUIET} ${-QUIET} ${path.span} ${path.span}`}
      shapeRendering="crispEdges"
      role="img"
      aria-label="QR code"
    >
      <rect x={-QUIET} y={-QUIET} width={path.span} height={path.span} fill="#fff" />
      <path d={path.d} fill="#000" />
    </svg>
  );
}

export function SecurityPanel() {
  const { t } = useI18n();
  const { toast } = useApp();
  const [status, setStatus] = useState<TotpStatus | null>(null);
  const [enrol, setEnrol] = useState<TotpEnrolment | null>(null);
  const [code, setCode] = useState("");
  const [offPassword, setOffPassword] = useState("");
  const [offCode, setOffCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [disabling, setDisabling] = useState(false);

  const load = () =>
    api
      .totpStatus()
      .then(setStatus)
      .catch(() => setStatus(null));

  useEffect(() => {
    load();
  }, []);

  // Nothing to show when the server has no login at all: the panel would be a
  // second factor on top of no first factor.
  if (!status) return null;

  const begin = async () => {
    setBusy(true);
    try {
      // Idempotent on the server while a setup is alive, so pressing this twice
      // does not kill the secret just saved in a password manager.
      setEnrol(await api.totpBegin());
      setCode("");
    } catch (e: any) {
      toast(e.message || t("Could not start enrolment"), "error");
    } finally {
      setBusy(false);
    }
  };

  const enable = async () => {
    setBusy(true);
    try {
      const r = await api.totpEnable(code);
      toast(r.note || t("Two-factor is on"), "success");
      setEnrol(null);
      setCode("");
      load();
    } catch (e: any) {
      toast(e.message || t("That code does not match"), "error");
    } finally {
      setBusy(false);
    }
  };

  const disable = async () => {
    setBusy(true);
    try {
      await api.totpDisable(offPassword, offCode);
      toast(t("Two-factor is off"), "success");
      setDisabling(false);
      setOffPassword("");
      setOffCode("");
      load();
    } catch (e: any) {
      toast(e.message || t("Could not turn two-factor off"), "error");
    } finally {
      setBusy(false);
    }
  };

  const signOut = async () => {
    try {
      await api.logout();
    } finally {
      window.location.reload();
    }
  };

  return (
    <div className="card p-5">
      <h2 className="mb-1 flex items-center gap-2 text-sm font-semibold text-slate-200">
        <ShieldCheck className="h-4 w-4 text-accent-400" /> {t("Security")}
      </h2>
      <p className="mb-4 text-xs text-slate-500">
        {t("This server's own login, signed in as {user}.", { user: status.user })}
      </p>

      <div className="mb-4 flex flex-wrap items-center gap-2">
        {status.broken ? (
          <span className="chip border-ember-500/30 bg-ember-500/10 text-ember-400">
            <AlertTriangle className="h-3.5 w-3.5" /> {t("Two-factor secret unreadable")}
          </span>
        ) : status.enabled ? (
          <span className="chip border-emerald-500/25 bg-emerald-500/10 text-emerald-300">
            <CheckCircle2 className="h-3.5 w-3.5" /> {t("Two-factor ON")}
          </span>
        ) : (
          <span className="chip border-white/[0.08] bg-white/[0.03] text-slate-400">
            <ShieldOff className="h-3.5 w-3.5" /> {t("Two-factor off")}
          </span>
        )}
        <span className="chip border-white/[0.08] bg-white/[0.03] text-slate-400">
          {t("{n} active sessions", { n: status.sessions })}
        </span>
        <button className="btn-ghost ml-auto px-3 py-1.5 text-xs" onClick={signOut}>
          <LogOut className="h-3.5 w-3.5" /> {t("Sign out")}
        </button>
      </div>

      {status.broken && (
        <p className="mb-4 rounded-xl border border-ember-500/25 bg-ember-500/[0.07] px-4 py-3 text-xs text-ember-400">
          {t(
            "Logins are refused until this is fixed — running with one factor when two were configured is the failure nobody notices. Delete {file} on the server to turn two-factor off.",
            { file: status.file },
          )}
        </p>
      )}

      {!status.managedHere && (
        <p className="mb-4 text-xs text-slate-500">
          {t(
            "Two-factor is set through KINOPUB_AUTH_TOTP_SECRET on the server. Change it there — this page cannot edit the environment.",
          )}
        </p>
      )}

      {status.managedHere && !status.enabled && !enrol && (
        <button className="btn-ghost text-sm" onClick={begin} disabled={busy}>
          {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : <ShieldCheck className="h-4 w-4" />}
          {t("Turn on two-factor")}
        </button>
      )}

      {status.managedHere && !status.enabled && enrol && (
        <div className="space-y-4 rounded-xl border border-accent-500/25 bg-accent-500/[0.06] p-4">
          <p className="text-sm text-slate-300">
            {t("Scan this with an authenticator app, then type the code it shows.")}
          </p>
          <div className="flex flex-wrap items-start gap-4">
            <QrSvg text={enrol.uri} className="h-40 w-40 rounded-lg" />
            <div className="min-w-[12rem] flex-1 space-y-2">
              <p className="text-xs text-slate-500">{t("Or type the secret in by hand:")}</p>
              <code className="block select-all break-all rounded-lg bg-ink-950/60 px-3 py-2 font-mono text-xs text-accent-300">
                {enrol.secret}
              </code>
              <p className="text-xs text-slate-500">
                {t("The app should be showing {code} right now. If it shows something else, this server and your phone disagree about the time.", {
                  code: enrol.expectedNow,
                })}
              </p>
            </div>
          </div>
          <div className="flex flex-wrap items-end gap-2">
            <div>
              <label className="label" htmlFor="totp-confirm">
                {t("Code from the app")}
              </label>
              <input
                id="totp-confirm"
                className="input login-input w-36"
                inputMode="numeric"
                maxLength={6}
                placeholder="000000"
                value={code}
                onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
              />
            </div>
            <button className="btn-primary" onClick={enable} disabled={busy || code.length !== 6}>
              {busy && <Loader2 className="h-4 w-4 animate-spin" />}
              {t("Confirm and turn on")}
            </button>
            <button className="btn-ghost" onClick={() => setEnrol(null)} disabled={busy}>
              {t("Cancel")}
            </button>
          </div>
          <p className="text-xs text-slate-500">
            {t("Nothing is saved until a code proves the app really has the secret.")}
          </p>
        </div>
      )}

      {status.managedHere && status.enabled && !disabling && (
        <button className="btn-ghost text-sm" onClick={() => setDisabling(true)}>
          <ShieldOff className="h-4 w-4" /> {t("Turn off two-factor")}
        </button>
      )}

      {status.managedHere && status.enabled && disabling && (
        <div className="space-y-3 rounded-xl border border-ember-500/25 bg-ember-500/[0.06] p-4">
          <p className="text-sm text-slate-300">
            {t("Both factors again, on purpose: a hijacked session must not be able to quietly remove the thing protecting the account.")}
          </p>
          <div className="flex flex-wrap items-end gap-2">
            <div>
              <label className="label" htmlFor="totp-off-password">
                {t("Password")}
              </label>
              <input
                id="totp-off-password"
                type="password"
                className="input login-input w-56"
                autoComplete="current-password"
                value={offPassword}
                onChange={(e) => setOffPassword(e.target.value)}
              />
            </div>
            {!status.broken && (
              <div>
                <label className="label" htmlFor="totp-off-code">
                  {t("Code from the app")}
                </label>
                <input
                  id="totp-off-code"
                  className="input login-input w-36"
                  inputMode="numeric"
                  maxLength={6}
                  placeholder="000000"
                  value={offCode}
                  onChange={(e) => setOffCode(e.target.value.replace(/\D/g, ""))}
                />
              </div>
            )}
            <button className="btn-danger" onClick={disable} disabled={busy || !offPassword}>
              {busy && <Loader2 className="h-4 w-4 animate-spin" />}
              {t("Turn off")}
            </button>
            <button className="btn-ghost" onClick={() => setDisabling(false)} disabled={busy}>
              {t("Cancel")}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
