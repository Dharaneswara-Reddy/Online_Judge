/**
 * Component tests for the problem list's search box.
 *
 * The behaviour that matters to a user typing in it: the page asks the
 * server once they stop rather than once per keystroke, the difficulty
 * filter they already picked survives the search, and a query that matches
 * nothing says so instead of showing a bare table.
 *
 * These run against the real clock. Faking it deadlocks against
 * userEvent's own scheduling, and the debounce is short enough that
 * waiting it out costs less than working around that.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';

vi.mock('../api/problems', () => ({
  fetchProblems: vi.fn(),
  fetchProblem: vi.fn(),
}));

vi.mock('../components/layout/NavBar', () => ({
  default: () => <nav />,
}));

import { fetchProblems } from '../api/problems';
import Problems from '../pages/Problems';

const TWO_SUM = { id: 'p1', slug: 'two-sum', title: 'Two Sum', difficulty: 'easy', tags: ['arrays'] };
const THREE_SUM = { id: 'p2', slug: 'three-sum', title: 'Three Sum', difficulty: 'medium', tags: ['arrays'] };

const renderPage = () =>
  render(
    <MemoryRouter>
      <Problems />
    </MemoryRouter>
  );

const searchBox = () => screen.getByRole('searchbox', { name: /search/i });

// The first render always fires one listing; waiting for it keeps the
// later call-count assertions about the search alone.
const afterFirstLoad = async () => {
  await waitFor(() => expect(fetchProblems).toHaveBeenCalledTimes(1));
  await screen.findByRole('table');
};

const lastSearchCall = () =>
  waitFor(() => expect(fetchProblems).toHaveBeenCalledTimes(2));

beforeEach(() => {
  vi.clearAllMocks();
  fetchProblems.mockResolvedValue({ success: true, data: [TWO_SUM, THREE_SUM], total: 2 });
});

describe('Problems search', () => {
  it('offers a search box', async () => {
    renderPage();
    await afterFirstLoad();

    expect(searchBox()).toBeInTheDocument();
  });

  it('waits for a pause in typing instead of querying per keystroke', async () => {
    const user = userEvent.setup();
    renderPage();
    await afterFirstLoad();

    await user.type(searchBox(), 'sum');
    // Three keystrokes, and the listing has not been re-requested yet.
    expect(fetchProblems).toHaveBeenCalledTimes(1);

    await lastSearchCall();
    expect(fetchProblems).toHaveBeenLastCalledWith(
      expect.objectContaining({ search: 'sum' })
    );
  });

  it('keeps the chosen difficulty while searching', async () => {
    const user = userEvent.setup();
    renderPage();
    await afterFirstLoad();

    await user.click(screen.getByRole('button', { name: 'Easy' }));
    await user.type(searchBox(), 'sum');

    await waitFor(() =>
      expect(fetchProblems).toHaveBeenLastCalledWith(
        expect.objectContaining({ search: 'sum', difficulty: 'easy' })
      )
    );
  });

  it('sends no search at all once the box is cleared', async () => {
    const user = userEvent.setup();
    renderPage();
    await afterFirstLoad();

    await user.type(searchBox(), 'sum');
    await waitFor(() =>
      expect(fetchProblems).toHaveBeenLastCalledWith(expect.objectContaining({ search: 'sum' }))
    );

    await user.clear(searchBox());
    await waitFor(() =>
      expect(fetchProblems).toHaveBeenLastCalledWith(expect.objectContaining({ search: undefined }))
    );
  });

  it('treats a whitespace-only query as no search', async () => {
    const user = userEvent.setup();
    renderPage();
    await afterFirstLoad();

    await user.type(searchBox(), '   ');

    // Nothing to commit, so no second request ever goes out.
    await new Promise((resolve) => setTimeout(resolve, 400));
    expect(fetchProblems).toHaveBeenCalledTimes(1);
    expect(fetchProblems).toHaveBeenLastCalledWith(expect.objectContaining({ search: undefined }));
  });

  it('names the query in the empty state when nothing matches', async () => {
    const user = userEvent.setup();
    renderPage();
    await afterFirstLoad();

    fetchProblems.mockResolvedValue({ success: true, data: [], total: 0 });
    await user.type(searchBox(), 'zzzz');

    expect(await screen.findByText(/no problems match/i)).toBeInTheDocument();
    expect(screen.getByText('zzzz')).toBeInTheDocument();
  });

  it('keeps the plain empty state when there is no query', async () => {
    fetchProblems.mockResolvedValue({ success: true, data: [], total: 0 });
    renderPage();
    await afterFirstLoad();

    expect(screen.getByText(/no problems found/i)).toBeInTheDocument();
    expect(screen.queryByText(/no problems match/i)).not.toBeInTheDocument();
  });

  it('returns to the first page when a new search starts', async () => {
    // A full page of results is what enables "Next".
    const fullPage = Array.from({ length: 20 }, (_, i) => ({
      ...TWO_SUM,
      id: `p${i}`,
      slug: `problem-${i}`,
      title: `Problem ${i}`,
    }));
    fetchProblems.mockResolvedValue({ success: true, data: fullPage, total: 40 });

    const user = userEvent.setup();
    renderPage();
    await afterFirstLoad();

    await user.click(screen.getByRole('button', { name: /next/i }));
    await waitFor(() =>
      expect(fetchProblems).toHaveBeenLastCalledWith(expect.objectContaining({ page: 2 }))
    );

    await user.type(searchBox(), 'sum');
    await waitFor(() =>
      expect(fetchProblems).toHaveBeenLastCalledWith(
        expect.objectContaining({ search: 'sum', page: 1 })
      )
    );
  });
});
