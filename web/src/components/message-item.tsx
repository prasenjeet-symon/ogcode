import { Index, Show, createEffect, createMemo, createSignal, onCleanup, onMount } from 'solid-js';
import type { MessageWithParts, Part, TextPartData, ToolPartData, ToolState, ReasoningPartData, ImagePartData, Interruption, InterruptReason } from '../api/client';
import MarkdownContent from './markdown-content';
import FileDiff, { diffStat } from './file-diff';
import { useNote } from '../context/note';
import { useSession } from '../context/session';
import DeliveryTicks, { formatLatency } from './delivery-ticks';

function formatTime(ts: number): string {
  const d = new Date(ts);
  const now = new Date();
  const diffMs = now.getTime() - d.getTime();
  const diffMin = Math.floor(diffMs / 60000);
  if (diffMin < 1) return 'just now';
  if (diffMin < 60) return `${diffMin}m ago`;
  const diffHr = Math.floor(diffMin / 60);
  if (diffHr < 24) return `${diffHr}h ago`;
  return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
}

// formatDuration renders an elapsed span (ms) as "1.2s" / "27s" / "1m 05s".
// `precise` (used for a finished total) keeps one decimal under a minute; the
// live-ticking form uses whole seconds.
function formatDuration(ms: number, precise: boolean): string {
  const sec = ms / 1000;
  if (sec < 60) return precise ? `${sec.toFixed(1)}s` : `${Math.floor(sec)}s`;
  const m = Math.floor(sec / 60);
  const s = Math.round(sec % 60);
  return `${m}m ${s < 10 ? '0' : ''}${s}s`;
}

function summarizeInput(tool: string, input: any): string | null {
  if (!input || typeof input !== 'object') return null;
  // Common shapes: file_path, path, command, pattern
  if (input.file_path) return String(input.file_path);
  if (input.path) return String(input.path);
  if (input.command) {
    const cmd = String(input.command);
    return cmd.length > 80 ? cmd.slice(0, 77) + '…' : cmd;
  }
  if (input.pattern) return String(input.pattern);
  if (input.query) return String(input.query);
  if (input.url) return String(input.url);
  // The skill tool's only argument. Checked last so a tool with both a name and
  // a more specific field still summarizes by the specific one.
  if (input.name) return String(input.name);
  return null;
}

function TextPartDisplay(props: { data: TextPartData }) {
  return <MarkdownContent text={props.data.text} />;
}

// Fallback used when a tool part arrives with missing/malformed state — e.g.
// from an Ollama-compatible proxy that streams incomplete tool-call deltas.
// Without this, `props.data.state` is undefined and the component throws a
// TypeError that aborts the entire SolidJS render (blank screen).
const DEFAULT_TOOL_STATE: ToolState = {
  status: 'error',
  input: {},
  error: 'Malformed tool part: missing state',
};

