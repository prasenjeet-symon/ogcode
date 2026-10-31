import { For, Index, Show, createSignal, createMemo, createEffect, untrack, onMount, onCleanup, type JSX } from 'solid-js';
import { useSession } from '../../context/session';
import type { ModelInfo, ProviderConfig, OGXStatus, CustomModelMeta } from '../../api/client';
import {
  deleteProviderConfig,
  getProviderConfigs,
  setProviderConfig,
  getOGXStatus,
  refreshOGX,
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
import ModelMetaDialog, { EMPTY_META, metaFrom, type MetaForm } from './model-meta';
import {
  PROVIDER_DEFS,
  COMPATIBLE_PRESETS,
  collectionForBaseURL,
  isVendorSlotId,
  providerGuideFor,
  vendorDefFor,
  vendorHostOf,
  vendorIdForBaseURL,
  vendorIdForPreset,
  vendorLabelFor,
  type ProviderDef,
  type CompatiblePreset,
} from '../../lib/providers';
import { useFeatureFlag } from '../../lib/feature-flags';

// ---------------------------------------------------------------------------
// Models — one tab per provider.
//
// ogcode speaks four protocols and only four: anthropic, openai, openrouter,
// ollama (see NewProviderWithConfig, which rejects everything else). Other
// OpenAI-compatible vendors — Gemini, DeepSeek, Groq, a company gateway — are
// each saved as their OWN tab once the user points at one: their own key,
// base URL and catalogue, stored as extra provider config rows server-side.
// "Add a vendor" in the strip is how one is created; a vendor tab exists only
// while such a row does.
//
// Each protocol gets a tab holding its credentials and its slice of the
// catalogue: one provider on screen at a time, the strip under the page title
// switching between them. A live search opens the first provider whose label,
// models or credential rows match, so typing a model name finds it wherever it
// lives; the found tab then stays open.
//
// One tab is not a protocol: OGX is the subscription plan sold by OGLAB.
// Its panel holds an account connection (browser hand-off, token stored
// server-side) rather than credentials — see OGXSection.
// ---------------------------------------------------------------------------

const CHIP_ICON =
  'M8.25 3v1.5M4.5 8.25H3m18 0h-1.5M4.5 12H3m18 0h-1.5m-15 3.75H3m18 0h-1.5M8.25 19.5V21M12 3v1.5m0 15V21m3.75-18v1.5m0 15V21m-9-1.5h10.5a2.25 2.25 0 002.25-2.25V6.75a2.25 2.25 0 00-2.25-2.25H6.75A2.25 2.25 0 004.5 6.75v10.5a2.25 2.25 0 002.25 2.25zm.75-12h9v9h-9v-9z';

/** The OGLAB plan tab — an account connection, not a provider protocol. */
const OGX_SLOT = 'ogx';

/** The OGLAB dashboard. Plan state, billing and token usage live there. */
const OGX_WEB_URL = 'https://oglab.ogcode.in';

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

  // Vendor slots: rows the frontend has saved outside the four builtin
  // protocols. The memo reads configs alone, never session.models() — a tab
  // must not appear or disappear because a model was toggled, so the strip
  // stays still through ordinary editing. Sorting keeps it stable however the
  // configs row arrives.
  const vendorSlots = createMemo<Slot[]>(() => {
    const out: Slot[] = [];
    for (const id of Object.keys(configs())) {
      if (!isVendorSlotId(id)) continue;
      const cfg = configs()[id];
      const baseURL = cfg.effectiveBaseUrl || cfg.baseUrl;
      out.push({ id, label: vendorLabelFor(id, baseURL), def: vendorDefFor(id, baseURL) });
    }
    return out.sort((a, b) => a.label.localeCompare(b.label));
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
      // credential rows — its panel manages the OGLAB account link instead.
      //
      // Held behind a PostHog flag while the plan feature is unreleased, and
      // off is the fail-safe: an install that cannot reach PostHog keeps the
      // four protocols alone. The id counts as known either way — an install
      // that already has plan models must not surface them as a generic
      // provider tab once the flag is off.
      if (ogxTab()) base.unshift({ id: OGX_SLOT, label: 'OGX', readOnly: true });
      // Vendor tabs ride after the protocols, ahead of the catalogue
      // catch-all: they are first-class editors, not the read-only extras
      // below. Each id counts as known here so the extras loop does not
      // add a second, read-only copy of the same vendor.
      const known = new Set([...base.map((s) => s.id), OGX_SLOT, ...vendorSlots().map((s) => s.id)]);
      const extra = new Set<string>();
      for (const m of session.models()) {
        const slot = m.providerId;
        if (!known.has(slot)) extra.add(slot);
      }
      for (const id of [...extra].sort()) base.push({ id, label: id, readOnly: true });
      return [...base, ...vendorSlots()];
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

  /** True when a query is live and nothing anywhere on the page matched it.
   *  Searching "vendor" or the old "point at" is a search for the
   *  affordance, not for a slot — the add-a-vendor lane answers it, and the
   *  empty state below would hide the very row that resolves the query. */
  const nothingMatched = createMemo(() => {
    const q = shell.query().trim();
    if (!q) return false;
    if (ADD_VENDOR_TERMS.some((t) => matches(q, t))) return false;
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

  const addVendorMatches = () => {
    const q = shell.query().trim();
    return !!q && ADD_VENDOR_TERMS.some((t) => matches(q, t));
  };
  const showAddRow = () => showAddVendor() || addVendorMatches();

  const [showAddVendor, setShowAddVendor] = createSignal(false);
  const [addVendorBusy, setAddVendorBusy] = createSignal(false);
  const [addError, setAddError] = createSignal('');
  const [customOpen, setCustomOpen] = createSignal(false);
  const [customURL, setCustomURL] = createSignal('');
  const [customURLError, setCustomURLError] = createSignal('');

  // A vendor joins by SAVING, not by filling a form first: the POST writes a
  // row with an empty key under the vendor's id, and the tab that then opens
  // carries the standard credential form — its key is filled in there. The
  // server reloads its providers in place, so the same refresh the Save path
  // uses is all it takes for the vendor's models to arrive.
  const addVendor = async (id: string, baseURL: string) => {
    // Already pointing at it: that is simply the open-tab click.
    if (configs()[id]) {
      setShowAddVendor(false);
      setActive(id);
      return;
    }
    setAddVendorBusy(true);
    setAddError('');
    try {
      const cfg = await setProviderConfig(id, { apiKey: '', baseUrl: baseURL });
      setConfigs({ ...configs(), [id]: cfg });
      setShowAddVendor(false);
      setCustomOpen(false);
      setCustomURL('');
      setActive(id);
      await session.reloadModels();
    } catch {
      setAddError('Could not add this vendor. Is the ogcode server still running?');
    } finally {
      setAddVendorBusy(false);
    }
  };

  const addCustomVendor = async () => {
    const url = customURL().trim();
    // No scheme means no hostname, which means no slug and no label — the
    // form has to demand the full URL rather than guess at a port.
    if (!vendorHostOf(url)) {
      setCustomURLError('Enter a full URL, including http:// or https://.');
      return;
    }
    setCustomURLError('');
    await addVendor(vendorIdForBaseURL(url), url);
  };

  // Removing a vendor is one DELETE: the config row goes, the slot memo loses
  // it, and the fallback effect above lands on the first remaining tab when
  // the removed one was open.
  const removeVendorById = async (id: string) => {
    await deleteProviderConfig(id);
    const next = { ...configs() };
    delete next[id];
    setConfigs(next);
    // The reload drops the vendor's models from the picker the same way the
    // save path drops a cleared key's.
    await session.reloadModels();
  };

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
          {/* The plus is how the strip grows. The tabs before it are data; this
              one is an affordance, so it carries no dot and no count. */}
          <button
            type="button"
            title="Add a vendor"
            aria-label="Add a vendor"
            onClick={() => {
              setShowAddVendor(!showAddVendor());
              setCustomOpen(false);
              setCustomURLError('');
            }}
            class={`relative flex items-center h-8 px-2.5 rounded-md text-ui whitespace-nowrap shrink-0
                    transition-colors duration-150
              ${
                showAddVendor()
                  ? 'text-[color:var(--text-primary)] font-medium'
                  : 'text-[color:var(--text-muted)] hover:text-[color:var(--text-primary)]'
              }`}
          >
            <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M12 5v14M5 12h14" />
            </svg>
          </button>
        </div>
      </div>

      {/* The way a vendor joins. One scrolling chip row — the lane the old
          "Point at" row used — kept under the strip, and shown only on the
          plus click or when a search names "vendor" or "point at". A preset
          that already has a tab just opens it; nothing duplicates. */}
      <Show when={showAddRow()}>
        <div class="mt-2">
          <div class="flex flex-nowrap gap-2 overflow-x-auto hide-scrollbar">
            <For each={COMPATIBLE_PRESETS}>
              {(preset) => (
                <Chip
                  title={`${preset.baseURL} — its own tab, with its own key (${preset.keyHint})`}
                  onClick={() => addVendor(vendorIdForPreset(preset), preset.baseURL)}
                >
                  {preset.label}
                </Chip>
              )}
            </For>
            <Chip
              active={customOpen()}
              title="Any OpenAI-compatible endpoint — vLLM, LM Studio, LiteLLM, a company gateway"
              onClick={() => setCustomOpen(!customOpen())}
            >
              Custom…
            </Chip>
          </div>
          <Show when={customOpen()}>
            <div class="mt-2 flex flex-wrap items-center gap-2">
              <div class="flex-1 min-w-[16rem]">
                <TextField
                  mono
                  value={customURL()}
                  onInput={(v) => {
                    setCustomURL(v);
                    setCustomURLError('');
                  }}
                  onEnter={addCustomVendor}
                  disabled={addVendorBusy()}
                  ariaLabel="Custom vendor base URL"
                  placeholder="Full URL, e.g. http://localhost:8000/v1 or https://vllm.mycompany.com/v1"
                />
              </div>
              <Button onClick={addCustomVendor} disabled={addVendorBusy()}>
                {addVendorBusy() ? 'Adding…' : 'Add'}
              </Button>
              <Button
                variant="text"
                onClick={() => {
                  setCustomOpen(false);
                  setCustomURL('');
                  setCustomURLError('');
                }}
              >
                Cancel
              </Button>
            </div>
          </Show>
          <Show when={customURLError() || addError()}>
            <p class="mt-1.5 text-micro" style={{ color: 'var(--danger)' }}>
              {customURLError() || addError()}
            </p>
          </Show>
        </div>
      </Show>

      <Show when={current()}>
        {(slot) => (
          <Show
            when={slot().id !== OGX_SLOT}
            fallback={
              <OGXSection
                slot={slot()}
                models={modelsFor(slot().id)}
                onToggle={(m) => session.toggleModel(m, !m.enabled)}
                onEditMeta={(m, meta) => session.updateModelMeta(m, meta)}
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
            onRemoveVendor={isVendorSlotId(slot().id) ? () => removeVendorById(slot().id) : undefined}
            onToggle={(m) => session.toggleModel(m, !m.enabled)}
            onEditMeta={(m, meta) => session.updateModelMeta(m, meta)}
            onRemove={async (m) => {
              if (!confirm(`Remove "${m.name}"? This deletes the custom model.`)) return;
              await session.removeCustomModel(m.id, m.providerId);
            }}
            onAdd={(id, name, collection, meta) =>
              session.addCustomModel(id, slot().id, name, collection || undefined, meta)
            }
          />
          </Show>
        )}
      </Show>

    </Show>
  );
}

// ---------------------------------------------------------------------------
// OGX — the subscription plan by OGLAB.
//
// The panel manages one thing: whether this install is linked to an OGLAB
// account, and what that account's plan is right now. Connect opens the web
// side in a new tab (sign-up, sign-in and payment all happen there), the
// browser is redirected back to the server with a token, and the panel —
// polling status while the tab is away — flips to the account the moment the
// token lands. The token stays server-side.
//
// The plan is re-read from the gateway whenever the panel opens and whenever
// the window comes back into focus, so a plan bought, lapsed or cancelled on
// the web side shows up here on its own — there is no disconnect-and-reconnect
// dance. The header says where the account stands and holds the one action that
// changes it; the rows below are the account, the plan's models and the way
// out.
//
// Once linked, the plan's models are a provider like any other. Two things
// behave differently from a credential tab: the catalogue comes and goes with
// the plan (so every change re-fetches it rather than waiting for a restart),
// and nothing in it is the user's to add or delete — the plan decides. The slot
// is readOnly, which hides the add row; the remove path cannot fire because
// none of these models is custom.
// ---------------------------------------------------------------------------

const EXTERNAL_ICON = 'M7 17L17 7M9 7h8v8';
const REFRESH_ICON =
  'M16.023 9.348h4.992v-.001M2.985 19.644v-4.992m0 0h4.992m-4.993 0l3.181 3.183a8.25 8.25 0 0013.803-3.7M4.031 9.865a8.25 8.25 0 0113.803-3.7l3.181 3.182m0-4.991v4.99';

/** Where the linked account stands, as the panel draws it. */
type OGXState = 'loading' | 'disconnected' | 'active' | 'noplan' | 'revoked';

/** A plan value the gateway would serve. Mirrors session.OGXAccount.HasPlan on
 *  the server: "none", or nothing at all, is a link without a plan. */
function hasPlan(st: OGXStatus | null): boolean {
  const p = (st?.plan || '').trim().toLowerCase();
  return p !== '' && p !== 'none';
}

/** Re-reading the plan on focus is throttled to this, so flicking between
 *  windows does not ask the gateway each time. */
const RECHECK_MS = 30_000;

function OGXSection(props: {
  slot: Slot;
  models: ModelInfo[];
  onToggle: (m: ModelInfo) => void | Promise<void>;
  onEditMeta: (m: ModelInfo, meta: CustomModelMeta) => Promise<void>;
}) {
  const session = useSession();
  const shell = useShell();
  const [status, setStatus] = createSignal<OGXStatus | null>(null);
  const [connecting, setConnecting] = createSignal(false);
  const [checking, setChecking] = createSignal(false);
  const [revoked, setRevoked] = createSignal(false);
  // What a check the user asked for came to, said once beside the header:
  // "up to date", or why it could not tell.
  const [note, setNote] = createSignal<{ tone: 'ok' | 'danger'; text: string } | null>(null);
  const [error, setError] = createSignal('');
  let pollTimer: number | undefined;
  let noteTimer: number | undefined;
  let lastCheck = 0;

  const state = (): OGXState => {
    const st = status();
    if (!st) return 'loading';
    if (!st.connected) return 'disconnected';
    if (revoked()) return 'revoked';
    return hasPlan(st) ? 'active' : 'noplan';
  };

  const say = (next: { tone: 'ok' | 'danger'; text: string } | null) => {
    clearTimeout(noteTimer);
    setNote(next);
    if (next) noteTimer = window.setTimeout(() => setNote(null), 5000);
  };

  // check asks the gateway what the plan is now. A plan that came or went
  // changed the provider registry server-side, so the catalogue is re-fetched
  // to match. Background checks (opening the tab, coming back to the window)
  // stay silent unless they change something; a check the user asked for
  // always says how it went.
  const check = async (manual: boolean) => {
    if (checking() || !status()?.connected) return;
    setChecking(true);
    lastCheck = Date.now();
    const before = hasPlan(status());
    try {
      const st = await refreshOGX();
      setStatus(st);
      setRevoked(st.check === 'revoked');
      const changed = st.check === 'ok' && hasPlan(st) !== before;
      if (changed) session.reloadModels().catch(() => {});
      if (changed && hasPlan(st)) {
        say({ tone: 'ok', text: 'Your plan is active. Its models are in the picker now.' });
      } else if (manual) {
        say(
          st.check === 'unreachable'
            ? { tone: 'danger', text: "Couldn't reach OGLAB. Try again in a moment." }
            : st.check === 'ok'
              ? { tone: 'ok', text: 'Up to date.' }
              : null,
        );
      }
    } catch {
      if (manual) say({ tone: 'danger', text: "Couldn't reach the ogcode server." });
    } finally {
      setChecking(false);
    }
  };

  onMount(async () => {
    try {
      setStatus(await getOGXStatus());
    } catch {
      setError('Could not read the OGX connection — is the ogcode server running?');
      return;
    }
    check(false);
  });

  // Coming back to the window is when a plan bought in the browser should
  // appear, so the plan is re-read then — at most every RECHECK_MS.
  const onFocus = () => {
    if (!connecting() && Date.now() - lastCheck > RECHECK_MS) check(false);
  };
  window.addEventListener('focus', onFocus);

  const stopPolling = () => {
    if (pollTimer !== undefined) {
      clearInterval(pollTimer);
      pollTimer = undefined;
    }
    setConnecting(false);
  };
  onCleanup(() => {
    stopPolling();
    clearTimeout(noteTimer);
    window.removeEventListener('focus', onFocus);
  });

  const connect = async () => {
    setError('');
    say(null);
    // A reconnect starts from a link that is already stored, so "connected"
    // alone cannot mean the new one landed: the link's timestamp changing can.
    const before = status()?.connectedAt;
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
        if (st?.connected && st.connectedAt !== before) {
          setStatus(st);
          setRevoked(false);
          stopPolling();
          // The link just landed, so the plan's models exist server-side now.
          // Fetch them without holding the panel on a spinner: a failure
          // leaves the tab showing the account, which a refresh corrects.
          session.reloadModels().catch(() => {});
          lastCheck = Date.now();
        } else if (Date.now() - startedAt > 15 * 60_000) {
          stopPolling();
        }
      }, 2000);
    } catch {
      setError('Could not start the connect flow — is the ogcode server reachable?');
    }
  };

  const disconnect = async () => {
    if (!confirm('Disconnect OGX? Your plan stays on your OGLAB account — this only unlinks ogcode.')) return;
    try {
      setStatus(await disconnectOGX());
      setRevoked(false);
      say(null);
      // Mirror the connect path: the provider is gone, so the catalogue has to
      // follow it out of the page.
      session.reloadModels().catch(() => {});
    } catch {
      setError('Could not disconnect.');
    }
  };

  const linkedOn = () => {
    const at = status()?.connectedAt;
    return at ? new Date(at).toLocaleDateString(undefined, { day: 'numeric', month: 'short', year: 'numeric' }) : '';
  };

  const initials = () => {
    const local = (status()?.email || '').split('@')[0];
    const parts = local.split(/[._\-+]+/).filter(Boolean);
    if (parts.length === 0) return '?';
    return (parts.length > 1 ? parts[0][0] + parts[1][0] : local.slice(0, 2)).toUpperCase();
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

  const chip = () => {
    switch (state()) {
      case 'disconnected':
        return <StatusChip tone="muted">Not connected</StatusChip>;
      case 'active':
        return <StatusChip tone="ok">Active</StatusChip>;
      case 'noplan':
        return <StatusChip tone="warn">No plan</StatusChip>;
      case 'revoked':
        return <StatusChip tone="danger">Reconnect needed</StatusChip>;
      default:
        return null;
    }
  };

  const waiting = () => (
    <span class="inline-flex items-center gap-2 text-meta text-[color:var(--text-secondary)]">
      <Spinner class="w-3.5 h-3.5" />
      Waiting for your browser…
      <Button variant="text" onClick={stopPolling}>
        Cancel
      </Button>
    </span>
  );

  return (
    <div class="page-enter">
      <Group
        id={OGX_SLOT}
        title="OGX"
        icon={CHIP_ICON}
        action={chip()}
        description={
          <>
            OGX is the ogcode plan by OGLAB. Your plan, usage and billing are managed on{' '}
            <LinkAction href={OGX_WEB_URL}>your OGLAB dashboard</LinkAction>.
          </>
        }
      >
        <Show when={error()}>
          <div class="px-3 pt-3">
            <Banner tone="danger">{error()}</Banner>
          </div>
        </Show>

        <Show when={state() === 'loading' && !error()}>
          <div data-setting class="flex items-center gap-3 px-4 py-4">
            <span class="w-10 h-10 rounded-[10px] bg-[color:var(--bg-hover)] animate-pulse" />
            <span class="flex-1 space-y-2">
              <span class="block h-3 w-24 rounded bg-[color:var(--bg-hover)] animate-pulse" />
              <span class="block h-2.5 w-56 max-w-full rounded bg-[color:var(--bg-hover)] animate-pulse" />
            </span>
          </div>
        </Show>

        {/* Not linked yet: one short, centred invitation — what the plan is
            for, and the one button — rather than a lone settings row in the
            middle of an empty tab. data-setting matters: the shell hides any
            section whose rows all filtered out (the :has() rule in index.css
            keys on that attribute), and this branch has no <Row> to carry it. */}
        <Show when={state() === 'disconnected'}>
          <div data-setting class="px-6 pt-9 pb-8 text-center">
            <OGXMark class="mx-auto w-11 h-11" />
            <p class="mt-3.5 text-ui font-semibold text-[color:var(--text-primary)]">Connect your ogcode plan</p>
            <p class="mx-auto mt-1.5 max-w-[26rem] text-meta leading-[1.6] text-[color:var(--text-tertiary)]">
              Two fast models and $2 of tokens every day, for one flat monthly price. Sign in with OGLAB in
              your browser to link this install.
            </p>
            <div class="mt-4 flex justify-center">
              <Show when={!connecting()} fallback={waiting()}>
                <Button onClick={connect}>Connect OGX</Button>
              </Show>
            </div>
            <ul class="mt-5 flex flex-wrap justify-center gap-x-4 gap-y-1.5 text-micro text-[color:var(--text-muted)]">
              <For each={['No API keys to manage', 'Refills every day at 00:00 UTC', 'Cancel anytime']}>
                {(perk) => (
                  <li class="inline-flex items-center gap-1.5">
                    <svg class="w-3 h-3" style={{ color: 'var(--success)' }} fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="3">
                      <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
                    </svg>
                    {perk}
                  </li>
                )}
              </For>
            </ul>
          </div>
        </Show>

        <Show when={state() === 'active' || state() === 'noplan' || state() === 'revoked'}>
          {/* The header: the plan's mark, where the account stands in a line,
              and the one action that changes it — the dashboard on a live
              plan, subscribing without one, reconnecting a revoked link. */}
          <div data-setting class="flex items-start gap-3.5 px-4 py-4 border-b border-[color:var(--border-subtle)] max-sm:flex-wrap">
            <OGXMark class="w-10 h-10" />
            <div class="min-w-0 flex-1 self-center">
              <p class="text-ui font-semibold text-[color:var(--text-primary)] leading-snug">
                {{ active: 'OGX', noplan: 'No active plan', revoked: 'Connection expired' }[state() as 'active' | 'noplan' | 'revoked']}
              </p>
              <p class="mt-0.5 text-meta leading-[1.5] text-[color:var(--text-tertiary)] max-w-[34rem]">
                {
                  {
                    active: '$2 of tokens every day, refilled at 00:00 UTC. Usage and billing live on your OGLAB dashboard.',
                    noplan:
                      'Subscribe to OGX on OGLAB for two fast models and $2 of tokens every day. This tab picks the plan up on its own — no need to reconnect.',
                    revoked: "OGLAB no longer accepts this install's link. Reconnect to keep using your plan.",
                  }[state() as 'active' | 'noplan' | 'revoked']
                }
              </p>
              <Show when={note()}>
                {(n) => (
                  <p class="mt-1 text-micro" style={{ color: n().tone === 'ok' ? 'var(--success)' : 'var(--danger)' }}>
                    {n().text}
                  </p>
                )}
              </Show>
            </div>
            <div class="shrink-0 self-center flex items-center gap-1.5 max-sm:w-full max-sm:pl-[3.375rem]">
              <Show when={state() === 'revoked'}>
                <Show when={!connecting()} fallback={waiting()}>
                  <Button onClick={connect}>Reconnect</Button>
                </Show>
              </Show>
              <Show when={state() !== 'revoked'}>
                <Button variant={state() === 'noplan' ? 'filled' : 'outlined'} href={OGX_WEB_URL}>
                  {state() === 'noplan' ? 'Get OGX' : 'Dashboard'}
                  <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.2">
                    <path stroke-linecap="round" stroke-linejoin="round" d={EXTERNAL_ICON} />
                  </svg>
                </Button>
                <button
                  type="button"
                  onClick={() => check(true)}
                  disabled={checking()}
                  title="Check the plan again"
                  aria-label="Check the plan again"
                  class="w-8 h-8 shrink-0 rounded-lg flex items-center justify-center transition-colors
                         text-[color:var(--text-tertiary)] hover:text-[color:var(--text-primary)]
                         hover:bg-[color:var(--bg-hover)] disabled:cursor-default"
                >
                  <svg class={`w-4 h-4 ${checking() ? 'animate-spin' : ''}`} fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                    <path stroke-linecap="round" stroke-linejoin="round" d={REFRESH_ICON} />
                  </svg>
                </button>
              </Show>
            </div>
          </div>

          <Row label="Account" helper={linkedOn() ? `Linked to this install on ${linkedOn()}.` : undefined}>
            <span class="flex items-center gap-2 min-w-0">
              <span
                class="w-6 h-6 shrink-0 rounded-full flex items-center justify-center text-[10px] font-semibold tracking-[0.02em]"
                style={{
                  background: 'color-mix(in srgb, var(--accent) 16%, transparent)',
                  color: 'var(--accent-ink)',
                }}
              >
                {initials()}
              </span>
              <span class="text-meta text-[color:var(--text-secondary)] truncate max-w-[16rem]">
                {status()?.email || 'Connected'}
              </span>
            </span>
          </Row>

          <Show when={state() === 'active'}>
            <Row
              label="Models"
              helper="Included with your plan. Untick any you don't want in the model picker."
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
                onEditMeta={props.onEditMeta}
                // Never called: the slot is readOnly, so the add row is hidden,
                // and no plan model is custom, so no row offers a remove button.
                onRemove={() => {}}
                onAdd={async () => {}}
                suggestedCollection=""
                bulk={false}
                included="Included"
              />
            </Row>
          </Show>

          <Row label="Disconnect" helper="Unlinks this install. Your plan, usage and billing stay on your OGLAB account.">
            <Button variant="danger" onClick={disconnect}>
              Disconnect
            </Button>
          </Row>
        </Show>
      </Group>
    </div>
  );
}

/** The plan's mark: OGLAB's own logo, the same artwork its dashboard shows.
 *  The image carries its own rounded tile, so no radius is set here. */
function OGXMark(props: { class?: string }) {
  return <img src="/oglab-logo.png" alt="" aria-hidden="true" class={`shrink-0 ${props.class ?? ''}`} />;
}

/** Words that keep the credential rows on screen while searching, so "key" or
 *  "endpoint" finds them under every provider. */
const CREDENTIAL_TERMS = ['API key', 'Base URL', 'endpoint', 'credentials', 'models'];

// Searching "vendor" or the old "point at" names the affordance rather than
// any slot: the add-a-vendor lane (and only it) answers that query.
const ADD_VENDOR_TERMS = ['vendor', 'point at'];

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
  /** Saves the user's own window, ceiling, price and image support for any of
   *  this provider's models, catalogue ones included. */
  onEditMeta: (m: ModelInfo, meta: CustomModelMeta) => Promise<void>;
  onRemove: (m: ModelInfo) => void;
  onAdd: (id: string, name: string, collection: string, meta: CustomModelMeta) => Promise<void>;
  /** Present only for a vendor slot: removes the vendor entirely — config
   *  row, stored key and models. The builtin protocols have no way out. */
  onRemoveVendor?: () => Promise<void>;
}) {
  const shell = useShell();
  // Vendor ids resolve through the guide fallback — PROVIDER_GUIDE has no
  // entry for a slug minted at save time.
  const guide = () => providerGuideFor(props.slot.id);
  const isVendor = () => isVendorSlotId(props.slot.id);

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

  const [removeErr, setRemoveErr] = createSignal('');
  const removeThisVendor = async () => {
    if (!confirm(`Remove the ${props.slot.label} vendor and its stored key from ogcode? Its models drop out of the picker.`)) return;
    setRemoveErr('');
    try {
      await props.onRemoveVendor!();
    } catch {
      setRemoveErr('Could not remove this vendor. Is the ogcode server still running?');
    }
  };

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
          onEditMeta={props.onEditMeta}
          onRemove={props.onRemove}
          onAdd={props.onAdd}
          suggestedCollection={
            // OpenAI and vendor tabs alike: collectionForBaseURL reads the
            // saved host — a preset's collection for a known one, empty for a
            // custom gateway the user names rows for by hand.
            props.slot.id === 'openai' || isVendor()
              ? collectionForBaseURL(props.config?.effectiveBaseUrl || props.config?.baseUrl || '')
              : ''
          }
        />
      </Row>

      {/* The way a vendor leaves: one DELETE takes the config row and its
          stored key, and removing it is confirmed for the key's sake just as
          clearing a key is. */}
      <Show when={props.onRemoveVendor}>
        <div class="flex items-center gap-3 flex-wrap">
          <LinkAction onClick={removeThisVendor}>Remove this vendor</LinkAction>
          <Show when={removeErr()}>
            <span class="text-micro" style={{ color: 'var(--danger)' }}>{removeErr()}</span>
          </Show>
        </div>
      </Show>
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

  const guide = () => providerGuideFor(props.def.id);

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

  // One state-dependent sentence under the field label — only what the state
  // marker next to the label does not already say. A stored key needs no
  // "leave blank to keep it" prose: the field's own placeholder says that.
  const keyHelper = () => {
    if (dbKeySet() && envKeySet())
      return (
        <>The environment key <Mono>{guide()?.envKey}</Mono> takes priority over the stored one.</>
      );
    if (dbKeySet() || envKeySet()) return null;
    return (
      <>
        <Show when={guide()?.envKey}>
          Read from <Mono>{guide()!.envKey}</Mono> if that variable is set.{' '}
        </Show>
        Applies as soon as you save.
      </>
    );
  };

  return (
    <>
      <Row
        label="Credentials"
        stacked
        hidden={props.hide('API key', 'credentials', 'token', 'Base URL', 'endpoint', 'host')}
      >
        <div class="space-y-4">
          {/* API key. The state rides the label line as quiet inline text —
              it is a fact, not a control — and only the action the state
              actually enables is shown: remove a stored key, or fetch one
              when there is none. */}
          <div>
            <div class="flex flex-wrap items-center gap-x-2.5 gap-y-1">
              <span class="text-meta font-medium text-[color:var(--text-primary)]">API key</span>
              <Show when={keyState()}>
                <span
                  class="inline-flex items-center gap-1.5 text-micro font-medium"
                  style={{ color: 'var(--success)' }}
                >
                  <span class="w-1.5 h-1.5 rounded-full shrink-0" style={{ background: 'var(--success)' }} />
                  {keyState()}
                </span>
              </Show>
            </div>
            <Show when={keyHelper()} fallback={null}>
              <p class="mt-1 text-meta leading-[1.5] text-[color:var(--text-tertiary)]">{keyHelper()}</p>
            </Show>
            <div class="mt-1.5">
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
            <div class="mt-1.5 flex items-center gap-3 flex-wrap">
              <Show when={dbKeySet()}>
                <LinkAction onClick={clearKey}>Remove stored key</LinkAction>
              </Show>
              <Show when={!dbKeySet() && !envKeySet() && guide()?.keysURL}>
                <LinkAction href={guide()!.keysURL}>Get a key at {guide()!.keysLabel}</LinkAction>
              </Show>
            </div>
          </div>

          {/* Base URL — the second half of the same save. One commit covers
              both, so the single Save below is the only button. */}
          <Show when={props.def.hasBaseURL}>
            <div>
              <span class="block text-meta font-medium text-[color:var(--text-primary)]">Base URL</span>
              <p class="mt-1 text-meta leading-[1.5] text-[color:var(--text-tertiary)]">
                Where requests are sent. Leave blank for the default
                <Show when={guide()?.defaultBaseURL}> — <Mono>{guide()!.defaultBaseURL}</Mono></Show>.
              </p>
              <div class="mt-1.5">
                <TextField
                  mono
                  value={baseURL()}
                  onInput={setBaseURL}
                  onEnter={commit}
                  disabled={busy()}
                  ariaLabel={`${props.def.label} base URL`}
                  placeholder={guide()?.defaultBaseURL ?? ''}
                />
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
            </div>
          </Show>

          {/* The outcome line: status on the left, the one Save on the right —
              both fields travel in the same POST. */}
          <div class="flex items-center justify-between gap-3 flex-wrap pt-1">
            <div class="min-w-0 flex items-center gap-3 flex-wrap">
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
            </div>
            <span class="shrink-0">
              <Button onClick={commit} disabled={busy()}>
                {saving() ? 'Saving…' : refreshing() ? 'Fetching…' : 'Save'}
              </Button>
            </span>
          </div>
        </div>
      </Row>
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
  onEditMeta: (m: ModelInfo, meta: CustomModelMeta) => Promise<void>;
  onRemove: (m: ModelInfo) => void;
  onAdd: (id: string, name: string, collection: string, meta: CustomModelMeta) => Promise<void>;
  suggestedCollection: string;
  /** Hide the enable/disable-all chips — for a short list they only add
   *  noise. On by default. */
  bulk?: boolean;
  /** What the price column says for a model with no per-token price, when
   *  the plan covers it ("Included") rather than the price being unknown. */
  included?: string;
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

      <Show when={props.all.length > 0 && props.bulk !== false}>
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
                      <ModelItem
                        model={m}
                        included={props.included}
                        onToggle={() => props.onToggle(m)}
                        onRemove={() => props.onRemove(m)}
                        onSave={(meta) => props.onEditMeta(m, meta)}
                      />
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
          class="mt-2 text-meta font-medium text-[color:var(--accent-ink)] hover:underline underline-offset-2"
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
              ? 'No models yet. Use the refresh button above to check your plan again.'
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
          // The Group field earns its row when a known host names it — OpenAI
          // pointed at a preset, or a vendor tab — and stays hidden where a
          // collection would be invented rather than derived.
          showCollection={props.slot.id === 'openai' || isVendorSlotId(props.slot.id)}
          onAdd={props.onAdd}
        />
      </Show>
    </div>
  );
}

