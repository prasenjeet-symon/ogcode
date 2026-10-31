import { For, Show, createSignal } from 'solid-js';
import type { ModelInfo, CustomModelMeta } from '../../api/client';
import DialogShell, { DialogHeader } from '../../components/dialog-shell';
import { fieldClass } from './ui';

/**
 * The metadata editor for one model: the figures a catalogue would otherwise
 * supply, and the ones the user supplies instead.
 *
 * It lives in a dialog rather than unfolding under the row on purpose. A row in
 * the model list is one line tall and carries a checkbox, an id, a window and a
 * price; the editor needs six fields, each with a unit and a fallback, and it
 * needs room to explain what a blank box means. Opening it over the page keeps
 * the list intact behind the dimmed backdrop, so the model being edited stays
 * visible while its figures change.
 */

/** The facts a catalogue would otherwise supply about a model, in the shape
 *  both the add row and the per-row editor edit them. Blank means "not set":
 *  the row keeps showing the catalogue's figure instead of a guess. */
export type MetaForm = {
  contextWindow: string;
  maxOutput: string;
  inputPrice: string;
  outputPrice: string;
  cacheReadPrice: string;
  cacheWritePrice: string;
  images: boolean;
};

export const EMPTY_META: MetaForm = {
  contextWindow: '',
  maxOutput: '',
  inputPrice: '',
  outputPrice: '',
  cacheReadPrice: '',
  cacheWritePrice: '',
  images: false,
};

/** Only what the user actually typed: a blank field is absent from the body,
 *  never NaN (which serialises to null and fails the server's decode). Absent
 *  means "not configured", so the catalogue and the provider answer instead. */
export function metaFrom(form: MetaForm): CustomModelMeta {
  const m: CustomModelMeta = {};
  const win = optNumber(form.contextWindow);
  if (win !== undefined) m.contextWindow = win;
  const out = optNumber(form.maxOutput);
  if (out !== undefined) m.maxOutputTokens = out;
  const inp = optNumber(form.inputPrice);
  if (inp !== undefined) m.inputPricePerM = inp;
  const outp = optNumber(form.outputPrice);
  if (outp !== undefined) m.outputPricePerM = outp;
  const cacheRead = optNumber(form.cacheReadPrice);
  if (cacheRead !== undefined) m.cacheReadPricePerM = cacheRead;
  const cacheWrite = optNumber(form.cacheWritePrice);
  if (cacheWrite !== undefined) m.cacheWritePricePerM = cacheWrite;
  if (form.images) m.supportsImages = true;
  return m;
}

// An optional numeric field: blank or unparseable reads as "not set"
// (undefined), never NaN — NaN serialises to null and would fail the server's
// decode of a number.
function optNumber(raw: string): number | undefined {
  const t = raw.trim();
  if (!t) return undefined;
  const n = Number(t);
  return Number.isFinite(n) && n > 0 ? n : undefined;
}

type FieldKey = 'contextWindow' | 'maxOutput' | 'inputPrice' | 'outputPrice' | 'cacheReadPrice' | 'cacheWritePrice';

type Field = {
  key: FieldKey;
  label: string;
  unit: string;
  step: string;
  /** What the box shows when it is empty and the model has no such figure. */
  example: string;
  /** What the model currently reports for this figure. */
  known: (m: ModelInfo) => number | undefined;
  /** How that figure is written out for a person. */
  fmt: (n: number) => string;
  hint: string;
};

const FIELDS: Field[] = [
  {
    key: 'contextWindow',
    label: 'Context window',
    unit: 'tokens',
    step: '1000',
    example: '200000',
    known: (m) => m.contextWindow,
    fmt: (n) => `${n.toLocaleString()} tokens`,
    hint: 'How much history the loop keeps before it compacts.',
  },
  {
    key: 'maxOutput',
    label: 'Max output tokens',
    unit: 'tokens',
    step: '1000',
    example: '64000',
    known: (m) => m.maxOutputTokens,
    fmt: (n) => `${n.toLocaleString()} tokens`,
    hint: 'The ceiling on what one reply may contain.',
  },
  {
    key: 'inputPrice',
    label: 'Input price',
    unit: '$ per 1M tokens',
    step: '0.01',
    example: '0.45',
    known: (m) => m.inputPricePerM,
    fmt: (n) => `$${n.toFixed(2)}`,
    hint: 'What 1M input tokens cost.',
  },
  {
    key: 'outputPrice',
    label: 'Output price',
    unit: '$ per 1M tokens',
    step: '0.01',
    example: '1.80',
    known: (m) => m.outputPricePerM,
    fmt: (n) => `$${n.toFixed(2)}`,
    hint: 'What 1M output tokens cost.',
  },
  {
    key: 'cacheReadPrice',
    label: 'Cache read price',
    unit: '$ per 1M tokens',
    step: '0.01',
    example: '0.05',
    known: (m) => m.cacheReadPricePerM,
    fmt: (n) => `$${n.toFixed(2)}`,
    hint: 'What 1M tokens read from the prompt cache cost.',
  },
  {
    key: 'cacheWritePrice',
    label: 'Cache write price',
    unit: '$ per 1M tokens',
    step: '0.01',
    example: '0.60',
    known: (m) => m.cacheWritePricePerM,
    fmt: (n) => `$${n.toFixed(2)}`,
    hint: 'What 1M tokens written to the prompt cache cost.',
  },
];

