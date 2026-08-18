// Package mongorepo provides the production MongoDB-backed implementation
// of problem.Repository.
package mongorepo

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/toji339/online-judge/internal/problem"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// MongoRepository implements problem.Repository using MongoDB collections.
type MongoRepository struct {
	problems  *mongo.Collection
	testCases *mongo.Collection
}

// New creates a MongoRepository backed by the given database.
func New(db *mongo.Database) *MongoRepository {
	return &MongoRepository{
		problems:  db.Collection("problems"),
		testCases: db.Collection("test_cases"),
	}
}

func (r *MongoRepository) Create(ctx context.Context, p *problem.Problem) error {
	result, err := r.problems.InsertOne(ctx, p)
	if err != nil {
		return fmt.Errorf("insert problem: %w", err)
	}
	p.ID = result.InsertedID.(bson.ObjectID).Hex()
	return nil
}

func (r *MongoRepository) GetBySlug(ctx context.Context, slug string) (*problem.Problem, error) {
	var p problem.Problem
	err := r.problems.FindOne(ctx, bson.M{"slug": slug}).Decode(&p)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, problem.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find by slug: %w", err)
	}
	return &p, nil
}

func (r *MongoRepository) GetByID(ctx context.Context, id string) (*problem.Problem, error) {
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return nil, problem.ErrNotFound
	}
	var p problem.Problem
	err = r.problems.FindOne(ctx, bson.M{"_id": oid}).Decode(&p)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, problem.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find by id: %w", err)
	}
	return &p, nil
}

// GetByIDs fetches every problem whose ID appears in ids. Malformed IDs
// are skipped rather than failing the whole query.
func (r *MongoRepository) GetByIDs(ctx context.Context, ids []string) ([]problem.Problem, error) {
	oids := make([]bson.ObjectID, 0, len(ids))
	for _, id := range ids {
		if oid, err := bson.ObjectIDFromHex(id); err == nil {
			oids = append(oids, oid)
		}
	}
	if len(oids) == 0 {
		return []problem.Problem{}, nil
	}

	cursor, err := r.problems.Find(ctx, bson.M{"_id": bson.M{"$in": oids}})
	if err != nil {
		return nil, fmt.Errorf("get problems by ids: %w", err)
	}
	defer cursor.Close(ctx)

	var problems []problem.Problem
	if err := cursor.All(ctx, &problems); err != nil {
		return nil, fmt.Errorf("decode problems: %w", err)
	}
	if problems == nil {
		problems = []problem.Problem{}
	}
	return problems, nil
}

// listQuery builds the Mongo filter shared by List and Count so the two
// can never drift apart and report inconsistent page counts.
func listQuery(filter problem.ListFilter) bson.M {
	query := bson.M{}
	if filter.Difficulty != "" {
		query["difficulty"] = filter.Difficulty
	}
	if filter.Tag != "" {
		query["tags"] = filter.Tag
	}
	if filter.Company != "" {
		query["company_tags.company"] = filter.Company
	}
	if filter.Search != "" {
		// A substring regex rather than a $text index: the box is a
		// search-as-you-type field, and $text only matches whole stemmed
		// words, so "subarr" would find nothing. QuoteMeta is what makes
		// that safe — an unescaped term is both a query injection and, for
		// a pattern like "(a+)+$", a backtracking bomb. The service caps
		// the term's length before it gets here.
		query["title"] = bson.M{"$regex": regexp.QuoteMeta(filter.Search), "$options": "i"}
	}
	return query
}

// Count returns how many problems match the filter, ignoring pagination.
func (r *MongoRepository) Count(ctx context.Context, filter problem.ListFilter) (int, error) {
	n, err := r.problems.CountDocuments(ctx, listQuery(filter))
	if err != nil {
		return 0, fmt.Errorf("count problems: %w", err)
	}
	return int(n), nil
}

func (r *MongoRepository) List(ctx context.Context, filter problem.ListFilter) ([]problem.Problem, error) {
	query := listQuery(filter)

	pageSize := int64(filter.PageSize)
	if pageSize <= 0 {
		pageSize = 20
	}
	page := int64(filter.Page)
	if page <= 0 {
		page = 1
	}
	skip := (page - 1) * pageSize

	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetSkip(skip).SetLimit(pageSize)

	cursor, err := r.problems.Find(ctx, query, opts)
	if err != nil {
		return nil, fmt.Errorf("list problems: %w", err)
	}
	defer cursor.Close(ctx)

	var problems []problem.Problem
	if err := cursor.All(ctx, &problems); err != nil {
		return nil, fmt.Errorf("decode problems: %w", err)
	}
	if problems == nil {
		problems = []problem.Problem{}
	}
	return problems, nil
}

func (r *MongoRepository) Update(ctx context.Context, id string, p *problem.Problem) error {
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return problem.ErrNotFound
	}

	update := bson.M{"$set": bson.M{
		"title":           p.Title,
		"statement":       p.Statement,
		"difficulty":      p.Difficulty,
		"tags":            p.Tags,
		"time_limit_ms":   p.TimeLimitMS,
		"memory_limit_mb": p.MemoryLimitMB,
		"starter_code":    p.StarterCode,
	}}

	result, err := r.problems.UpdateOne(ctx, bson.M{"_id": oid}, update)
	if err != nil {
		return fmt.Errorf("update problem: %w", err)
	}
	if result.MatchedCount == 0 {
		return problem.ErrNotFound
	}
	return nil
}

func (r *MongoRepository) AddTestCase(ctx context.Context, tc *problem.TestCase) error {
	result, err := r.testCases.InsertOne(ctx, tc)
	if err != nil {
		return fmt.Errorf("insert test case: %w", err)
	}
	tc.ID = result.InsertedID.(bson.ObjectID).Hex()
	return nil
}

func (r *MongoRepository) ListTestCases(ctx context.Context, problemID string, sampleOnly bool) ([]problem.TestCase, error) {
	query := bson.M{"problem_id": problemID}
	if sampleOnly {
		query["is_sample"] = true
	}

	cursor, err := r.testCases.Find(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list test cases: %w", err)
	}
	defer cursor.Close(ctx)

	var tcs []problem.TestCase
	if err := cursor.All(ctx, &tcs); err != nil {
		return nil, fmt.Errorf("decode test cases: %w", err)
	}
	if tcs == nil {
		tcs = []problem.TestCase{}
	}
	return tcs, nil
}
