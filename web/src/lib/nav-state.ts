import { createSignal } from 'solid-js';
import { getNavState, setNavState, type NavState } from '../api/client';

// The sidebar and the settings nav are mounted fresh by every page, so their
// fold state lives here rather than in a component signal: one value shared by
// every mount, loaded once per page load. The value itself is stored in the
// project database, so each project remembers its own layout.
const [state, setState] = createSignal<NavState>({
  sidebarCollapsed: false,
  settingsNavCollapsed: false,
});

let loading: Promise<void> | null = null;

export function navState(): NavState {
  return state();
}

// loadNavState fetches the stored state once. Every mount calls it; only the
// first call goes to the server. Until it resolves the panels paint expanded,
// which is the default a project starts with.
export function loadNavState(): Promise<void> {
  if (loading) return loading;
  loading = getNavState().then(
    (stored) => {
      setState(stored);
    },
    () => {
      // Keep the defaults: a server that cannot answer must not hold up the UI.
    },
  );
  return loading;
}

export type NavPanel = keyof NavState;

// Bumped on every fold so a slow response cannot overwrite a newer one.
let version = 0;

// setNavCollapsed records one panel's fold. The signal moves at once, so the
// panel never waits on the round trip; a failed write restores what it showed
// before.
export function setNavCollapsed(panel: NavPanel, collapsed: boolean): void {
  const previous = state();
  if (previous[panel] === collapsed) return;
  const patch: Partial<NavState> =
    panel === 'sidebarCollapsed'
      ? { sidebarCollapsed: collapsed }
      : { settingsNavCollapsed: collapsed };
  setState({ ...previous, ...patch });
  const mine = ++version;
  setNavState(patch).then(
    (stored) => {
      if (mine === version) setState(stored);
    },
    () => {
      if (mine === version) setState(previous);
    },
  );
}
