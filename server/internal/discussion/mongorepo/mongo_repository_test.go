package mongorepo_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/toji339/online-judge/internal/database"
	"github.com/toji339/online-judge/internal/discussion"
	"github.com/toji339/online-judge/internal/discussion/mongorepo"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// These tests need a real MongoDB: what is under test is how concurrent
// updates interleave inside the database, which a fake cannot reproduce.
// Set TEST_MONGO_URI to run them; without it they skip.
func testRepo(t *testing.T) (*mongorepo.MongoRepository, *mongo.Database, string) {
	t.Helper()

	uri := os.Getenv("TEST_MONGO_URI")
	if uri == "" {
		t.Skip("TEST_MONGO_URI not set — skipping MongoDB-backed discussion tests")
	}

	client, err := database.Connect(uri)
	if err != nil {
		t.Fatalf("connect to %s: %v", uri, err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })

	db := client.Database("online_judge_test")
	problemID := fmt.Sprintf("discussion-repo-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = db.Collection("discussions").DeleteMany(ctx, bson.M{"problem_id": problemID})
	})
	return mongorepo.New(db), db, problemID
}

func newComment(t *testing.T, repo *mongorepo.MongoRepository, problemID, parentID string) *discussion.Comment {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c := &discussion.Comment{
		ProblemID: problemID,
		UserID:    "author",
		Username:  "author",
		ParentID:  parentID,
		Content:   "a comment",
		UpvotedBy: []string{},
		CreatedAt: time.Now().UTC(),
	}
	if err := repo.Create(ctx, c); err != nil {
		t.Fatalf("create comment: %v", err)
	}
	return c
}

