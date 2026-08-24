import { For, Show, createSignal, createMemo, createEffect, untrack, onMount } from 'solid-js';
import { useSession } from '../../context/session';
import type { ModelInfo, ProviderConfig } from '../../api/client';
import { getProviderConfigs, setProviderConfig } from '../../api/client';
import {
  PROVIDER_DEFS,
  PROVIDER_GUIDE,
  COMPATIBLE_PRESETS,
  collectionForBaseURL,
  subProviderLabel,
  type ProviderDef,
} from '../../lib/providers';

// ---------------------------------------------------------------------------
// Models settings — a workbench, not a stack of cards.
//
// ogcode speaks four protocols and only four: anthropic, openai, openrouter,
// ollama (see NewProviderWithConfig, which rejects everything else). Every
// other vendor — Gemini, DeepSeek, Groq, Together, Mistral, … — arrives
// through the OpenAI slot with a different base URL. So the screen is built as
// four slots in a rail plus one detail pane, with the guidance needed to pick
// between them; it does not invent an integration per vendor.
//
// Layout is rail + pane rather than a column of collapsible cards: choosing a
// provider *replaces* the pane instead of expanding a section, so nothing
// pushes the rest of the page around and the model list is never buried under
// three accordions.
// ---------------------------------------------------------------------------

/** The bundled free pool arrives as providerId "ogcode-groq", "ogcode-…". */
const FREE_POOL = 'ogcode';

function slotOf(m: ModelInfo): string {
  return m.providerId.startsWith('ogcode-') ? FREE_POOL : m.providerId;
}

interface Slot {
  id: string;
  label: string;
  dot: string;
  def?: ProviderDef;
  /** Read-only slots have no credentials to configure. */
  readOnly?: boolean;
}

