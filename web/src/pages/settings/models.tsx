import { For, Index, Show, createSignal, createMemo, createEffect, untrack, onMount, type JSX } from 'solid-js';
import { useSession } from '../../context/session';
import type { ModelInfo, ProviderConfig } from '../../api/client';
import { getProviderConfigs, setProviderConfig } from '../../api/client';
import {
  Group,
  Row,
  Button,
  IconButton,
  LinkAction,
  Chip,
  Tag,
  StatusChip,
  Banner,
  TextField,
  EmptyState,
  Mono,
  Spinner,
  fieldClass,
  matches,
  useShell,
} from './ui';
import {
  PROVIDER_DEFS,
  PROVIDER_GUIDE,
  COMPATIBLE_PRESETS,
  collectionForBaseURL,
  subProviderLabel,
  type ProviderDef,
} from '../../lib/providers';

// ---------------------------------------------------------------------------
// Models — one card per provider.
//
// ogcode speaks four protocols and only four: anthropic, openai, openrouter,
// ollama (see NewProviderWithConfig, which rejects everything else). Every
// other vendor — Gemini, DeepSeek, Groq, Together — arrives through the OpenAI
// slot with a different base URL.
//
// Each protocol gets a sheet holding its credentials and its slice of the
// catalogue, so the whole picture is one scroll rather than a rail you have to
// click through provider by provider.
// ---------------------------------------------------------------------------

const CHIP_ICON =
  'M8.25 3v1.5M4.5 8.25H3m18 0h-1.5M4.5 12H3m18 0h-1.5m-15 3.75H3m18 0h-1.5M8.25 19.5V21M12 3v1.5m0 15V21m3.75-18v1.5m0 15V21m-9-1.5h10.5a2.25 2.25 0 002.25-2.25V6.75a2.25 2.25 0 00-2.25-2.25H6.75A2.25 2.25 0 004.5 6.75v10.5a2.25 2.25 0 002.25 2.25zm.75-12h9v9h-9v-9z';

/** The bundled free pool arrives as providerId "ogcode-groq", "ogcode-…". */
const FREE_POOL = 'ogcode';

function slotOf(m: ModelInfo): string {
  return m.providerId.startsWith('ogcode-') ? FREE_POOL : m.providerId;
}

interface Slot {
  id: string;
  label: string;
  def?: ProviderDef;
  /** Read-only slots have no credentials to configure. */
  readOnly?: boolean;
}

