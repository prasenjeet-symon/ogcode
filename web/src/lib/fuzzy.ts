// fuzzyMatch returns true when every character of query appears in text in order
// (case-insensitive subsequence) — the same forgiving match Linear's menus use.
//
// Shared by the command menu and the ⌘K session palette: both filter a list
// that is already loaded, so this is the whole "search" for it — the palette's
// fuller search over what sessions were about is the server's job.
export function fuzzyMatch(text: string, query: string): boolean {
  if (!query) return true;
  const t = text.toLowerCase();
  const q = query.toLowerCase();
  let i = 0;
  for (let j = 0; j < t.length && i < q.length; j++) {
    if (t[j] === q[i]) i++;
  }
  return i === q.length;
}
