import { Show, For, createMemo, createSignal, createEffect, onCleanup, onMount, type JSX } from 'solid-js';
import { useServer } from '../../context/server';
import { useSession } from '../../context/session';
import { useTheme } from '../../context/theme';
import { useDocIndex } from '../../context/docindex';
import {
  getBackgroundModel,
  setBackgroundModel,
  type ThemeMode,
} from '../../api/client';
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
import { CatalogueGroup } from './catalogue';

const ICONS = {
  workspace: 'M2.25 12.75V12A2.25 2.25 0 014.5 9.75h15A2.25 2.25 0 0121.75 12v.75m-8.69-6.44l-2.12-2.12a1.5 1.5 0 00-1.061-.44H4.5A2.25 2.25 0 002.25 6v12a2.25 2.25 0 002.25 2.25h15A2.25 2.25 0 0021.75 18V9a2.25 2.25 0 00-2.25-2.25h-5.379a1.5 1.5 0 01-1.06-.44z',
  palette: 'M9.53 16.122a3 3 0 00-5.78 1.128 2.25 2.25 0 01-2.4 2.245 4.5 4.5 0 008.4-2.245c0-.399-.078-.78-.22-1.128zm0 0a15.998 15.998 0 003.388-1.62m-5.043-.025a15.994 15.994 0 011.622-3.395m3.42 3.42a15.995 15.995 0 004.764-4.648l3.876-5.814a1.151 1.151 0 00-1.597-1.597L14.146 6.32a15.996 15.996 0 00-4.649 4.763m3.42 3.42a6.776 6.776 0 00-3.42-3.42',
  keyboard: 'M6.75 3.75h.008v.008H6.75v-.008zM6.75 7.5h.008v.008H6.75V7.5zm0 3.75h.008v.008H6.75v-.008zM10.5 3.75h.008v.008H10.5v-.008zM10.5 7.5h.008v.008H10.5V7.5zm0 3.75h.008v.008H10.5v-.008zM14.25 3.75h.008v.008h-.008v-.008zM14.25 7.5h.008v.008h-.008V7.5zm0 3.75h.008v.008h-.008v-.008zM17.25 3.75h.008v.008h-.008v-.008zM17.25 7.5h.008v.008h-.008V7.5zm0 3.75h.008v.008h-.008v-.008zM4.5 18.75h15a.75.75 0 00.75-.75v-1.5a.75.75 0 00-.75-.75h-15a.75.75 0 00-.75.75v1.5a.75.75 0 00.75.75z',
  hotkey: 'M15.75 15.75V18m-7.5-6.75h.008v.008H8.25v-.008zM12 15.75V18m3.75-3.75V18M4.5 4.5h15a1.5 1.5 0 011.5 1.5v12a1.5 1.5 0 01-1.5 1.5h-15A1.5 1.5 0 013 18V6a1.5 1.5 0 011.5-1.5z',
  index: 'M21 21l-5.197-5.197m0 0A7.5 7.5 0 105.196 5.196a7.5 7.5 0 0010.607 10.607z',
  background: 'M9.813 15.904L9 18.75l-.813-2.846a4.5 4.5 0 00-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 003.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 003.09 3.09L15.75 12l-2.846.813a4.5 4.5 0 00-3.09 3.09zM18.259 8.715L18 9.75l-.259-1.035a3.375 3.375 0 00-2.455-2.456L14.25 6l1.036-.259a3.375 3.375 0 002.455-2.456L18 2.25l.259 1.035a3.375 3.375 0 002.456 2.456L21.75 6l-1.035.259a3.375 3.375 0 00-2.456 2.456z',
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

      <BackgroundModelGroup hide={hide} />

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

      <CatalogueGroup hide={hide} />

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

  // Every save takes a ticket, and only the newest may touch the UI when it
  // settles — a slow earlier request must not clear the draft or the spinner
  // under a later one.
  let applySeq = 0;
  // The native colour picker fires `input` continuously while the user drags.
  // Show the value at once, save once the drag settles.
  let colorTimer: number | undefined;
  onCleanup(() => clearTimeout(colorTimer));

  const apply = async (hex: string) => {
    setDraft(hex);
    setError('');
    if (!isValidHex(hex)) {
      setError('Use a six-digit hex colour, for example #ff5e1f.');
      return;
    }
    setSaving(true);
    const seq = ++applySeq;
    try {
      await themeCtx.setPrimaryColor(hex);
      if (seq !== applySeq) return;
      setDraft(null);
      setSaving(false);
    } catch {
      if (seq !== applySeq) return;
      setError('Could not save the theme.');
      setSaving(false);
    }
  };

  const pickColour = (hex: string) => {
    setDraft(hex);
    setError('');
    clearTimeout(colorTimer);
    colorTimer = window.setTimeout(() => apply(hex), 150);
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
          class="inline-flex items-center gap-0.5 rounded-[5px] border border-[color:var(--border-default)] p-0.5"
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
                  class="inline-flex items-center gap-1.5 rounded-[4px] px-2.5 py-1.5 text-meta font-medium transition-colors"
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
              onInput={(e) => pickColour(e.currentTarget.value)}
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
                    // The active swatch paints the derived accent (slightly
                    // clamped from the preset) so the checkmark's label colour,
                    // --on-primary, is the one guaranteed to clear AA on it.
                    background: active() ? 'var(--accent)' : p.hex,
                    'box-shadow': active()
                      ? '0 0 0 2px var(--bg-surface), 0 0 0 4px var(--text-primary)'
                      : 'inset 0 0 0 1px rgba(255,255,255,0.14)',
                  }}
                >
                  <Show when={active()}>
                    <svg class="w-3 h-3 text-[color:var(--on-primary)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="3.5">
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
// Background tasks model — machine-wide override for behind-the-scenes LLM calls.
// ---------------------------------------------------------------------------

