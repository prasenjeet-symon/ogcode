export interface ProviderDef {
  id: string;
  label: string;
  dot: string;
  bg: string;
  ring: string;
  hasBaseURL: boolean;
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
  { id: 'anthropic',  label: 'Anthropic',  dot: 'bg-orange-400',  bg: 'bg-orange-500/10',  ring: 'ring-orange-400/20', hasBaseURL: true,  firstParty: true,  keyOptional: false },
  { id: 'openai',     label: 'OpenAI',     dot: 'bg-emerald-400', bg: 'bg-emerald-500/10', ring: 'ring-emerald-400/20', hasBaseURL: true,  firstParty: true,  keyOptional: false },
  { id: 'openrouter', label: 'OpenRouter', dot: 'bg-violet-400',  bg: 'bg-violet-500/10',  ring: 'ring-violet-400/20', hasBaseURL: false, firstParty: false, keyOptional: false },
  { id: 'ollama',     label: 'Ollama',     dot: 'bg-sky-400',     bg: 'bg-sky-500/10',     ring: 'ring-sky-400/20',    hasBaseURL: true,  firstParty: false, keyOptional: true  },
];

// ---------------------------------------------------------------------------
// Provider guidance
//
// The server speaks exactly four protocols — see NewProviderWithConfig in
// internal/provider/provider.go, which rejects anything that is not
// anthropic / openai / openrouter / ollama. Every other vendor a user might
// name (Gemini, DeepSeek, Groq, Together, …) reaches ogcode *through* one of
// those four, almost always the OpenAI one — or, since vendors grew their own
// tabs, as a saved vendor slot of its own (see the vendor-slot helpers below
// them). The guide covers the four slots plus that fallback for vendor ids,
// and the settings screen explains which to reach for, rather than pretending
// to offer a dozen integrations.
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
    tagline: 'GPT models, direct from OpenAI.',
    useWhen: 'Anything the OpenAI API serves — the protocol virtually every SDK and tool defaults to. OpenAI-compatible vendors get a tab of their own via "Add a vendor" in the strip.',
    tradeoff: 'Pay-as-you-go per token; needs an account with billing enabled. A Base URL here is an endpoint override for OpenAI itself, not a way to reach another vendor.',
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

// Known OpenAI-compatible endpoints. Connected through Settings → Models'
// "Add a vendor", each becomes its own tab keyed by the slug of its
// collection name (vendorIdForPreset below). The entry's collection name and
// key hints also serve the onboarding flow, where a preset still suggests the
// collection custom models are grouped under.
export interface CompatiblePreset {
  collection: string;
  label: string;
  baseURL: string;
  keyHint: string;
  dot: string;
  /** Where to get a key, when the URL is stable enough to be worth linking. */
  keysURL?: string;
}

