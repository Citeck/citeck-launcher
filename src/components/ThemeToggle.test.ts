import { afterEach, describe, expect, it } from 'vitest';
import { fireEvent, screen } from '@testing-library/dom';
import { mount } from '../../test/mount';
import ThemeToggle from './ThemeToggle.astro';
import { initThemeToggles } from '../scripts/theme';

afterEach(() => {
  document.body.innerHTML = '';
  document.documentElement.classList.remove('dark');
  localStorage.clear();
});

describe('ThemeToggle', () => {
  it('reflects the current theme, switches it and remembers the choice', async () => {
    document.documentElement.classList.add('dark');
    await mount(ThemeToggle, { label: 'Сменить тему' });
    initThemeToggles();
    const button = screen.getByRole('button', { name: 'Сменить тему' });
    expect(button.getAttribute('aria-pressed')).toBe('true');
    fireEvent.click(button);
    expect(document.documentElement.classList.contains('dark')).toBe(false);
    expect(localStorage.getItem('theme')).toBe('light');
    expect(button.getAttribute('aria-pressed')).toBe('false');
    fireEvent.click(button);
    expect(document.documentElement.classList.contains('dark')).toBe(true);
    expect(localStorage.getItem('theme')).toBe('dark');
  });
  it('renders pressed (dark) before any script runs, since dark is the default', async () => {
    await mount(ThemeToggle, { label: 'x' });
    expect(screen.getByRole('button').getAttribute('aria-pressed')).toBe('true');
  });
});
