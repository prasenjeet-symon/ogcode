import { Show, For, createMemo, createSignal, createEffect, type JSX } from 'solid-js';
import { useServer } from '../../context/server';
import { useSession } from '../../context/session';
import { useTheme } from '../../context/theme';
import { useDocIndex } from '../../context/docindex';
import type { ThemeMode } from '../../api/client';
import {
  Group,
  Row,
  Switch,
  Select,
  Value,
  StatusChip,
  Kbd,
  Spinner,
  CopyButton,
  fieldClass,
  matches,
  useShell,
} from './ui';

const ICONS = {
  workspace: 'M2.25 12.75V12A2.25 2.25 0 014.5 9.75h15A2.25 2.25 0 0121.75 12v.75m-8.69-6.44l-2.12-2.12a1.5 1.5 0 00-1.061-.44H4.5A2.25 2.25 0 002.25 6v12a2.25 2.25 0 002.25 2.25h15A2.25 2.25 0 0021.75 18V9a2.25 2.25 0 00-2.25-2.25h-5.379a1.5 1.5 0 01-1.06-.44z',
  palette: 'M9.53 16.122a3 3 0 00-5.78 1.128 2.25 2.25 0 01-2.4 2.245 4.5 4.5 0 008.4-2.245c0-.399-.078-.78-.22-1.128zm0 0a15.998 15.998 0 003.388-1.62m-5.043-.025a15.994 15.994 0 011.622-3.395m3.42 3.42a15.995 15.995 0 004.764-4.648l3.876-5.814a1.151 1.151 0 00-1.597-1.597L14.146 6.32a15.996 15.996 0 00-4.649 4.763m3.42 3.42a6.776 6.776 0 00-3.42-3.42',
  keyboard: 'M6.75 3.75h.008v.008H6.75v-.008zM6.75 7.5h.008v.008H6.75V7.5zm0 3.75h.008v.008H6.75v-.008zM10.5 3.75h.008v.008H10.5v-.008zM10.5 7.5h.008v.008H10.5V7.5zm0 3.75h.008v.008H10.5v-.008zM14.25 3.75h.008v.008h-.008v-.008zM14.25 7.5h.008v.008h-.008V7.5zm0 3.75h.008v.008h-.008v-.008zM17.25 3.75h.008v.008h-.008v-.008zM17.25 7.5h.008v.008h-.008V7.5zm0 3.75h.008v.008h-.008v-.008zM4.5 18.75h15a.75.75 0 00.75-.75v-1.5a.75.75 0 00-.75-.75h-15a.75.75 0 00-.75.75v1.5a.75.75 0 00.75.75z',
  hotkey: 'M15.75 15.75V18m-7.5-6.75h.008v.008H8.25v-.008zM12 15.75V18m3.75-3.75V18M4.5 4.5h15a1.5 1.5 0 011.5 1.5v12a1.5 1.5 0 01-1.5 1.5h-15A1.5 1.5 0 013 18V6a1.5 1.5 0 011.5-1.5z',
  index: 'M21 21l-5.197-5.197m0 0A7.5 7.5 0 105.196 5.196a7.5 7.5 0 0010.607 10.607z',
};

// Defaults, matching session.SearchConfig in Go.
const DEFAULTS = { primaryColor: '#ff5e1f' };

