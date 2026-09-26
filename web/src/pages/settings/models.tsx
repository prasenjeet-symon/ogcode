import { For, Index, Show, createSignal, createMemo, createEffect, untrack, onMount, onCleanup, type JSX } from 'solid-js';
import { useSession } from '../../context/session';
import type { ModelInfo, ProviderConfig, OGXStatus } from '../../api/client';
import {
  getProviderConfigs,
  setProviderConfig,
  getOGXStatus,
  startOGXConnect,
  disconnectOGX,
} from '../../api/client';
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
  type ProviderDef,
} from '../../lib/providers';
import { useFeatureFlag } from '../../lib/feature-flags';

// ---------------------------------------------------------------------------
// Models — one tab per provider.
//
// ogcode speaks four protocols and only four: anthropic, openai, openrouter,
// ollama (see NewProviderWithConfig, which rejects everything else). Every
// other vendor — Gemini, DeepSeek, Groq, Together — arrives through the OpenAI
// slot with a different base URL.
//
// Each protocol gets a tab holding its credentials and its slice of the
// catalogue: one provider on screen at a time, the strip under the page title
// switching between them. A live search opens the first provider whose label,
// models or credential rows match, so typing a model name finds it wherever it
// lives; the found tab then stays open.
//
// One tab is not a protocol: OGX is the subscription plan sold by OG Lab.
// Its panel holds an account connection (browser hand-off, token stored
// server-side) rather than credentials — see OGXSection.
// ---------------------------------------------------------------------------

const CHIP_ICON =
  'M8.25 3v1.5M4.5 8.25H3m18 0h-1.5M4.5 12H3m18 0h-1.5m-15 3.75H3m18 0h-1.5M8.25 19.5V21M12 3v1.5m0 15V21m3.75-18v1.5m0 15V21m-9-1.5h10.5a2.25 2.25 0 002.25-2.25V6.75a2.25 2.25 0 00-2.25-2.25H6.75A2.25 2.25 0 004.5 6.75v10.5a2.25 2.25 0 002.25 2.25zm.75-12h9v9h-9v-9z';

/** The OG Lab plan tab — an account connection, not a provider protocol. */
const OGX_SLOT = 'ogx';

/** The OG Lab dashboard. Plan state, billing and token usage live there. */
const OGX_WEB_URL = 'https://oglab.ogcode.xyz';

/**
 * PostHog flag gating the OGX tab while the plan feature is unreleased. The
 * flag has to exist in the ogcode PostHog project for the tab to show at all —
 * a missing flag, like an unreachable PostHog, reads as off.
 */
