import { Show, For, createMemo, createSignal, onMount } from 'solid-js';
import { useNavigate } from '@solidjs/router';
import { useServer } from '../../context/server';
import { useSession } from '../../context/session';
import { useTheme } from '../../context/theme';
import { getSearchConfig, setSearchConfig } from '../../api/client';

const PROVIDER_LABELS: Record<string, string> = {
  anthropic: 'Anthropic',
  openai: 'OpenAI',
  openrouter: 'OpenRouter',
  ollama: 'Ollama',
  google: 'Google',
  mistral: 'Mistral',
};

export default function GeneralSettings() {
  const server = useServer();
  const session = useSession();
  const theme = useTheme();
  const navigate = useNavigate();

  const stats = createMemo(() => {
    const all = session.models();
    const enabled = all.filter((m) => m.enabled);
    const providers = new Set(all.map((m) => m.providerId));
    const customs = all.filter((m) => m.isCustom);
    return {
      total: all.length,
      enabled: enabled.length,
      providers: providers.size,
      customs: customs.length,
    };
  });

  const defaultModel = createMemo(() => {
    const all = session.models();
    return all.find((m) => m.default && m.enabled) || all.find((m) => m.enabled);
  });

  return (
    <div class="max-w-3xl mx-auto px-8 py-10 anim-enter">
      {/* Page header */}
      <header class="mb-10">
        <h1 class="text-2xl font-semibold tracking-tight text-zinc-50">General</h1>
        <p class="text-[13px] text-zinc-500 mt-1.5">
          Workspace information and high-level defaults for this session.
        </p>
      </header>

      <div class="space-y-6">
        {/* Workspace card */}
        <Card title="Workspace" description="The directory ogcode is operating on.">
          <Row label="Directory">
            <span class="font-mono text-[12px] text-zinc-200 break-all">
              {server.directory() || '—'}
            </span>
          </Row>
          <Row label="Connection">
            <div class="flex items-center gap-2">
              <span class={`w-1.5 h-1.5 rounded-full ${server.connected() ? 'bg-emerald-400' : 'bg-zinc-600'}`} />
              <span class="text-[12px] text-zinc-200">
                {server.connected() ? 'Live' : 'Disconnected'}
              </span>
            </div>
          </Row>
          <Show when={server.branch()}>
            <Row label="Branch">
              <span class="font-mono text-[12px] text-zinc-200">{server.branch()}</span>
            </Row>
          </Show>
        </Card>

        {/* Search card */}
        <Card
          title="Web Search Agent"
          description="Enables parallel web research via headless Chrome. Build and note agents can call deep_search to fetch and synthesise live information. Requires a server restart to take effect."
        >
          <SearchConfigForm />
        </Card>

        {/* Theme card */}
        <Card
          title="Theme"
          description="Set a primary accent color for this project. The full palette is derived automatically and persisted per directory."
        >
          <ThemePicker />
        </Card>

        {/* Models summary */}
        <Card
          title="Models"
          description="Manage which AI models are available across your sessions."
          action={
            <button
              type="button"
              onClick={() => navigate('/settings/models')}
              class="h-8 px-3 text-[12px] font-medium text-zinc-200 border border-[color:var(--border-default)]
                     hover:border-[color:var(--border-strong)] hover:bg-[color:var(--bg-hover)]
                     rounded-lg transition flex items-center gap-1.5"
            >
              Manage
              <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M9 5l7 7-7 7" />
              </svg>
            </button>
          }
        >
          <div class="grid grid-cols-4 gap-px rounded-lg overflow-hidden bg-[color:var(--border-subtle)]">
            <Stat label="Enabled" value={stats().enabled} />
            <Stat label="Available" value={stats().total} />
            <Stat label="Providers" value={stats().providers} />
            <Stat label="Custom" value={stats().customs} />
          </div>
          <Show when={defaultModel()}>
            <div class="pt-3 mt-3 border-t border-[color:var(--border-subtle)]">
              <div class="text-[11px] text-zinc-500 mb-1">Default model</div>
              <div class="flex items-center gap-2">
                <span class="text-[13px] text-zinc-100 font-medium">{defaultModel()!.name}</span>
                <span class="text-[11px] text-zinc-500">
                  {PROVIDER_LABELS[defaultModel()!.providerId] || defaultModel()!.providerId}
                </span>
              </div>
            </div>
          </Show>
        </Card>

        {/* Model hotkeys */}
        <Card
          title="Model Hotkeys"
          description="Pin up to 4 models to numbered slots and switch between them instantly with Alt+1–4. Each slot holds one model — a model can only occupy a single slot at a time."
        >
          <ModelHotkeyConfig />
        </Card>

        {/* Keyboard shortcuts */}
        <Card title="Keyboard" description="Shortcuts available throughout the app.">
          <div class="space-y-2">
            <Shortcut keys={['Enter']} description="Send message" />
            <Shortcut keys={['Shift', 'Enter']} description="Insert newline" />
            <Shortcut keys={['⌘', 'N']} description="Start new session" />
            <Shortcut keys={['Alt', '1–4']} description="Switch to pinned model slot" />
          </div>
        </Card>
      </div>
    </div>
  );
}

