import type { AudioSpec, DiscoverAudio } from "./api";

// Codecs that appear verbatim in an HLS track name and mark a surround variant
// of a dub, so the plain stereo track and its 5.1 sibling can be told apart.
export const TAGGED_CODECS = ["ac3", "eac3", "e-ac3", "dts", "dts-hd", "truehd", "true-hd"];

const isTagged = (a: Pick<DiscoverAudio, "codec">) =>
  TAGGED_CODECS.includes((a.codec || "").toLowerCase());

// searchName approximates the HLS track NAME the server matches specs against
// — e.g. "01. Многоголосый. Первый канал ОРТ (RUS)". The catalog gives us the
// pieces separately, and the server's matcher is a case-insensitive substring
// over name + language, so joining them is equivalent for matching purposes.
function searchName(a: DiscoverAudio): string {
  return [a.type, a.author, a.lang].filter(Boolean).join(" ").toLowerCase();
}

const contains = (haystack: string, token: string) =>
  !!token && haystack.includes(token.toLowerCase());

// buildAudioSpecs turns the picked voiceovers into exact selection rules.
//
// The naive rule — require the track's own filter — is not enough, because the
// filter is only as specific as the catalog data: a dub with no studio falls
// back to its type, and "Многоголосый" is a substring of every other
// multi-voice dub. Picking one such track therefore pulled in all of its
// siblings.
//
// So each rule also forbids what distinguishes the tracks the user did NOT
// pick: for every unpicked track that would still satisfy the rule, its studio
// (or failing that, its type) is added to the rule's Forbid list — but only
// when that token doesn't also describe the picked track, which would make the
// rule match nothing.
//
// Returns undefined when every track is picked (or none is), which the server
// reads as "keep them all".
export function buildAudioSpecs(
  all: DiscoverAudio[],
  chosen: DiscoverAudio[],
): AudioSpec[] | undefined {
  if (chosen.length === 0 || chosen.length === all.length) return undefined;

  const chosenKeys = new Set(chosen.map((a) => a.label));
  const others = all.filter((a) => !chosenKeys.has(a.label));

  return chosen.map((a) => {
    const require = [a.filter].filter(Boolean);
    // The language is part of the identity, not decoration. A title can ship
    // "Дубляж" in two languages, and the manifest may hold dubs the catalog
    // never listed — a German one, say — which a rule requiring only "Дубляж"
    // would happily match too. The server's matcher understands a language
    // token (it compares canonical languages as well as the name), so adding it
    // pins the rule to the one the user actually picked.
    if (a.lang) require.push(a.lang);
    // A tagged track requires its codec; an untagged one forbids every tagged
    // codec so it doesn't also match its own surround sibling.
    if (isTagged(a) && a.codec) require.push(a.codec);
    const forbid = isTagged(a) ? [] : [...TAGGED_CODECS];

    const mine = searchName(a);
    for (const other of others) {
      const theirs = searchName(other);
      // Only tracks this rule would otherwise keep need excluding.
      if (!require.every((r) => contains(theirs, r))) continue;
      for (const token of [other.author, other.type]) {
        if (!token || contains(mine, token)) continue; // would kill the picked track
        if (!forbid.some((f) => f.toLowerCase() === token.toLowerCase())) forbid.push(token);
        break; // the studio alone is enough; fall back to the type only without one
      }
    }
    return { require, forbid };
  });
}
