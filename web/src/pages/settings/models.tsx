import { For, Show, createSignal, createMemo, onMount } from 'solid-js';
import { useSession } from '../../context/session';
import type { ModelInfo, ProviderConfig } from '../../api/client';
import { getProviderConfigs, setProviderConfig } from '../../api/client';
import { PROVIDER_DEFS, COMPATIBLE_PRESETS, modelGroup, type CompatiblePreset } from '../../lib/providers';

interface ProviderMeta {
  label: string;
  dot: string;
  text: string;
  bg: string;
  ring: string;
}

const PROVIDER_META: Record<string, ProviderMeta> = {
  anthropic:  { label: 'Anthropic',  dot: 'bg-orange-400',  text: 'text-orange-300',  bg: 'bg-orange-500/10',  ring: 'ring-orange-400/20' },
  openai:     { label: 'OpenAI',     dot: 'bg-emerald-400', text: 'text-emerald-300', bg: 'bg-emerald-500/10', ring: 'ring-emerald-400/20' },
  openrouter: { label: 'OpenRouter', dot: 'bg-violet-400',  text: 'text-violet-300',  bg: 'bg-violet-500/10',  ring: 'ring-violet-400/20' },
  ollama:     { label: 'Ollama',     dot: 'bg-sky-400',     text: 'text-sky-300',     bg: 'bg-sky-500/10',     ring: 'ring-sky-400/20' },
  google:     { label: 'Google',     dot: 'bg-blue-400',    text: 'text-blue-300',    bg: 'bg-blue-500/10',    ring: 'ring-blue-400/20' },
  mistral:    { label: 'Mistral',    dot: 'bg-rose-400',    text: 'text-rose-300',    bg: 'bg-rose-500/10',    ring: 'ring-rose-400/20' },
};

// Collection/group visual metadata for OpenAI-compatible providers so they get
// their own section styling instead of collapsing under "OpenAI".
const COLLECTION_META: Record<string, ProviderMeta> = {
  Gemini:   { label: 'Gemini',   dot: 'bg-blue-400',   text: 'text-blue-300',   bg: 'bg-blue-500/10',   ring: 'ring-blue-400/20' },
  DeepSeek: { label: 'DeepSeek', dot: 'bg-indigo-400', text: 'text-indigo-300', bg: 'bg-indigo-500/10', ring: 'ring-indigo-400/20' },
  Groq:     { label: 'Groq',     dot: 'bg-amber-400',  text: 'text-amber-300',  bg: 'bg-amber-500/10',  ring: 'ring-amber-400/20' },
  Together: { label: 'Together AI', dot: 'bg-teal-400', text: 'text-teal-300', bg: 'bg-teal-500/10',   ring: 'ring-teal-400/20' },
  Mistral:  { label: 'Mistral AI',  dot: 'bg-rose-400', text: 'text-rose-300', bg: 'bg-rose-500/10',  ring: 'ring-rose-400/20' },
};

function providerMeta(id: string): ProviderMeta {
  return PROVIDER_META[id] || COLLECTION_META[id] || { label: id, dot: 'bg-zinc-400', text: 'text-zinc-300', bg: 'bg-zinc-500/10', ring: 'ring-zinc-400/20' };
}

type Filter = 'all' | 'enabled' | 'custom';

