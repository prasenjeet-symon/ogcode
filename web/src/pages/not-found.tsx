import { useNavigate } from '@solidjs/router';
import { Show } from 'solid-js';
import { useServer } from '../context/server';
import SessionSidebar from '../components/session-sidebar';
import PlanSidebar from '../components/plan-sidebar';

function Sidebar() {
  const server = useServer();
  return (
    <Show when={server.mode() === 'plan'} fallback={<SessionSidebar />}>
      <PlanSidebar />
    </Show>
  );
}

interface NotFoundPanelProps {
  // Short headline, e.g. "Session not found".
  title?: string;
  // One-line explanation shown under the headline.
  message?: string;
  // Label for the primary action. Defaults to the mode-aware home label.
  actionLabel?: string;
  // Where the primary action goes. Defaults to the mode-aware home route.
  actionHref?: string;
}

// Centred "nothing here" panel. Rendered on its own by the catch-all route and
// embedded next to a sidebar by pages whose :id turned out not to exist, so a
// dead URL always leaves the user with a way out instead of a blank screen.
export function NotFoundPanel(props: NotFoundPanelProps) {
  const server = useServer();
  const navigate = useNavigate();

  const homeHref = () => (server.mode() === 'plan' ? '/plan' : '/');
  const homeLabel = () => (server.mode() === 'plan' ? 'Back to plans' : 'Back to home');
  const href = () => props.actionHref ?? homeHref();
  const label = () => props.actionLabel ?? homeLabel();

  return (
    <div class="flex-1 flex items-center justify-center px-6 bg-[color:var(--bg-base)]">
      <div class="flex flex-col items-center text-center max-w-sm">
        <div class="w-11 h-11 rounded-xl border border-[color:var(--border-subtle)] bg-[color:var(--bg-elevated)] flex items-center justify-center mb-4">
          <svg class="w-5 h-5 text-zinc-500" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.6">
            <path stroke-linecap="round" stroke-linejoin="round" d="M9.75 9.75l4.5 4.5m0-4.5l-4.5 4.5M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
          </svg>
        </div>
        <h1 class="text-[15px] font-semibold text-zinc-100">
          {props.title ?? 'Page not found'}
        </h1>
        <p class="mt-1.5 text-[13px] text-zinc-500 leading-relaxed">
          {props.message ?? "This URL doesn't match anything in ogcode. It may have been renamed or deleted."}
        </p>
        <button
          type="button"
          onClick={() => navigate(href(), { replace: true })}
          class="mt-5 h-8 px-3.5 rounded-lg text-[12px] font-medium text-zinc-100
                 border border-[color:var(--border-default)] hover:border-[color:var(--border-strong)]
                 bg-[color:var(--bg-surface)] hover:bg-[color:var(--bg-hover)] transition"
        >
          {label()}
        </button>
      </div>
    </div>
  );
}

// Catch-all route target for any URL the router doesn't match.
export default function NotFound() {
  return (
    <div class="flex h-dvh w-full">
      <Sidebar />
      <NotFoundPanel />
    </div>
  );
}