const OGX_FLAG = 'ogx-tab';

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
  const [active, setActive] = createSignal('');
  const ogxTab = useFeatureFlag(OGX_FLAG);

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

  // The page always shows the four real slots, plus any unexpected provider id
  // that turns up in the catalogue — a model the user can see in the picker must
  // be reachable here, whatever its provider.
  const slots = createMemo<Slot[]>(
    () => {
      const base: Slot[] = PROVIDER_DEFS.map((def) => ({ id: def.id, label: def.label, def }));
      // The plan tab leads rather than rides after the four protocols: it is
      // the one tab that is not a protocol, and the subscription is what a
      // fresh install with no keys reaches for first. readOnly: it has no
      // credential rows — its panel manages the OG Lab account link instead.
      //
      // Held behind a PostHog flag while the plan feature is unreleased, and
      // off is the fail-safe: an install that cannot reach PostHog keeps the
      // four protocols alone. The id counts as known either way — an install
      // that already has plan models must not surface them as a generic
      // provider tab once the flag is off.
      if (ogxTab()) base.unshift({ id: OGX_SLOT, label: 'OGX', readOnly: true });
      const known = new Set([...base.map((s) => s.id), OGX_SLOT]);
      const extra = new Set<string>();
      for (const m of session.models()) {
        const slot = m.providerId;
        if (!known.has(slot)) extra.add(slot);
      }
      for (const id of [...extra].sort()) base.push({ id, label: id, readOnly: true });
      return base;
    },
    [] as Slot[],
    // Toggling a model rebuilds this list, but the set of provider slots almost
    // never changes as a result. Returning the previous array when the slot ids
    // still match keeps the tab strip from re-rendering and the open section
    // from re-mounting on each toggle — the re-mount is what reset the scroll
    // position and flashed the whole page.
    { equals: (a, b) => a.length === b.length && a.every((s, i) => s.id === b[i].id) },
  );

  // The first tab is open until the user picks one. Held as an id, not an
  // index: a late catalogue fetch can surface an extra provider slot, and the
  // tab you were reading should stay open if it does.
  createEffect(() => {
    if (slots().length > 0 && !slots().some((s) => s.id === active())) {
      setActive(slots()[0].id);
    }
  });

  const modelsFor = (slotId: string) =>
    session
      .models()
      .filter((m) => m.providerId === slotId)
      // Sorted by name only — never by enabled state, or a row would jump out
      // from under the cursor the moment it was toggled.
      .sort((a, b) => a.name.localeCompare(b.name));

  // Does the query point at this slot? The credential vocabulary only counts
  // for a slot that has credential rows: "key" or "endpoint" would otherwise
  // match every slot, and the search opens the first match — which is now the
  // OGX plan tab, holding no keys at all.
  const slotSearchMatches = (slot: Slot, q: string) =>
    matches(q, slot.label, slot.id) ||
    modelsFor(slot.id).some((m) => modelMatches(m, q)) ||
    (!slot.readOnly && CREDENTIAL_TERMS.some((t) => matches(q, t)));

  /** True when a query is live and nothing anywhere on the page matched it. */
  const nothingMatched = createMemo(() => {
    const q = shell.query().trim();
    if (!q) return false;
    return !slots().some((slot) => slotSearchMatches(slot, q));
  });

  // A search is a query across every provider, so it has to reach the tabs
  // that are closed: typing opens the first provider whose label, models or
  // credential rows match — otherwise a hit on a hidden tab would read as
  // "nothing happened". The found tab then stays open, including after the
  // query is cleared — you searched for it, so it is where you are. The
  // lookups are untracked so this effect does not re-run when the slot list
  // itself changes.
  createEffect(() => {
    const q = shell.query().trim();
    if (!q) return;
    const hit = untrack(() => slots().find((slot) => slotSearchMatches(slot, q)));
    if (hit) untrack(() => setActive(hit.id));
  });

  const current = () => slots().find((s) => s.id === active()) ?? slots()[0];

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
      {/* One provider at a time, chosen by the strip — the full sheet for the
          open tab, the others one click away instead of stacked into a scroll
          the length of the whole catalogue. The strip sticks under the page
          header, so switching providers never costs a scroll back to the top. */}
      <div
        class="sticky top-0 z-10 -mx-3 sm:-mx-6 px-3 sm:px-6 pt-1 pb-3
               bg-[color:var(--bg-base)]"
      >
        <div
          role="tablist"
          aria-label="Providers"
          class="flex gap-1 overflow-x-auto hide-scrollbar border-b border-[color:var(--border-subtle)]"
        >
          <For each={slots()}>
            {(slot) => {
              const on = () => slot.id === active();
              return (
                <button
                  type="button"
                  role="tab"
                  aria-selected={on()}
                  onClick={() => setActive(slot.id)}
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
                  {slot.label}
                  <Show when={modelsFor(slot.id).some((m) => m.enabled)}>
                    <span class="text-micro tabular-nums text-[color:var(--text-muted)]">
                      {modelsFor(slot.id).filter((m) => m.enabled).length}
                    </span>
                  </Show>
                  <Show when={on()}>
                    {/* The accent underline rides the bottom border, not the
                        tab: the tab itself stays a quiet shape, and the marker
                        reads as part of the rule it interrupts. */}
                    <span class="absolute left-2 right-2 -bottom-px h-0.5 rounded-full bg-[color:var(--accent)]" />
                  </Show>
                </button>
              );
            }}
          </For>
        </div>
      </div>

      <Show when={current()}>
        {(slot) => (
          <Show
            when={slot().id !== OGX_SLOT}
            fallback={
              <OGXSection
                slot={slot()}
                models={modelsFor(slot().id)}
                onToggle={(m) => session.toggleModel(m, !m.enabled)}
              />
            }
          >
          <ProviderSection
            slot={slot()}
            models={modelsFor(slot().id)}
            config={configs()[slot().id]}
            loadingConfig={loadingConfigs()}
            onSaved={(c) => setConfigs({ ...configs(), [slot().id]: c })}
            onApplied={() => session.reloadModels()}
            onToggle={(m) => session.toggleModel(m, !m.enabled)}
            onRemove={async (m) => {
              if (!confirm(`Remove "${m.name}"? This deletes the custom model.`)) return;
              await session.removeCustomModel(m.id, m.providerId);
            }}
            onAdd={(id, name, collection) =>
              session.addCustomModel(id, slot().id, name, collection || undefined)
            }
          />
          </Show>
        )}
      </Show>
    </Show>
  );
}

