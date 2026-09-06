import { Show, For, createMemo, createSignal, onMount, createEffect, type JSX } from 'solid-js';
import { useServer } from '../../context/server';
import { useSession } from '../../context/session';
import { useTheme } from '../../context/theme';
import { getSearchConfig, setSearchConfig, validateSearchKey, type SearchProvider } from '../../api/client';
import {
  Group,
  Row,
  Switch,
  Slider,
  Select,
  TextField,
  Button,
  LinkAction,
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

const ICONS = {
  workspace: 'M2.25 12.75V12A2.25 2.25 0 014.5 9.75h15A2.25 2.25 0 0121.75 12v.75m-8.69-6.44l-2.12-2.12a1.5 1.5 0 00-1.061-.44H4.5A2.25 2.25 0 002.25 6v12a2.25 2.25 0 002.25 2.25h15A2.25 2.25 0 0021.75 18V9a2.25 2.25 0 00-2.25-2.25h-5.379a1.5 1.5 0 01-1.06-.44z',
  palette: 'M9.53 16.122a3 3 0 00-5.78 1.128 2.25 2.25 0 01-2.4 2.245 4.5 4.5 0 008.4-2.245c0-.399-.078-.78-.22-1.128zm0 0a15.998 15.998 0 003.388-1.62m-5.043-.025a15.994 15.994 0 011.622-3.395m3.42 3.42a15.995 15.995 0 004.764-4.648l3.876-5.814a1.151 1.151 0 00-1.597-1.597L14.146 6.32a15.996 15.996 0 00-4.649 4.763m3.42 3.42a6.776 6.776 0 00-3.42-3.42',
  globe: 'M12 21a9 9 0 100-18 9 9 0 000 18zm0 0c2.485 0 4.5-4.03 4.5-9S14.485 3 12 3 7.5 7.03 7.5 12s2.015 9 4.5 9zm-8.716-5.25h17.432M3.284 8.25h17.432',
  research: 'M10.5 6a7.5 7.5 0 107.5 7.5h-7.5V6z',
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
  const shell = useShell();

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

      <SearchGroups hide={hide} />

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
    </Group>
  );
}

// ---------------------------------------------------------------------------
// Web search + deep research
// ---------------------------------------------------------------------------

const SEARCH_PROVIDERS: Array<{ value: SearchProvider; label: string }> = [
  { value: 'native', label: 'Native (built-in)' },
  { value: 'tavily', label: 'Tavily' },
];

