// The theme switch (ThemeToggle.astro). The inline <head> script in Base.astro applies the stored theme before paint.

/** Wire every theme switch on the page; safe to call more than once. */
export function initThemeToggles(): void {
  const html = document.documentElement;
  for (const button of document.querySelectorAll<HTMLElement>('[data-theme-toggle]:not([data-ready])')) {
    button.dataset.ready = '';
    button.setAttribute('aria-pressed', String(html.classList.contains('dark')));
    button.addEventListener('click', () => {
      const dark = !html.classList.contains('dark');
      html.classList.toggle('dark', dark);
      try {
        localStorage.setItem('theme', dark ? 'dark' : 'light');
      } catch {
        /* private mode */
      }
      document.querySelectorAll('[data-theme-toggle]').forEach((b) => b.setAttribute('aria-pressed', String(dark)));
    });
  }
}
