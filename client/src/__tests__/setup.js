/**
 * Test setup shared by every suite.
 *
 * Registers the jest-dom matchers and clears the rendered DOM plus any
 * mock state between tests, so one test can never leak into the next.
 */

import '@testing-library/jest-dom/vitest';
import { cleanup } from '@testing-library/react';
import { afterEach, vi } from 'vitest';

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  vi.useRealTimers();
});
