// Package mongorepo provides the production MongoDB-backed
// implementation of discussion.Repository.
package mongorepo

import (
	"context"
	"errors"
	"fmt"

	"github.com/toji339/online-judge/internal/discussion"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// MongoRepository implements discussion.Repository on the "discussions"
// collection.
type MongoRepository struct {
	comments *mongo.Collection
}

// New creates a MongoRepository backed by the given database.
func New(db *mongo.Database) *MongoRepository {
	return &MongoRepository{comments: db.Collection("discussions")}
}

func (r *MongoRepository) Create(ctx context.Context, c *discussion.Comment) error {
	result, err := r.comments.InsertOne(ctx, c)
	if err != nil {
		return fmt.Errorf("insert comment: %w", err)
	}
	c.ID = result.InsertedID.(bson.ObjectID).Hex()
	return nil
}

func (r *MongoRepository) GetByID(ctx context.Context, id string) (*discussion.Comment, error) {
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return nil, discussion.ErrNotFound
	}

	var c discussion.Comment
	err = r.comments.FindOne(ctx, bson.M{"_id": oid}).Decode(&c)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, discussion.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find comment: %w", err)
	}
	return &c, nil
}

// rootFilter matches top-level comments. ParentID is stored with
// omitempty, so a root has no parent_id field at all — older documents
// may carry an empty string instead, and both must match.
func rootFilter(problemID string) bson.M {
	return bson.M{
		"problem_id": problemID,
		"$or": []bson.M{
			{"parent_id": bson.M{"$exists": false}},
			{"parent_id": ""},
		},
	}
}

// ListRoots returns one page of top-level comments, newest first.
//
// Ordering is (created_at, _id) descending. The id is part of the sort,
// not decoration: timestamps collide, and without a unique tie-break the
// boundary between pages is ambiguous, which shows up as a comment
// appearing twice or not at all.
func (r *MongoRepository) ListRoots(ctx context.Context, problemID string, after *discussion.Cursor, limit int) ([]discussion.Comment, error) {
	filter := rootFilter(problemID)

	if after != nil {
		oid, err := bson.ObjectIDFromHex(after.ID)
		if err != nil {
			return nil, discussion.ErrInvalidCursor
		}
		// Strictly past the cursor in the sort order.
		filter["$and"] = []bson.M{{
			"$or": []bson.M{
				{"created_at": bson.M{"$lt": after.CreatedAt}},
				{"created_at": after.CreatedAt, "_id": bson.M{"$lt": oid}},
			},
		}}
	}

	opts := options.Find().
		SetSort(bson.D{{Key: "created_at", Value: -1}, {Key: "_id", Value: -1}}).
		SetLimit(int64(limit))

	cursor, err := r.comments.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("list root comments: %w", err)
	}
	defer cursor.Close(ctx)

	var comments []discussion.Comment
	if err := cursor.All(ctx, &comments); err != nil {
		return nil, fmt.Errorf("decode root comments: %w", err)
	}
	if comments == nil {
		comments = []discussion.Comment{}
	}
	return comments, nil
}

// ListReplies fetches the oldest replies of each parent on one page,
// at most limitPerParent per parent.
//
// It issues one bounded query per parent rather than a single $in over
// all of them. A single query can only carry a limit for the whole call,
// and a whole-call limit sorted oldest-first is spent entirely on the
// busiest thread, leaving the other comments on the page with no replies
// at all. One query per parent keeps the cap fair and each query is an
// indexed lookup on parent_id, so the cost is a page-sized handful of
// small reads. The alternative — grouping in an aggregation — builds the
// whole reply array server-side before slicing it, which moves the
// unbounded read into the database instead of removing it.
func (r *MongoRepository) ListReplies(ctx context.Context, parentIDs []string, limitPerParent int) ([]discussion.Comment, error) {
	if len(parentIDs) == 0 || limitPerParent <= 0 {
		return []discussion.Comment{}, nil
	}

	opts := options.Find().
		SetSort(bson.D{{Key: "created_at", Value: 1}, {Key: "_id", Value: 1}}).
		SetLimit(int64(limitPerParent))

	replies := make([]discussion.Comment, 0, len(parentIDs)*limitPerParent)
	for _, parentID := range parentIDs {
		cursor, err := r.comments.Find(ctx, bson.M{"parent_id": parentID}, opts)
		if err != nil {
			return nil, fmt.Errorf("list replies: %w", err)
		}

		var batch []discussion.Comment
		if err := cursor.All(ctx, &batch); err != nil {
			cursor.Close(ctx)
			return nil, fmt.Errorf("decode replies: %w", err)
		}
		cursor.Close(ctx)
		replies = append(replies, batch...)
	}
	return replies, nil
}

// SetUpvote adds or removes a voter and returns the resulting count.
//
// Voting is idempotent — adding a voter already in the set changes
// nothing — which is what stops vote stuffing without a read-then-write.
//
// The voter set and the denormalised counter are updated by a single
// aggregation-pipeline update, so every reader sees them agree. They
// used to be two statements with the count recomputed in Go between
// them, and that could not hold: concurrent votes both read the same
// intermediate set and the later write put back a count that was already
// stale, so the counter drifted below the voter set. A process dying
// between the two statements left the drift permanently.
func (r *MongoRepository) SetUpvote(ctx context.Context, commentID, userID string, up bool) (int, error) {
	oid, err := bson.ObjectIDFromHex(commentID)
	if err != nil {
		return 0, discussion.ErrNotFound
	}

	// Older comments have no upvoted_by field at all; treat that as empty.
	voters := bson.M{"$ifNull": bson.A{"$upvoted_by", bson.A{}}}

	var nextVoters bson.M
	if up {
		// Append only when absent, which keeps the set unique while
		// preserving the order votes arrived in.
		nextVoters = bson.M{"$cond": bson.A{
			bson.M{"$in": bson.A{userID, voters}},
			voters,
			bson.M{"$concatArrays": bson.A{voters, bson.A{userID}}},
		}}
	} else {
		nextVoters = bson.M{"$filter": bson.M{
			"input": voters,
			"cond":  bson.M{"$ne": bson.A{"$$this", userID}},
		}}
	}

	// Two stages, one atomic update: the second stage counts what the
	// first stage just wrote, so the counter is derived from the voter
	// set inside the same operation.
	update := mongo.Pipeline{
		{{Key: "$set", Value: bson.M{"upvoted_by": nextVoters}}},
		{{Key: "$set", Value: bson.M{"upvotes": bson.M{"$size": "$upvoted_by"}}}},
	}

	opts := options.FindOneAndUpdate().
		SetReturnDocument(options.After).
		SetProjection(bson.M{"upvotes": 1})

	var updated struct {
		Upvotes int `bson:"upvotes"`
	}
	err = r.comments.FindOneAndUpdate(ctx, bson.M{"_id": oid}, update, opts).Decode(&updated)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return 0, discussion.ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("set upvote: %w", err)
	}
	return updated.Upvotes, nil
}

func (r *MongoRepository) SoftDelete(ctx context.Context, commentID string) error {
	oid, err := bson.ObjectIDFromHex(commentID)
	if err != nil {
		return discussion.ErrNotFound
	}

	result, err := r.comments.UpdateOne(ctx,
		bson.M{"_id": oid},
		bson.M{"$set": bson.M{"deleted": true, "content": ""}},
	)
	if err != nil {
		return fmt.Errorf("delete comment: %w", err)
	}
	if result.MatchedCount == 0 {
		return discussion.ErrNotFound
	}
	return nil
}
