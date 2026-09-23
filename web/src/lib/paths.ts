// Workspace path helpers. The server hands the UI one absolute directory — the
// workspace it was started in — and several places want a short, human name for
// it rather than the whole path.

/** The project's own name, not the whole path — the full path is one hover
 *  away and a truncated absolute path tells you nothing. */
export function projectName(dir: string): string {
  return dir.split('/').filter(Boolean).pop() || dir || 'No workspace';
}
