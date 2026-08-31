import { Show, For, createMemo, createSignal, onMount, createEffect, type JSX } from 'solid-js';
import { useNavigate } from '@solidjs/router';
import { useServer } from '../../context/server';
import { useSession } from '../../context/session';
import { useTheme } from '../../context/theme';
import { getSearchConfig, setSearchConfig } from '../../api/client';
import {
  Group,
  Row,
  Switch,
  Slider,
  Select,
  Value,
  StatusChip,
  Banner,
  Kbd,
  Mono,
  Spinner,
  CopyButton,
  fieldClass,
  matches,
  useShell,
} from './ui';

const PROVIDER_LABELS: Record<string, string> = {
  anthropic: 'Anthropic',
  openai: 'OpenAI',
  openrouter: 'OpenRouter',
  ollama: 'Ollama',
  google: 'Google',
  mistral: 'Mistral',
};

const ICONS = {
  workspace: 'M2.25 12.75V12A2.25 2.25 0 014.5 9.75h15A2.25 2.25 0 0121.75 12v.75m-8.69-6.44l-2.12-2.12a1.5 1.5 0 00-1.061-.44H4.5A2.25 2.25 0 002.25 6v12a2.25 2.25 0 002.25 2.25h15A2.25 2.25 0 0021.75 18V9a2.25 2.25 0 00-2.25-2.25h-5.379a1.5 1.5 0 01-1.06-.44z',
  palette: 'M9.53 16.122a3 3 0 00-5.78 1.128 2.25 2.25 0 01-2.4 2.245 4.5 4.5 0 008.4-2.245c0-.399-.078-.78-.22-1.128zm0 0a15.998 15.998 0 003.388-1.62m-5.043-.025a15.994 15.994 0 011.622-3.395m3.42 3.42a15.995 15.995 0 004.764-4.648l3.876-5.814a1.151 1.151 0 00-1.597-1.597L14.146 6.32a15.996 15.996 0 00-4.649 4.763m3.42 3.42a6.776 6.776 0 00-3.42-3.42',
  globe: 'M12 21a9 9 0 100-18 9 9 0 000 18zm0 0c2.485 0 4.5-4.03 4.5-9S14.485 3 12 3 7.5 7.03 7.5 12s2.015 9 4.5 9zm-8.716-5.25h17.432M3.284 8.25h17.432',
  research: 'M10.5 6a7.5 7.5 0 107.5 7.5h-7.5V6z',
  models: 'M8.25 3v1.5M4.5 8.25H3m18 0h-1.5M4.5 12H3m18 0h-1.5m-15 3.75H3m18 0h-1.5M8.25 19.5V21M12 3v1.5m0 15V21m3.75-18v1.5m0 15V21m-9-1.5h10.5a2.25 2.25 0 002.25-2.25V6.75a2.25 2.25 0 00-2.25-2.25H6.75A2.25 2.25 0 004.5 6.75v10.5a2.25 2.25 0 002.25 2.25zm.75-12h9v9h-9v-9z',
  keyboard: 'M6.75 3.75h.008v.008H6.75v-.008zM6.75 7.5h.008v.008H6.75V7.5zm0 3.75h.008v.008H6.75v-.008zM10.5 3.75h.008v.008H10.5v-.008zM10.5 7.5h.008v.008H10.5V7.5zm0 3.75h.008v.008H10.5v-.008zM14.25 3.75h.008v.008h-.008v-.008zM14.25 7.5h.008v.008h-.008V7.5zm0 3.75h.008v.008h-.008v-.008zM17.25 3.75h.008v.008h-.008v-.008zM17.25 7.5h.008v.008h-.008V7.5zm0 3.75h.008v.008h-.008v-.008zM4.5 18.75h15a.75.75 0 00.75-.75v-1.5a.75.75 0 00-.75-.75h-15a.75.75 0 00-.75.75v1.5a.75.75 0 00.75.75z',
  hotkey: 'M15.75 15.75V18m-7.5-6.75h.008v.008H8.25v-.008zM12 15.75V18m3.75-3.75V18M4.5 4.5h15a1.5 1.5 0 011.5 1.5v12a1.5 1.5 0 01-1.5 1.5h-15A1.5 1.5 0 013 18V6a1.5 1.5 0 011.5-1.5z',
};

// Defaults, matching session.SearchConfig in Go.
const DEFAULTS = { fetchTopK: 4, pageChars: 6000, primaryColor: '#5e6ad2' };
const BOUNDS = {
  fetchTopK: { min: 1, max: 10 },
  pageChars: { min: 1000, max: 20000, step: 500 },
};

