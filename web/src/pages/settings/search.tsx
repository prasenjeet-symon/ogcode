import { Show, For, createSignal, createEffect, on, onMount, type JSX } from 'solid-js';
import { useServer } from '../../context/server';
import {
  fetchAPI,
  getSearchConfig,
  setSearchConfig,
  type SearchProvider,
  type SearchBrowser,
  type SearchConfig,
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
  Value,
  Spinner,
  fieldClass,
  matches,
  useShell,
} from './ui';

const ICONS = {
  globe: 'M12 21a9 9 0 100-18 9 9 0 000 18zm0 0c2.485 0 4.5-4.03 4.5-9S14.485 3 12 3 7.5 7.03 7.5 12s2.015 9 4.5 9zm-8.716-5.25h17.432M3.284 8.25h17.432',
  // A stack of disks: the cache is a store, not a page of web results.
  cache: 'M4 6c0-1.657 3.582-3 8-3s8 1.343 8 3-3.582 3-8 3-8-1.343-8-3zm0 0v12c0 1.657 3.582 3 8 3s8-1.343 8-3V6M4 12c0 1.657 3.582 3 8 3s8-1.343 8-3',
};

// A minute count in the words people use for it: 1440 reads as "24 hours"
// rather than a number to convert in your head, and a week as "7 days". Only
// whole hours and whole days get named; anything else stays in minutes.
function humanMinutes(min: number): string {
  const n = Math.round(min);
  if (!Number.isFinite(n) || n <= 0) return 'not set';
  const days = n / 1440;
  if (Number.isInteger(days) && days >= 2) return `${days} days`;
  if (n % 60 === 0) {
    const hours = n / 60;
    return `${hours} ${hours === 1 ? 'hour' : 'hours'}`;
  }
  return `${n} min`;
}