export default function ModelsSettings() {
  const session = useSession();
  const shell = useShell();
  const [configs, setConfigs] = createSignal<Record<string, ProviderConfig>>({});
  const [loadingConfigs, setLoadingConfigs] = createSignal(true);

  createEffect(() => shell.report({ noun: 'settings' }));

  onMount(async () => {
    try {
      const list = await getProviderConfigs();
      const map: Record<string, ProviderConfig> = {};
      for (const c of list) map[c.providerId] = c;
      setConfigs(map);
    } finally {
      setLoadingConfigs(false);
    }
  });

  // The page always shows the four real slots, plus the free pool and any
  // unexpected provider id that turns up in the catalogue — a model the user
  // can see in the picker must be reachable here, whatever its provider.
  const slots = createMemo<Slot[]>(
    () => {
      const base: Slot[] = PROVIDER_DEFS.map((def) => ({ id: def.id, label: def.label, def }));
      const known = new Set(base.map((s) => s.id));
      const extra = new Set<string>();
      for (const m of session.models()) {
        const slot = slotOf(m);
        if (!known.has(slot)) extra.add(slot);
      }
      if (extra.has(FREE_POOL)) {
        base.push({ id: FREE_POOL, label: 'ogcode free pool', readOnly: true });
        extra.delete(FREE_POOL);
      }
      for (const id of [...extra].sort()) base.push({ id, label: id, readOnly: true });
      return base;
    },
    [] as Slot[],
    // Toggling a model rebuilds this list, but the set of provider slots almost
    // never changes as a result. Returning the previous array when the slot ids
    // still match keeps <For> from re-mounting every ProviderSection on each
    // toggle — the re-mount is what reset the scroll position and flashed the
    // whole page.
    { equals: (a, b) => a.length === b.length && a.every((s, i) => s.id === b[i].id) },
  );

  const modelsFor = (slotId: string) =>
    session
      .models()
      .filter((m) => slotOf(m) === slotId)
      // Sorted by name only — never by enabled state, or a row would jump out
      // from under the cursor the moment it was toggled.
      .sort((a, b) => a.name.localeCompare(b.name));

  /** True when a query is live and nothing anywhere on the page matched it. */
  const nothingMatched = createMemo(() => {
    const q = shell.query().trim();
    if (!q) return false;
    return !slots().some(
      (slot) =>
        matches(q, slot.label, slot.id) ||
        modelsFor(slot.id).some((m) => modelMatches(m, q)) ||
        CREDENTIAL_TERMS.some((t) => matches(q, t)),
    );
  });

  return (
    <Show
      when={!nothingMatched()}
      fallback={
        <EmptyState
          icon="M21 21l-4.35-4.35M17 10a7 7 0 11-14 0 7 7 0 0114 0z"
          title={`Nothing matches "${shell.query()}"`}
          body="Try a model name, a provider, or a word like key or endpoint."
        />
      }
    >
      <For each={slots()}>
        {(slot) => (
          <ProviderSection
            slot={slot}
            models={modelsFor(slot.id)}
            config={configs()[slot.id]}
            loadingConfig={loadingConfigs()}
            onSaved={(c) => setConfigs({ ...configs(), [slot.id]: c })}
            onApplied={() => session.reloadModels()}
            onToggle={(m) => session.toggleModel(m, !m.enabled)}
            onRemove={async (m) => {
              if (!confirm(`Remove "${m.name}"? This deletes the custom model.`)) return;
              await session.removeCustomModel(m.id);
            }}
            onAdd={(id, name, collection) =>
              session.addCustomModel(id, slot.id, name, collection || undefined)
            }
          />
        )}
      </For>
    </Show>
  );
}

/** Words that keep the credential rows on screen while searching, so "key" or
 *  "endpoint" finds them under every provider. */
const CREDENTIAL_TERMS = ['API key', 'Base URL', 'endpoint', 'credentials', 'models'];

// Names and IDs match anywhere, because people type fragments of them ("4o",
// "coder"). Collections match only at a word boundary: a plain substring test
// makes "llama" hit every model in *O-llama Cloud*, which drowns the models
// actually called llama.
function modelMatches(m: ModelInfo, q: string): boolean {
  const query = q.trim().toLowerCase();
  if (!query) return true;
  if (m.name.toLowerCase().includes(query) || m.id.toLowerCase().includes(query)) return true;
  const collection = (m.collection || '').toLowerCase();
  return collection ? collection.split(/[\s\-_/]+/).some((w) => w.startsWith(query)) : false;
}

/** A slot counts as configured when a key is set (app or env), or — for Ollama — an endpoint alone. */
function isConfigured(slot: Slot, config: ProviderConfig | undefined): boolean {
  if (slot.readOnly) return true;
  if (!config) return false;
  if (config.apiKey === '__SET__' || config.envKeySet) return true;
  return !!slot.def?.keyOptional && !!(config.effectiveBaseUrl || config.baseUrl);
}

