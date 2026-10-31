import { Show, createSignal, createEffect, onCleanup, onMount } from 'solid-js';
import { useNavigate } from '@solidjs/router';
import { useSession } from '../../context/session';
import type { CatalogSyncStatus, CatalogSyncResult } from '../../api/client';
import { getCatalogSyncStatus, syncModelCatalog } from '../../api/client';
import { Group, Row, Button, StatusChip, Banner, Spinner, CopyButton, Value } from './ui';

// ---------------------------------------------------------------------------
// Upstream catalogue — the shared model list.
//
// ogcode fetches one catalogue upstream and lays it over every provider's own
// rows: synced when the server starts, refetched on its own once a day, and
// its last landing reported here with the button that forces it now. The sync
// lives server-side; this panel only reads and asks.
//
// It sits on the General page because no provider owns it — the list belongs
// to the install, not to any one tab in Models.
// ---------------------------------------------------------------------------

/** A cloud with an arrow into it: the list arriving from somewhere else. */
const CATALOGUE_ICON =
  'M2.25 15a4.5 4.5 0 004.5 4.5H18a3.75 3.75 0 001.332-7.257 3 3 0 00-3.758-3.848 5.25 5.25 0 00-10.233 2.33A4.502 4.502 0 002.25 15zm10.5-1.5v-6m0 0l-2.25 2.25M12.75 7.5L15 9.75';

/** How old a sync may get before the list is worth fetching again. The
 *  server refetches on the same cadence. */
const STALE_AFTER_MS = 24 * 60 * 60 * 1000;

/** A row shows when the query appears anywhere a reader would look for it. */
export type Hide = (...text: (string | undefined)[]) => boolean;