function encodeModelKey(providerId: string, modelId: string): string {
  if (!modelId) return '';
  return `${providerId}\t${modelId}`;
}

function decodeModelKey(key: string): { providerId: string; model: string } {
  if (!key) return { providerId: '', model: '' };
  const idx = key.indexOf('\t');
  if (idx === -1) return { providerId: '', model: key };
  return {
    providerId: key.slice(0, idx),
    model: key.slice(idx + 1),
  };
}

function BackgroundModelGroup(props: { hide: Hide }) {
  const session = useSession();
  const [model, setModel] = createSignal('');
  const [providerId, setProviderId] = createSignal('');
  const [saving, setSaving] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);

  onMount(() => {
    getBackgroundModel()
      .then((pref) => {
        setModel(pref.model || '');
        setProviderId(pref.providerId || '');
      })
      .catch(() => {
        // Leave default ("Inherit from active session") on initial load failure.
      });
  });

  const enabledModels = createMemo(() =>
    session
      .models()
      .filter((m) => m.enabled)
      .sort((a, b) => a.name.localeCompare(b.name)),
  );

  // Detect duplicate display names across providers so we can disambiguate them.
  const duplicateNames = createMemo(() => {
    const counts = new Map<string, number>();
    for (const m of enabledModels()) {
      counts.set(m.name, (counts.get(m.name) ?? 0) + 1);
    }
    return counts;
  });

  const selectedKey = createMemo(() => {
    const m = model();
    if (!m) return '';
    const p = providerId();
    const list = enabledModels();
    const exact = list.find((item) => item.id === m && (!p || item.providerId === p));
    if (exact) return encodeModelKey(exact.providerId, exact.id);
    return encodeModelKey(p, m);
  });

  const options = createMemo(() => {
    const list = enabledModels();
    const dups = duplicateNames();
    const items = [
      { value: '', label: 'Inherit from active session' },
      ...list.map((m) => ({
        value: encodeModelKey(m.providerId, m.id),
        label: (dups.get(m.name) ?? 0) > 1 && m.providerId ? `${m.name} (${m.providerId})` : m.name,
      })),
    ];
    const currentKey = selectedKey();
    if (currentKey && !items.some((item) => item.value === currentKey)) {
      const known = session.models().find((item) => item.id === model() && (!providerId() || item.providerId === providerId()));
      items.push({
        value: currentKey,
        label: known ? known.name : model(),
      });
    }
    return items;
  });

  const selectedLabel = createMemo(() => {
    const m = model();
    if (!m) return 'Inherit from active session';
    const known = session.models().find((item) => item.id === m && (!providerId() || item.providerId === providerId()));
    return known?.name ?? m;
  });

  const apply = async (nextKey: string) => {
    const prevModel = model();
    const prevProvider = providerId();
    const next = decodeModelKey(nextKey);
    setModel(next.model);
    setProviderId(next.providerId);
    setSaving(true);
    setError(null);
    try {
      const saved = await setBackgroundModel({
        model: next.model,
        providerId: next.providerId,
      });
      setModel(saved.model || '');
      setProviderId(saved.providerId || '');
    } catch (e: any) {
      setModel(prevModel);
      setProviderId(prevProvider);
      setError(e?.message || 'Failed to save background model');
    } finally {
      setSaving(false);
    }
  };

  return (
    <Group
      id="background-tasks"
      title="Background tasks"
      icon={ICONS.background}
    >
      <Row
        label="Background model"
        helper="Choose a dedicated model for behind-the-scenes tasks, or inherit the active session's model."
        hidden={props.hide(
          'Background tasks',
          'Background model',
          'behind the scenes llm model',
          'auxiliary utility subagent title compaction memory summary indexing',
          selectedLabel(),
        )}
      >
        <div class="flex items-center gap-2">
          <Show when={saving()}>
            <Spinner class="w-4 h-4 text-[color:var(--text-muted)]" />
          </Show>
          <Select
            value={selectedKey()}
            options={options()}
            ariaLabel="Model for background tasks"
            disabled={saving()}
            onChange={(val) => apply(val)}
          />
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
