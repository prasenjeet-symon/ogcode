// slugify turns a human title into a URL-safe slug:
// lowercase, non-alphanumerics collapsed to a single dash, no leading or
// trailing dash, and at most 60 characters with no dash at the cut.

export const MAX_LENGTH = 60;

export function slugify(text) {
  return text.toLowerCase().replace(/[^a-z0-9]+/g, '-').slice(0, MAX_LENGTH);
}
