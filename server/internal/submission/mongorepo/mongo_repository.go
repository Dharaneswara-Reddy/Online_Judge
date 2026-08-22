// Package mongorepo provides the production MongoDB-backed implementation
// of submission.Repository.
package mongorepo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/toji339/online-judge/internal/submission"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// MongoRepository implements submission.Repository on the "submissions"
// collection.
type MongoRepository struct {
	submissions *mongo.Collection
}

// New creates a MongoRepository backed by the given database.
func New(db *mongo.Database) *MongoRepository {
	return &MongoRepository{submissions: db.Collection("submissions")}
}

func (r *MongoRepository) Create(ctx context.Context, s *submission.Submission) error {
	result, err := r.submissions.InsertOne(ctx, s)
	if err != nil {
		// The unique partial index on non-terminal submissions is what
		// actually enforces admission control. A duplicate key here means
		// this user already has work in flight — including when two
		// requests raced and both passed the pre-check.
		if mongo.IsDuplicateKeyError(err) {
			return submission.ErrTooManyPending
		}
		return fmt.Errorf("insert submission: %w", err)
	}
	s.ID = result.InsertedID.(bson.ObjectID).Hex()
	return nil
}

func (r *MongoRepository) GetByID(ctx context.Context, id string) (*submission.Submission, error) {
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return nil, submission.ErrNotFound
	}
	var s submission.Submission
	err = r.submissions.FindOne(ctx, bson.M{"_id": oid}).Decode(&s)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, submission.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find submission: %w", err)
	}
	return &s, nil
}

// query translates a ListFilter into a Mongo filter document.
func query(f submission.ListFilter) bson.M {
	q := bson.M{}
	if f.UserID != "" {
		q["user_id"] = f.UserID
	}
	if f.ProblemID != "" {
		q["problem_id"] = f.ProblemID
	}
	if f.Status != "" {
		q["status"] = f.Status
	}
	return q
}

func (r *MongoRepository) List(ctx context.Context, f submission.ListFilter) ([]submission.Submission, error) {
	pageSize := int64(f.PageSize)
	if pageSize <= 0 {
		pageSize = 20
	}
	page := int64(f.Page)
	if page <= 0 {
		page = 1
	}

	opts := options.Find().
		SetSort(bson.D{{Key: "submitted_at", Value: -1}}).
		SetSkip((page - 1) * pageSize).
		SetLimit(pageSize)

	cursor, err := r.submissions.Find(ctx, query(f), opts)
	if err != nil {
		return nil, fmt.Errorf("list submissions: %w", err)
	}
	defer cursor.Close(ctx)

	var out []submission.Submission
	if err := cursor.All(ctx, &out); err != nil {
		return nil, fmt.Errorf("decode submissions: %w", err)
	}
	if out == nil {
		out = []submission.Submission{}
	}
	return out, nil
}

func (r *MongoRepository) Count(ctx context.Context, f submission.ListFilter) (int, error) {
	n, err := r.submissions.CountDocuments(ctx, query(f))
	if err != nil {
		return 0, fmt.Errorf("count submissions: %w", err)
	}
	return int(n), nil
}

// nonTerminal is the set of states a submission can be in while the
// judge still owns it. It mirrors Status.IsTerminal and is what the
// admission-control partial index is built on.
var nonTerminal = []submission.Status{submission.StatusPending, submission.StatusRunning}

// ClaimForJudging takes ownership of a submission in a single conditional
// update.
//
// The preconditions live in the filter, not in a preceding read: two
// workers handed the same message both see "pending" if they look first,
// and both then judge it. Expressing the transition as
// pending -> running (or a stale running -> running) means the database
// settles the race and exactly one worker proceeds.
func (r *MongoRepository) ClaimForJudging(ctx context.Context, id string, staleBefore time.Time) error {
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return submission.ErrNotFound
	}

	filter := bson.M{"_id": oid, "$or": []bson.M{
		{"status": submission.StatusPending},
		// A running claim may only be taken over once it is old enough
		// that the worker holding it cannot still be alive.
		{"status": submission.StatusRunning, "started_at": bson.M{"$lt": staleBefore}},
	}}
	update := bson.M{"$set": bson.M{
		"status":     submission.StatusRunning,
		"started_at": time.Now().UTC(),
	}}

	res, err := r.submissions.UpdateOne(ctx, filter, update)
	if err != nil {
		return fmt.Errorf("claim submission for judging: %w", err)
	}
	if res.MatchedCount == 0 {
		return r.explainMiss(ctx, oid, submission.ErrAlreadyClaimed)
	}
	return nil
}

// CompleteJudging writes the verdict only while the submission is still
// running, so a second worker that finished the same job discards its
// result instead of flipping the verdict and restamping judged_at.
func (r *MongoRepository) CompleteJudging(ctx context.Context, id string, result submission.Result) error {
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return submission.ErrNotFound
	}

	res, err := r.submissions.UpdateOne(ctx,
		bson.M{"_id": oid, "status": submission.StatusRunning},
		bson.M{"$set": verdictFields(result.Status, result)},
	)
	if err != nil {
		return fmt.Errorf("record verdict: %w", err)
	}
	if res.MatchedCount == 0 {
		return r.explainMiss(ctx, oid, submission.ErrAlreadyJudged)
	}
	return nil
}

