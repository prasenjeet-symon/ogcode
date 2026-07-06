import { useNavigate } from '@solidjs/router';
import { For, Show, createSignal, createMemo, onMount } from 'solid-js';
import { useOnboarding } from '../context/onboarding';
import { useSession } from '../context/session';
import { setProviderConfig, validateProviderConfig, type ModelInfo } from '../api/client';
import { PROVIDER_DEFS, type ProviderDef } from '../lib/providers';

// Providers that work without an API key (local).
const KEYLESS = new Set(['ollama']);
const OLLAMA_DEFAULT_BASE_URL = 'http://localhost:11434/v1';

const KEY_HINTS: Record<string, string> = {
  anthropic: 'sk-ant-...',
  openai: 'sk-...',
  openrouter: 'sk-or-...',
  ollama: 'Optional — only for secured/remote Ollama',
};

// isCloudURL mirrors the backend (internal/provider.openai.go isCloudURL) so the
// onboarding wizard can decide whether Ollama is pointing at a remote/cloud
// endpoint that may require an API key, versus a local install that never does.
// An empty base URL is treated as local (defaults to the Ollama localhost URL).
function isCloudURL(baseURL: string): boolean {
  const u = baseURL.trim().toLowerCase();
  if (!u) return false;
  if (u.includes('localhost') || u.includes('127.0.0.1') || u.includes('0.0.0.0')) {
    return false;
  }
  if (u.includes('://10.') || u.includes('://192.168.')) return false;
  for (let i = 16; i <= 31; i++) {
    if (u.includes(`://172.${i}.`)) return false;
  }
  return true;
}