function ToolPartDisplay(props: { data: ToolPartData }) {
  // Defensively normalize the incoming data so downstream code never touches
  // an undefined `state`. Malformed proxies can send parts whose `data` field
  // parses to an object with no `state` property.
  const state = (): ToolState => props.data.state ?? DEFAULT_TOOL_STATE;
  const tool = (): string => props.data.tool || 'unknown';
  const [expanded, setExpanded] = createSignal(state().status === 'running');
  const status = () => state().status;
  const title = () => state().title || tool();
  const summary = () => {
    if (isDeepSearch() && state().status === 'completed') return 'Search results';
    return summarizeInput(tool(), state().input);
  };
  const hasOutput = () => !!state().output;
  const outputLineCount = () => (state().output || '').split('\n').length;

  // Deep search results contain the full synthesised answer (markdown with
  // Sources section). Render them as markdown instead of a code block, and
  // auto-expand on completion so the user sees the answer immediately.
  const isDeepSearch = () => tool() === 'deep_search';

  // File-editing tools render a GitHub-style before/after diff instead of raw input.
  const isFileEdit = () => tool() === 'edit' || tool() === 'write';

  // Agent navigation aids (codebase_map, file_map): their dense labeled
  // tree/outline output is how the agent orients itself in the codebase, not
  // something the user reads. Keep the collapsed status row, but never offer
  // disclosure into input/output.
  const isAgentNavTool = () => tool() === 'codebase_map' || tool() === 'file_map';
  const fileDiff = createMemo((): { oldText: string; newText: string; mode: 'create' | 'edit' | 'overwrite'; omitted: boolean } | null => {
    if (!isFileEdit()) return null;
    const input = state().input || {};
    const meta = state().metadata || {};
    if (tool() === 'edit') {
      return { oldText: String(input.old_string ?? ''), newText: String(input.new_string ?? ''), mode: 'edit', omitted: false };
    }
    const created = !!meta.created;
    return {
      oldText: created ? '' : String(meta.oldContent ?? ''),
      newText: String(input.content ?? ''),
      mode: created ? 'create' : 'overwrite',
      omitted: !!meta.diffOmitted,
    };
  });
  const diffStats = createMemo(() => {
    const d = fileDiff();
    if (!d || d.omitted) return null;
    return diffStat(d.oldText, d.newText);
  });

  // Auto-collapse when tool finishes (running/completed -> completed/error/denied)
  // Exception: deep_search auto-expands so the user sees the answer.
  createEffect(() => {
    const s = status();
    if (s === 'completed' || s === 'error' || s === 'denied') {
      if (isDeepSearch()) {
        setExpanded(true);
      } else {
        setExpanded(false);
      }
    }
  });

  // Total-time readout for deep_search (the one long-running tool). While it runs
  // we tick a signal every second so the elapsed time updates live; once it
  // finishes we show the exact total from the persisted start/end timestamps.
  const [nowTick, setNowTick] = createSignal(Date.now());
  createEffect(() => {
    if (isDeepSearch() && status() === 'running' && state().time?.start) {
      const id = setInterval(() => setNowTick(Date.now()), 1000);
      onCleanup(() => clearInterval(id));
    }
  });
  const elapsedMs = (): number | null => {
    const t = state().time;
    if (!t?.start) return null;
    if (t.end) return t.end - t.start;
    if (status() === 'running') return nowTick() - t.start;
    return null;
  };
  const durationLabel = (): string => {
    const ms = elapsedMs();
    if (ms == null || ms < 0) return '';
    return formatDuration(ms, status() === 'completed');
  };

  const statusColor = () => {
    switch (status()) {
      case 'running':   return 'text-[color:var(--accent)]';
      case 'completed': return 'text-emerald-400';
      case 'error':     return 'text-red-400';
      case 'denied':    return 'text-amber-400';
      default:          return 'text-zinc-500';
    }
  };

  // Detect if this tool was cancelled (error message contains cancellation text)
  const isCancelled = () => {
    const s = state();
    return s.status === 'error' &&
      s.error &&
      s.error.toLowerCase().includes('cancel');
  };

  return (
    <div class="my-1.5">
      <button
        type="button"
        disabled={isAgentNavTool()}
        aria-expanded={isAgentNavTool() ? undefined : expanded()}
        onClick={() => setExpanded(!expanded())}
        class={`flex items-center gap-2 w-full text-left text-meta h-7 px-2 rounded-md border
               transition-colors
               ${!isAgentNavTool() && expanded()
                 ? 'bg-[color:var(--bg-elevated)] border-[color:var(--border-default)]'
                 : `bg-[color:var(--bg-surface)] border-[color:var(--border-subtle)]${isAgentNavTool() ? '' : ' hover:bg-[color:var(--bg-elevated)] hover:border-[color:var(--border-default)]'}`
               }`}
      >
        <Show when={!isAgentNavTool()}>
          <svg
            class={`w-2.5 h-2.5 shrink-0 text-[color:var(--text-muted)] transition-transform duration-200 ${expanded() ? 'rotate-90' : ''}`}
            fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="3"
          >
            <path stroke-linecap="round" stroke-linejoin="round" d="M9 5l7 7-7 7" />
          </svg>
        </Show>
        <div class={`flex-shrink-0 ${statusColor()}`}>
          <Show when={status() === 'running'}>
            <div class="w-3 h-3 border-[1.5px] border-current border-t-transparent rounded-full animate-spin" />
          </Show>
          <Show when={status() === 'completed'}>
            <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="3">
              <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
            </svg>
          </Show>
          <Show when={status() === 'error' && isCancelled()}>
            <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.4">
              <path stroke-linecap="round" stroke-linejoin="round" d="M18.364 18.364A9 9 0 005.636 5.636m12.728 12.728A9 9 0 015.636 5.636m12.728 12.728L5.636 5.636" />
            </svg>
          </Show>
          <Show when={status() === 'error' && !isCancelled()}>
            <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.6">
              <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v2m0 4h.01M4.93 19h14.14c1.54 0 2.5-1.67 1.73-3L13.73 4.99c-.77-1.33-2.69-1.33-3.46 0L3.2 16c-.77 1.33.19 3 1.73 3z" />
            </svg>
          </Show>
          <Show when={status() === 'denied'}>
            <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.4">
              <circle cx="12" cy="12" r="9" stroke-linecap="round" stroke-linejoin="round" />
              <path stroke-linecap="round" stroke-linejoin="round" d="M5.64 5.64l12.72 12.72" />
            </svg>
          </Show>
          <Show when={status() === 'pending'}>
            <div class="w-3 h-3 border-[1.5px] border-current rounded-full opacity-60" />
          </Show>
        </div>
        <span
          class={`shrink-0 font-medium ${status() === 'running' ? 'sweep-text' : 'text-[color:var(--text-secondary)]'}`}
        >
          {title()}
        </span>
        <Show when={isCancelled()}>
          <span class="text-micro text-amber-400/80 font-medium shrink-0">cancelled</span>
        </Show>
        <Show when={status() === 'denied'}>
          <span class="text-micro text-amber-400 font-medium shrink-0">denied</span>
        </Show>
        <Show when={summary()} fallback={<span class="flex-1" />}>
          <span class="text-[color:var(--text-muted)] font-mono text-micro truncate flex-1 min-w-0">
            {summary()}
          </span>
        </Show>
        <Show when={diffStats()}>
          <span class="flex items-center gap-1.5 shrink-0 text-micro font-mono tabular-nums">
            <span style={{ color: 'var(--success)' }}>+{diffStats()!.adds}</span>
            <span style={{ color: 'var(--danger)' }}>−{diffStats()!.dels}</span>
          </span>
        </Show>
        <Show when={isDeepSearch() && durationLabel()}>
          <span
            class={`flex items-center gap-1 shrink-0 text-micro font-mono tabular-nums ${
              status() === 'running' ? 'text-[color:var(--accent)]' : 'text-[color:var(--text-muted)]'
            }`}
            title="Total time for this deep search"
          >
            <svg class="w-2.5 h-2.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
            {durationLabel()}
          </span>
        </Show>
        <Show when={hasOutput() && (isAgentNavTool() || !expanded()) && !isFileEdit()}>
          <span class="text-micro text-[color:var(--text-muted)] font-mono shrink-0 tabular-nums">
            {outputLineCount()} {outputLineCount() === 1 ? 'line' : 'lines'}
          </span>
        </Show>
      </button>

      <Show when={!isAgentNavTool() && expanded()}>
        {/* .reveal animates the disclosure open from zero height without a
            magic max-height, so long outputs and one-liners open alike. */}
        <div class="reveal">
          <div>
            <div class="mt-1.5 ml-[1.15rem] space-y-1.5 min-w-0 overflow-hidden">
              <Show when={isFileEdit() && fileDiff()}>
                <FileDiff oldText={fileDiff()!.oldText} newText={fileDiff()!.newText} mode={fileDiff()!.mode} omitted={fileDiff()!.omitted} />
              </Show>
              <Show when={!isFileEdit() && state().input && Object.keys(state().input).length > 0 && !(isDeepSearch() && status() === 'completed')}>
                <CodeBlock label="input" maxHeight={160} text={safeStringify(state().input)} />
              </Show>
              <Show when={state().output && !isFileEdit()}>
                <Show when={isDeepSearch()} fallback={<CodeBlock label="output" maxHeight={280} text={state().output || ''} />}>
                  <div class="rounded-md border border-[color:var(--border-subtle)] bg-[color:var(--bg-surface)] p-3 max-h-[600px] overflow-y-auto">
                    <MarkdownContent text={state().output || ''} />
                  </div>
                </Show>
              </Show>
              <Show when={state().error}>
                <CodeBlock label="error" maxHeight={160} text={state().error || ''} />
              </Show>
            </div>
          </div>
        </div>
      </Show>
    </div>
  );
}