function ModelItem(props: {
  model: ModelInfo;
  included?: string;
  onToggle: () => void;
  onRemove: () => void;
  /** Save what the user configured about this model. Every row has one: a
   *  catalogue or fetched model is edited exactly like one added by hand. */
  onSave: (meta: CustomModelMeta) => Promise<void>;
}) {
  const [editing, setEditing] = createSignal(false);
  const hasPrice = () => props.model.inputPricePerM > 0 || props.model.outputPricePerM > 0;
  // A disabled row steps the text down one token instead of dimming the whole row
  // with opacity (which composites text toward the background and drops below
  // WCAG AA). The unchecked switch plus this colour step are the disabled cues.
  const nameColor = () => props.model.enabled ? 'text-[color:var(--text-primary)]' : 'text-[color:var(--text-secondary)]';
  const numColor = () => props.model.enabled ? 'text-[color:var(--text-tertiary)]' : 'text-[color:var(--text-muted)]';
  return (
    <>
    <div
      class={`group/row flex items-center gap-3 h-8 border-b border-[color:var(--border-subtle)] last:border-b-0
              hover:bg-[color:var(--bg-hover)]/40 transition-colors`}
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
        <span class={`text-meta ${nameColor()} truncate`}>{props.model.name}</span>
        <Show when={props.model.default}>
          <Tag tone="accent">default</Tag>
        </Show>
        <Show when={props.model.isCustom}>
          <Tag>custom</Tag>
        </Show>
        {/* Marked so an edit on a catalogue or fetched model is legible: the
            row's figures are now partly the user's, not the catalogue's. */}
        <Show when={!props.model.isCustom && props.model.override}>
          <Tag>edited</Tag>
        </Show>
      </div>

      <span
        class="hidden md:block shrink-0 w-[10rem] text-micro font-mono text-[color:var(--text-muted)] truncate"
        title={props.model.id}
      >
        {props.model.id}
      </span>

      <span
        class={`hidden sm:block shrink-0 w-[3.5rem] text-micro font-mono tabular-nums text-right ${numColor()}`}
        title={props.model.contextWindow ? `Context window: ${props.model.contextWindow.toLocaleString()} tokens` : 'Context window unknown'}
      >
        <Show when={props.model.contextWindow} fallback={<span class="text-[color:var(--text-muted)]">—</span>}>
          {(w) => fmtWindow(w())}
        </Show>
      </span>

      {/* The output ceiling, when one is known: catalogue for a built-in, the
          user's own figure for a custom model. Absent means no column at all,
          so every other row keeps exactly the width it had. */}
      <Show when={props.model.maxOutputTokens}>
        {(out) => (
          <span
            class={`hidden sm:block shrink-0 w-[3rem] text-micro font-mono tabular-nums text-right ${numColor()}`}
            title={`Max output: ${out().toLocaleString()} tokens`}
          >
            {fmtWindow(out())}
          </span>
        )}
      </Show>

      <span
        class={`shrink-0 w-[7rem] whitespace-nowrap text-micro font-mono tabular-nums text-right ${numColor()}`}
        title={props.included && !hasPrice() ? 'Covered by your plan' : 'Input / output price per 1M tokens'}
      >
        <Show when={hasPrice()} fallback={<span class="text-[color:var(--text-muted)]">{props.included || '—'}</span>}>
          ${fmtPrice(props.model.inputPricePerM)} / ${fmtPrice(props.model.outputPricePerM)}
        </Show>
      </span>

      <span class="w-14 shrink-0 flex items-center justify-end gap-0.5">
        <span
          class={`transition-opacity ${editing() ? 'opacity-100' : 'opacity-0 group-hover/row:opacity-100 focus-within:opacity-100'}`}
        >
          <IconButton
            onClick={() => setEditing(true)}
            label={`Edit ${props.model.name}`}
            path="M16.5 3.5a2.1 2.1 0 013 3L7 19l-4 1 1-4zM3 21h18"
          />
        </span>
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
    <Show when={editing()}>
      <ModelMetaDialog model={props.model} onSave={props.onSave} onClose={() => setEditing(false)} />
    </Show>
    </>
  );
}