export default function ModelsSettings() {
  const session = useSession();
  const [query, setQuery] = createSignal('');
  const [filter, setFilter] = createSignal<Filter>('all');
  const [addOpen, setAddOpen] = createSignal(false);
  const [addId, setAddId] = createSignal('');
  const [addProvider, setAddProvider] = createSignal('');
  const [addName, setAddName] = createSignal('');
  const [addCollection, setAddCollection] = createSignal('');
  const [addError, setAddError] = createSignal('');
  const [collapsed, setCollapsed] = createSignal<Record<string, boolean>>({});

  const allProviders = createMemo(() => {
    const ids = new Set<string>();
    for (const m of session.models()) ids.add(modelGroup(m));
    return [...ids].sort();
  });

  const filteredModels = createMemo(() => {
    const q = query().trim().toLowerCase();
    const f = filter();
    return session.models().filter((m) => {
      if (f === 'enabled' && !m.enabled) return false;
      if (f === 'custom' && !m.isCustom) return false;
      if (!q) return true;
      const group = modelGroup(m);
      return (
        m.name.toLowerCase().includes(q) ||
        m.id.toLowerCase().includes(q) ||
        (PROVIDER_META[m.providerId]?.label.toLowerCase().includes(q) ?? false) ||
        (COLLECTION_META[group]?.label.toLowerCase().includes(q) ?? false) ||
        group.toLowerCase().includes(q)
      );
    });
  });

  const grouped = createMemo(() => {
    const map = new Map<string, ModelInfo[]>();
    for (const m of filteredModels()) {
      const group = modelGroup(m);
      const list = map.get(group) || [];
      list.push(m);
      map.set(group, list);
    }
    return [...map.entries()].sort(([a], [b]) => a.localeCompare(b));
  });

  const totals = createMemo(() => {
    const all = session.models();
    return {
      total: all.length,
      enabled: all.filter((m) => m.enabled).length,
      custom: all.filter((m) => m.isCustom).length,
    };
  });

  const handleToggle = (m: ModelInfo) => session.toggleModel(m, !m.enabled);

  const handleAdd = async () => {
    const id = addId().trim();
    const provider = addProvider();
    const name = addName().trim() || id;
    if (!id) { setAddError('Model ID is required'); return; }
    if (!provider) { setAddError('Provider is required'); return; }
    try {
      await session.addCustomModel(id, provider, name, addCollection().trim() || undefined);
      setAddId(''); setAddProvider(''); setAddName(''); setAddCollection('');
      setAddError(''); setAddOpen(false);
    } catch (e: any) {
      setAddError(e.message || 'Failed to add model');
    }
  };

  const handleRemove = async (m: ModelInfo) => {
    if (!confirm(`Remove "${m.name}"? This will delete the custom model.`)) return;
    await session.removeCustomModel(m.id);
  };

  const toggleSection = (pid: string) => {
    setCollapsed({ ...collapsed(), [pid]: !collapsed()[pid] });
  };

  return (
    <div class="max-w-4xl mx-auto px-8 py-10 anim-enter">
      {/* Page header */}
      <header class="mb-8 flex items-start justify-between gap-4">
        <div>
          <h1 class="text-2xl font-semibold tracking-tight text-zinc-50">Models</h1>
          <p class="text-[13px] text-zinc-500 mt-1.5">
            Choose which models appear in the model picker. Add custom models from any provider.
          </p>
        </div>
        <button
          type="button"
          onClick={() => {
            setAddOpen(true);
            setAddProvider('');
            setAddError('');
          }}
          class="h-9 px-3.5 bg-[color:var(--accent)] hover:bg-[color:var(--accent-hover)]
                 text-white text-[12.5px] font-medium rounded-lg flex items-center gap-1.5 transition shadow-sm shrink-0"
        >
          <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
            <path stroke-linecap="round" stroke-linejoin="round" d="M12 4v16m8-8H4" />
          </svg>
          Add custom model
        </button>
      </header>

      {/* Info card: using OpenAI-compatible providers */}
      <CompatibleProvidersCard />

      {/* Provider credentials */}
      <ProviderCredsPanel />

      {/* Stats strip */}
      <div class="grid grid-cols-3 gap-px rounded-xl overflow-hidden bg-[color:var(--border-subtle)] mb-6">
        <StatCell label="Enabled" value={totals().enabled} hint={`of ${totals().total}`} />
        <StatCell label="Available" value={totals().total} hint={`across ${allProviders().length} providers`} />
        <StatCell label="Custom" value={totals().custom} hint="user added" />
      </div>

      {/* Add custom model panel */}
      <Show when={addOpen()}>
        <div class="mb-6 rounded-xl border border-[color:var(--border-default)] bg-[color:var(--bg-surface)] overflow-hidden animate-fade-in">
          <div class="px-5 py-3.5 border-b border-[color:var(--border-subtle)] flex items-center justify-between">
            <div class="flex items-center gap-2">
              <div class="w-7 h-7 rounded-md bg-[color:var(--accent-soft)] text-[color:var(--accent)] flex items-center justify-center">
                <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M12 4v16m8-8H4" />
                </svg>
              </div>
              <div>
                <div class="text-[13px] font-semibold text-zinc-100">Add custom model</div>
                <div class="text-[11px] text-zinc-500">Connect any model ID supported by the selected provider</div>
              </div>
            </div>
            <button
              type="button"
              onClick={() => { setAddOpen(false); setAddError(''); }}
              class="w-7 h-7 rounded-md text-zinc-500 hover:text-zinc-200 hover:bg-[color:var(--bg-hover)] flex items-center justify-center transition"
            >
              <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
              </svg>
            </button>
          </div>

          <div class="px-5 py-4 space-y-3">
            <div class="grid grid-cols-[1fr_180px] gap-3">
              <FormField label="Model ID" required>
                <input
                  type="text"
                  value={addId()}
                  onInput={(e) => setAddId(e.currentTarget.value)}
                  placeholder="e.g. gpt-4o-mini"
                  class="w-full h-9 px-3 rounded-lg bg-[color:var(--bg-elevated)] border border-[color:var(--border-default)]
                         text-[12.5px] text-zinc-100 placeholder-zinc-600 font-mono
                         focus:outline-none focus:border-[color:var(--border-strong)] transition"
                />
              </FormField>
              <FormField label="Provider" required>
                <select
                  value={addProvider()}
                  onChange={(e) => setAddProvider(e.currentTarget.value)}
                  class="w-full h-9 px-3 rounded-lg bg-[color:var(--bg-elevated)] border border-[color:var(--border-default)]
                         text-[12.5px] text-zinc-100 focus:outline-none focus:border-[color:var(--border-strong)] transition"
                >
                  <option value="">Select…</option>
                  <For each={PROVIDER_DEFS}>
                    {(def) => <option value={def.id}>{def.label}</option>}
                  </For>
                </select>
              </FormField>
            </div>
            <FormField label="Display name" hint="Optional. Defaults to the model ID.">
              <input
                type="text"
                value={addName()}
                onInput={(e) => setAddName(e.currentTarget.value)}
                placeholder="e.g. GPT-4o Mini"
                class="w-full h-9 px-3 rounded-lg bg-[color:var(--bg-elevated)] border border-[color:var(--border-default)]
                       text-[12.5px] text-zinc-100 placeholder-zinc-600
                       focus:outline-none focus:border-[color:var(--border-strong)] transition"
              />
            </FormField>
            <FormField label="Collection" hint="Optional. Groups this model in the picker (e.g. DeepSeek, Gemini).">
              <input
                type="text"
                list="collection-presets"
                value={addCollection()}
                onInput={(e) => setAddCollection(e.currentTarget.value)}
                placeholder="e.g. DeepSeek"
                class="w-full h-9 px-3 rounded-lg bg-[color:var(--bg-elevated)] border border-[color:var(--border-default)]
                       text-[12.5px] text-zinc-100 placeholder-zinc-600
                       focus:outline-none focus:border-[color:var(--border-strong)] transition"
              />
              <datalist id="collection-presets">
                <For each={COMPATIBLE_PRESETS}>
                  {(preset) => <option value={preset.collection}>{preset.label}</option>}
                </For>
              </datalist>
            </FormField>
            <Show when={addError()}>
              <div class="flex items-start gap-2 text-[12px] text-red-300 bg-red-500/10 border border-red-500/20 rounded-lg px-3 py-2">
                <svg class="w-3.5 h-3.5 mt-0.5 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.732 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z" />
                </svg>
                <span>{addError()}</span>
              </div>
            </Show>
            <div class="flex items-center justify-end gap-2 pt-1">
              <button
                type="button"
                onClick={() => { setAddOpen(false); setAddError(''); }}
                class="h-8 px-3 text-[12px] font-medium text-zinc-300 hover:text-zinc-100 hover:bg-[color:var(--bg-hover)] rounded-lg transition"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={handleAdd}
                class="h-8 px-3.5 bg-[color:var(--accent)] hover:bg-[color:var(--accent-hover)] text-[color:var(--on-primary)] text-[12px] font-medium rounded-lg transition shadow-sm"
              >
                Add model
              </button>
            </div>
          </div>
        </div>
      </Show>

      {/* Toolbar: search + filter */}
      <div class="flex items-center gap-3 mb-5">
        <div class="relative flex-1">
          <svg class="w-3.5 h-3.5 text-zinc-500 absolute left-3 top-1/2 -translate-y-1/2 pointer-events-none" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M21 21l-4.35-4.35M17 10a7 7 0 11-14 0 7 7 0 0114 0z" />
          </svg>
          <input
            type="text"
            value={query()}
            onInput={(e) => setQuery(e.currentTarget.value)}
            placeholder="Search models, IDs, or providers…"
            class="w-full h-9 pl-9 pr-3 rounded-lg bg-[color:var(--bg-surface)] border border-[color:var(--border-subtle)]
                   text-[12.5px] text-zinc-200 placeholder-zinc-500
                   focus:outline-none focus:border-[color:var(--border-strong)] transition"
          />
        </div>
        <div class="flex items-center p-0.5 rounded-lg bg-[color:var(--bg-surface)] border border-[color:var(--border-subtle)]">
          <FilterPill active={filter() === 'all'} onClick={() => setFilter('all')}>All</FilterPill>
          <FilterPill active={filter() === 'enabled'} onClick={() => setFilter('enabled')}>Enabled</FilterPill>
          <FilterPill active={filter() === 'custom'} onClick={() => setFilter('custom')}>Custom</FilterPill>
        </div>
      </div>

      {/* Provider sections */}
      <div class="space-y-4">
        <For each={grouped()}>
          {([pid, models]) => {
            const meta = providerMeta(pid);
            const enabledCount = () => models.filter((m) => m.enabled).length;
            const isCollapsed = () => collapsed()[pid];
            return (
              <section class="rounded-xl border border-[color:var(--border-subtle)] bg-[color:var(--bg-surface)] overflow-hidden">
                <button
                  type="button"
                  onClick={() => toggleSection(pid)}
                  class="w-full px-4 py-3 flex items-center gap-3 hover:bg-[color:var(--bg-hover)]/40 transition"
                >
                  <div class={`w-8 h-8 rounded-lg ${meta.bg} flex items-center justify-center ring-1 ${meta.ring}`}>
                    <span class={`w-2 h-2 rounded-full ${meta.dot}`} />
                  </div>
                  <div class="flex-1 text-left min-w-0">
                    <div class="text-[13.5px] font-semibold text-zinc-100">{meta.label}</div>
                    <div class="text-[11px] text-zinc-500 mt-0.5">
                      {enabledCount()} of {models.length} enabled
                    </div>
                  </div>
                  <svg
                    class={`w-4 h-4 text-zinc-500 transition-transform ${isCollapsed() ? '-rotate-90' : ''}`}
                    fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2"
                  >
                    <path stroke-linecap="round" stroke-linejoin="round" d="M19 9l-7 7-7-7" />
                  </svg>
                </button>
                <Show when={!isCollapsed()}>
                  <div class="border-t border-[color:var(--border-subtle)] divide-y divide-[color:var(--border-subtle)]">
                    <For each={models}>
                      {(m) => (
                        <ModelRow
                          model={m}
                          onToggle={() => handleToggle(m)}
                          onRemove={() => handleRemove(m)}
                        />
                      )}
                    </For>
                  </div>
                </Show>
              </section>
            );
          }}
        </For>

        <Show when={grouped().length === 0}>
          <div class="rounded-xl border border-dashed border-[color:var(--border-default)] bg-[color:var(--bg-surface)]/50 py-16 text-center">
            <div class="w-12 h-12 mx-auto rounded-xl bg-[color:var(--bg-elevated)] border border-[color:var(--border-subtle)] flex items-center justify-center mb-4">
              <svg class="w-5 h-5 text-zinc-500" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
                <path stroke-linecap="round" stroke-linejoin="round" d="M21 21l-4.35-4.35M17 10a7 7 0 11-14 0 7 7 0 0114 0z" />
              </svg>
            </div>
            <div class="text-[13px] text-zinc-300 font-medium">No models match your filters</div>
            <div class="text-[12px] text-zinc-500 mt-1">
              Try adjusting your search or switching the active filter.
            </div>
          </div>
        </Show>
      </div>
    </div>
  );
}

