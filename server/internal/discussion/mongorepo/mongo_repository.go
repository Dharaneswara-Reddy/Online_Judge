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

func (r *MongoRepository) ListForProblem(ctx context.Context, problemID string) ([]discussion.Comment, error) {
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}})

	cursor, err := r.comments.Find(ctx, bson.M{"problem_id": problemID}, opts)
	if err != nil {
		return nil, fmt.Errorf("list comments: %w", err)
	}
	defer cursor.Close(ctx)

	var comments []discussion.Comment
	if err := cursor.All(ctx, &comments); err != nil {
		return nil, fmt.Errorf("decode comments: %w", err)
	}
	if comments == nil {
		comments = []discussion.Comment{}
	}
	return comments, nil
}

// SetUpvote adds or removes a voter using $addToSet / $pull, which are
// idempotent by construction — this is what stops vote stuffing without
// needing a read-then-write. The counter is then recomputed from the
// voter set so the two can never drift apart.
func (r *MongoRepository) SetUpvote(ctx context.Context, commentID, userID string, up bool) (int, error) {
	oid, err := bson.ObjectIDFromHex(commentID)
	if err != nil {
		return 0, discussion.ErrNotFound
	}

	var update bson.M
	if up {
		update = bson.M{"$addToSet": bson.M{"upvoted_by": userID}}
	} else {
		update = bson.M{"$pull": bson.M{"upvoted_by": userID}}
	}

	result, err := r.comments.UpdateOne(ctx, bson.M{"_id": oid}, update)
	if err != nil {
		return 0, fmt.Errorf("set upvote: %w", err)
	}
	if result.MatchedCount == 0 {
		return 0, discussion.ErrNotFound
	}

	// Recompute the denormalised count from the authoritative voter set.
	comment, err := r.GetByID(ctx, commentID)
	if err != nil {
		return 0, err
	}
	count := len(comment.UpvotedBy)
	if _, err := r.comments.UpdateOne(ctx, bson.M{"_id": oid}, bson.M{"$set": bson.M{"upvotes": count}}); err != nil {
		return 0, fmt.Errorf("update upvote count: %w", err)
	}
	return count, nil
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