/** The six fields in one row, for the add-a-model form. The per-row editor has
 *  its own labelled layout in the metadata dialog; the two share the same form
 *  shape so they cannot drift apart. */
function MetaFields(props: {
  form: MetaForm;
  onChange: (next: Partial<MetaForm>) => void;
  onKeyDown: (e: KeyboardEvent) => void;
}) {
  const field = `${fieldClass} min-w-0 text-meta`;
  return (
    <div class="flex items-center gap-2 flex-wrap">
      <input
        type="number"
        min="0"
        step="1000"
        value={props.form.contextWindow}
        onInput={(e) => props.onChange({ contextWindow: e.currentTarget.value })}
        onKeyDown={props.onKeyDown}
        placeholder="200000"
        aria-label="Context window in tokens"
        title="Context window (tokens) — how much history the loop sizes compaction against"
        class={`${field} w-[9.5rem] tabular-nums`}
      />
      <input
        type="number"
        min="0"
        step="1000"
        value={props.form.maxOutput}
        onInput={(e) => props.onChange({ maxOutput: e.currentTarget.value })}
        onKeyDown={props.onKeyDown}
        placeholder="Max output tokens"
        aria-label="Max output tokens"
        title="The model's output ceiling (tokens)"
        class={`${field} w-[9.5rem] tabular-nums`}
      />
      <input
        type="number"
        min="0"
        step="0.01"
        value={props.form.inputPrice}
        onInput={(e) => props.onChange({ inputPrice: e.currentTarget.value })}
        onKeyDown={props.onKeyDown}
        placeholder="$ in / 1M"
        aria-label="Input price in dollars per 1M tokens"
        title="What 1M input tokens cost, in dollars"
        class={`${field} w-[7rem] tabular-nums`}
      />
      <input
        type="number"
        min="0"
        step="0.01"
        value={props.form.outputPrice}
        onInput={(e) => props.onChange({ outputPrice: e.currentTarget.value })}
        onKeyDown={props.onKeyDown}
        placeholder="$ out / 1M"
        aria-label="Output price in dollars per 1M tokens"
        title="What 1M output tokens cost, in dollars"
        class={`${field} w-[7rem] tabular-nums`}
      />
      <input
        type="number"
        min="0"
        step="0.01"
        value={props.form.cacheReadPrice}
        onInput={(e) => props.onChange({ cacheReadPrice: e.currentTarget.value })}
        onKeyDown={props.onKeyDown}
        placeholder="cache read $/1M"
        aria-label="Cache read price in dollars per 1M tokens"
        title="What 1M tokens read from the prompt cache cost, in dollars"
        class={`${field} w-[7rem] tabular-nums`}
      />
      <input
        type="number"
        min="0"
        step="0.01"
        value={props.form.cacheWritePrice}
        onInput={(e) => props.onChange({ cacheWritePrice: e.currentTarget.value })}
        onKeyDown={props.onKeyDown}
        placeholder="cache write $/1M"
        aria-label="Cache write price in dollars per 1M tokens"
        title="What 1M tokens written to the prompt cache cost, in dollars"
        class={`${field} w-[7rem] tabular-nums`}
      />
      <label class="inline-flex items-center gap-1.5 h-8 text-meta text-[color:var(--text-secondary)] cursor-pointer">
        <input
          type="checkbox"
          checked={props.form.images}
          onChange={(e) => props.onChange({ images: e.currentTarget.checked })}
          class="shrink-0 accent-[color:var(--accent)] cursor-pointer"
        />
        Takes images
      </label>
    </div>
  );
}