function ProviderSection(props: {
  slot: Slot;
  models: ModelInfo[];
  config: ProviderConfig | undefined;
  loadingConfig: boolean;
  onSaved: (c: ProviderConfig) => void;
  onApplied: () => Promise<void>;
  onToggle: (m: ModelInfo) => void | Promise<void>;
  onRemove: (m: ModelInfo) => void;
  onAdd: (id: string, name: string, collection: string) => Promise<void>;
}) {
  const shell = useShell();
  const guide = () => PROVIDER_GUIDE[props.slot.id];

  const pointedAt = () => {
    if (props.slot.id !== 'openai') return '';
    return collectionForBaseURL(props.config?.effectiveBaseUrl || props.config?.baseUrl || '');
  };

  const visibleModels = createMemo(() => props.models.filter((m) => modelMatches(m, shell.query())));
  const slotMatches = () => matches(shell.query(), props.slot.label, props.slot.id);
  const hideRow = (...terms: string[]) =>
    !slotMatches() && !matches(shell.query(), ...terms) && visibleModels().length === 0;

  const enabledCount = () => props.models.filter((m) => m.enabled).length;
  const configured = () => isConfigured(props.slot, props.config);

  return (
    <Group
      id={props.slot.id}
      title={pointedAt() ? `${props.slot.label} → ${pointedAt()}` : props.slot.label}
      icon={CHIP_ICON}
      description={
        <Show when={guide()} fallback="Bundled with ogcode — no credentials required.">
          <>
            {guide()!.tagline}
            <span class="block mt-1">
              <span class="text-[color:var(--text-muted)]">Use when </span>
              {guide()!.useWhen}
            </span>
          </>
        </Show>
      }
      action={
        <Show when={configured()} fallback={<StatusChip tone="muted">Not set up</StatusChip>}>
          <StatusChip tone="ok">{enabledCount()} on</StatusChip>
        </Show>
      }
    >
      <Show when={props.slot.def && !props.loadingConfig}>
        <Credentials
          def={props.slot.def!}
          config={props.config}
          onSaved={props.onSaved}
          onApplied={props.onApplied}
          hide={hideRow}
        />
      </Show>

      <Row
        label="Models"
        helper="Which of this provider's models appear in the picker."
        stacked
        hidden={hideRow('models', 'catalogue', 'available')}
      >
        <ModelList
          all={props.models}
          visible={visibleModels()}
          slot={props.slot}
          configured={configured()}
          filtering={!!shell.query().trim()}
          onToggle={props.onToggle}
          onRemove={props.onRemove}
          onAdd={props.onAdd}
          suggestedCollection={
            props.slot.id === 'openai'
              ? collectionForBaseURL(props.config?.effectiveBaseUrl || props.config?.baseUrl || '')
              : ''
          }
        />
      </Row>
    </Group>
  );
}

// ---------- Credentials ------------------------------------------------------

