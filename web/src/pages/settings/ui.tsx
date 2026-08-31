import {
  Show,
  createContext,
  createSignal,
  useContext,
  type Accessor,
  type JSX,
} from 'solid-js';

// ---------------------------------------------------------------------------
// Settings UI kit — a sheet, detailed the way iOS details its Settings app.
//
// Still one continuous page rather than a stack of floating cards. What is
// borrowed is the detailing, which is where that app does its work:
//
//   · A section is a plain grey caption above its rows and a grey footnote
//     below them. The explanation goes *under* the group it explains, so the
//     rows themselves stay the first thing you read.
//   · Rows are 44px, label left, value grey on the right, chevron when the row
//     leads somewhere. A row that has an icon gets it as a filled rounded
//     square — the one detail that makes an iOS settings list recognisable at
//     a glance.
//   · Separators are inset: they start at the label, not at the edge of the
//     screen, so an icon column reads as a column.
//   · Controls are the platform's own — a capsule switch with a raised knob, a
//     thin slider with a round white knob, tinted text for anything that is an
//     action rather than a value.
//
// Colour still comes from the project's accent, so the whole page follows
// whatever theme the workspace is set to.
// ---------------------------------------------------------------------------

/** What a page tells the shell about itself: the vocabulary for its search
 *  box. Counts are read off the rendered page so they cannot fall out of step
 *  with the rows actually on screen. */
export interface ShellReport {
  /** Plural noun for the things being filtered: "settings", "skills", … */
  noun: string;
}

interface ShellContext {
  query: Accessor<string>;
  setQuery: (v: string) => void;
  report: (r: ShellReport) => void;
}

export const SettingsShell = createContext<ShellContext>();

/** Read the shell's live search query. The shell owns the input; each page
 *  owns what matching means for its own content. */
export function useShell(): ShellContext {
  const ctx = useContext(SettingsShell);
  if (!ctx) throw new Error('useShell must be used inside the settings layout');
  return ctx;
}

/** Case-insensitive "does this row match the query" helper shared by pages. */
export function matches(query: string, ...fields: (string | undefined | null)[]): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  return fields.some((f) => (f ?? '').toLowerCase().includes(q));
}

// ---------------------------------------------------------------------------
// Structure
// ---------------------------------------------------------------------------

/** A section of the page: a heading, a rule beneath it, then its rows.
 *
 *  The shell reads `data-section` out of the DOM to count what survived the
 *  search, and a section whose rows are all filtered out removes itself (see
 *  the `:has()` rule in index.css), taking its heading with it. */
export function Group(props: {
  id: string;
  title: string;
  /** Small glyph set before the caption. */
  icon?: string;
  /** Explanatory text. Rendered as a footnote *below* the rows, where iOS puts
   *  it: the rows are what you came for, the explanation is what you read if
   *  they did not answer the question. */
  description?: JSX.Element;
  /** Trailing control for the whole section, aligned with the caption. */
  action?: JSX.Element;
  children: JSX.Element;
}) {
  return (
    <section data-section={props.id} data-label={props.title} class="pt-7 first:pt-1">
      <div class="flex items-end gap-2 px-4 pb-1.5">
        <h2 class="flex items-center gap-1.5 text-micro font-medium uppercase tracking-[0.06em] text-[color:var(--text-muted)]">
          <Show when={props.icon}>
            <svg class="w-3 h-3 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.9">
              <path stroke-linecap="round" stroke-linejoin="round" d={props.icon} />
            </svg>
          </Show>
          {props.title}
        </h2>
        <div class="flex-1" />
        <Show when={props.action}>
          <div class="shrink-0 pb-px">{props.action}</div>
        </Show>
      </div>

      {/* The grouped list. Rounded and filled a half-step above the page, with
          no border and no shadow — the shape reads as a group without becoming
          the floating card this screen deliberately moved away from. */}
      <div class="rounded-[10px] bg-[color:var(--bg-elevated)]/40 overflow-hidden">
        {props.children}
      </div>

      <Show when={props.description}>
        <p class="px-4 pt-2 text-meta leading-[1.5] text-[color:var(--text-tertiary)] max-w-[40rem]">
          {props.description}
        </p>
      </Show>
    </section>
  );
}

