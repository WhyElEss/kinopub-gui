/** @type {import('tailwindcss').Config} */

// withAlpha builds a colour that reads its channels from a CSS variable while
// still honouring Tailwind's /opacity modifier.
const withAlpha = (v) => `rgb(var(${v}) / <alpha-value>)`;

export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      // Every colour resolves through a CSS variable holding an "R G B" triplet,
      // so a theme swap is a variable swap and the class names in the components
      // never change. <alpha-value> keeps Tailwind's /opacity modifiers working.
      colors: {
        ink: {
          950: withAlpha("--c-ink-950"), 900: withAlpha("--c-ink-900"),
          850: withAlpha("--c-ink-850"), 800: withAlpha("--c-ink-800"),
          750: withAlpha("--c-ink-750"), 700: withAlpha("--c-ink-700"),
          600: withAlpha("--c-ink-600"), 500: withAlpha("--c-ink-500"),
        },
        slate: {
          100: withAlpha("--c-slate-100"), 200: withAlpha("--c-slate-200"),
          300: withAlpha("--c-slate-300"), 400: withAlpha("--c-slate-400"),
          500: withAlpha("--c-slate-500"), 600: withAlpha("--c-slate-600"),
        },
        gold: {
          200: withAlpha("--c-gold-200"), 300: withAlpha("--c-gold-300"),
          400: withAlpha("--c-gold-400"), 500: withAlpha("--c-gold-500"),
          600: withAlpha("--c-gold-600"),
        },
        ember: {
          200: withAlpha("--c-ember-200"), 300: withAlpha("--c-ember-300"),
          400: withAlpha("--c-ember-400"), 500: withAlpha("--c-ember-500"),
        },
        emerald: {
          100: withAlpha("--c-emerald-100"), 300: withAlpha("--c-emerald-300"),
          400: withAlpha("--c-emerald-400"), 500: withAlpha("--c-emerald-500"),
          600: withAlpha("--c-emerald-600"),
        },
        amber: {
          300: withAlpha("--c-amber-300"), 400: withAlpha("--c-amber-400"),
          500: withAlpha("--c-amber-500"),
        },
        sky: {
          300: withAlpha("--c-sky-300"), 400: withAlpha("--c-sky-400"),
          500: withAlpha("--c-sky-500"), 600: withAlpha("--c-sky-600"),
        },
        rose: { 100: withAlpha("--c-rose-100") },
        // "white" is the HAIRLINE/WASH base — border-white/[0.06] and friends.
        // On the dark theme it is white; on the light one it flips to near-black,
        // so ~95 existing utilities keep meaning "a faint line over the surface".
        white: withAlpha("--c-hairline"),
        // Literal white, for the few places that must stay white in any theme:
        // controls over video, and the toggle knob.
        pure: "#ffffff",
      },
      fontFamily: {
        sans: [
          "Inter",
          "-apple-system",
          "BlinkMacSystemFont",
          "Segoe UI",
          "Roboto",
          "Helvetica Neue",
          "Arial",
          "sans-serif",
        ],
        mono: ["ui-monospace", "SFMono-Regular", "Menlo", "monospace"],
      },
      boxShadow: {
        glow: "0 0 0 1px rgba(245,158,11,0.15), 0 8px 40px -12px var(--shadow-glow)",
        card: "0 1px 0 0 var(--shadow-inset) inset, 0 12px 40px -20px var(--shadow-card)",
      },
      keyframes: {
        "fade-in": {
          from: { opacity: "0", transform: "translateY(4px)" },
          to: { opacity: "1", transform: "translateY(0)" },
        },
        shimmer: {
          "100%": { transform: "translateX(100%)" },
        },
        "pulse-soft": {
          "0%, 100%": { opacity: "1" },
          "50%": { opacity: "0.55" },
        },
      },
      animation: {
        "fade-in": "fade-in 0.25s ease-out",
        shimmer: "shimmer 1.6s infinite",
        "pulse-soft": "pulse-soft 1.8s ease-in-out infinite",
      },
    },
  },
  plugins: [],
};