function Card(props: { title: string; description?: string; action?: any; children: any }) {
  return (
    <section class="rounded-xl border border-[color:var(--border-subtle)] bg-[color:var(--bg-surface)] overflow-hidden">
      <header class="px-5 pt-4 pb-3 flex items-start justify-between gap-4 border-b border-[color:var(--border-subtle)]">
        <div class="min-w-0">
          <h2 class="text-[14px] font-semibold text-zinc-100">{props.title}</h2>
          <Show when={props.description}>
            <p class="text-[12px] text-zinc-500 mt-0.5">{props.description}</p>
          </Show>
        </div>
        {props.action}
      </header>
      <div class="px-5 py-4">{props.children}</div>
    </section>
  );
}

function Row(props: { label: string; children: any }) {
  return (
    <div class="flex items-start justify-between gap-4 py-2 first:pt-0 last:pb-0">
      <div class="text-[12px] text-zinc-500 shrink-0 pt-0.5 w-24">{props.label}</div>
      <div class="flex-1 min-w-0 text-right">{props.children}</div>
    </div>
  );
}

function Stat(props: { label: string; value: number | string }) {
  return (
    <div class="bg-[color:var(--bg-surface)] px-3 py-3 text-center">
      <div class="text-[18px] font-semibold text-zinc-100 tabular-nums leading-none">{props.value}</div>
      <div class="text-[10.5px] text-zinc-500 mt-1.5 uppercase tracking-wider">{props.label}</div>
    </div>
  );
}

function Shortcut(props: { keys: string[]; description: string }) {
  return (
    <div class="flex items-center justify-between py-1">
      <span class="text-[12.5px] text-zinc-300">{props.description}</span>
      <div class="flex items-center gap-1">
        <For each={props.keys}>
          {(k, i) => (
            <span class="flex items-center gap-1">
              <Show when={i() > 0}>
                <span class="text-zinc-600 text-[10px]">+</span>
              </Show>
              <kbd class="px-1.5 py-0.5 rounded border border-[color:var(--border-default)] bg-[color:var(--bg-elevated)] font-mono text-[10.5px] text-zinc-300">
                {k}
              </kbd>
            </span>
          )}
        </For>
      </div>
    </div>
  );
}


