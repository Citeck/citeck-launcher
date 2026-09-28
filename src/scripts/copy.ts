// Copy buttons of the command blocks (CopyCommand.astro).

/** Fade the edge(s) that hide part of the command; `data-cut` says which, for tests and styling. */
function updateCut(code: HTMLElement): void {
  const left = code.scrollLeft > 1;
  const right = code.scrollLeft + code.clientWidth < code.scrollWidth - 1;
  const fade = `linear-gradient(to right, ${left ? 'transparent, #000 2rem' : '#000'}, ${right ? '#000 calc(100% - 2rem), transparent' : '#000'})`;
  code.style.maskImage = fade;
  code.style.setProperty('-webkit-mask-image', fade);
  code.dataset.cut = [left && 'left', right && 'right'].filter(Boolean).join(' ');
}

async function copy(code: HTMLElement): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(code.textContent ?? '');
    return true;
  } catch {
    // No clipboard API (plain http, old browser, denied): select the command and try the legacy copy; if that fails
    // too, the selection stays so the visitor can press Ctrl+C.
    const range = document.createRange();
    range.selectNodeContents(code);
    const sel = getSelection();
    sel?.removeAllRanges();
    sel?.addRange(range);
    try {
      return document.execCommand('copy');
    } catch {
      return false;
    }
  }
}

/** Wire every command block on the page; safe to call more than once. */
export function initCopyCommands(): void {
  for (const root of document.querySelectorAll<HTMLElement>('[data-copy-command]:not([data-ready])')) {
    root.dataset.ready = '';
    const code = root.querySelector<HTMLElement>('code')!;
    const button = root.querySelector<HTMLButtonElement>('button')!;
    const idle = button.textContent ?? '';
    let timer: ReturnType<typeof setTimeout> | undefined;

    const update = () => updateCut(code);
    update();
    if (typeof ResizeObserver === 'function') new ResizeObserver(update).observe(code);
    code.addEventListener('scroll', update, { passive: true });

    button.addEventListener('click', async () => {
      const ok = await copy(code);
      button.textContent = ok ? `✓ ${root.dataset.copied}` : (root.dataset.failed ?? '');
      clearTimeout(timer);
      timer = setTimeout(() => (button.textContent = idle), ok ? 2000 : 4000);
    });
  }
}
