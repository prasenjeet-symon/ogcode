import { createSignal, Show, For, onCleanup, createEffect } from 'solid-js';
import { createStore } from 'solid-js/store';
import DOMPurify from 'dompurify';
import { marked } from 'marked';
import { transformText as apiTransformText } from '../api/client';

// ── Commands ──────────────────────────────────────────────────────────────────

interface Command {
  id: string;
  label: string;
  description: string;
  textIcon: string;
  keywords: string[];
  insert: string;
  cursorOffset?: number;
  special?: 'image';
}

const COMMANDS: Command[] = [
  { id: 'h1',       label: 'Heading 1',     description: 'Big section heading',    textIcon: 'H1',  keywords: ['h1','heading1','heading','title'],          insert: '# ' },
  { id: 'h2',       label: 'Heading 2',     description: 'Medium section heading', textIcon: 'H2',  keywords: ['h2','heading2','heading'],                  insert: '## ' },
  { id: 'h3',       label: 'Heading 3',     description: 'Small section heading',  textIcon: 'H3',  keywords: ['h3','heading3','heading'],                  insert: '### ' },
  { id: 'bullet',   label: 'Bullet List',   description: 'Unordered list item',    textIcon: '•',   keywords: ['bullet','list','ul','unordered'],           insert: '- ' },
  { id: 'numbered', label: 'Numbered List', description: 'Ordered list item',      textIcon: '1.',  keywords: ['numbered','number','list','ol','ordered'],  insert: '1. ' },
  { id: 'todo',     label: 'To-do',         description: 'Checkbox task item',     textIcon: '☐',   keywords: ['todo','task','check','checkbox'],           insert: '- [ ] ' },
  { id: 'code',     label: 'Code Block',    description: 'Fenced code block',      textIcon: '</>',  keywords: ['code','block','snippet','fence'],          insert: '```\n\n```', cursorOffset: 4 },
  { id: 'quote',    label: 'Blockquote',    description: 'Indented quote text',    textIcon: '"',   keywords: ['quote','blockquote','callout'],             insert: '> ' },
  { id: 'divider',  label: 'Divider',       description: 'Horizontal separator',   textIcon: '—',   keywords: ['divider','hr','rule','separator','line'],   insert: '---' },
  { id: 'image',    label: 'Image',         description: 'Upload an image',        textIcon: '🖼',   keywords: ['image','img','photo','picture','upload'],   insert: '', special: 'image' },
  { id: 'bold',     label: 'Bold',          description: 'Bold text',              textIcon: 'B',   keywords: ['bold','strong','b'],                       insert: '**bold**', cursorOffset: 2 },
  { id: 'italic',   label: 'Italic',        description: 'Italic text',            textIcon: 'I',   keywords: ['italic','em','i'],                         insert: '*italic*', cursorOffset: 1 },
  { id: 'link',     label: 'Link',          description: 'Hyperlink',              textIcon: '↗',   keywords: ['link','url','href'],                       insert: '[text](url)', cursorOffset: 4 },
];

// ── Block model ───────────────────────────────────────────────────────────────

type BlockType = 'h1' | 'h2' | 'h3' | 'bullet' | 'numbered' | 'todo' | 'code' | 'quote' | 'divider' | 'image' | 'empty' | 'paragraph';

interface Block { id: string; raw: string; }

interface SlashMenu { slashPos: number; filter: string; selectedIdx: number; }

interface SelState { text: string; top: number; left: number; startBlockId: string | null; endBlockId: string | null; }

let _ctr = 0;
function uid(): string { return `b${++_ctr}`; }

