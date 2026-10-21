import { createContext, useContext, type ParentComponent } from 'solid-js';
import { createSignal, createEffect, createMemo, on, onMount, onCleanup } from 'solid-js';
import {
  type Theme,
  type ThemeMode,
  getTheme,
  setTheme as setThemeAPI,
  getAppearance,
  setAppearance,
} from '../api/client';
import { useServer } from './server';

interface ThemeContextValue {
  theme: () => Theme | null;
  primaryColor: () => string;
  setPrimaryColor: (hex: string) => Promise<void>;
  resetTheme: () => void;
  /** The chosen mode: 'light', 'dark', or 'system'. */
  mode: () => ThemeMode;
  /** The mode actually rendered, with 'system' resolved against the OS. */
  resolvedMode: () => 'light' | 'dark';
  setMode: (mode: ThemeMode) => Promise<void>;
}

const ThemeContext = createContext<ThemeContextValue>();

const DEFAULT_THEME: Theme = {
  directory: '',
  primaryColor: '#ff5e1f',
  accent: '#ff5e1f',
  accentHover: '#f54500',
  accentSoft: 'rgba(255, 94, 31, 0.12)',
  accentRing: 'rgba(255, 94, 31, 0.35)',
  onPrimary: '#0a0a0b',
  glow: 'rgba(255, 94, 31, 0.12)',
  tint: 'rgba(255, 94, 31, 0.05)',
  accentInk: '#ff6022',
  accentInkLight: '#ab3000',
};

// The localStorage mirror of the server-side mode. It exists only so the
// pre-paint script in index.html can pick the right theme before the bundle
// loads; the server is the source of truth and reconciles on every mount.
const MODE_STORAGE_KEY = 'ogcode-theme-mode';

function systemPrefersLight(): boolean {
  return typeof window !== 'undefined' && window.matchMedia('(prefers-color-scheme: light)').matches;
}

/** Resolve a mode to the concrete theme to render, following the OS for 'system'. */
function resolveMode(mode: ThemeMode): 'light' | 'dark' {
  if (mode === 'system') return systemPrefersLight() ? 'light' : 'dark';
  return mode;
}

function persistMode(mode: ThemeMode) {
  try {
    localStorage.setItem(MODE_STORAGE_KEY, mode);
  } catch {
    /* private mode or storage disabled — the server still holds the truth */
  }
}

/** Stamp the resolved theme on <html> and keep the browser chrome in step. */
function applyModeCSS(mode: ThemeMode) {
  const resolved = resolveMode(mode);
  const root = document.documentElement;
  root.dataset.theme = resolved;
  root.style.colorScheme = resolved;
  const meta = document.querySelector('meta[name="theme-color"]');
  if (meta) meta.setAttribute('content', resolved === 'light' ? '#f8f8fa' : '#09090b');
}

function applyThemeCSS(t: Theme) {
  const root = document.documentElement;
  root.style.setProperty('--accent', t.accent);
  root.style.setProperty('--accent-hover', t.accentHover);
  root.style.setProperty('--accent-soft', t.accentSoft);
  root.style.setProperty('--accent-ring', t.accentRing);
  root.style.setProperty('--on-primary', t.onPrimary);
  root.style.setProperty('--glow', t.glow);
  root.style.setProperty('--tint', t.tint);
  root.style.setProperty('--accent-ink-dark', t.accentInk);
  root.style.setProperty('--accent-ink-light', t.accentInkLight);
}

function resetCSS() {
  const root = document.documentElement;
  root.style.removeProperty('--accent');
  root.style.removeProperty('--accent-hover');
  root.style.removeProperty('--accent-soft');
  root.style.removeProperty('--accent-ring');
  root.style.removeProperty('--on-primary');
  root.style.removeProperty('--glow');
  root.style.removeProperty('--tint');
  root.style.removeProperty('--accent-ink-dark');
  root.style.removeProperty('--accent-ink-light');
}

export const ThemeProvider: ParentComponent = (props) => {
  const server = useServer();
  const [theme, setTheme] = createSignal<Theme | null>(null);
  const [mode, setModeSignal] = createSignal<ThemeMode>('system');
  // Bumped when the OS colour preference changes, so `resolvedMode` recomputes.
  const [systemTick, setSystemTick] = createSignal(0);

  const primaryColor = () => theme()?.primaryColor || DEFAULT_THEME.primaryColor;
  const resolvedMode = createMemo(() => {
    systemTick();
    return resolveMode(mode());
  });

  // Load the global theme mode once, and keep 'system' following the OS live.
  onMount(() => {
    getAppearance()
      .then((a) => {
        setModeSignal(a.mode);
        persistMode(a.mode);
        applyModeCSS(a.mode);
      })
      .catch(() => applyModeCSS(mode()));

    const mq = window.matchMedia('(prefers-color-scheme: light)');
    const onChange = () => {
      setSystemTick((n) => n + 1);
      if (mode() === 'system') applyModeCSS('system');
    };
    mq.addEventListener('change', onChange);
    onCleanup(() => mq.removeEventListener('change', onChange));
  });

  // Load theme when directory changes
  createEffect(on(server.directory, async (dir) => {
    if (!dir) return;
    try {
      const t = await getTheme(dir);
      setTheme(t);
      applyThemeCSS(t);
    } catch {
      setTheme({ ...DEFAULT_THEME, directory: dir });
      resetCSS();
      applyThemeCSS(DEFAULT_THEME);
    }
  }));

  const setPrimaryColor = async (hex: string) => {
    const dir = server.directory();
    if (!dir) return;
    try {
      const t = await setThemeAPI(hex, dir);
      setTheme(t);
      applyThemeCSS(t);
    } catch (e) {
      console.error('set theme failed:', e);
      throw e;
    }
  };

  const resetTheme = () => {
    resetCSS();
    if (theme()) {
      applyThemeCSS({ ...DEFAULT_THEME, directory: theme()!.directory });
    }
    setTheme(null);
  };

  const setMode = async (next: ThemeMode) => {
    const prev = mode();
    setModeSignal(next);
    persistMode(next);
    applyModeCSS(next);
    try {
      await setAppearance(next);
    } catch (e) {
      setModeSignal(prev);
      persistMode(prev);
      applyModeCSS(prev);
      throw e;
    }
  };

  const value: ThemeContextValue = {
    theme,
    primaryColor,
    setPrimaryColor,
    resetTheme,
    mode,
    resolvedMode,
    setMode,
  };

  return (
    <ThemeContext.Provider value={value}>
      {props.children}
    </ThemeContext.Provider>
  );
};

export function useTheme() {
  const ctx = useContext(ThemeContext);
  if (!ctx) throw new Error('useTheme must be used within ThemeProvider');
  return ctx;
}
