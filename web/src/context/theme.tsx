import { createContext, useContext, type ParentComponent } from 'solid-js';
import { createSignal, createEffect, on } from 'solid-js';
import { type Theme, getTheme, setTheme as setThemeAPI } from '../api/client';
import { useServer } from './server';

interface ThemeContextValue {
  theme: () => Theme | null;
  primaryColor: () => string;
  setPrimaryColor: (hex: string) => Promise<void>;
  resetTheme: () => void;
}

const ThemeContext = createContext<ThemeContextValue>();

const DEFAULT_THEME: Theme = {
  directory: '',
  primaryColor: '#ff5e1f',
  accent: '#ff5e1f',
  accentHover: '#f54500',
  accentSoft: 'rgba(255, 94, 31, 0.12)',
  accentRing: 'rgba(255, 94, 31, 0.35)',
  onPrimary: '#ffffff',
  glow: 'rgba(255, 94, 31, 0.12)',
  tint: 'rgba(255, 94, 31, 0.05)',
};

function applyThemeCSS(t: Theme) {
  const root = document.documentElement;
  root.style.setProperty('--accent', t.accent);
  root.style.setProperty('--accent-hover', t.accentHover);
  root.style.setProperty('--accent-soft', t.accentSoft);
  root.style.setProperty('--accent-ring', t.accentRing);
  root.style.setProperty('--on-primary', t.onPrimary);
  root.style.setProperty('--glow', t.glow);
  root.style.setProperty('--tint', t.tint);
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
}

export const ThemeProvider: ParentComponent = (props) => {
  const server = useServer();
  const [theme, setTheme] = createSignal<Theme | null>(null);

  const primaryColor = () => theme()?.primaryColor || DEFAULT_THEME.primaryColor;

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

  const value: ThemeContextValue = {
    theme,
    primaryColor,
    setPrimaryColor,
    resetTheme,
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