// ---------------------------------------------------------------------------
// OGX — the subscription plan by OG Lab.
//
// The panel manages one thing: whether this install is linked to an OG Lab
// account. Connect opens the web side in a new tab (sign-up, sign-in and
// payment all happen there), the browser is redirected back to the server
// with a token, and the panel — polling status while the tab is away — flips
// to active the moment the token lands. The token stays server-side.
//
// Once linked, the plan's models are a provider like any other, so the panel
// also renders their list. Two things there behave differently from a
// credential tab: the catalogue comes and goes with the account link (so both
// transitions re-fetch it rather than waiting for a restart), and nothing in it
// is the user's to add or delete — the plan decides. The slot is readOnly,
// which hides the add row; the remove path cannot fire because none of these
// models is custom.
// ---------------------------------------------------------------------------
function OGXSection(props: {
  slot: Slot;
  models: ModelInfo[];
  onToggle: (m: ModelInfo) => void | Promise<void>;
}) {
  const session = useSession();
  const shell = useShell();
  const [status, setStatus] = createSignal<OGXStatus | null>(null);
  const [connecting, setConnecting] = createSignal(false);
  const [error, setError] = createSignal('');
  let pollTimer: number | undefined;

  const refresh = async () => {
    try {
      setStatus(await getOGXStatus());
    } catch {
      /* leave the last known state on screen */
    }
  };
  onMount(refresh);

  const stopPolling = () => {
    if (pollTimer !== undefined) {
      clearInterval(pollTimer);
      pollTimer = undefined;
    }
    setConnecting(false);
  };
  onCleanup(stopPolling);

  // Mirrors session.OGXAccount.HasPlan on the server: a plan value of "none"
  // (or nothing at all) means the account is linked but holds no plan, so the
  // gateway grants no models. Both sides must agree, or the chip would read
  // "Active" over an empty list.
  const planLabel = () => {
    const p = (status()?.plan || '').trim();
    return p && p.toLowerCase() !== 'none' ? p : '';
  };

  const connect = async () => {
    setError('');
    try {
      const { url } = await startOGXConnect();
      window.open(url, '_blank', 'noopener');
      setConnecting(true);
      // Poll while the user is away in the browser. The server-side connect
      // state lives 15 minutes; polling longer would wait on a flow that can
      // no longer complete.
      const startedAt = Date.now();
      pollTimer = window.setInterval(async () => {
        const st = await getOGXStatus().catch(() => null);
        if (st?.connected) {
          setStatus(st);
          stopPolling();
          // The link just landed, so the plan's models exist server-side now.
          // Fetch them without holding the panel open on a spinner: a failure
          // leaves the tab showing the connection and an empty list, which the
          // list itself explains.
          session.reloadModels().catch(() => {});
        } else if (Date.now() - startedAt > 15 * 60_000) {
          stopPolling();
        }
      }, 2000);
    } catch {
      setError('Could not start the connect flow — is the server reachable?');
    }
  };

  const disconnect = async () => {
    if (!confirm('Disconnect OGX? Your plan stays on your OG Lab account — this only unlinks ogcode.')) return;
    try {
      setStatus(await disconnectOGX());
      // Mirror the connect path: the provider is gone, so the catalogue has to
      // follow it out of the page.
      session.reloadModels().catch(() => {});
    } catch {
      setError('Could not disconnect.');
    }
  };

  const connectedSince = () => {
    const at = status()?.connectedAt;
    return at ? new Date(at).toLocaleDateString() : '';
  };

  // Same two-way split ProviderSection uses: the page-wide search narrows what
  // is listed, while the list itself still knows the full catalogue (for its
  // filter box and bulk chips). The row hides only when the search excludes
  // the provider and everything it holds.
  const visibleModels = () => props.models.filter((m) => modelMatches(m, shell.query()));
  const modelsHidden = () =>
    !matches(shell.query(), 'OGX', OGX_SLOT) &&
    !matches(shell.query(), 'models', 'catalogue', 'available') &&
    visibleModels().length === 0;

  return (
    <div class="page-enter">
      <Group
        id={OGX_SLOT}
        title="OGX"
        icon={CHIP_ICON}
        description="The ogcode plan, by OG Lab."
        action={
          <Show when={status()?.connected} fallback={<StatusChip tone="muted">Not connected</StatusChip>}>
            <Show when={planLabel()} fallback={<StatusChip tone="warn">No plan</StatusChip>}>
              <StatusChip tone="ok">Active · {planLabel()}</StatusChip>
            </Show>
          </Show>
        }
      >
        <Show when={error()}>
          <Banner tone="danger">{error()}</Banner>
        </Show>

        {/* Not linked yet. One short, centred invitation rather than a lone
            settings row: an unconfigured provider tab is mostly whitespace, and
            a single label/button pair in the middle of it reads as unfinished.
            The paragraph that used to sit here explained the mechanism
            (sign-up, payment, where the token lives) instead of offering the
            action, so it is gone — the button is the whole story.

            data-setting matters: the shell hides any section whose rows all
            filtered out (the :has() rule in index.css keys on that attribute),
            and this branch has no <Row> left to carry it. */}
        <Show
          when={status()?.connected}
          fallback={
            <div data-setting class="px-4 py-8 text-center">
              <span
                class="mx-auto mb-3 w-10 h-10 rounded-full flex items-center justify-center"
                style={{
                  background: 'color-mix(in srgb, var(--accent) 12%, transparent)',
                  color: 'var(--accent)',
                }}
              >
                <svg class="w-[19px] h-[19px]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.7">
                  <path
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    d="M13.19 8.688a4.5 4.5 0 011.242 7.244l-4.5 4.5a4.5 4.5 0 01-6.364-6.364l1.757-1.757M6.81 15.312a4.5 4.5 0 011.242-7.244l4.5-4.5a4.5 4.5 0 016.364 6.364l-1.757 1.757"
                  />
                </svg>
              </span>
              <p class="text-ui font-medium text-[color:var(--text-primary)]">Connect your ogcode plan</p>
              <Show
                when={!connecting()}
                fallback={
                  <p class="mt-2 flex items-center justify-center gap-2 text-meta text-[color:var(--text-secondary)]">
                    <Spinner class="w-3.5 h-3.5" />
                    Waiting for the browser…
                    <Button variant="text" onClick={stopPolling}>
                      Cancel
                    </Button>
                  </p>
                }
              >
                <p class="mx-auto mt-1.5 max-w-[24rem] text-meta leading-[1.6] text-[color:var(--text-tertiary)]">
                  Opens the OG Lab sign-in in a new tab.
                </p>
                <div class="mt-4">
                  <Button onClick={connect} disabled={status() === null}>
                    Connect OGX
                  </Button>
                </div>
              </Show>
            </div>
          }
        >
          <Show when={!planLabel()}>
            <Banner tone="warn">
              This account is linked but holds no plan, so the gateway grants no models. Choose a
              plan on the OG Lab side, then disconnect and reconnect here to pick it up.
            </Banner>
          </Show>
          <Row label="Account" helper="The OG Lab account this install is linked to.">
            <span class="flex items-center gap-2 text-meta">
              <Mono>{status()?.email || 'connected'}</Mono>
              <Show when={connectedSince()}>
                <span class="text-[color:var(--text-muted)]">since {connectedSince()}</span>
              </Show>
            </span>
          </Row>
          <Row label="Usage" helper="Token spend and plan details live on the OG Lab side.">
            <Button variant="outlined" href={OGX_WEB_URL}>
              Check usage
            </Button>
          </Row>
          <Row
            label="Disconnect"
            helper="Unlinks ogcode from the account. The plan itself is managed on the web side."
          >
            <Button variant="outlined" onClick={disconnect}>
              Disconnect
            </Button>
          </Row>
          <Row
            label="Models"
            helper="Every model your plan grants. Managed on the OG Lab side — none of them is removable here."
            stacked
            hidden={modelsHidden()}
          >
            <ModelList
              all={props.models}
              visible={visibleModels()}
              slot={props.slot}
              configured={true}
              filtering={!!shell.query().trim()}
              onToggle={props.onToggle}
              // Never called: the slot is readOnly, so the add row is hidden,
              // and no plan model is custom, so no row offers a remove button.
              onRemove={() => {}}
              onAdd={async () => {}}
              suggestedCollection=""
            />
          </Row>
        </Show>
      </Group>
    </div>
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
    <div class="page-enter">
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
          modelCount={() => props.models.length}
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
    </div>
  );
}

