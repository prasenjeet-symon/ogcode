/**
 * The pill at the top of a transcript while an older page is on its way. An
 * overlay rather than a row, so it never changes the height above the reader
 * and leaves the scroll anchoring nothing to account for.
 */
export default function LoadingOlder() {
  return (
    <div class="pointer-events-none absolute inset-x-0 top-3 flex justify-center" role="status" aria-live="polite">
      <div class="inline-flex items-center h-7 px-3 rounded-full border border-[color:var(--border-subtle)] bg-[color:var(--bg-overlay)]/90 backdrop-blur-sm text-micro font-medium shadow-lg shadow-black/30 anim-enter">
        <span class="sweep-text">Loading earlier messages</span>
      </div>
    </div>
  );
}
