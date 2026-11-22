// The before/after text of one `edit` call, read from the call's own input.
// The tool result carries no diff for an edit (unlike write, whose metadata
// holds the old content), so these two strings are all the transcript has to
// render one from.

// A hunk is either POSITIONED (at/find/replace) or ANCHORED
// (old_string/new_string); the tool refuses a call that mixes the two. Both
// shapes are read here: a positioned hunk has no old_string at all, so reading
// only the anchored keys left every positioned edit rendering "+0 −0" with an
// empty body while the file changed correctly underneath.
function isPositioned(h: any): boolean {
  // Mirrors resolveHunk in internal/tool/edit.go: `at` counts only when set,
  // while find/replace count without one — that call is applied as anchored,
  // but its text is still carried in the positioned field names.
  return (h?.at ?? 0) !== 0 || h?.find != null || h?.replace != null;
}

function hunkTexts(h: any): [oldText: string, newText: string] {
  if (isPositioned(h)) return [String(h?.find ?? ''), String(h?.replace ?? '')];
  return [String(h?.old_string ?? ''), String(h?.new_string ?? '')];
}

/**
 * editDiffTexts joins a call's hunks in order into the one before/after the
 * call applied to the file. A positioned hunk may omit `find` — that is an
 * insert before `at`, so its before text is empty.
 *
 * The flat fallback is for history: messages written before the tool took an
 * array still hold old_string/new_string at the top level, and reading only
 * the array would blank out the diff on every past session.
 */
export function editDiffTexts(input: any): { oldText: string; newText: string } {
  const hunks = Array.isArray(input?.edits) ? input.edits : null;
  if (!hunks) {
    return { oldText: String(input?.old_string ?? ''), newText: String(input?.new_string ?? '') };
  }
  const pairs: [string, string][] = hunks.map(hunkTexts);
  return {
    oldText: pairs.map((p) => p[0]).join('\n'),
    newText: pairs.map((p) => p[1]).join('\n'),
  };
}
