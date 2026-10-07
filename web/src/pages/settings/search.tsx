import { Show, For, createSignal, createEffect, onMount, type JSX } from 'solid-js';
import { useServer } from '../../context/server';
import {
  getSearchConfig,
  setSearchConfig,
  type SearchProvider,
  type SearchBrowser,
} from '../../api/client';
import {
  Group,
  Row,
  Switch,
  Select,
  TextField,
  Button,
  LinkAction,
  StatusChip,
  Banner,
  Mono,
  Spinner,
  fieldClass,
  matches,
  useShell,
} from './ui';

const ICONS = {
  globe: 'M12 21a9 9 0 100-18 9 9 0 000 18zm0 0c2.485 0 4.5-4.03 4.5-9S14.485 3 12 3 7.5 7.03 7.5 12s2.015 9 4.5 9zm-8.716-5.25h17.432M3.284 8.25h17.432',
};

// The providers on offer, as tabs. Native is the built-in engine; more can be
// added as their own tab without disturbing the others.
const TABS: Array<{ id: SearchProvider; label: string }> = [
  { id: 'native', label: 'Native' },
  { id: 'tavily', label: 'Tavily' },
];

// How the native engine leads. '' keeps the HTTP path in front with Safari
// behind it, which is the sane default; the others pin one engine.
const BROWSERS: Array<{ value: SearchBrowser; label: string }> = [
  { value: '', label: 'HTTP first (recommended)' },
  { value: 'native', label: 'HTTP only' },
  { value: 'safari', label: 'Safari first' },
];

