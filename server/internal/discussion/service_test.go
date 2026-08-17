package discussion_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/toji339/online-judge/internal/discussion"
	"github.com/toji339/online-judge/internal/discussion/discussiontest"
)

func newService() *discussion.Service {
	return discussion.NewService(discussiontest.New())
}

func post(t *testing.T, svc *discussion.Service, userID, content string) *discussion.Comment {
	t.Helper()
	c, err := svc.Create(context.Background(), discussion.CreateInput{
		ProblemID: "problem-1", UserID: userID, Username: "user-" + userID, Content: content,
	})
	require.NoError(t, err)
	return c
}

// --- Posting ---

func TestCreate_StoresATopLevelComment(t *testing.T) {
	svc := newService()

	c := post(t, svc, "user-1", "Try a hash map here.")

	assert.NotEmpty(t, c.ID)
	assert.Empty(t, c.ParentID)
	assert.Equal(t, 0, c.Upvotes)
	assert.False(t, c.CreatedAt.IsZero())
}

func TestCreate_TrimsAndRejectsEmptyContent(t *testing.T) {
	svc := newService()

	for _, content := range []string{"", "   ", "\n\t", "x"} {
		_, err := svc.Create(context.Background(), discussion.CreateInput{
			ProblemID: "problem-1", UserID: "user-1", Content: content,
		})
		var vErr discussion.ValidationError
		assert.ErrorAs(t, err, &vErr, "content %q must be rejected", content)
	}
}

func TestCreate_RejectsOverlongContent(t *testing.T) {
	svc := newService()

	_, err := svc.Create(context.Background(), discussion.CreateInput{
		ProblemID: "problem-1", UserID: "user-1", Content: strings.Repeat("a", 5000),
	})

	var vErr discussion.ValidationError
	assert.ErrorAs(t, err, &vErr)
}

// --- Threading ---

func TestCreate_ReplyAttachesToItsParent(t *testing.T) {
	svc := newService()
	parent := post(t, svc, "user-1", "Why is this O(n)?")

	reply, err := svc.Create(context.Background(), discussion.CreateInput{
		ProblemID: "problem-1", UserID: "user-2", Username: "user-2",
		ParentID: parent.ID, Content: "Because each element is visited once.",
	})

	require.NoError(t, err)
	assert.Equal(t, parent.ID, reply.ParentID)
	assert.True(t, reply.IsReply())
}

func TestCreate_RejectsReplyingToAReply(t *testing.T) {
	svc := newService()
	parent := post(t, svc, "user-1", "Why is this O(n)?")
	reply, err := svc.Create(context.Background(), discussion.CreateInput{
		ProblemID: "problem-1", UserID: "user-2", ParentID: parent.ID, Content: "Because of the single pass.",
	})
	require.NoError(t, err)

	_, err = svc.Create(context.Background(), discussion.CreateInput{
		ProblemID: "problem-1", UserID: "user-3", ParentID: reply.ID, Content: "Thanks!",
	})

	assert.ErrorIs(t, err, discussion.ErrNestingTooDeep, "threads stay one level deep")
}

func TestCreate_ReplyInheritsItsParentProblem(t *testing.T) {
	svc := newService()
	parent := post(t, svc, "user-1", "A question about this problem.")

	// The caller claims a different problem; the parent's must win.
	reply, err := svc.Create(context.Background(), discussion.CreateInput{
		ProblemID: "some-other-problem", UserID: "user-2", ParentID: parent.ID, Content: "An answer.",
	})

	require.NoError(t, err)
	assert.Equal(t, "problem-1", reply.ProblemID)
}

func TestListThreads_NestsRepliesUnderTheirParent(t *testing.T) {
	svc := newService()
	ctx := context.Background()
	first := post(t, svc, "user-1", "First question.")
	second := post(t, svc, "user-2", "Second question.")
	_, err := svc.Create(ctx, discussion.CreateInput{
		ProblemID: "problem-1", UserID: "user-3", ParentID: first.ID, Content: "An answer to the first.",
	})
	require.NoError(t, err)

	page, err := svc.ListThreads(ctx, "problem-1", "")
	require.NoError(t, err)
	threads := page.Threads
	require.Len(t, threads, 2, "only top-level comments are threads")
	// Newest first.
	assert.Equal(t, second.ID, threads[0].ID)
	assert.Empty(t, threads[0].Replies)
	assert.Equal(t, first.ID, threads[1].ID)
	require.Len(t, threads[1].Replies, 1)
	assert.Equal(t, "An answer to the first.", threads[1].Replies[0].Content)
}

