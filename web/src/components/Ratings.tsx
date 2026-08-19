import type { DiscoverItem } from "../api";

// rateColor tints a 0–10 score green when good, amber when mid, dim when low.
function rateColor(v: number): string {
  if (v >= 7) return "text-emerald-400";
  if (v >= 5) return "text-amber-300";
  return "text-slate-400";
}

// Ratings shows the IMDb score, styled like the site: the source mark followed
// by the number. Shared by the catalog grid and the title-detail card so they
// look identical.
//
// IMDb only, by request. kino.pub's own 👍 score and the Kinopoisk badge used
// to sit here too, and three of them did not fit: a catalog card is ~118px
// wide in the three-column phone grid against the ~158px the row needed, so
// the last badge was sliced through the middle of its glyphs. The Kinopoisk
// sort and rating filter are untouched — those are search, not a badge.
//
// The yellow is IMDb's own brand yellow and is deliberately NOT a theme
// variable: it is a logo mark, and it must not flip with the theme. Same
// reasoning as `pure` for video-overlay chrome.
export function Ratings({ item, className }: { item: Pick<DiscoverItem, "imdbRating">; className?: string }) {
  if (item.imdbRating <= 0) return null;
  return (
    <div className={`flex flex-nowrap items-center gap-x-2.5 text-xs font-bold ${className || ""}`}>
      <span className="inline-flex items-center gap-1">
        <span className="rounded bg-yellow-400/90 px-1 text-[8px] font-extrabold text-black">IMDb</span>
        <span className={rateColor(item.imdbRating)}>{item.imdbRating.toFixed(1)}</span>
      </span>
    </div>
  );
}