function CodeBlock(props: { label: string; text: string; maxHeight: number }) {
  const [wrap, setWrap] = createSignal(true);
  const [copied, setCopied] = createSignal(false);
  let copyTimer: ReturnType<typeof setTimeout>;

  const handleCopy = (e: MouseEvent) => {
    e.stopPropagation();
    if (!props.text) return;
    navigator.clipboard.writeText(props.text).then(() => {
      setCopied(true);
      clearTimeout(copyTimer);
      copyTimer = setTimeout(() => setCopied(false), 1500);
    }).catch(() => {});
  };

  return (
    <div class="relative group/code rounded-md border border-[color:var(--border-subtle)] bg-[color:var(--bg-surface)] overflow-hidden w-full min-w-0">
      <div class="flex items-center justify-between px-2 py-0.5 border-b border-[color:var(--border-subtle)] bg-[color:var(--bg-base)]/40">
        <span class="text-micro uppercase tracking-[0.08em] text-[color:var(--text-muted)] font-medium">{props.label}</span>
        <div class="flex items-center gap-1">
          <button
            type="button"
            onClick={(e) => { e.stopPropagation(); setWrap(!wrap()); }}
            title={wrap() ? 'Disable word wrap' : 'Enable word wrap'}
            class={`h-6 px-1.5 rounded text-micro font-medium transition flex items-center gap-1
              ${wrap()
                ? 'text-[color:var(--accent)] bg-[color:var(--accent-soft)] hover:bg-[color:var(--accent-soft)]'
                : 'text-[color:var(--text-tertiary)] hover:text-[color:var(--text-primary)] hover:bg-[color:var(--bg-hover)]'
              }`}
          >
            <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M3 6h18M3 12h12a3 3 0 010 6h-3m0 0l3-3m-3 3l3 3M3 18h6" />
            </svg>
            {wrap() ? 'Wrapped' : 'Wrap'}
          </button>
          <button
            type="button"
            onClick={handleCopy}
            title="Copy"
            class={`h-6 px-1.5 rounded text-micro font-medium transition flex items-center gap-1
              ${copied()
                ? 'text-emerald-300 bg-emerald-500/10'
                : 'text-[color:var(--text-tertiary)] hover:text-[color:var(--text-primary)] hover:bg-[color:var(--bg-hover)]'
              }`}
          >
            <Show
              when={copied()}
              fallback={
                <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 002 2h2a2 2 0 002-2M9 5a2 2 0 012-2h2a2 2 0 012 2" />
                </svg>
              }
            >
              <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
                <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
              </svg>
            </Show>
            {copied() ? 'Copied' : 'Copy'}
          </button>
        </div>
      </div>
      <pre
        class={`text-micro text-[color:var(--text-secondary)] font-mono leading-[1.6] p-2.5 overflow-y-auto
          ${wrap() ? 'whitespace-pre-wrap break-words overflow-x-hidden' : 'whitespace-pre overflow-x-auto'}`}
        style={{ 'max-height': `${props.maxHeight}px` }}
      >
        {props.text}
      </pre>
    </div>
  );
}

