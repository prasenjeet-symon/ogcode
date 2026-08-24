export interface ProviderDef {
  id: string;
  label: string;
  dot: string;
  bg: string;
  ring: string;
  hasBaseURL: boolean;
  supportsEmbed: boolean;
  // keyOptional providers authenticate some other way (Ollama signs in on the
  // host, or runs unauthenticated locally), so an endpoint alone is a complete
  // configuration — reporting "Not configured" for them is simply wrong.
  keyOptional: boolean;
  // first-party providers have their own native API/SDK and a dedicated
  // onboarding credential step; OpenAI-compatible providers (Gemini, DeepSeek,
  // Groq, …) are added through the OpenAI provider with a custom base URL and
  // grouped by collection instead.
  firstParty: boolean;
}

export const PROVIDER_DEFS: ProviderDef[] = [
  { id: 'anthropic',  label: 'Anthropic',  dot: 'bg-orange-400',  bg: 'bg-orange-500/10',  ring: 'ring-orange-400/20', hasBaseURL: true,  supportsEmbed: false, firstParty: true,  keyOptional: false },
  { id: 'openai',     label: 'OpenAI',     dot: 'bg-emerald-400', bg: 'bg-emerald-500/10', ring: 'ring-emerald-400/20', hasBaseURL: true,  supportsEmbed: true,  firstParty: true,  keyOptional: false },
  { id: 'openrouter', label: 'OpenRouter', dot: 'bg-violet-400',  bg: 'bg-violet-500/10',  ring: 'ring-violet-400/20', hasBaseURL: false, supportsEmbed: true,  firstParty: false, keyOptional: false },
  { id: 'ollama',     label: 'Ollama',     dot: 'bg-sky-400',     bg: 'bg-sky-500/10',     ring: 'ring-sky-400/20',    hasBaseURL: true,  supportsEmbed: true,  firstParty: false, keyOptional: true  },
];

// ---------------------------------------------------------------------------
// Provider guidance
//
// The server speaks exactly four protocols — see NewProviderWithConfig in
// internal/provider/provider.go, which rejects anything that is not
// anthropic / openai / openrouter / ollama. Every other vendor a user might
// name (Gemini, DeepSeek, Groq, Together, …) reaches ogcode *through* one of
// those four, almost always the OpenAI one. The settings screen therefore
// lists four slots and explains which to reach for, rather than pretending to
// offer a dozen integrations.
// ---------------------------------------------------------------------------
export interface ProviderGuide {
  /** One line under the provider name: what this slot actually is. */
  tagline: string;
  /** The decision rule — when this is the right slot to use. */
  useWhen: string;
  /** The trade-off worth knowing before committing to it. */
  tradeoff: string;
  /** Env var read at startup; a key here needs no app-side configuration. */
  envKey: string;
  /** Env var for the endpoint, when the provider has one. */
  envBaseURL?: string;
  /** Where to get credentials. */
  keysURL?: string;
  keysLabel?: string;
  /** Shape of a valid key, shown as the input's placeholder. */
  keyHint: string;
  /** Endpoint used when nothing is configured. */
  defaultBaseURL?: string;
}