function ModelRow(props: { model: ModelInfo; onToggle: () => void; onRemove: () => void }) {
  const hasPrice = () => props.model.inputPricePerM > 0 || props.model.outputPricePerM > 0;
  return (
    <div class={`group px-4 py-3 flex items-center gap-3 transition ${props.model.enabled ? '' : 'opacity-60'}`}>
      <div class="flex-1 min-w-0">
        <div class="flex items-center gap-2 flex-wrap">
          <span class="text-[13px] text-zinc-100 font-medium">{props.model.name}</span>
          <Show when={props.model.default}>
            <Badge tone="blue">Default</Badge>
          </Show>
          <Show when={props.model.isCustom}>
            <Badge tone="violet">Custom</Badge>
          </Show>
          <Show when={hasPrice()}>
            <Badge tone="zinc">${fmtPrice(props.model.inputPricePerM)} / ${fmtPrice(props.model.outputPricePerM)}</Badge>
          </Show>
        </div>
        <div class="flex items-center gap-2 mt-0.5">
          <Show when={props.model.id !== props.model.name}>
            <span class="text-[11px] text-zinc-500 font-mono truncate">{props.model.id}</span>
          </Show>
          <Show when={hasPrice()}>
            <span class="text-[10px] text-zinc-600">in / out per 1M tokens</span>
          </Show>
        </div>
      </div>

      <Show when={props.model.isCustom}>
        <button
          type="button"
          onClick={props.onRemove}
          class="w-8 h-8 flex items-center justify-center rounded-md text-zinc-500
                 opacity-0 group-hover:opacity-100 hover:text-red-400 hover:bg-red-500/10 transition shrink-0"
          title="Remove custom model"
        >
          <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
            <path stroke-linecap="round" stroke-linejoin="round" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
          </svg>
        </button>
      </Show>

      <Toggle on={props.model.enabled} onClick={props.onToggle} />
    </div>
  );
}

