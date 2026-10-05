import { For, Show, createSignal, createMemo, createEffect, onMount } from 'solid-js';
import { Dynamic } from 'solid-js/web';
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
    title: 'ogcode.in',
    helper: 'Install script and documentation.',
    href: 'https://ogcode.in',
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

// The rooms where everyone hangs out — the same three the dashboard links to.
// Telegram leads the set; all three are plain links out, nothing metered.
const COMMUNITY = [
  {
    name: 'Telegram',
    sub: 'group',
    desc: 'Builders, founders, and developers shipping cool things with OGcode and OGX — the main room for news, model drops, and help from the team.',
    action: 'Join the Telegram group',
    href: 'https://t.me/+FZN0ENsFSzowMjI1',
    color: '#229ED9',
    Mark: TelegramMark,
  },
  {
    name: 'WhatsApp',
    sub: 'group',
    desc: 'The same builders and founders, a quieter pace. Good place to lurk for the same updates with fewer pings.',
    action: 'Join the WhatsApp group',
    href: 'https://chat.whatsapp.com/KN71lswguCyLvxHWdMCtHn',
    color: '#25D366',
    Mark: WhatsAppMark,
  },
  {
    name: 'Reddit',
    sub: 'community',
    desc: 'Longer conversations, questions, and things people have built. Good for a search before you ask.',
    action: 'Open r/ogcode',
    href: 'https://www.reddit.com/r/ogcode/',
    color: '#ff4500',
    Mark: RedditMark,
  },
];

// The rooms' own glyphs, filled rather than stroked — brand marks, not the
// app's line icons. They inherit `currentColor` from the tile that wraps them.
function TelegramMark() {
  return (
    <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
      <path d="M21.94 4.6 18.9 19.03c-.23 1.02-.84 1.27-1.7.79l-4.7-3.46-2.27 2.18c-.25.25-.46.46-.94.46l.33-4.77 8.68-7.84c.38-.34-.08-.52-.58-.19L6.99 13.13l-4.6-1.44c-1-.31-1.02-1 .21-1.48l17.98-6.93c.83-.31 1.56.2 1.36 1.32Z" />
    </svg>
  );
}

function WhatsAppMark() {
  return (
    <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
      <path d="M12.04 2C6.58 2 2.13 6.45 2.13 11.91c0 1.75.46 3.45 1.32 4.95L2 22l5.25-1.38a9.9 9.9 0 0 0 4.79 1.22h.01c5.46 0 9.91-4.45 9.91-9.91 0-2.65-1.03-5.14-2.9-7.01A9.83 9.83 0 0 0 12.04 2Zm0 18.02h-.01a8.2 8.2 0 0 1-4.19-1.15l-.3-.18-3.12.82.83-3.04-.2-.31a8.23 8.23 0 0 1-1.26-4.35c0-4.54 3.7-8.24 8.25-8.24 2.2 0 4.27.86 5.83 2.42a8.19 8.19 0 0 1 2.41 5.83c0 4.54-3.7 8.2-8.24 8.2Zm4.52-6.16c-.25-.12-1.47-.72-1.69-.81-.23-.08-.39-.12-.56.13-.16.25-.64.81-.79.97-.14.16-.29.18-.54.06-.25-.12-1.05-.39-1.99-1.23-.74-.66-1.23-1.47-1.38-1.72-.14-.25-.02-.38.11-.51.11-.11.25-.29.37-.43.13-.14.17-.25.25-.41.08-.16.04-.31-.02-.43-.06-.12-.56-1.34-.76-1.84-.2-.48-.4-.42-.56-.43h-.48c-.16 0-.43.06-.66.31-.22.25-.86.85-.86 2.07 0 1.22.89 2.4 1.01 2.56.12.16 1.74 2.66 4.22 3.73.59.26 1.05.41 1.41.52.59.19 1.13.16 1.56.1.48-.07 1.47-.6 1.68-1.18.21-.58.21-1.07.14-1.18-.06-.1-.22-.16-.47-.28Z" />
    </svg>
  );
}

// Reddit's alien mark: a head with ears and an antenna, its eyes and smile
// cut back to the platform's orange so they read as the platform's face.
function RedditMark() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path fill="currentColor" d="M13.3 2.4 14.9 3l-1.7 3.6-1.6-.75z" />
      <circle cx="14.1" cy="2.6" r="1.5" fill="currentColor" />
      <circle cx="4.3" cy="13.2" r="1.6" fill="currentColor" />
      <circle cx="19.7" cy="13.2" r="1.6" fill="currentColor" />
      <ellipse cx="12" cy="14" rx="7.9" ry="6.1" fill="currentColor" />
      <circle cx="9.3" cy="13" r="1.35" fill="#ff4500" />
      <circle cx="14.7" cy="13" r="1.35" fill="#ff4500" />
      <path fill="none" stroke="#ff4500" stroke-width="1.15" stroke-linecap="round" d="M8.9 15.7c.8.65 1.9 1 3.1 1s2.3-.35 3.1-1" />
    </svg>
  );
}