function Credentials(props: {
  def: ProviderDef;
  config: ProviderConfig | undefined;
  onSaved: (c: ProviderConfig) => void;
  onApplied: () => Promise<void>;
  hide: (...terms: string[]) => boolean;
}) {
  const [apiKey, setApiKey] = createSignal('');
  const [baseURL, setBaseURL] = createSignal('');
  const [saving, setSaving] = createSignal(false);
  // Set while the freshly-saved credentials are being applied and the provider's
  // catalogue re-fetched. Drives the "Fetching models…" indicator.
  const [refreshing, setRefreshing] = createSignal(false);
  const [saved, setSaved] = createSignal(false);
  const [error, setError] = createSignal('');
  // The form is locked through both the save and the follow-up catalogue fetch.
  const busy = () => saving() || refreshing();
  let baseRef: HTMLInputElement | undefined;

  const guide = () => PROVIDER_GUIDE[props.def.id];

  // Re-seed the form when the card switches providers — otherwise one
  // provider's endpoint would carry into another's form.
  //
  // The guard is load-bearing. Reading `props.def` walks back to
  // session.models(), so every model toggle and every background catalogue
  // refresh re-ran the seed and wiped a half-typed API key out of the field.
  // Comparing the id ourselves makes those runs no-ops.
  let seededFor = '';
  createEffect(() => {
    const id = props.def.id;
    if (id === seededFor) return;
    seededFor = id;
    untrack(() => {
      setApiKey('');
      setBaseURL(props.config?.baseUrl || '');
      setSaved(false);
      setError('');
    });
  });

  const dbKeySet = () => props.config?.apiKey === '__SET__';
  const envKeySet = () => !!props.config?.envKeySet;
  const effectiveURL = () => props.config?.effectiveBaseUrl || '';
  const endpointOverridden = () => !!effectiveURL() && effectiveURL() !== (props.config?.baseUrl || '');

  const keyState = () => {
    if (envKeySet() && dbKeySet()) return 'Key set — environment and app';
    if (envKeySet()) return `Key set via ${guide()?.envKey ?? 'the environment'}`;
    if (dbKeySet()) return 'Key stored in ogcode';
    return '';
  };

  // The server preserves a stored key ONLY when it receives the "__SET__"
  // sentinel (handleSetProviderConfig); every other value is written verbatim.
  // So an untouched, empty key field must send the sentinel — sending "" would
  // silently delete a working key the moment someone edited only the Base URL.
  const commit = async () => {
    setError('');
    setSaved(false);
    setSaving(true);
    const typed = apiKey().trim();
    let result: ProviderConfig;
    try {
      result = await setProviderConfig(props.def.id, {
        apiKey: typed === '' && dbKeySet() ? '__SET__' : typed,
        baseUrl: baseURL().trim(),
      });
    } catch {
      setError('Could not save. Is the ogcode server still running?');
      setSaving(false);
      return;
    }
    props.onSaved(result);
    setApiKey('');
    setSaving(false);
    // The server applies credentials the moment they are saved (it reloads its
    // providers in place), so pull the freshly-fetched catalogue rather than ask
    // for a restart. A failure past this point is a fetch failure, not a save
    // failure — the key is stored either way, so say which.
    setRefreshing(true);
    try {
      await props.onApplied();
      setSaved(true);
      setTimeout(() => setSaved(false), 4000);
    } catch {
      setError('Saved, but the model list could not be fetched. Check the key or endpoint, then Save again.');
    } finally {
      setRefreshing(false);
    }
  };

  // Because a blank field means "keep the stored key", clearing one needs its
  // own deliberate action rather than a side effect of saving.
  const clearKey = async () => {
    if (!confirm(`Remove the stored ${props.def.label} API key from ogcode?`)) return;
    setError('');
    setSaving(true);
    let result: ProviderConfig;
    try {
      result = await setProviderConfig(props.def.id, { apiKey: '', baseUrl: baseURL().trim() });
    } catch {
      setError('Could not remove the key.');
      setSaving(false);
      return;
    }
    props.onSaved(result);
    setApiKey('');
    setSaving(false);
    // Removing the key unregisters the provider server-side, so refresh to drop
    // its models from the list without a restart.
    setRefreshing(true);
    try {
      await props.onApplied();
    } catch {
      // The key is gone regardless; a stale row corrects on the next refresh.
    } finally {
      setRefreshing(false);
    }
  };

  return (
    <>
      <Row
        label="API key"
        helper={
          <Show
            when={dbKeySet()}
            fallback={<>Read from <Mono>{guide()?.envKey}</Mono> if that variable is set. Applies as soon as you save.</>}
          >
            <>
              Leave blank to keep the stored key.
              <Show when={envKeySet()}>
                {' '}<Mono>{guide()?.envKey}</Mono> is also set and takes priority.
              </Show>
            </>
          </Show>
        }
        stacked
        hidden={props.hide('API key', 'credentials', 'token')}
      >
        <div class="flex items-center gap-2 flex-wrap">
          <div class="flex-1 min-w-[14rem]">
            <TextField
              password
              mono
              value={apiKey()}
              onInput={setApiKey}
              onEnter={commit}
              disabled={busy()}
              ariaLabel={`${props.def.label} API key`}
              placeholder={
                dbKeySet() ? 'leave blank to keep the saved key'
                : envKeySet() ? 'leave blank to use the environment key'
                : guide()?.keyHint ?? 'sk-…'
              }
            />
          </div>
          <Button onClick={commit} disabled={busy()}>{saving() ? 'Saving…' : refreshing() ? 'Fetching…' : 'Save'}</Button>
        </div>
        <div class="mt-2 flex items-center gap-3 flex-wrap">
          <Show when={keyState()}>
            <StatusChip tone="ok">{keyState()}</StatusChip>
          </Show>
          <Show when={refreshing()}>
            <span class="inline-flex items-center gap-1.5 text-micro text-[color:var(--text-muted)]">
              <Spinner class="w-3 h-3" />
              Fetching models…
            </span>
          </Show>
          <Show when={saved() && !refreshing()}>
            <span class="text-micro" style={{ color: 'var(--success)' }}>Applied — models updated.</span>
          </Show>
          <Show when={error()}>
            <span class="text-micro" style={{ color: 'var(--danger)' }}>{error()}</span>
          </Show>
          <Show when={dbKeySet()}>
            <LinkAction onClick={clearKey}>Remove stored key</LinkAction>
          </Show>
          <Show when={guide()?.keysURL}>
            <LinkAction href={guide()!.keysURL}>Get a key at {guide()!.keysLabel}</LinkAction>
          </Show>
        </div>
      </Row>

      <Show when={props.def.hasBaseURL}>
        <Row
          label="Base URL"
          helper={
            <>
              Where requests are sent. Leave blank for the default
              <Show when={guide()?.defaultBaseURL}> — <Mono>{guide()!.defaultBaseURL}</Mono></Show>.
            </>
          }
          stacked
          hidden={props.hide('Base URL', 'endpoint', 'host')}
        >
          <div class="flex items-center gap-2 flex-wrap">
            <div class="flex-1 min-w-[14rem]">
              <TextField
                mono
                ref={(el) => (baseRef = el)}
                value={baseURL()}
                onInput={setBaseURL}
                onEnter={commit}
                disabled={busy()}
                ariaLabel={`${props.def.label} base URL`}
                placeholder={guide()?.defaultBaseURL ?? ''}
              />
            </div>
            <Button onClick={commit} disabled={busy()}>{saving() ? 'Saving…' : refreshing() ? 'Fetching…' : 'Save'}</Button>
          </div>
          <Show when={endpointOverridden()}>
            <div class="mt-1.5">
              <Banner tone="warn">
                Currently calling <Mono>{effectiveURL()}</Mono>
                {props.config?.envBaseURLSet
                  ? ` — ${guide()?.envBaseURL ?? 'the environment variable'} wins over the value above.`
                  : ' — the saved endpoint was unreachable.'}
              </Banner>
            </div>
          </Show>
        </Row>
      </Show>

      {/* One-click endpoints — the whole point of the OpenAI slot. Choosing a
          vendor fills the field; the user still supplies that vendor's key and
          saves, so nothing changes behind their back. */}
      <Show when={props.def.id === 'openai'}>
        <Row
          label="Point at"
          helper="Aim this slot at another OpenAI-compatible vendor. One at a time — the key above must belong to whichever you pick."
          stacked
          hidden={props.hide('Point at', 'gemini deepseek groq together vendor preset compatible')}
        >
          {/* One scrolling row rather than wrapping to two: the vendor list keeps
              growing, and a single lane reads as a picker. hide-scrollbar +
              shrink-0 chips match the settings tab rail. */}
          <div class="flex flex-nowrap gap-2 overflow-x-auto hide-scrollbar">
            <Chip active={!collectionForBaseURL(baseURL())} onClick={() => setBaseURL('')}>
              OpenAI
            </Chip>
            <For each={COMPATIBLE_PRESETS}>
              {(preset) => (
                <Chip
                  active={collectionForBaseURL(baseURL()) === preset.collection}
                  title={`${preset.baseURL} — key looks like ${preset.keyHint}`}
                  onClick={() => setBaseURL(preset.baseURL)}
                >
                  {preset.label}
                </Chip>
              )}
            </For>
            <Chip
              active={false}
              title="Any OpenAI-compatible endpoint — vLLM, LM Studio, LiteLLM, a company gateway"
              onClick={() => baseRef?.focus()}
            >
              Custom…
            </Chip>
          </div>
        </Row>
      </Show>
    </>
  );
}