export default function ModelsSettings() {
  const session = useSession();
  const [selected, setSelected] = createSignal<string>('anthropic');
  const [query, setQuery] = createSignal('');
  const [enabledOnly, setEnabledOnly] = createSignal(false);
  const [configs, setConfigs] = createSignal<Record<string, ProviderConfig>>({});
  const [loadingConfigs, setLoadingConfigs] = createSignal(true);
  // Opening on a provider the user never set up means landing on an empty pane.
  // Once the catalogue arrives, jump to the slot that actually has models —
  // but only once, so it never yanks the pane out from under a later click.
  let landed = false;

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

  // The rail always shows the four real slots, plus the free pool and any
  // unexpected provider id that turns up in the catalogue — a model the user
  // can see in the picker must be reachable here, whatever its provider.
  const slots = createMemo<Slot[]>(() => {
    const base: Slot[] = PROVIDER_DEFS.map((def) => ({
      id: def.id,
      label: def.label,
      dot: def.dot,
      def,
    }));
    const known = new Set(base.map((s) => s.id));
    const extra = new Set<string>();
    for (const m of session.models()) {
      const slot = slotOf(m);
      if (!known.has(slot)) extra.add(slot);
    }
    if (extra.has(FREE_POOL)) {
      base.push({ id: FREE_POOL, label: 'ogcode free pool', dot: 'bg-emerald-400', readOnly: true });
      extra.delete(FREE_POOL);
    }
    for (const id of [...extra].sort()) {
      base.push({ id, label: id, dot: 'bg-zinc-400', readOnly: true });
    }
    return base;
  });

  // Names and IDs match anywhere, because people type fragments of them ("4o",
  // "coder"). Collections match only at a word boundary: a plain substring test
  // makes "llama" hit every model in *O-llama Cloud*, which drowns the models
  // actually called llama. Provider ids are not searched at all — the rail
  // already carries that dimension, and "ollama" would collide the same way.
  const matches = (m: ModelInfo, q: string) => {
    if (!q) return true;
    if (m.name.toLowerCase().includes(q) || m.id.toLowerCase().includes(q)) return true;
    const collection = (m.collection || '').toLowerCase();
    return collection ? collection.split(/[\s\-_/]+/).some((word) => word.startsWith(q)) : false;
  };

  /** Per-slot counts, recomputed against the live query so the rail doubles as a search result map. */
  const counts = createMemo(() => {
    const q = query().trim().toLowerCase();
    const out: Record<string, { total: number; enabled: number; hits: number }> = {};
    for (const m of session.models()) {
      const slot = slotOf(m);
      const c = out[slot] || (out[slot] = { total: 0, enabled: 0, hits: 0 });
      c.total++;
      if (m.enabled) c.enabled++;
      if (matches(m, q)) c.hits++;
    }
    return out;
  });

  createEffect(() => {
    if (landed) return;
    const models = session.models();
    if (models.length === 0) return;
    landed = true;
    const c = counts();
    // Prefer a slot the user actually owns and has models behind: configurable
    // beats the read-only bundled pool, then richest first. Landing on the free
    // pool would open a pane with nothing to configure.
    const best = slots()
      .filter((s) => (c[s.id]?.total ?? 0) > 0)
      .sort((a, b) => {
        const own = Number(!!b.def) - Number(!!a.def);
        if (own !== 0) return own;
        return (c[b.id]?.enabled ?? 0) - (c[a.id]?.enabled ?? 0);
      })[0];
    if (best) setSelected(best.id);
  });

  const activeSlot = createMemo(() => slots().find((s) => s.id === selected()) ?? slots()[0]);

  const visibleModels = createMemo(() => {
    const q = query().trim().toLowerCase();
    const slot = selected();
    const only = enabledOnly();
    return session.models()
      .filter((m) => slotOf(m) === slot && matches(m, q) && (!only || m.enabled))
      // Sorted by name only — never by enabled state, or a row would jump out
      // from under the cursor the moment it was toggled.
      .sort((a, b) => a.name.localeCompare(b.name));
  });

  /** Slots holding matches for the current query other than the one on screen. */
  const elsewhere = createMemo(() => {
    if (!query().trim()) return [];
    const c = counts();
    return slots().filter((s) => s.id !== selected() && (c[s.id]?.hits ?? 0) > 0);
  });

  const totals = createMemo(() => {
    const all = session.models();
    return { total: all.length, enabled: all.filter((m) => m.enabled).length };
  });

  return (
    <div class="h-full flex flex-col overflow-hidden anim-enter">
      {/* Toolbar */}
      <header class="h-12 shrink-0 border-b border-[color:var(--border-subtle)] flex items-center gap-3 px-6">
        <h1 class="text-ui font-semibold text-[color:var(--text-primary)]">Models</h1>
        <span class="text-micro text-[color:var(--text-muted)] tabular-nums">
          {totals().enabled} of {totals().total} enabled
        </span>
        <div class="flex-1" />
        <div class="relative w-64">
          <svg class="w-3 h-3 text-[color:var(--text-muted)] absolute left-2.5 top-1/2 -translate-y-1/2 pointer-events-none" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M21 21l-4.35-4.35M17 10a7 7 0 11-14 0 7 7 0 0114 0z" />
          </svg>
          <input
            type="text"
            value={query()}
            onInput={(e) => setQuery(e.currentTarget.value)}
            placeholder="Filter models"
            aria-label="Filter models"
            class="w-full h-7 pl-7 pr-2 rounded-[5px] bg-[color:var(--bg-elevated)] border border-[color:var(--border-subtle)]
                   text-meta text-[color:var(--text-primary)] placeholder:text-[color:var(--text-muted)]
                   focus:outline-none focus:border-[color:var(--border-strong)] transition-colors"
          />
        </div>
      </header>

      <div class="flex-1 min-h-0 flex">
        {/* Provider rail */}
        <nav class="w-[14.5rem] shrink-0 border-r border-[color:var(--border-subtle)] overflow-y-auto py-4">
          <div class="px-4 pb-2 text-micro font-medium uppercase tracking-[0.07em] text-[color:var(--text-muted)] select-none">
            Providers
          </div>
          <For each={slots()}>
            {(slot) => {
              const c = () => counts()[slot.id] ?? { total: 0, enabled: 0, hits: 0 };
              const isActive = () => selected() === slot.id;
              const configured = () => isConfigured(slot, configs()[slot.id]);
              return (
                <button
                  type="button"
                  onClick={() => setSelected(slot.id)}
                  class={`group/row relative w-full flex items-center gap-2.5 h-8 pl-4 pr-3 text-ui text-left transition-colors
                    ${isActive()
                      ? 'bg-[color:var(--accent-soft)] text-[color:var(--accent)] font-medium'
                      : 'text-[color:var(--text-secondary)] hover:bg-[color:var(--bg-hover)] hover:text-[color:var(--text-primary)]'
                    }`}
                >
                  <Show when={isActive()}>
                    <span class="absolute left-0 top-1.5 bottom-1.5 w-[2px] rounded-full bg-[color:var(--accent)]" />
                  </Show>
                  <span
                    class={`w-1.5 h-1.5 rounded-full shrink-0 ${slot.dot} ${configured() ? '' : 'opacity-30'}`}
                    title={configured() ? 'Configured' : 'Not configured'}
                  />
                  <span class="truncate flex-1">{slot.label}</span>
                  <Show
                    when={query().trim()}
                    fallback={
                      <span class="text-micro tabular-nums text-[color:var(--text-muted)]">
                        {c().enabled}/{c().total}
                      </span>
                    }
                  >
                    <span
                      class={`text-micro tabular-nums ${c().hits > 0 ? 'text-[color:var(--text-secondary)]' : 'text-[color:var(--text-muted)] opacity-40'}`}
                    >
                      {c().hits}
                    </span>
                  </Show>
                </button>
              );
            }}
          </For>

          <div class="mx-4 mt-6 pt-4 border-t border-[color:var(--border-subtle)]">
            <p class="text-micro leading-[1.7] text-[color:var(--text-muted)]">
              Every other vendor — Gemini, DeepSeek, Groq, Together — connects through the
              <span class="text-[color:var(--text-tertiary)]"> OpenAI </span>
              slot.
            </p>
          </div>
        </nav>

        {/* Detail pane. The content sits in a bounded column with generous
            gutters: stretched edge to edge on a wide display the guidance
            became one long line and the price column drifted an inch away
            from the model it belongs to. */}
        <section class="flex-1 min-w-0 overflow-y-auto">
          {/* Not `keyed`. slots() is rebuilt from session.models(), so every
              model toggle — and every background catalogue refresh — produces
              a new Slot object. Keying on it tore the pane down and built it
              again, which silently emptied a half-typed API key and closed the
              add-model row mid-edit. Keying on nothing keeps the components
              alive and lets their props update in place. */}
          <Show when={activeSlot()}>
            {(slot) => (
              // One column width for the whole pane. When the table ran wider
              // than the guidance and the fields above it, the name and its
              // model ID drifted apart and the price floated off on its own.
              <div class="max-w-[48rem] px-8 py-7">
                <ProviderHeader slot={slot()} config={configs()[slot().id]} />

                <Show when={slot().def && !loadingConfigs()}>
                  <ConnectionBlock
                    def={slot().def!}
                    config={configs()[slot().id]}
                    onSaved={(c) => setConfigs({ ...configs(), [slot().id]: c })}
                  />
                </Show>

                <Show when={slot().readOnly}>
                  <p class="mt-7 text-meta leading-[1.7] text-[color:var(--text-tertiary)] max-w-[44rem]">
                    These models ship with ogcode and need no credentials. Toggle them off if you would
                    rather keep the picker to your own providers.
                  </p>
                </Show>

                <ModelTable
                  models={visibleModels()}
                  slot={slot()}
                  query={query()}
                  totalInSlot={counts()[slot().id]?.total ?? 0}
                  enabledInSlot={counts()[slot().id]?.enabled ?? 0}
                  configured={isConfigured(slot(), configs()[slot().id])}
                  enabledOnly={enabledOnly()}
                  onEnabledOnly={setEnabledOnly}
                  onToggle={(m) => session.toggleModel(m, !m.enabled)}
                  onRemove={async (m) => {
                    if (!confirm(`Remove "${m.name}"? This deletes the custom model.`)) return;
                    await session.removeCustomModel(m.id);
                  }}
                  onAdd={(id, name, collection) =>
                    session.addCustomModel(id, slot().id, name, collection || undefined)
                  }
                  suggestedCollection={
                    slot().id === 'openai'
                      ? collectionForBaseURL(configs()['openai']?.effectiveBaseUrl || configs()['openai']?.baseUrl || '')
                      : ''
                  }
                />

                <Show when={elsewhere().length > 0}>
                  <div class="mt-5 text-meta text-[color:var(--text-tertiary)] flex items-center gap-2 flex-wrap">
                    <span>Also matching elsewhere:</span>
                    <For each={elsewhere()}>
                      {(s) => (
                        <button
                          type="button"
                          onClick={() => setSelected(s.id)}
                          class="inline-flex items-center gap-1.5 h-6 px-2 rounded-[5px] border border-[color:var(--border-subtle)]
                                 bg-[color:var(--bg-elevated)] hover:border-[color:var(--border-strong)]
                                 text-[color:var(--text-secondary)] hover:text-[color:var(--text-primary)] transition-colors"
                        >
                          <span class={`w-1.5 h-1.5 rounded-full ${s.dot}`} />
                          {s.label}
                          <span class="tabular-nums text-[color:var(--text-muted)]">{counts()[s.id]?.hits ?? 0}</span>
                        </button>
                      )}
                    </For>
                  </div>
                </Show>
              </div>
            )}
          </Show>
        </section>
      </div>
    </div>
  );
}

