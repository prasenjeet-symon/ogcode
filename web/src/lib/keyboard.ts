let initialized = false;

// Publish the on-screen keyboard's height as the --kb-inset CSS variable on
// <html>, so the composer can pad itself above the keyboard.
//
// Why this exists: dvh reacts to browser chrome (URL bar) but NOT to the
// keyboard. On iOS Safari the keyboard simply overlays the bottom of a
// fixed-height layout, hiding the composer; Android Chrome can opt into
// `interactive-widget=resizes-content`, where innerHeight shrinks instead —
// which this calculation handles too, because then the delta is ~0 and no
// padding is added. One mechanism, correct on both.
//
// Registered once per page load by the first composer that mounts; the
// listener is deliberately module-global rather than cleaned up on unmount,
// because composers mount/unmount per route and the variable must stay
// correct for whichever one is on screen.
export function trackKeyboardInset(): void {
  if (initialized || typeof window === 'undefined') return;
  const vv = window.visualViewport;
  if (!vv) return;
  initialized = true;

  const root = document.documentElement;
  const update = () => {
    const keyboard = Math.max(0, window.innerHeight - vv.height - vv.offsetTop);
    root.style.setProperty('--kb-inset', `${Math.round(keyboard)}px`);
  };

  vv.addEventListener('resize', update);
  vv.addEventListener('scroll', update);
  update();
}
