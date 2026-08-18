/**
 * Component tests for the Playground's navigation.
 *
 * The Playground was the one signed-in page that rendered without the
 * shared nav bar, so the only way out of it was the browser's back
 * button. These tests pin the site nav to the page, and check that
 * leaving happens inside the SPA — a full reload would throw away the
 * code the user has typed.
 */

import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Routes, Route } from 'react-router-dom';

vi.mock('../context/AuthContext', () => ({
  useAuth: () => ({ user: { username: 'alice' }, logout: vi.fn() }),
}));

// Monaco expects a real browser, and the editor is loaded lazily anyway.
vi.mock('../components/editor/CodeEditor', () => ({
  default: () => <div data-testid="code-editor" />,
}));

vi.mock('../api/judge', () => ({ runCode: vi.fn() }));

import Playground from '../pages/Playground';

const renderPlayground = () =>
  render(
    <MemoryRouter initialEntries={['/playground']}>
      <Playground />
    </MemoryRouter>
  );

describe('Playground navigation', () => {
  it('offers a way back to the rest of the site', () => {
    renderPlayground();

    expect(screen.getByRole('link', { name: /codearena/i })).toHaveAttribute('href', '/');
    expect(screen.getByRole('link', { name: /^problems$/i })).toHaveAttribute('href', '/problems');
  });

  it('leaves without a full page load, so unsaved code survives a wrong turn', async () => {
    render(
      <MemoryRouter initialEntries={['/playground']}>
        <Routes>
          <Route path="/playground" element={<Playground />} />
          <Route path="/problems" element={<div>problem list</div>} />
        </Routes>
      </MemoryRouter>
    );

    await userEvent.click(screen.getByRole('link', { name: /^problems$/i }));

    expect(await screen.findByText('problem list')).toBeInTheDocument();
  });

  it('keeps the editor and its run control alongside the nav', async () => {
    renderPlayground();

    expect(await screen.findByTestId('code-editor')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /run/i })).toBeInTheDocument();
  });
});