/** A slot counts as configured when a key is set (app or env), or — for Ollama — an endpoint alone. */
function isConfigured(slot: Slot, config: ProviderConfig | undefined): boolean {
  if (slot.readOnly) return true;
  if (!config) return false;
  if (config.apiKey === '__SET__' || config.envKeySet) return true;
  return !!slot.def?.keyOptional && !!(config.effectiveBaseUrl || config.baseUrl);
}

// ---------- Provider header: what this slot is, and when to reach for it ----

function ProviderHeader(props: { slot: Slot; config: ProviderConfig | undefined }) {
  const guide = () => PROVIDER_GUIDE[props.slot.id];
  const pointedAt = () => {
    if (props.slot.id !== 'openai') return '';
    const url = props.config?.effectiveBaseUrl || props.config?.baseUrl || '';
    return collectionForBaseURL(url);
  };

  return (
    <div>
      <div class="flex items-center gap-2.5">
        <span class={`w-2 h-2 rounded-full ${props.slot.dot}`} />
        <h2 class="text-[0.9375rem] font-semibold tracking-[-0.01em] text-[color:var(--text-primary)]">{props.slot.label}</h2>
        {/* When the OpenAI slot is aimed at another vendor, say so in the title —
            it is the single most confusing state this screen can be in. */}
        <Show when={pointedAt()}>
          {(name) => (
            <span class="text-micro font-medium px-1.5 h-5 inline-flex items-center rounded-[4px] bg-[color:var(--accent-soft)] text-[color:var(--accent)]">
              pointed at {name()}
            </span>
          )}
        </Show>
      </div>

      <Show when={guide()} fallback={
        <p class="mt-2 text-meta text-[color:var(--text-tertiary)]">
          Bundled models — no configuration required.
        </p>
      }>
        {(g) => (
          <>
            <p class="mt-2 text-ui text-[color:var(--text-secondary)] max-w-[44rem]">{g().tagline}</p>
            {/* The two questions a provider has to answer, as a labelled pair
                rather than three same-weight sentences — at a glance you can
                find the one you came for instead of reading all of it. */}
            <dl class="mt-4 grid grid-cols-[5.5rem_minmax(0,1fr)] gap-x-4 gap-y-2.5 max-w-[46rem]">
              <dt class="text-micro font-medium uppercase tracking-[0.06em] text-[color:var(--text-muted)] pt-[3px]">Use when</dt>
              <dd class="text-meta leading-[1.65] text-[color:var(--text-secondary)]">{g().useWhen}</dd>
              <dt class="text-micro font-medium uppercase tracking-[0.06em] text-[color:var(--text-muted)] pt-[3px]">Trade-off</dt>
              <dd class="text-meta leading-[1.65] text-[color:var(--text-tertiary)]">{g().tradeoff}</dd>
            </dl>
          </>
        )}
      </Show>
    </div>
  );
}