function fmtPrice(n: number): string {
  if (n === 0) return '0';
  if (n < 0.01) return n.toFixed(2);
  if (n < 1) return n.toFixed(2).replace(/0+$/, '').replace(/\.$/, '');
  if (Number.isInteger(n)) return String(n);
  return n.toFixed(2).replace(/0+$/, '').replace(/\.$/, '');
}

function Toggle(props: { on: boolean; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={props.onClick}
      role="switch"
      aria-checked={props.on}
      class={`group relative inline-flex items-center h-[22px] w-[38px] shrink-0 rounded-full
              transition-colors duration-200 ease-out
              focus:outline-none focus-visible:ring-2 focus-visible:ring-[color:var(--accent-ring)]
        ${props.on
          ? 'bg-[color:var(--accent)] shadow-[inset_0_0_0_1px_rgba(255,255,255,0.10)]'
          : 'bg-[color:var(--bg-hover)] hover:bg-[color:var(--bg-elevated)] shadow-[inset_0_0_0_1px_rgba(255,255,255,0.04)]'
        }`}
      title={props.on ? 'Enabled' : 'Disabled'}
    >
      <span
        aria-hidden
        class={`pointer-events-none inline-block h-[18px] w-[18px] rounded-full bg-white
                shadow-[0_1px_2px_rgba(0,0,0,0.45),0_0_0_0.5px_rgba(0,0,0,0.06)]
                transform transition-transform duration-200 ease-out
                group-active:scale-95
          ${props.on ? 'translate-x-[18px]' : 'translate-x-[2px]'}`}
      />
    </button>
  );
}

