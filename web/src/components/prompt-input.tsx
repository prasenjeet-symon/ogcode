import { createSignal, createEffect, Show, For, onCleanup, onMount, untrack } from 'solid-js';
import { Portal } from 'solid-js/web';
import { useSession } from '../context/session';
import { type ImagePartData } from '../api/client';
import ModelSelector from './model-selector';
import PermissionPrompt from './permission-prompt';
import PermissionModeToggle from './permission-mode-toggle';
import { trackKeyboardInset } from '../lib/keyboard';

// Maximum image file size: 10 MB
const MAX_IMAGE_SIZE = 10 * 1024 * 1024;
const ACCEPTED_IMAGE_TYPES = ['image/jpeg', 'image/png', 'image/webp', 'image/gif'];

interface PendingImage {
  mediaType: string;
  data: string;       // base64 (without data: prefix)
  name: string;
  previewUrl: string; // object URL for thumbnail
}

// A composer draft, stashed per session id while the user is elsewhere.
interface Draft {
  text: string;
  images: PendingImage[];
}

function isAcceptedType(type: string): boolean {
  return ACCEPTED_IMAGE_TYPES.includes(type);
}

function fileToBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      // result is "data:image/jpeg;base64,...."
      const result = reader.result as string;
      const commaIdx = result.indexOf(',');
      resolve(commaIdx >= 0 ? result.slice(commaIdx + 1) : '');
    };
    reader.onerror = () => reject(reader.error);
    reader.readAsDataURL(file);
  });
}

