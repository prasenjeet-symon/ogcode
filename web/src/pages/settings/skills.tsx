import { createSignal, Show, For, createMemo, createResource, createEffect, type JSX } from 'solid-js';
import { useServer } from '../../context/server';
import { listSkills, type Skill } from '../../api/client';
import {
  Group,
  Row,
  Tag,
  LinkAction,
  EmptyState,
  Spinner,
  Mono,
  matches,
  useShell,
} from './ui';

// ---------------------------------------------------------------------------
// Skills — the catalogue of capabilities the agent can reach for.
//
// It lives under Settings beside Models because it answers the same question:
// what is available to the agent here, and where did it come from. The list is
// read-only — skills are added by putting a SKILL.md on disk — so each card
// leads with the directories it was read from.
// ---------------------------------------------------------------------------

interface SourceMeta {
  label: string;
  origin: JSX.Element;
  icon: string;
}

// Ordered the way a user acts on them: their own skills first, the built-ins —
// which they cannot edit — last. The keys match skill.Source in Go.
const SOURCES: Array<{ id: string } & SourceMeta> = [
  {
    id: 'project',
    label: 'Project',
    icon: 'M2.25 12.75V12A2.25 2.25 0 014.5 9.75h15A2.25 2.25 0 0121.75 12v.75m-8.69-6.44l-2.12-2.12a1.5 1.5 0 00-1.061-.44H4.5A2.25 2.25 0 002.25 6v12a2.25 2.25 0 002.25 2.25h15A2.25 2.25 0 0021.75 18V9a2.25 2.25 0 00-2.25-2.25h-5.379a1.5 1.5 0 01-1.06-.44z',
    origin: (
      <>
        Written in this repo — <Mono>.agents/skills/</Mono> or <Mono>.claude/skills/</Mono>, searched up to
        the repo root. A project skill replaces a same-named skill from anywhere else.
      </>
    ),
  },
  {
    id: 'config',
    label: 'Config',
    icon: 'M11.42 15.17L17.25 21A2.652 2.652 0 0021 17.25l-5.877-5.877M11.42 15.17l2.496-3.03c.317-.384.74-.626 1.208-.766M11.42 15.17l-4.655 5.653a2.548 2.548 0 11-3.586-3.586l6.837-5.63m5.108-.233c.55-.164 1.163-.188 1.743-.14a4.5 4.5 0 004.486-6.336l-3.276 3.277a3.004 3.004 0 01-2.25-2.25l3.276-3.276a4.5 4.5 0 00-6.336 4.486c.091 1.076-.071 2.264-.904 2.95l-.102.085m-1.745 1.437L5.909 7.5H4.5L2.25 3.75l1.5-1.5L7.5 4.5v1.409l4.26 4.26m-1.745 1.437l1.745-1.437m6.615 8.206L15.75 15.75M4.867 19.125h.008v.008h-.008v-.008z',
    origin: (
      <>
        Extra directories listed under <Mono>skills.paths</Mono> in <Mono>ogcode.json</Mono>.
      </>
    ),
  },
  {
    id: 'global',
    label: 'Global',
    icon: 'M12 21a9 9 0 100-18 9 9 0 000 18zm0 0c2.485 0 4.5-4.03 4.5-9S14.485 3 12 3 7.5 7.03 7.5 12s2.015 9 4.5 9zm-8.716-5.25h17.432M3.284 8.25h17.432',
    origin: (
      <>
        Your personal library — <Mono>~/.config/ogcode/skills</Mono>, <Mono>~/.ogcode/skills</Mono>,{' '}
        <Mono>~/.agents/skills</Mono> or <Mono>~/.claude/skills</Mono>.
      </>
    ),
  },
  {
    id: 'remote',
    label: 'Remote',
    icon: 'M2.25 15a4.5 4.5 0 004.5 4.5H18a3.75 3.75 0 001.332-7.257 3 3 0 00-3.758-3.848 5.25 5.25 0 00-10.233 2.33A4.502 4.502 0 002.25 15z',
    origin: (
      <>
        Fetched from the <Mono>skills.urls</Mono> manifests in <Mono>ogcode.json</Mono> and cached on disk.
      </>
    ),
  },
  {
    id: 'built-in',
    label: 'Built-in',
    icon: 'M21 7.5l-9-5.25L3 7.5m18 0l-9 5.25m9-5.25v9l-9 5.25M3 7.5l9 5.25M3 7.5v9l9 5.25m0-9v9',
    origin: <>Ships with ogcode. Add a skill of the same name to your project to override one.</>,
  },
];

const SOURCE_INDEX = new Map(SOURCES.map((s) => [s.id, s]));

/** A source ogcode grows later still gets a card, just without the
 *  explanatory line. */
function sourceMeta(id: string): SourceMeta {
  return (
    SOURCE_INDEX.get(id) ?? {
      label: id.charAt(0).toUpperCase() + id.slice(1),
      origin: '',
      icon: 'M12 6v12m6-6H6',
    }
  );
}