export default function GeneralSettings() {
  const server = useServer();
  const shell = useShell();
  const docIndex = useDocIndex();

  createEffect(() => shell.report({ noun: 'settings' }));

  /** A row shows when the query appears anywhere a reader would look for it. */
  const hide = (...text: (string | undefined)[]) => !matches(shell.query(), ...text);

  return (
    <>
      <Group id="workspace" title="Workspace" icon={ICONS.workspace}>
        <Row
          label="Directory"
          helper="Sessions, themes and indexes are scoped to this folder."
          hidden={hide('Directory', 'folder workspace path')}
        >
          <Value>{shortPath(server.directory())}</Value>
          <Show when={server.directory()}>
            <CopyButton text={server.directory()!} label="" />
          </Show>
        </Row>
        <Row label="Git branch" hidden={hide('Git branch', 'repository vcs')}>
          <Show when={server.branch()} fallback={<Value mono={false} tone="muted">Not a git repository</Value>}>
            <Value>{server.branch()}</Value>
          </Show>
        </Row>
        <Row label="Server" helper="This window's link to the ogcode process." hidden={hide('Server connection status')}>
          <Show when={server.connected()} fallback={<StatusChip tone="danger">Disconnected</StatusChip>}>
            <StatusChip tone="ok">Connected</StatusChip>
          </Show>
        </Row>
      </Group>

      <ThemeGroup hide={hide} />

      <Group id="index" title="Project index" icon={ICONS.index}>
        <Row
          label="Index large projects automatically"
          helper={`Projects with more than ${docIndex.consent()?.limit ?? 500} files wait for your say-so before ogcode indexes them automatically. The manual index button always runs.`}
          hidden={hide('Index large projects automatically', 'project index automatic', 'consent index')}
        >
          <Switch
            checked={docIndex.consent()?.allowed ?? false}
            onChange={(v) => docIndex.setConsent(v)}
            disabled={docIndex.consent()?.autoIndexEnabled === false}
            label="Index large projects automatically"
          />
        </Row>
      </Group>

      <Group
        id="hotkeys"
        title="Model hotkeys"
        icon={ICONS.hotkey}
        description="Pin a model to a numbered slot and switch to it from anywhere in the app."
      >
        <HotkeyRows hide={hide} />
      </Group>

      <Group id="keyboard" title="Keyboard shortcuts" icon={ICONS.keyboard}>
        <For each={SHORTCUTS}>
          {(s) => (
            <Row label={s.action} hidden={hide(s.action, s.keys.join(' '), 'shortcut keyboard')}>
              <span class="flex items-center gap-1">
                <For each={s.keys}>
                  {(k, i) => (
                    <>
                      <Show when={i() > 0}>
                        <span class="text-micro text-[color:var(--text-muted)]">+</span>
                      </Show>
                      <Kbd>{k}</Kbd>
                    </>
                  )}
                </For>
              </span>
            </Row>
          )}
        </For>
      </Group>
    </>
  );
}

const SHORTCUTS = [
  { action: 'Send message', keys: ['Enter'] },
  { action: 'Insert newline', keys: ['Shift', 'Enter'] },
  { action: 'Start new session', keys: ['⌘', 'N'] },
  { action: 'Switch to pinned model slot', keys: ['Alt', '1–4'] },
];

/** Absolute paths are long and their tail is the informative half. */
function shortPath(dir: string): string {
  if (!dir) return '—';
  const home = dir.match(/^\/(Users|home)\/[^/]+/);
  return home ? `~${dir.slice(home[0].length)}` : dir;
}

type Hide = (...text: (string | undefined)[]) => boolean;

// ---------------------------------------------------------------------------
// Appearance
// ---------------------------------------------------------------------------

const PRESETS = [
  { label: 'Orange', hex: '#ff5e1f' },
  { label: 'Violet', hex: '#5e6ad2' },
  { label: 'Blue', hex: '#3b82f6' },
  { label: 'Indigo', hex: '#6366f1' },
  { label: 'Purple', hex: '#8b5cf6' },
  { label: 'Rose', hex: '#f43f5e' },
  { label: 'Amber', hex: '#f59e0b' },
  { label: 'Emerald', hex: '#10b981' },
  { label: 'Cyan', hex: '#06b6d4' },
  { label: 'Pink', hex: '#ec4899' },
];

