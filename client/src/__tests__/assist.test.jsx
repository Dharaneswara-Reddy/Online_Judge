/**
 * Component tests for the AI assist UI.
 *
 * The rules being checked here are pedagogical and financial, not
 * cosmetic. An assistant that interrupts gets switched off; one that
 * descends the hint ladder by itself hands out answers; and one that
 * calls the model without a click puts the API bill in the hands of
 * whoever is submitting fastest. Each of those is a behaviour, so each
 * of them gets a test.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

vi.mock('../api/assist', () => ({
  fetchAssistState: vi.fn(),
  requestHint: vi.fn(),
  explainVerdict: vi.fn(),
  reviewSolution: vi.fn(),
}));

import {
  fetchAssistState,
  requestHint,
  explainVerdict,
  reviewSolution,
} from '../api/assist';
import StuckNudge from '../components/assist/StuckNudge';
import HintPanel from '../components/assist/HintPanel';
import VerdictInsight from '../components/assist/VerdictInsight';

const stuckState = {
  stuck: true,
  reason: '3 of your attempts failed on the same hidden test case.',
  attempts: 3,
  maxRung: 3,
  lastStatus: 'wrong_answer',
};

beforeEach(() => {
  vi.clearAllMocks();
  window.localStorage.clear();
  fetchAssistState.mockResolvedValue({ stuck: false, reason: '', attempts: 0, maxRung: 0 });
  requestHint.mockImplementation(async ({ rung }) => ({
    rung,
    text: `Prose for rung ${rung}.`,
    cached: false,
  }));
});

describe('StuckNudge', () => {
  it('stays out of the way when the student is not stuck', async () => {
    render(<StuckNudge slug="two-sum" />);

    await waitFor(() => expect(fetchAssistState).toHaveBeenCalled());
    expect(screen.queryByRole('button', { name: /want a hint/i })).toBeNull();
  });

  it('offers help, quietly, once the signals say so', async () => {
    fetchAssistState.mockResolvedValue(stuckState);

    render(<StuckNudge slug="two-sum" />);

    expect(await screen.findByText(stuckState.reason)).toBeInTheDocument();
    // A status region, not a dialog: it must not take focus or trap it.
    expect(screen.getByRole('status')).toBeInTheDocument();
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('never asks again on a problem the student turned down', async () => {
    fetchAssistState.mockResolvedValue(stuckState);
    const user = userEvent.setup();

    const { unmount } = render(<StuckNudge slug="two-sum" />);
    await user.click(await screen.findByRole('button', { name: /no thanks/i }));
    expect(screen.queryByText(stuckState.reason)).toBeNull();

    // A dismissal that only lasted the session would be an assistant
    // that asks again tomorrow, which is the thing people disable.
    unmount();
    fetchAssistState.mockClear();
    render(<StuckNudge slug="two-sum" />);

    await waitFor(() => expect(screen.queryByText(stuckState.reason)).toBeNull());
    expect(fetchAssistState).not.toHaveBeenCalled();
  });

  it('keeps dismissals separate per problem', async () => {
    fetchAssistState.mockResolvedValue(stuckState);
    const user = userEvent.setup();

    const { unmount } = render(<StuckNudge slug="two-sum" />);
    await user.click(await screen.findByRole('button', { name: /no thanks/i }));
    unmount();

    render(<StuckNudge slug="valid-parentheses" />);
    expect(await screen.findByText(stuckState.reason)).toBeInTheDocument();
  });

  it('disappears entirely when assist is switched off server-side', async () => {
    fetchAssistState.mockResolvedValue(null);
    const onDisabled = vi.fn();

    render(<StuckNudge slug="two-sum" onDisabled={onDisabled} />);

    await waitFor(() => expect(onDisabled).toHaveBeenCalled());
    expect(screen.queryByRole('status')).toBeNull();
  });

  it('says nothing when the check itself fails', async () => {
    fetchAssistState.mockRejectedValue(new Error('network'));

    render(<StuckNudge slug="two-sum" />);

    await waitFor(() => expect(fetchAssistState).toHaveBeenCalled());
    expect(screen.queryByRole('status')).toBeNull();
  });

  it('re-checks on a new verdict and not otherwise', async () => {
    const { rerender } = render(<StuckNudge slug="two-sum" refreshKey={0} />);
    await waitFor(() => expect(fetchAssistState).toHaveBeenCalledTimes(1));

    // A re-render with nothing new must not cost a request.
    rerender(<StuckNudge slug="two-sum" refreshKey={0} />);
    expect(fetchAssistState).toHaveBeenCalledTimes(1);

    rerender(<StuckNudge slug="two-sum" refreshKey={1} />);
    await waitFor(() => expect(fetchAssistState).toHaveBeenCalledTimes(2));
  });
});

describe('HintPanel', () => {
  const renderPanel = (props = {}) =>
    render(<HintPanel slug="two-sum" language="python" code="pass" maxRung={4} {...props} />);

  it('opens at the first rung and stops there', async () => {
    renderPanel();

    expect(await screen.findByText('Prose for rung 1.')).toBeInTheDocument();
    // The ladder descending on its own would be the whole guardrail gone.
    expect(requestHint).toHaveBeenCalledTimes(1);
    expect(requestHint).toHaveBeenCalledWith(expect.objectContaining({ rung: 1 }));
    expect(screen.queryByText('Prose for rung 2.')).toBeNull();
  });

  it('descends only when asked, one rung per click', async () => {
    const user = userEvent.setup();
    renderPanel();
    await screen.findByText('Prose for rung 1.');

    await user.click(screen.getByRole('button', { name: /show me a little more/i }));

    expect(await screen.findByText('Prose for rung 2.')).toBeInTheDocument();
    expect(requestHint).toHaveBeenCalledTimes(2);
    expect(requestHint).toHaveBeenLastCalledWith(expect.objectContaining({ rung: 2 }));
    expect(screen.queryByText('Prose for rung 3.')).toBeNull();
  });

  it('says what the next rung will and will not tell them', async () => {
    renderPanel();
    await screen.findByText('Prose for rung 1.');

    expect(screen.getByText(/names the kind of approach/i)).toBeInTheDocument();
    expect(screen.getByText(/does not name the algorithm/i)).toBeInTheDocument();
  });

  it('keeps the tally of hints taken in view', async () => {
    const user = userEvent.setup();
    renderPanel();
    await screen.findByText('Prose for rung 1.');
    expect(screen.getByText(/1 of 4 used on this problem/i)).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: /show me a little more/i }));
    expect(await screen.findByText(/2 of 4 used on this problem/i)).toBeInTheDocument();
  });

  it("stops at the rung the server signals justify", async () => {
    const user = userEvent.setup();
    renderPanel({ maxRung: 2 });
    await screen.findByText('Prose for rung 1.');

    await user.click(screen.getByRole('button', { name: /show me a little more/i }));
    await screen.findByText('Prose for rung 2.');

    expect(screen.queryByRole('button', { name: /show me a little more/i })).toBeNull();
    expect(screen.getByText(/as far as the ladder goes/i)).toBeInTheDocument();
  });

  it('renders hints as prose, never as a code block', async () => {
    requestHint.mockResolvedValue({
      rung: 1,
      text: 'Track the smallest price seen so far.\n\nThen compare each day against it.',
      cached: false,
    });
    const { container } = renderPanel();

    await screen.findByText('Track the smallest price seen so far.');
    expect(container.querySelector('pre')).toBeNull();
    expect(container.querySelector('code')).toBeNull();
  });

  it('discloses that code leaves the platform', async () => {
    renderPanel();
    await screen.findByText('Prose for rung 1.');

    expect(screen.getByText(/sent to an external AI model/i)).toBeInTheDocument();
  });

  it('offers a retry rather than a dead panel when the first rung fails', async () => {
    requestHint.mockRejectedValueOnce(new Error('boom'));
    const user = userEvent.setup();
    renderPanel();

    const retry = await screen.findByRole('button', { name: /try again/i });
    await user.click(retry);

    expect(await screen.findByText('Prose for rung 1.')).toBeInTheDocument();
  });
});

describe('VerdictInsight', () => {
  it('offers an explanation for a failing verdict, and only on a click', async () => {
    explainVerdict.mockResolvedValue({ text: 'Your loop is quadratic.', cached: false });
    const user = userEvent.setup();

    render(<VerdictInsight submissionId="s1" status="wrong_answer" />);

    // Nothing is generated until someone asks for it.
    expect(explainVerdict).not.toHaveBeenCalled();

    await user.click(screen.getByRole('button', { name: /explain this verdict/i }));
    expect(await screen.findByText('Your loop is quadratic.')).toBeInTheDocument();
    expect(explainVerdict).toHaveBeenCalledWith('s1');
  });

  it('offers a review instead once the submission is accepted', async () => {
    reviewSolution.mockResolvedValue({ text: 'Linear time, constant space.' });
    const user = userEvent.setup();

    render(<VerdictInsight submissionId="s2" status="accepted" />);

    await user.click(screen.getByRole('button', { name: /review my solution/i }));
    expect(await screen.findByText('Linear time, constant space.')).toBeInTheDocument();
    expect(explainVerdict).not.toHaveBeenCalled();
  });

  it('offers nothing while a verdict is still pending', () => {
    render(<VerdictInsight submissionId="s3" status="pending" />);
    expect(screen.queryByRole('button')).toBeNull();
  });

  it('offers nothing when the judge itself failed', () => {
    // "error" is infrastructure, not a verdict on the code; there is
    // nothing about it a model could usefully explain.
    render(<VerdictInsight submissionId="s4" status="error" />);
    expect(screen.queryByRole('button')).toBeNull();
  });

  it('hides itself when assist is switched off server-side', async () => {
    explainVerdict.mockResolvedValue(null);
    const onDisabled = vi.fn();
    const user = userEvent.setup();

    render(<VerdictInsight submissionId="s5" status="tle" onDisabled={onDisabled} />);
    await user.click(screen.getByRole('button', { name: /explain this verdict/i }));

    await waitFor(() => expect(onDisabled).toHaveBeenCalled());
    expect(screen.queryByRole('button')).toBeNull();
  });

  it('reports a failed call in one sentence and keeps the offer', async () => {
    explainVerdict.mockRejectedValue({ response: { data: { message: 'Too many requests.' } } });
    const user = userEvent.setup();

    render(<VerdictInsight submissionId="s6" status="wrong_answer" />);
    await user.click(screen.getByRole('button', { name: /explain this verdict/i }));

    expect(await screen.findByText('Too many requests.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /explain this verdict/i })).toBeInTheDocument();
  });
});
