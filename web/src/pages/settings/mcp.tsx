import { createSignal, Show, For, createMemo, createResource, createEffect, type JSX } from 'solid-js';
import { useServer } from '../../context/server';
import { listMCPServers, setMCPEnabled, type MCPServer } from '../../api/client';
import {
  Group,
  Row,
  Switch,
  Tag,
  EmptyState,
  Spinner,
  Mono,
  matches,
  useShell,
} from './ui';

// ---------------------------------------------------------------------------
// MCP servers — the external Model Context Protocol servers this project
// connects to, declared under the "mcp" key of ogcode.json.
//
// It sits beside Skills because it answers the same question — what the agent
// can reach for, and from where — and carries the same switch. Turning a server
// off writes "disabled": true into the project's ogcode.json: ogcode stops
// connecting to it and drops its tools from the agent's toolset, so none of its
// tool schemas reach the prompt (the tokens are saved) and the assistant has no
// idea it exists. The server's config and any stored OAuth tokens are left
// exactly as they were, so flipping it back on reconnects.
// ---------------------------------------------------------------------------

// A stable glyph per transport, so the kind of server reads at a glance.
const TRANSPORT_ICON: Record<string, string> = {
  // stdio — a subprocess / command line
  stdio: 'M6.75 7.5l3 2.25-3 2.25m4.5 0h3m-9 8.25h13.5A2.25 2.25 0 0021 18V6a2.25 2.25 0 00-2.25-2.25H5.25A2.25 2.25 0 003 6v12a2.25 2.25 0 002.25 2.25z',
  // http — a cloud endpoint
  http: 'M2.25 15a4.5 4.5 0 004.5 4.5H18a3.75 3.75 0 001.332-7.257 3 3 0 00-3.758-3.848 5.25 5.25 0 00-10.233 2.33A4.502 4.502 0 002.25 15z',
  // sse — a stream
  sse: 'M6.429 9.75L2.25 12l4.179 2.25m0-4.5l5.571 3 5.571-3m-11.142 0L2.25 7.5 12 2.25l9.75 5.25-4.179 2.25m0 0L21.75 12l-4.179 2.25m0 0l4.179 2.25L12 21.75 2.25 16.5l4.179-2.25m11.142 0l-5.571 3-5.571-3',
};
function iconFor(transport: string): string {
  return TRANSPORT_ICON[transport] ?? TRANSPORT_ICON.http;
}

// A short, human label for how the server authenticates, shown so the user can
// see that disabling will not cost them their credentials.
const AUTH_LABEL: Record<string, string> = {
  oauth: 'OAuth',
  headers: 'Token',
  none: '',
};

/** The status line beneath a server's name: what it is doing right now. Colour
 *  carries the state so it reads without parsing the words. */
function StatusLine(props: { server: MCPServer }): JSX.Element {
  const s = props.server;
  if (!s.enabled) {
    return <span class="text-[color:var(--text-muted)]">Disabled · tools hidden from the agent</span>;
  }
  if (s.error) {
    return <span class="text-[color:var(--warning)]">Not connected · {s.error}</span>;
  }
  if (s.connected) {
    return (
      <span class="text-[color:var(--success)]">
        Connected · {s.toolCount === 0 ? 'no tools' : `${s.toolCount} ${s.toolCount === 1 ? 'tool' : 'tools'}`}
      </span>
    );
  }
  return <span class="text-[color:var(--text-tertiary)]">Not connected</span>;
}

function ServerRow(props: {
  server: MCPServer;
  hidden: boolean;
  busy: boolean;
  onToggle: (next: boolean) => void;
}) {
  const s = () => props.server;
  const enabled = () => s().enabled;
  const auth = () => AUTH_LABEL[s().auth] ?? '';
  return (
    <Row
      icon={iconFor(s().transport)}
      hidden={props.hidden}
      label={
        <span class="flex items-center gap-2 min-w-0">
          <span
            class="font-mono text-meta truncate"
            classList={{
              'text-[color:var(--text-primary)]': enabled(),
              'text-[color:var(--text-muted)]': !enabled(),
            }}
          >
            {s().name}
          </span>
          <Show when={auth()}>
            <Tag>{auth()}</Tag>
          </Show>
        </span>
      }
      helper={
        <span classList={{ 'opacity-70': !enabled() }}>
          <span class="block text-[color:var(--text-tertiary)]">
            <span class="uppercase tracking-[0.04em] text-micro">{s().transport}</span>
            <Show when={s().target}>
              {' · '}
              <Mono>{s().target}</Mono>
            </Show>
          </span>
          <span class="block mt-0.5">
            <StatusLine server={s()} />
          </span>
        </span>
      }
    >
      <Switch
        checked={enabled()}
        disabled={props.busy}
        onChange={(next) => props.onToggle(next)}
        label={`${enabled() ? 'Disable' : 'Enable'} the ${s().name} MCP server`}
      />
    </Row>
  );
}

