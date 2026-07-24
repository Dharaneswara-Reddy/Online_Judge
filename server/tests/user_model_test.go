// Package tests contains all test files for the Online Judge backend.
// Tests follow the TDD approach — they are written BEFORE the
// implementation code. Each test file targets a specific layer
// (model, controller) and uses a dedicated test database on
// MongoDB Atlas that is cleaned between test runs.
package tests

import (
	"context"
	"log"
	"os"
	"testing"
	"time"

	"github.com/joho/godotenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/toji339/online-judge/internal/database"
	"github.com/toji339/online-judge/internal/models"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// testDB holds the MongoDB database reference used across all
// model tests. It is set up once in TestMain and torn down after
// all tests complete.
var testDB *mongo.Database
var testClient *mongo.Client

// TestMain is the entry point for all tests in this package.
// It connects to MongoDB Atlas using the MONGO_URI from .env,
// sets up a dedicated test database, and cleans up afterward.
func TestMain(m *testing.M) {
	// Steps to set up the test environment
	// ======================================

	// 1. Load .env from the server directory
	if err := godotenv.Load("../.env"); err != nil {
		log.Println("Warning: Could not load .env file for tests, using system env vars")
	}

	// 2. Connect to MongoDB Atlas
	uri := os.Getenv("MONGO_URI")
	if uri == "" {
		log.Fatal("FATAL: MONGO_URI is not set — cannot run tests without a database")
	}

	var err error
	testClient, err = database.Connect(uri)
	if err != nil {
		log.Fatalf("FATAL: Could not connect to MongoDB for tests: %v", err)
	}

	// 3. Use a separate test database so we never touch production data
	testDB = testClient.Database("online_judge_test")

	// 4. Ensure indexes exist on the test database
	if err := database.EnsureIndexes(testDB); err != nil {
		log.Fatalf("FATAL: Could not create indexes on test database: %v", err)
	}

	// 5. Run all the tests
	code := m.Run()

	// 6. Clean up — drop the test database and disconnect
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = testDB.Drop(ctx)
	database.Disconnect(testClient)

	os.Exit(code)
}

// cleanUsersCollection drops all documents from the users
// collection. Called before each test to ensure a clean slate.
func cleanUsersCollection(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := testDB.Collection("users").DeleteMany(ctx, bson.M{})
	require.NoError(t, err, "Failed to clean users collection")
}

// =============================================================
// Password hashing tests
// =============================================================

// TestHashPassword_Success verifies that HashPassword returns
// a non-empty hash that is different from the original password.
func TestHashPassword_Success(t *testing.T) {
	password := "securePassword123"

	hash, err := models.HashPassword(password)

	assert.NoError(t, err)
	assert.NotEmpty(t, hash)
	assert.NotEqual(t, password, hash, "Hash should not equal the plaintext password")
}

// TestCheckPassword_CorrectPassword verifies that CheckPassword
// returns true when the hash matches the plaintext password.
func TestCheckPassword_CorrectPassword(t *testing.T) {
	password := "securePassword123"
	hash, _ := models.HashPassword(password)

	result := models.CheckPassword(hash, password)

	assert.True(t, result, "CheckPassword should return true for the correct password")
}

// TestCheckPassword_WrongPassword verifies that CheckPassword
// returns false when the password does not match the hash.
func TestCheckPassword_WrongPassword(t *testing.T) {
	password := "securePassword123"
	hash, _ := models.HashPassword(password)

	result := models.CheckPassword(hash, "wrongPassword")

	assert.False(t, result, "CheckPassword should return false for an incorrect password")
}

// =============================================================
// CreateUser tests
// =============================================================

// TestCreateUser_Success verifies that a user is successfully
// created with the correct fields and a hashed password.
func TestCreateUser_Success(t *testing.T) {
	cleanUsersCollection(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	input := &models.RegisterInput{
		FullName: "John Doe",
		Username: "johndoe",
		Email:    "john@example.com",
		Password: "password123",
	}

	user, err := models.CreateUser(ctx, testDB, input)

	// The user should be created without errors
	require.NoError(t, err)
	assert.NotNil(t, user)

	// Check that all fields are set correctly
	assert.Equal(t, "johndoe", user.Username)
	assert.Equal(t, "john@example.com", user.Email)
	assert.Equal(t, "John Doe", user.FullName)
	assert.Equal(t, "user", user.Role, "Default role should be 'user'")
	assert.False(t, user.ID.IsZero(), "User should have a non-zero ID")
	assert.False(t, user.CreatedAt.IsZero(), "CreatedAt should be set")

	// Password hash should be set and should NOT be the plaintext password
	assert.NotEmpty(t, user.PasswordHash)
	assert.NotEqual(t, "password123", user.PasswordHash)

	// The hash should validate against the original password
	assert.True(t, models.CheckPassword(user.PasswordHash, "password123"))
}

// TestCreateUser_WithDOB verifies that a user can be created
// with an optional date of birth field.
func TestCreateUser_WithDOB(t *testing.T) {
	cleanUsersCollection(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	input := &models.RegisterInput{
		FullName: "Jane Doe",
		Username: "janedoe",
		Email:    "jane@example.com",
		Password: "password123",
		DOB:      "2000-05-15",
	}

	user, err := models.CreateUser(ctx, testDB, input)

	require.NoError(t, err)
	assert.NotNil(t, user.DOB, "DOB should be set when provided")
	assert.Equal(t, 2000, user.DOB.Year())
	assert.Equal(t, time.May, user.DOB.Month())
	assert.Equal(t, 15, user.DOB.Day())
}

// TestCreateUser_InvalidDOBFormat verifies that CreateUser
// returns an error when the date of birth is not in YYYY-MM-DD format.
func TestCreateUser_InvalidDOBFormat(t *testing.T) {
	cleanUsersCollection(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	input := &models.RegisterInput{
		FullName: "Bad Date",
		Username: "baddate",
		Email:    "bad@example.com",
		Password: "password123",
		DOB:      "15/05/2000", // wrong format
	}

	user, err := models.CreateUser(ctx, testDB, input)

	assert.Error(t, err)
	assert.Nil(t, user)
	assert.Contains(t, err.Error(), "invalid date of birth format")
}

// TestCreateUser_DuplicateEmail verifies that creating two users
// with the same email returns a duplicate key error.
func TestCreateUser_DuplicateEmail(t *testing.T) {
	cleanUsersCollection(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create the first user
	input1 := &models.RegisterInput{
		FullName: "User One",
		Username: "userone",
		Email:    "duplicate@example.com",
		Password: "password123",
	}
	_, err := models.CreateUser(ctx, testDB, input1)
	require.NoError(t, err)

	// Try to create a second user with the same email
	input2 := &models.RegisterInput{
		FullName: "User Two",
		Username: "usertwo",
		Email:    "duplicate@example.com", // same email
		Password: "password456",
	}
	user, err := models.CreateUser(ctx, testDB, input2)

	assert.Error(t, err)
	assert.Nil(t, user)
	assert.Contains(t, err.Error(), "already exists")
}

// TestCreateUser_DuplicateUsername verifies that creating two
// users with the same username returns a duplicate key error.
func TestCreateUser_DuplicateUsername(t *testing.T) {
	cleanUsersCollection(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create the first user
	input1 := &models.RegisterInput{
		FullName: "User One",
		Username: "sameusername",
		Email:    "user1@example.com",
		Password: "password123",
	}
	_, err := models.CreateUser(ctx, testDB, input1)
	require.NoError(t, err)

	// Try to create a second user with the same username
	input2 := &models.RegisterInput{
		FullName: "User Two",
		Username: "sameusername", // same username
		Email:    "user2@example.com",
		Password: "password456",
	}
	user, err := models.CreateUser(ctx, testDB, input2)

	assert.Error(t, err)
	assert.Nil(t, user)
	assert.Contains(t, err.Error(), "already exists")
}

// =============================================================
// FindUserByEmail tests
// =============================================================

// TestFindUserByEmail_Success verifies that we can find a user
// by their email address after creating them.
func TestFindUserByEmail_Success(t *testing.T) {
	cleanUsersCollection(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create a user first
	input := &models.RegisterInput{
		FullName: "Find Me",
		Username: "findme",
		Email:    "findme@example.com",
		Password: "password123",
	}
	created, err := models.CreateUser(ctx, testDB, input)
	require.NoError(t, err)

	// Find the user by email
	found, err := models.FindUserByEmail(ctx, testDB, "findme@example.com")

	assert.NoError(t, err)
	assert.NotNil(t, found)
	assert.Equal(t, created.ID, found.ID)
	assert.Equal(t, "findme", found.Username)
	assert.Equal(t, "findme@example.com", found.Email)
	assert.NotEmpty(t, found.PasswordHash, "PasswordHash should be returned for auth checks")
}

// TestFindUserByEmail_NotFound verifies that FindUserByEmail
// returns an error when no user matches the given email.
func TestFindUserByEmail_NotFound(t *testing.T) {
	cleanUsersCollection(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	user, err := models.FindUserByEmail(ctx, testDB, "nonexistent@example.com")

	assert.Error(t, err)
	assert.Nil(t, user)
	assert.Contains(t, err.Error(), "user not found")
}

// =============================================================
// FindUserByID tests
// =============================================================

// TestFindUserByID_Success verifies that we can find a user
// by their MongoDB ObjectID after creating them.
func TestFindUserByID_Success(t *testing.T) {
	cleanUsersCollection(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create a user first
	input := &models.RegisterInput{
		FullName: "ID Lookup",
		Username: "idlookup",
		Email:    "idlookup@example.com",
		Password: "password123",
	}
	created, err := models.CreateUser(ctx, testDB, input)
	require.NoError(t, err)

	// Find the user by ID
	found, err := models.FindUserByID(ctx, testDB, created.ID)

	assert.NoError(t, err)
	assert.NotNil(t, found)
	assert.Equal(t, created.ID, found.ID)
	assert.Equal(t, "idlookup", found.Username)
}

// TestFindUserByID_NotFound verifies that FindUserByID
// returns an error when no user matches the given ObjectID.
func TestFindUserByID_NotFound(t *testing.T) {
	cleanUsersCollection(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	fakeID := bson.NewObjectID()
	user, err := models.FindUserByID(ctx, testDB, fakeID)

	assert.Error(t, err)
	assert.Nil(t, user)
	assert.Contains(t, err.Error(), "user not found")
}