// ---------- Model list -------------------------------------------------------

function ModelList(props: {
  all: ModelInfo[];
  visible: ModelInfo[];
  slot: Slot;
  configured: boolean;
  filtering: boolean;
  onToggle: (m: ModelInfo) => void | Promise<void>;
  onRemove: (m: ModelInfo) => void;
  onAdd: (id: string, name: string, collection: string) => Promise<void>;
  suggestedCollection: string;
}) {
  const [enabledOnly, setEnabledOnly] = createSignal(false);
  const [bulkBusy, setBulkBusy] = createSignal(false);
  const [expanded, setExpanded] = createSignal(false);
  // Per-provider model search. OpenRouter alone lists hundreds of models, so each
  // catalogue gets its own filter box rather than leaning on the page-wide search.
  const [query, setQuery] = createSignal('');

  const rows = createMemo(() => {
    const q = query().trim();
    return props.visible.filter(
      (m) => (!enabledOnly() || m.enabled) && (q === '' || modelMatches(m, q)),
    );
  });

  // A live search — this box or the page-wide one — means the user is after
  // something specific, so show every match instead of the first LIMIT.
  const isFiltering = () => props.filtering || query().trim() !== '';

  // A provider with hundreds of models (OpenRouter) would otherwise put its
  // whole catalogue into a page that already holds four other providers.
  const LIMIT = 20;
  const capped = createMemo(() => (expanded() || isFiltering() ? rows() : rows().slice(0, LIMIT)));
  const hiddenCount = () => rows().length - capped().length;

  // Sequential, not a parallel fan-out. Each toggle POSTs one preference and
  // gets the *entire* model list back, which replaces local state — so firing
  // 400 of them at once means 400 responses racing to overwrite each other with
  // snapshots taken before their siblings landed.
  const setAll = async (enabled: boolean) => {
    if (bulkBusy()) return;
    const targets = rows().filter((m) => m.enabled !== enabled);
    if (targets.length === 0) return;
    setBulkBusy(true);
    try {
      for (const m of targets) await props.onToggle(m);
    } finally {
      setBulkBusy(false);
    }
  };

  // Collection sub-headers only earn their line when a provider actually holds
  // more than one — Ollama's local versus cloud catalogue, for instance.
  const sections = createMemo(() => {
    const map = new Map<string, ModelInfo[]>();
    for (const m of capped()) {
      const key = m.collection || '';
      map.set(key, [...(map.get(key) || []), m]);
    }
    return [...map.entries()].sort(([a], [b]) => a.localeCompare(b));
  });

  return (
    <div>
      <Show when={props.all.length > 5}>
        <div class="relative mb-1.5">
          <svg
            class="pointer-events-none absolute left-2 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-[color:var(--text-muted)]"
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
            stroke-width="2"
          >
            <path stroke-linecap="round" stroke-linejoin="round" d="M21 21l-4.35-4.35M17 10a7 7 0 11-14 0 7 7 0 0114 0z" />
          </svg>
          <input
            type="text"
            value={query()}
            onInput={(e) => setQuery(e.currentTarget.value)}
            onKeyDown={(e) => {
              if (e.key === 'Escape') setQuery('');
            }}
            placeholder={`Search ${props.slot.label} models…`}
            aria-label={`Search ${props.slot.label} models`}
            spellcheck={false}
            class={`${fieldClass} w-full pl-7 ${query() ? 'pr-7' : ''} text-meta`}
          />
          <Show when={query()}>
            <button
              type="button"
              onClick={() => setQuery('')}
              aria-label="Clear model search"
              class="absolute right-1.5 top-1/2 -translate-y-1/2 w-5 h-5 flex items-center justify-center rounded
                     text-[color:var(--text-muted)] hover:text-[color:var(--text-primary)] hover:bg-[color:var(--bg-hover)] transition-colors"
            >
              <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
                <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
              </svg>
            </button>
          </Show>
        </div>
      </Show>

      <Show when={props.all.length > 0}>
        <div class="flex items-center gap-1.5 flex-wrap mb-1.5">
          <Chip active={enabledOnly()} onClick={() => setEnabledOnly(!enabledOnly())}>
            Enabled only
          </Chip>
          <Chip onClick={() => setAll(true)}>{bulkBusy() ? 'Working…' : 'Enable all'}</Chip>
          <Chip onClick={() => setAll(false)}>Disable all</Chip>
          <Show when={query().trim() && rows().length > 0}>
            <span class="ml-0.5 text-micro tabular-nums text-[color:var(--text-muted)]">
              {rows().length} match{rows().length === 1 ? '' : 'es'}
            </span>
          </Show>
        </div>
      </Show>

      <Show when={rows().length > 0}>
        <div class="border-t border-[color:var(--border-subtle)]">
          {/* <Index>, not <For>: sections() yields fresh tuples on every toggle,
              which a reference-keyed <For> would treat as new — re-mounting the
              whole list and flashing it. <Index> keys by position, so the section
              wrappers persist and the inner <For> (keyed by the model objects,
              whose identity toggleModel preserves) updates only the one row that
              actually changed. */}
          <Index each={sections()}>
            {(section) => {
              const collection = () => section()[0];
              const models = () => section()[1];
              return (
                <>
                  <Show when={collection() && sections().length > 1}>
                    <div class="flex items-center gap-2 pt-3 pb-1">
                      <span class="text-micro font-medium uppercase tracking-[0.06em] text-[color:var(--text-muted)]">
                        {collection()}
                      </span>
                      <span class="text-micro tabular-nums text-[color:var(--text-muted)]">{models().length}</span>
                    </div>
                  </Show>
                  <For each={models()}>
                    {(m) => (
                      <ModelItem model={m} onToggle={() => props.onToggle(m)} onRemove={() => props.onRemove(m)} />
                    )}
                  </For>
                </>
              );
            }}
          </Index>
        </div>
      </Show>

      <Show when={hiddenCount() > 0}>
        <button
          type="button"
          onClick={() => setExpanded(true)}
          class="mt-2 text-meta font-medium text-[color:var(--accent)] hover:underline underline-offset-2"
        >
          Show {hiddenCount()} more
        </button>
      </Show>

      {/* An empty list is nearly always a search with no hits or a missing
          credential, so say which — "no models" alone leaves nothing to act on. */}
      <Show when={rows().length === 0}>
        <p class="text-meta text-[color:var(--text-tertiary)] leading-[1.6]">
          {isFiltering()
            ? 'No models here match the search.'
            : enabledOnly()
            ? 'Every model from this provider is currently disabled.'
            : props.configured
            ? props.slot.id === 'ollama'
              ? 'Connected, but the catalogue is empty. Pull a model with `ollama pull qwen2.5-coder`, then Save the endpoint again to refresh.'
              : 'Connected, but the catalogue is empty. Save again to re-fetch, or add a model ID by hand below.'
            : props.slot.id === 'ollama'
            ? 'Not connected. Install Ollama, pull a model, and point Base URL at it — no API key needed.'
            : 'Not connected. Add an API key above; its models appear here the moment you save.'}
        </p>
      </Show>

      <Show when={!props.slot.readOnly}>
        <AddModelRow
          providerLabel={props.slot.label}
          suggestedCollection={props.suggestedCollection}
          showCollection={props.slot.id === 'openai'}
          onAdd={props.onAdd}
        />
      </Show>
    </div>
  );
}

