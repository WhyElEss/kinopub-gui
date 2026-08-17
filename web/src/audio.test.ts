import { describe, expect, it } from "vitest";
import { buildAudioSpecs } from "./audio";
import type { AudioSpec, DiscoverAudio } from "./api";

// track builds a catalog entry plus the HLS track NAME the server would see for
// it, e.g. "01. Многоголосый. Первый канал ОРТ (RUS)".
function track(
  index: number,
  type: string,
  author: string,
  lang: string,
  codec?: string,
): DiscoverAudio & { hlsName: string } {
  const label = [type, author].filter(Boolean).join(" · ") + (codec ? ` · ${codec}` : "");
  return {
    index,
    lang,
    type,
    author,
    label,
    filter: author || type || lang,
    codec,
    surround: !!codec,
    hlsName:
      `${String(index).padStart(2, "0")}. ` +
      [type, author].filter(Boolean).join(". ") +
      ` (${lang.toUpperCase()})` +
      (codec ? ` ${codec.toUpperCase()}` : ""),
  };
}

// matches mirrors domain.AudioSpec.matches: every Require token must appear in
// the track's name+language, and no Forbid token may (case-insensitive
// substring). Keeping a copy here is what makes these tests meaningful — they
// assert the rules select the right tracks under the server's own semantics.
function matches(hlsName: string, lang: string, spec: AudioSpec): boolean {
  const hay = `${hlsName} ${lang}`.toLowerCase();
  if (spec.require.length === 0) return false;
  return (
    spec.require.every((r) => hay.includes(r.toLowerCase())) &&
    !spec.forbid.some((f) => hay.includes(f.toLowerCase()))
  );
}

function selected(all: ReturnType<typeof track>[], specs: AudioSpec[] | undefined): string[] {
  if (!specs) return all.map((a) => a.label); // undefined = keep everything
  return all.filter((a) => specs.some((s) => matches(a.hlsName, a.lang, s))).map((a) => a.label);
}

describe("buildAudioSpecs", () => {
  // The reported bug: picking the studio-less "Многоголосый" dub also pulled in
  // every other multi-voice dub, because its filter is a substring of theirs.
  const film = [
    track(1, "Многоголосый", "Первый канал ОРТ", "rus"),
    track(2, "Многоголосый", "Позитив-Мультимедиа", "rus"),
    track(3, "Многоголосый", "", "rus"),
    track(4, "Дубляж", "Студия Горького", "rus"),
    track(8, "Оригинал", "", "eng"),
  ];

  it("keeps only the studio-less dub when it alone is picked", () => {
    const chosen = [film[2]];
    expect(selected(film, buildAudioSpecs(film, chosen))).toEqual([film[2].label]);
  });

  it("keeps exactly one dub and the original when both are picked", () => {
    const chosen = [film[2], film[4]];
    expect(selected(film, buildAudioSpecs(film, chosen))).toEqual([film[2].label, film[4].label]);
  });

  it("keeps a studio dub without its siblings", () => {
    const chosen = [film[0]];
    expect(selected(film, buildAudioSpecs(film, chosen))).toEqual([film[0].label]);
  });

  it("sends no rules when everything is picked", () => {
    expect(buildAudioSpecs(film, film)).toBeUndefined();
    expect(buildAudioSpecs(film, [])).toBeUndefined();
  });

  // A surround variant carries the same studio as its stereo sibling, so only
  // the codec tells them apart.
  const withSurround = [
    track(1, "Многоголосый", "AniLibria", "rus"),
    track(2, "Многоголосый", "AniLibria", "rus", "ac3"),
    track(3, "Оригинал", "", "jpn"),
  ];

  it("separates a dub from its AC3 sibling in both directions", () => {
    expect(selected(withSurround, buildAudioSpecs(withSurround, [withSurround[0]]))).toEqual([
      withSurround[0].label,
    ]);
    expect(selected(withSurround, buildAudioSpecs(withSurround, [withSurround[1]]))).toEqual([
      withSurround[1].label,
    ]);
  });

  // Nothing distinguishes two identically-described tracks; the rule must still
  // keep the picked one rather than collapsing to nothing.
  it("keeps the picked track when a twin cannot be told apart", () => {
    const twins = [track(1, "Многоголосый", "", "rus"), track(2, "Многоголосый", "", "rus")];
    const got = selected(twins, buildAudioSpecs(twins, [twins[0]]));
    expect(got).toContain(twins[0].label);
  });
});