function ImagePartDisplay(props: { data: ImagePartData }) {
  const [expanded, setExpanded] = createSignal(false);
  const src = () => `data:${props.data.mediaType};base64,${props.data.data}`;
  const name = () => props.data.name || 'image';

  return (
    <div class="my-2">
      <button
        type="button"
        onClick={() => setExpanded(!expanded())}
        class="block rounded-lg overflow-hidden border border-[color:var(--border-subtle)] transition-all"
      >
        <img
          src={src()}
          alt={name()}
          class={`object-contain bg-black/20 ${expanded() ? 'max-h-[500px] w-auto' : 'h-24 w-auto max-w-full'}`}
        />
      </button>
    </div>
  );
}

function ReasoningPartDisplay(props: { data: ReasoningPartData }) {
  const [expanded, setExpanded] = createSignal(false);
  // A block can legitimately carry no text: safety-redacted reasoning, or a
  // model whose thinking display is withheld. There is nothing to reveal, so
  // the row says so rather than offering a drawer that opens on nothing.
  const text = () => props.data.text ?? '';
  const hasText = () => text().length > 0;
  const charCount = () => text().length;
  const isLong = () => charCount() > 500;

  return (
    <div class="my-1.5">
      <button
        type="button"
        disabled={!hasText()}
        aria-expanded={hasText() ? expanded() : undefined}
        onClick={() => hasText() && setExpanded(!expanded())}
        class="reasoning-toggle flex items-center gap-1.5 text-meta h-7 px-2 rounded-md transition-colors"
      >
        <Show when={hasText()}>
          <svg
            class={`w-2.5 h-2.5 transition-transform duration-200 ${expanded() ? 'rotate-90' : ''}`}
            fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="3"
          >
            <path stroke-linecap="round" stroke-linejoin="round" d="M9 5l7 7-7 7" />
          </svg>
        </Show>
        <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
          <path stroke-linecap="round" stroke-linejoin="round" d="M12 3v1m0 16v1m9-9h-1M4 12H3m15.364-6.364l-.707.707M6.343 17.657l-.707.707m12.728 0l-.707-.707M6.343 6.343l-.707-.707M16 12a4 4 0 11-8 0 4 4 0 018 0z" />
        </svg>
        <span class="font-medium">Thought</span>
        <Show when={isLong()}>
          <span class="reasoning-count text-micro font-mono tabular-nums ml-0.5">
            {charCount().toLocaleString()} chars
          </span>
        </Show>
        <Show when={!hasText()}>
          <span class="reasoning-count text-micro ml-0.5">not shown</span>
        </Show>
      </button>
      <Show when={expanded() && hasText()}>
        <div class="reveal">
          <div>
            <div class="reasoning-body ml-[1.15rem] mt-1.5 pl-3 text-meta text-[color:var(--text-tertiary)] whitespace-pre-wrap break-words leading-[1.65] max-h-[400px] overflow-y-auto">
              {text()}
            </div>
          </div>
        </div>
      </Show>
    </div>
  );
}

