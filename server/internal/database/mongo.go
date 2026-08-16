// Package database handles connecting to MongoDB Atlas,
// graceful disconnection, and index creation. All database
// access in the application flows through the client and
// database references established here.
package database

import (
	"context"
	"fmt"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Connect establishes a connection to MongoDB Atlas using
// the provided URI. It pings the server to verify the
// connection is alive before returning the client.
func Connect(uri string) (*mongo.Client, error) {
	// Steps to follow while connecting to MongoDB
	// =============================================

	// 1. Create a context with a 10-second timeout for the connection attempt
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 2. Connect to MongoDB using the provided URI.
	//
	//    ObjectIDAsHexString lets documents whose _id is an ObjectID
	//    decode into a plain Go string field. Domain types such as
	//    problem.Problem and submission.Submission model their ID as a
	//    string so the domain packages stay free of driver types; without
	//    this option every read of those collections fails to decode.
	clientOpts := options.Client().
		ApplyURI(uri).
		SetBSONOptions(&options.BSONOptions{ObjectIDAsHexString: true})

	client, err := mongo.Connect(clientOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to MongoDB: %w", err)
	}

	// 3. Ping the database to confirm the connection is alive
	err = client.Ping(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to ping MongoDB: %w", err)
	}

	log.Println("Connected to MongoDB Atlas successfully")
	return client, nil
}

// Disconnect gracefully closes the MongoDB connection.
// This should be called when the server is shutting down
// to release resources cleanly.
func Disconnect(client *mongo.Client) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Disconnect(ctx); err != nil {
		log.Printf("Error disconnecting from MongoDB: %v", err)
	} else {
		log.Println("Disconnected from MongoDB")
	}
}

