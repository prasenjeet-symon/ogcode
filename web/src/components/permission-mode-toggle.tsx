import { useSession } from '../context/session';

// A compact segmented control in the composer toolbar that switches how tool
// calls are gated for the active session:
//   Ask  — prompt before every bash / write / edit (default, safest)
//   Auto — auto-run low-risk commands; still ask for risky ones (the backend
//          classifies risk with rules + an LLM check for the unclear middle).
export default function PermissionModeToggle() {
  const session = useSession();
  const mode = () => session.permissionMode();

  const pill = (active: boolean) =>
    `inline-flex h-[26px] items-center gap-1.5 rounded-md px-2 text-meta font-medium transition-all ${
      active
        ? 'bg-[color:var(--bg-elevated)] text-[color:var(--text-primary)] shadow-sm'
        : 'text-[color:var(--text-tertiary)] hover:text-[color:var(--text-secondary)]'
    }`;

  return (
    <div
      class="inline-flex h-8 shrink-0 items-center gap-0.5 rounded-lg border border-[color:var(--border-default)] bg-[color:var(--bg-base)] p-[3px]"
      role="group"
      aria-label="Command approval mode"
    >
      <button
        type="button"
        onClick={() => session.setPermissionMode('ask')}
        aria-pressed={mode() === 'ask'}
        class={pill(mode() === 'ask')}
        title="Ask — approve every command, file write, and edit before it runs (default, safest)"
      >
        <svg class="w-[13px] h-[13px]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
          <path stroke-linecap="round" stroke-linejoin="round" d="M9 12.75L11.25 15 15 9.75m-3-7.036A11.96 11.96 0 013.598 6 11.99 11.99 0 003 9.75c0 5.592 3.824 10.29 9 11.623 5.176-1.332 9-6.03 9-11.622 0-1.31-.21-2.571-.598-3.751h-.152A11.96 11.96 0 0112 2.714z" />
        </svg>
        Ask
      </button>
      <button
        type="button"
        onClick={() => session.setPermissionMode('auto')}
        aria-pressed={mode() === 'auto'}
        class={pill(mode() === 'auto')}
        title="Auto — run safe commands automatically; still ask for risky ones (rm, git push, sudo, writes outside the project, …)"
      >
        <svg class="w-[13px] h-[13px]" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
          <path stroke-linecap="round" stroke-linejoin="round" d="M3.75 13.5l10.5-11.25L12 10.5h8.25L9.75 21.75 12 13.5H3.75z" />
        </svg>
        Auto
      </button>
    </div>
  );
}