function SearchConfigForm() {
  const server = useServer();
  const [enabled, setEnabled] = createSignal(false);
  const [useRealProfile, setUseRealProfile] = createSignal(false);
  const [loading, setLoading] = createSignal(true);
  const [saving, setSaving] = createSignal(false);
  const [restartNeeded, setRestartNeeded] = createSignal(false);

  onMount(async () => {
    try {
      const cfg = await getSearchConfig();
      setEnabled(cfg.enabled);
      setUseRealProfile(cfg.useRealProfile);
    } catch {
      // defaults stay false
    } finally {
      setLoading(false);
    }
  });

  const save = async (next: { enabled: boolean; useRealProfile: boolean }) => {
    setSaving(true);
    try {
      await setSearchConfig(next);
      setRestartNeeded(true);
    } finally {
      setSaving(false);
    }
  };

  const handleToggle = async () => {
    const next = !enabled();
    setEnabled(next);
    try {
      await save({ enabled: next, useRealProfile: useRealProfile() });
    } catch {
      setEnabled(!next);
    }
  };

  const handleProfileToggle = async () => {
    const next = !useRealProfile();
    setUseRealProfile(next);
    try {
      await save({ enabled: enabled(), useRealProfile: next });
    } catch {
      setUseRealProfile(!next);
    }
  };

  return (
    <Show when={!loading()} fallback={
      <div class="py-4 flex items-center gap-2 text-[12px] text-zinc-500">
        <svg class="w-3.5 h-3.5 animate-spin" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
          <path stroke-linecap="round" stroke-linejoin="round" d="M12 4v4m0 8v4m8-8h-4M8 12H4" />
        </svg>
        Loading…
      </div>
    }>
      <div class="space-y-3">
        <div class="flex items-center justify-between gap-4">
          <div class="min-w-0">
            <div class="text-[13px] text-zinc-100 font-medium">Enable web search</div>
            <div class="text-[11.5px] text-zinc-500 mt-0.5 leading-snug">
              Starts a headless Chrome bridge at server launch. Build and note agents gain <code class="font-mono bg-[color:var(--bg-elevated)] px-1 rounded">deep_search</code> and <code class="font-mono bg-[color:var(--bg-elevated)] px-1 rounded">web_search</code> tools.
            </div>
          </div>
          <button
            type="button"
            role="switch"
            aria-checked={enabled()}
            disabled={saving()}
            onClick={handleToggle}
            class={`relative inline-flex h-5 w-9 shrink-0 cursor-pointer rounded-full border-2 border-transparent
              transition-colors duration-200 focus:outline-none disabled:opacity-50
              ${enabled() ? 'bg-[color:var(--accent)]' : 'bg-[color:var(--bg-hover)]'}`}
          >
            <span class={`pointer-events-none inline-block h-4 w-4 transform rounded-full bg-white shadow
              transition duration-200 ${enabled() ? 'translate-x-4' : 'translate-x-0'}`} />
          </button>
        </div>
        {/* Bridge live status */}
        <Show when={enabled() && !restartNeeded()}>
          <Show
            when={server.searchRunning()}
            fallback={
              <div class="flex items-center gap-2 text-[11.5px] text-red-400 bg-red-400/10 rounded-md px-3 py-2">
                <span class="w-1.5 h-1.5 rounded-full bg-red-400 shrink-0" />
                Bridge failed to start. Check that Node.js is installed and run <code class="font-mono bg-[color:var(--bg-elevated)] px-1 rounded">npx playwright install chromium</code> in <code class="font-mono bg-[color:var(--bg-elevated)] px-1 rounded">~/.local/share/ogcode/search-bridge/</code>, then restart.
              </div>
            }
          >
            <div class="flex items-center gap-2 text-[11.5px] text-emerald-400">
              <span class="w-1.5 h-1.5 rounded-full bg-emerald-400 shrink-0" />
              Bridge running — web_search, fetch_page and deep_search tools are active.
            </div>
          </Show>
        </Show>

        <Show when={restartNeeded()}>
          <div class="flex items-center gap-2 text-[11.5px] text-amber-400 bg-amber-400/10 rounded-md px-3 py-2">
            <svg class="w-3.5 h-3.5 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126ZM12 15.75h.007v.008H12v-.008Z" />
            </svg>
            Restart the server for this change to take effect.
          </div>
        </Show>
        <Show when={enabled()}>
          <div class="border-t border-[color:var(--border-subtle)] pt-3 mt-1 space-y-3">
            {/* Real Chrome profile toggle */}
            <div class="flex items-center justify-between gap-4">
              <div class="min-w-0">
                <div class="text-[13px] text-zinc-100 font-medium">Use real Chrome profile</div>
                <div class="text-[11.5px] text-zinc-500 mt-0.5 leading-snug">
                  Uses your Chrome cookies and logins for better search results. Chrome must be <strong class="text-zinc-400">fully closed</strong> before starting ogcode.
                </div>
              </div>
              <button
                type="button"
                role="switch"
                aria-checked={useRealProfile()}
                disabled={saving()}
                onClick={handleProfileToggle}
                class={`relative inline-flex h-5 w-9 shrink-0 cursor-pointer rounded-full border-2 border-transparent
                  transition-colors duration-200 focus:outline-none disabled:opacity-50
                  ${useRealProfile() ? 'bg-[color:var(--accent)]' : 'bg-[color:var(--bg-hover)]'}`}
              >
                <span class={`pointer-events-none inline-block h-4 w-4 transform rounded-full bg-white shadow
                  transition duration-200 ${useRealProfile() ? 'translate-x-4' : 'translate-x-0'}`} />
              </button>
            </div>

            {/* Real profile warning */}
            <Show when={useRealProfile()}>
              <div class="flex items-center gap-2 text-[11.5px] text-amber-400 bg-amber-400/10 rounded-md px-3 py-2">
                <svg class="w-3.5 h-3.5 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126ZM12 15.75h.007v.008H12v-.008Z" />
                </svg>
                Close Chrome completely before restarting ogcode. Chrome locks its profile — two processes cannot share it simultaneously.
              </div>
            </Show>
          </div>
        </Show>
      </div>
    </Show>
  );
}