function parsePartData<T>(raw: unknown): T {
  if (typeof raw === 'string') {
    try { return JSON.parse(raw) as T; } catch { return {} as T; }
  }
  return (raw ?? {}) as T;
}

const MAX_STRINGIFY_LEN = 10_000;

function safeStringify(obj: any): string {
  try {
    const s = JSON.stringify(obj, null, 2);
    return s.length > MAX_STRINGIFY_LEN ? s.slice(0, MAX_STRINGIFY_LEN) + '\n… (truncated)' : s;
  } catch {
    return '[unable to display]';
  }
}

function PartDisplay(props: { part: Part }) {
  return (
    <>
      <Show when={props.part.type === 'text'}>
        <TextPartDisplay data={parsePartData<TextPartData>(props.part.data)} />
      </Show>
      <Show when={props.part.type === 'image'}>
        <ImagePartDisplay data={parsePartData<ImagePartData>(props.part.data)} />
      </Show>
      <Show when={props.part.type === 'tool'}>
        <ToolPartDisplay data={parsePartData<ToolPartData>(props.part.data)} />
      </Show>
      <Show when={props.part.type === 'reasoning'}>
        <ReasoningPartDisplay data={parsePartData<ReasoningPartData>(props.part.data)} />
      </Show>
    </>
  );
}

