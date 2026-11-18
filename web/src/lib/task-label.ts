// The label a `task` (sub-agent) call is known by.
//
// `internal/tool/task.go` takes the caller's short `description` and falls back
// to the literal "task"; a RUNNING tool part's `state.title` is seeded with the
// tool name too, so neither the tool nor the part can say what a delegation is
// about. The call's own args can: `description`, or — when the model omitted it
// — the first line of the `prompt`. Everything user-facing keys off this, so a
// pill or a tool row never reads as the bare tool name.
const MAX_LABEL = 80;

function firstLine(prompt: string): string {
  for (const raw of prompt.split('\n')) {
    const line = raw.replace(/^[\s>#*_-]+/, '').replace(/\s+/g, ' ').trim();
    if (line) return line;
  }
  return '';
}

/** A human label for a `task` call's input, or '' when there is nothing to show. */
export function taskLabel(input: unknown): string {
  const args = (input ?? {}) as { description?: unknown; prompt?: unknown };
  let label = typeof args.description === 'string' ? args.description.trim() : '';
  // A description of "task" is the tool's own placeholder, not a label.
  if (!label || label.toLowerCase() === 'task') {
    label = typeof args.prompt === 'string' ? firstLine(args.prompt) : '';
  }
  if (!label) return '';
  return label.length > MAX_LABEL ? label.slice(0, MAX_LABEL - 1).trimEnd() + '…' : label;
}
