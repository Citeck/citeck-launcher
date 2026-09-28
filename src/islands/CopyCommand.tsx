import { useEffect, useId, useRef, useState } from 'react';

/** `full` stretches the box to its container (the copy button then sits at the right edge); otherwise it hugs the command. */
export default function CopyCommand({
  command,
  labels,
  full = false,
}: {
  command: string;
  labels: { copy: string; copied: string; failed: string; label: string };
  full?: boolean;
}) {
  const [state, setState] = useState<'idle' | 'copied' | 'failed'>('idle');
  const id = useId();
  const codeRef = useRef<HTMLElement>(null);
  // Which edges hide part of the command; they fade out instead of showing a scrollbar. The server render assumes the
  // usual case: a long command cut on the right.
  const [cut, setCut] = useState({ left: false, right: true });

  useEffect(() => {
    const el = codeRef.current;
    if (!el) return;
    const update = () =>
      setCut({ left: el.scrollLeft > 1, right: el.scrollLeft + el.clientWidth < el.scrollWidth - 1 });
    update();
    const ro = typeof ResizeObserver === 'function' ? new ResizeObserver(update) : null;
    ro?.observe(el);
    el.addEventListener('scroll', update, { passive: true });
    return () => {
      ro?.disconnect();
      el.removeEventListener('scroll', update);
    };
  }, []);
  const fade = `linear-gradient(to right, ${cut.left ? 'transparent, #000 2rem' : '#000'}, ${cut.right ? '#000 calc(100% - 2rem), transparent' : '#000'})`;

  async function copy() {
    let ok = false;
    try {
      await navigator.clipboard.writeText(command);
      ok = true;
    } catch {
      // No clipboard API (plain http, old browser, denied): select the command, try the legacy copy, and if that
      // fails too the selection stays so the visitor can press Ctrl+C.
      const el = document.getElementById(id);
      if (el) {
        const range = document.createRange();
        range.selectNodeContents(el);
        const sel = getSelection();
        sel?.removeAllRanges();
        sel?.addRange(range);
        try {
          ok = document.execCommand('copy');
        } catch {
          ok = false;
        }
      }
    }
    setState(ok ? 'copied' : 'failed');
    setTimeout(() => setState('idle'), ok ? 2000 : 4000);
  }

  return (
    <div className={`flex ${full ? 'w-full' : 'w-fit max-w-full'} items-center gap-3 rounded-xl bg-ink px-4 py-3 font-mono text-[12px] text-[#c9d8ff] shadow-lg ring-1 ring-ink/10 dark:bg-black/40 dark:ring-white/10`}>
      <span aria-hidden="true" className="select-none text-brand-light">$</span>
      <code
        id={id}
        ref={codeRef}
        tabIndex={0}
        aria-label={labels.label}
        style={{ maskImage: fade, WebkitMaskImage: fade }}
        data-cut={[cut.left && 'left', cut.right && 'right'].filter(Boolean).join(' ')}
        className="min-w-0 flex-1 overflow-x-auto whitespace-nowrap rounded-sm [scrollbar-width:none] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-brand-light [&::-webkit-scrollbar]:hidden"
      >
        {command}
      </code>
      <button
        type="button"
        onClick={copy}
        aria-live="polite"
        className="shrink-0 rounded-lg bg-white/10 px-3 py-1.5 font-sans text-xs font-semibold text-white transition hover:bg-white/20 focus-visible:outline-2 focus-visible:outline-brand-light"
      >
        {state === 'copied' ? `✓ ${labels.copied}` : state === 'failed' ? labels.failed : labels.copy}
      </button>
    </div>
  );
}