// A room, as a card. The whole card is the link, so the action reads as a
// button but is a span — an anchor cannot nest inside an anchor.
function CommunityCard(props: { entry: (typeof COMMUNITY)[number]; hidden: boolean }) {
  return (
    <a
      href={props.entry.href}
      target="_blank"
      rel="noreferrer noopener"
      data-setting
      hidden={props.hidden}
      class="group flex flex-col rounded-[10px] border border-[color:var(--border-subtle)] bg-[color:var(--bg-elevated)]/40 p-4
        transition-colors hover:border-[color:var(--border-default)] hover:bg-[color:var(--bg-elevated)]/70"
    >
      <div class="flex items-center gap-3">
        <span
          class="shrink-0 flex h-[32px] w-[32px] items-center justify-center rounded-[8px] text-white"
          style={{ background: props.entry.color }}
        >
          <span class="block h-[18px] w-[18px]">
            <Dynamic component={props.entry.Mark} />
          </span>
        </span>
        <span class="flex min-w-0 items-baseline gap-1.5">
          <span class="text-ui font-semibold text-[color:var(--text-primary)]">{props.entry.name}</span>
          <span class="text-micro text-[color:var(--text-muted)]">{props.entry.sub}</span>
        </span>
      </div>
      <p class="mt-3 mb-3 text-meta leading-[1.55] text-[color:var(--text-tertiary)]">{props.entry.desc}</p>
      <span class="mt-auto inline-flex h-8 items-center gap-1.5 self-start rounded-lg bg-[color:var(--bg-elevated)] px-3.5
        text-meta font-medium text-[color:var(--accent)] transition-colors group-hover:brightness-125">
        {props.entry.action}
        <svg class="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
          <path stroke-linecap="round" stroke-linejoin="round" d="M4.5 19.5l15-15m0 0H8.25m11.25 0v11.25" />
        </svg>
      </span>
    </a>
  );
}

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
  // AGPL §13: anyone reaching this interface over a network is entitled to the
  // source of the build actually running — so pin the link to this commit when the
  // binary knows it, and fall back to the branch when it does not.
  const sourceUrl = createMemo(() =>
    commit()
      ? `https://github.com/prasenjeet-symon/ogcode/tree/${commit()}`
      : 'https://github.com/prasenjeet-symon/ogcode',
  );

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
        <Row label="License" hidden={hide('License AGPL GPL open source commercial dual')}>
          <Value mono={false}>AGPL-3.0</Value>
          <LinkAction href="https://github.com/prasenjeet-symon/ogcode/blob/main/LICENSING.md">Terms</LinkAction>
        </Row>
        <Row
          label="Source code"
          helper="The complete corresponding source for this build, as the AGPL requires."
          hidden={hide('Source code AGPL corresponding license')}
        >
          <LinkAction href={sourceUrl()}>Browse</LinkAction>
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

      <section data-section="community" data-label="Community" class="pt-7">
        <div class="flex items-end gap-2 px-4 pb-1.5">
          <h2 class="flex items-center gap-1.5 text-micro font-medium uppercase tracking-[0.06em] text-[color:var(--text-muted)]">
            <svg class="w-3 h-3 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.9">
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                d="M15 19.128a9.38 9.38 0 002.625.372 9.337 9.337 0 004.121-.952 4.125 4.125 0 00-7.533-2.493M15 19.128v-.003c0-1.113-.285-2.16-.786-3.07M15 19.128v.106A12.318 12.318 0 018.624 21c-2.331 0-4.512-.645-6.374-1.766l-.001-.109a6.375 6.375 0 0111.964-3.07M12 6.375a3.375 3.375 0 11-6.75 0 3.375 3.375 0 016.75 0zm8.25 2.25a2.625 2.625 0 11-5.25 0 2.625 2.625 0 015.25 0z"
              />
            </svg>
            Community
          </h2>
          <div class="flex-1" />
        </div>
        <p class="px-4 pt-1 text-meta leading-[1.5] text-[color:var(--text-tertiary)] max-w-[40rem]">
          Come say hi. The rooms where everyone hangs out.
        </p>
        <div class="grid gap-3 px-4 pt-3 sm:grid-cols-2 lg:grid-cols-3">
          <For each={COMMUNITY}>
            {(c) => <CommunityCard entry={c} hidden={hide(c.name, c.sub, c.desc, c.action, 'community join')} />}
          </For>
        </div>
      </section>
    </>
  );
}
