/**
 * Component tests for the per-problem discussion thread.
 *
 * These cover the behaviour a user actually depends on: replies stay
 * nested under their parent, a vote updates in place without reloading
 * the thread, and the moderation controls only appear to people allowed
 * to use them.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

vi.mock('../api/discussions', () => ({
  fetchDiscussions: vi.fn(),
  postComment: vi.fn(),
  setUpvote: vi.fn(),
  deleteComment: vi.fn(),
}));

import {
  fetchDiscussions,
  postComment,
  setUpvote,
  deleteComment,
} from '../api/discussions';
import DiscussionPanel from '../components/problem/DiscussionPanel';

const AUTHOR = { id: 'user-1', role: 'user' };

function thread(overrides = {}) {
  return {
    id: 'c1',
    userId: 'user-1',
    username: 'alice',
    content: 'Use a hash map for O(n).',
    upvotes: 2,
    upvotedByMe: false,
    createdAt: '2026-08-01T10:00:00Z',
    replies: [],
    ...overrides,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  fetchDiscussions.mockResolvedValue({ threads: [], nextCursor: null, hasMore: false });
});

describe('DiscussionPanel', () => {
  it('renders replies nested under their parent comment', async () => {
    fetchDiscussions.mockResolvedValue({
      threads: [
        thread({
          replies: [{
            id: 'r1', userId: 'user-2', username: 'bob', content: 'That helped, thanks.',
            upvotes: 0, upvotedByMe: false, createdAt: '2026-08-01T11:00:00Z',
          }],
        }),
      ],
      nextCursor: null,
      hasMore: false,
    });

    render(<DiscussionPanel slug="two-sum" currentUser={AUTHOR} />);

    expect(await screen.findByText('Use a hash map for O(n).')).toBeInTheDocument();
    expect(screen.getByText('That helped, thanks.')).toBeInTheDocument();
    // The reply must live inside the parent's thread element, not beside it.
    const parent = screen.getByText('Use a hash map for O(n).').closest('.discussion-thread');
    expect(parent).toContainElement(screen.getByText('That helped, thanks.'));
  });

  it('shows an empty state when nobody has commented', async () => {
    render(<DiscussionPanel slug="two-sum" currentUser={AUTHOR} />);

    expect(await screen.findByText(/no comments yet/i)).toBeInTheDocument();
  });

  it('invites anonymous readers to sign in instead of showing a composer', async () => {
    fetchDiscussions.mockResolvedValue({ threads: [thread()], nextCursor: null, hasMore: false });

    render(<DiscussionPanel slug="two-sum" currentUser={null} />);

    expect(await screen.findByText(/sign in to join the discussion/i)).toBeInTheDocument();
    expect(screen.queryByPlaceholderText(/ask a question/i)).not.toBeInTheDocument();
  });

  it('updates the vote count in place without refetching the thread', async () => {
    fetchDiscussions.mockResolvedValue({ threads: [thread()], nextCursor: null, hasMore: false });
    setUpvote.mockResolvedValue({ upvotes: 3, upvotedByMe: true });

    render(<DiscussionPanel slug="two-sum" currentUser={AUTHOR} />);
    await screen.findByText('Use a hash map for O(n).');
    expect(fetchDiscussions).toHaveBeenCalledTimes(1);

    await userEvent.click(screen.getByTitle('Upvote'));

    await waitFor(() => expect(screen.getByRole('button', { name: /3/ })).toBeInTheDocument());
    expect(setUpvote).toHaveBeenCalledWith('c1', true);
    // Refetching would scroll the reader away from the comment they voted on.
    expect(fetchDiscussions).toHaveBeenCalledTimes(1);
  });

  it('withdraws a vote that is already cast', async () => {
    fetchDiscussions.mockResolvedValue({ threads: [thread({ upvotes: 1, upvotedByMe: true })], nextCursor: null, hasMore: false });
    setUpvote.mockResolvedValue({ upvotes: 0, upvotedByMe: false });

    render(<DiscussionPanel slug="two-sum" currentUser={AUTHOR} />);
    await screen.findByText('Use a hash map for O(n).');

    await userEvent.click(screen.getByTitle('Upvote'));

    await waitFor(() => expect(setUpvote).toHaveBeenCalledWith('c1', false));
  });

  it('posts a comment and reloads the thread', async () => {
    postComment.mockResolvedValue({ id: 'new' });

    render(<DiscussionPanel slug="two-sum" currentUser={AUTHOR} />);
    await screen.findByText(/no comments yet/i);

    await userEvent.type(screen.getByPlaceholderText(/ask a question/i), 'My approach');
    await userEvent.click(screen.getByRole('button', { name: /post/i }));

    await waitFor(() =>
      expect(postComment).toHaveBeenCalledWith('two-sum', { content: 'My approach' })
    );
    expect(fetchDiscussions).toHaveBeenCalledTimes(2);
  });

  it('refuses to post an empty comment', async () => {
    render(<DiscussionPanel slug="two-sum" currentUser={AUTHOR} />);
    await screen.findByText(/no comments yet/i);

    // The button stays disabled until there is something to say.
    expect(screen.getByRole('button', { name: /post/i })).toBeDisabled();
    await userEvent.type(screen.getByPlaceholderText(/ask a question/i), '   ');
    expect(screen.getByRole('button', { name: /post/i })).toBeDisabled();
    expect(postComment).not.toHaveBeenCalled();
  });

  it('surfaces a rejected post to the user instead of failing silently', async () => {
    postComment.mockRejectedValue({ response: { data: { message: "You're doing that too often." } } });

    render(<DiscussionPanel slug="two-sum" currentUser={AUTHOR} />);
    await screen.findByText(/no comments yet/i);

    await userEvent.type(screen.getByPlaceholderText(/ask a question/i), 'spam');
    await userEvent.click(screen.getByRole('button', { name: /post/i }));

    expect(await screen.findByText("You're doing that too often.")).toBeInTheDocument();
  });

  it('offers Delete only on your own comments', async () => {
    fetchDiscussions.mockResolvedValue({ threads: [thread({ userId: 'someone-else', username: 'bob' })], nextCursor: null, hasMore: false });

    render(<DiscussionPanel slug="two-sum" currentUser={AUTHOR} />);
    await screen.findByText('Use a hash map for O(n).');

    expect(screen.queryByRole('button', { name: /delete/i })).not.toBeInTheDocument();
  });

  it('lets an admin moderate anyone', async () => {
    fetchDiscussions.mockResolvedValue({ threads: [thread({ userId: 'someone-else', username: 'bob' })], nextCursor: null, hasMore: false });
    deleteComment.mockResolvedValue();

    render(<DiscussionPanel slug="two-sum" currentUser={{ id: 'mod', role: 'admin' }} />);
    await screen.findByText('Use a hash map for O(n).');

    await userEvent.click(screen.getByRole('button', { name: /delete/i }));

    await waitFor(() => expect(deleteComment).toHaveBeenCalledWith('c1'));
  });

  it('renders a removed comment as a tombstone so its replies keep their place', async () => {
    fetchDiscussions.mockResolvedValue({
      threads: [
        thread({
          deleted: true,
          content: '',
          replies: [{
            id: 'r1', userId: 'user-2', username: 'bob', content: 'Still visible.',
            upvotes: 0, upvotedByMe: false, createdAt: '2026-08-01T11:00:00Z',
          }],
        }),
      ],
      nextCursor: null,
      hasMore: false,
    });

    render(<DiscussionPanel slug="two-sum" currentUser={AUTHOR} />);

    expect(await screen.findByText(/comment removed/i)).toBeInTheDocument();
    expect(screen.getByText('Still visible.')).toBeInTheDocument();
  });
});

// --- Pagination ---

describe('DiscussionPanel pagination', () => {
  it('offers Load more only while another page exists', async () => {
    fetchDiscussions.mockResolvedValue({
      threads: [thread()], nextCursor: 'CURSOR1', hasMore: true,
    });

    render(<DiscussionPanel slug="two-sum" currentUser={AUTHOR} />);
    await screen.findByText('Use a hash map for O(n).');

    expect(screen.getByRole('button', { name: /load more/i })).toBeInTheDocument();
    expect(screen.queryByText(/end of the thread/i)).not.toBeInTheDocument();
  });

  it('marks the end of the thread when there is no next page', async () => {
    fetchDiscussions.mockResolvedValue({
      threads: [thread()], nextCursor: null, hasMore: false,
    });

    render(<DiscussionPanel slug="two-sum" currentUser={AUTHOR} />);
    await screen.findByText('Use a hash map for O(n).');

    expect(screen.queryByRole('button', { name: /load more/i })).not.toBeInTheDocument();
    expect(screen.getByText(/end of the thread/i)).toBeInTheDocument();
  });

  it('appends the next page and keeps what is already on screen', async () => {
    fetchDiscussions
      .mockResolvedValueOnce({ threads: [thread()], nextCursor: 'CURSOR1', hasMore: true })
      .mockResolvedValueOnce({
        threads: [thread({ id: 'c2', content: 'A later comment.' })],
        nextCursor: null,
        hasMore: false,
      });

    render(<DiscussionPanel slug="two-sum" currentUser={AUTHOR} />);
    await screen.findByText('Use a hash map for O(n).');

    await userEvent.click(screen.getByRole('button', { name: /load more/i }));

    expect(await screen.findByText('A later comment.')).toBeInTheDocument();
    expect(screen.getByText('Use a hash map for O(n).')).toBeInTheDocument();
    // The cursor from the first page must be sent back verbatim.
    expect(fetchDiscussions).toHaveBeenLastCalledWith('two-sum', { cursor: 'CURSOR1' });
  });

  it('does not duplicate a comment returned on both pages', async () => {
    fetchDiscussions
      .mockResolvedValueOnce({ threads: [thread()], nextCursor: 'CURSOR1', hasMore: true })
      .mockResolvedValueOnce({ threads: [thread()], nextCursor: null, hasMore: false });

    render(<DiscussionPanel slug="two-sum" currentUser={AUTHOR} />);
    await screen.findByText('Use a hash map for O(n).');

    await userEvent.click(screen.getByRole('button', { name: /load more/i }));

    await waitFor(() =>
      expect(screen.queryByRole('button', { name: /load more/i })).not.toBeInTheDocument()
    );
    expect(screen.getAllByText('Use a hash map for O(n).')).toHaveLength(1);
  });

  it('surfaces a failure to load more without losing the first page', async () => {
    fetchDiscussions
      .mockResolvedValueOnce({ threads: [thread()], nextCursor: 'CURSOR1', hasMore: true })
      .mockRejectedValueOnce(new Error('network down'));

    render(<DiscussionPanel slug="two-sum" currentUser={AUTHOR} />);
    await screen.findByText('Use a hash map for O(n).');

    await userEvent.click(screen.getByRole('button', { name: /load more/i }));

    expect(await screen.findByText(/could not load more comments/i)).toBeInTheDocument();
    expect(screen.getByText('Use a hash map for O(n).')).toBeInTheDocument();
  });

  it('requests the first page without a cursor', async () => {
    fetchDiscussions.mockResolvedValue({ threads: [], nextCursor: null, hasMore: false });

    render(<DiscussionPanel slug="two-sum" currentUser={AUTHOR} />);
    await screen.findByText(/no comments yet/i);

    expect(fetchDiscussions).toHaveBeenCalledWith('two-sum');
  });
});