function UserMessage(props: { msg: MessageWithParts }) {
  const timestamp = () => formatTime(props.msg.info.createdAt);
  const [expanded, setExpanded] = createSignal(false);
  const [overflow, setOverflow] = createSignal(false);
  const [sendingToNote, setSendingToNote] = createSignal(false);
  const [noteSaved, setNoteSaved] = createSignal(false);
  const [copied, setCopied] = createSignal(false);
  let copyTimer: ReturnType<typeof setTimeout>;
  const noteCtx = useNote();
  const sessionCtx = useSession();
  let contentRef: HTMLDivElement | undefined;

  const userText = () => {
    for (const p of props.msg.parts) {
      if (p.type === 'text') {
        const d = parsePartData<TextPartData>(p.data);
        if (d.text) return d.text;
      }
    }
    return '';
  };

  const handleSendToNotes = async (e: MouseEvent) => {
    e.stopPropagation();
    const text = userText();
    if (!text || sendingToNote()) return;
    setSendingToNote(true);
    try {
      const model = sessionCtx.selectedModel();
      const sessionId = sessionCtx.activeSession()?.id;
      await noteCtx.createNote(text, model, sessionId, window.innerWidth, window.innerHeight);
      setNoteSaved(true);
      setTimeout(() => setNoteSaved(false), 2000);
    } catch (err) {
      console.error('send to notes failed:', err);
    } finally {
      setSendingToNote(false);
    }
  };

  const handleCopy = (e: MouseEvent) => {
    e.stopPropagation();
    const text = userText();
    if (!text) return;
    navigator.clipboard.writeText(text).then(() => {
      setCopied(true);
      clearTimeout(copyTimer);
      copyTimer = setTimeout(() => setCopied(false), 2000);
    }).catch(() => {});
  };

  // Clamp height for ~5 lines of chat text (5 × 1.65 line-height × 15px ≈ 124px
  // + vertical padding). Long pastes collapse; ordinary prompts never do.
  const CLAMP_HEIGHT = 140;

  function checkOverflow() {
    if (!contentRef) return;
    const el = contentRef;
    // When expanded, compare against a tall sentinel. When collapsed, check if content exceeds clamp.
    const limit = expanded() ? 9999 : CLAMP_HEIGHT;
    setOverflow(el.scrollHeight > limit + 1);
  }

  onMount(() => {
    // Defer so the DOM layout settles before measuring.
    requestAnimationFrame(() => {
      requestAnimationFrame(checkOverflow);
    });
  });

  return (
    <div data-role="user" class="flex justify-end animate-msg-in group">
      <div class="max-w-[82%] flex flex-col items-end min-w-0">
        <div class="relative min-w-0 max-w-full">
          <div
            ref={contentRef}
            classList={{
              // border-default rather than -subtle: with a neutral custom accent
              // the tint alone is nearly invisible, and the bubble still has to
              // read as a bubble.
              'rounded-2xl px-3.5 py-2 border border-[color:var(--border-default)] min-w-0 max-w-full break-words': true,
              'overflow-hidden': !expanded(),
            }}
            style={{
              // The accent tint is layered over the elevated surface rather than
              // painted as a stripe, so a re-themed accent recolours the whole
              // bubble instead of leaving a mismatched edge.
              background: 'linear-gradient(var(--accent-soft), var(--accent-soft)) var(--bg-elevated)',
              ...(!expanded() ? { 'max-height': `${CLAMP_HEIGHT}px` } : {}),
            }}
          >
            <Index each={props.msg.parts}>
              {(part) => <PartDisplay part={part()} />}
            </Index>
          </div>
          {/* Bottom fade to indicate truncated content */}
          <Show when={!expanded() && overflow()}>
            <div class="absolute bottom-0 left-0 right-0 h-9 pointer-events-none rounded-b-2xl"
              style={{ background: 'linear-gradient(to top, var(--bg-elevated) 15%, transparent 100%)' }}
            />
          </Show>
        </div>
        <Show when={overflow()}>
          <button
            type="button"
            onClick={() => { setExpanded(!expanded()); requestAnimationFrame(checkOverflow); }}
            class="text-micro text-[color:var(--text-tertiary)] hover:text-[color:var(--text-secondary)] mt-1 mr-1 transition-colors"
          >
            {expanded() ? 'Show less' : 'Show more'}
          </button>
        </Show>

        {/* Receipt line. The delivery state and the time stay visible — a
            receipt you have to hover to read is not a receipt — while the
            actions beside them still appear only on hover, so a turn is never
            framed by a row of buttons competing with the message itself. */}
        <div class="flex items-center justify-end gap-0.5 mt-0.5 -mr-1 h-7">
          <div class="flex items-center gap-0.5
                      opacity-0 group-hover:opacity-100 focus-within:opacity-100 transition-opacity duration-200">
          <Show when={userText()}>
            <button
              type="button"
              onClick={handleCopy}
              title={copied() ? 'Copied' : 'Copy message'}
              aria-label="Copy message"
              class="icon-btn"
              classList={{ 'text-emerald-400': copied() }}
            >
              <Show when={copied()} fallback={
                <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z" />
                </svg>
              }>
                <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.4">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
                </svg>
              </Show>
            </button>
          </Show>
          <Show when={userText()}>
            <button
              type="button"
              onClick={handleSendToNotes}
              disabled={sendingToNote()}
              title={noteSaved() ? 'Saved to Notes' : 'Save to Notes'}
              aria-label="Save to Notes"
              class="icon-btn"
              classList={{ 'text-emerald-400': noteSaved(), 'opacity-60': sendingToNote() }}
            >
              <Show when={noteSaved()} fallback={
                <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M19.5 14.25v-2.625a3.375 3.375 0 00-3.375-3.375h-1.5A1.125 1.125 0 0113.5 7.125v-1.5a3.375 3.375 0 00-3.375-3.375H8.25m0 12.75h7.5m-7.5 3H12M10.5 2.25H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 00-9-9z" />
                </svg>
              }>
                <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.4">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
                </svg>
              </Show>
            </button>
          </Show>
          </div>
          <span class="text-micro text-[color:var(--text-muted)] tabular-nums ml-1 mr-1">{timestamp()}</span>
          <DeliveryTicks msg={props.msg} />
        </div>
      </div>
    </div>
  );
}