function humanBytes(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return '0 B';
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(n < 10240 ? 1 : 0)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)} MB`;
  return `${(n / (1024 * 1024 * 1024)).toFixed(1)} GB`;
}

// What GET /api/search/cache reports. Its TTLs are seconds, which is why they
// are not read through humanMinutes above.
interface WebCacheStats {
  enabled: boolean;
  entries: number;
  bytes: number;
  hits: number;
  misses: number;
  evictions: number;
  ttlSeconds: { search: number; page: number; answer: number };
}

// The cache knobs POST /api/search/config carries beside the search ones. The
// TTLs are minutes; the server clamps each to 1..10080 (a week).
type CacheConfig = {
  cacheEnabled: boolean;
  cacheSearchTTL: number;
  cachePageTTL: number;
  cacheAnswerTTL: number;
};

// The providers on offer, as tabs. Native is the built-in engine; more can be
// added as their own tab without disturbing the others.
const TABS: Array<{ id: SearchProvider; label: string }> = [
  { id: 'native', label: 'Native' },
  { id: 'tavily', label: 'Tavily' },
  { id: 'youcom', label: 'You.com' },
];

// How the native engine leads. '' keeps the HTTP path in front with the
// browser behind it, which is the sane default; the others pin one engine.
// Each browser is offered only on the machine that can drive it — the server's
// host: Safari on macOS, Chrome/Edge on Windows.
const BROWSERS: Array<{ value: SearchBrowser; label: string }> = [
  { value: '', label: 'HTTP first (recommended)' },
  { value: 'native', label: 'HTTP only' },
  { value: 'safari', label: 'Safari first (macOS only)' },
  { value: 'chrome', label: 'Chrome or Edge first (Windows only)' },
];

export default function SearchSettings() {
  const server = useServer();
  const shell = useShell();

  // Browser search drives a real browser on the machine running the server, so
  // each browser option belongs only where the server's host can drive it
  // (osascript on macOS, a Chrome/Edge binary on Windows).
  const isMac = () => server.os() === 'darwin';
  const isWindows = () => server.os() === 'windows';
  // A browser stays listed when it is the stored value, so the select always
  // has a matching option even if the config arrived from another OS.
  const browsers = () =>
    BROWSERS.filter(
      (b) =>
        (b.value !== 'safari' || isMac() || browser() === 'safari') &&
        (b.value !== 'chrome' || isWindows() || browser() === 'chrome'),
    );

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
  // The web cache: one switch, three TTLs in minutes, and the counters the
  // cache itself reports. The TTLs are read when the backend is built, so a
  // change needs a restart, the same as the native knobs above.
  const [cacheEnabled, setCacheEnabled] = createSignal(true);
  const [cacheSearchTTL, setCacheSearchTTL] = createSignal(60);
  const [cachePageTTL, setCachePageTTL] = createSignal(1440);
  const [cacheAnswerTTL, setCacheAnswerTTL] = createSignal(360);
  const [cacheStats, setCacheStats] = createSignal<WebCacheStats | null>(null);
  const [cacheStatsError, setCacheStatsError] = createSignal('');
  const [clearing, setClearing] = createSignal(false);
  const [clearStatus, setClearStatus] = createSignal('');
  // The knobs past the engine itself stay folded away until asked for.
  const [advanced, setAdvanced] = createSignal(false);
  // The Safari setup steps stay folded behind "Show me how" until asked for.
  const [showHow, setShowHow] = createSignal(false);
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
    if (q && matches(q, 'Show me how', 'safari setup automation permissions apple events javascript macos')) {
      setShowHow(true);
    }
  });

  // Choosing Safari opens the setup steps straight away — the two permissions
  // are what make it work, so they should not be hidden on a fresh selection.
  // The effect runs on a stored 'safari' loaded from config too.
  createEffect(on(browser, (b) => {
    if (b === 'safari') setShowHow(true);
  }));

  // The stats line stands on its own, so a failed config read still leaves it
  // saying what the cache holds.
  const loadCacheStats = async () => {
    try {
      setCacheStats(await fetchAPI<WebCacheStats>('/search/cache'));
      setCacheStatsError('');
    } catch (err) {
      setCacheStatsError(err instanceof Error ? err.message : 'Could not read the cache');
    }
  };

  onMount(async () => {
    void loadCacheStats();
    try {
      const cfg = await getSearchConfig();
      setEnabled(cfg.enabled);
      setProvider(cfg.provider ?? 'native');
      setFetchTopK(cfg.fetchTopK || 4);
      setPageChars(cfg.pageChars || 6000);
      setBrowser(cfg.browser ?? '');
      setDbKeySet(cfg.tavilyApiKey === '__SET__');
      setEnvKeySet(!!cfg.tavilyEnvKeySet);
      // The cache knobs ride the same object. A server that predates them
      // replies without the keys, so keep the defaults already on screen.
      const cache = cfg as SearchConfig & Partial<CacheConfig>;
      setCacheEnabled(cache.cacheEnabled ?? true);
      setCacheSearchTTL(cache.cacheSearchTTL || 60);
      setCachePageTTL(cache.cachePageTTL || 1440);
      setCacheAnswerTTL(cache.cacheAnswerTTL || 360);
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
      // The cache knobs travel with the rest of the config: there is one save
      // path, and the server stores these four beside the search settings.
      // Sent as a variable because the shared SearchConfig type has not been
      // widened to the cache keys yet.
      const payload = {
        enabled: enabled(),
        provider: provider(),
        tavilyApiKey: opts.key ?? resolveKey(),
        fetchTopK: fetchTopK(),
        pageChars: pageChars(),
        browser: browser(),
        cacheEnabled: cacheEnabled(),
        cacheSearchTTL: cacheSearchTTL(),
        cachePageTTL: cachePageTTL(),
        cacheAnswerTTL: cacheAnswerTTL(),
      };
      const result = await setSearchConfig(payload);
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

  // The cache switch takes effect on the next restart, like the knobs above.
  const toggleCache = async (next: boolean) => {
    setCacheEnabled(next);
    setClearStatus('');
    try {
      await save({ restart: true });
    } catch {
      setCacheEnabled(!next);
    }
  };

  // A TTL changed. Clamped to the range the store accepts, so the field never
  // sends a value the server would reject.
  const changeTTL = async (which: 'search' | 'page' | 'answer', raw: string) => {
    const n = Number(raw);
    if (!Number.isFinite(n)) return;
    const v = Math.min(10080, Math.max(1, Math.round(n)));
    if (which === 'search') setCacheSearchTTL(v);
    else if (which === 'page') setCachePageTTL(v);
    else setCacheAnswerTTL(v);
    try {
      await save({ restart: true });
    } catch {
      // the flashed value stays; the next read reconciles it
    }
  };

  const clearCache = async () => {
    if (!confirm('Clear every cached search result, page and answer from ogcode?')) return;
    setClearing(true);
    setClearStatus('');
    try {
      const res = await fetchAPI<{ deleted: number }>('/search/cache/clear', { method: 'POST' });
      const n = res.deleted;
      setClearStatus(
        n === 0
          ? 'Nothing was cached.'
          : `Removed ${n.toLocaleString()} ${n === 1 ? 'entry' : 'entries'}.`,
      );
      await loadCacheStats();
    } catch {
      setClearStatus('Could not clear the cache.');
    } finally {
      setClearing(false);
    }
  };

  // One line for the whole store: what it holds, how much room that takes, and
  // how often it answered. An untouched cache honestly says so.
  const cacheStatsLine = () => {
    if (cacheStatsError()) return 'Could not read the cache.';
    const s = cacheStats();
    if (!s) return 'Reading…';
    const entries = `${s.entries.toLocaleString()} ${s.entries === 1 ? 'entry' : 'entries'}`;
    const seen = s.hits + s.misses;
    const rate = seen === 0 ? 'no traffic yet' : `hit rate ${Math.round((s.hits / seen) * 100)}%`;
    return `${entries} · ${humanBytes(s.bytes)} · ${rate}${s.enabled ? '' : ' · off on the server'}`;
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
                helper={
                  isMac()
                    ? 'Safari drives a real browser, which handles JavaScript-heavy pages the HTTP path cannot. Applies after ogcode restarts.'
                    : isWindows()
                      ? 'Chrome — or Edge when Chrome is not installed — drives a real browser, which handles JavaScript-heavy pages the HTTP path cannot. Applies after ogcode restarts.'
                      : 'The HTTP path fetches pages directly; no browser is involved on this system. Applies after ogcode restarts.'
                }
                hidden={hide('Which engine leads', 'safari chrome edge browser http engine native')}
              >
                <Select
                  value={browser()}
                  options={browsers()}
                  disabled={saving()}
                  ariaLabel="Which engine leads"
                  onChange={changeBrowser}
                />
              </Row>

              <Show when={isMac() && browser() === 'safari'}>
                <Row
                  label="Show me how"
                  helper="Safari needs two permissions on your Mac. Neither one needs a restart."
                  onClick={() => setShowHow(!showHow())}
                  expanded={showHow()}
                  hidden={hide('Show me how', 'safari setup automation permissions apple events javascript macos')}
                >
                  <span class="text-meta text-[color:var(--text-tertiary)]">{showHow() ? 'Hide' : 'Show'}</span>
                </Row>

                <Show when={showHow()}>
                  <Row
                    stacked
                    label="Set up Safari search"
                    hidden={hide('Set up Safari search', 'safari setup automation permissions apple events javascript macos')}
                  >
                    <ol class="list-decimal ml-4 space-y-1.5 text-meta leading-[1.5] text-[color:var(--text-tertiary)]">
                      <li>
                        <span class="text-[color:var(--text-secondary)]">Let ogcode control Safari.</span>{' '}
                        Open System Settings → Privacy &amp; Security → Automation, then tick Safari.
                        ogcode asks the first time it uses Safari — click OK.
                      </li>
                      <li>
                        <span class="text-[color:var(--text-secondary)]">Allow JavaScript from Apple Events.</span>{' '}
                        In Safari, open Settings → Advanced and turn on “Show features for web
                        developers”, then choose Develop → “Allow JavaScript from Apple Events”.
                        Without it, pages that JavaScript builds come back mostly empty.
                      </li>
                    </ol>
                  </Row>
                </Show>
              </Show>
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

          <Show when={provider() === 'youcom'}>
            <Row
              label="You.com API key"
              helper={
                <>
                  You.com uses a static key from the server environment — set <Mono>YDC_API_KEY</Mono>{' '}
                  and it is picked up live, no restart needed. The native engines stay behind it as
                  fallback, so a missing or rejected key never leaves a query unanswered.
                </>
              }
              hidden={hide('You.com API key', 'ydc credentials key search provider')}
            >
              <LinkAction href="https://you.com/platform/api-keys">
                Get a key at you.com
              </LinkAction>
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

      {/* The cache sits beside web search rather than inside a provider tab: it
          fronts whichever engine answers, so its knobs belong to neither. */}
      <Show when={!loading() && enabled()}>
        <Group
          id="cache"
          title="Web cache"
          icon={ICONS.cache}
          description="Cached copies are safe to delete at any time. The only cost is fetching that page or answer again."
        >
          <Row
            label="Cache search results, pages and answers"
            helper={
              <>
                Kept in <Mono>~/.ogcode/config.db</Mono>, so the cache survives restarts and is
                shared by every project on this machine. Only public web content is stored.
              </>
            }
            hidden={hide(
              'Cache search results, pages and answers',
              'web cache caching disk config db shared restart',
            )}
          >
            <Switch
              checked={cacheEnabled()}
              disabled={saving()}
              onChange={toggleCache}
              label="Cache search results, pages and answers"
            />
          </Row>

          <Row
            label="Search results stay for"
            helper="How long a search's ranked results are reused before the engine is asked again. Applies after ogcode restarts."
            hidden={hide('Search results stay for', 'cache ttl search results minutes hours expiry')}
          >
            <input
              type="number"
              min="1"
              max="10080"
              value={cacheSearchTTL()}
              disabled={saving() || !cacheEnabled()}
              aria-label="Search results cache in minutes"
              onChange={(e) => changeTTL('search', e.currentTarget.value)}
              class={`${fieldClass} w-[6rem] tabular-nums`}
            />
            <span class="text-meta tabular-nums text-[color:var(--text-tertiary)]">
              {humanMinutes(cacheSearchTTL())}
            </span>
          </Row>

          <Row
            label="Pages stay for"
            helper="How long a fetched page's text is reused instead of being fetched again. Applies after ogcode restarts."
            hidden={hide('Pages stay for', 'cache ttl pages fetch minutes hours expiry')}
          >
            <input
              type="number"
              min="1"
              max="10080"
              value={cachePageTTL()}
              disabled={saving() || !cacheEnabled()}
              aria-label="Page cache in minutes"
              onChange={(e) => changeTTL('page', e.currentTarget.value)}
              class={`${fieldClass} w-[6rem] tabular-nums`}
            />
            <span class="text-meta tabular-nums text-[color:var(--text-tertiary)]">
              {humanMinutes(cachePageTTL())}
            </span>
          </Row>

          <Row
            label="Answers stay for"
            helper="How long a deep search's written answer is reused. Applies after ogcode restarts."
            hidden={hide('Answers stay for', 'cache ttl answers deep search minutes hours expiry')}
          >
            <input
              type="number"
              min="1"
              max="10080"
              value={cacheAnswerTTL()}
              disabled={saving() || !cacheEnabled()}
              aria-label="Answer cache in minutes"
              onChange={(e) => changeTTL('answer', e.currentTarget.value)}
              class={`${fieldClass} w-[6rem] tabular-nums`}
            />
            <span class="text-meta tabular-nums text-[color:var(--text-tertiary)]">
              {humanMinutes(cacheAnswerTTL())}
            </span>
          </Row>

          <Row
            label="Cached now"
            helper="What this machine's cache holds at the moment."
            hidden={hide('Cached now', 'cache entries size bytes hit rate statistics stats')}
          >
            <Value>{cacheStatsLine()}</Value>
          </Row>

          <Row
            label="Clear the cache"
            helper="Deletes every cached entry. Nothing else is touched, and the next search fetches fresh."
            hidden={hide('Clear the cache', 'clear delete remove empty cache entries')}
          >
            <Show when={clearStatus()}>
              <span class="text-micro text-[color:var(--text-tertiary)]">{clearStatus()}</span>
            </Show>
            <Button
              variant="danger"
              onClick={clearCache}
              disabled={saving() || clearing() || !cacheEnabled()}
            >
              {clearing() ? 'Clearing…' : 'Clear cache'}
            </Button>
          </Row>
        </Group>
      </Show>
    </>
  );
}
