// Package tests contains all test files for the Online Judge backend.
// Tests follow the TDD approach — they are written BEFORE the
// implementation code. Each test file targets a specific layer
// (model, controller).
//
// # Which database these tests use
//
// A throwaway database on a LOCAL MongoDB, created fresh for each run and
// dropped afterwards. Two rules make that structural rather than a
// convention, because both were broken before and both cost real data:
//
//  1. The suite never loads ../.env. That file holds the production Atlas
//     credential, and the only thing that used to separate a test run from
//     live data was the literal string "online_judge_test" happening to
//     differ from DB_NAME. Test configuration comes from the environment
//     or from ../.env.test, which is a different file on purpose.
//
//  2. The resolved URI must point at a local or containerised MongoDB.
//     Anything else — in particular any mongodb+srv:// Atlas cluster —
//     aborts the run before a single connection is opened. Overriding it
//     takes a deliberate CODEARENA_ALLOW_REMOTE_TEST_MONGO=1.
//
// The database name carries a random suffix, so two runs on the same host
// (a developer and a watch process, two CI jobs) cannot collide. They used
// to: one run's unconditional Drop landed in the middle of another, the
// second run then wrote into an index-less collection, and every later run
// died at EnsureIndexes with a duplicate-key error.
//
// This is a deliberate exception to constitution §10 ("MongoDB Atlas —
// no local MongoDB"). §10 governs how the application is deployed and
// developed; a test suite that can reach production credentials is a
// different kind of risk, and CI has no business holding an Atlas
// password at all.
//
// Configuration:
//
//	TEST_MONGO_URI                       preferred; a local MongoDB URI
//	MONGO_URI                            used only if TEST_MONGO_URI is unset
//	../.env.test                         dotenv fallback for the two above
//	CODEARENA_ALLOW_REMOTE_TEST_MONGO=1  opt out of the locality check
package tests

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"strings"
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

// testDBPrefix marks every database this suite is allowed to create or
// destroy. Nothing without this prefix is ever dropped.
const testDBPrefix = "online_judge_test_"

// localMongoHosts are the only hosts a test run may target without an
// explicit override: loopback, and the service names a MongoDB container
// gets under Docker Compose or a GitHub Actions service container.
var localMongoHosts = map[string]bool{
	"localhost":  true,
	"127.0.0.1":  true,
	"::1":        true,
	"0.0.0.0":    true,
	"mongo":      true,
	"mongodb":    true,
	"mongo-test": true,
}

// TestMain is the entry point for all tests in this package. It resolves a
// test-only MongoDB, refuses to continue if that database could be a
// production one, creates a run-scoped database, and drops it afterwards.
func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

