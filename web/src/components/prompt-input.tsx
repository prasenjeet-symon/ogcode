import { createSignal, createEffect, Show, For, onCleanup, onMount } from 'solid-js';
import { useSession } from '../context/session';
import { type ImagePartData } from '../api/client';
import ModelSelector from './model-selector';

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
  let textareaRef: HTMLTextAreaElement | undefined;
  let fileInputRef: HTMLInputElement | undefined;
  let guidanceSentTimer: ReturnType<typeof setTimeout> | null = null;

  // Auto-resize textarea
  createEffect(() => {
    text();
    if (textareaRef) {
      textareaRef.style.height = 'auto';
      textareaRef.style.height = Math.min(textareaRef.scrollHeight, 240) + 'px';
    }
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
      // (409), fall back to a normal prompt. Always cancel the current tool so
      // the loop can act on the guidance immediately.
      const accepted = await session.guidance(content, true);
      if (accepted) {
        setText('');
        if (textareaRef) textareaRef.style.height = 'auto';
        setGuidanceSent(true);
        if (guidanceSentTimer) clearTimeout(guidanceSentTimer);
        guidanceSentTimer = setTimeout(() => setGuidanceSent(false), 2500);
      } else {
        // No running loop — fall back to a normal prompt
        setText('');
        if (textareaRef) textareaRef.style.height = 'auto';
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
    if (textareaRef) textareaRef.style.height = 'auto';
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

  // Global Escape key handler to cancel the running agent loop
  const handleGlobalKeyDown = (e: KeyboardEvent) => {
    if (e.key === 'Escape' && isRunning()) {
      e.preventDefault();
      e.stopPropagation();
      session.abort();
    }
  };

  onMount(() => {
    document.addEventListener('keydown', handleGlobalKeyDown);
  });
  onCleanup(() => {
    document.removeEventListener('keydown', handleGlobalKeyDown);
  });

  return (
    <div class="shrink-0 bg-gradient-to-t from-[color:var(--bg-base)] via-[color:var(--bg-base)] to-transparent pt-4">
      <form onSubmit={handleSubmit} class="max-w-3xl mx-auto px-4 md:px-6 pb-4">
        <div
          class={`rounded-2xl border bg-[color:var(--bg-surface)] transition-all var(--spring-md)
            ${focused()
              ? 'border-[color:var(--accent)]/40 shadow-lg shadow-black/30 shadow-[color:var(--accent)]/5'
              : 'border-[color:var(--border-default)] shadow-md shadow-black/20'
            }`}
        >
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
                             flex items-center justify-center text-white/80 hover:text-white transition var(--spring-sm)"
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
            <div class="px-4 pt-2 text-[11px] text-amber-400/80">{imageError()}</div>
          </Show>

          {/* Guidance-in-flight indicator */}
          <Show when={session.guidanceActive()}>
            <div class="px-4 pt-2 flex items-center gap-1.5 text-[11px] text-[color:var(--accent)]/80">
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
            placeholder={isRunning() ? "Agent is working… type to send mid-loop guidance (cancels current tool)" : "Ask anything, paste an error, or describe a task…"}
            rows={1}
            class="block w-full resize-none bg-transparent px-4 pt-3.5 pb-1 text-[14px] text-zinc-100
                   placeholder-zinc-500 focus:outline-none
                   min-h-[44px] max-h-[240px] leading-relaxed"
          />

          {/* Toolbar */}
          <div class="flex items-center gap-2 px-2.5 pb-2.5 pt-1">
            <ModelSelector />

            {/* Image upload button */}
            <button
              type="button"
              onClick={() => fileInputRef?.click()}
              disabled={isDisabled()}
              title="Attach images"
              class="h-8 w-8 rounded-lg flex items-center justify-center transition-all var(--spring-sm)
                     text-zinc-400 hover:text-zinc-200 hover:bg-[color:var(--bg-hover)]
                     disabled:opacity-40 disabled:cursor-not-allowed active:scale-[0.95]"
            >
              <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
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
              <span class="text-[11px] font-medium text-[color:var(--accent)]/80 select-none animate-pulse">
                Guidance sent
              </span>
            </Show>

            <Show when={isRunning()}>
              <button
                type="button"
                onClick={() => session.abort()}
                class="h-8 px-3 rounded-lg flex items-center gap-1.5 text-[12px] font-medium
                       text-red-400 hover:text-red-300 hover:bg-red-500/10 border border-red-500/30
                       transition-all var(--spring-sm) active:scale-[0.97]"
                title="Cancel agent (Esc)"
              >
                <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
                </svg>
                Cancel
              </button>
              {/* Send-as-guidance button: visible while running so the user has
                  an explicit affordance that submitting injects mid-loop guidance. */}
              <button
                type="submit"
                disabled={!canSend()}
                title={canSend() ? 'Send mid-loop guidance (Enter)' : 'Type guidance to send'}
                class={`h-8 w-8 rounded-lg flex items-center justify-center transition-all var(--spring-sm)
                  ${canSend()
                    ? 'bg-[color:var(--accent)]/80 hover:bg-[color:var(--accent)] text-[color:var(--on-primary)] shadow-sm hover:scale-[1.06] active:scale-[0.95]'
                    : 'bg-[color:var(--bg-elevated)] text-zinc-600 cursor-not-allowed scale-95'
                  }`}
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
                title={canSend() ? 'Send (Enter)' : 'Type a message or attach an image'}
                class={`h-8 w-8 rounded-lg flex items-center justify-center transition-all var(--spring-sm)
                  ${canSend()
                    ? 'bg-[color:var(--accent)] hover:bg-[color:var(--accent-hover)] text-[color:var(--on-primary)] shadow-sm hover:shadow-md hover:scale-[1.06] active:scale-[0.95]'
                    : 'bg-[color:var(--bg-elevated)] text-zinc-600 cursor-not-allowed scale-95'
                  }`}
              >
                <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.4">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M12 19V5m0 0l-6 6m6-6l6 6" />
                </svg>
              </button>
            </Show>
          </div>
        </div>

        {/* Footer hint */}
        <div class="mt-2 flex items-center justify-center gap-4 text-[10.5px] text-zinc-600">
          <span class="flex items-center gap-1">
            <kbd class="px-1 py-[1px] rounded border border-[color:var(--border-default)] bg-[color:var(--bg-elevated)] font-mono text-[9.5px]">↵</kbd>
            send
          </span>
          <span class="flex items-center gap-1">
            <kbd class="px-1 py-[1px] rounded border border-[color:var(--border-default)] bg-[color:var(--bg-elevated)] font-mono text-[9.5px]">⇧ ↵</kbd>
            newline
          </span>
          <Show when={isRunning()}>
            <span class="flex items-center gap-1">
              <kbd class="px-1 py-[1px] rounded border border-[color:var(--border-default)] bg-[color:var(--bg-elevated)] font-mono text-[9.5px]">Esc</kbd>
              cancel
            </span>
          </Show>
          <span class="text-zinc-700">ogcode may make mistakes — verify important output.</span>
        </div>
      </form>
    </div>
  );
}