export default function MCPSettings() {
  const server = useServer();
  const shell = useShell();
  const [servers, { mutate }] = createResource(server.directory, () => listMCPServers());

  const all = () => servers() ?? [];
  createEffect(() => shell.report({ noun: 'servers' }));

  const visible = createMemo(() => {
    const q = shell.query();
    return all().filter((s) => matches(q, s.name, s.target, s.transport, s.scope));
  });
  const shownNames = createMemo(() => new Set(visible().map((s) => s.name)));

  const [busy, setBusy] = createSignal<Set<string>>(new Set());
  const setBusyFor = (name: string, on: boolean) =>
    setBusy((prev) => {
      const next = new Set(prev);
      if (on) next.add(name);
      else next.delete(name);
      return next;
    });

  const patch = (name: string, changes: Partial<MCPServer>) =>
    mutate((list) => list?.map((s) => (s.name === name ? { ...s, ...changes } : s)));

  const toggle = async (srv: MCPServer, next: boolean) => {
    setBusyFor(srv.name, true);
    patch(srv.name, { enabled: next }); // optimistic
    try {
      const updated = await setMCPEnabled(srv.name, next);
      patch(srv.name, updated);
    } catch {
      patch(srv.name, { enabled: !next }); // the server refused — put it back
    } finally {
      setBusyFor(srv.name, false);
    }
  };

  const sorted = createMemo(() => [...all()].sort((a, b) => a.name.localeCompare(b.name)));
  const enabledCount = createMemo(() => all().filter((s) => s.enabled).length);

  return (
    <Show
      when={!servers.loading}
      fallback={
        <div class="py-10 flex items-center gap-2.5 text-meta text-[color:var(--text-muted)]">
          <Spinner />
          Loading MCP servers…
        </div>
      }
    >
      <Show
        when={!servers.error}
        fallback={
          <EmptyState
            icon="M12 9v3.75m9-.75a9 9 0 11-18 0 9 9 0 0118 0zm-9 3.75h.008v.008H12v-.008z"
            title="Could not load MCP servers"
            body="The ogcode server did not answer. Check that it is still running, then reload."
          />
        }
      >
        <Show
          when={all().length > 0}
          fallback={
            <EmptyState
              icon="M2.25 15a4.5 4.5 0 004.5 4.5H18a3.75 3.75 0 001.332-7.257 3 3 0 00-3.758-3.848 5.25 5.25 0 00-10.233 2.33A4.502 4.502 0 002.25 15z"
              title="No MCP servers configured"
              body={
                <>
                  Add servers under the <Mono>mcp</Mono> key of <Mono>ogcode.json</Mono> — a local command
                  or a remote URL — and they will appear here to connect and toggle.
                </>
              }
            />
          }
        >
          <Show
            when={visible().length > 0}
            fallback={
              <EmptyState
                icon="M21 21l-4.35-4.35M17 10a7 7 0 11-14 0 7 7 0 0114 0z"
                title={`No servers match "${shell.query()}"`}
                body="Try a shorter query, or clear the filter to see every server."
              />
            }
          >
            <Group
              id="mcp"
              title="Model Context Protocol"
              icon="M2.25 15a4.5 4.5 0 004.5 4.5H18a3.75 3.75 0 001.332-7.257 3 3 0 00-3.758-3.848 5.25 5.25 0 00-10.233 2.33A4.502 4.502 0 002.25 15z"
              description={
                <>
                  Turning a server off writes <Mono>"disabled": true</Mono> into this project's{' '}
                  <Mono>ogcode.json</Mono>: ogcode stops connecting to it and drops its tools from the
                  agent, so none of its tool schemas reach the prompt. Its config and any stored auth
                  tokens are left untouched — flip it back on to reconnect.
                </>
              }
              action={<Tag>{enabledCount()} on</Tag>}
            >
              <For each={sorted()}>
                {(s) => (
                  <ServerRow
                    server={s}
                    hidden={!shownNames().has(s.name)}
                    busy={busy().has(s.name)}
                    onToggle={(next) => toggle(s, next)}
                  />
                )}
              </For>
            </Group>
          </Show>
        </Show>
      </Show>
    </Show>
  );
}