// storedUpvotes returns the denormalised counter and the authoritative
// voter set, which must agree.
func storedUpvotes(t *testing.T, db *mongo.Database, commentID string) (int, int) {
	t.Helper()

	oid, err := bson.ObjectIDFromHex(commentID)
	if err != nil {
		t.Fatalf("bad comment id: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var doc struct {
		Upvotes   int      `bson:"upvotes"`
		UpvotedBy []string `bson:"upvoted_by"`
	}
	if err := db.Collection("discussions").FindOne(ctx, bson.M{"_id": oid}).Decode(&doc); err != nil {
		t.Fatalf("read comment: %v", err)
	}
	return doc.Upvotes, len(doc.UpvotedBy)
}

// TestSetUpvote_ConcurrentVotesDoNotDrift is the regression test. The
// counter used to be recomputed with a read-then-write, so two votes
// landing together both read the same intermediate voter set and the
// second wrote back a count that was already stale.
func TestSetUpvote_ConcurrentVotesDoNotDrift(t *testing.T) {
	repo, db, problemID := testRepo(t)
	comment := newComment(t, repo, problemID, "")

	const voters = 32
	var wg sync.WaitGroup
	start := make(chan struct{})
	stopWatching := make(chan struct{})
	errs := make(chan error, voters)

	for i := 0; i < voters; i++ {
		userID := fmt.Sprintf("user-%d", i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			// Toggle rather than vote once. A single burst of votes rarely
			// catches the read-then-write, because every read lands after
			// every write; sustained churn is what real traffic looks like
			// and it is what exposes the stale recompute. The final state
			// is deterministic: every voter has voted.
			for round := 0; round < 5; round++ {
				if _, err := repo.SetUpvote(ctx, comment.ID, userID, true); err != nil {
					errs <- err
					return
				}
				if _, err := repo.SetUpvote(ctx, comment.ID, userID, false); err != nil {
					errs <- err
					return
				}
			}
			if _, err := repo.SetUpvote(ctx, comment.ID, userID, true); err != nil {
				errs <- err
			}
		}()
	}
	// While the votes are in flight, keep reading the comment the way the
	// thread view does. The counter and the voter set must agree in every
	// snapshot a reader can observe — that is the invariant the code
	// claims. A read-then-write cannot hold it: between the $addToSet and
	// the recomputed $set the document is visibly inconsistent, and a
	// process that dies in that window leaves it that way for good.
	watchDone := make(chan struct{})
	mismatches := make(chan string, 1)
	go func() {
		defer close(watchDone)
		oid, err := bson.ObjectIDFromHex(comment.ID)
		if err != nil {
			return
		}
		for {
			select {
			case <-stopWatching:
				return
			default:
			}
			var doc struct {
				Upvotes   int      `bson:"upvotes"`
				UpvotedBy []string `bson:"upvoted_by"`
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := db.Collection("discussions").FindOne(ctx, bson.M{"_id": oid}).Decode(&doc)
			cancel()
			if err != nil {
				continue
			}
			if doc.Upvotes != len(doc.UpvotedBy) {
				select {
				case mismatches <- fmt.Sprintf("upvotes=%d but the voter set holds %d",
					doc.Upvotes, len(doc.UpvotedBy)):
				default:
				}
				return
			}
		}
	}()

	close(start)
	wg.Wait()
	close(errs)
	close(stopWatching)
	<-watchDone

	for err := range errs {
		t.Fatalf("SetUpvote: %v", err)
	}
	select {
	case mismatch := <-mismatches:
		t.Errorf("a reader saw the counter out of step with the voter set: %s", mismatch)
	default:
	}

	count, voterSetSize := storedUpvotes(t, db, comment.ID)
	if voterSetSize != voters {
		t.Fatalf("voter set holds %d entries, want %d", voterSetSize, voters)
	}
	if count != voters {
		t.Errorf("upvotes = %d, want %d — the counter drifted from the voter set", count, voters)
	}
}

// TestListReplies_ReadsAtMostTheLimitPerParent is the regression test
// for the unbounded query: replies were fetched with no SetLimit, so one
// thread with tens of thousands of replies was read whole into a process
// with a hard memory cap.
func TestListReplies_ReadsAtMostTheLimitPerParent(t *testing.T) {
	repo, _, problemID := testRepo(t)

	busy := newComment(t, repo, problemID, "")
	quiet := newComment(t, repo, problemID, "")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const busyReplies = 40
	for i := 0; i < busyReplies; i++ {
		reply := newComment(t, repo, problemID, busy.ID)
		_ = reply
	}
	newComment(t, repo, problemID, quiet.ID)

	const limit = 5
	replies, err := repo.ListReplies(ctx, []string{busy.ID, quiet.ID}, limit)
	if err != nil {
		t.Fatalf("list replies: %v", err)
	}

	perParent := map[string]int{}
	for _, reply := range replies {
		perParent[reply.ParentID]++
	}
	if got := perParent[busy.ID]; got != limit {
		t.Errorf("busy thread returned %d replies, want the limit of %d", got, limit)
	}
	// The busy thread must not eat the quiet one's share: the limit is per
	// parent, not per call.
	if got := perParent[quiet.ID]; got != 1 {
		t.Errorf("quiet thread returned %d replies, want 1", got)
	}
	if len(replies) > limit*2 {
		t.Errorf("read %d rows, want at most %d", len(replies), limit*2)
	}
}

func TestListReplies_NoParentsReadsNothing(t *testing.T) {
	repo, _, _ := testRepo(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	replies, err := repo.ListReplies(ctx, nil, 10)
	if err != nil {
		t.Fatalf("list replies: %v", err)
	}
	if len(replies) != 0 {
		t.Errorf("got %d replies, want none", len(replies))
	}
}

// TestSetUpvote_IsIdempotent pins the property the voter set exists for:
// voting twice the same way must not inflate the count.
func TestSetUpvote_IsIdempotent(t *testing.T) {
	repo, db, problemID := testRepo(t)
	comment := newComment(t, repo, problemID, "")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	for i := 0; i < 3; i++ {
		count, err := repo.SetUpvote(ctx, comment.ID, "voter", true)
		if err != nil {
			t.Fatalf("SetUpvote: %v", err)
		}
		if count != 1 {
			t.Fatalf("upvote %d returned count %d, want 1", i+1, count)
		}
	}

	if stored, voterSetSize := storedUpvotes(t, db, comment.ID); stored != 1 || voterSetSize != 1 {
		t.Errorf("stored upvotes=%d voters=%d, want 1 and 1", stored, voterSetSize)
	}
}

// TestSetUpvote_RemovesAVote covers the withdrawal path, including a
// withdrawal by someone who never voted.
func TestSetUpvote_RemovesAVote(t *testing.T) {
	repo, db, problemID := testRepo(t)
	comment := newComment(t, repo, problemID, "")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if _, err := repo.SetUpvote(ctx, comment.ID, "voter", true); err != nil {
		t.Fatalf("upvote: %v", err)
	}
	count, err := repo.SetUpvote(ctx, comment.ID, "voter", false)
	if err != nil {
		t.Fatalf("remove upvote: %v", err)
	}
	if count != 0 {
		t.Errorf("count after removal = %d, want 0", count)
	}

	// Withdrawing a vote that was never cast changes nothing.
	count, err = repo.SetUpvote(ctx, comment.ID, "never-voted", false)
	if err != nil {
		t.Fatalf("remove upvote: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}

	if stored, voterSetSize := storedUpvotes(t, db, comment.ID); stored != 0 || voterSetSize != 0 {
		t.Errorf("stored upvotes=%d voters=%d, want 0 and 0", stored, voterSetSize)
	}
}

func TestSetUpvote_UnknownCommentIsNotFound(t *testing.T) {
	repo, _, _ := testRepo(t)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if _, err := repo.SetUpvote(ctx, bson.NewObjectID().Hex(), "voter", true); !errors.Is(err, discussion.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	if _, err := repo.SetUpvote(ctx, "not-an-object-id", "voter", true); !errors.Is(err, discussion.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