// ---------- Connection: key + endpoint, always visible, never an accordion ---

function ConnectionBlock(props: {
  def: ProviderDef;
  config: ProviderConfig | undefined;
  onSaved: (c: ProviderConfig) => void;
}) {
  const [apiKey, setApiKey] = createSignal('');
  const [baseURL, setBaseURL] = createSignal('');
  const [saving, setSaving] = createSignal(false);
  const [saved, setSaved] = createSignal(false);
  const [error, setError] = createSignal('');
  let baseRef: HTMLInputElement | undefined;

  const guide = () => PROVIDER_GUIDE[props.def.id];

  // Re-seed the form when the pane switches providers — otherwise one
  // provider's endpoint would carry into another's form.
  //
  // The guard is load-bearing. `on(() => props.def.id, …)` looks like it fires
  // on id changes, but `on` re-runs its body whenever the dependency
  // *expression* re-evaluates, and reading `props.def` walks the slots memo
  // back to session.models(). Every model toggle and every background
  // catalogue refresh therefore re-ran the seed and wiped a half-typed API key
  // out of the field. Comparing the id ourselves makes those runs no-ops.
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

  const status = () => {
    if (envKeySet() && dbKeySet()) return { text: 'Key set — env and app', tone: 'ok' as const };
    if (envKeySet()) return { text: `Key set via ${guide()?.envKey ?? 'env'}`, tone: 'ok' as const };
    if (dbKeySet()) return { text: 'Key set', tone: 'ok' as const };
    if (props.def.keyOptional && (effectiveURL() || props.config?.baseUrl)) {
      return { text: 'Endpoint set — no key needed', tone: 'ok' as const };
    }
    return { text: 'Not configured', tone: 'off' as const };
  };

  // The server preserves a stored key ONLY when it receives the "__SET__"
  // sentinel (handleSetProviderConfig); every other value is written verbatim.
  // So an untouched, empty key field must send the sentinel — sending "" would
  // silently delete a working key the moment someone edited only the Base URL,
  // which is exactly what the endpoint presets invite you to do.
  const save = async () => {
    setError('');
    setSaved(false);
    setSaving(true);
    const typed = apiKey().trim();
    try {
      const result = await setProviderConfig(props.def.id, {
        apiKey: typed === '' && dbKeySet() ? '__SET__' : typed,
        baseUrl: baseURL().trim(),
      });
      props.onSaved(result);
      setApiKey('');
      setSaved(true);
      setTimeout(() => setSaved(false), 4000);
    } catch {
      setError('Could not save. Is the ogcode server still running?');
    } finally {
      setSaving(false);
    }
  };

  // Because a blank field now means "keep the stored key", clearing one needs
  // its own deliberate action rather than a side effect of saving.
  const clearKey = async () => {
    if (!confirm(`Remove the stored ${props.def.label} API key from ogcode?`)) return;
    setError('');
    setSaving(true);
    try {
      const result = await setProviderConfig(props.def.id, { apiKey: '', baseUrl: baseURL().trim() });
      props.onSaved(result);
      setApiKey('');
    } catch {
      setError('Could not remove the key.');
    } finally {
      setSaving(false);
    }
  };

  const field = `w-full h-8 px-2.5 rounded-md bg-[color:var(--bg-elevated)] border border-[color:var(--border-default)]
                 text-meta font-mono text-[color:var(--text-primary)] placeholder:text-[color:var(--text-muted)]
                 focus:outline-none focus:border-[color:var(--accent)] transition-colors`;

  return (
    <div class="mt-8 pt-7 border-t border-[color:var(--border-subtle)]">
      <div class="flex items-center gap-3 mb-4">
        <span class="text-micro font-medium uppercase tracking-[0.07em] text-[color:var(--text-muted)]">
          Connection
        </span>
        <span class="flex items-center gap-1.5">
          <span class={`w-1.5 h-1.5 rounded-full ${status().tone === 'ok' ? 'bg-emerald-400' : 'bg-[color:var(--text-muted)]'}`} />
          <span class={`text-micro font-medium ${status().tone === 'ok' ? 'text-emerald-400' : 'text-[color:var(--text-muted)]'}`}>
            {status().text}
          </span>
        </span>
        <div class="flex-1" />
        <Show when={guide()?.keysURL}>
          <a
            href={guide()!.keysURL}
            target="_blank"
            rel="noreferrer noopener"
            class="text-micro text-[color:var(--text-tertiary)] hover:text-[color:var(--accent)] transition-colors"
          >
            get a key at {guide()!.keysLabel} ↗
          </a>
        </Show>
      </div>

      <div class="space-y-3.5 max-w-[46rem]">
        <Row
          label="API key"
          note={
            <Show
              when={dbKeySet()}
              fallback={<>Read from <span class="font-mono text-[color:var(--text-tertiary)]">{guide()?.envKey}</span> if that variable is set.</>}
            >
              <>
                Leave blank to keep the stored key.
                <Show when={envKeySet()}>
                  {' '}<span class="font-mono text-[color:var(--text-tertiary)]">{guide()?.envKey}</span> is also set and takes priority over it.
                </Show>
                {' · '}
                <button
                  type="button"
                  onClick={clearKey}
                  class="text-[color:var(--text-tertiary)] hover:text-red-400 transition-colors underline underline-offset-2"
                >
                  remove stored key
                </button>
              </>
            </Show>
          }
        >
          <input
            type="password"
            value={apiKey()}
            onInput={(e) => setApiKey(e.currentTarget.value)}
            onKeyDown={(e) => { if (e.key === 'Enter') save(); }}
            placeholder={
              dbKeySet() ? 'leave blank to keep the saved key'
              : envKeySet() ? 'leave blank to use the environment key'
              : guide()?.keyHint ?? 'sk-…'
            }
            class={field}
          />
        </Row>

        <Show when={props.def.hasBaseURL}>
          <Row label="Base URL" hint="optional">
            <input
              ref={baseRef}
              type="text"
              value={baseURL()}
              onInput={(e) => setBaseURL(e.currentTarget.value)}
              onKeyDown={(e) => { if (e.key === 'Enter') save(); }}
              placeholder={guide()?.defaultBaseURL ?? ''}
              class={field}
            />
          </Row>
        </Show>

        {/* One-click endpoints — the whole point of the OpenAI slot. Clicking a
            vendor fills the field; the user still supplies that vendor's key
            and presses Save, so nothing changes behind their back. */}
        <Show when={props.def.id === 'openai'}>
          <Row label="Point at" hint="one at a time">
            <div class="flex flex-wrap gap-1.5">
              <PresetChip
                label="OpenAI"
                dot="bg-emerald-400"
                active={!collectionForBaseURL(baseURL())}
                onClick={() => setBaseURL('')}
              />
              <For each={COMPATIBLE_PRESETS}>
                {(preset) => (
                  <PresetChip
                    label={preset.label}
                    dot={preset.dot}
                    active={collectionForBaseURL(baseURL()) === preset.collection}
                    title={`${preset.baseURL} — key looks like ${preset.keyHint}`}
                    onClick={() => setBaseURL(preset.baseURL)}
                  />
                )}
              </For>
              <PresetChip
                label="Custom…"
                dot="bg-zinc-400"
                active={false}
                title="Any OpenAI-compatible endpoint — vLLM, LM Studio, LiteLLM, a company gateway"
                onClick={() => baseRef?.focus()}
              />
            </div>
          </Row>
        </Show>

        <Show when={endpointOverridden()}>
          <Row label="">
            <p class="text-micro text-[color:var(--text-tertiary)]">
              Currently calling <span class="font-mono text-[color:var(--accent)]">{effectiveURL()}</span>
              {props.config?.envBaseURLSet
                ? ` — ${guide()?.envBaseURL ?? 'the environment variable'} wins over the value above.`
                : ' — the saved endpoint was unreachable.'}
            </p>
          </Row>
        </Show>

        <div class="flex items-center gap-3 pt-1 pl-[6.5rem]">
          <button
            type="button"
            onClick={save}
            disabled={saving()}
            class="h-8 px-3.5 rounded-md text-meta font-medium bg-[color:var(--accent)] text-[color:var(--on-primary)]
                   hover:bg-[color:var(--accent-hover)] disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
          >
            {saving() ? 'Saving…' : 'Save'}
          </button>
          <span class="text-micro text-[color:var(--text-muted)]">
            <Show when={error()} fallback={
              <Show when={saved()} fallback="Applies after ogcode restarts.">
                <span class="text-emerald-400">Saved — restart ogcode to apply.</span>
              </Show>
            }>
              <span class="text-red-400">{error()}</span>
            </Show>
          </span>
        </div>
      </div>
    </div>
  );
}

