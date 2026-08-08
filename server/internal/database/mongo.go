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

	// 2. Connect to MongoDB using the provided URI
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := collection.Indexes().CreateMany(ctx, indexes)
	if err != nil {
		return fmt.Errorf("failed to create indexes: %w", err)
	}

	log.Println("User indexes created successfully")

	// --- Problem collection indexes ---
	problemsColl := db.Collection("problems")
	problemIndexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "slug", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
		{
			Keys: bson.D{{Key: "difficulty", Value: 1}},
		},
		{
			Keys: bson.D{{Key: "tags", Value: 1}},
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

	return nil
}
