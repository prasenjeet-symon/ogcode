import { For, Show, createSignal, createMemo, createEffect, onMount } from 'solid-js';
import { getVersion, checkForUpdate, type VersionResponse } from '../../api/client';
import Logo from '../../components/logo';
import {
  Group,
  Row,
  Button,
  LinkAction,
  Value,
  StatusChip,
  Banner,
  CopyButton,
  Spinner,
  matches,
  useShell,
} from './ui';

// goreleaser stamps "none"/"unknown" into commit and date for builds made
// outside a release, so those strings are absence, not data — a row reading
// "Commit  none" is worse than no row at all.
function real(raw: string | undefined): string | null {
  const v = (raw || '').trim();
  if (!v || v === 'none' || v === 'unknown' || v === 'dev') return null;
  return v;
}

/** The running version arrives with a leading "v" and the release tag with one
 *  too; one normaliser keeps the pair comparable at a glance. */
function tag(raw: string | null | undefined): string | null {
  const v = real(raw || '');
  return v ? `v${v.replace(/^v/, '')}` : null;
}

function fmtDate(raw: string | undefined): string | null {
  const v = real(raw);
  if (!v) return null;
  const d = new Date(v);
  if (Number.isNaN(d.getTime())) return null;
  return d.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
}

const LINKS = [
  {
    title: 'GitHub',
    helper: 'Source, issues and releases.',
    href: 'https://github.com/prasenjeet-symon/ogcode',
    icon: 'M12 2C6.477 2 2 6.484 2 12.017c0 4.425 2.865 8.18 6.839 9.504.5.092.682-.217.682-.483 0-.237-.008-.868-.013-1.703-2.782.605-3.369-1.343-3.369-1.343-.454-1.158-1.11-1.466-1.11-1.466-.908-.62.069-.608.069-.608 1.003.07 1.531 1.032 1.531 1.032.892 1.53 2.341 1.088 2.91.832.092-.647.35-1.088.636-1.338-2.22-.253-4.555-1.113-4.555-4.951 0-1.093.39-1.988 1.029-2.688-.103-.253-.446-1.272.098-2.65 0 0 .84-.27 2.75 1.026A9.564 9.564 0 0112 6.844c.85.004 1.705.115 2.504.337 1.909-1.296 2.747-1.027 2.747-1.027.546 1.379.203 2.398.1 2.651.64.7 1.028 1.595 1.028 2.688 0 3.848-2.339 4.695-4.566 4.943.359.309.678.919.678 1.852 0 1.336-.012 2.415-.012 2.743 0 .268.18.58.688.482A10.02 10.02 0 0022 12.017C22 6.484 17.522 2 12 2z',
  },
  {
    title: 'Discord',
    helper: 'Ask questions and share what you build.',
    href: 'https://discord.gg/JQP9t8y2Zv',
    icon: 'M8.25 6.75h12M8.25 12h12m-12 5.25h12M3.75 6.75h.007v.008H3.75V6.75zm.375 0a.375.375 0 11-.75 0 .375.375 0 01.75 0zM3.75 12h.007v.008H3.75V12zm.375 0a.375.375 0 11-.75 0 .375.375 0 01.75 0zm-.375 5.25h.007v.008H3.75v-.008zm.375 0a.375.375 0 11-.75 0 .375.375 0 01.75 0z',
  },
  {
    title: 'ogcode.xyz',
    helper: 'Install script and documentation.',
    href: 'https://ogcode.xyz',
    icon: 'M12 21a9 9 0 100-18 9 9 0 000 18zm0 0c2.485 0 4.5-4.03 4.5-9S14.485 3 12 3 7.5 7.03 7.5 12s2.015 9 4.5 9zm-8.716-5.25h17.432M3.284 8.25h17.432',
  },
];

