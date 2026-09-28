// Keeps a section opened through its #anchor (#features, #downloads, #faq) where its scroll-margin-top puts it,
// 24 px under the header. The browser computes the scroll target once, but the page can still grow above the target
// while it gets there: fonts, lazy images (the phone gallery gains a scrollbar once its pictures load), the downloads
// table replacing its fallback text. So once the scroll has come to rest, and again when the page changes, a target
// that ended up off its mark is nudged back — unless the visitor has started scrolling on their own.
// The install tabs (#desktop, #server) position themselves in Tracks.astro.

const OWN = ['desktop', 'server'];
let pinned = false; // an anchor was followed and the visitor has not scrolled since

function target(): HTMLElement | null {
  const id = decodeURIComponent(location.hash.slice(1));
  return id && !OWN.includes(id) ? document.getElementById(id) : null;
}

export function realignToHash(): void {
  const el = pinned ? target() : null;
  if (!el) return;
  const off = el.getBoundingClientRect().top - (parseFloat(getComputedStyle(el).scrollMarginTop) || 0);
  if (Math.abs(off) > 1) scrollBy({ top: off, behavior: 'instant' });
}

export function initAnchorRealign(): void {
  pinned = target() !== null;
  for (const ev of ['wheel', 'touchmove', 'keydown']) addEventListener(ev, () => (pinned = false), { passive: true });
  // A press anywhere but on an in-page link (dragging the scrollbar, selecting text) also hands control back.
  addEventListener('pointerdown', (e) => {
    if (!(e.target as Element | null)?.closest?.('a[href^="#"]')) pinned = false;
  }, { passive: true });
  addEventListener('hashchange', () => (pinned = target() !== null));
  // When a scroll comes to rest (scrollend is not in every browser, so also a short idle timer).
  let idle: ReturnType<typeof setTimeout> | undefined;
  addEventListener('scroll', () => {
    clearTimeout(idle);
    idle = setTimeout(realignToHash, 150);
  }, { passive: true });
  addEventListener('scrollend', realignToHash);
  if (document.readyState === 'complete') requestAnimationFrame(realignToHash);
  else addEventListener('load', () => requestAnimationFrame(realignToHash), { once: true });
  // The downloads table is rendered after the GitHub API answers, possibly after load (download.ts).
  addEventListener('citeck:layout', () => requestAnimationFrame(realignToHash));
}