// The three-way appearance control. Machine-wide, stored on the server.
const MODES: { value: ThemeMode; label: string; icon: string }[] = [
  {
    value: 'light',
    label: 'Light',
    icon: 'M12 3v2.25m6.364.386l-1.591 1.591M21 12h-2.25m-.386 6.364l-1.591-1.591M12 18.75V21m-4.773-4.227l-1.591 1.591M5.25 12H3m4.227-4.773L5.636 5.636M15.75 12a3.75 3.75 0 11-7.5 0 3.75 3.75 0 017.5 0z',
  },
  {
    value: 'dark',
    label: 'Dark',
    icon: 'M21.752 15.002A9.718 9.718 0 0118 15.75c-5.385 0-9.75-4.365-9.75-9.75 0-1.33.266-2.597.748-3.752A9.753 9.753 0 003 11.25C3 16.635 7.365 21 12.75 21a9.753 9.753 0 009.002-5.998z',
  },
  {
    value: 'system',
    label: 'System',
    icon: 'M9 17.25v1.007a3 3 0 01-.879 2.122L7.5 21h9l-.621-.621A3 3 0 0115 18.257V17.25m6-12V15a2.25 2.25 0 01-2.25 2.25H5.25A2.25 2.25 0 013 15V5.25m18 0A2.25 2.25 0 0018.75 3H5.25A2.25 2.25 0 003 5.25m18 0V12a2.25 2.25 0 01-2.25 2.25H5.25A2.25 2.25 0 013 12V5.25',
  },
];

function ThemeGroup(props: { hide: Hide }) {
  const themeCtx = useTheme();
  const [saving, setSaving] = createSignal(false);
  const [error, setError] = createSignal('');
  const [draft, setDraft] = createSignal<string | null>(null);
  const [modeError, setModeError] = createSignal('');

  const selectMode = async (m: ThemeMode) => {
    if (themeCtx.mode() === m) return;
    setModeError('');
    try {
      await themeCtx.setMode(m);
    } catch {
      setModeError('Could not save the theme.');
    }
  };

  const isValidHex = (s: string) => /^#[0-9a-fA-F]{6}$/.test(s) || /^#[0-9a-fA-F]{3}$/.test(s);
  const current = () => draft() ?? themeCtx.primaryColor();

  const apply = async (hex: string) => {
    setDraft(hex);
    setError('');
    if (!isValidHex(hex)) {
      setError('Use a six-digit hex colour, for example #ff5e1f.');
      return;
    }
    setSaving(true);
    try {
      await themeCtx.setPrimaryColor(hex);
      setDraft(null);
    } catch {
      setError('Could not save the theme.');
    } finally {
      setSaving(false);
    }
  };

  return (
    <Group
      id="appearance"
      title="Appearance"
      icon={ICONS.palette}
      description="Choose a light, dark, or system-following interface. One colour drives the whole palette."
    >
      <Row
        label="Theme"
        helper="Follows your operating system, or pin light or dark. Saved for the whole machine."
        hidden={props.hide('Theme', 'light dark mode system appearance colour scheme')}
      >
        <div
          role="radiogroup"
          aria-label="Theme mode"
          class="inline-flex items-center gap-0.5 rounded-[8px] border border-[color:var(--border-default)] p-0.5"
        >
          <For each={MODES}>
            {(m) => {
              const active = () => themeCtx.mode() === m.value;
              return (
                <button
                  type="button"
                  role="radio"
                  aria-checked={active()}
                  onClick={() => selectMode(m.value)}
                  class="inline-flex items-center gap-1.5 rounded-[6px] px-2.5 py-1.5 text-meta font-medium transition-colors"
                  style={{
                    background: active() ? 'var(--accent)' : 'transparent',
                    color: active() ? 'var(--on-primary)' : 'var(--text-secondary)',
                  }}
                >
                  <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                    <path stroke-linecap="round" stroke-linejoin="round" d={m.icon} />
                  </svg>
                  {m.label}
                </button>
              );
            }}
          </For>
        </div>
        <Show when={modeError()}>
          <p class="mt-2 text-meta" style={{ color: 'var(--danger)' }}>{modeError()}</p>
        </Show>
      </Row>

      <Row
        label="Accent colour"
        helper="Pick a colour, type a hex value, or start from a preset."
        stacked
        hidden={props.hide('Accent colour', 'theme primary color palette hex')}
      >
        <div class="flex items-center gap-2 flex-wrap">
          <label class="relative w-8 h-8 shrink-0 rounded-[3px] overflow-hidden border border-[color:var(--border-default)] cursor-pointer">
            <input
              type="color"
              value={current()}
              onInput={(e) => apply(e.currentTarget.value)}
              disabled={saving()}
              aria-label="Pick an accent colour"
              class="absolute -inset-2 w-[calc(100%+1rem)] h-[calc(100%+1rem)] cursor-pointer bg-transparent appearance-none border-0 p-0"
            />
          </label>
          <input
            type="text"
            value={current()}
            onChange={(e) => apply(e.currentTarget.value.trim())}
            disabled={saving()}
            placeholder={DEFAULTS.primaryColor}
            aria-label="Accent colour hex"
            spellcheck={false}
            class={`${fieldClass} w-[6.75rem] font-mono text-meta`}
          />
          <Show when={saving()}>
            <Spinner class="w-4 h-4 text-[color:var(--text-muted)]" />
          </Show>
          <Show when={current().toLowerCase() !== DEFAULTS.primaryColor}>
            <button
              type="button"
              onClick={() => apply(DEFAULTS.primaryColor)}
              class="text-meta font-medium text-[color:var(--text-tertiary)] hover:text-[color:var(--text-primary)] transition-colors"
            >
              Reset
            </button>
          </Show>
        </div>

        <div class="mt-2 flex items-center gap-1.5 flex-wrap">
          <For each={PRESETS}>
            {(p) => {
              const active = () => current().toLowerCase() === p.hex;
              return (
                <button
                  type="button"
                  onClick={() => apply(p.hex)}
                  disabled={saving()}
                  title={p.label}
                  aria-label={`Use ${p.label}`}
                  aria-pressed={active()}
                  class="w-6 h-6 rounded-full shrink-0 flex items-center justify-center transition-transform hover:scale-110"
                  style={{
                    background: p.hex,
                    'box-shadow': active()
                      ? '0 0 0 2px var(--bg-surface), 0 0 0 4px var(--text-primary)'
                      : 'inset 0 0 0 1px rgba(255,255,255,0.14)',
                  }}
                >
                  <Show when={active()}>
                    <svg class="w-3 h-3 text-white" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="3.5">
                      <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
                    </svg>
                  </Show>
                </button>
              );
            }}
          </For>
        </div>

        <Show when={error()}>
          <p class="mt-2 text-meta" style={{ color: 'var(--danger)' }}>{error()}</p>
        </Show>
      </Row>
    </Group>
  );
}

