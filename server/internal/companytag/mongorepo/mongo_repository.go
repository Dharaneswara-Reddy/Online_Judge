// Package mongorepo provides the production MongoDB-backed
// implementation of companytag.Repository.
package mongorepo

import (
	"context"
	"fmt"

	"github.com/toji339/online-judge/internal/companytag"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// MongoRepository implements companytag.Repository across two
// collections: the individual reports in "problem_company_tags" and the
// denormalised summary array on each problem document.
type MongoRepository struct {
	tags     *mongo.Collection
	problems *mongo.Collection
}

// New creates a MongoRepository backed by the given database.
func New(db *mongo.Database) *MongoRepository {
	return &MongoRepository{
		tags:     db.Collection("problem_company_tags"),
		problems: db.Collection("problems"),
	}
}

// Add inserts one report. The unique index on
// (problem_id, user_id, company) is what enforces one tag per user per
// company, so a duplicate surfaces here as a duplicate-key error.
func (r *MongoRepository) Add(ctx context.Context, tag *companytag.Tag) error {
	result, err := r.tags.InsertOne(ctx, tag)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return companytag.ErrAlreadyTagged
		}
		return fmt.Errorf("insert company tag: %w", err)
	}
	tag.ID = result.InsertedID.(bson.ObjectID).Hex()
	return nil
}

// maxSummaryAttempts bounds the increment retry loop. Each attempt only
// retries because another writer won the race to append the company, and
// once the entry exists every later attempt takes the $inc path — so one
// retry is normally enough and the loop cannot spin for long. The bound
// is there so a pathological interleaving fails loudly instead of
// looping forever.
const maxSummaryAttempts = 10

// IncrementSummary bumps the count for a company inside the problem's
// denormalised company_tags array, appending the entry when it is the
// first report for that company.
//
// Appending is guarded by a $ne filter so two concurrent first reports
// cannot create the entry twice. That guard means the append can match
// nothing — another request appended first — and the increment would be
// silently dropped, permanently under-reporting the count. So the result
// is checked and the whole thing retried: the second pass finds the
// entry and takes the $inc path.
func (r *MongoRepository) IncrementSummary(ctx context.Context, problemID, company string) error {
	oid, err := bson.ObjectIDFromHex(problemID)
	if err != nil {
		return fmt.Errorf("invalid problem id: %s", problemID)
	}

	for attempt := 0; attempt < maxSummaryAttempts; attempt++ {
		// Try to bump an existing entry first.
		result, err := r.problems.UpdateOne(ctx,
			bson.M{"_id": oid, "company_tags.company": company},
			bson.M{"$inc": bson.M{"company_tags.$.count": 1}},
		)
		if err != nil {
			return fmt.Errorf("increment company tag count: %w", err)
		}
		if result.MatchedCount > 0 {
			return nil
		}

		// Problems created before company tags existed store the field as
		// null, and $push cannot append to null. Normalise those to an empty
		// array first so the append below always has somewhere to go.
		if _, err := r.problems.UpdateOne(ctx,
			bson.M{"_id": oid, "company_tags": nil},
			bson.M{"$set": bson.M{"company_tags": []any{}}},
		); err != nil {
			return fmt.Errorf("initialise company tag summary: %w", err)
		}

		// No entry yet — append one. The filter guards against a concurrent
		// insert having just added it.
		appended, err := r.problems.UpdateOne(ctx,
			bson.M{"_id": oid, "company_tags.company": bson.M{"$ne": company}},
			bson.M{"$push": bson.M{"company_tags": bson.M{"company": company, "count": 1}}},
		)
		if err != nil {
			return fmt.Errorf("add company tag summary: %w", err)
		}
		if appended.MatchedCount > 0 {
			return nil
		}

		// Matched nothing: either the problem is gone, or a concurrent
		// request appended the entry between the two statements. Tell those
		// apart, because only the second is worth retrying — otherwise a
		// deleted problem would spin through every attempt.
		exists, err := r.problems.CountDocuments(ctx, bson.M{"_id": oid})
		if err != nil {
			return fmt.Errorf("check problem exists: %w", err)
		}
		if exists == 0 {
			return fmt.Errorf("problem %s not found", problemID)
		}
	}

	return fmt.Errorf("increment company tag count for %q: gave up after %d attempts",
		company, maxSummaryAttempts)
}

func (r *MongoRepository) ListForProblem(ctx context.Context, problemID string) ([]companytag.CompanyCount, error) {
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"problem_id": problemID}}},
		{{Key: "$group", Value: bson.M{
			"_id":       "$company",
			"tag_count": bson.M{"$sum": 1},
		}}},
		// Scoped to one problem, so the problem count is trivially one.
		// Setting it keeps the shape identical to the explorer's rows.
		{{Key: "$addFields", Value: bson.M{"problem_count": 1}}},
		{{Key: "$sort", Value: bson.D{{Key: "tag_count", Value: -1}, {Key: "_id", Value: 1}}}},
	}
	return r.aggregate(ctx, pipeline)
}

func (r *MongoRepository) ListCompanies(ctx context.Context, limit int) ([]companytag.CompanyCount, error) {
	if limit <= 0 {
		limit = 100
	}
	pipeline := mongo.Pipeline{
		{{Key: "$group", Value: bson.M{
			"_id":       "$company",
			"tag_count": bson.M{"$sum": 1},
			// Distinct problems, so a company tagged 50 times across 3
			// problems reports 3, not 50.
			"problems": bson.M{"$addToSet": "$problem_id"},
		}}},
		{{Key: "$project", Value: bson.M{
			"tag_count":     1,
			"problem_count": bson.M{"$size": "$problems"},
		}}},
		{{Key: "$sort", Value: bson.D{{Key: "tag_count", Value: -1}, {Key: "_id", Value: 1}}}},
		{{Key: "$limit", Value: int64(limit)}},
	}
	return r.aggregate(ctx, pipeline)
}

func (r *MongoRepository) aggregate(ctx context.Context, pipeline mongo.Pipeline) ([]companytag.CompanyCount, error) {
	cursor, err := r.tags.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, fmt.Errorf("aggregate company tags: %w", err)
	}
	defer cursor.Close(ctx)

	var counts []companytag.CompanyCount
	if err := cursor.All(ctx, &counts); err != nil {
		return nil, fmt.Errorf("decode company tags: %w", err)
	}
	if counts == nil {
		counts = []companytag.CompanyCount{}
	}
	return counts, nil
}

func (r *MongoRepository) ListUserTags(ctx context.Context, problemID, userID string) ([]companytag.Tag, error) {
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}})

	cursor, err := r.tags.Find(ctx, bson.M{"problem_id": problemID, "user_id": userID}, opts)
	if err != nil {
		return nil, fmt.Errorf("list user company tags: %w", err)
	}
	defer cursor.Close(ctx)

	var tags []companytag.Tag
	if err := cursor.All(ctx, &tags); err != nil {
		return nil, fmt.Errorf("decode user company tags: %w", err)
	}
	if tags == nil {
		tags = []companytag.Tag{}
	}
	return tags, nil
}

// ProblemsForCompany returns the IDs of every problem tagged with a
// company, which the company explorer joins against the problems.
func (r *MongoRepository) ProblemsForCompany(ctx context.Context, company string) ([]string, error) {
	var ids []string
	err := r.tags.Distinct(ctx, "problem_id", bson.M{"company": company}).Decode(&ids)
	if err != nil {
		return nil, fmt.Errorf("distinct problems for company: %w", err)
	}
	if ids == nil {
		ids = []string{}
	}
	return ids, nil
}