// runTests holds the body of TestMain so that cleanup can run through
// defer. TestMain itself cannot: os.Exit skips deferred calls, which is
// how a failed run used to leave its database behind.
func runTests(m *testing.M) int {
	// Steps to set up the test environment
	// ======================================

	// 1. Resolve the URI — environment first, then ../.env.test.
	//    ../.env is deliberately NOT consulted: it is the production
	//    credential, and this suite must not be able to reach it.
	uri := resolveTestMongoURI()

	// 2. Refuse to run against anything that is not clearly a local or
	//    containerised MongoDB. Fails loudly and prints no credential.
	if err := assertLocalMongo(uri); err != nil {
		log.Printf("FATAL: refusing to run the test suite: %v", err)
		log.Printf("Point TEST_MONGO_URI at a local MongoDB (for example " +
			"mongodb://localhost:27017), or start one with `docker run --rm -p 27017:27017 mongo:7`.")
		log.Printf("If you really mean to test against a remote cluster, set " +
			"CODEARENA_ALLOW_REMOTE_TEST_MONGO=1 and accept that it is not production.")
		return 1
	}

	// 3. Connect.
	var err error
	testClient, err = database.Connect(uri)
	if err != nil {
		// The error can embed the URI, so report only its type.
		log.Printf("FATAL: could not connect to the test MongoDB (%T)", err)
		return 1
	}
	defer database.Disconnect(testClient)

	// 4. Use a database unique to this run, so two runs on one host cannot
	//    drop each other's collections halfway through.
	dbName, err := uniqueTestDBName()
	if err != nil {
		log.Printf("FATAL: could not generate a test database name: %v", err)
		return 1
	}
	if prod := os.Getenv("DB_NAME"); prod != "" && dbName == prod {
		log.Printf("FATAL: the generated test database collides with DB_NAME")
		return 1
	}
	testDB = testClient.Database(dbName)

	// 5. Always drop this run's database, pass or fail. The prefix check is
	//    a belt-and-braces guard on the one destructive call in the suite.
	defer func() {
		if !strings.HasPrefix(testDB.Name(), testDBPrefix) {
			log.Printf("REFUSING to drop %q: not a test database", testDB.Name())
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := testDB.Drop(ctx); err != nil {
			log.Printf("warning: could not drop the test database: %v", err)
		}
	}()

	// 6. Ensure indexes exist on the fresh test database.
	if err := database.EnsureIndexes(testDB); err != nil {
		log.Printf("FATAL: could not create indexes on the test database: %v", err)
		return 1
	}

	// 7. Run all the tests.
	return m.Run()
}

// resolveTestMongoURI returns the URI to test against, preferring an
// explicitly test-scoped variable and falling back to ../.env.test.
func resolveTestMongoURI() string {
	if uri := os.Getenv("TEST_MONGO_URI"); uri != "" {
		return uri
	}
	if uri := os.Getenv("MONGO_URI"); uri != "" {
		return uri
	}
	// godotenv.Load never overrides variables already set, so this only
	// fills the gap left above.
	if err := godotenv.Load("../.env.test"); err != nil {
		log.Println("note: no ../.env.test file; relying on the environment")
	}
	if uri := os.Getenv("TEST_MONGO_URI"); uri != "" {
		return uri
	}
	return os.Getenv("MONGO_URI")
}

// uniqueTestDBName builds a database name scoped to this run.
func uniqueTestDBName() (string, error) {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	// 18 + 12 characters, comfortably inside MongoDB's 63-byte limit.
	return testDBPrefix + hex.EncodeToString(buf), nil
}

// assertLocalMongo returns an error unless the URI clearly points at a
// local or containerised MongoDB. It never includes the URI, any host or
// any credential in what it returns — a failure message is a thing people
// paste into issues.
func assertLocalMongo(uri string) error {
	if uri == "" {
		return fmt.Errorf("no test MongoDB URI is configured " +
			"(set TEST_MONGO_URI, or create server/.env.test)")
	}

	if os.Getenv("CODEARENA_ALLOW_REMOTE_TEST_MONGO") != "" {
		log.Println("warning: CODEARENA_ALLOW_REMOTE_TEST_MONGO is set — " +
			"the locality check on the test database is disabled")
		return nil
	}

	scheme, authority, err := splitMongoURI(uri)
	if err != nil {
		return err
	}
	if scheme == "mongodb+srv" {
		// mongodb+srv is the Atlas connection form. Nothing that resolves
		// through SRV is a local test instance.
		return fmt.Errorf("the URI uses mongodb+srv://, which is a hosted " +
			"cluster and never a test instance")
	}
	if scheme != "mongodb" {
		return fmt.Errorf("unrecognised MongoDB URI scheme")
	}

	hosts := mongoHosts(authority)
	if len(hosts) == 0 {
		return fmt.Errorf("the URI names no host")
	}
	for _, h := range hosts {
		if !localMongoHosts[h] {
			// Deliberately does not echo the host.
			return fmt.Errorf("the URI points at a host that is not a known " +
				"local or containerised MongoDB")
		}
	}
	return nil
}

// splitMongoURI pulls the scheme and the host section out of a MongoDB
// URI without net/url, which rejects the comma-separated host lists a
// replica-set URI is allowed to use.
func splitMongoURI(uri string) (scheme, authority string, err error) {
	i := strings.Index(uri, "://")
	if i < 0 {
		return "", "", fmt.Errorf("the URI has no scheme")
	}
	scheme = strings.ToLower(uri[:i])
	rest := uri[i+3:]

	// Trim the path and query, leaving [userinfo@]host[,host...].
	if j := strings.IndexAny(rest, "/?"); j >= 0 {
		rest = rest[:j]
	}
	// A password may itself contain an escaped '@', so take the last one.
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		rest = rest[at+1:]
	}
	return scheme, rest, nil
}

// mongoHosts splits a host section into lowercased hostnames, dropping
// ports and IPv6 brackets.
func mongoHosts(authority string) []string {
	var hosts []string
	for _, hp := range strings.Split(authority, ",") {
		hp = strings.TrimSpace(hp)
		if hp == "" {
			continue
		}
		if strings.HasPrefix(hp, "[") {
			// [::1]:27017 — the bracketed literal is the host.
			if end := strings.Index(hp, "]"); end > 0 {
				hosts = append(hosts, strings.ToLower(hp[1:end]))
				continue
			}
		}
		if colon := strings.LastIndex(hp, ":"); colon >= 0 {
			hp = hp[:colon]
		}
		hosts = append(hosts, strings.ToLower(hp))
	}
	return hosts
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
