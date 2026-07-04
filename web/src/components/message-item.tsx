import { Index, Show, createEffect, createMemo, createSignal, onMount } from 'solid-js';
import type { MessageWithParts, Part, TextPartData, ToolPartData, ReasoningPartData, ImagePartData } from '../api/client';
import MarkdownContent from './markdown-content';
import FileDiff, { diffStat } from './file-diff';
import { useNote } from '../context/note';
import { useSession } from '../context/session';

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
  return null;
}

function TextPartDisplay(props: { data: TextPartData }) {
  return <MarkdownContent text={props.data.text} />;
}

function ToolPartDisplay(props: { data: ToolPartData }) {
  const [expanded, setExpanded] = createSignal(props.data.state.status === 'running');
  const status = () => props.data.state.status;
  const title = () => props.data.state.title || props.data.tool;
  const summary = () => {
    if (isDeepSearch() && props.data.state.status === 'completed') return 'Search results';
    return summarizeInput(props.data.tool, props.data.state.input);
  };
  const hasOutput = () => !!props.data.state.output;
  const outputLineCount = () => (props.data.state.output || '').split('\n').length;

  // Deep search results contain the full synthesised answer (markdown with
  // Sources section). Render them as markdown instead of a code block, and
  // auto-expand on completion so the user sees the answer immediately.
  const isDeepSearch = () => props.data.tool === 'deep_search';

  // File-editing tools render a GitHub-style before/after diff instead of raw input.
  const isFileEdit = () => props.data.tool === 'edit' || props.data.tool === 'write';
  const fileDiff = createMemo((): { oldText: string; newText: string; mode: 'create' | 'edit' | 'overwrite'; omitted: boolean } | null => {
    if (!isFileEdit()) return null;
    const input = props.data.state.input || {};
    const meta = props.data.state.metadata || {};
    if (props.data.tool === 'edit') {
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

  // Auto-collapse when tool finishes (running/completed -> completed/error)
  // Exception: deep_search auto-expands so the user sees the answer.
  createEffect(() => {
    const s = status();
    if (s === 'completed' || s === 'error') {
      if (isDeepSearch()) {
        setExpanded(true);
      } else {
        setExpanded(false);
      }
    }
  });

  const statusColor = () => {
    switch (status()) {
      case 'running':   return 'text-[color:var(--accent)]';
      case 'completed': return 'text-emerald-400';
      case 'error':     return 'text-red-400';
      default:          return 'text-zinc-500';
    }
  };

  // Detect if this tool was cancelled (error message contains cancellation text)
  const isCancelled = () => {
    return props.data.state.status === 'error' &&
      props.data.state.error &&
      props.data.state.error.toLowerCase().includes('cancel');
  };

  return (
    <div class="my-2">
      <button
        type="button"
        onClick={() => setExpanded(!expanded())}
        class="flex items-center gap-2 w-full text-left text-[12px] px-2.5 py-1.5 rounded-md
               bg-[color:var(--bg-elevated)] hover:bg-[color:var(--bg-hover)]
               border border-[color:var(--border-subtle)]
               transition-all var(--spring-sm) group"
      >
        <div class={`flex-shrink-0 ${statusColor()}`}>
          <Show when={status() === 'running'}>
            <div class="w-3.5 h-3.5 border-[1.5px] border-current border-t-transparent rounded-full animate-spin" />
          </Show>
          <Show when={status() === 'completed'}>
            <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
              <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
            </svg>
          </Show>
          <Show when={status() === 'error' && isCancelled()}>
            <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M18.364 18.364A9 9 0 005.636 5.636m12.728 12.728A9 9 0 015.636 5.636m12.728 12.728L5.636 5.636" />
            </svg>
          </Show>
          <Show when={status() === 'error' && !isCancelled()}>
            <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
              <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v2m0 4h.01M4.93 19h14.14c1.54 0 2.5-1.67 1.73-3L13.73 4.99c-.77-1.33-2.69-1.33-3.46 0L3.2 16c-.77 1.33.19 3 1.73 3z" />
            </svg>
          </Show>
          <Show when={status() === 'pending'}>
            <div class="w-3.5 h-3.5 border-[1.5px] border-current rounded-full opacity-60" />
          </Show>
        </div>
        <span class="text-zinc-300 font-medium shrink-0">{title()}</span>
        <Show when={isCancelled()}>
          <span class="text-[10px] text-amber-400/80 font-medium shrink-0">cancelled</span>
        </Show>
        <Show when={summary()}>
          <span class="text-zinc-500 font-mono text-[11px] truncate flex-1 min-w-0">
            {summary()}
          </span>
        </Show>
        <Show when={!summary()}>
          <span class="flex-1" />
        </Show>
        <Show when={diffStats()}>
          <span class="flex items-center gap-1.5 shrink-0 text-[10px] font-mono tabular-nums">
            <span style={{ color: 'var(--success)' }}>+{diffStats()!.adds}</span>
            <span style={{ color: 'var(--danger)' }}>−{diffStats()!.dels}</span>
          </span>
        </Show>
        <Show when={hasOutput() && !expanded() && !isFileEdit()}>
          <span class="text-[10px] text-zinc-600 font-mono shrink-0">
            {outputLineCount()} {outputLineCount() === 1 ? 'line' : 'lines'}
          </span>
        </Show>
        <svg
          class={`w-3 h-3 text-zinc-600 transition-transform var(--spring-sm) shrink-0 ${expanded() ? 'rotate-90' : ''}`}
          fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.2"
        >
          <path stroke-linecap="round" stroke-linejoin="round" d="M9 5l7 7-7 7" />
        </svg>
      </button>

      <Show when={expanded()}>
        <div class="mt-1.5 ml-2 space-y-1.5 min-w-0 overflow-hidden">
          <Show when={isFileEdit() && fileDiff()}>
            <FileDiff oldText={fileDiff()!.oldText} newText={fileDiff()!.newText} mode={fileDiff()!.mode} omitted={fileDiff()!.omitted} />
          </Show>
          <Show when={!isFileEdit() && props.data.state.input && Object.keys(props.data.state.input).length > 0 && !(isDeepSearch() && status() === 'completed')}>
            <CodeBlock label="input" maxHeight={160} text={safeStringify(props.data.state.input)} />
          </Show>
          <Show when={props.data.state.output && !isFileEdit()}>
            <Show when={isDeepSearch()} fallback={<CodeBlock label="output" maxHeight={280} text={props.data.state.output || ''} />}>
              <div class="rounded-md border border-[color:var(--border-subtle)] bg-[color:var(--bg-surface)] p-3 max-h-[600px] overflow-y-auto">
                <MarkdownContent text={props.data.state.output || ''} />
              </div>
            </Show>
          </Show>
          <Show when={props.data.state.error}>
            <CodeBlock label="error" maxHeight={160} text={props.data.state.error || ''} />
          </Show>
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
      <div class="flex items-center justify-between px-2.5 py-1 border-b border-[color:var(--border-subtle)] bg-[color:var(--bg-base)]/40">
        <span class="text-[10px] uppercase tracking-wider text-zinc-500 font-medium">{props.label}</span>
        <div class="flex items-center gap-1">
          <button
            type="button"
            onClick={(e) => { e.stopPropagation(); setWrap(!wrap()); }}
            title={wrap() ? 'Disable word wrap' : 'Enable word wrap'}
            class={`h-6 px-2 rounded text-[10.5px] font-medium transition flex items-center gap-1
              ${wrap()
                ? 'text-[color:var(--accent)] bg-[color:var(--accent-soft)] hover:bg-[color:var(--accent-soft)]'
                : 'text-zinc-500 hover:text-zinc-200 hover:bg-[color:var(--bg-hover)]'
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
            class={`h-6 px-2 rounded text-[10.5px] font-medium transition flex items-center gap-1
              ${copied()
                ? 'text-emerald-300 bg-emerald-500/10'
                : 'text-zinc-500 hover:text-zinc-200 hover:bg-[color:var(--bg-hover)]'
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
        class={`text-[11.5px] text-zinc-300 font-mono leading-relaxed p-2.5 overflow-y-auto
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
        class="block rounded-lg overflow-hidden border border-[color:var(--border-subtle)] transition-all var(--spring-sm)"
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
  const charCount = () => props.data.text.length;
  const isLong = () => charCount() > 500;
  const preview = () => isLong() ? props.data.text.slice(0, 300) + '…' : props.data.text;

  return (
    <div class="my-2">
      <button
        type="button"
        onClick={() => setExpanded(!expanded())}
        class="flex items-center gap-1.5 text-[12px] px-2.5 py-1 rounded-md
               text-violet-300/90 hover:text-violet-200 hover:bg-violet-500/5
               transition-colors duration-150"
      >
        <svg
          class={`w-3 h-3 transition-transform duration-200 ${expanded() ? 'rotate-90' : ''}`}
          fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.2"
        >
          <path stroke-linecap="round" stroke-linejoin="round" d="M9 5l7 7-7 7" />
        </svg>
        <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
          <path stroke-linecap="round" stroke-linejoin="round" d="M12 3v1m0 16v1m9-9h-1M4 12H3m15.364-6.364l-.707.707M6.343 17.657l-.707.707m12.728 0l-.707-.707M6.343 6.343l-.707-.707M16 12a4 4 0 11-8 0 4 4 0 018 0z" />
        </svg>
        <span class="font-medium">Thought</span>
        <Show when={isLong()}>
          <span class="text-[10px] text-violet-400/60 font-mono ml-1">
            {charCount().toLocaleString()} chars
          </span>
        </Show>
      </button>
      <Show when={expanded()}>
        <div class="ml-5 mt-1.5 pl-3 border-l-2 border-violet-500/20 text-[13px] text-zinc-400 whitespace-pre-wrap break-words italic leading-relaxed max-h-[400px] overflow-y-auto">
          {props.data.text}
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

  // Clamp height for ~4 lines of text (4 × 1.65 line-height × 15px ≈ 99px + padding)
  const CLAMP_HEIGHT = 112;

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
    <div class="flex justify-end animate-fade-in-right group">
      <div class="max-w-[85%] flex flex-col items-end min-w-0">
        <div class="relative min-w-0">
          <div
            ref={contentRef}
            classList={{
              'rounded-2xl rounded-br-sm px-4 py-2.5 border-l-2 border-l-[color:var(--accent)] border border-[color:var(--border-subtle)] min-w-0 break-words': true,
              'overflow-hidden': !expanded(),
            }}
            style={{ background: 'linear-gradient(var(--tint), var(--tint)) var(--bg-elevated)', ...(!expanded() ? { 'max-height': `${CLAMP_HEIGHT}px` } : {}) }}
          >
            <Index each={props.msg.parts}>
              {(part) => <PartDisplay part={part()} />}
            </Index>
          </div>
          {/* Bottom fade to indicate truncated content */}
          <Show when={!expanded() && overflow()}>
            <div class="absolute bottom-0 left-0 right-0 h-10 pointer-events-none rounded-b-2xl"
              style={{ background: 'linear-gradient(to top, var(--bg-elevated) 20%, transparent 100%)' }}
            />
          </Show>
        </div>
        <Show when={overflow()}>
          <button
            type="button"
            onClick={() => { setExpanded(!expanded()); requestAnimationFrame(checkOverflow); }}
            class="text-[11px] text-zinc-500 hover:text-zinc-300 mt-1 mr-1 transition"
          >
            {expanded() ? 'Show less' : 'Show more'}
          </button>
        </Show>
        <div class="flex items-center justify-end gap-2 mt-1 mr-1">
          <Show when={userText()}>
            <button
              type="button"
              onClick={handleSendToNotes}
              disabled={sendingToNote()}
              title="Save to Notes"
              class={`flex items-center gap-1.5 text-[11px] px-2 py-1 rounded-md font-medium transition-all var(--spring-sm)
                opacity-0 group-hover:opacity-100
                ${noteSaved()
                  ? 'text-emerald-400 bg-emerald-500/10 opacity-100'
                  : sendingToNote()
                  ? 'text-[color:var(--accent)] bg-[color:var(--accent-soft)] opacity-60'
                  : 'text-zinc-400 hover:text-[color:var(--accent)] hover:bg-[color:var(--accent-soft)] bg-[color:var(--bg-elevated)]'
                }`}
            >
              <Show when={noteSaved()} fallback={
                <svg class="w-3.5 h-3.5 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M19.5 14.25v-2.625a3.375 3.375 0 00-3.375-3.375h-1.5A1.125 1.125 0 0113.5 7.125v-1.5a3.375 3.375 0 00-3.375-3.375H8.25m0 12.75h7.5m-7.5 3H12M10.5 2.25H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 00-9-9z" />
                </svg>
              }>
                <svg class="w-3.5 h-3.5 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
                </svg>
              </Show>
              {noteSaved() ? 'Saved!' : sendingToNote() ? 'Saving…' : 'Save to Notes'}
            </button>
          </Show>
          <span class="text-[10px] text-zinc-600 opacity-0 group-hover:opacity-100 transition">{timestamp()}</span>
        </div>
      </div>
    </div>
  );
}

function AssistantMessage(props: { msg: MessageWithParts }) {
  const timestamp = () => formatTime(props.msg.info.createdAt);
  const [copied, setCopied] = createSignal(false);
  let copyTimer: ReturnType<typeof setTimeout>;

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
    <div class="flex gap-3 animate-fade-in-left group">
      {/* Avatar */}
      <div class="w-7 h-7 shrink-0 rounded-lg bg-[color:var(--accent)] flex items-center justify-center shadow-sm shadow-[color:var(--accent)]/15 mt-0.5">
        <svg class="w-3.5 h-3.5 text-[color:var(--on-primary)]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.4">
          <path stroke-linecap="round" stroke-linejoin="round" d="M13 10V3L4 14h7v7l9-11h-7z" />
        </svg>
      </div>

      <div class="flex-1 min-w-0">
        <div class="flex items-center gap-2 mb-1">
          <span class="text-[12px] font-semibold text-[color:var(--accent)]">ogcode</span>
          <span class="text-[10px] text-zinc-600 opacity-0 group-hover:opacity-100 transition">
            {timestamp()}
          </span>
        </div>

        <div class="space-y-1 min-w-0 overflow-hidden">
          <Index each={props.msg.parts}>
            {(part) => <PartDisplay part={part()} />}
          </Index>

          <Show when={props.msg.parts.length === 0 && props.msg.info.finish && !props.msg.info.error}>
            <div class="text-[13px] text-zinc-500 italic">No response</div>
          </Show>
        </div>

        <Show when={props.msg.info.error}>
          <div class="mt-2 text-[12px] text-red-300 bg-red-950/30 border border-red-800/40 rounded-md px-3 py-2">
            <span class="font-medium">Error:</span> {props.msg.info.error}
          </div>
        </Show>

        <Show when={props.msg.info.finish === 'aborted'}>
          <div class="mt-2 text-[12px] text-amber-300 bg-amber-950/30 border border-amber-700/40 rounded-md px-3 py-1.5 flex items-center gap-1.5">
            <svg class="w-3.5 h-3.5 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M18.364 18.364A9 9 0 005.636 5.636m12.728 12.728A9 9 0 015.636 5.636m12.728 12.728L5.636 5.636" />
            </svg>
            <span>Generation cancelled</span>
          </div>
        </Show>

        {/* Action bar (hover) */}
        <Show when={props.msg.info.finish}>
          <div class="mt-2 flex items-center gap-1 opacity-0 group-hover:opacity-100 transition-opacity var(--spring-md)">
            <button
              onClick={handleCopy}
              class={`text-[11px] px-2 py-1 rounded-md flex items-center gap-1.5 transition-all var(--spring-sm) ${
                copied()
                  ? 'text-emerald-400 bg-emerald-500/10'
                  : 'text-zinc-500 hover:text-zinc-200 hover:bg-[color:var(--bg-hover)]'
              }`}
              title="Copy response"
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
              {copied() ? 'Copied!' : 'Copy'}
            </button>
          </div>
        </Show>
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