function Badge(props: { tone: 'blue' | 'violet' | 'zinc'; children: any }) {
  const tones = {
    blue: 'text-[color:var(--accent)] bg-[color:var(--accent-soft)] ring-[color:var(--accent-ring)]',
    violet: 'text-violet-300 bg-violet-500/10 ring-violet-400/20',
    zinc: 'text-zinc-300 bg-zinc-500/10 ring-zinc-400/20',
  };
  return (
    <span class={`text-[10px] font-medium px-1.5 py-0.5 rounded ring-1 ${tones[props.tone]}`}>
      {props.children}
    </span>
  );
}

function StatCell(props: { label: string; value: number | string; hint?: string }) {
  return (
    <div class="bg-[color:var(--bg-surface)] px-4 py-3.5">
      <div class="text-[10.5px] text-zinc-500 uppercase tracking-wider font-medium">{props.label}</div>
      <div class="mt-1 flex items-baseline gap-1.5">
        <div class="text-[20px] font-semibold text-zinc-100 tabular-nums leading-none">{props.value}</div>
        <Show when={props.hint}>
          <div class="text-[11px] text-zinc-500">{props.hint}</div>
        </Show>
      </div>
    </div>
  );
}

function FilterPill(props: { active: boolean; onClick: () => void; children: any }) {
  return (
    <button
      type="button"
      onClick={props.onClick}
      class={`h-7 px-3 text-[11.5px] font-medium rounded-md transition
        ${props.active
          ? 'bg-[color:var(--bg-elevated)] text-zinc-100 shadow-sm'
          : 'text-zinc-400 hover:text-zinc-200'
        }`}
    >
      {props.children}
    </button>
  );
}

// ---------- OpenAI-compatible providers info card ----------

