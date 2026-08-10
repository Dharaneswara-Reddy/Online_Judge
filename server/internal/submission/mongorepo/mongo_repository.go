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

func (r *MongoRepository) UpdateStatus(ctx context.Context, id string, status submission.Status, result *submission.Result) error {
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return submission.ErrNotFound
	}

	set := bson.M{"status": status}
	if result != nil {
		set["runtime_ms"] = result.RuntimeMS
		set["memory_kb"] = result.MemoryKB
		set["failed_case"] = result.FailedCase
		set["compile_error"] = result.CompileError
	}
	// judged_at is stamped server-side, never taken from a client.
	if status.IsTerminal() {
		set["judged_at"] = time.Now().UTC()
	}

	res, err := r.submissions.UpdateOne(ctx, bson.M{"_id": oid}, bson.M{"$set": set})
	if err != nil {
		return fmt.Errorf("update submission status: %w", err)
	}
	if res.MatchedCount == 0 {
		return submission.ErrNotFound
	}
	return nil
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