// Every preset's host is one `collectionFromBaseURL` recognises in
// internal/provider/openai.go. Keeping the two lists in step is what makes a
// preset useful: the server reads the saved base URL, derives the collection
// label from the host, and the model picker then groups those models under the
// vendor's own name instead of a generic "openai". A URL the server does not
// recognise still works — its models simply stay grouped under OpenAI.
//
// The server also recognises a few hosts that are not presets: OpenRouter,
// which has its own slot, and retired vendors, kept so a slot still saved
// against one keeps its name. collectionForBaseURL lists those by hand.
export const COMPATIBLE_PRESETS: CompatiblePreset[] = [
  { collection: 'Gemini',   label: 'Google Gemini', baseURL: 'https://generativelanguage.googleapis.com/v1beta/openai', keyHint: 'AIza…', dot: 'bg-blue-400',   keysURL: 'https://aistudio.google.com/apikey' },
  { collection: 'DeepSeek', label: 'DeepSeek',      baseURL: 'https://api.deepseek.com/v1',        keyHint: 'sk-…',   dot: 'bg-indigo-400' },
  { collection: 'Groq',     label: 'Groq',          baseURL: 'https://api.groq.com/openai/v1',     keyHint: 'gsk_…',  dot: 'bg-amber-400',  keysURL: 'https://console.groq.com/keys' },
  { collection: 'Cerebras', label: 'Cerebras',      baseURL: 'https://api.cerebras.ai/v1',         keyHint: 'csk-…',  dot: 'bg-orange-300' },
  { collection: 'Together', label: 'Together AI',   baseURL: 'https://api.together.xyz/v1',        keyHint: '…',      dot: 'bg-teal-400' },
  { collection: 'Mistral',  label: 'Mistral AI',    baseURL: 'https://api.mistral.ai/v1',          keyHint: '…',      dot: 'bg-rose-400' },
  { collection: 'SambaNova', label: 'SambaNova',    baseURL: 'https://api.sambanova.ai/v1',        keyHint: '…',      dot: 'bg-fuchsia-400' },
  { collection: 'NVIDIA',   label: 'NVIDIA NIM',    baseURL: 'https://integrate.api.nvidia.com/v1', keyHint: 'nvapi-…', dot: 'bg-lime-400' },
  { collection: 'Ollama Cloud', label: 'Ollama Cloud', baseURL: 'https://ollama.com/v1', keyHint: 'sk-…', dot: 'bg-sky-400', keysURL: 'https://ollama.com/settings/keys' },
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
  // GitHub Models was retired on 2026-07-30 and its host no longer resolves,
  // so it is no longer a preset. A slot still saved against it keeps its name
  // here, as it does on the server, instead of passing for plain OpenAI.
  if (u.includes('models.inference.ai.azure.com')) return 'GitHub Models';
  return '';
}

// ---------------------------------------------------------------------------
// Vendor slots
//
// The OpenAI slot once pointed at these presets one at a time; now each preset
// — and any custom OpenAI-compatible endpoint — can be saved as its OWN tab
// (Settings → Models → "Add a vendor"), with its own key and base URL. A
// vendor tab exists only once the settings page has saved a config for its
// id, so the strip carries no always-on empty tabs.
// ---------------------------------------------------------------------------

// The ids the four builtin protocols plus the OGX plan slot own. A vendor id
// must never pass for one of these.
const RESERVED_PROVIDER_IDS = ['anthropic', 'openai', 'openrouter', 'ollama', 'ogx'];

// Lowercase ascii, single dashes, nothing else. Capped, so a pathological
// hostname cannot grow the tab strip forever.
function slugify(name: string): string {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 48)
    .replace(/-+$/g, '');
}

/** True for an id the settings page may treat as a vendor slot — anything
 *  saved outside the five reserved ids, kept to the slug charset. */
export function isVendorSlotId(id: string): boolean {
  return !!id && !RESERVED_PROVIDER_IDS.includes(id) && /^[a-z0-9][a-z0-9-]*$/.test(id);
}

/** The vendor id a preset connects as: its collection name lowercased with
 *  spaces to dashes. Gemini → "gemini", Together AI → "together",
 *  Ollama Cloud → "ollama-cloud". */
export function vendorIdForPreset(preset: CompatiblePreset): string {
  return slugify(preset.collection);
}

/** The hostname of a base URL, or '' when the URL carries none — the caller
 *  uses that to reject half-typed endpoints like "localhost:8000/v1". */
export function vendorHostOf(baseURL: string): string {
  try { return new URL(baseURL).hostname.toLowerCase(); } catch { return ''; }
}

/** The vendor id a custom endpoint connects as: "custom-" plus the slugified
 *  hostname. https://vllm.mycompany.com/v1 → "custom-vllm-mycompany-com".
 *  The prefix is what distinguishes customs from preset slugs and keeps them
 *  clear of the reserved ids even for a bare-IP endpoint. */
export function vendorIdForBaseURL(baseURL: string): string {
  return `custom-${slugify(vendorHostOf(baseURL)) || 'endpoint'}`;
}

const presetForVendorId = (id: string): CompatiblePreset | undefined =>
  COMPATIBLE_PRESETS.find((p) => vendorIdForPreset(p) === id);

/** The tab label a vendor slot reads as: the preset's own label, or — for a
 *  custom endpoint — a capitalized slice of the host with the domain kept for
 *  context ("Vllm (mycompany.com)"). This is what search matches on, so it
 *  must stay a human word, never the raw slug. */