export default function ModelMetaDialog(props: {
  model: ModelInfo;
  onSave: (meta: CustomModelMeta) => Promise<void>;
  onClose: () => void;
}) {
  const own = () => props.model.override ?? {};
  const [form, setForm] = createSignal<MetaForm>({
    contextWindow: own().contextWindow ? String(own().contextWindow) : '',
    maxOutput: own().maxOutputTokens ? String(own().maxOutputTokens) : '',
    inputPrice: own().inputPricePerM ? String(own().inputPricePerM) : '',
    outputPrice: own().outputPricePerM ? String(own().outputPricePerM) : '',
    cacheReadPrice: own().cacheReadPricePerM ? String(own().cacheReadPricePerM) : '',
    cacheWritePrice: own().cacheWritePricePerM ? String(own().cacheWritePricePerM) : '',
    images: !!own().supportsImages,
  });
  const [error, setError] = createSignal('');
  const [busy, setBusy] = createSignal(false);
  const patch = (next: Partial<MetaForm>) => setForm({ ...form(), ...next });

  let firstField: HTMLInputElement | undefined;

  const save = async () => {
    if (busy()) return;
    setBusy(true);
    setError('');
    try {
      await props.onSave(metaFrom(form()));
      props.onClose();
    } catch (e: any) {
      setError(e?.message || 'Could not save those figures.');
    } finally {
      setBusy(false);
    }
  };

  // Enter saves from any field; Escape is DialogShell's job.
  const onKey = (e: KeyboardEvent) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      save();
    }
  };

  /** The figure this box falls back to while it is empty — the model's own
   *  current number, which is the catalogue's unless the user has set one. */
  const fallback = (f: Field) => {
    if (form()[f.key].trim() !== '') return undefined;
    const n = f.known(props.model);
    return n && n > 0 ? f.fmt(n) : undefined;
  };

  const uid = `model-meta-${props.model.id.replace(/[^a-zA-Z0-9_-]/g, '-')}`;

  return (
    <DialogShell
      labelledBy={`${uid}-title`}
      describedBy={`${uid}-desc`}
      onClose={props.onClose}
      class="sm:max-w-[560px]"
      initialFocus={() => firstField ?? null}
      onKeyDown={onKey}
    >
      <DialogHeader
        id={`${uid}-title`}
        descriptionId={`${uid}-desc`}
        title={`Edit ${props.model.name}`}
        description={
          <>
            <span class="font-mono text-[color:var(--text-secondary)] break-all">{props.model.id}</span>
            {' — these figures sit over whatever the catalogue and the provider say.'}
          </>
        }
        onClose={props.onClose}
        icon={
          <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
            <path stroke-linecap="round" stroke-linejoin="round" d="M16.5 3.5a2.1 2.1 0 013 3L7 19l-4 1 1-4zM3 21h18" />
          </svg>
        }
      />

      {/* ---- Fields ---- */}
      <div class="flex-1 min-h-0 overflow-y-auto px-5 py-4">
        <div class="grid grid-cols-1 sm:grid-cols-2 gap-x-4 gap-y-3.5">
          <For each={FIELDS}>
            {(f, i) => (
              <label class="block">
                <span class="flex items-baseline justify-between gap-2 mb-1">
                  <span class="text-meta font-medium text-[color:var(--text-secondary)]">{f.label}</span>
                  <span class="text-micro text-[color:var(--text-muted)]">{f.unit}</span>
                </span>
                <input
                  ref={i() === 0 ? (el) => { firstField = el; } : undefined}
                  type="number"
                  min="0"
                  step={f.step}
                  value={form()[f.key]}
                  onInput={(e) => patch({ [f.key]: e.currentTarget.value } as Partial<MetaForm>)}
                  placeholder={f.example}
                  aria-label={`${f.label} in ${f.unit}`}
                  class={`${fieldClass} w-full tabular-nums`}
                />
                <span class="mt-1 block text-micro text-[color:var(--text-muted)] leading-snug">
                  <Show when={fallback(f)} fallback={f.hint}>
                    {(v) => <>Falls back to {v()}.</>}
                  </Show>
                </span>
              </label>
            )}
          </For>
        </div>

        <label class="mt-3.5 flex items-center gap-2 text-meta text-[color:var(--text-secondary)] cursor-pointer">
          <input
            type="checkbox"
            checked={form().images}
            onChange={(e) => patch({ images: e.currentTarget.checked })}
            class="shrink-0 accent-[color:var(--accent)] cursor-pointer"
          />
          Takes images
          <Show when={props.model.supportsImages && !form().images}>
            <span class="text-micro text-[color:var(--text-muted)]">(already on)</span>
          </Show>
        </label>
      </div>

      {/* ---- Footer ---- */}
      <div class="shrink-0 px-5 py-3 border-t border-[color:var(--border-subtle)] bg-[color:var(--bg-base)]/40 flex items-center justify-between gap-3">
        <p class="text-micro text-[color:var(--text-muted)] leading-snug min-w-0">
          <Show when={error()} fallback="Blank falls back to what the catalogue and provider say.">
            <span style={{ color: 'var(--danger)' }}>{error()}</span>
          </Show>
        </p>
        <div class="flex items-center gap-2 shrink-0">
          <button
            type="button"
            onClick={props.onClose}
            class="h-8 px-3 rounded-lg text-meta text-[color:var(--text-secondary)] hover:text-[color:var(--text-primary)] hover:bg-[color:var(--bg-elevated)] transition"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={save}
            disabled={busy()}
            class="h-8 px-3.5 rounded-lg text-meta font-medium bg-[color:var(--accent)] text-[color:var(--on-primary)] hover:bg-[color:var(--accent-hover)] disabled:opacity-40 disabled:cursor-not-allowed transition shadow-[var(--shadow-sm)]"
          >
            {busy() ? 'Saving…' : 'Save'}
          </button>
        </div>
      </div>
    </DialogShell>
  );
}
