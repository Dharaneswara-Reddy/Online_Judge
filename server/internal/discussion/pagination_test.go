package discussion_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/toji339/online-judge/internal/discussion"
)

// seedThread posts n top-level comments and returns their IDs in the
// order they were created (oldest first).
func seedThread(t *testing.T, svc *discussion.Service, n int) []string {
	t.Helper()
	ids := make([]string, 0, n)
	for i := range n {
		c := post(t, svc, "user-1", fmt.Sprintf("comment number %d", i))
		ids = append(ids, c.ID)
	}
	return ids
}

// collectAllPages walks the thread with the given page size and returns
// every id it saw, plus how many requests it took.
func collectAllPages(t *testing.T, svc *discussion.Service, limit int) ([]string, int) {
	t.Helper()
	var seen []string
	cursor := ""
	requests := 0

	for {
		page, err := svc.ListThreadPage(context.Background(), "problem-1", "", cursor, limit)
		require.NoError(t, err)
		requests++

		for _, thread := range page.Threads {
			seen = append(seen, thread.ID)
		}
		if !page.HasMore {
			assert.Empty(t, page.NextCursor, "the last page must not offer a cursor")
			break
		}
		require.NotEmpty(t, page.NextCursor, "hasMore implies a cursor")
		cursor = page.NextCursor

		require.Less(t, requests, 50, "pagination did not terminate")
	}
	return seen, requests
}

// --- Page shape ---

func TestPagination_EmptyThread(t *testing.T) {
	svc := newService()

	page, err := svc.ListThreadPage(context.Background(), "problem-1", "", "", 10)

	require.NoError(t, err)
	assert.Empty(t, page.Threads)
	assert.False(t, page.HasMore)
	assert.Empty(t, page.NextCursor)
}

func TestPagination_SinglePageThread(t *testing.T) {
	svc := newService()
	seedThread(t, svc, 3)

	page, err := svc.ListThreadPage(context.Background(), "problem-1", "", "", 10)

	require.NoError(t, err)
	assert.Len(t, page.Threads, 3)
	assert.False(t, page.HasMore, "everything fits on one page")
	assert.Empty(t, page.NextCursor)
}

func TestPagination_ExactPageBoundary(t *testing.T) {
	svc := newService()
	seedThread(t, svc, 10)

	// Exactly one page of results: there is no second page, and claiming
	// otherwise would send the client on a pointless extra request.
	page, err := svc.ListThreadPage(context.Background(), "problem-1", "", "", 10)

	require.NoError(t, err)
	assert.Len(t, page.Threads, 10)
	assert.False(t, page.HasMore)
}

func TestPagination_FinalPartialPage(t *testing.T) {
	svc := newService()
	seedThread(t, svc, 7)

	first, err := svc.ListThreadPage(context.Background(), "problem-1", "", "", 5)
	require.NoError(t, err)
	require.True(t, first.HasMore)
	require.Len(t, first.Threads, 5)

	last, err := svc.ListThreadPage(context.Background(), "problem-1", "", first.NextCursor, 5)
	require.NoError(t, err)
	assert.Len(t, last.Threads, 2, "the final page carries the remainder")
	assert.False(t, last.HasMore)
}

// --- Walking the whole thread ---

func TestPagination_WalksEveryCommentExactlyOnce(t *testing.T) {
	svc := newService()
	created := seedThread(t, svc, 25)

	seen, requests := collectAllPages(t, svc, 10)

	assert.Len(t, seen, len(created), "every comment appears")
	assert.Equal(t, 3, requests, "25 comments at 10 per page is three requests")

	unique := map[string]int{}
	for _, id := range seen {
		unique[id]++
	}
	assert.Len(t, unique, len(created), "no comment is skipped")
	for id, count := range unique {
		assert.Equal(t, 1, count, "comment %s appeared more than once across pages", id)
	}
}

func TestPagination_OrderingIsNewestFirstAndStable(t *testing.T) {
	svc := newService()
	created := seedThread(t, svc, 12)

	seen, _ := collectAllPages(t, svc, 5)

	// created is oldest-first, so the paged order is its reverse.
	expected := make([]string, 0, len(created))
	for i := len(created) - 1; i >= 0; i-- {
		expected = append(expected, created[i])
	}
	assert.Equal(t, expected, seen)
}

// TestPagination_HandlesDuplicateTimestamps is why the cursor carries an
// id as well as a time: the in-memory fake stamps these comments within
// the same instant, which without a tie-break makes the page boundary
// ambiguous.
func TestPagination_HandlesDuplicateTimestamps(t *testing.T) {
	svc := newService()
	created := seedThread(t, svc, 9)

	seen, _ := collectAllPages(t, svc, 2)

	assert.Len(t, seen, len(created))
	unique := map[string]bool{}
	for _, id := range seen {
		assert.False(t, unique[id], "comment %s was returned twice", id)
		unique[id] = true
	}
}