export function vendorLabelFor(id: string, baseURL: string): string {
  const preset = presetForVendorId(id);
  if (preset) return preset.label;
  let host = '';
  let port = '';
  try { host = new URL(baseURL).hostname.toLowerCase(); port = new URL(baseURL).port; } catch { /* malformed */ }
  const parts = host.split('.').filter(Boolean);
  const name = parts[0]?.replace(/[^a-z0-9]/g, '');
  // A bare-IP (or otherwise all-numeric) host has no word in it — slice off the
  // first octet and you get an ambiguous "127 (0.0.1)". Label it host:port so
  // two IP endpoints side by side stay distinct.
  if (!name || /^\d+$/.test(name)) return port ? `${host}:${port}` : (host || id);
  const capitalized = name[0].toUpperCase() + name.slice(1);
  const rest = parts.slice(1).join('.');
  return rest ? `${capitalized} (${rest})` : capitalized;
}

/** The ProviderDef a vendor tab presents — the preset's color where one
 *  applies, neutral otherwise, and always an endpoint the tab's Base URL row
 *  can retarget. Not first-party: vendors authenticate with a key. */
export function vendorDefFor(id: string, baseURL: string): ProviderDef {
  const preset = presetForVendorId(id);
  return {
    id,
    label: vendorLabelFor(id, baseURL),
    dot: preset?.dot ?? 'bg-[color:var(--border-strong)]',
    bg: 'bg-[color:var(--bg-elevated)]',
    ring: 'ring-[color:var(--border-subtle)]',
    hasBaseURL: true,
    keyOptional: false,
    firstParty: false,
  };
}

// Guidance for a vendor tab. PROVIDER_GUIDE is keyed by id, and vendor ids
// are whatever slug the page minted at save time — they resolve through
// providerGuideFor instead.
export const VENDOR_GUIDE: ProviderGuide = {
  tagline: 'An OpenAI-compatible vendor connected as its own slot — its key, endpoint and catalogue are independent of every other tab.',
  useWhen: 'You want this vendor\'s models alongside the others without repointing any existing provider. Its Base URL holds the endpoint it was added from; edit it there if the vendor moves.',
  tradeoff: 'The vendor must be reachable from the ogcode server, and its catalogue is only as fresh as the last save — a wrong key or URL shows up as an empty model list here rather than an error.',
  envKey: '',
  keyHint: 'sk-…',
};

/** Guide for any settings slot id: the builtin entry when one exists, the
 *  vendor fallback for a saved vendor id, nothing otherwise. */
export function providerGuideFor(id: string): ProviderGuide | undefined {
  const built = PROVIDER_GUIDE[id];
  if (built) return built;
  if (!isVendorSlotId(id)) return undefined;
  // A preset vendor carries the preset's key hint and, when one is known,
  // its key URL — the tab then reads exactly like any credential row.
  const preset = presetForVendorId(id);
  if (!preset) return VENDOR_GUIDE;
  const keysURL = preset.keysURL;
  return {
    ...VENDOR_GUIDE,
    keyHint: preset.keyHint,
    ...(keysURL
      ? { keysURL, keysLabel: (() => {
          try { return new URL(keysURL).hostname; } catch { return keysURL; }
        })() }
      : {}),
  };
}

export const CHAT_PROVIDERS = [
  { id: '', label: 'Use default (your main LLM)' },
  ...PROVIDER_DEFS.map((p) => ({ id: p.id, label: p.label })),
];

// The grouping key used in the model picker and settings. Built-in models group
// by their providerId; custom models group by their collection (falling back to
// the providerId when no collection is set, so legacy custom models still group
// correctly).
export function modelGroup(m: { providerId: string; isCustom?: boolean; collection?: string }): string {
  // Prefer an explicit collection label (e.g. an OpenAI-compatible provider's
  // collection) so those models group under their collection instead of a raw
  // provider id. Built-in providers carry no collection and fall back to the
  // provider id.
  if (m.collection) return m.collection;
  return m.providerId;
}