// Onboarding wizard shown on first run when no LLM provider is configured.
// Step 1 pick provider → step 2 enter credentials → step 3 choose default model.
//
// When a local Ollama server is detected as running, a dedicated "ready to go"
// screen is shown instead of the credential steps — the user can pick a model
// and start chatting immediately (zero-config flow).
export default function Onboarding() {
  const onboarding = useOnboarding();
  const session = useSession();
  const navigate = useNavigate();

  // 'ready' is a special step shown when Ollama is detected as running. It
  // skips credentials and goes straight to model selection.
  const [step, setStep] = createSignal<1 | 2 | 3 | 'ready'>(1);
  const [providerId, setProviderId] = createSignal('');
  const [apiKey, setApiKey] = createSignal('');
  const [baseUrl, setBaseUrl] = createSignal('');
  const [saving, setSaving] = createSignal(false);
  const [error, setError] = createSignal('');
  const [selectedModelId, setSelectedModelId] = createSignal('');
  const [modelQuery, setModelQuery] = createSignal('');
  const [testing, setTesting] = createSignal(false);
  const [testResult, setTestResult] = createSignal<'idle' | 'ok' | 'fail'>('idle');
  const [testMessage, setTestMessage] = createSignal('');

  const ollamaStatus = () => onboarding.ollamaStatus();

  // On mount, if Ollama is detected as running, jump straight to the "ready"
  // screen so the user sees the zero-config welcome instead of the provider
  // picker. We only do this once (before the user has chosen anything).
  onMount(() => {
    if (ollamaStatus()?.running) {
      setProviderId('ollama');
      setBaseUrl(OLLAMA_DEFAULT_BASE_URL);
      setStep('ready');
    }
  });

  const def = createMemo<ProviderDef | undefined>(() =>
    PROVIDER_DEFS.find((p) => p.id === providerId()),
  );
  // A key field is shown for non-keyless providers, and also for Ollama when the
  // base URL points at a remote/cloud endpoint (where a key may be required).
  const showKey = createMemo(() => !KEYLESS.has(providerId()) || isCloudURL(baseUrl()));
  // The key is strictly required only for non-keyless providers. For Ollama
  // (local or cloud) the key remains optional even when the field is shown.
  const keyRequired = createMemo(() => !KEYLESS.has(providerId()));
  const providerModels = createMemo<ModelInfo[]>(() =>
    session.models().filter((m) => m.providerId === providerId()),
  );
  const filteredModels = createMemo<ModelInfo[]>(() => {
    const q = modelQuery().trim().toLowerCase();
    const models = providerModels();
    if (!q) return models;
    return models.filter(
      (m) => m.name.toLowerCase().includes(q) || m.id.toLowerCase().includes(q),
    );
  });

  const resetTest = () => {
    setTestResult('idle');
    setTestMessage('');
  };

  const pickProvider = (id: string) => {
    setProviderId(id);
    setError('');
    setApiKey('');
    setBaseUrl(id === 'ollama' ? OLLAMA_DEFAULT_BASE_URL : '');
    setModelQuery('');
    resetTest();
    setStep(2);
  };

  const testKey = async () => {
    setError('');
    if (keyRequired() && !apiKey().trim()) {
      setError('Please enter your API key first.');
      return;
    }
    setTesting(true);
    resetTest();
    try {
      const res = await validateProviderConfig(providerId(), {
        apiKey: apiKey().trim(),
        baseUrl: baseUrl().trim(),
      });
      if (res.ok) {
        setTestResult('ok');
      } else {
        setTestResult('fail');
        setTestMessage(res.error || 'Connection failed.');
      }
    } catch (e: any) {
      setTestResult('fail');
      setTestMessage(e?.message || 'Connection failed.');
    } finally {
      setTesting(false);
    }
  };

  const saveAndContinue = async () => {
    setError('');
    if (keyRequired() && !apiKey().trim()) {
      setError('Please enter your API key.');
      return;
    }
    setSaving(true);
    try {
      await setProviderConfig(providerId(), {
        apiKey: apiKey().trim(),
        baseUrl: baseUrl().trim(),
      });
      // The provider hot-reloads server-side on save, so the model list now
      // reflects the new credentials.
      await session.refreshModels();
      const models = providerModels();
      if (models.length === 0) {
        setError(
          'Saved, but no models were found for this provider. Double-check your API key or base URL.',
        );
        return;
      }
      const preferred =
        models.find((m) => m.default && m.enabled) ||
        models.find((m) => m.enabled) ||
        models[0];
      setSelectedModelId(preferred.id);
      setStep(3);
    } catch (e: any) {
      setError(e?.message || 'Failed to save provider configuration.');
    } finally {
      setSaving(false);
    }
  };

  // Ollama is already running and registered server-side — no credentials to
  // save. Refresh the model list (which fetches pulled models from Ollama) and
  // go straight to model selection.
  const goReadyToModels = async () => {
    setError('');
    setSaving(true);
    try {
      await session.refreshModels();
      const models = providerModels();
      if (models.length === 0) {
        setError('Ollama is running, but no models are pulled. Run `ollama pull <model>` first, then refresh.');
        setSaving(false);
        return;
      }
      const preferred =
        models.find((m) => m.default && m.enabled) ||
        models.find((m) => m.enabled) ||
        models[0];
      setSelectedModelId(preferred.id);
      setStep(3);
    } catch (e: any) {
      setError(e?.message || 'Failed to load Ollama models.');
    } finally {
      setSaving(false);
    }
  };

  const finish = async () => {
    const modelId = selectedModelId();
    if (!modelId) {
      setError('Please choose a model.');
      return;
    }
    // Make sure the chosen model is enabled so it shows up in the model picker.
    const model = providerModels().find((m) => m.id === modelId);
    if (model && !model.enabled) {
      try {
        await session.toggleModel(model, true);
      } catch {
        /* non-fatal */
      }
    }
    session.selectModel(modelId); // persists as the default (localStorage)
    await onboarding.refresh(); // clears needs-onboarding so the gate won't bounce
    navigate('/', { replace: true });
  };

  const skip = () => {
    onboarding.dismiss();
    navigate('/', { replace: true });
  };

  const back = () => {
    setError('');
    setStep((s) => (s === 'ready' || s === 3 ? 2 : s === 2 ? 1 : s) as 1 | 2 | 3);
  };

  return (
    <div class="flex h-screen w-full items-center justify-center bg-[color:var(--bg-base)] px-6">
      <div class="w-full max-w-lg rounded-2xl border border-zinc-800 bg-[color:var(--bg-elevated,#18181b)] p-8 shadow-xl">
        {/* Header + step indicator */}
        <div class="mb-6 text-center">
          <h1 class="text-2xl font-semibold text-zinc-100">Welcome to Ogcode</h1>
          <p class="mt-2 text-sm text-zinc-400">
            Connect an AI provider to start coding. Takes about a minute.
          </p>
          <Show when={step() !== 'ready'}>
            <div class="mt-5 flex items-center justify-center gap-2">
              <For each={[1, 2, 3]}>
                {(n) => (
                  <div
                    class={`h-1.5 rounded-full transition-all ${
                      (step() === n || (step() === 'ready' && n === 1)) ? 'w-8 bg-zinc-200' : 'w-4 bg-zinc-700'
                    }`}
                  />
                )}
              </For>
            </div>
          </Show>
        </div>

        {/* Ollama detected — ready to go (zero-config) */}
        <Show when={step() === 'ready'}>
          <div class="space-y-5">
            <div class="flex flex-col items-center gap-3 text-center">
              <div class="flex h-14 w-14 items-center justify-center rounded-2xl bg-sky-500/10 ring-1 ring-sky-400/30">
                <svg class="h-7 w-7 text-sky-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
                </svg>
              </div>
              <div>
                <h2 class="text-lg font-semibold text-zinc-100">Ollama detected — you're all set</h2>
                <p class="mt-1.5 text-sm text-zinc-400">
                  A running Ollama instance was found on your machine. No API key or
                  configuration needed — pick a model and start coding.
                </p>
              </div>
            </div>

            <div class="rounded-xl border border-zinc-800 bg-zinc-900/50 px-4 py-3">
              <div class="flex items-center justify-between text-xs">
                <span class="text-zinc-500">Status</span>
                <span class="flex items-center gap-1.5 font-medium text-emerald-400">
                  <span class="h-1.5 w-1.5 rounded-full bg-emerald-400 animate-pulse" />
                  Running
                </span>
              </div>
              <div class="mt-2 flex items-center justify-between text-xs">
                <span class="text-zinc-500">Endpoint</span>
                <span class="font-mono text-zinc-300">{ollamaStatus()?.baseUrl || OLLAMA_DEFAULT_BASE_URL}</span>
              </div>
            </div>

            <Show when={error()}>
              <p class="rounded-lg border border-red-900/50 bg-red-950/40 px-3 py-2 text-xs text-red-300">
                {error()}
              </p>
            </Show>
          </div>
        </Show>

        {/* Step 1 — pick provider */}
        <Show when={step() === 1}>
          <div class="grid grid-cols-2 gap-3">
            <For each={PROVIDER_DEFS}>
              {(p) => (
                <button
                  type="button"
                  onClick={() => pickProvider(p.id)}
                  class={`flex items-center gap-3 rounded-xl border border-zinc-800 ${p.bg} p-4 text-left ring-1 ${p.ring} transition hover:border-zinc-600`}
                >
                  <span class={`h-2.5 w-2.5 rounded-full ${p.dot}`} />
                  <span class="text-sm font-medium text-zinc-100">{p.label}</span>
                </button>
              )}
            </For>
          </div>
        </Show>

        {/* Step 2 — credentials */}
        <Show when={step() === 2}>
          <div class="space-y-4">
            <div class="flex items-center gap-2 text-sm text-zinc-300">
              <span class={`h-2.5 w-2.5 rounded-full ${def()?.dot}`} />
              <span class="font-medium">{def()?.label}</span>
            </div>

            <Show when={showKey()}>
              <label class="block">
                <span class="mb-1.5 block text-xs font-medium text-zinc-400">
                  API key{keyRequired() ? '' : ' (optional)'}
                </span>
                <input
                  type="password"
                  autocomplete="off"
                  value={apiKey()}
                  onInput={(e) => {
                    setApiKey(e.currentTarget.value);
                    resetTest();
                  }}
                  placeholder={KEY_HINTS[providerId()] || ''}
                  class="w-full rounded-lg border border-zinc-700 bg-zinc-900 px-3 py-2 text-sm text-zinc-100 outline-none focus:border-zinc-500"
                />
              </label>
            </Show>

            <Show when={def()?.hasBaseURL}>
              <label class="block">
                <span class="mb-1.5 block text-xs font-medium text-zinc-400">
                  Base URL{providerId() === 'ollama' ? '' : ' (optional)'}
                </span>
                <input
                  type="text"
                  autocomplete="off"
                  value={baseUrl()}
                  onInput={(e) => {
                    setBaseUrl(e.currentTarget.value);
                    resetTest();
                  }}
                  placeholder="https://..."
                  class="w-full rounded-lg border border-zinc-700 bg-zinc-900 px-3 py-2 text-sm text-zinc-100 outline-none focus:border-zinc-500"
                />
              </label>
            </Show>

            <Show when={providerId() === 'ollama'}>
              <p class="text-xs text-zinc-500">
                {isCloudURL(baseUrl())
                  ? 'Cloud Ollama endpoint detected — enter your API key if the endpoint requires one, then confirm the base URL.'
                  : 'Ollama runs locally — no API key needed. Just confirm the base URL.'}
              </p>
            </Show>

            <div class="flex items-center gap-3">
              <button
                type="button"
                onClick={testKey}
                disabled={testing()}
                class="rounded-lg border border-zinc-700 px-3 py-1.5 text-xs font-medium text-zinc-300 transition hover:border-zinc-500 hover:text-zinc-100 disabled:opacity-50"
              >
                {testing() ? 'Testing…' : 'Test connection'}
              </button>
              <Show when={testResult() === 'ok'}>
                <span class="text-xs text-emerald-400">✓ Connected</span>
              </Show>
              <Show when={testResult() === 'fail'}>
                <span class="truncate text-xs text-red-400" title={testMessage()}>
                  ✗ {testMessage()}
                </span>
              </Show>
            </div>
          </div>
        </Show>

        {/* Step 3 — choose default model */}
        <Show when={step() === 3}>
          <div class="space-y-3">
            <p class="text-sm text-zinc-400">Choose your default model:</p>
            <input
              type="text"
              autocomplete="off"
              value={modelQuery()}
              onInput={(e) => setModelQuery(e.currentTarget.value)}
              placeholder="Search models…"
              class="w-full rounded-lg border border-zinc-700 bg-zinc-900 px-3 py-2 text-sm text-zinc-100 outline-none focus:border-zinc-500"
            />
            <div class="max-h-64 space-y-1.5 overflow-y-auto pr-1">
              <For each={filteredModels()}>
                {(m) => (
                  <button
                    type="button"
                    onClick={() => setSelectedModelId(m.id)}
                    class={`flex w-full items-center justify-between rounded-lg border px-3 py-2.5 text-left transition ${
                      selectedModelId() === m.id
                        ? 'border-zinc-400 bg-zinc-800/60'
                        : 'border-zinc-800 hover:border-zinc-600'
                    }`}
                  >
                    <span class="min-w-0">
                      <span class="block truncate text-sm text-zinc-100">{m.name}</span>
                      <span class="block truncate text-xs text-zinc-500">{m.id}</span>
                    </span>
                    <Show when={selectedModelId() === m.id}>
                      <span class="ml-3 shrink-0 text-xs text-zinc-300">✓</span>
                    </Show>
                  </button>
                )}
              </For>
              <Show when={filteredModels().length === 0}>
                <p class="px-1 py-6 text-center text-xs text-zinc-500">
                  No models match “{modelQuery()}”.
                </p>
              </Show>
            </div>
          </div>
        </Show>

        {/* Error */}
        <Show when={error() && step() !== 'ready'}>
          <p class="mt-4 rounded-lg border border-red-900/50 bg-red-950/40 px-3 py-2 text-xs text-red-300">
            {error()}
          </p>
        </Show>

        {/* Footer actions */}
        <div class="mt-7 flex items-center justify-between">
          <Show
            when={step() !== 1}
            fallback={
              <button
                type="button"
                onClick={skip}
                class="text-xs text-zinc-500 underline underline-offset-4 hover:text-zinc-300"
              >
                Skip for now
              </button>
            }
          >
            <button
              type="button"
              onClick={back}
              class="text-sm text-zinc-400 hover:text-zinc-200"
            >
              ← Back
            </button>
          </Show>

          <Show when={step() === 2}>
            <button
              type="button"
              onClick={saveAndContinue}
              disabled={saving()}
              class="rounded-lg bg-zinc-100 px-4 py-2 text-sm font-medium text-zinc-900 transition hover:bg-white disabled:opacity-50"
            >
              {saving() ? 'Saving…' : 'Continue'}
            </button>
          </Show>

          <Show when={step() === 'ready'}>
            <button
              type="button"
              onClick={goReadyToModels}
              disabled={saving()}
              class="rounded-lg bg-zinc-100 px-4 py-2 text-sm font-medium text-zinc-900 transition hover:bg-white disabled:opacity-50"
            >
              {saving() ? 'Loading models…' : 'Choose a model'}
            </button>
          </Show>

          <Show when={step() === 3}>
            <button
              type="button"
              onClick={finish}
              class="rounded-lg bg-zinc-100 px-4 py-2 text-sm font-medium text-zinc-900 transition hover:bg-white"
            >
              Start coding
            </button>
          </Show>
        </div>
      </div>
    </div>
  );
}