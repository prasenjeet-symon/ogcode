import { createSignal } from 'solid-js';

// Which navigation drawer is open on small screens. A module-level signal is
// the point: the hamburger buttons live in page headers, the drawer itself is
// the sidebar component wrapped in SidebarShell, and the two halves never
// share a parent — they meet here.
export type DrawerId = 'sessions' | 'plans';

const [open, setOpen] = createSignal<DrawerId | null>(null);

export function openDrawer(id: DrawerId): void {
  setOpen(id);
}

export function closeDrawer(): void {
  setOpen(null);
}

export function drawerIsOpen(id: DrawerId): boolean {
  return open() === id;
}