export const PROVIDER_GUIDE: Record<string, ProviderGuide> = {
  anthropic: {
    tagline: 'Claude models, direct from Anthropic.',
    useWhen: 'Your default for agentic coding — the strongest tool use and the longest useful context of the four.',
    tradeoff: 'Pay-as-you-go per token; needs an account with billing enabled.',
    envKey: 'ANTHROPIC_API_KEY',
    envBaseURL: 'ANTHROPIC_BASE_URL',
    keysURL: 'https://console.anthropic.com/settings/keys',
    keysLabel: 'console.anthropic.com',
    keyHint: 'sk-ant-…',
    defaultBaseURL: 'https://api.anthropic.com/v1',
  },
  openai: {
    tagline: 'GPT models — and every service that speaks the OpenAI API.',
    useWhen: 'Use for OpenAI itself, or point Base URL at Gemini, DeepSeek, Groq, Together, Mistral, or your own vLLM / LM Studio server.',
    tradeoff: 'This slot holds one endpoint at a time: aiming it at DeepSeek means GPT models stop answering until you aim it back.',
    envKey: 'OPENAI_API_KEY',
    envBaseURL: 'OPENAI_BASE_URL',
    keysURL: 'https://platform.openai.com/api-keys',
    keysLabel: 'platform.openai.com',
    keyHint: 'sk-…',
    defaultBaseURL: 'https://api.openai.com/v1',
  },
  openrouter: {
    tagline: 'One key, models from every vendor.',
    useWhen: 'Trying models you have no account for, or switching vendors mid-session without collecting more keys.',
    tradeoff: 'Requests are relayed through OpenRouter and billed from its credits, so it is a third party in the path.',
    envKey: 'OPENROUTER_API_KEY',
    keysURL: 'https://openrouter.ai/keys',
    keysLabel: 'openrouter.ai/keys',
    keyHint: 'sk-or-…',
  },
  ollama: {
    tagline: 'Models running on your own machine.',
    useWhen: 'Offline work, private code, or zero cost per token — nothing leaves the device.',
    tradeoff: 'Needs Ollama installed with a model pulled, and local hardware sets the ceiling on both speed and model size.',
    envKey: 'OLLAMA_API_KEY',
    envBaseURL: 'OLLAMA_BASE_URL',
    keysURL: 'https://ollama.com/download',
    keysLabel: 'ollama.com',
    keyHint: 'usually blank — local Ollama needs no key',
    defaultBaseURL: 'http://localhost:11434/v1',
  },
};

// Known OpenAI-compatible endpoints that can be used through the OpenAI
// provider by setting a custom base URL. Each entry suggests a collection name
// (used to group custom models in the picker) and the API base URL.
// The OpenAI provider already supports an optional base URL, so these are just
// convenience presets surfaced in the "Add custom model" and onboarding flows.
export interface CompatiblePreset {
  collection: string;
  label: string;
  baseURL: string;
  keyHint: string;
  dot: string;
  /** Where to get a key, when the URL is stable enough to be worth linking. */
  keysURL?: string;
}

// These endpoints are exactly the hosts `collectionFromBaseURL` recognises in
// internal/provider/openai.go. Keeping the two lists in step is what makes a
// preset useful: the server reads the saved base URL, derives the collection
// label from the host, and the model picker then groups those models under the
// vendor's own name instead of a generic "openai". A URL the server does not
// recognise still works — its models simply stay grouped under OpenAI.
export const COMPATIBLE_PRESETS: CompatiblePreset[] = [
  { collection: 'Gemini',   label: 'Google Gemini', baseURL: 'https://generativelanguage.googleapis.com/v1beta/openai', keyHint: 'AIza…', dot: 'bg-blue-400',   keysURL: 'https://aistudio.google.com/apikey' },
  { collection: 'DeepSeek', label: 'DeepSeek',      baseURL: 'https://api.deepseek.com/v1',        keyHint: 'sk-…',   dot: 'bg-indigo-400' },
  { collection: 'Groq',     label: 'Groq',          baseURL: 'https://api.groq.com/openai/v1',     keyHint: 'gsk_…',  dot: 'bg-amber-400',  keysURL: 'https://console.groq.com/keys' },
  { collection: 'Cerebras', label: 'Cerebras',      baseURL: 'https://api.cerebras.ai/v1',         keyHint: 'csk-…',  dot: 'bg-orange-300' },
  { collection: 'Together', label: 'Together AI',   baseURL: 'https://api.together.xyz/v1',        keyHint: '…',      dot: 'bg-teal-400' },
  { collection: 'Mistral',  label: 'Mistral AI',    baseURL: 'https://api.mistral.ai/v1',          keyHint: '…',      dot: 'bg-rose-400' },
  { collection: 'SambaNova', label: 'SambaNova',    baseURL: 'https://api.sambanova.ai/v1',        keyHint: '…',      dot: 'bg-fuchsia-400' },
  { collection: 'NVIDIA',   label: 'NVIDIA NIM',    baseURL: 'https://integrate.api.nvidia.com/v1', keyHint: 'nvapi-…', dot: 'bg-lime-400' },
  { collection: 'GitHub Models', label: 'GitHub Models', baseURL: 'https://models.inference.ai.azure.com', keyHint: 'ghp_…', dot: 'bg-zinc-300' },
];