function Row(props: { label: string; hint?: string; children: any; note?: any }) {
  return (
    <div class="flex items-start gap-4">
      <div class="w-[5.5rem] shrink-0 pt-1.5 text-meta text-[color:var(--text-tertiary)] leading-tight">
        {props.label}
        <Show when={props.hint}>
          <div class="text-micro text-[color:var(--text-muted)] mt-0.5">{props.hint}</div>
        </Show>
      </div>
      <div class="flex-1 min-w-0">
        {props.children}
        <Show when={props.note}>
          <div class="mt-1.5 text-micro text-[color:var(--text-muted)]">{props.note}</div>
        </Show>
      </div>
    </div>
  );
}

function PresetChip(props: { label: string; dot: string; active: boolean; title?: string; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={props.onClick}
      title={props.title}
      class={`inline-flex items-center gap-1.5 h-7 px-2.5 rounded-md text-micro font-medium border transition-colors
        ${props.active
          ? 'border-[color:var(--accent)] bg-[color:var(--accent-soft)] text-[color:var(--accent)]'
          : 'border-[color:var(--border-subtle)] bg-[color:var(--bg-elevated)] text-[color:var(--text-secondary)] hover:border-[color:var(--border-strong)] hover:text-[color:var(--text-primary)]'
        }`}
    >
      <span class={`w-1.5 h-1.5 rounded-full ${props.dot} ${props.active ? '' : 'opacity-70'}`} />
      {props.label}
    </button>
  );
}

