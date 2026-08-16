/**
 * Tests for the submission API module.
 *
 * The polling helper is the most delicate piece of client logic: the API
 * answers a submission with 202 and no verdict, so everything the user
 * sees depends on this loop terminating correctly.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest';

// The module under test imports the shared axios instance, so that is
// what gets mocked — the tests never make a real request.
vi.mock('../api/axios', () => ({
  default: { get: vi.fn(), post: vi.fn(), delete: vi.fn(), patch: vi.fn() },
}));

import api from '../api/axios';
import {
  isTerminalStatus,
  pollSubmission,
  submitSolution,
  fetchMySubmissions,
} from '../api/submissions';

beforeEach(() => {
  vi.clearAllMocks();
});

describe('isTerminalStatus', () => {
  it('treats every verdict as terminal', () => {
    for (const status of [
      'accepted', 'wrong_answer', 'tle', 'mle', 'runtime_error', 'compile_error',
    ]) {
      expect(isTerminalStatus(status)).toBe(true);
    }
  });

  it('treats queued and running as non-terminal', () => {
    expect(isTerminalStatus('pending')).toBe(false);
    expect(isTerminalStatus('running')).toBe(false);
  });

  it('does not treat an unknown status as terminal', () => {
    // Guarding against a future backend status silently ending the poll
    // and showing the user "no verdict".
    expect(isTerminalStatus('something_new')).toBe(false);
    expect(isTerminalStatus(undefined)).toBe(false);
  });
});

describe('pollSubmission', () => {
  it('stops as soon as a verdict arrives and returns it', async () => {
    api.get
      .mockResolvedValueOnce({ data: { status: 'pending' } })
      .mockResolvedValueOnce({ data: { status: 'running' } })
      .mockResolvedValueOnce({ data: { status: 'accepted', runtimeMs: 12 } });

    const result = await pollSubmission('sub-1', { intervalMs: 1 });

    expect(result.status).toBe('accepted');
    expect(api.get).toHaveBeenCalledTimes(3);
    expect(api.get).toHaveBeenCalledWith('/submissions/sub-1');
  });

  it('reports every intermediate state so the UI can show progress', async () => {
    api.get
      .mockResolvedValueOnce({ data: { status: 'pending' } })
      .mockResolvedValueOnce({ data: { status: 'running' } })
      .mockResolvedValueOnce({ data: { status: 'wrong_answer' } });

    const seen = [];
    await pollSubmission('sub-1', { intervalMs: 1, onUpdate: (s) => seen.push(s.status) });

    expect(seen).toEqual(['pending', 'running', 'wrong_answer']);
  });

  it('returns the last known state rather than hanging when the verdict never comes', async () => {
    api.get.mockResolvedValue({ data: { status: 'pending' } });

    const result = await pollSubmission('stuck', { intervalMs: 1, timeoutMs: 30 });

    expect(result.status).toBe('pending');
    // The important property is that it resolved at all.
    expect(api.get).toHaveBeenCalled();
  });

  it('does not poll again once the first response is already terminal', async () => {
    api.get.mockResolvedValueOnce({ data: { status: 'compile_error' } });

    const result = await pollSubmission('sub-1', { intervalMs: 1 });

    expect(result.status).toBe('compile_error');
    expect(api.get).toHaveBeenCalledTimes(1);
  });

  it('propagates a request failure instead of silently looping forever', async () => {
    api.get.mockRejectedValue(new Error('network down'));

    await expect(pollSubmission('sub-1', { intervalMs: 1 })).rejects.toThrow('network down');
  });
});

describe('submitSolution', () => {
  it('posts the language and code to the problem submit endpoint', async () => {
    api.post.mockResolvedValue({ data: { submissionId: 'abc', status: 'pending' } });

    const data = await submitSolution({ slug: 'two-sum', language: 'python', code: 'print(1)' });

    expect(api.post).toHaveBeenCalledWith('/problems/two-sum/submit', {
      language: 'python',
      code: 'print(1)',
    });
    expect(data.submissionId).toBe('abc');
  });
});

describe('fetchMySubmissions', () => {
  it('omits empty filters rather than sending blank query parameters', async () => {
    api.get.mockResolvedValue({ data: { data: [], total: 0 } });

    await fetchMySubmissions({ status: '', page: 2, pageSize: 10 });

    expect(api.get).toHaveBeenCalledWith('/users/me/submissions', {
      params: { page: 2, pageSize: 10 },
    });
  });
});