// Client-side mirror of collectionFromBaseURL (internal/provider/openai.go):
// given the endpoint the OpenAI slot is pointing at, name the vendor behind it.
// Used to label the slot in settings and to prefill the collection when adding
// a custom model, so the UI agrees with how the server will group it.
const PRESET_HOSTS: { host: string; collection: string }[] = COMPATIBLE_PRESETS.map((p) => {
  let host = '';
  try { host = new URL(p.baseURL).host.toLowerCase(); } catch { /* preset is malformed; skip it */ }
  return { host, collection: p.collection };
}).filter((h) => h.host);

export function collectionForBaseURL(baseURL: string): string {
  const u = (baseURL || '').toLowerCase();
  if (!u) return '';
  for (const { host, collection } of PRESET_HOSTS) {
    if (u.includes(host)) return collection;
  }
  if (u.includes('openrouter.ai')) return 'OpenRouter';
  return '';
}

// Inbuilt embedder — runs a sentence model inside the ogcode binary. No API
// key, base URL, or model selection required; always available.
export const LOCAL_EMBED_PROVIDER = {
  id: 'local',
  label: 'Built-in (no setup)',
  dot: 'bg-zinc-300',
  bg: 'bg-zinc-400/10',
  ring: 'ring-zinc-300/20',
  hasBaseURL: false,
  supportsEmbed: true,
};

// Embed providers shown in the settings UI, with the built-in option first so
// agentic memory works out of the box with zero configuration.
export const EMBED_PROVIDERS = [LOCAL_EMBED_PROVIDER, ...PROVIDER_DEFS.filter((p) => p.supportsEmbed)];

export const CHAT_PROVIDERS = [
  { id: '', label: 'Use default (your main LLM)' },
  ...PROVIDER_DEFS.map((p) => ({ id: p.id, label: p.label })),
];

// The grouping key used in the model picker and settings. Built-in models group
// by their providerId; custom models group by their collection (falling back to
// the providerId when no collection is set, so legacy custom models still group
// correctly).
export function modelGroup(m: { providerId: string; isCustom?: boolean; collection?: string }): string {
  // Prefer an explicit collection label (e.g. the free-tier "ogcode" pool, or an
  // OpenAI-compatible provider's collection) so those models group under their
  // collection instead of a raw provider id. Built-in providers carry no
  // collection and fall back to the provider id.
  if (m.collection) return m.collection;
  return m.providerId;
}

// Free-pool providers use ids like "ogcode-groq" / "ogcode-openrouter". Their
// models all group under the shared "ogcode" collection, so this returns the
// underlying provider's display label (e.g. "Groq", "OpenRouter") to tag each
// model with where it actually comes from. Returns null for non-free-pool
// models (whose provider is already the group header).
const SUBPROVIDER_LABELS: Record<string, string> = {
  groq: 'Groq',
  openrouter: 'OpenRouter',
  cerebras: 'Cerebras',
  sambanova: 'SambaNova',
  github_models: 'GitHub Models',
  nvidia: 'NVIDIA',
};

export function subProviderLabel(m: { providerId: string }): string | null {
  if (!m.providerId.startsWith('ogcode-')) return null;
  const key = m.providerId.slice('ogcode-'.length);
  return SUBPROVIDER_LABELS[key] ?? (key.charAt(0).toUpperCase() + key.slice(1));
}