// ---------- Model table ------------------------------------------------------

const GRID = 'grid grid-cols-[1.25rem_minmax(7rem,1fr)_minmax(0,1.4fr)_7rem_1.25rem] items-center gap-4';

function ModelTable(props: {
  models: ModelInfo[];
  slot: Slot;
  query: string;
  totalInSlot: number;
  enabledInSlot: number;
  configured: boolean;
  enabledOnly: boolean;
  onEnabledOnly: (v: boolean) => void;
  onToggle: (m: ModelInfo) => void | Promise<void>;
  onRemove: (m: ModelInfo) => void;
  onAdd: (id: string, name: string, collection: string) => Promise<void>;
  suggestedCollection: string;
}) {

  // Collection sub-headers only earn their line when a provider actually holds
  // more than one — Ollama's local vs cloud catalogue, or OpenAI plus models
  // added under a vendor collection.
  const sections = createMemo(() => {
    const map = new Map<string, ModelInfo[]>();
    for (const m of props.models) {
      const key = m.collection || '';
      const list = map.get(key) || [];
      list.push(m);
      map.set(key, list);
    }
    return [...map.entries()].sort(([a], [b]) => a.localeCompare(b));
  });

  const [bulkBusy, setBulkBusy] = createSignal(false);

  // Sequential, not a parallel fan-out. Each toggle POSTs one preference and
  // gets the *entire* model list back, which replaces local state — so firing
  // 400 of them at once (OpenRouter's catalogue) means 400 responses racing to
  // overwrite each other with snapshots taken before their siblings landed,
  // and the last one to arrive quietly undoes an arbitrary number of the rest.
  const setAll = async (enabled: boolean) => {
    if (bulkBusy()) return;
    const targets = props.models.filter((m) => m.enabled !== enabled);
    if (targets.length === 0) return;
    setBulkBusy(true);
    try {
      for (const m of targets) await props.onToggle(m);
    } finally {
      setBulkBusy(false);
    }
  };

  return (
    <div class="mt-8 pt-7 border-t border-[color:var(--border-subtle)]">
      {/* One header bar, not two. The previous version stacked a toolbar on a
          row of column labels; the labels said what the columns obviously
          were, so only the price unit survived — it is the one thing you
          cannot infer from the values. */}
      <div class="sticky top-0 z-10 bg-[color:var(--bg-base)] -mx-8 px-8 pb-3">
        <div class="flex items-center gap-3">
          <span class="text-micro font-medium uppercase tracking-[0.07em] text-[color:var(--text-muted)]">
            Models
          </span>
          <span class="text-micro tabular-nums text-[color:var(--text-muted)]">
            {props.enabledInSlot}/{props.totalInSlot} enabled
          </span>
          <div class="flex-1" />
          <Show when={props.totalInSlot > 0}>
            <button
              type="button"
              onClick={() => props.onEnabledOnly(!props.enabledOnly)}
              class={`text-micro transition-colors ${props.enabledOnly
                ? 'text-[color:var(--accent)]'
                : 'text-[color:var(--text-tertiary)] hover:text-[color:var(--text-primary)]'}`}
              title="Show only the models that appear in the picker"
            >
              Enabled only
            </button>
            <span class="text-[color:var(--border-strong)]">·</span>
            <button
              type="button"
              onClick={() => setAll(true)}
              disabled={bulkBusy()}
              class="text-micro text-[color:var(--text-tertiary)] hover:text-[color:var(--text-primary)] disabled:opacity-50 transition-colors"
            >
              {bulkBusy() ? 'Working…' : 'Enable all'}
            </button>
            <span class="text-[color:var(--border-strong)]">·</span>
            <button
              type="button"
              onClick={() => setAll(false)}
              disabled={bulkBusy()}
              class="text-micro text-[color:var(--text-tertiary)] hover:text-[color:var(--text-primary)] disabled:opacity-50 transition-colors"
            >
              None
            </button>
            {/* Caption for the price column, pushed clear of the action run so
                it does not read as a fourth button. */}
            <span class="text-micro text-[color:var(--text-muted)] ml-6">in / out per 1M</span>
          </Show>
        </div>
      </div>

      <For each={sections()}>
        {([collection, models]) => (
          <>
            <Show when={collection && sections().length > 1}>
              <div class="flex items-center gap-2 mt-4 mb-1.5">
                <span class="text-micro font-medium text-[color:var(--text-tertiary)]">{collection}</span>
                <span class="text-micro tabular-nums text-[color:var(--text-muted)]">{models.length}</span>
                <span class="flex-1 h-px bg-[color:var(--border-subtle)]" />
              </div>
            </Show>
            <For each={models}>
              {(m) => (
                <ModelRow
                  model={m}
                  onToggle={() => props.onToggle(m)}
                  onRemove={() => props.onRemove(m)}
                />
              )}
            </For>
          </>
        )}
      </For>

      {/* An empty list is nearly always a missing credential or a pending
          restart, so say which — "no models" on its own leaves the user with
          nothing to act on. */}
      <Show when={props.models.length === 0}>
        <div class="py-6 max-w-[44rem]">
          <p class="text-meta text-[color:var(--text-secondary)]">
            {props.query
              ? `Nothing here matches "${props.query}".`
              : props.enabledOnly
              ? 'Every model from this provider is currently disabled.'
              : props.configured
              ? `${props.slot.label} is connected, but its catalogue is empty.`
              : `${props.slot.label} has no models because it is not connected yet.`}
          </p>
          <Show when={!props.query && !props.enabledOnly}>
            <p class="mt-1.5 text-meta text-[color:var(--text-tertiary)] leading-relaxed">
              {props.configured
                ? props.slot.id === 'ollama'
                  ? 'Pull a model with `ollama pull qwen2.5-coder`, then restart ogcode.'
                  : 'Restart ogcode to refresh the catalogue, or add a model ID by hand below.'
                : props.slot.id === 'ollama'
                ? 'Install Ollama, pull a model, and point Base URL at it — no API key needed.'
                : 'Add an API key above and restart ogcode; its models then appear here.'}
            </p>
          </Show>
        </div>
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

function ModelRow(props: { model: ModelInfo; onToggle: () => void; onRemove: () => void }) {
  const hasPrice = () => props.model.inputPricePerM > 0 || props.model.outputPricePerM > 0;
  return (
    // 36px rows with the hover band bled into the gutter: the list breathes,
    // and the row you are pointing at is unmistakable without a border on
    // every single one.
    <div
      class={`${GRID} group/row -mx-3 px-3 h-9 rounded-md
              hover:bg-[color:var(--bg-hover)]/60 transition-colors
              ${props.model.enabled ? '' : 'opacity-50'}`}
    >
      <Check on={props.model.enabled} onClick={props.onToggle} label={props.model.name} />

      <div class="flex items-center gap-1.5 min-w-0">
        <span class="text-meta text-[color:var(--text-primary)] truncate">{props.model.name}</span>
        <Show when={props.model.default}>
          <Tag tone="accent">default</Tag>
        </Show>
        <Show when={props.model.isCustom}>
          <Tag tone="muted">custom</Tag>
        </Show>
        <Show when={subProviderLabel(props.model)}>
          {(label) => <Tag tone="muted">{label()}</Tag>}
        </Show>
      </div>

      <span class="text-micro font-mono text-[color:var(--text-muted)] truncate" title={props.model.id}>
        {props.model.id}
      </span>

      <span class="text-micro font-mono tabular-nums text-right text-[color:var(--text-tertiary)]">
        <Show when={hasPrice()} fallback={<span class="text-[color:var(--text-muted)]">—</span>}>
          ${fmtPrice(props.model.inputPricePerM)} / ${fmtPrice(props.model.outputPricePerM)}
        </Show>
      </span>

      <Show when={props.model.isCustom} fallback={<span />}>
        <button
          type="button"
          onClick={props.onRemove}
          title="Remove custom model"
          aria-label={`Remove ${props.model.name}`}
          class="w-5 h-5 flex items-center justify-center rounded text-[color:var(--text-muted)]
                 opacity-0 group-hover/row:opacity-100 hover:text-red-400 hover:bg-red-500/10 transition"
        >
          <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
          </svg>
        </button>
      </Show>
    </div>
  );
}

/** A checkbox, not a pill switch: this is a list of things to include, and a
 *  40px switch per row would double the row height for no added meaning. */
function Check(props: { on: boolean; onClick: () => void; label: string }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={props.on}
      aria-label={`${props.on ? 'Disable' : 'Enable'} ${props.label}`}
      onClick={props.onClick}
      class={`w-[15px] h-[15px] rounded-[4px] border flex items-center justify-center transition-colors
        ${props.on
          ? 'bg-[color:var(--accent)] border-[color:var(--accent)] text-[color:var(--on-primary)]'
          : 'border-[color:var(--border-strong)] text-transparent hover:border-[color:var(--text-tertiary)]'
        }`}
    >
      <svg class="w-2.5 h-2.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="3.5">
        <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
      </svg>
    </button>
  );
}

function Tag(props: { tone: 'accent' | 'muted'; children: any }) {
  return (
    <span
      class={`shrink-0 text-micro leading-none px-1 py-[3px] rounded-[3px] font-medium
        ${props.tone === 'accent'
          ? 'bg-[color:var(--accent-soft)] text-[color:var(--accent)]'
          : 'bg-[color:var(--bg-elevated)] text-[color:var(--text-muted)]'
        }`}
    >
      {props.children}
    </span>
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

  const field = `h-7 px-2 rounded-[5px] bg-[color:var(--bg-elevated)] border border-[color:var(--border-default)]
                 text-meta text-[color:var(--text-primary)] placeholder:text-[color:var(--text-muted)]
                 focus:outline-none focus:border-[color:var(--accent)] transition-colors min-w-0`;

  return (
    <Show
      when={open()}
      fallback={
        <button
          type="button"
          onClick={start}
          class="mt-3 -mx-3 px-3 h-9 w-[calc(100%+1.5rem)] rounded-md flex items-center gap-2 text-meta text-[color:var(--text-tertiary)]
                 hover:text-[color:var(--text-primary)] hover:bg-[color:var(--bg-hover)]/60 transition-colors"
        >
          <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
            <path stroke-linecap="round" stroke-linejoin="round" d="M12 5v14M5 12h14" />
          </svg>
          Add a model to {props.providerLabel}
        </button>
      }
    >
      <div class="mt-4 pt-4 border-t border-[color:var(--border-subtle)]">
        <div class="flex items-center gap-2">
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
              title="Groups this model under a vendor name in the picker"
              class={`${field} w-28`}
            />
          </Show>
          <button
            type="button"
            onClick={submit}
            disabled={busy()}
            class="h-7 px-3 shrink-0 rounded-[5px] text-meta font-medium bg-[color:var(--accent)] text-[color:var(--on-primary)]
                   hover:bg-[color:var(--accent-hover)] disabled:opacity-50 transition-colors"
          >
            {busy() ? 'Adding…' : 'Add'}
          </button>
          <button
            type="button"
            onClick={() => setOpen(false)}
            class="h-7 px-2 shrink-0 rounded-[5px] text-meta text-[color:var(--text-tertiary)]
                   hover:text-[color:var(--text-primary)] hover:bg-[color:var(--bg-hover)] transition-colors"
          >
            Cancel
          </button>
        </div>
        <p class="mt-1.5 text-micro text-[color:var(--text-muted)]">
          <Show when={error()} fallback="ogcode does not verify the ID — a typo shows up as a failed request on first use.">
            <span class="text-red-400">{error()}</span>
          </Show>
        </p>
      </div>
    </Show>
  );
}

// Always two decimals. In a right-aligned tabular column, "$1.6" next to
// "$0.40" breaks the decimal alignment that makes prices comparable at a
// glance — the point of putting them in a column at all. Sub-cent rates are
// real on aggregators, and rounding them to "0.00" would read as free.
function fmtPrice(n: number): string {
  if (n > 0 && n < 0.01) return '<0.01';
  return n.toFixed(2);
}
