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
}

export const COMPATIBLE_PRESETS: CompatiblePreset[] = [
  { collection: 'Gemini',   label: 'Google Gemini (OpenAI-compatible)', baseURL: 'https://generativelanguage.googleapis.com/v1beta/openai', keyHint: 'AIza…', dot: 'bg-blue-400' },
  { collection: 'DeepSeek', label: 'DeepSeek', baseURL: 'https://api.deepseek.com/v1', keyHint: 'sk-…', dot: 'bg-indigo-400' },
  { collection: 'Groq',     label: 'Groq',     baseURL: 'https://api.groq.com/openai/v1', keyHint: 'gsk_…', dot: 'bg-amber-400' },
  { collection: 'Together', label: 'Together AI', baseURL: 'https://api.together.xyz/v1', keyHint: '…', dot: 'bg-teal-400' },
  { collection: 'Mistral',  label: 'Mistral AI', baseURL: 'https://api.mistral.ai/v1', keyHint: '…', dot: 'bg-rose-400' },
];

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