function AssistantMessage(props: { msg: MessageWithParts }) {
  const timestamp = () => formatTime(props.msg.info.createdAt);
  const [copied, setCopied] = createSignal(false);

  // The visible figure is the model's latency alone. Everything that explains a
  // slow one — ogcode's prompt building and compaction, a stream that had to be
  // reopened, whether the model thought before it wrote — lives in the tooltip,
  // because those are questions asked after the fact.
  const ttftTitle = () => {
    const d = props.msg.info.delivery;
    if (!d?.ttftMs) return '';
    const parts = [`Model answered in ${formatLatency(d.ttftMs)}`];
    if (d.firstTokenKind && d.firstTokenKind !== 'text') parts.push(`starting with ${d.firstTokenKind}`);
    if (d.queuedMs) parts.push(`${formatLatency(d.queuedMs)} spent before the request left`);
    if (d.attempts && d.attempts > 1) parts.push(`${d.attempts} connection attempts`);
    return parts.join(' · ');
  };
  let copyTimer: ReturnType<typeof setTimeout>;

  // A turn with no finish reason and no error is still being written. When its
  // trailing part is text we mark the container so a caret rides the end of the
  // prose — the one signal that separates "still writing" from "answered in one
  // short line", which a spinner somewhere else on screen cannot give.
  const isStreaming = () => !props.msg.info.finish && !props.msg.info.error;
  const streamingText = () => {
    if (!isStreaming()) return false;
    const parts = props.msg.parts;
    return parts.length > 0 && parts[parts.length - 1].type === 'text';
  };

  // Most assistant turns in a long run are a single tool call with no prose.
  // Reserving the hover action bar under those added ~28px of dead space per
  // turn — the main reason a run of tool calls looked so airy — and offered a
  // Copy button with nothing to copy.
  const hasText = () => props.msg.parts.some(
    (p) => p.type === 'text' && (parsePartData<TextPartData>(p.data).text || '').trim().length > 0
  );

  const handleCopy = () => {
    const text = props.msg.parts
      .filter((p) => p.type === 'text')
      .map((p) => parsePartData<TextPartData>(p.data).text || '')
      .join('\n\n');
    if (!text) return;
    navigator.clipboard.writeText(text).then(() => {
      setCopied(true);
      clearTimeout(copyTimer);
      copyTimer = setTimeout(() => setCopied(false), 2000);
    }).catch(() => {});
  };

  return (
    // The assistant answer runs the full width of the reading column with no
    // avatar or name plate. Repeating a badge on every turn costs a line of
    // vertical space and ~2rem of horizontal space per message and tells the
    // reader nothing they cannot see from the alignment of the user's bubble.
    <div data-role="assistant" class="animate-msg-in group">
      <div class="min-w-0">
        <div class="space-y-1 min-w-0 overflow-hidden" classList={{ 'msg-streaming': streamingText() }}>
          <Index each={props.msg.parts}>
            {(part) => <PartDisplay part={part()} />}
          </Index>

          <Show when={props.msg.parts.length === 0 && props.msg.info.finish && !props.msg.info.error}>
            <div class="text-ui text-[color:var(--text-tertiary)] italic">No response</div>
          </Show>
        </div>

        <Show when={props.msg.info.error}>
          <div class="mt-2 text-meta text-red-300 bg-red-950/30 border border-red-800/40 rounded-md px-3 py-2">
            <span class="font-medium">Error:</span> {props.msg.info.error}
          </div>
        </Show>

        <Show when={props.msg.info.interrupted}>
          {(interrupted) => <ResumeBanner interruption={interrupted()} />}
        </Show>

        {/* The model hit its output ceiling mid-answer. The loop treats this as
            a terminal state and stops, which without a notice reads as the
            answer simply ending — the reply is truncated, not complete. */}
        <Show when={props.msg.info.finish === 'length' || props.msg.info.finish === 'max_tokens'}>
          <div class="mt-2 text-meta text-amber-300 bg-amber-950/30 border border-amber-700/40 rounded-md px-3 py-1.5 flex items-center gap-1.5">
            <svg class="w-3.5 h-3.5 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z" />
            </svg>
            <span>Response truncated — the model hit its output token limit. Ask it to continue.</span>
          </div>
        </Show>

        <Show when={props.msg.info.finish === 'aborted'}>
          <div class="mt-2 text-meta text-amber-300 bg-amber-950/30 border border-amber-700/40 rounded-md px-3 py-1.5 flex items-center gap-1.5">
            <svg class="w-3.5 h-3.5 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M18.364 18.364A9 9 0 005.636 5.636m12.728 12.728A9 9 0 015.636 5.636m12.728 12.728L5.636 5.636" />
            </svg>
            <span>Generation cancelled</span>
          </div>
        </Show>

        {/* Footer. The latency reading stays visible — it is the answer to a
            question the reader has already asked by the time the turn lands —
            while the buttons beside it still fade in on hover. */}
        <Show when={props.msg.info.finish && hasText()}>
          <div class="mt-1 flex items-center gap-0.5 -ml-1.5 h-7">
            {/* Time to first token: how long the model took to start answering,
                measured from when the request left for the provider. Rendered
                only once the turn has finished, so the figure never ticks.
                Leads the row so it never sits behind the hover-only copy
                button's reserved width. */}
            <Show when={props.msg.info.delivery?.ttftMs}>
              {(ttft) => (
                <span
                  class="text-micro text-[color:var(--text-tertiary)] tabular-nums -ml-1.5"
                  title={ttftTitle()}
                >
                  {formatLatency(ttft())} to first token
                </span>
              )}
            </Show>
            <div class="flex items-center gap-0.5
                        opacity-0 group-hover:opacity-100 focus-within:opacity-100 transition-opacity duration-200">
            <button
              type="button"
              onClick={handleCopy}
              class="icon-btn"
              classList={{ 'text-emerald-400': copied() }}
              title={copied() ? 'Copied' : 'Copy response'}
              aria-label="Copy response"
            >
              <Show
                when={copied()}
                fallback={
                  <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 002 2h2a2 2 0 002-2M9 5a2 2 0 012-2h2a2 2 0 012 2" />
                  </svg>
                }
              >
                <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.4">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
                </svg>
              </Show>
            </button>
            <span class="text-micro text-[color:var(--text-muted)] tabular-nums ml-1">{timestamp()}</span>
            </div>
          </div>
        </Show>
      </div>
    </div>
  );
}