// FailNonTerminal records an infrastructure failure, but never over a
// verdict that has already been decided.
func (r *MongoRepository) FailNonTerminal(ctx context.Context, id string, status submission.Status, reason string) error {
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return submission.ErrNotFound
	}

	res, err := r.submissions.UpdateOne(ctx,
		bson.M{"_id": oid, "status": bson.M{"$in": nonTerminal}},
		bson.M{"$set": verdictFields(status, submission.Result{
			Status:       status,
			FailedCase:   -1,
			CompileError: reason,
		})},
	)
	if err != nil {
		return fmt.Errorf("fail submission: %w", err)
	}
	if res.MatchedCount == 0 {
		return r.explainMiss(ctx, oid, submission.ErrAlreadyJudged)
	}
	return nil
}

// ExpireStale reclaims submissions no worker will ever finish.
//
// It is one UpdateMany rather than a read-then-write loop, so several
// workers running the reaper on the same schedule cannot each reclaim the
// same rows: whichever runs first moves them out of the filter.
func (r *MongoRepository) ExpireStale(ctx context.Context, pendingBefore, runningBefore time.Time) (int, error) {
	filter := bson.M{"$or": []bson.M{
		{"status": submission.StatusPending, "submitted_at": bson.M{"$lt": pendingBefore}},
		{"status": submission.StatusRunning, "started_at": bson.M{"$lt": runningBefore}},
		// A running submission with no started_at predates this field.
		// Age it by submitted_at so it can still be reclaimed.
		{"status": submission.StatusRunning, "started_at": nil, "submitted_at": bson.M{"$lt": runningBefore}},
	}}

	res, err := r.submissions.UpdateMany(ctx, filter, bson.M{"$set": verdictFields(
		submission.StatusJudgeError,
		submission.Result{
			Status:       submission.StatusJudgeError,
			FailedCase:   -1,
			CompileError: submission.StaleReason,
		},
	)})
	if err != nil {
		return 0, fmt.Errorf("expire stale submissions: %w", err)
	}
	return int(res.ModifiedCount), nil
}

// verdictFields builds the $set document for a terminal transition.
// judged_at is stamped here, server-side, and never taken from a client.
func verdictFields(status submission.Status, result submission.Result) bson.M {
	set := bson.M{
		"status":        status,
		"runtime_ms":    result.RuntimeMS,
		"memory_kb":     result.MemoryKB,
		"failed_case":   result.FailedCase,
		"total_cases":   result.TotalCases,
		"compile_error": result.CompileError,
	}
	if status.IsTerminal() {
		set["judged_at"] = time.Now().UTC()
	}
	return set
}

// explainMiss turns a MatchedCount of zero into the right error: the
// submission either does not exist, or it does and its state no longer
// permits the transition.
func (r *MongoRepository) explainMiss(ctx context.Context, oid bson.ObjectID, conflict error) error {
	err := r.submissions.FindOne(ctx, bson.M{"_id": oid}, options.FindOne().
		SetProjection(bson.M{"_id": 1})).Err()
	if errors.Is(err, mongo.ErrNoDocuments) {
		return submission.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("inspect submission after conditional write: %w", err)
	}
	return conflict
}

func (r *MongoRepository) CountPending(ctx context.Context, userID string) (int, error) {
	n, err := r.submissions.CountDocuments(ctx, bson.M{
		"user_id": userID,
		"status":  bson.M{"$in": []submission.Status{submission.StatusPending, submission.StatusRunning}},
	})
	if err != nil {
		return 0, fmt.Errorf("count pending submissions: %w", err)
	}
	return int(n), nil
}

func (r *MongoRepository) SolvedProblemIDs(ctx context.Context, userID string) ([]string, error) {
	var ids []string
	err := r.submissions.Distinct(ctx, "problem_id", bson.M{
		"user_id": userID,
		"status":  submission.StatusAccepted,
	}).Decode(&ids)
	if err != nil {
		return nil, fmt.Errorf("distinct solved problems: %w", err)
	}
	if ids == nil {
		ids = []string{}
	}
	return ids, nil
}

func (r *MongoRepository) CountAccepted(ctx context.Context, userID string) (int, error) {
	n, err := r.submissions.CountDocuments(ctx, bson.M{
		"user_id": userID,
		"status":  submission.StatusAccepted,
	})
	if err != nil {
		return 0, fmt.Errorf("count accepted submissions: %w", err)
	}
	return int(n), nil
}

func (r *MongoRepository) FirstAcceptedInRoom(ctx context.Context, warRoomID string) (*submission.Submission, error) {
	opts := options.FindOne().SetSort(bson.D{{Key: "judged_at", Value: 1}})

	var s submission.Submission
	err := r.submissions.FindOne(ctx, bson.M{
		"war_room_id": warRoomID,
		"status":      submission.StatusAccepted,
	}, opts).Decode(&s)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, submission.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find room winner: %w", err)
	}
	return &s, nil
}