function ModelItem(props: { model: ModelInfo; onToggle: () => void; onRemove: () => void }) {
  const hasPrice = () => props.model.inputPricePerM > 0 || props.model.outputPricePerM > 0;
  return (
    <div
      class={`group/row flex items-center gap-3 h-8 border-b border-[color:var(--border-subtle)] last:border-b-0
              hover:bg-[color:var(--bg-hover)]/40 transition-colors ${props.model.enabled ? '' : 'opacity-60'}`}
    >
      <button
        type="button"
        role="switch"
        aria-checked={props.model.enabled}
        aria-label={`${props.model.enabled ? 'Disable' : 'Enable'} ${props.model.name}`}
        onClick={props.onToggle}
        class={`w-[15px] h-[15px] shrink-0 rounded-[4px] border flex items-center justify-center transition-colors
          ${props.model.enabled
            ? 'bg-[color:var(--accent)] border-[color:var(--accent)] text-[color:var(--on-primary)]'
            : 'bg-[color:var(--bg-elevated)] border-[color:var(--border-strong)] text-transparent hover:border-[color:var(--text-tertiary)]'
          }`}
      >
        <svg class="w-2.5 h-2.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="3.5">
          <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
        </svg>
      </button>

      <div class="min-w-0 flex-1 flex items-center gap-1.5">
        <span class="text-meta text-[color:var(--text-primary)] truncate">{props.model.name}</span>
        <Show when={props.model.default}>
          <Tag tone="accent">default</Tag>
        </Show>
        <Show when={props.model.isCustom}>
          <Tag>custom</Tag>
        </Show>
        <Show when={subProviderLabel(props.model)}>{(label) => <Tag>{label()}</Tag>}</Show>
      </div>

      <span
        class="hidden md:block shrink-0 w-[10rem] text-micro font-mono text-[color:var(--text-muted)] truncate"
        title={props.model.id}
      >
        {props.model.id}
      </span>

      <span
        class="shrink-0 w-[6rem] text-micro font-mono tabular-nums text-right text-[color:var(--text-tertiary)]"
        title="Input / output price per 1M tokens"
      >
        <Show when={hasPrice()} fallback={<span class="text-[color:var(--text-muted)]">—</span>}>
          ${fmtPrice(props.model.inputPricePerM)} / ${fmtPrice(props.model.outputPricePerM)}
        </Show>
      </span>

      <span class="w-7 shrink-0 flex justify-end">
        <Show when={props.model.isCustom}>
          <span class="opacity-0 group-hover/row:opacity-100 focus-within:opacity-100 transition-opacity">
            <IconButton
              danger
              onClick={props.onRemove}
              label={`Remove ${props.model.name}`}
              path="M6 18L18 6M6 6l12 12"
            />
          </span>
        </Show>
      </span>
    </div>
  );
}