// A short, fixed palette of glyphs hashed from the skill name, so every skill
// gets a stable, distinct mark without shipping an icon set.
const GLYPHS = [
  'M12 2l2.4 7.4H22l-6 4.4 2.3 7.2-6.3-4.6-6.3 4.6L7.9 13.8 2 9.4h7.6L12 2z',
  'M12 3v18M3 12h18',
  'M5 13l4 4L19 7',
  'M9.66 17a4 4 0 100-8 4 4 0 000 8zm0 0l7.34 7',
  'M4 6h16M4 12h16M4 18h16',
  'M12 22a10 10 0 100-20 10 10 0 000 20zm0-14v4l3 3',
  'M3 7l9-4 9 4-9 4-9-4zm0 10l9-4 9 4-9 4-9-4z',
  'M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 002 2h2a2 2 0 002-2M9 5a2 2 0 012-2h2a2 2 0 012 2',
  'M13 10V3L4 14h7v7l9-11h-7z',
  'M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z',
];
function glyphFor(name: string) {
  let h = 0;
  for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) >>> 0;
  return GLYPHS[h % GLYPHS.length];
}

/** A skill's description is its trigger — the sentence that decides whether the
 *  agent reaches for it — and they run long. Two lines by default, all of it on
 *  request, so the list stays scannable without hiding anything for good. */
function SkillRow(props: { skill: Skill; hidden: boolean }) {
  const [open, setOpen] = createSignal(false);
  const long = () => props.skill.description.length > 140;
  return (
    <Row
      icon={glyphFor(props.skill.name)}
      hidden={props.hidden}
      label={
        <span class="font-mono text-meta text-[color:var(--text-primary)]">{props.skill.name}</span>
      }
      helper={
        <>
          <span class={open() || !long() ? 'block' : 'line-clamp-2'}>{props.skill.description}</span>
          <Show when={long()}>
            <span class="inline-block mt-1">
              <LinkAction onClick={() => setOpen(!open())}>{open() ? 'Show less' : 'Show more'}</LinkAction>
            </span>
          </Show>
        </>
      }
    />
  );
}

export default function SkillsSettings() {
  const server = useServer();
  const shell = useShell();
  const [skills] = createResource(server.directory, () => listSkills());

  const all = () => skills() ?? [];
  createEffect(() => shell.report({ noun: 'skills' }));

  const visible = createMemo(() => {
    const q = shell.query();
    return all().filter((s) => matches(q, s.name, s.description, sourceMeta(s.source).label));
  });

  // Grouped by source: the built-ins, the project's own, and anything remote
  // each get their own card. A flat list of forty mixed-origin skills tells you
  // nothing about which ones you control.
  const grouped = createMemo(() => {
    const groups = new Map<string, Skill[]>();
    for (const s of all()) {
      const arr = groups.get(s.source) ?? [];
      arr.push(s);
      groups.set(s.source, arr);
    }
    const known = SOURCES.map((s) => s.id).filter((id) => groups.has(id));
    const unknown = [...groups.keys()].filter((id) => !SOURCE_INDEX.has(id)).sort();
    return [...known, ...unknown].map((id) => ({
      id,
      meta: sourceMeta(id),
      items: groups.get(id)!.sort((a, b) => a.name.localeCompare(b.name)),
    }));
  });

  /** Keyed by name and source, because the same skill name can legitimately
   *  appear under two sources while one shadows the other. */
  const shown = createMemo(() => new Set(visible().map((s) => `${s.name} ${s.source}`)));

  return (
    <Show
      when={!skills.loading}
      fallback={
        <div class="py-10 flex items-center gap-2.5 text-meta text-[color:var(--text-muted)]">
          <Spinner />
          Loading skills…
        </div>
      }
    >
      <Show
        when={!skills.error}
        fallback={
          <EmptyState
            icon="M12 9v3.75m9-.75a9 9 0 11-18 0 9 9 0 0118 0zm-9 3.75h.008v.008H12v-.008z"
            title="Could not load skills"
            body="The ogcode server did not answer. Check that it is still running, then reload."
          />
        }
      >
        <Show
          when={all().length > 0}
          fallback={
            <EmptyState
              icon="M9.813 15.904L9 18.75l-.813-2.846a4.5 4.5 0 00-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 003.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 003.09 3.09L15.75 12l-2.846.813a4.5 4.5 0 00-3.09 3.09z"
              title="No skills installed"
              body={
                <>
                  Create <Mono>.agents/skills/&lt;name&gt;/SKILL.md</Mono> in this project, or drop one into{' '}
                  <Mono>~/.ogcode/skills</Mono> to make it available everywhere.
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
                title={`No skills match "${shell.query()}"`}
                body="Try a shorter query, or clear the filter to see every skill."
              />
            }
          >
            <For each={grouped()}>
              {(group) => (
                <Group
                  id={group.id}
                  title={group.meta.label}
                  icon={group.meta.icon}
                  description={group.meta.origin}
                  action={<Tag>{group.items.length}</Tag>}
                >
                  <For each={group.items}>
                    {(s) => <SkillRow skill={s} hidden={!shown().has(`${s.name} ${s.source}`)} />}
                  </For>
                </Group>
              )}
            </For>
          </Show>
        </Show>
      </Show>
    </Show>
  );
}