/** A row in a grouped list. Label on the left, value or control on the right,
 *  chevron when it leads somewhere.
 *
 *  The separator lives on the inner element rather than the row, so it starts
 *  at the label and leaves the icon column clear — the inset rule that makes a
 *  column of icons read as a column instead of a ragged edge. */
export function Row(props: {
  label: JSX.Element;
  helper?: JSX.Element;
  /** Icon path. Drawn as a filled rounded square, the way a settings list
   *  marks its rows. */
  icon?: string;
  /** Puts the control on its own line beneath the label, for wide controls. */
  stacked?: boolean;
  onClick?: () => void;
  /** Filtered out by the current search. The row stays mounted and simply goes
   *  `hidden`, so a half-typed key survives a search the user then clears. */
  hidden?: boolean;
  children?: JSX.Element;
}) {
  const inner = `flex-1 min-w-0 flex items-start gap-3 py-2.5 pr-4
                 border-b border-[color:var(--border-subtle)] group-last:border-b-0`;

  return (
    <div
      data-setting
      hidden={props.hidden}
      class={`group flex items-stretch pl-4 min-h-[2.75rem] ${
        props.onClick ? 'cursor-pointer hover:bg-[color:var(--bg-hover)]/50 active:bg-[color:var(--bg-hover)]/70 transition-colors' : ''
      }`}
      onClick={props.onClick ? () => props.onClick!() : undefined}
      role={props.onClick ? 'button' : undefined}
      tabindex={props.onClick ? 0 : undefined}
      onKeyDown={
        props.onClick
          ? (e) => {
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault();
                props.onClick!();
              }
            }
          : undefined
      }
    >
      <Show when={props.icon}>
        <span
          class="w-[26px] h-[26px] mt-2 mr-3 shrink-0 rounded-[7px] flex items-center justify-center
                 bg-[color:var(--accent)] text-[color:var(--on-primary)]"
        >
          <svg class="w-[15px] h-[15px]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d={props.icon} />
          </svg>
        </span>
      </Show>

      <span class={inner}>
        <span class="min-w-0 flex-1 self-center">
          <span class="block text-ui text-[color:var(--text-primary)] leading-snug">{props.label}</span>
          <Show when={props.helper}>
            <span class="block mt-1 text-meta leading-[1.5] text-[color:var(--text-tertiary)] max-w-[38rem]">
              {props.helper}
            </span>
          </Show>
          <Show when={props.stacked && props.children}>
            <span class="block mt-3">{props.children}</span>
          </Show>
        </span>
        <Show when={!props.stacked && props.children}>
          <span class="shrink-0 self-center flex items-center gap-2.5 pl-4">{props.children}</span>
        </Show>
        <Show when={props.onClick}>
          <svg
            class="w-4 h-4 shrink-0 self-center ml-2 text-[color:var(--text-muted)]"
            fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.4"
          >
            <path stroke-linecap="round" stroke-linejoin="round" d="M9 5l7 7-7 7" />
          </svg>
        </Show>
      </span>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Controls
// ---------------------------------------------------------------------------

/** A capsule switch with a raised white knob — the platform's own shape, and
 *  the one control on this page people recognise before reading its label. */
export function Switch(props: {
  checked: boolean;
  onChange: (next: boolean) => void;
  disabled?: boolean;
  label: string;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={props.checked}
      aria-label={props.label}
      disabled={props.disabled}
      onClick={() => props.onChange(!props.checked)}
      class={`relative inline-flex h-[26px] w-[42px] shrink-0 items-center rounded-full
              transition-colors disabled:opacity-50 disabled:cursor-not-allowed
        ${props.checked ? 'bg-[color:var(--accent)]' : 'bg-[color:var(--bg-hover)]'}`}
    >
      <span
        class={`pointer-events-none block w-[22px] h-[22px] rounded-full bg-white
                shadow-[0_1px_3px_rgba(0,0,0,0.35)] transition-transform
          ${props.checked ? 'translate-x-[18px]' : 'translate-x-[2px]'}`}
      />
    </button>
  );
}

/** Field chrome: square-cornered, one hairline, no fill beyond a half-step of
 *  elevation so it reads as a slot ruled into the page. */
export const fieldClass = `h-8 px-3 rounded-lg bg-[color:var(--bg-elevated)]
  border border-[color:var(--border-default)] text-ui text-[color:var(--text-primary)]
  placeholder:text-[color:var(--text-muted)] focus:outline-none focus:border-[color:var(--accent)]
  focus:ring-1 focus:ring-[color:var(--accent-ring)] transition-colors disabled:opacity-50`;

export function TextField(props: {
  value: string;
  onInput: (value: string) => void;
  onEnter?: () => void;
  placeholder?: string;
  password?: boolean;
  mono?: boolean;
  width?: string;
  disabled?: boolean;
  ariaLabel: string;
  ref?: (el: HTMLInputElement) => void;
}) {
  return (
    <input
      ref={props.ref}
      type={props.password ? 'password' : 'text'}
      value={props.value}
      disabled={props.disabled}
      placeholder={props.placeholder}
      aria-label={props.ariaLabel}
      spellcheck={false}
      onInput={(e) => props.onInput(e.currentTarget.value)}
      onKeyDown={(e) => { if (e.key === 'Enter') props.onEnter?.(); }}
      class={`${fieldClass} ${props.width ?? 'w-full'} ${props.mono ? 'font-mono text-meta' : ''}`}
    />
  );
}

/** A slider with its value set beside it. Dragging a number beats typing one
 *  when the range is small and the effect is felt rather than exact —
 *  `accent-color` paints the native control in the project's own theme. */
export function Slider(props: {
  value: number;
  min: number;
  max: number;
  step?: number;
  disabled?: boolean;
  ariaLabel: string;
  format?: (v: number) => string;
  onInput: (v: number) => void;
  onCommit: (v: number) => void;
}) {
  const shown = () => (props.format ? props.format(props.value) : String(props.value));
  return (
    <span class="flex items-center gap-4 w-full max-w-[24rem]">
      <input
        type="range"
        min={props.min}
        max={props.max}
        step={props.step ?? 1}
        value={props.value}
        disabled={props.disabled}
        aria-label={props.ariaLabel}
        onInput={(e) => props.onInput(Number(e.currentTarget.value))}
        onChange={(e) => props.onCommit(Number(e.currentTarget.value))}
        class="slider-ios flex-1 cursor-pointer"
        style={{
          // WebKit has no ::-moz-range-progress, so the filled portion is a
          // hard-stopped gradient positioned from the current value.
          'background-image': `linear-gradient(var(--accent), var(--accent))`,
          'background-size': `${((props.value - props.min) / Math.max(1, props.max - props.min)) * 100}% 4px`,
          'background-position': 'left center',
          'background-repeat': 'no-repeat',
          'accent-color': 'var(--accent)',
        }}
      />
      <span class="shrink-0 min-w-[4rem] font-mono text-meta tabular-nums text-[color:var(--text-secondary)]">
        {shown()}
      </span>
    </span>
  );
}

export function Select(props: {
  value: string;
  options: Array<{ value: string; label: string }>;
  onChange: (value: string) => void;
  disabled?: boolean;
  ariaLabel: string;
  width?: string;
}) {
  return (
    <span class="relative inline-flex">
      <select
        value={props.value}
        disabled={props.disabled}
        aria-label={props.ariaLabel}
        onChange={(e) => props.onChange(e.currentTarget.value)}
        class={`${fieldClass} ${props.width ?? 'w-[13rem]'} max-w-full appearance-none pr-7 cursor-pointer`}
      >
        {props.options.map((o) => (
          <option value={o.value}>{o.label}</option>
        ))}
      </select>
      <svg
        class="w-3.5 h-3.5 absolute right-2 top-1/2 -translate-y-1/2 pointer-events-none text-[color:var(--text-tertiary)]"
        fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2"
      >
        <path stroke-linecap="round" stroke-linejoin="round" d="M19 9l-7 7-7-7" />
      </svg>
    </span>
  );
}

type ButtonVariant = 'filled' | 'outlined' | 'text';

const VARIANTS: Record<ButtonVariant, string> = {
  filled: 'bg-[color:var(--accent)] text-[color:var(--on-primary)] hover:bg-[color:var(--accent-hover)] border border-transparent',
  outlined: 'bg-[color:var(--bg-elevated)] text-[color:var(--accent)] border border-transparent hover:brightness-125',
  // An action, not a value: tinted text, no chrome around it.
  text: 'bg-transparent text-[color:var(--accent)] border border-transparent hover:opacity-70',
};

export function Button(props: {
  variant?: ButtonVariant;
  onClick?: () => void;
  disabled?: boolean;
  title?: string;
  href?: string;
  children: JSX.Element;
}) {
  const cls = () =>
    `inline-flex items-center justify-center gap-1.5 h-8 px-3.5 rounded-lg text-meta font-medium
     transition-colors disabled:opacity-50 disabled:cursor-not-allowed whitespace-nowrap
     ${VARIANTS[props.variant ?? 'filled']}`;
  return (
    <Show
      when={!props.href}
      fallback={
        <a href={props.href} target="_blank" rel="noreferrer noopener" title={props.title} class={cls()}>
          {props.children}
        </a>
      }
    >
      <button type="button" onClick={() => props.onClick?.()} disabled={props.disabled} title={props.title} class={cls()}>
        {props.children}
      </button>
    </Show>
  );
}

export function IconButton(props: {
  onClick: () => void;
  label: string;
  path: string;
  disabled?: boolean;
  danger?: boolean;
}) {
  return (
    <button
      type="button"
      onClick={() => props.onClick()}
      disabled={props.disabled}
      title={props.label}
      aria-label={props.label}
      class={`w-6 h-6 shrink-0 rounded-[3px] flex items-center justify-center transition-colors
              disabled:opacity-40 disabled:cursor-not-allowed
        ${props.danger
          ? 'text-[color:var(--text-muted)] hover:text-[color:var(--danger)]'
          : 'text-[color:var(--text-tertiary)] hover:text-[color:var(--text-primary)]'
        }`}
    >
      <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.9">
        <path stroke-linecap="round" stroke-linejoin="round" d={props.path} />
      </svg>
    </button>
  );
}

export function LinkAction(props: { onClick?: () => void; href?: string; children: JSX.Element }) {
  const cls = 'text-meta font-medium text-[color:var(--accent)] hover:underline underline-offset-2 transition-colors';
  return (
    <Show
      when={!props.href}
      fallback={
        <a href={props.href} target="_blank" rel="noreferrer noopener" class={cls}>
          {props.children}
        </a>
      }
    >
      <button type="button" onClick={() => props.onClick?.()} class={cls}>
        {props.children}
      </button>
    </Show>
  );
}

/** A read-only fact: something the page reports but cannot change. */
export function Value(props: { children: JSX.Element; mono?: boolean; tone?: 'default' | 'muted' }) {
  return (
    <span
      class={`text-meta break-all text-right ${props.mono !== false ? 'font-mono' : ''}
        ${props.tone === 'muted' ? 'text-[color:var(--text-tertiary)]' : 'text-[color:var(--text-secondary)]'}`}
    >
      {props.children}
    </span>
  );
}

type Tone = 'ok' | 'warn' | 'danger' | 'muted' | 'accent';

const TONE_COLOR: Record<Tone, string> = {
  ok: 'var(--success)',
  warn: 'var(--warning)',
  danger: 'var(--danger)',
  muted: 'var(--text-muted)',
  accent: 'var(--accent)',
};

/** Dot plus label. No pill around it — on a printed page a state is a mark in
 *  the margin, not a badge. */
export function StatusChip(props: { tone: Tone; children: JSX.Element; pulse?: boolean }) {
  const color = () => TONE_COLOR[props.tone];
  return (
    <span
      class="inline-flex items-center gap-1.5 shrink-0 h-[22px] px-2 rounded-full text-micro font-medium"
      style={{ color: color(), background: `color-mix(in srgb, ${color()} 14%, transparent)` }}
    >
      <span
        class={`w-1.5 h-1.5 rounded-full shrink-0 ${props.pulse ? 'animate-pulse' : ''}`}
        style={{ background: color() }}
      />
      {props.children}
    </span>
  );
}

/** An aside, marked with a rule down its left edge the way a printed note is —
 *  no fill, no box. */
export function Banner(props: { tone: Tone; children: JSX.Element; action?: JSX.Element }) {
  const color = () => TONE_COLOR[props.tone];
  return (
    <div
      class="flex items-start gap-3 rounded-lg px-3 py-2 text-meta leading-[1.5]"
      style={{ color: color(), background: `color-mix(in srgb, ${color()} 12%, transparent)` }}
    >
      <span class="min-w-0 flex-1">{props.children}</span>
      <Show when={props.action}>
        <span class="shrink-0">{props.action}</span>
      </Show>
    </div>
  );
}

export function Chip(props: {
  active?: boolean;
  onClick?: () => void;
  title?: string;
  children: JSX.Element;
}) {
  return (
    <button
      type="button"
      onClick={() => props.onClick?.()}
      title={props.title}
      aria-pressed={props.active}
      class={`inline-flex items-center gap-1.5 h-7 px-3 rounded-full text-meta font-medium border transition-colors
        ${props.active
          ? 'border-[color:var(--accent)] text-[color:var(--accent)]'
          : 'border-[color:var(--border-default)] text-[color:var(--text-secondary)] hover:border-[color:var(--border-strong)] hover:text-[color:var(--text-primary)]'
        }`}
    >
      {props.children}
    </button>
  );
}

/** A printed label: outlined, not filled. */
export function Tag(props: { tone?: 'accent' | 'muted'; children: JSX.Element }) {
  return (
    <span
      class={`shrink-0 inline-flex items-center h-[17px] px-1.5 rounded-full text-micro font-medium leading-none border
        ${props.tone === 'accent'
          ? 'border-[color:var(--accent)] text-[color:var(--accent)]'
          : 'border-[color:var(--border-default)] text-[color:var(--text-muted)]'
        }`}
    >
      {props.children}
    </span>
  );
}

export function Kbd(props: { children: JSX.Element }) {
  return (
    <kbd class="inline-flex items-center justify-center min-w-[1.35rem] h-5 px-1.5 rounded-md
                border border-[color:var(--border-default)]
                font-mono text-micro text-[color:var(--text-secondary)]">
      {props.children}
    </kbd>
  );
}

export function Spinner(props: { class?: string }) {
  return (
    <svg class={`animate-spin ${props.class ?? 'w-4 h-4'}`} fill="none" viewBox="0 0 24 24">
      <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="3" />
      <path class="opacity-80" fill="currentColor" d="M4 12a8 8 0 018-8V1a11 11 0 00-11 11h3z" />
    </svg>
  );
}

export function CopyButton(props: { text: string; label?: string }) {
  const [copied, setCopied] = createSignal(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(props.text);
      setCopied(true);
      setTimeout(() => setCopied(false), 1400);
    } catch {
      // Clipboard blocked (insecure origin, denied permission) — say nothing
      // rather than claim a copy that never happened.
    }
  };
  return (
    <button
      type="button"
      onClick={copy}
      title="Copy to clipboard"
      class="inline-flex items-center gap-1 h-6 px-1 rounded-[3px] text-micro font-medium
             text-[color:var(--text-muted)] hover:text-[color:var(--text-primary)] transition-colors"
    >
      <Show
        when={copied()}
        fallback={
          <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
            <path stroke-linecap="round" stroke-linejoin="round" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z" />
          </svg>
        }
      >
        <svg class="w-3.5 h-3.5" style={{ color: 'var(--success)' }} fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.4">
          <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
        </svg>
      </Show>
      {copied() ? 'Copied' : props.label ?? 'Copy'}
    </button>
  );
}

/** Nothing to show. Set in the middle of the page with room around it, rather
 *  than dressed up in a container. */
export function EmptyState(props: { title: string; body?: JSX.Element; icon?: string; action?: JSX.Element }) {
  return (
    <div class="py-20 text-center">
      <Show when={props.icon}>
        <svg class="mx-auto mb-4 w-8 h-8 text-[color:var(--text-muted)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.3">
          <path stroke-linecap="round" stroke-linejoin="round" d={props.icon} />
        </svg>
      </Show>
      <p class="text-ui font-medium text-[color:var(--text-secondary)]">{props.title}</p>
      <Show when={props.body}>
        <p class="mt-2 text-meta leading-[1.7] text-[color:var(--text-tertiary)] max-w-[30rem] mx-auto">
          {props.body}
        </p>
      </Show>
      <Show when={props.action}>
        <div class="mt-5 flex justify-center">{props.action}</div>
      </Show>
    </div>
  );
}

/** Monospace inline fragment — a path, an env var, a tool name. Underlined
 *  rather than boxed, so a sentence keeps its line. */
export function Mono(props: { children: JSX.Element }) {
  return (
    <code class="font-mono text-[0.95em] text-[color:var(--text-secondary)]">{props.children}</code>
  );
}