// ---------- Credentials ------------------------------------------------------

function Credentials(props: {
  def: ProviderDef;
  config: ProviderConfig | undefined;
  onSaved: (c: ProviderConfig) => void;
  onApplied: () => Promise<void>;
  // This provider's catalogue size after a refresh. Read AFTER onApplied settles
  // to tell a real credential problem (0 models) from a transient refresh blip.
  modelCount: () => number;
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
    // The refresh promise rejects only when the request fails to round-trip
    // (server unreachable / transient) — NOT when the list is empty: the server's
    // /models/refresh returns 200 with a fallback list even when a provider's own
    // fetch fails. So "did it throw" and "did this provider get models" are two
    // independent signals, and an honest result needs both:
    //   resolved + models  -> fetched fine
    //   resolved + none    -> the key or base URL is the problem
    //   threw    + models  -> a transient blip; the stored catalogue is already fine
    //   threw    + none    -> could not reach the server to refresh
    // The old code blamed the key on ANY throw — crying wolf on a blip while a full
    // catalogue was loaded (the "223 on / could not be fetched" contradiction), and
    // staying silent on the real 0-model case, which resolves without throwing.
    setRefreshing(true);
    let refreshFailed = false;
    try {
      await props.onApplied();
    } catch {
      refreshFailed = true;
    } finally {
      setRefreshing(false);
    }
    const gotModels = props.modelCount() > 0;
    if (gotModels) {
      // Key stored and models present. If the request itself blipped we simply
      // didn't refresh this time — not a credential problem, so don't alarm.
      setSaved(true);
      setTimeout(() => setSaved(false), 4000);
    } else if (refreshFailed) {
      setError('Saved, but ogcode could not reach the server to refresh the model list. Check that the ogcode server is running, then Save again.');
    } else {
      setError('Saved, but no models came back for this provider. Double-check the API key or base URL, then Save again.');
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
              : props.slot.id === OGX_SLOT
              ? 'Connected, but your plan grants no models. Pick a plan on the OG Lab side, then disconnect and reconnect to pick it up.'
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
