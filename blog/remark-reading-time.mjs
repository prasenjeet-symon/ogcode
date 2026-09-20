// Computes an estimated reading time for each post and exposes it to layouts
// as remarkPluginFrontmatter.readingTime (whole minutes, always >= 1).
// Implemented by hand to avoid a dependency on mdast-util-to-string.

function textOf(node) {
  if (typeof node.value === 'string') return node.value;
  if (!node.children) return '';
  return node.children.map(textOf).join(' ');
}

export function remarkReadingTime() {
  return (tree, { data }) => {
    const words = textOf(tree).trim().split(/\s+/).filter(Boolean).length;
    data.astro.frontmatter.readingTime = Math.max(1, Math.round(words / 215));
  };
}
