// The ogcode mark — "Recall Loop".
//
// One stroke that comes three quarters of the way round and then turns inward
// instead of closing: ogcode recalls rather than replays, so the loop never has
// to close and the session never has to end.
//
// Two cuts, because one set of curves cannot serve both ends of the range. The
// master's inner turn sits 10 units clear of the outer ring, which reads at
// 24px and up; below that the gap lands on less than a pixel and fills in, so
// the small cut trades a shorter tail for a heavier stroke and keeps the gap.
// Pick by rendered size, not by convenience — `small` below ~20px.
//
// Both are stroked in currentColor, so a caller sets the colour the same way
// it sets it on any other icon in this codebase.

const MASTER = 'M55 32A23 23 0 0 0 32 9A23 23 0 0 0 9 32A23 23 0 0 0 32 55A15 15 0 0 0 45 32';
const SMALL = 'M54 32A22 22 0 0 0 32 10A22 22 0 0 0 10 32A22 22 0 0 0 32 54A13 13 0 0 0 43 33';

export default function Logo(props: { class?: string; small?: boolean }) {
  return (
    <svg
      class={props.class}
      viewBox="0 0 64 64"
      fill="none"
      role="img"
      aria-label="ogcode"
    >
      <path
        d={props.small ? SMALL : MASTER}
        stroke="currentColor"
        stroke-width={props.small ? 8 : 6.5}
        stroke-linecap="round"
      />
    </svg>
  );
}