// EnsureIndexes creates the required unique indexes on the
// users collection. These indexes enforce that no two users
// can share the same username or email address.
//
// Indexes are created idempotently — calling this function
// multiple times is safe. If the indexes already exist,
// MongoDB simply returns them without error.
func EnsureIndexes(db *mongo.Database) error {
	// Steps to follow while creating indexes
	// ========================================

	// 1. Get a reference to the users collection
	collection := db.Collection("users")

	// 2. Define the unique indexes we need:
	//    - username must be unique across all users
	//    - email must be unique across all users
	indexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "username", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
		{
			Keys:    bson.D{{Key: "email", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
	}

	// 3. Create the indexes on the collection
	// Index builds on a collection with millions of documents take
	// far longer than a request would; a short budget here means the
	// API refuses to start rather than waiting.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	_, err := collection.Indexes().CreateMany(ctx, indexes)
	if err != nil {
		return fmt.Errorf("failed to create indexes: %w", err)
	}

	log.Println("User indexes created successfully")

	// --- Problem collection indexes ---
	problemsColl := db.Collection("problems")
	// Every listing sorts by created_at descending, so each filter needs
	// that as the trailing key — otherwise Mongo has to fetch the matches
	// and sort them in memory, which fails outright past 32MB.
	problemIndexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "slug", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
		{
			Keys: bson.D{{Key: "created_at", Value: -1}},
		},
		{
			Keys: bson.D{{Key: "difficulty", Value: 1}, {Key: "created_at", Value: -1}},
		},
		{
			Keys: bson.D{{Key: "tags", Value: 1}, {Key: "created_at", Value: -1}},
		},
		{
			Keys: bson.D{{Key: "company_tags.company", Value: 1}, {Key: "created_at", Value: -1}},
		},
	}
	_, err = problemsColl.Indexes().CreateMany(ctx, problemIndexes)
	if err != nil {
		return fmt.Errorf("failed to create problem indexes: %w", err)
	}
	log.Println("Problem indexes created successfully")

	// --- Test case collection indexes ---
	testCasesColl := db.Collection("test_cases")
	testCaseIndexes := []mongo.IndexModel{
		{
			Keys: bson.D{
				{Key: "problem_id", Value: 1},
				{Key: "is_sample", Value: 1},
			},
		},
	}
	_, err = testCasesColl.Indexes().CreateMany(ctx, testCaseIndexes)
	if err != nil {
		return fmt.Errorf("failed to create test case indexes: %w", err)
	}
	log.Println("Test case indexes created successfully")

	// --- Submission collection indexes ---
	// The compound user/time index backs the submission history page,
	// while the room index backs War Room winner lookups.
	submissionsColl := db.Collection("submissions")
	submissionIndexes := []mongo.IndexModel{
		{
			Keys: bson.D{
				{Key: "user_id", Value: 1},
				{Key: "submitted_at", Value: -1},
			},
		},
		{
			Keys: bson.D{
				{Key: "problem_id", Value: 1},
				{Key: "status", Value: 1},
			},
		},
		{
			Keys: bson.D{
				{Key: "war_room_id", Value: 1},
				{Key: "judged_at", Value: 1},
			},
		},
		// The landing page counts accepted submissions; status is not a
		// prefix of any index above, so without this it is a collection
		// scan on the largest collection in the system.
		{
			Keys: bson.D{{Key: "status", Value: 1}},
		},
		// Covers both the profile's solved-problem set and its accepted
		// count without touching a document.
		{
			Keys: bson.D{
				{Key: "user_id", Value: 1},
				{Key: "status", Value: 1},
				{Key: "problem_id", Value: 1},
			},
		},
	}
	_, err = submissionsColl.Indexes().CreateMany(ctx, submissionIndexes)
	if err != nil {
		return fmt.Errorf("failed to create submission indexes: %w", err)
	}
	log.Println("Submission indexes created successfully")

	// --- War room collection indexes ---
	// The room code is the shareable join key, so it must be unique.
	warRoomsColl := db.Collection("war_rooms")
	warRoomIndexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "room_code", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
		{
			Keys: bson.D{
				{Key: "status", Value: 1},
				{Key: "created_at", Value: -1},
			},
		},
		{
			Keys: bson.D{{Key: "participants.user_id", Value: 1}, {Key: "created_at", Value: -1}},
		},
		// The second branch of the stale-room sweep filters on started_at.
		{
			Keys: bson.D{{Key: "status", Value: 1}, {Key: "started_at", Value: 1}},
		},
	}
	_, err = warRoomsColl.Indexes().CreateMany(ctx, warRoomIndexes)
	if err != nil {
		return fmt.Errorf("failed to create war room indexes: %w", err)
	}
	log.Println("War room indexes created successfully")

	// --- Discussion collection indexes ---
	discussionsColl := db.Collection("discussions")
	discussionIndexes := []mongo.IndexModel{
		{
			Keys: bson.D{
				{Key: "problem_id", Value: 1},
				{Key: "created_at", Value: 1},
			},
		},
		{
			Keys: bson.D{{Key: "parent_id", Value: 1}},
		},
	}
	_, err = discussionsColl.Indexes().CreateMany(ctx, discussionIndexes)
	if err != nil {
		return fmt.Errorf("failed to create discussion indexes: %w", err)
	}
	log.Println("Discussion indexes created successfully")

	// --- Company tag collection indexes ---
	// The unique compound index is the constraint that stops one user
	// from inflating a single company's tag count on a problem.
	companyTagsColl := db.Collection("problem_company_tags")
	companyTagIndexes := []mongo.IndexModel{
		{
			Keys: bson.D{
				{Key: "problem_id", Value: 1},
				{Key: "user_id", Value: 1},
				{Key: "company", Value: 1},
			},
			Options: options.Index().SetUnique(true),
		},
		{
			Keys: bson.D{{Key: "company", Value: 1}},
		},
	}
	_, err = companyTagsColl.Indexes().CreateMany(ctx, companyTagIndexes)
	if err != nil {
		return fmt.Errorf("failed to create company tag indexes: %w", err)
	}
	log.Println("Company tag indexes created successfully")

	return nil
}