const HIGHLIGHTS = [
  {
    title: 'Local-first',
    body: 'The agent runs on your machine with full read and write access to your project.',
    icon: 'M3.75 9.75l7.5-6 7.5 6m-13.5 0v9a1.5 1.5 0 001.5 1.5h3.75v-6h4.5v6h3.75a1.5 1.5 0 001.5-1.5v-9m-13.5 0H3m18 0h-1.5',
  },
  {
    title: 'Bring your own model',
    body: 'Connect Anthropic, OpenAI, OpenRouter, Ollama, or any OpenAI-compatible endpoint.',
    icon: 'M9.813 15.904L9 18.75l-.813-2.846a4.5 4.5 0 00-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 003.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 003.09 3.09L15.75 12l-2.847.813a4.5 4.5 0 00-3.09 3.091z',
  },
  {
    title: 'Persistent sessions',
    body: 'Every conversation is saved and resumable. Pick up where you left off, anytime.',
    icon: 'M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z',
  },
  {
    title: 'Keyboard-first',
    body: 'Built for the terminal mindset. Send, abort and switch sessions without leaving home row.',
    icon: 'M6.75 3.75h.008v.008H6.75v-.008zM6.75 7.5h.008v.008H6.75V7.5zm0 3.75h.008v.008H6.75v-.008zM10.5 3.75h.008v.008H10.5v-.008zM10.5 7.5h.008v.008H10.5V7.5zm0 3.75h.008v.008H10.5v-.008zM14.25 3.75h.008v.008h-.008v-.008zM14.25 7.5h.008v.008h-.008V7.5zm0 3.75h.008v.008h-.008v-.008zM17.25 3.75h.008v.008h-.008v-.008zM17.25 7.5h.008v.008h-.008V7.5zm0 3.75h.008v.008h-.008v-.008zM4.5 18.75h15a.75.75 0 00.75-.75v-1.5a.75.75 0 00-.75-.75h-15a.75.75 0 00-.75.75v1.5a.75.75 0 00.75.75z',
  },
];