function CompatibleProvidersCard() {
  const [dismissed, setDismissed] = createSignal(false);

  return (
    <Show when={!dismissed()}>
      <div class="mb-6 rounded-xl border border-[color:var(--border-subtle)] bg-[color:var(--bg-surface)] overflow-hidden">
        <div class="px-5 py-4">
          {/* Header */}
          <div class="flex items-start gap-3">
            <div class="w-8 h-8 rounded-lg bg-emerald-500/10 ring-1 ring-emerald-400/20 flex items-center justify-center shrink-0 mt-0.5">
              <svg class="w-4 h-4 text-emerald-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M11.25 11.25l.041-.02a.75.75 0 011.063.852l-.708 2.836a.75.75 0 001.063.853l.041-.021M21 12a9 9 0 11-18 0 9 9 0 0118 0zm-9-3.75h.008v.008H12V8.25z" />
              </svg>
            </div>
            <div class="flex-1 min-w-0">
              <div class="text-[13.5px] font-semibold text-zinc-100">Use OpenAI-compatible providers</div>
              <p class="text-[12px] text-zinc-400 mt-1 leading-relaxed">
                Providers like DeepSeek, Google Gemini, Groq, Together AI, and Mistral AI all speak the OpenAI API.
                You can connect to any of them through the <span class="text-zinc-200 font-medium">OpenAI</span> provider by
                setting a custom <span class="text-zinc-200 font-medium">Base URL</span> and your provider's API key — no separate integration needed.
              </p>
            </div>
            <button
              type="button"
              onClick={() => setDismissed(true)}
              class="w-7 h-7 rounded-md text-zinc-500 hover:text-zinc-200 hover:bg-[color:var(--bg-hover)] flex items-center justify-center transition shrink-0"
              title="Dismiss"
            >
              <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
              </svg>
            </button>
          </div>

          {/* Steps */}
          <ol class="mt-4 space-y-2.5">
            <StepRow n={1}>
              Open the <span class="text-zinc-200 font-medium">API Keys</span> section below and expand the
              <span class="inline-flex items-center gap-1.5 mx-1">
                <span class="w-2 h-2 rounded-full bg-emerald-400" />
                <span class="text-zinc-200 font-medium">OpenAI</span>
              </span>
              row.
            </StepRow>
            <StepRow n={2}>
              Paste your provider's API key (e.g. a DeepSeek <code class="font-mono text-zinc-300 bg-[color:var(--bg-elevated)] px-1.5 py-0.5 rounded text-[11px]">sk-…</code> key or a Google
              <code class="font-mono text-zinc-300 bg-[color:var(--bg-elevated)] px-1.5 py-0.5 rounded text-[11px] mx-1">AIza…</code> key).
            </StepRow>
            <StepRow n={3}>
              Set the <span class="text-zinc-200 font-medium">Base URL</span> to your provider's OpenAI-compatible endpoint (see the list below).
            </StepRow>
            <StepRow n={4}>
              Save and restart ogcode, then click <span class="text-zinc-200 font-medium">Add custom model</span> to add a model from that provider.
              Use the <span class="text-zinc-200 font-medium">Collection</span> field to group it under the provider name in the model picker.
            </StepRow>
          </ol>

          {/* Preset list */}
          <div class="mt-4 grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-2">
            <For each={COMPATIBLE_PRESETS}>
              {(preset) => (
                <div class="flex items-center gap-2.5 rounded-lg bg-[color:var(--bg-elevated)]/60 border border-[color:var(--border-subtle)] px-3 py-2">
                  <span class={`w-2 h-2 rounded-full ${preset.dot} shrink-0`} />
                  <div class="min-w-0 flex-1">
                    <div class="text-[12px] font-medium text-zinc-200 truncate">{preset.label}</div>
                    <div class="text-[10.5px] text-zinc-500 font-mono truncate">{preset.baseURL}</div>
                  </div>
                </div>
              )}
            </For>
          </div>
        </div>
      </div>
    </Show>
  );
}

function StepRow(props: { n: number; children: any }) {
  return (
    <li class="flex items-start gap-2.5">
      <span class="shrink-0 w-5 h-5 rounded-full bg-[color:var(--accent-soft)] text-[color:var(--accent)] flex items-center justify-center text-[11px] font-semibold tabular-nums mt-px">
        {props.n}
      </span>
      <span class="text-[12px] text-zinc-400 leading-relaxed">{props.children}</span>
    </li>
  );
}

// ---------- Provider credentials panel ----------

