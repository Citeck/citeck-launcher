import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { ru } from '../i18n/ru';
import CopyCommand from './CopyCommand';

const CMD = 'curl -fsSL https://example.test/install.sh | bash';

function stubClipboard(writeText: () => Promise<void>) {
  Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

function sizeCode(code: HTMLElement, scrollWidth: number, clientWidth: number) {
  Object.defineProperty(code, 'scrollWidth', { value: scrollWidth, configurable: true });
  Object.defineProperty(code, 'clientWidth', { value: clientWidth, configurable: true });
}

describe('CopyCommand', () => {
  it('fades the cut edge instead of showing a scrollbar, and follows the scroll position', () => {
    render(<CopyCommand command={CMD} labels={ru.copy} />);
    const code = screen.getByLabelText(ru.copy.label);
    expect(code.className).toContain('[scrollbar-width:none]');
    sizeCode(code, 500, 200);
    code.scrollLeft = 0;
    fireEvent.scroll(code);
    expect(code.dataset.cut).toBe('right');
    code.scrollLeft = 300;
    fireEvent.scroll(code);
    expect(code.dataset.cut).toBe('left');
    sizeCode(code, 200, 200);
    code.scrollLeft = 0;
    fireEvent.scroll(code);
    expect(code.dataset.cut).toBe('');
  });

  it('the command is focusable and labelled, so keyboard users can scroll and select it', () => {
    render(<CopyCommand command={CMD} labels={ru.copy} />);
    const code = screen.getByLabelText(ru.copy.label);
    expect(code.textContent).toBe(CMD);
    expect(code.tabIndex).toBe(0);
  });

  it('says "copied" after a successful copy', async () => {
    stubClipboard(() => Promise.resolve());
    render(<CopyCommand command={CMD} labels={ru.copy} />);
    fireEvent.click(screen.getByRole('button'));
    await waitFor(() => expect(screen.getByRole('button').textContent).toContain(ru.copy.copied));
  });

  it('does not claim success when nothing was copied, and leaves the command selected', async () => {
    stubClipboard(() => Promise.reject(new Error('denied')));
    document.execCommand = vi.fn(() => false);
    render(<CopyCommand command={CMD} labels={ru.copy} />);
    fireEvent.click(screen.getByRole('button'));
    await waitFor(() => expect(screen.getByRole('button').textContent).toContain(ru.copy.failed));
    expect(screen.getByRole('button').textContent).not.toContain(ru.copy.copied);
    expect(getSelection()?.toString()).toBe(CMD);
  });
});