export function CatalogueGroup(props: { hide: Hide }) {
  const session = useSession();
  const navigate = useNavigate();
  const [status, setStatus] = createSignal<CatalogSyncStatus | null>(null);
  const [loading, setLoading] = createSignal(true);
  const [syncing, setSyncing] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);
  const [result, setResult] = createSignal<CatalogSyncResult | null>(null);

  // Reading the status on mount can fail only in mid-restart corners; the row
  // then reads as never synced, and a sync attempt surfaces the real story —
  // so the error is swallowed here, not shown.
  onMount(async () => {
    try {
      setStatus(await getCatalogSyncStatus());
    } catch {
      /* unreachable server or an older build; the row stays "Never" */
    } finally {
      setLoading(false);
    }
  });

  // "3 hours ago" is only true until the next hour; a slow tick keeps the
  // age honest without the user reloading the page.
  const [tick, setTick] = createSignal(0);
  createEffect(() => {
    const t = setInterval(() => setTick((n) => n + 1), 30_000);
    onCleanup(() => clearInterval(t));
  });

  const syncedAt = () => {
    const at = status()?.syncedAt;
    if (!at) return null;
    const d = new Date(at);
    return Number.isNaN(d.getTime()) ? null : d;
  };

  /** How long ago the list landed, in words. */
  const ago = () => {
    // The tick is read here so the age re-renders as the clock moves; without
    // it the text would stay frozen at whatever it said when it first drew.
    tick();
    const d = syncedAt();
    if (!d) return '';
    const secs = Math.max(0, Math.round((Date.now() - d.getTime()) / 1000));
    if (secs < 60) return 'just now';
    const mins = Math.round(secs / 60);
    if (mins < 60) return `${mins} minute${mins === 1 ? '' : 's'} ago`;
    const hours = Math.round(mins / 60);
    if (hours < 24) return `${hours} hour${hours === 1 ? '' : 's'} ago`;
    const days = Math.round(hours / 24);
    return `${days} day${days === 1 ? '' : 's'} ago`;
  };

  const absolute = () => {
    const d = syncedAt();
    if (!d) return '';
    return d.toLocaleString(undefined, {
      day: 'numeric',
      month: 'short',
      year: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
    });
  };

  /** Ok while the list is fresh, a warning once it has aged past a day,
   *  muted before the first sync ever landed. */
  const tone = (): 'ok' | 'warn' | 'muted' => {
    const d = syncedAt();
    if (!d) return 'muted';
    return Date.now() - d.getTime() > STALE_AFTER_MS ? 'warn' : 'ok';
  };

  const stateLabel = () => {
    if (loading()) return 'Checking…';
    const t = tone();
    if (t === 'muted') return 'Never synced';
    if (t === 'warn') return 'Needs a refresh';
    return 'Up to date';
  };

  const source = () => status()?.source || '';
  const modelCount = () => status()?.modelCount ?? 0;

  const syncNow = async () => {
    if (syncing()) return;
    setSyncing(true);
    setError(null);
    setResult(null);
    try {
      const res = await syncModelCatalog();
      setResult(res);
      // The sync route reports where it landed but not the synced flag, so the
      // status is rebuilt here — otherwise a successful sync would leave the
      // row reading "Never synced".
      setStatus({
        synced: true,
        syncedAt: res.syncedAt,
        source: res.source,
        modelCount: res.modelCount,
      });
      // The rows the server just wrote are the ones on screen elsewhere.
      void session.reloadModels();
    } catch (e) {
      setError(errorText(e));
    } finally {
      setSyncing(false);
    }
  };

  const summary = () => {
    const r = result();
    if (!r) return '';
    const bits: string[] = [];
    if (r.installed) bits.push(`${r.installed} added`);
    if (r.updated) bits.push(`${r.updated} updated`);
    if (r.skipped) bits.push(`${r.skipped} skipped`);
    const what = bits.length ? ` — ${bits.join(', ')}` : '';
    return `Synced ${r.modelCount ?? 0} models${what}.`;
  };

  const terms = ['catalogue', 'catalog', 'upstream', 'model list', 'models', 'sync'];

  /** The list without the noisy protocol and host tail: enough to recognise
   *  where it came from, short enough to read on one line. */
  const shortSource = () => {
    const url = source();
    if (!url) return '';
    try {
      const u = new URL(url);
      const path = u.pathname.split('/').filter(Boolean).slice(-1)[0] || '';
      return `${u.host}/${path}`;
    } catch {
      return url;
    }
  };

  return (
    <Group
      id="catalogue"
      title="Model catalogue"
      icon={CATALOGUE_ICON}
      description="One shared list of models, fetched by ogcode itself and laid over every provider's rows. It syncs at startup and again once a day."
    >
      <Row
        label="Shared model list"
        helper="The list every provider's rows sit under. Fetch it yourself whenever you want the newest one."
        hidden={props.hide('Shared model list', ...terms)}
      >
        <StatusChip tone={tone()} pulse={syncing()}>
          {stateLabel()}
        </StatusChip>
        <Button onClick={syncNow} disabled={syncing()} title="Fetch the shared model list again now">
          <Show when={syncing()} fallback={<>Sync now</>}>
            <Spinner class="w-3.5 h-3.5" />
            Syncing…
          </Show>
        </Button>
      </Row>

      <Row
        label="Models in the list"
        helper={
          modelCount()
            ? 'Models the shared list currently carries.'
            : 'How many models the shared list carries.'
        }
        hidden={props.hide('Models in the list', ...terms)}
      >
        <Show
          when={modelCount()}
          fallback={<Value mono={false} tone="muted">Unknown</Value>}
        >
          <Value mono={false}>{modelCount()}</Value>
        </Show>
        <button
          type="button"
          onClick={() => navigate('/settings/models')}
          class="text-meta font-medium text-[color:var(--accent-ink)] hover:underline underline-offset-2 transition-colors whitespace-nowrap"
        >
          View models
        </button>
      </Row>

      <Row
        label="Last synced"
        helper={ago() || 'Not yet synced — ogcode fetches the list at startup and once a day.'}
        hidden={props.hide('Last synced', ...terms)}
      >
        <Show when={syncedAt()} fallback={<Value mono={false} tone="muted">Never</Value>}>
          <Value mono={false} tone="muted">{absolute()}</Value>
        </Show>
      </Row>

      <Row
        label="Source"
        stacked
        helper="Where the shared list is fetched from."
        hidden={props.hide('Source', ...terms)}
      >
        <Show
          when={source()}
          fallback={
            <Value mono={false} tone="muted">
              Not known yet — sync once to see where the list comes from.
            </Value>
          }
        >
          <div class="flex items-center gap-2 min-w-0">
            <a
              href={source()}
              target="_blank"
              rel="noreferrer noopener"
              title={source()}
              class="font-mono text-micro text-[color:var(--text-secondary)] hover:text-[color:var(--text-primary)] hover:underline underline-offset-2 truncate transition-colors"
            >
              {shortSource()}
            </a>
            <CopyButton text={source()} label="" />
          </div>
        </Show>
      </Row>

      <Show when={error()}>
        {(e) => (
          <div class="px-4 py-2.5">
            <Banner tone="danger">
              Couldn't sync the catalogue — {e()}. The last synced list is still in place.
            </Banner>
          </div>
        )}
      </Show>

      <Show when={summary()}>
        {(s) => (
          <div class="px-4 py-2.5">
            <Banner tone="ok">{s()}</Banner>
          </div>
        )}
      </Show>
    </Group>
  );
}

/** The route's error body is {"error": "..."} inside a generic API error —
 *  dig the sentence out of it rather than showing "API error 502". */
function errorText(e: unknown): string {
  const raw = e instanceof Error ? e.message : String(e);
  const m = raw.match(/"error"\s*:\s*"([^"]*)"/);
  return m ? m[1] : raw;
}
