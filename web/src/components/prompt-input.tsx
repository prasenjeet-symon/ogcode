import { createSignal, createEffect, Show, For, onCleanup, onMount } from 'solid-js';
import { useSession } from '../context/session';
import { type ImagePartData } from '../api/client';
import ModelSelector from './model-selector';
import PermissionPrompt from './permission-prompt';
import PermissionModeToggle from './permission-mode-toggle';

// Maximum image file size: 10 MB
const MAX_IMAGE_SIZE = 10 * 1024 * 1024;
const ACCEPTED_IMAGE_TYPES = ['image/jpeg', 'image/png', 'image/webp', 'image/gif'];

interface PendingImage {
  mediaType: string;
  data: string;       // base64 (without data: prefix)
  name: string;
  previewUrl: string; // object URL for thumbnail
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
  // Whether to cancel the in-flight tool when sending mid-loop guidance. The
  // user can toggle this with a checkbox that appears while the agent is running.
  // Defaults to true (cancel) so guidance is acted on immediately.
  const [cancelTool, setCancelTool] = createSignal(true);
  let textareaRef: HTMLTextAreaElement | undefined;
  let fileInputRef: HTMLInputElement | undefined;
  let guidanceSentTimer: ReturnType<typeof setTimeout> | null = null;

  // Clear per-session transient state when the active session changes.
  // PromptInput stays mounted across session switches (the route param changes
  // but the component is reused), so local signals like guidanceSent would
  // otherwise linger on the destination session's UI.
  createEffect(() => {
    session.activeSession()?.id;
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
    pendingImages().forEach((img) => URL.revokeObjectURL(img.previewUrl));
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
      // (409), fall back to a normal prompt. The user controls whether the
      // currently-running tool is cancelled via the cancelTool checkbox.
      const targetSessionId = session.activeSession()?.id;
      const accepted = await session.guidance(content, cancelTool());
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
  });
  onCleanup(() => {
    document.removeEventListener('keydown', handleGlobalKeyDown);
  });

  return (
    <div class="shrink-0 bg-gradient-to-t from-[color:var(--bg-base)] via-[color:var(--bg-base)] to-transparent pt-3">
      <form onSubmit={handleSubmit} class="chat-col px-4 md:px-8 pb-3">
        <div
          class="composer relative rounded-[1.25rem] border bg-[color:var(--bg-surface)] transition-[border-color,box-shadow] duration-200"
          classList={{ 'is-focused': focused(), 'is-dragging': dragging() }}
          onDragEnter={handleDragEnter}
          onDragOver={handleDragOver}
          onDragLeave={handleDragLeave}
          onDrop={handleDrop}
        >
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

          {/* Toolbar */}
          <div class="flex items-center gap-1.5 px-2 pb-2 pt-0.5">
            <ModelSelector />

            {/* Approval mode: Ask (default) vs Auto (risk-gated) */}
            <PermissionModeToggle />

            {/* Image upload button */}
            <button
              type="button"
              onClick={() => fileInputRef?.click()}
              disabled={isDisabled()}
              title="Attach images — or drop them anywhere on the composer"
              aria-label="Attach images"
              class="icon-btn h-8 min-w-8 transition-colors"
            >
              <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8">
                <path stroke-linecap="round" stroke-linejoin="round" d="M2.25 15.75l5.159-5.159a2.25 2.25 0 013.182 0l5.159 5.159m-1.5-1.5l1.409-1.409a2.25 2.25 0 013.182 0l2.909 2.909m-18 3.75h16.5a1.5 1.5 0 001.5-1.5V6a1.5 1.5 0 00-1.5-1.5H3.75A1.5 1.5 0 002.25 6v12a1.5 1.5 0 001.5 1.5zm10.5-11.25h.008v.008h-.008V8.25zm.375 0a.375.375 0 11-.75 0 .375.375 0 01.75 0z" />
              </svg>
            </button>
            <input
              ref={fileInputRef}
              type="file"
              accept={ACCEPTED_IMAGE_TYPES.join(',')}
              multiple
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

            <Show when={isRunning()}>
              {/* Cancel-in-flight checkbox: when checked (default), sending mid-loop
                  guidance cancels the currently-running LLM stream AND any running
                  tool so the loop acts on the guidance immediately. Uncheck to let
                  the current generation/tool finish naturally — the guidance still
                  applies on the next iteration. */}
              <label
                class="flex items-center gap-1.5 text-micro text-[color:var(--text-tertiary)] hover:text-[color:var(--text-secondary)] cursor-pointer select-none transition-colors h-8 px-1.5 rounded-lg hover:bg-[color:var(--bg-hover)]"
                title={cancelTool() ? 'The running LLM stream and tools will be cancelled when you send guidance' : 'The current generation/tool will be allowed to finish before guidance is applied'}
              >
                <input
                  type="checkbox"
                  checked={cancelTool()}
                  onChange={(e) => setCancelTool((e.target as HTMLInputElement).checked)}
                  class="w-3.5 h-3.5 accent-[color:var(--accent)] cursor-pointer"
                />
                <span>Cancel current work</span>
              </label>
              <button
                type="button"
                onClick={() => session.abort()}
                class="h-8 px-2.5 rounded-lg flex items-center gap-1.5 text-meta font-medium
                       text-red-400 hover:text-red-300 hover:bg-red-500/10 border border-red-500/25
                       transition-colors"
                title="Cancel agent (Esc)"
              >
                <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
                </svg>
                Stop
              </button>
              {/* Send-as-guidance button: visible while running so the user has
                  an explicit affordance that submitting injects mid-loop guidance. */}
              <button
                type="submit"
                disabled={!canSend()}
                aria-label="Send guidance"
                title={canSend() ? `Send mid-loop guidance (Enter)${cancelTool() ? ' — cancels current tool' : ''}` : 'Type guidance to send'}
                class="send-btn"
                classList={{ 'is-ready': canSend() }}
              >
                <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.4">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M12 19V5m0 0l-6 6m6-6l6 6" />
                </svg>
              </button>
            </Show>

            <Show when={!isRunning()}>
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
            </Show>
          </div>
        </div>

        {/* Footer hint.
            One fixed-height row that swaps content instead of two stacked
            rows: the keyboard hints are only useful while you are typing, and
            the caveat is only worth reading when you are not. Reserving the
            height keeps the composer from shifting on focus. */}
        <div class="mt-1.5 h-4 flex items-center justify-center text-micro text-[color:var(--text-muted)]">
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