export default function PromptInput() {
  const session = useSession();
  const [text, setText] = createSignal('');
  const [focused, setFocused] = createSignal(false);
  const [pendingImages, setPendingImages] = createSignal<PendingImage[]>([]);
  const [imageError, setImageError] = createSignal('');
  const [guidanceSent, setGuidanceSent] = createSignal(false); // brief confirmation after sending guidance
  const [attachOpen, setAttachOpen] = createSignal(false);
  const [attachPos, setAttachPos] = createSignal<{ left: number; top?: number; bottom?: number } | null>(null);
  let textareaRef: HTMLTextAreaElement | undefined;
  let fileInputRef: HTMLInputElement | undefined;
  let cameraInputRef: HTMLInputElement | undefined;
  let attachBtnRef: HTMLButtonElement | undefined;
  let guidanceSentTimer: ReturnType<typeof setTimeout> | null = null;

  // Composer contents are per-session. PromptInput stays mounted across session
  // switches (the route param changes but the component is reused), so a single
  // set of signals would otherwise carry a half-typed message and its attached
  // images from the session you left into the one you opened — the images are
  // the visible symptom, since their previews render in the destination's
  // composer. Stash the draft on the way out, restore it on the way back in.
  //
  // In-memory only: an image is held as base64 plus an object URL, too large
  // (and too sensitive) to write to localStorage.
  const drafts = new Map<string, Draft>();
  let draftSessionId = '';

  createEffect(() => {
    const id = session.activeSession()?.id ?? '';
    if (!id || id === draftSessionId) return;

    // Stash what the session we are leaving holds, verbatim. untrack keeps the
    // effect subscribed to the session id alone — a keystroke must not wake it.
    const leaving = draftSessionId;
    untrack(() => {
      if (leaving) drafts.set(leaving, { text: text(), images: pendingImages() });
    });

    // Restore — or start empty — for the session we are entering. Taking the
    // draft out of the map keeps a live draft in exactly one place, so the
    // unmount teardown revokes each preview URL once.
    const draft = drafts.get(id);
    drafts.delete(id);
    draftSessionId = id;
    setText(draft?.text ?? '');
    setPendingImages(draft?.images ?? []);

    // The badge is a transient confirmation for the session it was sent in,
    // never something the destination session should inherit.
    setGuidanceSent(false);
    if (guidanceSentTimer) { clearTimeout(guidanceSentTimer); guidanceSentTimer = null; }
  });

  // Auto-resize the textarea.
  //
  // Measuring `scrollHeight` after setting height to `auto` is the usual
  // recipe and it is wrong here: on a fresh load this effect runs before the
  // first layout, and an auto-height textarea in a flex column reports the
  // whole free column as its scrollHeight — so the composer opened at its
  // 240px maximum and ate half the screen. Pinning to the CSS min-height while
  // empty, and measuring from `0px` otherwise, always yields the true content
  // height.
  const MAX_COMPOSER_HEIGHT = 220;
  const resize = () => {
    const el = textareaRef;
    if (!el) return;
    if (!text()) {
      el.style.height = '';   // fall back to the min-height in the class list
      return;
    }
    el.style.height = '0px';
    el.style.height = Math.min(el.scrollHeight, MAX_COMPOSER_HEIGHT) + 'px';
  };
  createEffect(() => {
    text();
    resize();
  });

  // The agent loop is "running" if we're loading (LLM streaming) OR tools are executing
  const isRunning = () => session.loading() || session.hasRunningTools();

  // Open the attach popover above the trigger. On phones (<640px) the
  // .attach-menu CSS in index.css overrides this into a bottom sheet.
  const toggleAttach = () => {
    if (attachOpen()) { setAttachOpen(false); return; }
    if (window.matchMedia('(max-width: 640px)').matches) { setAttachOpen(true); return; }
    const r = attachBtnRef?.getBoundingClientRect();
    if (r) {
      const W = 224, GAP = 6, M = 8; // menu width (w-56), gap, viewport margin
      const vw = window.innerWidth;
      let left = r.left;
      if (left + W > vw - M) left = vw - W - M;
      if (left < M) left = M;
      setAttachPos({ left, bottom: window.innerHeight - r.top + GAP });
    }
    setAttachOpen(true);
  };

  // ── Image handling ──

  const addFiles = async (files: FileList | File[]) => {
    const fileArray = Array.from(files);
    const newImages: PendingImage[] = [];
    let error = '';

    for (const file of fileArray) {
      if (!file.type.startsWith('image/')) {
        error = 'Only image files are supported';
        continue;
      }
      if (!isAcceptedType(file.type)) {
        error = `Unsupported image type: ${file.type}`;
        continue;
      }
      if (file.size > MAX_IMAGE_SIZE) {
        error = `Image "${file.name}" exceeds 10 MB limit`;
        continue;
      }
      try {
        const base64 = await fileToBase64(file);
        if (!base64) continue;
        newImages.push({
          mediaType: file.type,
          data: base64,
          name: file.name,
          previewUrl: URL.createObjectURL(file),
        });
      } catch (e) {
        error = `Failed to read "${file.name}"`;
      }
    }

    if (error) {
      setImageError(error);
      setTimeout(() => setImageError(''), 4000);
    }
    if (newImages.length > 0) {
      setPendingImages((prev) => [...prev, ...newImages]);
    }
  };

  const handleFileSelect = (e: Event) => {
    const input = e.target as HTMLInputElement;
    if (input.files && input.files.length > 0) {
      addFiles(input.files);
    }
    // Reset so the same file can be selected again
    input.value = '';
  };

  const handlePaste = (e: ClipboardEvent) => {
    const items = e.clipboardData?.items;
    if (!items) return;
    const imageFiles: File[] = [];
    for (const item of items) {
      if (item.kind === 'file' && item.type.startsWith('image/')) {
        const file = item.getAsFile();
        if (file) imageFiles.push(file);
      }
    }
    if (imageFiles.length > 0) {
      e.preventDefault();
      addFiles(imageFiles);
    }
  };

  const removeImage = (index: number) => {
    setPendingImages((prev) => {
      const removed = prev[index];
      if (removed) URL.revokeObjectURL(removed.previewUrl);
      return prev.filter((_, i) => i !== index);
    });
  };

  onCleanup(() => {
    // Revoke every preview URL — the ones on screen and every stashed draft.
    pendingImages().forEach((img) => URL.revokeObjectURL(img.previewUrl));
    drafts.forEach((d) => d.images.forEach((img) => URL.revokeObjectURL(img.previewUrl)));
    if (guidanceSentTimer) clearTimeout(guidanceSentTimer);
  });

  const hasImages = () => pendingImages().length > 0;
  // When the agent is running, the user can still type and send — this becomes
  // mid-loop guidance (not a new prompt). Images are not supported for guidance.
  const canSend = () => {
    if (isRunning()) return text().trim().length > 0;
    return text().trim().length > 0 || hasImages();
  };

  const handleSubmit = async (e: Event) => {
    e.preventDefault();
    const content = text().trim();
    if (!content) return;

    if (isRunning()) {
      // Mid-loop guidance: inject into the running loop. If no loop is running
      // (409), fall back to a normal prompt. The in-flight stream and tool are
      // always cancelled so the loop acts on the guidance immediately.
      const targetSessionId = session.activeSession()?.id;
      const accepted = await session.guidance(content, true);
      // Guard against session-switch race: if the user navigated to a different
      // session while the guidance request was in flight, don't show the
      // "Guidance sent" badge on the destination session.
      if (accepted && session.activeSession()?.id === targetSessionId) {
        setText('');
        if (textareaRef) textareaRef.style.height = '';
        setGuidanceSent(true);
        if (guidanceSentTimer) clearTimeout(guidanceSentTimer);
        guidanceSentTimer = setTimeout(() => setGuidanceSent(false), 2500);
      } else if (accepted) {
        // Session changed after guidance was accepted — still clear the input.
        setText('');
        if (textareaRef) textareaRef.style.height = '';
      } else {
        // No running loop — fall back to a normal prompt
        setText('');
        if (textareaRef) textareaRef.style.height = '';
        session.prompt(content);
      }
      return;
    }

    const images = pendingImages();
    if (images.length === 0 && !content) return;

    // Convert pending images to the API format
    const apiImages: ImagePartData[] = images.map((img) => ({
      mediaType: img.mediaType,
      data: img.data,
      name: img.name,
    }));

    // Clean up preview URLs
    images.forEach((img) => URL.revokeObjectURL(img.previewUrl));
    setPendingImages([]);
    setText('');
    if (textareaRef) textareaRef.style.height = '';
    session.prompt(content, apiImages);
  };

  const handleKeyDown = (e: KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.metaKey && !e.ctrlKey) {
      e.preventDefault();
      handleSubmit(e);
    }
  };

  const handleInput = (e: Event) => {
    const target = e.target as HTMLTextAreaElement;
    setText(target.value);
  };

  const isDisabled = () => isRunning();

  // ── Drag & drop ──
  // dragDepth counts enter/leave pairs: dragging across a child element fires
  // dragleave on the parent, so a plain boolean flickers the overlay off and on
  // as the pointer crosses the toolbar or the textarea.
  const [dragging, setDragging] = createSignal(false);
  let dragDepth = 0;

  // Images ride along with a new prompt, and mid-loop guidance is text-only —
  // so while the agent is running a drop would silently queue an attachment the
  // user could never send. Ignore drags entirely in that state.
  const hasFiles = (e: DragEvent) =>
    !isRunning() && Array.from(e.dataTransfer?.types ?? []).includes('Files');

  const handleDragEnter = (e: DragEvent) => {
    if (!hasFiles(e)) return;
    dragDepth++;
    setDragging(true);
  };
  const handleDragOver = (e: DragEvent) => {
    if (!hasFiles(e)) return;
    e.preventDefault();               // required for drop to fire at all
    if (e.dataTransfer) e.dataTransfer.dropEffect = 'copy';
  };
  const handleDragLeave = () => {
    dragDepth = Math.max(0, dragDepth - 1);
    if (dragDepth === 0) setDragging(false);
  };
  const handleDrop = (e: DragEvent) => {
    if (!hasFiles(e)) return;
    e.preventDefault();
    dragDepth = 0;
    setDragging(false);
    const files = e.dataTransfer?.files;
    if (files && files.length > 0) addFiles(files);
  };

  // Global keys:
  //   Escape  — cancel the running agent loop
  //   any printable character — start typing anywhere and the composer takes
  //     it, the way a terminal or a chat client does. The keystroke is not
  //     swallowed: focus moves during keydown, so the character itself lands
  //     in the textarea and nothing is lost.
  const handleGlobalKeyDown = (e: KeyboardEvent) => {
    if (e.key === 'Escape' && isRunning()) {
      e.preventDefault();
      e.stopPropagation();
      session.abort();
      return;
    }

    if (e.metaKey || e.ctrlKey || e.altKey) return;
    if (e.key.length !== 1) return;                 // arrows, F-keys, Tab, …
    const target = e.target as HTMLElement | null;
    if (!target) return;
    // Never steal from another field — the sidebar search, a rename box, the
    // command menu — or from the composer itself.
    const tag = target.tagName;
    if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || target.isContentEditable) return;
    if (window.getSelection()?.toString()) return;  // let a copy of selected text proceed
    textareaRef?.focus();
  };

  onMount(() => {
    document.addEventListener('keydown', handleGlobalKeyDown);
    // Publishes --kb-inset on <html> so this composer (and any other) can
    // pad above the on-screen keyboard. Idempotent across mounts.
    trackKeyboardInset();
    // The capture attribute (opens the camera directly on phones) is not in
    // Solid's JSX types, so set it on the element.
    cameraInputRef?.setAttribute('capture', 'environment');
  });
  onCleanup(() => {
    document.removeEventListener('keydown', handleGlobalKeyDown);
  });

  return (
    <div class="shrink-0 bg-gradient-to-t from-[color:var(--bg-base)] via-[color:var(--bg-base)] to-transparent pt-3 composer-safe">
      <form onSubmit={handleSubmit} class="chat-col px-4 md:px-8 pb-3">
        <div
          class="composer relative rounded-[1.25rem] border bg-[color:var(--bg-surface)] transition-[border-color,box-shadow] duration-200"
          classList={{ 'is-focused': focused(), 'is-dragging': dragging() }}
          onDragEnter={handleDragEnter}
          onDragOver={handleDragOver}
          onDragLeave={handleDragLeave}
          onDrop={handleDrop}
        >
          {/* Attach dialogue — opened by the + button. Rendered through a
              portal so it is not clipped by the composer's rounded box. */}
          <Show when={attachOpen()}>
            <Portal>
              <div class="fixed inset-0 z-[210]" onClick={() => setAttachOpen(false)} />
              <div
                class="attach-menu fixed z-[211] w-56 py-1 overflow-hidden rounded-xl
                       border border-[color:var(--border-default)] bg-[color:var(--bg-overlay)]
                       shadow-[0_16px_40px_rgba(0,0,0,0.5)] animate-fade-in"
                style={{
                  left: `${attachPos()?.left ?? 0}px`,
                  ...(attachPos()?.top !== undefined ? { top: `${attachPos()!.top}px` } : { bottom: `${attachPos()?.bottom ?? 0}px` }),
                }}
              >
                <button
                  type="button"
                  onClick={() => { setAttachOpen(false); fileInputRef?.click(); }}
                  class="w-full flex items-center gap-2.5 px-3 py-2 text-ui text-zinc-200
                         hover:bg-[color:var(--bg-hover)] transition-colors text-left"
                >
                  <svg class="w-4 h-4 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M2.25 15.75l5.159-5.159a2.25 2.25 0 013.182 0l5.159 5.159m-1.5-1.5l1.409-1.409a2.25 2.25 0 013.182 0l2.909 2.909m-18 3.75h16.5a1.5 1.5 0 001.5-1.5V6a1.5 1.5 0 00-1.5-1.5H3.75A1.5 1.5 0 002.25 6v12a1.5 1.5 0 001.5 1.5zm10.5-11.25h.008v.008h-.008V8.25zm.375 0a.375.375 0 11-.75 0 .375.375 0 01.75 0z" />
                  </svg>
                  <span>Choose image</span>
                </button>
                <button
                  type="button"
                  onClick={() => { setAttachOpen(false); cameraInputRef?.click(); }}
                  class="w-full flex items-center gap-2.5 px-3 py-2 text-ui text-zinc-200
                         hover:bg-[color:var(--bg-hover)] transition-colors text-left"
                >
                  <svg class="w-4 h-4 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M6.827 6.175A2.31 2.31 0 015.186 7.23c-.38.054-.757.112-1.134.175C2.999 7.58 2.25 8.507 2.25 9.574V18a2.25 2.25 0 002.25 2.25h15A2.25 2.25 0 0021.75 18V9.574c0-1.067-.75-1.994-1.802-2.169a47.865 47.865 0 00-1.134-.175 2.31 2.31 0 01-1.64-1.055l-.822-1.316a2.192 2.192 0 00-1.736-1.039 48.774 48.774 0 00-5.232 0 2.192 2.192 0 00-1.736 1.039l-.821 1.316z" />
                    <path stroke-linecap="round" stroke-linejoin="round" d="M16.5 12.75a4.5 4.5 0 11-9 0 4.5 4.5 0 019 0z" />
                  </svg>
                  <span>Take photo</span>
                </button>
                <div class="px-3 pt-1.5 pb-1 mt-0.5 border-t border-[color:var(--border-subtle)] text-micro text-[color:var(--text-muted)]">
                  or paste / drop an image
                </div>
              </div>
            </Portal>
          </Show>
          {/* Drop target overlay — only while a file is actually over the box */}
          <Show when={dragging()}>
            <div class="absolute inset-0 z-10 rounded-[1.25rem] flex items-center justify-center gap-2
                        pointer-events-none animate-fade-in
                        border-2 border-dashed border-[color:var(--accent)]
                        bg-[color:var(--bg-surface)]/92 text-[color:var(--accent)] text-ui font-medium">
              <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                <path stroke-linecap="round" stroke-linejoin="round" d="M12 16.5V9m0 0L8.25 12.75M12 9l3.75 3.75M3 16.5v.75A2.25 2.25 0 005.25 19.5h13.5A2.25 2.25 0 0021 17.25v-.75" />
              </svg>
              Drop images to attach
            </div>
          </Show>

          {/* Tool-permission request — surfaces at the very top of the composer */}
          <PermissionPrompt />

          {/* Image previews */}
          <Show when={hasImages()}>
            <div class="flex flex-wrap gap-2 px-3 pt-3">
              <For each={pendingImages()}>
                {(img, index) => (
                  <div class="relative group/img rounded-lg overflow-hidden border border-[color:var(--border-default)] bg-[color:var(--bg-elevated)]">
                    <img src={img.previewUrl} alt={img.name} class="h-16 w-16 object-cover" />
                    <button
                      type="button"
                      onClick={(e) => { e.stopPropagation(); removeImage(index()); }}
                      class="absolute top-0.5 right-0.5 w-5 h-5 rounded-full bg-black/60 hover:bg-black/80
                             flex items-center justify-center text-white/80 hover:text-white transition"
                      title="Remove image"
                    >
                      <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.4">
                        <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
                      </svg>
                    </button>
                    <div class="absolute bottom-0 left-0 right-0 px-1 py-0.5 bg-black/50 text-[8px] text-white/70 truncate">
                      {img.name}
                    </div>
                  </div>
                )}
              </For>
            </div>
          </Show>

          {/* Error message */}
          <Show when={imageError()}>
            <div class="px-4 pt-2 text-micro text-amber-400/80">{imageError()}</div>
          </Show>

          {/* Guidance-in-flight indicator */}
          <Show when={session.guidanceActive()}>
            <div class="px-4 pt-2 flex items-center gap-1.5 text-micro text-[color:var(--accent)]">
              <span class="inline-block w-1.5 h-1.5 rounded-full bg-[color:var(--accent)] animate-pulse" />
              Guidance queued — will be applied on the next loop iteration
            </div>
          </Show>

          {/* Textarea */}
          <textarea
            ref={textareaRef}
            value={text()}
            onInput={handleInput}
            onKeyDown={handleKeyDown}
            onPaste={handlePaste}
            onFocus={() => setFocused(true)}
            onBlur={() => setFocused(false)}
            placeholder={isRunning() ? "Agent is working… type to send mid-loop guidance" : "Ask anything, paste an error, or describe a task…"}
            rows={1}
            class="block w-full resize-none bg-transparent px-3.5 pt-3 pb-1 text-chat text-[color:var(--text-primary)]
                   placeholder:text-[color:var(--text-muted)] focus:outline-none
                   min-h-[2.25rem] max-h-[13.75rem] leading-[1.6]"
          />

          {/* Toolbar. flex-wrap so a 320px viewport can stack the selector
              row and the send controls rather than overflow. Approval mode and
              attachments sit leftmost; the model selector shares the right
              cluster with the round action button. */}
          <div class="flex flex-wrap items-center gap-1.5 px-2 pb-2 pt-0.5">
            {/* Attach — a + that opens the choose-image / take-photo dialogue,
                sitting just left of the approval-mode control */}
            <button
              ref={attachBtnRef}
              type="button"
              onClick={toggleAttach}
              disabled={isDisabled()}
              aria-haspopup="menu"
              aria-expanded={attachOpen()}
              title="Attach an image — or paste / drop one anywhere on the composer"
              aria-label="Attach image"
              class="icon-btn h-8 min-w-8 transition-colors"
              classList={{ 'is-open': attachOpen() }}
            >
              <svg class="w-4 h-4 transition-transform" classList={{ 'rotate-45': attachOpen() }} fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.9" aria-hidden="true">
                <path stroke-linecap="round" stroke-linejoin="round" d="M12 4.5v15m7.5-7.5h-15" />
              </svg>
            </button>

            {/* Approval mode: Ask (default), Auto (risk-gated), Yolo (unguarded) */}
            <PermissionModeToggle />

            <input
              ref={fileInputRef}
              type="file"
              accept={ACCEPTED_IMAGE_TYPES.join(',')}
              multiple
              onChange={handleFileSelect}
              class="hidden"
            />
            <input
              ref={cameraInputRef}
              type="file"
              accept="image/*"
              onChange={handleFileSelect}
              class="hidden"
            />

            <div class="flex-1" />

            {/* Guidance-sent confirmation badge */}
            <Show when={guidanceSent()}>
              <span class="text-micro font-medium text-[color:var(--accent)] select-none animate-fade-in">
                Guidance sent
              </span>
            </Show>

            <ModelSelector />

            {/* Round action button. While the agent is running it is the
                work-in-progress indicator: a pause glyph that stops the loop
                and the session when clicked, and the send arrow once there is
                guidance to send. Submitting while running injects mid-loop
                guidance and always cancels the in-flight stream/tool so the
                loop acts on it immediately. */}
            <Show
              when={isRunning()}
              fallback={
                <button
                  type="submit"
                  disabled={!canSend()}
                  aria-label="Send message"
                  title={canSend() ? 'Send (Enter)' : 'Type a message or attach an image'}
                  class="send-btn"
                  classList={{ 'is-ready': canSend() }}
                >
                  <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.4">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M12 19V5m0 0l-6 6m6-6l6 6" />
                  </svg>
                </button>
              }
            >
              {/* A button, not a submit, while there is nothing to send: the
                  pause glyph is the stop control. It becomes the submit form
                  control the moment there is guidance to deliver. */}
              <button
                type={canSend() ? 'submit' : 'button'}
                onClick={() => { if (!canSend()) session.abort(); }}
                aria-label={canSend() ? 'Send guidance' : 'Stop the agent'}
                title={canSend() ? 'Send mid-loop guidance (Enter) — cancels the current tool' : 'Stop the agent (Esc)'}
                class="send-btn"
                classList={{ 'is-ready': canSend(), 'is-stop': !canSend() }}
              >
                <Show
                  when={canSend()}
                  fallback={
                    <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.4" aria-hidden="true">
                      <path stroke-linecap="round" d="M9.5 5.5v13M14.5 5.5v13" />
                    </svg>
                  }
                >
                  <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.4">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M12 19V5m0 0l-6 6m6-6l6 6" />
                  </svg>
                </Show>
              </button>
            </Show>
          </div>
        </div>

        {/* Footer hint.
            One fixed-height row that swaps content instead of two stacked
            rows: the keyboard hints are only useful while you are typing, and
            the caveat is only worth reading when you are not. Reserving the
            height keeps the composer from shifting on focus. The whole row
            is display:none on touch (see .composer-hints in index.css) —
            touch keyboards have their own return key. */}
        <div class="composer-hints mt-1.5 h-4 flex items-center justify-center text-micro text-[color:var(--text-muted)]">
          <Show
            when={focused() || isRunning()}
            fallback={<span>ogcode may make mistakes — verify important output.</span>}
          >
            <div class="flex items-center gap-3.5 animate-fade-in">
              <span class="flex items-center gap-1"><kbd class="kbd">↵</kbd>send</span>
              <span class="flex items-center gap-1"><kbd class="kbd">⇧↵</kbd>newline</span>
              <Show when={isRunning()}>
                <span class="flex items-center gap-1"><kbd class="kbd">esc</kbd>stop</span>
              </Show>
            </div>
          </Show>
        </div>
      </form>
    </div>
  );
}