func TestListThreads_UnknownProblemIsEmptyNotAnError(t *testing.T) {
	svc := newService()

	page, err := svc.ListThreads(context.Background(), "no-such-problem", "")
	require.NoError(t, err)
	assert.Empty(t, page.Threads)
	assert.False(t, page.HasMore)
}

// --- Voting ---

func TestUpvote_IsIdempotentPerUser(t *testing.T) {
	svc := newService()
	ctx := context.Background()
	c := post(t, svc, "user-1", "A helpful hint.")

	count, err := svc.Upvote(ctx, c.ID, "user-2")
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// Voting again must not inflate the count.
	count, err = svc.Upvote(ctx, c.ID, "user-2")
	require.NoError(t, err)
	assert.Equal(t, 1, count, "one user can only contribute one vote")

	count, err = svc.Upvote(ctx, c.ID, "user-3")
	require.NoError(t, err)
	assert.Equal(t, 2, count)
}

func TestRemoveUpvote_WithdrawsTheVote(t *testing.T) {
	svc := newService()
	ctx := context.Background()
	c := post(t, svc, "user-1", "A helpful hint.")
	_, err := svc.Upvote(ctx, c.ID, "user-2")
	require.NoError(t, err)

	count, err := svc.RemoveUpvote(ctx, c.ID, "user-2")

	require.NoError(t, err)
	assert.Equal(t, 0, count)

	// Removing a vote that is not there is harmless.
	count, err = svc.RemoveUpvote(ctx, c.ID, "user-2")
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestListThreads_MarksTheViewersOwnVotes(t *testing.T) {
	svc := newService()
	ctx := context.Background()
	c := post(t, svc, "user-1", "A helpful hint.")
	_, err := svc.Upvote(ctx, c.ID, "user-2")
	require.NoError(t, err)

	forVoter, err := svc.ListThreads(ctx, "problem-1", "user-2")
	require.NoError(t, err)
	require.Len(t, forVoter.Threads, 1)
	assert.True(t, forVoter.Threads[0].UpvotedByMe)

	forOther, err := svc.ListThreads(ctx, "problem-1", "user-9")
	require.NoError(t, err)
	assert.False(t, forOther.Threads[0].UpvotedByMe)
	assert.Nil(t, forOther.Threads[0].UpvotedBy, "the voter list is never exposed")
}

func TestUpvote_UnknownCommentReturnsNotFound(t *testing.T) {
	svc := newService()

	_, err := svc.Upvote(context.Background(), "missing", "user-1")

	assert.ErrorIs(t, err, discussion.ErrNotFound)
}

// --- Deletion ---

func TestDelete_RemovesOwnCommentButKeepsTheThread(t *testing.T) {
	svc := newService()
	ctx := context.Background()
	parent := post(t, svc, "user-1", "A question.")
	_, err := svc.Create(ctx, discussion.CreateInput{
		ProblemID: "problem-1", UserID: "user-2", ParentID: parent.ID, Content: "An answer.",
	})
	require.NoError(t, err)

	require.NoError(t, svc.Delete(ctx, parent.ID, "user-1", false))

	page, err := svc.ListThreads(ctx, "problem-1", "")
	require.NoError(t, err)
	threads := page.Threads
	require.Len(t, threads, 1, "the thread survives so its replies stay readable")
	assert.True(t, threads[0].Deleted)
	assert.Empty(t, threads[0].Content)
	assert.Len(t, threads[0].Replies, 1)
}

func TestDelete_RejectsDeletingSomeoneElsesComment(t *testing.T) {
	svc := newService()
	c := post(t, svc, "user-1", "A question.")

	err := svc.Delete(context.Background(), c.ID, "user-2", false)

	assert.ErrorIs(t, err, discussion.ErrNotAuthor)
}

func TestDelete_AdminMayModerateAnyComment(t *testing.T) {
	svc := newService()
	c := post(t, svc, "user-1", "Spam spam spam.")

	err := svc.Delete(context.Background(), c.ID, "moderator", true)

	require.NoError(t, err)
}