export default function SearchSettings() {
  const server = useServer();
  const shell = useShell();

  createEffect(() => shell.report({ noun: 'settings' }));

  /** A row shows when the query appears anywhere a reader would look for it. */
  const hide = (...text: (string | undefined)[]) => !matches(shell.query(), ...text);

  const [enabled, setEnabled] = createSignal(true);
  const [provider, setProvider] = createSignal<SearchProvider>('native');
  // The native engine's knobs. Persisted per project; both are read when a
  // search runs, so a change needs a restart to reach the running backend.
  const [fetchTopK, setFetchTopK] = createSignal(4);
  const [pageChars, setPageChars] = createSignal(6000);
  const [browser, setBrowser] = createSignal<SearchBrowser>('');
  // The knobs past the engine itself stay folded away until asked for.
  const [advanced, setAdvanced] = createSignal(false);
  const [loading, setLoading] = createSignal(true);
  const [saving, setSaving] = createSignal(false);
  const [restartNeeded, setRestartNeeded] = createSignal(false);
  // Set when the config could not be read, so a failure is visible rather than
  // a blank screen sitting on defaults.
  const [loadError, setLoadError] = createSignal('');

  // Tavily credential state. dbKeySet mirrors the server's masked response
  // ('__SET__' means a key is stored); apiKey holds an unsaved edit; envKeySet
  // reports a TAVILY_API_KEY set in the server environment.
  const [dbKeySet, setDbKeySet] = createSignal(false);
  const [envKeySet, setEnvKeySet] = createSignal(false);
  const [apiKey, setApiKey] = createSignal('');
  const [keyStatus, setKeyStatus] = createSignal<{ ok: boolean; msg: string } | null>(null);
  // Flashed briefly after a provider/key change is applied live (no restart).
  const [applied, setApplied] = createSignal(false);
  const flashApplied = () => {
    setApplied(true);
    setTimeout(() => setApplied(false), 2200);
  };

  // A search for something folded away opens the fold so the hit is visible
  // rather than hidden behind a collapsed row. An empty query leaves it shut.
  createEffect(() => {
    const q = shell.query().trim();
    if (q && matches(q, 'Advanced', 'pages extract characters page length')) setAdvanced(true);
  });

  onMount(async () => {
    try {
      const cfg = await getSearchConfig();
      setEnabled(cfg.enabled);
      setProvider(cfg.provider ?? 'native');
      setFetchTopK(cfg.fetchTopK || 4);
      setPageChars(cfg.pageChars || 6000);
      setBrowser(cfg.browser ?? '');
      setDbKeySet(cfg.tavilyApiKey === '__SET__');
      setEnvKeySet(!!cfg.tavilyEnvKeySet);
    } catch (err) {
      // Keep the defaults on screen, but say the read failed.
      setLoadError(err instanceof Error ? err.message : 'Could not load the search settings');
    } finally {
      setLoading(false);
    }
  });

  // The key value to send: a freshly typed key wins; otherwise the '__SET__'
  // sentinel preserves the stored one, and '' when none is stored. So a
  // provider change (which also calls save) never wipes a saved key.
  const resolveKey = () => {
    const typed = apiKey().trim();
    if (typed !== '') return typed;
    return dbKeySet() ? '__SET__' : '';
  };

  // save persists the full config and signals how the change took effect:
  //   restart — a native knob or the enable toggle; these are read when the
  //             backend is built, so they need a restart
  //   live    — a provider/key change; the backend is swapped in place, applied now
  const save = async (opts: { restart?: boolean; live?: boolean; key?: string }) => {
    setSaving(true);
    try {
      const result = await setSearchConfig({
        enabled: enabled(),
        provider: provider(),
        tavilyApiKey: opts.key ?? resolveKey(),
        fetchTopK: fetchTopK(),
        pageChars: pageChars(),
        browser: browser(),
      });
      setDbKeySet(result.tavilyApiKey === '__SET__');
      if (opts.restart) setRestartNeeded(true);
      if (opts.live) flashApplied();
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

  // A native knob changed. Each takes effect on the next restart, and the input
  // is clamped to the range the store accepts so the field never sends a value
  // the server would reject.
  const changeKnob = async (which: 'topK' | 'chars', raw: string) => {
    const n = Number(raw);
    if (!Number.isFinite(n)) return;
    if (which === 'topK') {
      const v = Math.min(10, Math.max(1, Math.round(n)));
      setFetchTopK(v);
    } else {
      const v = Math.min(20000, Math.max(1000, Math.round(n)));
      setPageChars(v);
    }
    try {
      await save({ restart: true });
    } catch {
      // the flashed value stays; the next read reconciles it
    }
  };

  const changeBrowser = async (next: string) => {
    const prev = browser();
    setBrowser(next as SearchBrowser);
    try {
      await save({ restart: true });
    } catch {
      setBrowser(prev);
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
        <Show when={loadError()}>
          <div data-setting class="px-3 pt-3">
            <Banner tone="danger">{loadError()}</Banner>
          </div>
        </Show>

        <Row
          label="Enable web search"
          helper={
            <>
              Adds <Mono>web_search</Mono>, <Mono>fetch_page</Mono> and <Mono>deep_search</Mono> to the
              agent's toolset. Takes effect after ogcode restarts.
            </>
          }
          hidden={hide('Enable web search', 'deep_search web_search fetch_page internet research tools')}
        >
          <Show when={!loading()} fallback={<Spinner class="w-4 h-4 text-[color:var(--text-muted)]" />}>
            <Switch checked={enabled()} disabled={saving()} onChange={toggle} label="Enable web search" />
          </Show>
        </Row>

        {/* One provider at a time, chosen by the strip — the same shape the
            Models page uses, so a second provider is a tab away rather than a
            stacked section. The strip sticks under the page header. */}
        <Show when={!loading() && enabled()}>
          <div
            class="sticky top-0 z-10 -mx-3 sm:-mx-6 px-3 sm:px-6 pt-1 pb-3
                   bg-[color:var(--bg-base)]"
          >
            <div
              role="tablist"
              aria-label="Search providers"
              class="flex gap-1 overflow-x-auto hide-scrollbar border-b border-[color:var(--border-subtle)]"
            >
              <For each={TABS}>
                {(tab) => {
                  const on = () => tab.id === provider();
                  return (
                    <button
                      type="button"
                      role="tab"
                      aria-selected={on()}
                      onClick={() => changeProvider(tab.id)}
                      class={`relative flex items-center gap-1.5 h-8 px-2.5 rounded-md text-ui whitespace-nowrap shrink-0
                              transition-colors duration-150
                        ${
                          on()
                            ? 'text-[color:var(--text-primary)] font-medium'
                            : 'text-[color:var(--text-secondary)] hover:text-[color:var(--text-primary)] hover:bg-[color:var(--bg-hover)]/50'
                        }`}
                    >
                      <span
                        class={`w-1.5 h-1.5 rounded-full shrink-0 ${
                          on() ? 'bg-[color:var(--accent)]' : 'bg-[color:var(--border-strong)]'
                        }`}
                      />
                      {tab.label}
                      <Show when={on()}>
                        {/* The accent underline rides the bottom border, not the
                            tab: the tab stays a quiet shape and the marker reads
                            as part of the rule it interrupts. */}
                        <span class="absolute left-2 right-2 -bottom-px h-0.5 rounded-full bg-[color:var(--accent)]" />
                      </Show>
                    </button>
                  );
                }}
              </For>
            </div>
          </div>

          <Show when={provider() === 'native'}>
            <Row
              label="Advanced"
              helper="How the native engine fetches a page. The defaults suit most projects."
              onClick={() => setAdvanced(!advanced())}
              expanded={advanced()}
              hidden={hide('Advanced', 'pages extract characters page length engine browser native')}
            >
              <span class="text-meta text-[color:var(--text-tertiary)]">{advanced() ? 'Hide' : 'Show'}</span>
            </Row>

            <Show when={advanced()}>
              <Row
                label="Pages to extract"
                helper="How many ranked pages the native engine reads in full. Applies after ogcode restarts."
                hidden={hide('Pages to extract', 'fetch top k results depth native')}
              >
                <input
                  type="number"
                  min="1"
                  max="10"
                  value={fetchTopK()}
                  disabled={saving()}
                  aria-label="Pages to extract"
                  onChange={(e) => changeKnob('topK', e.currentTarget.value)}
                  class={`${fieldClass} w-[6rem] tabular-nums`}
                />
              </Row>

              <Row
                label="Characters per page"
                helper="How much of each page is handed to the agent. Applies after ogcode restarts."
                hidden={hide('Characters per page', 'page chars length extract native')}
              >
                <input
                  type="number"
                  min="1000"
                  max="20000"
                  step="500"
                  value={pageChars()}
                  disabled={saving()}
                  aria-label="Characters per page"
                  onChange={(e) => changeKnob('chars', e.currentTarget.value)}
                  class={`${fieldClass} w-[7rem] tabular-nums`}
                />
              </Row>

              <Row
                label="Which engine leads"
                helper="Safari drives a real browser, which handles JavaScript-heavy pages the HTTP path cannot. Applies after ogcode restarts."
                hidden={hide('Which engine leads', 'safari browser http engine native')}
              >
                <Select
                  value={browser()}
                  options={BROWSERS}
                  disabled={saving()}
                  ariaLabel="Which engine leads"
                  onChange={changeBrowser}
                />
              </Row>
            </Show>
          </Show>

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
              hidden={hide('Tavily API key', 'credentials token tvly search provider')}
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
              </div>
              <div class="mt-2 flex items-center gap-3 flex-wrap">
                <Show when={keyStatus()}>
                  <span
                    class="text-micro"
                    style={{ color: keyStatus()!.ok ? 'var(--success)' : 'var(--danger)' }}
                  >
                    {keyStatus()!.msg}
                  </span>
                </Show>
                <Show when={!keyStatus() && dbKeySet()}>
                  <span class="text-micro text-[color:var(--text-tertiary)]">Key stored in ogcode.</span>
                </Show>
                <Show when={!keyStatus() && !dbKeySet() && envKeySet()}>
                  <span class="text-micro text-[color:var(--text-tertiary)]">
                    Using <Mono>TAVILY_API_KEY</Mono> from the server environment.
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

        <Show when={!loading() && restartNeeded()}>
          <div data-setting class="px-3 pt-3">
            <Banner tone="warn">Restart the server for this change to take effect.</Banner>
          </div>
        </Show>

        <Show when={!loading() && enabled() && !restartNeeded() && !server.searchRunning()}>
          <div data-setting class="px-3 pt-3">
            <Banner tone="danger">
              The search backend did not start. Check the server logs, then restart ogcode.
            </Banner>
          </div>
        </Show>
      </Group>
    </>
  );
}
