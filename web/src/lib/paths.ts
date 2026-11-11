// Workspace path helpers. The server hands the UI one absolute directory — the
// workspace it was started in — and several places want a short, human name for
// it rather than the whole path.

/** The project's own name, not the whole path — the full path is one hover
 *  away and a truncated absolute path tells you nothing. */
export function projectName(dir: string): string {
  // Both separators: a Windows workspace (C:\Users\me\proj) and a UNC share
  // (\\server\share) name themselves by their last segment, as a POSIX path
  // does. A bare drive root (C:\) has no name to give, so it stays whole.
  const parts = dir.split(/[\\/]+/).filter(Boolean);
  const last = parts.pop() || '';
  return last && !/^[A-Za-z]:$/.test(last) ? last : (dir || 'No workspace');
}
