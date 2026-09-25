import { useId, useState } from 'react';

export default function CopyCommand({ command, labels }: { command: string; labels: { copy: string; copied: string; failed: string; label: string } }) {
  const [state, setState] = useState<'idle' | 'copied' | 'failed'>('idle');
  const id = useId();

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
    <div className="flex w-full max-w-2xl items-center gap-3 rounded-xl bg-ink px-4 py-3 font-mono text-[13px] text-[#c9d8ff] shadow-lg ring-1 ring-ink/10 dark:bg-black/40 dark:ring-white/10">
      <span aria-hidden="true" className="select-none text-brand-light">$</span>
      <code id={id} tabIndex={0} aria-label={labels.label} className="min-w-0 flex-1 overflow-x-auto whitespace-nowrap [scrollbar-width:thin]">
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