// ---------------------------------------------------------------------------
// Model hotkeys — one row per slot, each a plain dropdown.
// ---------------------------------------------------------------------------

function HotkeyRows(props: { hide: Hide }) {
  const session = useSession();

  const options = createMemo(() => [
    { value: '', label: 'None' },
    ...session
      .models()
      .filter((m) => m.enabled)
      .sort((a, b) => a.name.localeCompare(b.name))
      .map((m) => ({ value: m.id, label: m.name })),
  ]);

  const nameOf = (id: string | null) => session.models().find((m) => m.id === id)?.name ?? '';

  return (
    <For each={session.modelSlots()}>
      {(modelId, index) => (
        <Row
          label={
            <span class="flex items-center gap-1.5">
              <Kbd>Alt</Kbd>
              <span class="text-micro text-[color:var(--text-muted)]">+</span>
              <Kbd>{index() + 1}</Kbd>
            </span>
          }
          hidden={props.hide(`Alt ${index() + 1}`, 'hotkey model slot switch', nameOf(modelId))}
        >
          <Select
            value={modelId || ''}
            options={options()}
            ariaLabel={`Model for Alt+${index() + 1}`}
            onChange={(id) => session.setModelSlot(index(), id || null)}
          />
        </Row>
      )}
    </For>
  );
}