// INTERRUPT_LABEL names each failure in the words a user would use, so the
// banner leads with what happened rather than with a status code.
const INTERRUPT_LABEL: Record<InterruptReason, string> = {
  rate_limit: 'Rate limit or quota reached',
  server_error: 'The provider failed',
  network: 'The connection dropped',
  auth: 'The provider rejected the credentials',
  context: 'The conversation outgrew the context window',
  crashed: 'The server stopped mid-turn',
  stalled: 'This turn never finished',
  fatal: 'The request was rejected',
};

// formatWait renders the gap to a unix second as a short phrase, or '' once the
// moment has passed — at which point saying "resume in -3s" would be worse than
// saying nothing.
function formatWait(retryAfter?: number): string {
  if (!retryAfter) return '';
  const sec = Math.round(retryAfter - Date.now() / 1000);
  if (sec <= 0) return '';
  if (sec < 60) return `${sec}s`;
  return `${Math.ceil(sec / 60)}m`;
}

// ResumeBanner offers to pick a broken turn back up.
//
// It is shown for every interruption, not only the resumable ones. A turn that
// cannot be resumed still needs to say so — otherwise the user is left with a
// bare error and no idea whether waiting would have helped, which is the exact
// confusion this feature exists to remove.
function ResumeBanner(props: { interruption: Interruption }) {
  const session = useSession();
  const [busy, setBusy] = createSignal(false);
  const [note, setNote] = createSignal('');
  // Ticks once a second so a countdown does not sit frozen at its initial value.
  const [now, setNow] = createSignal(Date.now());
  const timer = setInterval(() => setNow(Date.now()), 1000);
  onCleanup(() => clearInterval(timer));

  const wait = () => {
    now();
    return formatWait(props.interruption.retryAfter);
  };

  async function onResume() {
    setBusy(true);
    setNote('');
    const result = await session.resume();
    if (!result.resumed) setNote(result.message || 'Nothing to resume.');
    setBusy(false);
  }

  return (
    <div class="mt-2 text-meta text-amber-200 bg-amber-950/30 border border-amber-700/40 rounded-md px-3 py-2">
      <div class="flex items-start gap-2">
        <svg class="w-3.5 h-3.5 shrink-0 mt-0.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
          <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v3.75m9-.75a9 9 0 11-18 0 9 9 0 0118 0zm-9 3.75h.008v.008H12v-.008z" />
        </svg>
        <div class="min-w-0 flex-1">
          <div class="font-medium">{INTERRUPT_LABEL[props.interruption.reason] || 'The turn was interrupted'}</div>
          <Show when={props.interruption.detail}>
            <div class="mt-0.5 text-amber-200/70">{props.interruption.detail}</div>
          </Show>
          <Show when={props.interruption.resumable}>
            <div class="mt-2 flex items-center gap-2">
              <button
                type="button"
                class="px-2 py-1 rounded border border-amber-600/50 bg-amber-900/30 hover:bg-amber-900/50 disabled:opacity-50 disabled:cursor-not-allowed"
                disabled={busy()}
                onClick={onResume}
              >
                {busy() ? 'Resuming…' : 'Resume'}
              </button>
              <Show when={wait()}>
                <span class="text-amber-200/60">provider asked to wait {wait()}</span>
              </Show>
            </div>
          </Show>
          <Show when={note()}>
            <div class="mt-1.5 text-amber-200/70">{note()}</div>
          </Show>
        </div>
      </div>
    </div>
  );
}

export default function MessageItem(props: { msg: MessageWithParts }) {
  const isUser = () => props.msg.info.role === 'user';

  return (
    <Show when={isUser()} fallback={<AssistantMessage msg={props.msg} />}>
      <UserMessage msg={props.msg} />
    </Show>
  );
}
