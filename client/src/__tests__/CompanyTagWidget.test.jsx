/**
 * Component tests for the "have you seen this in an interview?" widget.
 *
 * The aggregation and deduplication rules are enforced on the server;
 * what matters here is that the widget reflects them honestly — showing
 * the tally, remembering what you already reported, and surfacing a
 * rejected duplicate rather than pretending it worked.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';

vi.mock('../api/companies', () => ({
  fetchCompanyTags: vi.fn(),
  tagCompany: vi.fn(),
}));

import { fetchCompanyTags, tagCompany } from '../api/companies';
import CompanyTagWidget from '../components/problem/CompanyTagWidget';

const renderWidget = (props = {}) =>
  render(
    <MemoryRouter>
      <CompanyTagWidget slug="two-sum" signedIn {...props} />
    </MemoryRouter>
  );

beforeEach(() => {
  vi.clearAllMocks();
  fetchCompanyTags.mockResolvedValue({ companies: [], myTags: [] });
});

describe('CompanyTagWidget', () => {
  it('lists reported companies with their tally', async () => {
    fetchCompanyTags.mockResolvedValue({
      companies: [
        { company: 'Google', tagCount: 12, problemCount: 1 },
        { company: 'Amazon', tagCount: 3, problemCount: 1 },
      ],
      myTags: [],
    });

    renderWidget();

    expect(await screen.findByText('Google')).toBeInTheDocument();
    expect(screen.getByText('12')).toBeInTheDocument();
    expect(screen.getByText('Amazon')).toBeInTheDocument();
  });

  it('links each company through to the explorer', async () => {
    fetchCompanyTags.mockResolvedValue({
      companies: [{ company: 'Jane Street', tagCount: 1, problemCount: 1 }],
      myTags: [],
    });

    renderWidget();

    const link = await screen.findByRole('link', { name: /jane street/i });
    expect(link).toHaveAttribute('href', '/companies/Jane%20Street');
  });

  it('reminds you which companies you already reported', async () => {
    fetchCompanyTags.mockResolvedValue({
      companies: [{ company: 'Google', tagCount: 1, problemCount: 1 }],
      myTags: [{ id: 't1', company: 'Google', round: 'oa' }],
    });

    renderWidget();

    expect(await screen.findByText(/you reported: google/i)).toBeInTheDocument();
  });

  it('submits a normalised tag and refreshes the tally', async () => {
    tagCompany.mockResolvedValue({ id: 't1' });

    renderWidget();
    await screen.findByText(/no reports yet/i);

    await userEvent.click(screen.getByRole('button', { name: /seen this in an interview/i }));
    await userEvent.type(screen.getByPlaceholderText(/company name/i), 'Stripe');
    await userEvent.selectOptions(screen.getByRole('combobox'), 'onsite');
    await userEvent.click(screen.getByRole('button', { name: /^add$/i }));

    await waitFor(() =>
      expect(tagCompany).toHaveBeenCalledWith('two-sum', { company: 'Stripe', round: 'onsite' })
    );
    expect(fetchCompanyTags).toHaveBeenCalledTimes(2);
  });

  it('shows the server’s rejection when you tag the same company twice', async () => {
    tagCompany.mockRejectedValue({
      response: { data: { message: 'you have already tagged this problem with that company' } },
    });

    renderWidget();
    await screen.findByText(/no reports yet/i);

    await userEvent.click(screen.getByRole('button', { name: /seen this in an interview/i }));
    await userEvent.type(screen.getByPlaceholderText(/company name/i), 'Google');
    await userEvent.click(screen.getByRole('button', { name: /^add$/i }));

    expect(await screen.findByText(/already tagged this problem/i)).toBeInTheDocument();
  });

  it('asks anonymous readers to sign in rather than offering the form', async () => {
    renderWidget({ signedIn: false });

    expect(await screen.findByText(/sign in to report/i)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /seen this in an interview/i })).not.toBeInTheDocument();
  });
});
