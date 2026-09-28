import { useEffect, useState } from 'react';
import { MoonIcon, SunIcon } from './icons';

export default function ThemeToggle({ label }: { label: string }) {
  // Dark is the default, so the server-rendered icon matches what most visitors see before hydration.
  const [dark, setDark] = useState(true);
  useEffect(() => setDark(document.documentElement.classList.contains('dark')), []);

  function toggle() {
    const next = !dark;
    setDark(next);
    document.documentElement.classList.toggle('dark', next);
    try {
      localStorage.setItem('theme', next ? 'dark' : 'light');
    } catch {
      /* private mode */
    }
  }

  return (
    <button
      type="button"
      onClick={toggle}
      aria-label={label}
      aria-pressed={dark}
      title={label}
      className="grid size-9 place-items-center rounded-lg text-muted transition hover:bg-slate-100 hover:text-ink focus-visible:outline-2 focus-visible:outline-brand dark:text-night-muted dark:hover:bg-white/10 dark:hover:text-night-ink"
    >
      {dark ? <SunIcon /> : <MoonIcon />}
    </button>
  );
}