function ProviderCredsPanel() {
  const [configs, setConfigs] = createSignal<Record<string, ProviderConfig>>({});
  const [expanded, setExpanded] = createSignal<string | null>(null);
  const [loading, setLoading] = createSignal(true);

  onMount(async () => {
    try {
      const list = await getProviderConfigs();
      const map: Record<string, ProviderConfig> = {};
      for (const c of list) map[c.providerId] = c;
      setConfigs(map);
    } finally {
      setLoading(false);
    }
  });

  const toggle = (id: string) => setExpanded(expanded() === id ? null : id);

  return (
    <div class="rounded-xl border border-[color:var(--border-subtle)] bg-[color:var(--bg-surface)] overflow-hidden mb-6">
      <div class="px-5 py-3.5 border-b border-[color:var(--border-subtle)]">
        <div class="text-[14px] font-semibold text-zinc-100">API Keys</div>
        <div class="text-[12px] text-zinc-500 mt-0.5">
          Add credentials for each provider. Restart ogcode to activate changes.
        </div>
      </div>

      <Show when={!loading()} fallback={
        <div class="px-5 py-4 text-[12px] text-zinc-500">Loading…</div>
      }>
        <div class="divide-y divide-[color:var(--border-subtle)]">
          <For each={PROVIDER_DEFS}>
            {(def) => (
              <ProviderCredRow
                def={def}
                config={configs()[def.id]}
                expanded={expanded() === def.id}
                onToggle={() => toggle(def.id)}
                onSaved={(c) => setConfigs({ ...configs(), [def.id]: c })}
              />
            )}
          </For>
        </div>
      </Show>
    </div>
  );
}