function SearchGroups(props: { hide: Hide }) {
  const server = useServer();
  const [enabled, setEnabled] = createSignal(true);
  const [provider, setProvider] = createSignal<SearchProvider>('native');
  const [fetchTopK, setFetchTopK] = createSignal(DEFAULTS.fetchTopK);
  const [pageChars, setPageChars] = createSignal(DEFAULTS.pageChars);
  const [loading, setLoading] = createSignal(true);
  const [saving, setSaving] = createSignal(false);
  const [restartNeeded, setRestartNeeded] = createSignal(false);
  const [paramsSaved, setParamsSaved] = createSignal(false);

  // Tavily credential state. dbKeySet mirrors the server's masked response
  // ('__SET__' means a key is stored); apiKey holds an unsaved edit; envKeySet
  // reports a TAVILY_API_KEY set in the server environment.
  const [dbKeySet, setDbKeySet] = createSignal(false);
  const [envKeySet, setEnvKeySet] = createSignal(false);
  const [apiKey, setApiKey] = createSignal('');
  const [testing, setTesting] = createSignal(false);
  const [keyStatus, setKeyStatus] = createSignal<{ ok: boolean; msg: string } | null>(null);
  // Flashed briefly after a provider/key change is applied live (no restart).
  const [applied, setApplied] = createSignal(false);
  const flashApplied = () => {
    setApplied(true);
    setTimeout(() => setApplied(false), 2200);
  };

  onMount(async () => {
    try {
      const cfg = await getSearchConfig();
      setEnabled(cfg.enabled);
      setProvider(cfg.provider ?? 'native');
      setDbKeySet(cfg.tavilyApiKey === '__SET__');
      setEnvKeySet(!!cfg.tavilyEnvKeySet);
      if (cfg.fetchTopK) setFetchTopK(cfg.fetchTopK);
      if (cfg.pageChars) setPageChars(cfg.pageChars);
    } catch {
      // defaults stay in place
    } finally {
      setLoading(false);
    }
  });

  // The key value to send: a freshly typed key wins; otherwise the '__SET__'
  // sentinel preserves the stored one, and '' when none is stored. So the
  // deep-research sliders (which also call save) never wipe a saved key.
  const resolveKey = () => {
    const typed = apiKey().trim();
    if (typed !== '') return typed;
    return dbKeySet() ? '__SET__' : '';
  };

  // save persists the full config and signals how the change took effect:
  //   restart — the enable toggle; it changes which tools exist, so it needs one
  //   live    — a provider/key change; the backend is swapped in place, applied now
  //   params  — the deep-research knobs; read live on the next deep_search
  const save = async (opts: { restart?: boolean; live?: boolean; params?: boolean; key?: string }) => {
    setSaving(true);
    try {
      const result = await setSearchConfig({
        enabled: enabled(),
        provider: provider(),
        tavilyApiKey: opts.key ?? resolveKey(),
        fetchTopK: fetchTopK(),
        pageChars: pageChars(),
      });
      setDbKeySet(result.tavilyApiKey === '__SET__');
      if (opts.restart) setRestartNeeded(true);
      if (opts.live) flashApplied();
      if (opts.params) {
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

  const changeProvider = async (next: string) => {
    const prev = provider();
    setProvider(next as SearchProvider);
    setKeyStatus(null);
    try {
      await save({ live: true });
    } catch {
      setProvider(prev);
    }
  };

  const saveKey = async () => {
    setKeyStatus(null);
    try {
      await save({ live: true });
      setApiKey('');
      setKeyStatus({ ok: true, msg: 'Saved — applied now.' });
    } catch {
      setKeyStatus({ ok: false, msg: 'Could not save. Is the ogcode server still running?' });
    }
  };

  const removeKey = async () => {
    if (!confirm('Remove the stored Tavily API key from ogcode?')) return;
    setKeyStatus(null);
    setApiKey('');
    try {
      await save({ live: true, key: '' });
    } catch {
      setKeyStatus({ ok: false, msg: 'Could not remove the key.' });
    }
  };

  const testKey = async () => {
    setTesting(true);
    setKeyStatus(null);
    try {
      const r = await validateSearchKey(resolveKey());
      setKeyStatus(r.ok ? { ok: true, msg: 'Key works — Tavily accepted it.' } : { ok: false, msg: r.error || 'Tavily rejected the key.' });
    } catch {
      setKeyStatus({ ok: false, msg: 'Could not reach the server.' });
    } finally {
      setTesting(false);
    }
  };

  // Sliders fire continuously while dragging; only the release writes to the
  // server, so one drag is one request instead of forty.
  const commit = async (setter: (v: number) => void, prev: number, v: number) => {
    setter(v);
    try {
      await save({ params: true });
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
        action={
          <Show when={applied()}>
            <StatusChip tone="ok">Applied</StatusChip>
          </Show>
        }
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

        <Show when={!loading() && enabled()}>
          <Row
            label="Search provider"
            helper={
              <>
                Native runs inside ogcode with nothing to install. Tavily routes{' '}
                <Mono>web_search</Mono> and <Mono>fetch_page</Mono> through your Tavily account,
                and falls back to native if a call fails. Applies immediately — no restart needed.
              </>
            }
            hidden={props.hide('Search provider', 'tavily native third party engine')}
          >
            <Select
              value={provider()}
              options={SEARCH_PROVIDERS}
              disabled={saving()}
              ariaLabel="Search provider"
              onChange={changeProvider}
            />
          </Row>

          <Show when={provider() === 'tavily'}>
            <Row
              label="Tavily API key"
              helper={
                <Show
                  when={dbKeySet()}
                  fallback={
                    <>
                      Paste your <Mono>tvly-…</Mono> key — Tavily uses a static API key, so there
                      is no sign-in to complete. <Mono>TAVILY_API_KEY</Mono> is used when that
                      variable is set. Applies immediately — no restart needed.
                    </>
                  }
                >
                  <>Leave blank to keep the stored key. Applies immediately — no restart needed.</>
                </Show>
              }
              stacked
              hidden={props.hide('Tavily API key', 'credentials token tvly search provider')}
            >
              <div class="flex items-center gap-2 flex-wrap">
                <div class="flex-1 min-w-[14rem]">
                  <TextField
                    password
                    mono
                    value={apiKey()}
                    onInput={setApiKey}
                    onEnter={saveKey}
                    disabled={saving()}
                    ariaLabel="Tavily API key"
                    placeholder={
                      dbKeySet()
                        ? 'leave blank to keep the saved key'
                        : envKeySet()
                          ? 'leave blank to use TAVILY_API_KEY'
                          : 'tvly-…'
                    }
                  />
                </div>
                <Button onClick={saveKey} disabled={saving()}>{saving() ? 'Saving…' : 'Save'}</Button>
                <Button
                  variant="outlined"
                  onClick={testKey}
                  disabled={testing() || saving() || (!apiKey().trim() && !dbKeySet() && !envKeySet())}
                >
                  {testing() ? 'Testing…' : 'Test key'}
                </Button>
              </div>
              <div class="mt-2 flex items-center gap-3 flex-wrap">
                <Show when={envKeySet()}>
                  <StatusChip tone="ok">Key set via TAVILY_API_KEY</StatusChip>
                </Show>
                <Show when={dbKeySet()}>
                  <StatusChip tone="ok">Key stored in ogcode</StatusChip>
                </Show>
                <Show when={keyStatus()}>
                  <span
                    class="text-micro"
                    style={{ color: keyStatus()!.ok ? 'var(--success)' : 'var(--danger)' }}
                  >
                    {keyStatus()!.msg}
                  </span>
                </Show>
                <Show when={dbKeySet()}>
                  <LinkAction onClick={removeKey}>Remove stored key</LinkAction>
                </Show>
                <LinkAction href="https://app.tavily.com">Get a key at tavily.com</LinkAction>
              </div>
            </Row>
          </Show>
        </Show>

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
