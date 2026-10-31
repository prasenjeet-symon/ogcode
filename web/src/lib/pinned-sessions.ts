// Pinned sessions are a local preference (there is no server field for them).
// Stored as a plain list of ids; they keep the order they were pinned in.
// The sidebar writes them and the ⌘K palette reads them, so the key and the
// parsing live here rather than in either component.
export const PIN_STORAGE_KEY = 'ogcode-pinned-sessions';

export function loadPinned(): Set<string> {
  try {
    const raw = localStorage.getItem(PIN_STORAGE_KEY);
    if (!raw) return new Set();
    const arr = JSON.parse(raw);
    return Array.isArray(arr) ? new Set(arr.filter((x) => typeof x === 'string')) : new Set();
  } catch {
    return new Set();
  }
}

// savePinned persists the current list. A failure (private mode, quota) is
// swallowed: the in-memory set remains authoritative for this tab.
export function savePinned(ids: Iterable<string>): void {
  try {
    localStorage.setItem(PIN_STORAGE_KEY, JSON.stringify([...ids]));
  } catch {
    // private mode / quota — the pin still holds for this session
  }
}