func TestPagination_LargeThreadStaysBounded(t *testing.T) {
	svc := newService()
	seedThread(t, svc, 150)

	page, err := svc.ListThreadPage(context.Background(), "problem-1", "", "", 0)

	require.NoError(t, err)
	assert.Len(t, page.Threads, discussion.DefaultPageSize,
		"an unspecified limit must not return the whole thread")
	assert.True(t, page.HasMore)
}

// --- Limits ---

func TestPagination_LimitBounds(t *testing.T) {
	svc := newService()
	seedThread(t, svc, 150)
	ctx := context.Background()

	cases := map[string]struct {
		requested int
		expect    int
	}{
		"omitted takes the default":  {0, discussion.DefaultPageSize},
		"negative takes the default": {-5, discussion.DefaultPageSize},
		"one is honoured":            {1, 1},
		"under the maximum":          {50, 50},
		"at the maximum":             {discussion.MaxPageSize, discussion.MaxPageSize},
		"over the maximum is capped": {100000, discussion.MaxPageSize},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			page, err := svc.ListThreadPage(ctx, "problem-1", "", "", tc.requested)
			require.NoError(t, err)
			assert.Len(t, page.Threads, tc.expect)
		})
	}
}

func TestClampPageSize(t *testing.T) {
	assert.Equal(t, discussion.DefaultPageSize, discussion.ClampPageSize(0))
	assert.Equal(t, discussion.DefaultPageSize, discussion.ClampPageSize(-1))
	assert.Equal(t, 7, discussion.ClampPageSize(7))
	assert.Equal(t, discussion.MaxPageSize, discussion.ClampPageSize(discussion.MaxPageSize+1))
}

// --- Cursors ---

func TestPagination_RejectsMalformedCursors(t *testing.T) {
	svc := newService()
	seedThread(t, svc, 5)
	ctx := context.Background()

	for _, bad := range []string{
		"not-base64!!",
		"YWJjZGVm",               // valid base64, wrong shape
		"MTIzfA",                 // timestamp with an empty id
		"fGFiYw",                 // empty timestamp
		"bm90LWEtbnVtYmVyfGFiYw", // non-numeric timestamp
	} {
		_, err := svc.ListThreadPage(ctx, "problem-1", "", bad, 10)
		assert.ErrorIs(t, err, discussion.ErrInvalidCursor, "cursor %q must be rejected", bad)
	}
}

func TestCursor_RoundTrips(t *testing.T) {
	svc := newService()
	seedThread(t, svc, 4)

	page, err := svc.ListThreadPage(context.Background(), "problem-1", "", "", 2)
	require.NoError(t, err)
	require.True(t, page.HasMore)

	decoded, err := discussion.DecodeCursor(page.NextCursor)
	require.NoError(t, err)
	require.NotNil(t, decoded)

	last := page.Threads[len(page.Threads)-1]
	assert.Equal(t, last.ID, decoded.ID)
	assert.Equal(t, last.CreatedAt.UTC().UnixNano(), decoded.CreatedAt.UnixNano())
}

func TestCursor_EmptyMeansStartOfThread(t *testing.T) {
	decoded, err := discussion.DecodeCursor("")

	require.NoError(t, err)
	assert.Nil(t, decoded, "an absent cursor is not an error")
}

// --- Replies ---

func TestPagination_RepliesTravelWithTheirRoot(t *testing.T) {
	svc := newService()
	ctx := context.Background()
	root := post(t, svc, "user-1", "The question everyone replies to.")
	for i := range 3 {
		_, err := svc.Create(ctx, discussion.CreateInput{
			ProblemID: "problem-1", UserID: "user-2",
			ParentID: root.ID, Content: fmt.Sprintf("reply %d", i),
		})
		require.NoError(t, err)
	}
	// Newer roots push the replied-to one onto a later page.
	seedThread(t, svc, 4)

	seen, _ := collectAllPages(t, svc, 2)
	assert.Len(t, seen, 5, "replies are not counted as threads")

	// Find the page holding the root and check its replies came with it.
	var found *discussion.Comment
	cursor := ""
	for found == nil {
		page, err := svc.ListThreadPage(ctx, "problem-1", "", cursor, 2)
		require.NoError(t, err)
		for i := range page.Threads {
			if page.Threads[i].ID == root.ID {
				found = &page.Threads[i]
				break
			}
		}
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}
	require.NotNil(t, found, "the root should appear on some page")
	assert.Len(t, found.Replies, 3)
}
