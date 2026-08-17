import { Component, type ErrorInfo, type ReactNode } from "react";

// ErrorBoundary keeps a crash in one component from blanking the whole app.
// React unmounts the entire tree when a render throws, and with the app's dark
// background that reads as "the interface vanished" with nothing to act on — so
// the failure is shown instead, with the message and a way back.
//
// It is deliberately not translated: it has to render even when something as
// basic as the i18n context is what broke.
export class ErrorBoundary extends Component<{ children: ReactNode }, { error: Error | null }> {
  state: { error: Error | null } = { error: null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("UI crashed:", error, info.componentStack);
  }

  render() {
    const { error } = this.state;
    if (!error) return this.props.children;
    return (
      <div style={{ minHeight: "100vh", display: "flex", alignItems: "center", justifyContent: "center", padding: "2rem", background: "#0b0d12", color: "#e2e8f0", fontFamily: "system-ui, sans-serif" }}>
        <div style={{ maxWidth: "40rem" }}>
          <h1 style={{ fontSize: "1.25rem", fontWeight: 700, marginBottom: "0.75rem" }}>The interface hit an error</h1>
          <p style={{ fontSize: "0.875rem", color: "#94a3b8", marginBottom: "1rem" }}>
            Downloads already running are not affected — they run in the server, not in this page.
          </p>
          <pre style={{ fontSize: "0.75rem", background: "rgba(255,255,255,0.05)", padding: "0.75rem", borderRadius: "0.5rem", overflowX: "auto", marginBottom: "1rem" }}>
            {error.message}
          </pre>
          <button
            onClick={() => window.location.reload()}
            style={{ background: "#f59e0b", color: "#0b0d12", border: 0, borderRadius: "0.75rem", padding: "0.5rem 1rem", fontWeight: 600, cursor: "pointer" }}
          >
            Reload
          </button>
        </div>
      </div>
    );
  }
}