function AddModelRow(props: {
  providerLabel: string;
  suggestedCollection: string;
  showCollection: boolean;
  onAdd: (id: string, name: string, collection: string) => Promise<void>;
}) {
  const [open, setOpen] = createSignal(false);
  const [id, setId] = createSignal('');
  const [name, setName] = createSignal('');
  const [collection, setCollection] = createSignal('');
  const [error, setError] = createSignal('');
  const [busy, setBusy] = createSignal(false);

  const start = () => {
    setId('');
    setName('');
    setCollection(props.suggestedCollection);
    setError('');
    setOpen(true);
  };

  const submit = async () => {
    const modelId = id().trim();
    if (!modelId) { setError('A model ID is required.'); return; }
    setBusy(true);
    setError('');
    try {
      await props.onAdd(modelId, name().trim() || modelId, collection().trim());
      setOpen(false);
    } catch (e: any) {
      setError(e?.message || 'Could not add that model.');
    } finally {
      setBusy(false);
    }
  };

  const field = `${fieldClass} min-w-0 text-meta`;

  return (
    <Show
      when={open()}
      fallback={
        <button
          type="button"
          onClick={start}
          class="mt-2 inline-flex items-center gap-1.5 h-8 px-2.5 rounded-lg text-meta font-medium
                 text-[color:var(--text-tertiary)] hover:text-[color:var(--text-primary)]
                 hover:bg-[color:var(--bg-hover)] transition-colors"
        >
          <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M12 5v14M5 12h14" />
          </svg>
          Add a model to {props.providerLabel}
        </button>
      }
    >
      <div class="mt-4 pt-4 border-t border-[color:var(--border-subtle)]">
        <div class="flex items-center gap-2 flex-wrap">
          <input
            ref={(el) => requestAnimationFrame(() => el.focus())}
            type="text"
            value={id()}
            onInput={(e) => setId(e.currentTarget.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') submit();
              if (e.key === 'Escape') setOpen(false);
            }}
            placeholder="model-id (exactly as the provider names it)"
            aria-label="Model ID"
            class={`${field} font-mono flex-[1.4]`}
          />
          <input
            type="text"
            value={name()}
            onInput={(e) => setName(e.currentTarget.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') submit();
              if (e.key === 'Escape') setOpen(false);
            }}
            placeholder="Display name (optional)"
            aria-label="Display name"
            class={`${field} flex-1`}
          />
          <Show when={props.showCollection}>
            <input
              type="text"
              value={collection()}
              onInput={(e) => setCollection(e.currentTarget.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') submit();
                if (e.key === 'Escape') setOpen(false);
              }}
              placeholder="Group"
              aria-label="Group"
              title="Groups this model under a vendor name in the picker"
              class={`${field} w-24`}
            />
          </Show>
          <Button onClick={submit} disabled={busy()}>{busy() ? 'Adding…' : 'Add'}</Button>
          <Button variant="text" onClick={() => setOpen(false)}>Cancel</Button>
        </div>
        <p class="mt-1.5 text-micro text-[color:var(--text-muted)]">
          <Show when={error()} fallback="ogcode does not verify the ID — a typo shows up as a failed request on first use.">
            <span style={{ color: 'var(--danger)' }}>{error()}</span>
          </Show>
        </p>
      </div>
    </Show>
  );
}

// Always two decimals. In a right-aligned tabular column, "$1.6" next to
// "$0.40" breaks the decimal alignment that makes prices comparable at a
// glance. Sub-cent rates are real on aggregators, and rounding them to "0.00"
// would read as free.
function fmtPrice(n: number): string {
  if (n > 0 && n < 0.01) return '<0.01';
  return n.toFixed(2);
}