function ModelHotkeyConfig() {
  const session = useSession();
  const slots = () => session.modelSlots();
  const enabledModels = createMemo(() => session.models().filter((m) => m.enabled));

  const modelName = (id: string | null): string => {
    if (!id) return 'Empty';
    const m = session.models().find((mod) => mod.id === id);
    return m ? m.name : 'Unknown model';
  };

  const handleChange = (slot: number, value: string) => {
    // value === "" means clear the slot
    session.setModelSlot(slot, value || null);
  };

  return (
    <div class="space-y-2">
      <For each={slots()}>
        {(modelId, index) => (
          <div class="flex items-center gap-3">
            <kbd class="px-2 py-1 rounded border border-[color:var(--border-default)] bg-[color:var(--bg-elevated)]
                         font-mono text-[12px] text-zinc-300 w-12 text-center shrink-0">
              Alt+{index() + 1}
            </kbd>
            <div class="flex-1 min-w-0">
              <select
                value={modelId || ''}
                onChange={(e) => handleChange(index(), e.currentTarget.value)}
                class="w-full h-8 px-2.5 rounded-md border border-[color:var(--border-default)]
                       bg-[color:var(--bg-elevated)] text-[12.5px] text-zinc-200
                       focus:outline-none focus:border-[color:var(--accent)] transition cursor-pointer"
              >
                <option value="">— Empty —</option>
                <For each={enabledModels()}>
                  {(m) => <option value={m.id}>{m.name}</option>}
                </For>
              </select>
            </div>
          </div>
        )}
      </For>
      <Show when={slots().every((s) => !s)}>
        <p class="text-[11.5px] text-zinc-500 pt-1 leading-relaxed">
          Assign a model to each slot, then press <kbd class="px-1 py-0.5 rounded border border-[color:var(--border-default)] bg-[color:var(--bg-elevated)] font-mono text-[10.5px] text-zinc-300">Alt</kbd> + the slot number anywhere in the app to switch instantly.
        </p>
      </Show>
    </div>
  );
}