export default function AboutSettings() {
  const shell = useShell();
  const [info, setInfo] = createSignal<VersionResponse | null>(null);
  const [checking, setChecking] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);

  createEffect(() => shell.report({ noun: 'entries' }));
  const hide = (...text: (string | undefined)[]) => !matches(shell.query(), ...text);

  onMount(async () => {
    try {
      setInfo(await getVersion());
      setError(null);
    } catch (err) {
      console.error('Failed to fetch version:', err);
      setError('Could not reach the ogcode server.');
    }
  });

  const check = async () => {
    setChecking(true);
    setError(null);
    try {
      await checkForUpdate();
      setInfo(await getVersion());
    } catch (err) {
      console.error('Failed to check for updates:', err);
      setError('Update check failed. Are you online?');
    } finally {
      setChecking(false);
    }
  };

  const installed = createMemo(() => tag(info()?.version));
  const latest = createMemo(() => tag(info()?.latestVersion));
  const hasUpdate = createMemo(() => info()?.updateAvailable ?? false);
  const commit = createMemo(() => real(info()?.commit));
  const built = createMemo(() => fmtDate(info()?.date));
  const goVersion = createMemo(() => real(info()?.goVersion));

  return (
    <>
      {/* Identity card — the app introducing itself, the way an About screen
          does before it gets to the details. */}
      <div class="pb-2 pt-2 text-center">
        <div class="relative inline-flex mb-3">
          <div class="absolute inset-0 rounded-[6px] bg-[color:var(--accent)] blur-xl opacity-25" />
          <div class="relative w-12 h-12 rounded-[6px] bg-[color:var(--accent)] flex items-center justify-center">
            <Logo class="w-6 h-6 text-[color:var(--on-primary)]" />
          </div>
        </div>
        <h2 class="text-[1.125rem] font-semibold tracking-[-0.02em] text-[color:var(--text-primary)]">ogcode</h2>
        <p class="mt-1.5 text-meta leading-[1.55] text-[color:var(--text-tertiary)] max-w-[30rem] mx-auto">
          A coding agent at home in your terminal, with a fast local web UI to drive it. Your code never
          leaves the machine except to reach the model you chose.
        </p>
        <div class="mt-3 flex items-center justify-center gap-2 flex-wrap">
          <Show when={installed()} fallback={<Spinner class="w-4 h-4 text-[color:var(--text-muted)]" />}>
            <span class="font-mono text-micro text-[color:var(--text-tertiary)]">{installed()}</span>
          </Show>
          <Show when={hasUpdate()} fallback={
            <Show when={info() && !error()}>
              <StatusChip tone="ok">Up to date</StatusChip>
            </Show>
          }>
            <StatusChip tone="warn" pulse>Update available</StatusChip>
          </Show>
        </div>
      </div>

      <Group
        id="version"
        title="Version"
        icon="M4.5 12.75l6 6 9-13.5"
        action={
          <Button variant="outlined" onClick={check} disabled={checking()}>
            <Show
              when={checking()}
              fallback={
                <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
                </svg>
              }
            >
              <Spinner class="w-3.5 h-3.5" />
            </Show>
            {checking() ? 'Checking…' : 'Check for updates'}
          </Button>
        }
      >
        <Row label="Installed" helper="The ogcode binary serving this window." hidden={hide('Installed version binary')}>
          <Value>{installed() ?? '—'}</Value>
        </Row>
        <Row label="Latest release" helper="The newest version published upstream." hidden={hide('Latest release update')}>
          <Show when={latest()} fallback={<Value tone="muted">Unknown</Value>}>
            <span class="font-mono text-meta" style={hasUpdate() ? { color: 'var(--warning)' } : {}}>
              {latest()}
            </span>
          </Show>
          <Show when={info()?.releaseUrl}>
            <LinkAction href={info()!.releaseUrl}>Notes</LinkAction>
          </Show>
        </Row>
        <Show when={error()}>
          <Row label="Update check" stacked hidden={hide('Update check error')}>
            <Banner tone="danger">{error()}</Banner>
          </Row>
        </Show>
        <Show when={hasUpdate() && info()?.installCommand}>
          <Row
            label="Update command"
            helper="Run this in a terminal, then restart ogcode."
            stacked
            hidden={hide('Update command install upgrade curl')}
          >
            <div class="flex items-center gap-2">
              <code class="flex-1 min-w-0 truncate h-8 px-2.5 inline-flex items-center rounded-[3px]
                           bg-[color:var(--bg-elevated)] border border-[color:var(--border-subtle)]
                           font-mono text-meta text-[color:var(--text-primary)]">
                <span class="text-[color:var(--text-muted)] select-none">$&nbsp;</span>
                {info()!.installCommand}
              </code>
              <CopyButton text={info()!.installCommand} />
            </div>
          </Row>
        </Show>
      </Group>

      <Group
        id="build"
        title="Build"
        icon="M11.42 15.17L17.25 21A2.652 2.652 0 0021 17.25l-5.877-5.877M11.42 15.17l2.496-3.03c.317-.384.74-.626 1.208-.766M11.42 15.17l-4.655 5.653a2.548 2.548 0 11-3.586-3.586l6.837-5.63m5.108-.233c.55-.164 1.163-.188 1.743-.14a4.5 4.5 0 004.486-6.336l-3.276 3.277a3.004 3.004 0 01-2.25-2.25l3.276-3.276a4.5 4.5 0 00-6.336 4.486c.091 1.076-.071 2.264-.904 2.95l-.102.085m-1.745 1.437L5.909 7.5H4.5L2.25 3.75l1.5-1.5L7.5 4.5v1.409l4.26 4.26m-1.745 1.437l1.745-1.437m6.615 8.206L15.75 15.75M4.867 19.125h.008v.008h-.008v-.008z"
      >
        <Show when={built()}>
          <Row label="Built" hidden={hide('Built date')}>
            <Value mono={false}>{built()}</Value>
          </Row>
        </Show>
        <Show when={commit()}>
          <Row label="Commit" hidden={hide('Commit revision sha git')}>
            <Value>{commit()!.slice(0, 12)}</Value>
            <CopyButton text={commit()!} label="" />
          </Row>
        </Show>
        <Show when={goVersion()}>
          <Row label="Engine" hidden={hide('Engine go runtime')}>
            <Value>Go {goVersion()}</Value>
          </Row>
        </Show>
        <Row label="Interface" hidden={hide('Interface solidjs vite tailwind frontend')}>
          <Value mono={false}>SolidJS · Vite · Tailwind</Value>
        </Row>
        <Row label="License" hidden={hide('License MIT open source')}>
          <Value mono={false}>MIT</Value>
        </Row>
      </Group>

      <Group
        id="resources"
        title="Resources"
        icon="M13.19 8.688a4.5 4.5 0 011.242 7.244l-4.5 4.5a4.5 4.5 0 01-6.364-6.364l1.757-1.757m13.35-.622l1.757-1.757a4.5 4.5 0 00-6.364-6.364l-4.5 4.5a4.5 4.5 0 001.242 7.244"
      >
        <For each={LINKS}>
          {(l) => (
            <Row
              label={l.title}
              helper={l.helper}
              icon={l.icon}
              onClick={() => window.open(l.href, '_blank', 'noopener,noreferrer')}
              hidden={hide(l.title, l.helper, 'link resource')}
            />
          )}
        </For>
      </Group>

      <Group
        id="highlights"
        title="What you get"
        icon="M11.48 3.499a.562.562 0 011.04 0l2.125 5.111a.563.563 0 00.475.345l5.518.442c.499.04.701.663.321.988l-4.204 3.602a.563.563 0 00-.182.557l1.285 5.385a.562.562 0 01-.84.61l-4.725-2.885a.563.563 0 00-.586 0L6.982 20.54a.562.562 0 01-.84-.61l1.285-5.386a.562.562 0 00-.182-.557l-4.204-3.602a.562.562 0 01.321-.988l5.518-.442a.563.563 0 00.475-.345L11.48 3.5z"
      >
        <For each={HIGHLIGHTS}>
          {(h) => <Row label={h.title} helper={h.body} icon={h.icon} hidden={hide(h.title, h.body)} />}
        </For>
      </Group>
    </>
  );
}
