package discussion_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/toji339/online-judge/internal/discussion"
	"github.com/toji339/online-judge/internal/discussion/discussiontest"
)

// Replies were fetched with no limit at all, for every root on the page.
// One thread with 50k replies pulled all of them into a process with a
// 112 MB memory cap, while three separate comments claimed the query was
// bounded.

func TestListThreadPage_BoundsRepliesPerComment(t *testing.T) {
	repo := discussiontest.New()
	svc := discussion.NewService(repo)
	ctx := context.Background()

	root, err := svc.Create(ctx, discussion.CreateInput{
		ProblemID: "p1", UserID: "u1", Username: "u1", Content: "the root",
	})
	if err != nil {
		t.Fatalf("create root: %v", err)
	}

	const replies = discussion.MaxRepliesPerComment + 25
	for i := 0; i < replies; i++ {
		if _, err := svc.Create(ctx, discussion.CreateInput{
			ProblemID: "p1", UserID: "u2", Username: "u2",
			ParentID: root.ID, Content: fmt.Sprintf("reply %d", i),
		}); err != nil {
			t.Fatalf("create reply %d: %v", i, err)
		}
	}

	page, err := svc.ListThreads(ctx, "p1", "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(page.Threads) != 1 {
		t.Fatalf("got %d threads, want 1", len(page.Threads))
	}

	thread := page.Threads[0]
	if len(thread.Replies) > discussion.MaxRepliesPerComment {
		t.Errorf("thread carries %d replies, want at most %d",
			len(thread.Replies), discussion.MaxRepliesPerComment)
	}
	if !thread.HasMoreReplies {
		t.Error("hasMoreReplies is false even though replies were held back")
	}
	// Oldest first, and the page starts at the beginning of the thread.
	if len(thread.Replies) > 0 && thread.Replies[0].Content != "reply 0" {
		t.Errorf("first reply = %q, want %q", thread.Replies[0].Content, "reply 0")
	}
}

func TestListThreadPage_ShortThreadIsNotMarkedTruncated(t *testing.T) {
	repo := discussiontest.New()
	svc := discussion.NewService(repo)
	ctx := context.Background()

	root, err := svc.Create(ctx, discussion.CreateInput{
		ProblemID: "p1", UserID: "u1", Username: "u1", Content: "the root",
	})
	if err != nil {
		t.Fatalf("create root: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := svc.Create(ctx, discussion.CreateInput{
			ProblemID: "p1", UserID: "u2", Username: "u2",
			ParentID: root.ID, Content: fmt.Sprintf("reply %d", i),
		}); err != nil {
			t.Fatalf("create reply: %v", err)
		}
	}

	page, err := svc.ListThreads(ctx, "p1", "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	thread := page.Threads[0]
	if len(thread.Replies) != 3 {
		t.Fatalf("got %d replies, want 3", len(thread.Replies))
	}
	if thread.HasMoreReplies {
		t.Error("hasMoreReplies is true for a thread that was returned whole")
	}
}

// TestListThreadPage_OneHugeThreadDoesNotStarveTheOthers is the fairness
// half of the bound: the cap is per comment, so a thread with thousands
// of replies cannot consume the whole budget and leave its neighbours on
// the page showing none.
func TestListThreadPage_OneHugeThreadDoesNotStarveTheOthers(t *testing.T) {
	repo := discussiontest.New()
	svc := discussion.NewService(repo)
	ctx := context.Background()

	huge, err := svc.Create(ctx, discussion.CreateInput{
		ProblemID: "p1", UserID: "u1", Username: "u1", Content: "huge",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := 0; i < discussion.MaxRepliesPerComment*3; i++ {
		if _, err := svc.Create(ctx, discussion.CreateInput{
			ProblemID: "p1", UserID: "u2", Username: "u2",
			ParentID: huge.ID, Content: fmt.Sprintf("noise %d", i),
		}); err != nil {
			t.Fatalf("create reply: %v", err)
		}
	}

	quiet, err := svc.Create(ctx, discussion.CreateInput{
		ProblemID: "p1", UserID: "u3", Username: "u3", Content: "quiet",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.Create(ctx, discussion.CreateInput{
		ProblemID: "p1", UserID: "u4", Username: "u4",
		ParentID: quiet.ID, Content: "the only reply",
	}); err != nil {
		t.Fatalf("create reply: %v", err)
	}

	page, err := svc.ListThreads(ctx, "p1", "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	byID := make(map[string]discussion.Comment, len(page.Threads))
	for _, thread := range page.Threads {
		byID[thread.ID] = thread
	}
	if got := len(byID[quiet.ID].Replies); got != 1 {
		t.Errorf("the quiet thread shows %d replies, want 1", got)
	}
	if got := len(byID[huge.ID].Replies); got != discussion.MaxRepliesPerComment {
		t.Errorf("the huge thread shows %d replies, want the cap of %d",
			got, discussion.MaxRepliesPerComment)
	}
}

// TestListReplies_AsksForNoMoreThanTheCap pins the bound at the storage
// boundary, which is where the memory is actually spent.
func TestListReplies_AsksForNoMoreThanTheCap(t *testing.T) {
	repo := discussiontest.New()
	svc := discussion.NewService(repo)
	ctx := context.Background()

	root, err := svc.Create(ctx, discussion.CreateInput{
		ProblemID: "p1", UserID: "u1", Username: "u1", Content: "the root",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := 0; i < 500; i++ {
		if _, err := svc.Create(ctx, discussion.CreateInput{
			ProblemID: "p1", UserID: "u2", Username: "u2",
			ParentID: root.ID, Content: fmt.Sprintf("reply %d", i),
		}); err != nil {
			t.Fatalf("create reply: %v", err)
		}
	}

	if _, err := svc.ListThreads(ctx, "p1", ""); err != nil {
		t.Fatalf("list: %v", err)
	}

	limit := repo.LastReplyLimit()
	if limit <= 0 {
		t.Fatalf("the repository was asked for an unbounded reply list (limit=%d)", limit)
	}
	if limit > discussion.MaxRepliesPerComment+1 {
		t.Errorf("the repository was asked for %d replies per comment, want at most %d",
			limit, discussion.MaxRepliesPerComment+1)
	}
	if rows := repo.LastReplyRowCount(); rows > (discussion.MaxRepliesPerComment+1)*1 {
		t.Errorf("the repository returned %d rows for one root, want at most %d",
			rows, discussion.MaxRepliesPerComment+1)
	}
}