function ProviderCredRow(props: {
  def: typeof PROVIDER_DEFS[number];
  config: ProviderConfig | undefined;
  expanded: boolean;
  onToggle: () => void;
  onSaved: (c: ProviderConfig) => void;
}) {
  const [apiKey, setApiKey] = createSignal('');
  const [baseURL, setBaseURL] = createSignal('');
  const [saving, setSaving] = createSignal(false);
  const [error, setError] = createSignal('');
  const [saved, setSaved] = createSignal(false);

  // Populate form when row expands.
  const handleToggle = () => {
    if (!props.expanded) {
      setApiKey(props.config?.apiKey || '');
      setBaseURL(props.config?.baseUrl || '');
      setError('');
      setSaved(false);
    }
    props.onToggle();
  };

  const handleSave = async () => {
    setError('');
    setSaved(false);
    setSaving(true);
    try {
      const saved = await setProviderConfig(props.def.id, {
        apiKey: apiKey(),
        baseUrl: baseURL(),
      });
      props.onSaved(saved);
      setSaved(true);
      setTimeout(() => setSaved(false), 3000);
    } catch {
      setError('Failed to save');
    } finally {
      setSaving(false);
    }
  };

  // Key is "active" when set in DB or in the environment. Env takes priority at runtime.
  const dbKeySet  = () => props.config?.apiKey === '__SET__';
  const envKeySet = () => !!(props.config?.envKeySet);
  const isSet     = () => dbKeySet() || envKeySet();

  const statusLabel = () => {
    if (envKeySet() && dbKeySet()) return 'Key set (env + app)';
    if (envKeySet()) return 'Key set via env';
    if (dbKeySet()) return 'Key set';
    return 'Not configured';
  };

  const inputCls = 'w-full h-8 px-2.5 rounded-md border border-[color:var(--border-default)] bg-[color:var(--bg-elevated)] text-[12px] text-zinc-200 font-mono focus:outline-none focus:border-[color:var(--accent)] transition';

  return (
    <div>
      {/* Row header — always visible */}
      <button
        type="button"
        onClick={handleToggle}
        class="w-full px-5 py-3 flex items-center gap-3 hover:bg-[color:var(--bg-hover)]/40 transition text-left"
      >
        <div class={`w-7 h-7 rounded-lg ${props.def.bg} flex items-center justify-center ring-1 ${props.def.ring} shrink-0`}>
          <span class={`w-2 h-2 rounded-full ${props.def.dot}`} />
        </div>
        <span class="flex-1 text-[13px] font-medium text-zinc-200">{props.def.label}</span>
        <span class={`text-[11px] flex items-center gap-1.5 ${isSet() ? 'text-emerald-400' : 'text-zinc-600'}`}>
          <span class={`w-1.5 h-1.5 rounded-full ${isSet() ? 'bg-emerald-400' : 'bg-zinc-600'}`} />
          {statusLabel()}
        </span>
        <svg
          class={`w-4 h-4 text-zinc-500 transition-transform shrink-0 ${props.expanded ? 'rotate-180' : ''}`}
          fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2"
        >
          <path stroke-linecap="round" stroke-linejoin="round" d="M19 9l-7 7-7-7" />
        </svg>
      </button>

      {/* Expandable form */}
      <Show when={props.expanded}>
        <div class="px-5 pb-4 pt-1 space-y-3 border-t border-[color:var(--border-subtle)] bg-[color:var(--bg-elevated)]/30">

          {/* Env-var notice */}
          <Show when={envKeySet()}>
            <div class="flex items-start gap-2 px-3 py-2 rounded-lg bg-sky-500/8 border border-sky-500/20 text-[11.5px] text-sky-300">
              <svg class="w-3.5 h-3.5 shrink-0 mt-[1px]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
              </svg>
              <span>
                Key is active from the <code class="font-mono text-sky-200 bg-sky-500/15 px-1 rounded text-[10.5px]">{providerEnvVar(props.def.id)}</code> environment variable.
                Enter a key below only if you want to override it inside the app.
              </span>
            </div>
          </Show>

          <div>
            <label class="block text-[11px] text-zinc-500 mb-1.5">
              API key
              <Show when={dbKeySet()}>
                <span class="ml-1.5 text-emerald-500">● already set in app</span>
              </Show>
            </label>
            <input
              type="password"
              value={apiKey() === '__SET__' ? '' : apiKey()}
              onInput={e => setApiKey(e.currentTarget.value)}
              placeholder={
                dbKeySet()  ? 'leave blank to keep existing key' :
                envKeySet() ? 'leave blank to use env var key'   :
                              'sk-… or leave blank to use env var'
              }
              class={inputCls}
            />
          </div>

          <Show when={props.def.hasBaseURL}>
            <div>
              <label class="block text-[11px] text-zinc-500 mb-1.5">
                Base URL <span class="text-zinc-600">(optional)</span>
                <Show when={props.config?.envBaseURLSet}>
                  <span class="ml-1.5 text-sky-400">● set via env</span>
                </Show>
              </label>
              <input
                type="text"
                value={baseURL()}
                onInput={e => setBaseURL(e.currentTarget.value)}
                placeholder={
                  props.def.id === 'ollama'    ? 'http://localhost:11434/v1' :
                  props.def.id === 'anthropic' ? 'https://api.anthropic.com/v1' :
                                                 'https://api.openai.com/v1'
                }
                class={inputCls}
              />
            </div>
          </Show>

          <div class="flex items-center justify-between gap-3 pt-1">
            <Show when={error()}>
              <p class="text-[11px] text-red-400">{error()}</p>
            </Show>
            <Show when={saved() && !error()}>
              <p class="text-[11px] text-emerald-400">Saved — restart ogcode to apply.</p>
            </Show>
            <Show when={!error() && !saved()}>
              <p class="text-[11px] text-zinc-600">Changes apply after restarting ogcode.</p>
            </Show>
            <button
              type="button"
              onClick={handleSave}
              disabled={saving()}
              class="shrink-0 h-8 px-4 text-[12px] font-medium rounded-lg transition
                bg-[color:var(--accent)] text-[color:var(--on-primary)] hover:bg-[color:var(--accent-hover)]
                disabled:opacity-50 disabled:cursor-not-allowed flex items-center gap-1.5"
            >
              <Show when={saving()}>
                <svg class="w-3 h-3 animate-spin" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M12 4v4m0 8v4m8-8h-4M8 12H4" />
                </svg>
              </Show>
              {saving() ? 'Saving…' : 'Save'}
            </button>
          </div>
        </div>
      </Show>
    </div>
  );
}

function providerEnvVar(id: string): string {
  const map: Record<string, string> = {
    anthropic:  'ANTHROPIC_API_KEY',
    openai:     'OPENAI_API_KEY',
    openrouter: 'OPENROUTER_API_KEY',
    ollama:     'OLLAMA_API_KEY',
  };
  return map[id] ?? `${id.toUpperCase()}_API_KEY`;
}

function FormField(props: { label: string; required?: boolean; hint?: string; children: any }) {
  return (
    <label class="block">
      <div class="flex items-baseline justify-between mb-1.5">
        <span class="text-[11.5px] font-medium text-zinc-300">
          {props.label}
          <Show when={props.required}>
            <span class="text-[color:var(--accent)] ml-0.5">*</span>
          </Show>
        </span>
        <Show when={props.hint}>
          <span class="text-[10.5px] text-zinc-500">{props.hint}</span>
        </Show>
      </div>
      {props.children}
    </label>
  );
}
