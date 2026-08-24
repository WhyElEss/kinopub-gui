import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import { AppProvider } from "./store";
import { I18nProvider } from "./i18n";
import { ErrorBoundary } from "./components/ErrorBoundary";
import { AuthGate } from "./components/AuthGate";
import { applyStoredTheme } from "./theme";
import "./index.css";

// Before the first paint: the server's setting arrives later over SSE, and a
// dark flash on a light theme (or the reverse) is exactly what a cached value
// avoids.
applyStoredTheme();

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ErrorBoundary>
      <I18nProvider>
        {/* Outside AppProvider on purpose: the store opens an SSE stream and
            starts polling, and none of that should run before anyone is signed
            in. Without a password on the server the gate is invisible. */}
        <AuthGate>
          <AppProvider>
            <App />
          </AppProvider>
        </AuthGate>
      </I18nProvider>
    </ErrorBoundary>
  </StrictMode>,
);