function ThemePicker() {
  const themeCtx = useTheme();
  const [saving, setSaving] = createSignal(false);
  const [error, setError] = createSignal('');

  const isValidHex = (s: string) => /^#[0-9a-fA-F]{6}$/.test(s) || /^#[0-9a-fA-F]{3}$/.test(s);

  const handleInput = async (hex: string) => {
    setError('');
    if (!isValidHex(hex)) {
      setError('Invalid hex color (use #RRGGBB)');
      return;
    }
    setSaving(true);
    try {
      await themeCtx.setPrimaryColor(hex);
    } catch {
      setError('Failed to save theme');
    } finally {
      setSaving(false);
    }
  };

  const presets = [
    { label: 'Linear Violet', hex: '#5e6ad2' },
    { label: 'Blue', hex: '#3b82f6' },
    { label: 'Indigo', hex: '#6366f1' },
    { label: 'Violet', hex: '#8b5cf6' },
    { label: 'Rose', hex: '#f43f5e' },
    { label: 'Amber', hex: '#f59e0b' },
    { label: 'Emerald', hex: '#10b981' },
    { label: 'Cyan', hex: '#06b6d4' },
    { label: 'Pink', hex: '#ec4899' },
  ];

  return (
    <div class="space-y-4">
      <div class="flex items-center gap-3">
        {/* Native color picker */}
        <div class="relative">
          <input
            type="color"
            value={themeCtx.primaryColor()}
            onInput={(e) => handleInput(e.currentTarget.value)}
            disabled={saving()}
            class="w-10 h-10 rounded-lg border-2 border-[color:var(--border-default)] cursor-pointer
                   bg-transparent appearance-none [&::-webkit-color-swatch-wrapper]:p-0
                   [&::-webkit-color-swatch]:rounded-md [&::-webkit-color-swatch]:border-none"
          />
        </div>
        <div class="flex-1">
          <div class="flex items-center gap-2">
            <input
              type="text"
              value={themeCtx.primaryColor()}
              onChange={(e) => handleInput(e.currentTarget.value)}
              disabled={saving()}
              placeholder="#5e6ad2"
              class="w-28 h-8 px-2.5 rounded-md border border-[color:var(--border-default)]
                     bg-[color:var(--bg-elevated)] text-[12px] font-mono text-zinc-200
                     focus:outline-none focus:border-[color:var(--accent)] transition"
            />
            <Show when={saving()}>
              <svg class="w-3.5 h-3.5 animate-spin text-zinc-500" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
                <path stroke-linecap="round" stroke-linejoin="round" d="M12 4v4m0 8v4m8-8h-4M8 12H4" />
              </svg>
            </Show>
          </div>
          <Show when={error()}>
            <p class="text-[11px] text-red-400 mt-1">{error()}</p>
          </Show>
        </div>
        {/* Live preview swatch with derived accent */}
        <div class="flex items-center gap-1.5">
          <span class="text-[11px] text-zinc-500 mr-1">Preview</span>
          <span class="w-6 h-6 rounded-md border border-[color:var(--border-default)]" style={{ background: 'var(--accent)' }} />
          <span class="w-6 h-6 rounded-md border border-[color:var(--border-default)]" style={{ background: 'var(--accent-hover)' }} />
          <span class="w-6 h-6 rounded-md border border-[color:var(--border-default)]" style={{ background: 'var(--accent-soft)' }} />
          <span class="w-6 h-6 rounded-md border border-[color:var(--border-default)]" style={{ background: 'linear-gradient(var(--tint), var(--tint)) var(--bg-surface)' }} title="Sidebar tint" />
        </div>
      </div>

      {/* Preset swatches */}
      <div>
        <div class="text-[11px] text-zinc-500 mb-2">Presets</div>
        <div class="flex flex-wrap gap-2">
          <For each={presets}>
            {(p) => (
              <button
                type="button"
                onClick={() => handleInput(p.hex)}
                disabled={saving()}
                title={p.label}
                class={`w-7 h-7 rounded-lg border-2 transition-colors
                  ${themeCtx.primaryColor() === p.hex
                    ? 'border-white ring-2 ring-white/20'
                    : 'border-[color:var(--border-default)] hover:border-[color:var(--border-strong)]'
                  }`}
                style={{ background: p.hex }}
              />
            )}
          </For>
        </div>
      </div>

      <div class="pt-3 mt-1 border-t border-[color:var(--border-subtle)]">
        <p class="text-[11px] text-zinc-500 leading-relaxed">
          Theme is saved per project directory. Reopening ogcode from this path restores your colors automatically.
        </p>
      </div>
    </div>
  );
}