function AddModelRow(props: {
  providerLabel: string;
  suggestedCollection: string;
  showCollection: boolean;
  onAdd: (id: string, name: string, collection: string, meta: CustomModelMeta) => Promise<void>;
}) {
  const [open, setOpen] = createSignal(false);
  const [id, setId] = createSignal('');
  const [name, setName] = createSignal('');
  const [collection, setCollection] = createSignal('');
  // A custom model's window, ceiling, price and image support are facts no
  // catalogue can supply for an ID it has never seen. They stay folded behind
  // one toggle, so the row reads exactly as it did before they existed.
  const [advanced, setAdvanced] = createSignal(false);
  // The five advanced fields live in one object, shared with the per-row
  // editor, so the add row and the editor cannot drift apart.
  const [form, setForm] = createSignal<MetaForm>(EMPTY_META);
  const patch = (next: Partial<MetaForm>) => setForm({ ...form(), ...next });
  const [error, setError] = createSignal('');
  const [busy, setBusy] = createSignal(false);

  const start = () => {
    setId('');
    setName('');
    setCollection(props.suggestedCollection);
    setAdvanced(false);
    setForm(EMPTY_META);
    setError('');
    setOpen(true);
  };

  const submit = async () => {
    const modelId = id().trim();
    if (!modelId) { setError('A model ID is required.'); return; }
    setBusy(true);
    setError('');
    try {
      await props.onAdd(modelId, name().trim() || modelId, collection().trim(), metaFrom(form()));
      setOpen(false);
    } catch (e: any) {
      setError(e?.message || 'Could not add that model.');
    } finally {
      setBusy(false);
    }
  };

  // Enter adds, Escape folds the form away — from any field, advanced ones too.
  const onKey = (e: KeyboardEvent) => {
    if (e.key === 'Enter') submit();
    if (e.key === 'Escape') setOpen(false);
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
            onKeyDown={onKey}
            placeholder="model-id (exactly as the provider names it)"
            aria-label="Model ID"
            class={`${field} font-mono flex-[1.4]`}
          />
          <input
            type="text"
            value={name()}
            onInput={(e) => setName(e.currentTarget.value)}
            onKeyDown={onKey}
            placeholder="Display name (optional)"
            aria-label="Display name"
            class={`${field} flex-1`}
          />
          <Show when={props.showCollection}>
            <input
              type="text"
              value={collection()}
              onInput={(e) => setCollection(e.currentTarget.value)}
              onKeyDown={onKey}
              placeholder="Group"
              aria-label="Group"
              title="Groups this model under a vendor name in the picker"
              class={`${field} w-24`}
            />
          </Show>
          <Button onClick={submit} disabled={busy()}>{busy() ? 'Adding…' : 'Add'}</Button>
          <Button variant="text" onClick={() => setOpen(false)}>Cancel</Button>
        </div>
        <div class="mt-1.5 flex items-start gap-3 flex-wrap">
          <button
            type="button"
            onClick={() => setAdvanced(!advanced())}
            aria-expanded={advanced()}
            class="inline-flex items-center gap-1 shrink-0 h-5 text-micro font-medium
                   text-[color:var(--text-tertiary)] hover:text-[color:var(--text-primary)] transition-colors"
          >
            Advanced
            <svg
              class={`w-2.5 h-2.5 transition-transform ${advanced() ? 'rotate-180' : ''}`}
              fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5"
            >
              <path stroke-linecap="round" stroke-linejoin="round" d="M19 9l-7 7-7-7" />
            </svg>
          </button>
          <p class="text-micro text-[color:var(--text-muted)]">
            <Show when={error()} fallback="ogcode does not verify the ID — a typo shows up as a failed request on first use.">
              <span style={{ color: 'var(--danger)' }}>{error()}</span>
            </Show>
          </p>
        </div>
        {/* Everything a catalogue would have said about this ID. Blank means
            "not set": the row shows a dash and the loop falls back to what it
            knows instead of guessing. */}
        <Show when={advanced()}>
          <div class="mt-2">
            <MetaFields form={form()} onChange={patch} onKeyDown={onKey} />
          </div>
        </Show>
      </div>
    </Show>
  );
}

// A window in the unit it is quoted in: "1M", "1.05M", "200K", "922K".
function fmtWindow(n: number): string {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(2).replace(/\.?0+$/, '') + 'M';
  return Math.round(n / 1000) + 'K';
}

// Always two decimals. In a right-aligned tabular column, "$1.6" next to
// "$0.40" breaks the decimal alignment that makes prices comparable at a
// glance. Sub-cent rates are real on aggregators, and rounding them to "0.00"
// would read as free.
function fmtPrice(n: number): string {
  if (n > 0 && n < 0.01) return '<0.01';
  return n.toFixed(2);
}