function blockType(raw: string): BlockType {
  if (raw.startsWith('# '))                              return 'h1';
  if (raw.startsWith('## '))                             return 'h2';
  if (raw.startsWith('### '))                            return 'h3';
  if (/^[-*+] \[[ xX]\] /.test(raw))                    return 'todo';
  if (/^[-*+] /.test(raw))                               return 'bullet';
  if (/^\d+\. /.test(raw))                               return 'numbered';
  if (raw.startsWith('> '))                              return 'quote';
  if (/^(-{3,}|\*{3,}|_{3,})$/.test(raw.trim()))        return 'divider';
  if (raw.startsWith('```'))                             return 'code';
  if (/^!\[[^\]]*\]\(data:image\//.test(raw))            return 'image';
  if (raw.trim() === '')                                 return 'empty';
  return 'paragraph';
}

function parse(md: string): Block[] {
  if (!md.trim()) return [{ id: uid(), raw: '' }];
  const lines = md.split('\n');
  const out: Block[] = [];
  let i = 0;
  while (i < lines.length) {
    if (lines[i].startsWith('```')) {
      const chunk = [lines[i++]];
      while (i < lines.length) { chunk.push(lines[i]); if (lines[i++].startsWith('```') && chunk.length > 1) break; }
      out.push({ id: uid(), raw: chunk.join('\n') });
    } else { out.push({ id: uid(), raw: lines[i++] }); }
  }
  return out.length ? out : [{ id: uid(), raw: '' }];
}

function serialize(blocks: Block[]): string { return blocks.map(b => b.raw).join('\n'); }
function safeInline(text: string): string { return DOMPurify.sanitize(marked.parseInline(text) as string); }

function stripPrefix(raw: string, type: BlockType): string {
  switch (type) {
    case 'h1':       return raw.slice(2);
    case 'h2':       return raw.slice(3);
    case 'h3':       return raw.slice(4);
    case 'bullet':   return raw.replace(/^[-*+] /, '');
    case 'numbered': return raw.replace(/^\d+\. /, '');
    case 'todo':     return raw.replace(/^[-*+] \[[ xX]\] /, '');
    case 'quote':    return raw.replace(/^> /, '');
    default:         return raw;
  }
}

function getPrefix(raw: string, type: BlockType): string {
  switch (type) {
    case 'h1':       return '# ';
    case 'h2':       return '## ';
    case 'h3':       return '### ';
    case 'bullet':   return raw.match(/^([-*+] )/)?.[1] ?? '- ';
    case 'numbered': return raw.match(/^(\d+\. )/)?.[1] ?? '1. ';
    case 'todo':     return raw.match(/^([-*+] \[[ xX]\] )/)?.[1] ?? '- [ ] ';
    case 'quote':    return '> ';
    default:         return '';
  }
}

function toBase64(file: File): Promise<string> {
  return new Promise((res, rej) => { const r = new FileReader(); r.onload = () => res(r.result as string); r.onerror = rej; r.readAsDataURL(file); });
}

const MAX_IMG = 5 * 1024 * 1024;

// ── Props ─────────────────────────────────────────────────────────────────────

interface NoteEditorProps {
  content: string;
  onChange: (content: string) => void;
  placeholder?: string;
  autofocus?: boolean;
  model?: string;
  provider?: string;
}

// ── Grip icon ─────────────────────────────────────────────────────────────────

function GripIcon() {
  return (
    <svg viewBox="0 0 16 16" fill="currentColor" class="w-3.5 h-3.5">
      <circle cx="5"  cy="3.5"  r="1.3"/><circle cx="11" cy="3.5"  r="1.3"/>
      <circle cx="5"  cy="8"    r="1.3"/><circle cx="11" cy="8"    r="1.3"/>
      <circle cx="5"  cy="12.5" r="1.3"/><circle cx="11" cy="12.5" r="1.3"/>
    </svg>
  );
}

// ── Component ─────────────────────────────────────────────────────────────────

export default function NoteEditor(props: NoteEditorProps) {
  const [blocks, setBlocks] = createStore<Block[]>(parse(props.content));
  const [activeId,    setActiveId]    = createSignal<string | null>(null);
  const [slashMenu,   setSlashMenu]   = createSignal<SlashMenu | null>(null);
  const [palPos,      setPalPos]      = createSignal({ top: 0, left: 0 });
  const [fileDragOver,setFileDragOver]= createSignal(false);
  // block drag-reorder state
  const [dragSrcId,   setDragSrcId]   = createSignal<string | null>(null);
  const [dragOverId,  setDragOverId]  = createSignal<string | null>(null);
  const [dragOverHalf,setDragOverHalf]= createSignal<'top'|'bottom'>('top');
  // AI selection toolbar state
  const [selState,        setSelState]        = createSignal<SelState | null>(null);
  const [transformResult, setTransformResult] = createSignal<string | null>(null);
  const [transformLoading,setTransformLoading]= createSignal(false);

  let paletteRef: HTMLDivElement | undefined;
  let listRef: HTMLDivElement | undefined;
  let editorRef: HTMLDivElement | undefined;
  const taRefs = new Map<string, HTMLTextAreaElement>();

  createEffect(() => {
    const m = slashMenu();
    if (!m || !listRef) return;
    listRef.querySelector<HTMLElement>(`[data-idx="${m.selectedIdx}"]`)?.scrollIntoView({ block: 'nearest' });
  });

  function notify() { props.onChange(serialize(blocks)); }
  function resize(ta: HTMLTextAreaElement) { ta.style.height = 'auto'; ta.style.height = ta.scrollHeight + 'px'; }
  function idxOf(id: string) { return blocks.findIndex(b => b.id === id); }

  function focusBlock(id: string, pos: 'start' | 'end' | number = 'end') {
    setActiveId(id); setSlashMenu(null);
    requestAnimationFrame(() => {
      const ta = taRefs.get(id);
      if (!ta) return;
      resize(ta); ta.focus();
      const p = pos === 'end' ? ta.value.length : pos === 'start' ? 0 : (pos as number);
      ta.selectionStart = p; ta.selectionEnd = p;
    });
  }

  // ── Block mutations ───────────────────────────────────────────────────────

  function updateRaw(id: string, raw: string) { const i = idxOf(id); if (i !== -1) setBlocks(i, 'raw', raw); }

  function insertAfter(idx: number, raw = ''): string {
    const b: Block = { id: uid(), raw };
    setBlocks(bs => [...bs.slice(0, idx + 1), b, ...bs.slice(idx + 1)]);
    return b.id;
  }

  function deleteBlock(id: string): string | null {
    const i = idxOf(id);
    if (i === -1) return null;
    if (blocks.length <= 1) { setBlocks(0, 'raw', ''); return blocks[0].id; }
    const fid = i > 0 ? blocks[i - 1].id : blocks[1].id;
    setBlocks(bs => bs.filter(b => b.id !== id));
    return fid;
  }

  // ── Image insertion ───────────────────────────────────────────────────────

  async function insertImageAt(idx: number, file: File) {
    if (file.size > MAX_IMG) { alert(`Image too large (${(file.size/1024/1024).toFixed(1)} MB). Max is 5 MB.`); return; }
    const dataUrl = await toBase64(file);
    const name = file.name.replace(/[^\w\s.-]/g, '') || 'image';
    const imgBlock: Block  = { id: uid(), raw: `![${name}](${dataUrl})` };
    const nextBlock: Block = { id: uid(), raw: '' };
    setBlocks(bs => [...bs.slice(0, idx + 1), imgBlock, nextBlock, ...bs.slice(idx + 1)]);
    notify();
    // Focus the empty block below the image so user can keep typing
    focusBlock(nextBlock.id, 'end');
  }

  function openPicker(afterIdx: number) {
    const inp = document.createElement('input');
    inp.type = 'file'; inp.accept = 'image/*';
    inp.onchange = () => { const f = inp.files?.[0]; if (f) insertImageAt(afterIdx, f); };
    inp.click();
  }

  // ── Block drag reorder ────────────────────────────────────────────────────

  // SolidJS tuple handlers call fn(data, event) — data arg comes first
  function onHandleDragStart(id: string, e: DragEvent) {
    setDragSrcId(id);
    e.dataTransfer!.effectAllowed = 'move';
    e.dataTransfer!.setData('text/plain', id);
  }

  function onHandleDragEnd() { setDragSrcId(null); setDragOverId(null); }

  function onBlockDragOver(id: string, e: DragEvent) {
    if (!dragSrcId()) return; // not a block drag — let file drops bubble
    e.preventDefault(); e.stopPropagation();
    if (dragSrcId() === id) return;
    e.dataTransfer!.dropEffect = 'move';
    const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
    setDragOverId(id);
    setDragOverHalf(e.clientY < rect.top + rect.height / 2 ? 'top' : 'bottom');
  }

  function onBlockDragLeave(id: string) { if (dragOverId() === id) setDragOverId(null); }

  function onBlockDrop(targetId: string, e: DragEvent) {
    if (!dragSrcId()) return;
    e.preventDefault(); e.stopPropagation();
    const srcId = dragSrcId()!;
    const half  = dragOverHalf();
    setDragSrcId(null); setDragOverId(null);
    if (srcId === targetId) return;
    setBlocks(bs => {
      const si = bs.findIndex(b => b.id === srcId);
      const src = bs[si];
      const without = bs.filter(b => b.id !== srcId);
      const ti = without.findIndex(b => b.id === targetId);
      const at = half === 'top' ? ti : ti + 1;
      return [...without.slice(0, at), src, ...without.slice(at)];
    });
    notify();
  }

  // ── File drag/drop (images from OS) ──────────────────────────────────────

  function onFileDragOver(e: DragEvent) {
    if (dragSrcId()) return; // block reorder in progress
    const hasImg = Array.from(e.dataTransfer?.items ?? []).some(it => it.type.startsWith('image/'));
    if (hasImg) { e.preventDefault(); setFileDragOver(true); }
  }
  function onFileDragLeave() { setFileDragOver(false); }
  function onFileDrop(e: DragEvent) {
    if (dragSrcId()) return;
    e.preventDefault(); setFileDragOver(false);
    const files = Array.from(e.dataTransfer?.files ?? []).filter(f => f.type.startsWith('image/'));
    const idx = blocks.length - 1;
    for (const f of files) insertImageAt(idx, f);
  }

  // ── Paste ─────────────────────────────────────────────────────────────────

  function onPaste(e: ClipboardEvent) {
    const imgItem = Array.from(e.clipboardData?.items ?? []).find(it => it.type.startsWith('image/'));
    if (!imgItem) return;
    e.preventDefault();
    const file = imgItem.getAsFile();
    if (!file) return;
    const aid = activeId();
    insertImageAt(aid ? idxOf(aid) : blocks.length - 1, file);
  }

  // ── Keyboard ──────────────────────────────────────────────────────────────

  function onKeyDown(id: string, e: KeyboardEvent) {
    const ta   = e.currentTarget as HTMLTextAreaElement;
    const i    = idxOf(id);
    const raw  = blocks[i].raw;
    const type = blockType(raw);

    // Slash menu nav
    const sm = slashMenu();
    if (sm) {
      const cmds = filteredCmds();
      if (e.key === 'ArrowDown')                { e.preventDefault(); setSlashMenu(m => m ? { ...m, selectedIdx: Math.min(m.selectedIdx+1, cmds.length-1) } : null); return; }
      if (e.key === 'ArrowUp')                  { e.preventDefault(); setSlashMenu(m => m ? { ...m, selectedIdx: Math.max(m.selectedIdx-1, 0) } : null); return; }
      if (e.key === 'Enter' || e.key === 'Tab') { e.preventDefault(); const c = cmds[sm.selectedIdx]; if (c) applyCmd(c, id); return; }
      if (e.key === 'Escape')                   { e.preventDefault(); setSlashMenu(null); return; }
    }

    if (e.key === 'Enter' && type !== 'code') {
      e.preventDefault();
      const inner = stripPrefix(raw, type);
      if (!inner.trim() && ['bullet','numbered','todo','quote'].includes(type)) {
        updateRaw(id, ''); notify(); focusBlock(id, 'end'); return;
      }
      let next = '';
      if (type === 'bullet')   next = (raw.match(/^([-*+] )/)?.[1] ?? '- ');
      if (type === 'numbered') { const n = parseInt(raw.match(/^(\d+)/)?.[1] ?? '1'); next = `${n+1}. `; }
      if (type === 'todo')     next = '- [ ] ';
      if (type === 'quote')    next = '> ';
      const nid = insertAfter(i, next);
      notify(); focusBlock(nid, 'end'); return;
    }

    if (e.key === 'Backspace' && ta.selectionStart === 0 && ta.selectionEnd === 0) {
      if (raw === '' && blocks.length > 1) {
        e.preventDefault();
        const prev = deleteBlock(id); notify(); if (prev) focusBlock(prev, 'end'); return;
      }
      let stripped: string | null = null;
      if (type === 'h1') stripped = raw.slice(2);
      if (type === 'h2') stripped = raw.slice(3);
      if (type === 'h3') stripped = raw.slice(4);
      if (stripped !== null) {
        e.preventDefault(); updateRaw(id, stripped); notify();
        const ref = taRefs.get(id); if (ref) { ref.value = stripped; resize(ref); ref.selectionStart = 0; ref.selectionEnd = 0; } return;
      }
    }

    if (e.key === 'ArrowUp'   && ta.selectionStart === 0 && i > 0)                       { e.preventDefault(); focusBlock(blocks[i-1].id, 'end'); }
    if (e.key === 'ArrowDown' && ta.selectionStart === ta.value.length && i < blocks.length-1) { e.preventDefault(); focusBlock(blocks[i+1].id, 'start'); }
  }

  function onInput(id: string, e: InputEvent) {
    const ta = e.currentTarget as HTMLTextAreaElement;
    resize(ta); updateRaw(id, ta.value); notify(); detectSlash(ta);
  }

  function onBlur(id: string) { setTimeout(() => { if (activeId() === id) setActiveId(null); }, 120); }

  // ── Slash palette ─────────────────────────────────────────────────────────

  const filteredCmds = () => {
    const m = slashMenu();
    if (!m?.filter) return COMMANDS;
    const f = m.filter.toLowerCase();
    return COMMANDS.filter(c => c.label.toLowerCase().includes(f) || c.keywords.some(k => k.startsWith(f)));
  };

  function palettePos(ta: HTMLTextAreaElement, si: number) {
    const cs = window.getComputedStyle(ta);
    const rect = ta.getBoundingClientRect();
    const lh = parseFloat(cs.lineHeight) || 20;
    const lines = ta.value.substring(0, si).split('\n').length - 1;
    const y = rect.top + parseFloat(cs.paddingTop) + lines * lh - ta.scrollTop + lh + 4;
    const x = rect.left + parseFloat(cs.paddingLeft) + 4;
    const ph = 330;
    return { top: y + ph > window.innerHeight - 20 ? y - lh - ph - 8 : y, left: Math.min(x, window.innerWidth - 240) };
  }

  function detectSlash(ta: HTMLTextAreaElement) {
    const pos = ta.selectionStart; const text = ta.value;
    let si = -1;
    for (let i = pos-1; i >= 0; i--) {
      if (text[i] === '/') { const p = i > 0 ? text[i-1] : '\n'; if (p === '\n' || p === ' ' || p === '\t' || i === 0) si = i; break; }
      if (text[i] === '\n' || text[i] === ' ') break;
    }
    if (si === -1) { setSlashMenu(null); return; }
    const filter = text.substring(si+1, pos);
    if (filter.includes(' ') || filter.includes('\n')) { setSlashMenu(null); return; }
    const cmds = COMMANDS.filter(c => { const f = filter.toLowerCase(); return !f || c.label.toLowerCase().includes(f) || c.keywords.some(k => k.startsWith(f)); });
    if (!cmds.length) { setSlashMenu(null); return; }
    setSlashMenu({ slashPos: si, filter, selectedIdx: 0 });
    setPalPos(palettePos(ta, si));
  }

  function applyCmd(cmd: Command, blockId: string) {
    const m = slashMenu(); if (!m) return;
    const ta = taRefs.get(blockId);
    const before = (ta?.value ?? '').substring(0, m.slashPos);
    const after  = (ta?.value ?? '').substring(ta?.selectionStart ?? m.slashPos);
    if (cmd.special === 'image') {
      const cleaned = before + after;
      updateRaw(blockId, cleaned); notify();
      if (ta) { ta.value = cleaned; resize(ta); }
      setSlashMenu(null);
      openPicker(idxOf(blockId)); return;
    }
    const newRaw = before + cmd.insert + after;
    let cursor = before.length + cmd.insert.length - (cmd.cursorOffset ?? 0);
    updateRaw(blockId, newRaw); notify();
    setSlashMenu(null); setActiveId(blockId);
    requestAnimationFrame(() => {
      const ref = taRefs.get(blockId); if (!ref) return;
      ref.value = newRaw; ref.selectionStart = cursor; ref.selectionEnd = cursor; resize(ref); ref.focus();
    });
  }

  function onDocMouseDown(e: MouseEvent) {
    if (paletteRef?.contains(e.target as Node)) return;
    setSlashMenu(null);
    // If clicking outside the selection toolbar, clear it
    const tb = document.getElementById('note-sel-toolbar');
    if (tb && tb.contains(e.target as Node)) return;
    setSelState(null);
    setTransformResult(null);
  }
  document.addEventListener('mousedown', onDocMouseDown);
  onCleanup(() => document.removeEventListener('mousedown', onDocMouseDown));

  // ── AI selection toolbar ──────────────────────────────────────────────────

  function handleSelectionChange() {
    // Use mouseup-based detection (more reliable than selectionchange)
    // This is called from the mouseup handler below
    const sel = window.getSelection();
    const text = sel?.toString() ?? '';

    if (!text.trim() || !editorRef || !sel || sel.isCollapsed || !sel.rangeCount) {
      return;
    }

    const range = sel.getRangeAt(0);
    if (!editorRef.contains(range.commonAncestorContainer)) {
      return;
    }

    const rect = range.getBoundingClientRect();
    if (rect.width === 0 && rect.height === 0) return;

    const startEl = range.startContainer.nodeType === Node.TEXT_NODE
      ? range.startContainer.parentElement
      : (range.startContainer as Element);
    const endEl = range.endContainer.nodeType === Node.TEXT_NODE
      ? range.endContainer.parentElement
      : (range.endContainer as Element);

    const startBlockEl = startEl?.closest('[data-block-id]');
    const endBlockEl   = endEl?.closest('[data-block-id]');

    setSelState({
      text,
      top:  rect.top,
      left: Math.max(140, Math.min(rect.left + rect.width / 2, window.innerWidth - 140)),
      startBlockId: startBlockEl?.getAttribute('data-block-id') ?? null,
      endBlockId:   endBlockEl?.getAttribute('data-block-id') ?? null,
    });
    setTransformResult(null);
  }

  function onDocMouseUp(e: MouseEvent) {
    // Don't re-check if clicking inside the toolbar (would collapse selection)
    const tb = document.getElementById('note-sel-toolbar');
    if (tb && tb.contains(e.target as Node)) return;
    // Small rAF so browser has finalised selection before we read it
    requestAnimationFrame(() => handleSelectionChange());
  }
  document.addEventListener('mouseup', onDocMouseUp);
  onCleanup(() => document.removeEventListener('mouseup', onDocMouseUp));

  async function applyTransform(instruction: string) {
    const ss = selState();
    if (!ss || !ss.text.trim() || transformLoading()) return;
    setTransformLoading(true);
    setTransformResult(null);
    try {
      const res = await apiTransformText(ss.text, instruction, props.model || undefined, props.provider || undefined);
      setTransformResult(res.result);
    } catch (err) {
      console.error('transform error', err);
    } finally {
      setTransformLoading(false);
    }
  }

  function applyResult() {
    const ss = selState();
    const result = transformResult();
    if (!ss || result == null) return;

    if (ss.startBlockId === ss.endBlockId && ss.startBlockId) {
      const i = idxOf(ss.startBlockId);
      if (i !== -1) {
        const raw = blocks[i].raw;
        const type = blockType(raw);
        if (raw.includes(ss.text)) {
          updateRaw(ss.startBlockId, raw.replace(ss.text, result));
        } else {
          const prefix = getPrefix(raw, type);
          const content = stripPrefix(raw, type);
          if (content.includes(ss.text)) {
            updateRaw(ss.startBlockId, prefix + content.replace(ss.text, result));
          } else {
            insertAfter(i, result);
          }
        }
        notify();
      }
    } else {
      const startIdx = ss.startBlockId ? idxOf(ss.startBlockId) : -1;
      const endIdx   = ss.endBlockId   ? idxOf(ss.endBlockId)   : -1;
      if (startIdx !== -1 && endIdx !== -1) {
        const from = Math.min(startIdx, endIdx);
        const to   = Math.max(startIdx, endIdx);
        const resultBlock: Block = { id: uid(), raw: result };
        setBlocks(bs => [...bs.slice(0, from), resultBlock, ...bs.slice(to + 1)]);
        notify();
      }
    }

    setSelState(null);
    setTransformResult(null);
  }


  // ── Textarea class ────────────────────────────────────────────────────────

  function taClass(type: BlockType): string {
    const base = 'w-full resize-none bg-transparent focus:outline-none leading-relaxed caret-[color:var(--accent)]';
    if (type === 'h1')   return `${base} text-[26px] font-bold text-zinc-100`;
    if (type === 'h2')   return `${base} text-[20px] font-semibold text-zinc-100`;
    if (type === 'h3')   return `${base} text-[16px] font-medium text-zinc-200`;
    if (type === 'code') return `${base} text-[13px] font-mono text-zinc-300`;
    return `${base} text-[14px] text-zinc-200`;
  }

  // ── JSX ───────────────────────────────────────────────────────────────────

  return (
    <div
      ref={editorRef}
      class={`flex-1 flex flex-col min-h-0 cursor-text select-text relative transition-colors ${fileDragOver() ? 'ring-2 ring-[color:var(--accent)] ring-inset rounded-md' : ''}`}
      onPaste={onPaste}
      onDragOver={onFileDragOver}
      onDragLeave={onFileDragLeave}
      onDrop={onFileDrop}
      onClick={e => { if (e.target === e.currentTarget) { const last = blocks[blocks.length-1]; if (last) focusBlock(last.id, 'end'); } }}
    >
      {/* File drag-over hint */}
      <Show when={fileDragOver()}>
        <div class="absolute inset-0 z-10 flex items-center justify-center pointer-events-none rounded-md bg-[color:var(--accent)]/5">
          <div class="flex items-center gap-2 px-4 py-2 rounded-lg bg-[color:var(--bg-elevated)] border border-[color:var(--accent)]/40 text-[13px] text-[color:var(--accent)] font-medium">
            <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M4 16l4.586-4.586a2 2 0 012.828 0L16 16m-2-2l1.586-1.586a2 2 0 012.828 0L20 14m-6-6h.01M6 20h12a2 2 0 002-2V6a2 2 0 00-2-2H6a2 2 0 00-2 2v12a2 2 0 002 2z" />
            </svg>
            Drop image to insert
          </div>
        </div>
      </Show>

      <For each={blocks}>
        {(block) => {
          const type     = () => blockType(block.raw);
          const isActive = () => activeId() === block.id;
          const isDragging  = () => dragSrcId()  === block.id;
          const isDropTarget = () => dragOverId() === block.id;

          function activate(pos: 'start' | 'end' | number = 'end') {
            // Don't steal focus while user has text selected (cross-block selection)
            if (window.getSelection()?.toString().trim()) return;
            if (type() === 'image') { setActiveId(block.id); return; }
            focusBlock(block.id, pos);
          }

          // ── Image editor ─────────────────────────────────────────────────
          function ImageEditor() {
            const match = () => block.raw.match(/^!\[([^\]]*)\]\((data:image\/[^\s)]+)\)/);
            const src   = () => match()?.[2] ?? '';
            const [alt, setAlt] = createSignal(match()?.[1] ?? '');

            function saveAlt(v: string) {
              setAlt(v); const s = src();
              if (s) { updateRaw(block.id, `![${v}](${s})`); notify(); }
            }

            function replace() {
              const inp = document.createElement('input'); inp.type = 'file'; inp.accept = 'image/*';
              inp.onchange = async () => {
                const f = inp.files?.[0]; if (!f) return;
                if (f.size > MAX_IMG) { alert('Image too large (max 5 MB).'); return; }
                const url = await toBase64(f);
                updateRaw(block.id, `![${f.name}](${url})`); notify(); setAlt(f.name);
              };
              inp.click();
            }

            return (
              <div class="my-1 rounded-lg border border-[color:var(--border-default)] overflow-hidden bg-[color:var(--bg-surface)]">
                <img src={src()} alt={alt()} class="max-w-full block" />
                <div class="flex items-center gap-2 px-3 py-2 border-t border-[color:var(--border-subtle)]">
                  <svg class="w-3.5 h-3.5 text-zinc-600 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M7 8h10M7 12h4m1 8l-4-4H5a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v8a2 2 0 01-2 2h-3l-4 4z" />
                  </svg>
                  <input type="text" value={alt()} onInput={e => saveAlt(e.currentTarget.value)} placeholder="Alt text…"
                    class="flex-1 text-[12px] text-zinc-400 bg-transparent focus:outline-none placeholder-zinc-700" />
                  <button onClick={replace} class="shrink-0 text-[11px] text-zinc-500 hover:text-zinc-200 transition px-2 py-1 rounded border border-[color:var(--border-subtle)] hover:border-[color:var(--border-default)]">Replace</button>
                  <button onClick={() => { const fid = deleteBlock(block.id); notify(); if (fid) focusBlock(fid, 'end'); }}
                    class="shrink-0 text-[11px] text-red-400/70 hover:text-red-400 transition px-2 py-1 rounded border border-red-400/10 hover:border-red-400/30">Remove</button>
                </div>
              </div>
            );
          }

          // ── Textarea ─────────────────────────────────────────────────────
          function Textarea() {
            return (
              <textarea
                ref={el => {
                  taRefs.set(block.id, el); el.value = block.raw;
                  requestAnimationFrame(() => resize(el));
                  if (props.autofocus && block.id === blocks[0]?.id)
                    requestAnimationFrame(() => { el.focus(); el.selectionStart = el.value.length; el.selectionEnd = el.value.length; });
                }}
                rows={1}
                placeholder={type() === 'code' ? '```\ncode\n```' : 'Type something, or / for commands…'}
                class={taClass(type())}
                style="overflow:hidden;"
                onInput={[onInput, block.id] as any}
                onKeyDown={[onKeyDown, block.id] as any}
                onBlur={() => onBlur(block.id)}
                onFocus={e => { resize(e.currentTarget); detectSlash(e.currentTarget); }}
              />
            );
          }

          // ── Rendered view ─────────────────────────────────────────────────
          function View() {
            switch (type()) {
              case 'h1': return <h1 onClick={() => activate()} class="text-[26px] font-bold text-zinc-100 leading-tight py-1 cursor-text min-h-[1.4em]"><Show when={stripPrefix(block.raw,'h1')} fallback={<span class="text-zinc-700 font-normal text-[14px]">Heading 1</span>}>{stripPrefix(block.raw,'h1')}</Show></h1>;
              case 'h2': return <h2 onClick={() => activate()} class="text-[20px] font-semibold text-zinc-100 leading-tight py-1 cursor-text min-h-[1.4em]"><Show when={stripPrefix(block.raw,'h2')} fallback={<span class="text-zinc-700 font-normal text-[14px]">Heading 2</span>}>{stripPrefix(block.raw,'h2')}</Show></h2>;
              case 'h3': return <h3 onClick={() => activate()} class="text-[16px] font-medium text-zinc-200 leading-tight py-1 cursor-text min-h-[1.4em]"><Show when={stripPrefix(block.raw,'h3')} fallback={<span class="text-zinc-700 font-normal text-[14px]">Heading 3</span>}>{stripPrefix(block.raw,'h3')}</Show></h3>;
              case 'bullet': return <div onClick={() => activate()} class="flex items-baseline gap-2.5 py-0.5 cursor-text"><span class="text-zinc-500 shrink-0 select-none">•</span><span class="text-[14px] text-zinc-300 leading-relaxed" innerHTML={safeInline(stripPrefix(block.raw,'bullet'))} /></div>;
              case 'numbered': { const num = () => block.raw.match(/^(\d+)/)?.[1] ?? '1'; return <div onClick={() => activate()} class="flex items-baseline gap-2.5 py-0.5 cursor-text"><span class="text-zinc-500 shrink-0 tabular-nums text-[14px] select-none">{num()}.</span><span class="text-[14px] text-zinc-300 leading-relaxed" innerHTML={safeInline(stripPrefix(block.raw,'numbered'))} /></div>; }
              case 'todo': {
                const done = () => /^[-*+] \[[xX]\] /.test(block.raw);
                return <div class="flex items-center gap-2.5 py-0.5">
                  <input type="checkbox" checked={done()} class="shrink-0 accent-[color:var(--accent)] cursor-pointer mt-[1px]"
                    onClick={e => { e.stopPropagation(); const nr = done() ? block.raw.replace(/^([-*+] )\[[xX]\] /,'$1[ ] ') : block.raw.replace(/^([-*+] )\[ \] /,'$1[x] '); const i = idxOf(block.id); if (i !== -1) setBlocks(i,'raw',nr); notify(); }} />
                  <span onClick={() => activate()} class={`text-[14px] leading-relaxed cursor-text flex-1 ${done() ? 'line-through text-zinc-600' : 'text-zinc-300'}`} innerHTML={safeInline(stripPrefix(block.raw,'todo'))} />
                </div>;
              }
              case 'code': { const cl = () => block.raw.split('\n'); const lang = () => cl()[0].slice(3).trim(); const code = () => cl().slice(1,cl().length-1).join('\n');
                return <div onClick={() => activate()} class="cursor-text my-1"><pre class="bg-[color:var(--bg-surface)] border border-[color:var(--border-subtle)] rounded-md px-4 py-3 overflow-x-auto"><Show when={lang()}><div class="text-[10px] text-zinc-600 font-mono mb-2 -mt-0.5 select-none">{lang()}</div></Show><code class="text-[13px] font-mono text-zinc-300 leading-relaxed whitespace-pre">{code()}</code></pre></div>; }
              case 'quote': return <blockquote onClick={() => activate()} class="border-l-2 border-zinc-600 pl-4 py-0.5 cursor-text my-0.5"><span class="text-[14px] text-zinc-400 italic leading-relaxed" innerHTML={safeInline(stripPrefix(block.raw,'quote'))} /></blockquote>;
              case 'divider': return <div onClick={() => activate()} class="py-3 cursor-text"><hr class="border-[color:var(--border-subtle)]" /></div>;
              case 'image': {
                const m = () => block.raw.match(/^!\[([^\]]*)\]\((data:image\/[^\s)]+)\)/);
                return <div onClick={() => activate()} class="my-1 cursor-pointer group/imgv relative">
                  <img src={m()?.[2] ?? ''} alt={m()?.[1] ?? ''} class="max-w-full rounded-md border border-[color:var(--border-subtle)] block" />
                  <Show when={m()?.[1]}><p class="text-[11px] text-zinc-600 mt-1 italic">{m()?.[1]}</p></Show>
                  <div class="absolute top-2 right-2 opacity-0 group-hover/imgv:opacity-100 transition pointer-events-none">
                    <span class="text-[10px] bg-black/60 text-zinc-300 px-2 py-1 rounded">Click to edit</span>
                  </div>
                </div>;
              }
              case 'empty': return <div onClick={() => activate()} class="min-h-[1.75em] text-[14px] leading-relaxed cursor-text" />;
              default: return <p onClick={() => activate()} class="text-[14px] text-zinc-300 leading-relaxed py-0.5 cursor-text" innerHTML={safeInline(block.raw)} />;
            }
          }

          return (
            <div
              class={`group/block flex items-start gap-1 transition-opacity ${isDragging() ? 'opacity-30' : ''}`}
              onDragOver={[onBlockDragOver, block.id] as any}
              onDragLeave={() => onBlockDragLeave(block.id)}
              onDrop={[onBlockDrop, block.id] as any}
            >
              {/* Drag handle */}
              <div
                draggable="true"
                onDragStart={[onHandleDragStart, block.id] as any}
                onDragEnd={onHandleDragEnd}
                class="shrink-0 w-5 flex items-center justify-center pt-[5px] opacity-0 group-hover/block:opacity-100 transition cursor-grab active:cursor-grabbing text-zinc-700 hover:text-zinc-400 select-none"
                title="Drag to reorder"
              >
                <GripIcon />
              </div>

              {/* Block content + drop indicators */}
              <div class="flex-1 min-w-0 relative" data-block-id={block.id}>
                {/* Drop line above */}
                <Show when={isDropTarget() && dragOverHalf() === 'top'}>
                  <div class="absolute -top-px left-0 right-0 h-0.5 bg-[color:var(--accent)] rounded-full z-20 pointer-events-none" />
                </Show>

                <Show when={isActive()}>
                  <Show when={type() === 'image'} fallback={<Textarea />}>
                    <ImageEditor />
                  </Show>
                </Show>
                <Show when={!isActive()}>
                  <View />
                </Show>

                {/* Drop line below */}
                <Show when={isDropTarget() && dragOverHalf() === 'bottom'}>
                  <div class="absolute -bottom-px left-0 right-0 h-0.5 bg-[color:var(--accent)] rounded-full z-20 pointer-events-none" />
                </Show>
              </div>
            </div>
          );
        }}
      </For>

      {/* Slash command palette */}
      <Show when={slashMenu() && filteredCmds().length > 0}>
        <div
          ref={paletteRef}
          class="fixed z-50 w-56 bg-[color:var(--bg-elevated)] border border-[color:var(--border-default)] rounded-lg shadow-xl overflow-hidden"
          style={`top:${palPos().top}px;left:${palPos().left}px;`}
        >
          <div class="px-3 py-1.5 text-[10px] text-zinc-600 font-medium uppercase tracking-wider border-b border-[color:var(--border-subtle)]">Commands</div>
          <div ref={listRef} class="max-h-52 overflow-y-auto py-1">
            <For each={filteredCmds()}>
              {(cmd, i) => {
                const sel = () => slashMenu()?.selectedIdx === i();
                return (
                  <button
                    data-idx={i()}
                    onMouseDown={e => { e.preventDefault(); const aid = activeId(); if (aid) applyCmd(cmd, aid); }}
                    onMouseEnter={() => setSlashMenu(m => m ? { ...m, selectedIdx: i() } : null)}
                    class={`w-full flex items-center gap-3 px-3 py-2 text-left transition ${sel() ? 'bg-[color:var(--accent-soft)] text-zinc-100' : 'text-zinc-300 hover:bg-[color:var(--bg-hover)]/50'}`}
                  >
                    <span class={`w-7 h-7 rounded-md flex items-center justify-center text-[11px] font-bold shrink-0 ${sel() ? 'bg-[color:var(--accent)]/20 text-[color:var(--accent)]' : 'bg-[color:var(--bg-surface)] text-zinc-400'}`}>{cmd.textIcon}</span>
                    <div class="min-w-0">
                      <div class="text-[12px] font-medium leading-none mb-0.5">{cmd.label}</div>
                      <div class="text-[10px] text-zinc-500 leading-none">{cmd.description}</div>
                    </div>
                  </button>
                );
              }}
            </For>
          </div>
          <div class="px-3 py-1.5 border-t border-[color:var(--border-subtle)] text-[10px] text-zinc-600">↑↓ navigate · Enter select · Esc dismiss</div>
        </div>
      </Show>

      {/* AI text selection toolbar */}
      <Show when={selState()}>
        <div
          id="note-sel-toolbar"
          class="fixed z-[9999] flex flex-col items-center gap-1.5 pointer-events-auto"
          style={`top: ${(selState()?.top ?? 0) - 8}px; left: ${selState()?.left ?? 0}px; transform: translate(-50%, -100%);`}
          onMouseDown={e => e.preventDefault()}
        >
          {/* Action buttons + model picker row */}
          <div class="flex items-center gap-0.5 p-1 bg-[color:var(--bg-elevated)] border border-[color:var(--border-default)] rounded-lg shadow-xl">
            {([ ['improve', 'Improve'], ['shorter', 'Shorter'], ['longer', 'Longer'], ['grammar', 'Fix grammar'] ] as [string, string][]).map(([instr, label]) => (
              <button
                class="px-2.5 py-1.5 text-[12px] font-medium text-zinc-300 hover:text-zinc-100 hover:bg-[color:var(--bg-hover)] rounded-md transition disabled:opacity-40 disabled:cursor-not-allowed whitespace-nowrap"
                disabled={transformLoading()}
                onMouseDown={e => { e.preventDefault(); applyTransform(instr); }}
              >
                {label}
              </button>
            ))}

            <Show when={transformLoading()}>
              <div class="w-4 h-4 border-2 border-[color:var(--accent)] border-t-transparent rounded-full animate-spin mx-1" />
            </Show>
          </div>

          {/* Result panel */}
          <Show when={transformResult() !== null}>
            <div class="w-80 max-w-[calc(100vw-32px)] bg-[color:var(--bg-elevated)] border border-[color:var(--accent)]/30 rounded-lg shadow-xl overflow-hidden">
              <div class="px-3 py-2 border-b border-[color:var(--border-subtle)] flex items-center justify-between">
                <span class="text-[10px] font-medium text-[color:var(--accent)] uppercase tracking-wider">Result</span>
                <button
                  class="text-[10px] text-zinc-600 hover:text-zinc-400 transition leading-none"
                  onMouseDown={e => { e.preventDefault(); setSelState(null); setTransformResult(null); }}
                >✕</button>
              </div>
              <div class="px-3 py-2.5 max-h-44 overflow-y-auto">
                <p class="text-[13px] text-zinc-300 leading-relaxed whitespace-pre-wrap">{transformResult()}</p>
              </div>
              <div class="px-3 py-2 border-t border-[color:var(--border-subtle)] flex items-center gap-2">
                <button
                  class="flex-1 px-3 py-1.5 text-[12px] font-medium bg-[color:var(--accent)] text-white rounded-md hover:opacity-90 transition"
                  onMouseDown={e => { e.preventDefault(); applyResult(); }}
                >
                  Apply
                </button>
                <button
                  class="px-3 py-1.5 text-[12px] text-zinc-400 hover:text-zinc-200 rounded-md border border-[color:var(--border-subtle)] hover:border-[color:var(--border-default)] transition"
                  onMouseDown={e => { e.preventDefault(); setSelState(null); setTransformResult(null); }}
                >
                  Dismiss
                </button>
              </div>
            </div>
          </Show>
        </div>
      </Show>
    </div>
  );
}