export default function GeneralSettings() {
  const server = useServer();
  const session = useSession();
  const navigate = useNavigate();
  const shell = useShell();

  createEffect(() => shell.report({ noun: 'settings' }));

  /** A row shows when the query appears anywhere a reader would look for it. */
  const hide = (...text: (string | undefined)[]) => !matches(shell.query(), ...text);

  const stats = createMemo(() => {
    const all = session.models();
    return {
      total: all.length,
      enabled: all.filter((m) => m.enabled).length,
      providers: new Set(all.map((m) => m.providerId)).size,
      custom: all.filter((m) => m.isCustom).length,
    };
  });

  const defaultModel = createMemo(() => {
    const all = session.models();
    return all.find((m) => m.default && m.enabled) || all.find((m) => m.enabled);
  });

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

      <SearchGroups hide={hide} />

      <Group id="models" title="Models" icon={ICONS.models}>
        <Row
          label="Default model"
          helper="What a new session starts with."
          onClick={() => navigate('/settings/models')}
          hidden={hide('Default model', 'provider picker')}
        >
          <Show when={defaultModel()} fallback={<Value mono={false} tone="muted">None enabled</Value>}>
            <span class="flex items-center gap-2 min-w-0">
              <Value>{defaultModel()!.name}</Value>
              <span class="text-micro text-[color:var(--text-muted)] hidden sm:inline">
                {PROVIDER_LABELS[defaultModel()!.providerId] || defaultModel()!.providerId}
              </span>
            </span>
          </Show>
        </Row>
        <Row
          label="Catalogue"
          helper="How many models ogcode knows about, and how many reach the picker."
          stacked
          hidden={hide('Catalogue', 'how many models providers custom enabled')}
        >
          <div class="flex flex-wrap gap-x-10 gap-y-4">
            <Stat label="Enabled" value={stats().enabled} accent />
            <Stat label="Available" value={stats().total} />
            <Stat label="Providers" value={stats().providers} />
            <Stat label="Custom" value={stats().custom} />
          </div>
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

function Stat(props: { label: string; value: number; accent?: boolean }) {
  return (
    <div>
      <div
        class="text-[1.125rem] font-semibold tabular-nums leading-none"
        style={{ color: props.accent ? 'var(--accent)' : 'var(--text-primary)' }}
      >
        {props.value}
      </div>
      <div class="mt-1 text-micro text-[color:var(--text-muted)]">{props.label}</div>
    </div>
  );
}

type Hide = (...text: (string | undefined)[]) => boolean;

// ---------------------------------------------------------------------------
// Appearance
// ---------------------------------------------------------------------------

const PRESETS = [
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

function ThemeGroup(props: { hide: Hide }) {
  const themeCtx = useTheme();
  const [saving, setSaving] = createSignal(false);
  const [error, setError] = createSignal('');
  const [draft, setDraft] = createSignal<string | null>(null);

  const isValidHex = (s: string) => /^#[0-9a-fA-F]{6}$/.test(s) || /^#[0-9a-fA-F]{3}$/.test(s);
  const current = () => draft() ?? themeCtx.primaryColor();

  const apply = async (hex: string) => {
    setDraft(hex);
    setError('');
    if (!isValidHex(hex)) {
      setError('Use a six-digit hex colour, for example #5e6ad2.');
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
      description="One colour drives the whole palette. Saved per project directory, so each workspace keeps its own identity."
    >
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

      <Row
        label="Preview"
        helper="How the derived palette reads on the controls it colours."
        stacked
        hidden={props.hide('Preview', 'palette derived colours theme')}
      >
        <div class="flex flex-wrap items-center gap-2">
          <span class="h-8 px-3 inline-flex items-center rounded-[3px] text-meta font-medium bg-[color:var(--accent)] text-[color:var(--on-primary)] shadow-sm">
            Primary
          </span>
          <span class="h-8 px-3 inline-flex items-center rounded-[3px] text-meta font-medium bg-[color:var(--accent-soft)] text-[color:var(--accent)]">
            Tonal
          </span>
          <span class="h-8 px-3 inline-flex items-center gap-2 rounded-lg text-meta border border-[color:var(--border-default)] bg-[color:var(--bg-elevated)] text-[color:var(--text-secondary)]">
            <span class="w-1.5 h-1.5 rounded-full bg-[color:var(--accent)]" />
            Active
          </span>
          <span
            class="h-8 px-3 inline-flex items-center rounded-[3px] text-meta font-mono text-[color:var(--accent)] border border-[color:var(--accent)]"
            style={{ 'box-shadow': '0 0 0 3px var(--accent-ring)' }}
          >
            Focus
          </span>
        </div>
      </Row>
    </Group>
  );
}

// ---------------------------------------------------------------------------
// Web search + deep research
// ---------------------------------------------------------------------------

function SearchGroups(props: { hide: Hide }) {
  const server = useServer();
  const [enabled, setEnabled] = createSignal(true);
  const [fetchTopK, setFetchTopK] = createSignal(DEFAULTS.fetchTopK);
  const [pageChars, setPageChars] = createSignal(DEFAULTS.pageChars);
  const [loading, setLoading] = createSignal(true);
  const [saving, setSaving] = createSignal(false);
  const [restartNeeded, setRestartNeeded] = createSignal(false);
  const [paramsSaved, setParamsSaved] = createSignal(false);

  onMount(async () => {
    try {
      const cfg = await getSearchConfig();
      setEnabled(cfg.enabled);
      if (cfg.fetchTopK) setFetchTopK(cfg.fetchTopK);
      if (cfg.pageChars) setPageChars(cfg.pageChars);
    } catch {
      // defaults stay in place
    } finally {
      setLoading(false);
    }
  });

  // save persists the full config. Toggling search on or off is applied when
  // the tools are registered at startup, so it needs a restart; research
  // parameters apply live on the next deep_search and show a transient "Saved".
  const save = async (opts: { restart: boolean }) => {
    setSaving(true);
    try {
      await setSearchConfig({ enabled: enabled(), fetchTopK: fetchTopK(), pageChars: pageChars() });
      if (opts.restart) {
        setRestartNeeded(true);
      } else {
        setParamsSaved(true);
        setTimeout(() => setParamsSaved(false), 1600);
      }
    } finally {
      setSaving(false);
    }
  };

  const toggle = async (next: boolean) => {
    setEnabled(next);
    try {
      await save({ restart: true });
    } catch {
      setEnabled(!next);
    }
  };

  // Sliders fire continuously while dragging; only the release writes to the
  // server, so one drag is one request instead of forty.
  const commit = async (setter: (v: number) => void, prev: number, v: number) => {
    setter(v);
    try {
      await save({ restart: false });
    } catch {
      setter(prev);
    }
  };

  return (
    <>
      <Group
        id="search"
        title="Web search"
        icon={ICONS.globe}
        description="Lets the build and note agents research live information. Search ships inside ogcode — there is nothing to install."
      >
        <Row
          label="Enable web search"
          helper={
            <>
              Adds <Mono>web_search</Mono>, <Mono>fetch_page</Mono> and <Mono>deep_search</Mono> to the
              agent's toolset. Takes effect after ogcode restarts.
            </>
          }
          hidden={props.hide('Enable web search', 'deep_search web_search fetch_page internet research tools')}
        >
          <Show when={!loading()} fallback={<Spinner class="w-4 h-4 text-[color:var(--text-muted)]" />}>
            <Switch checked={enabled()} disabled={saving()} onChange={toggle} label="Enable web search" />
          </Show>
        </Row>

        <Show when={!loading() && (restartNeeded() || enabled())}>
          <Row label="Status" stacked hidden={props.hide('Status', 'web search backend running restart')}>
            <Show
              when={restartNeeded()}
              fallback={
                <Show
                  when={server.searchRunning()}
                  fallback={
                    <Banner tone="danger">
                      The search backend did not start. Check the server logs, then restart ogcode.
                    </Banner>
                  }
                >
                  <Banner tone="ok">
                    Search is active — <Mono>web_search</Mono>, <Mono>fetch_page</Mono> and{' '}
                    <Mono>deep_search</Mono> are ready.
                  </Banner>
                </Show>
              }
            >
              <Banner tone="warn">Restart the server for this change to take effect.</Banner>
            </Show>
          </Row>
        </Show>
      </Group>

      {/* Tuning is its own card: it saves live, while the switch above needs a
          restart — folding them together misled people about what was in
          effect. */}
      <Show when={!loading() && enabled()}>
        <Group
          id="research"
          title="Deep research"
          icon={ICONS.research}
          description={
            <>
              Shapes the <Mono>deep_search</Mono> pipeline. Higher values dig deeper, lower values answer
              faster. Changes apply to the next search.
            </>
          }
          action={
            <Show when={paramsSaved()}>
              <StatusChip tone="ok">Saved</StatusChip>
            </Show>
          }
        >
          <Row
            label="Pages fetched"
            helper="Top-ranked results read in full before the agent answers."
            stacked
            hidden={props.hide('Pages fetched', 'deep research top results read in full')}
          >
            <Slider
              value={fetchTopK()}
              min={BOUNDS.fetchTopK.min}
              max={BOUNDS.fetchTopK.max}
              disabled={saving()}
              ariaLabel="Pages fetched"
              format={(v) => `${v} page${v === 1 ? '' : 's'}`}
              onInput={setFetchTopK}
              onCommit={(v) => commit(setFetchTopK, fetchTopK(), v)}
            />
          </Row>
          <Row
            label="Characters per page"
            helper="How much of each fetched page feeds the final synthesis."
            stacked
            hidden={props.hide('Characters per page', 'deep research synthesis length')}
          >
            <Slider
              value={pageChars()}
              min={BOUNDS.pageChars.min}
              max={BOUNDS.pageChars.max}
              step={BOUNDS.pageChars.step}
              disabled={saving()}
              ariaLabel="Characters per page"
              format={(v) => v.toLocaleString()}
              onInput={setPageChars}
              onCommit={(v) => commit(setPageChars, pageChars(), v)}
            />
          </Row>
        </Group>
      </Show>
    </>
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
