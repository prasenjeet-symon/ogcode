// Reference fix: proves the js-bugfix task is solvable. Never copied into a run.

export const MAX_LENGTH = 60;

export function slugify(text) {
  const slug = text
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '');
  if (slug.length <= MAX_LENGTH) return slug;
  return slug.slice(0, MAX_LENGTH).replace(/-+$/, '');
}
