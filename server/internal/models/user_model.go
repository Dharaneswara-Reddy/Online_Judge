// Package models defines the data structures and database
// operations for the application. Each model file contains
// the struct definition and all CRUD functions for one
// MongoDB collection.
//
// Models are the only layer that talks to the database.
// Controllers call model functions with plain Go types —
// models never read HTTP request objects.
package models

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// User represents a user document in the "users" collection.
// The struct maps directly to the MongoDB schema defined in
// the HLD document (Online_Judge.pdf).
//
// Fields:
//   - ID:           MongoDB ObjectID, auto-generated on insert
//   - Username:     Unique handle chosen by the user
//   - Email:        Unique email address for login
//   - PasswordHash: bcrypt hash of the user's password (never sent to client)
//   - FullName:     User's display name
//   - DOB:          Date of birth (optional)
//   - Role:         Either "user" or "admin"
//   - CreatedAt:    Timestamp of account creation
type User struct {
	ID           bson.ObjectID `bson:"_id,omitempty" json:"id"`
	Username     string        `bson:"username"      json:"username"`
	Email        string        `bson:"email"         json:"email"`
	PasswordHash string        `bson:"password_hash" json:"-"`
	FullName     string        `bson:"full_name"     json:"full_name"`
	DOB          *time.Time    `bson:"dob,omitempty" json:"dob,omitempty"`
	Role         string        `bson:"role"          json:"role"`
	CreatedAt    time.Time     `bson:"created_at"    json:"created_at"`
}

// RegisterInput holds the data needed to create a new user.
// This is separate from User because it contains the raw
// password (not the hash) and doesn't include server-set
// fields like ID, Role, or CreatedAt.
type RegisterInput struct {
	FullName string `json:"full_name"`
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
	DOB      string `json:"dob,omitempty"` // ISO 8601 date string (optional)
}

// LoginInput holds the data needed to authenticate a user.
type LoginInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// HashPassword takes a plaintext password and returns its
// bcrypt hash. The cost factor of 10 balances security with
// performance — each hash takes roughly 100ms.
func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), 10)
	if err != nil {
		return "", fmt.Errorf("failed to hash password: %w", err)
	}
	return string(bytes), nil
}

// dummyHash is a bcrypt hash of a fixed random string, used only to
// spend the same time as a real comparison when no account exists.
var dummyHash = []byte("$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy")

// BurnPasswordComparison performs a throwaway bcrypt comparison.
//
// Login must take about as long whether or not the account exists.
// Returning early for an unknown email would leak that difference as a
// timing oracle, letting an attacker enumerate which addresses are
// registered before spending any guesses on passwords.
func BurnPasswordComparison(password string) {
	_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
}

// CheckPassword compares a bcrypt hash with a plaintext
// password. Returns true if they match, false otherwise.
// This function never reveals which part failed — it simply
// returns a boolean.
func CheckPassword(hash, password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// CreateUser inserts a new user document into the "users"
// collection. It hashes the password, sets default values
// (role, created_at), and returns the created user.
//
// If the username or email already exists (unique index
// violation), MongoDB returns a duplicate key error which
// this function wraps and returns.
func CreateUser(ctx context.Context, db *mongo.Database, input *RegisterInput) (*User, error) {
	// Steps to follow while creating a user
	// =======================================

	// 1. Hash the plaintext password with bcrypt
	hash, err := HashPassword(input.Password)
	if err != nil {
		return nil, err
	}

	// 2. Parse the optional date of birth if provided
	var dob *time.Time
	if input.DOB != "" {
		parsed, err := time.Parse("2006-01-02", input.DOB)
		if err != nil {
			return nil, fmt.Errorf("invalid date of birth format, expected YYYY-MM-DD: %w", err)
		}
		dob = &parsed
	}

	// 3. Build the user document with default values
	user := &User{
		Username:     input.Username,
		Email:        input.Email,
		PasswordHash: hash,
		FullName:     input.FullName,
		DOB:          dob,
		Role:         "user",
		CreatedAt:    time.Now(),
	}

	// 4. Insert the document into the users collection
	collection := db.Collection("users")
	result, err := collection.InsertOne(ctx, user)
	if err != nil {
		// Check if this is a duplicate key error (username or email already exists)
		if mongo.IsDuplicateKeyError(err) {
			return nil, fmt.Errorf("username or email already exists")
		}
		return nil, fmt.Errorf("failed to insert user: %w", err)
	}

	// 5. Set the generated ID on the user struct
	if oid, ok := result.InsertedID.(bson.ObjectID); ok {
		user.ID = oid
	}

	// 6. Return the created user (password hash is excluded from JSON by the json:"-" tag)
	return user, nil
}

// ProfileUpdate holds the fields a user is allowed to change on their
// own account. Role, email, and password are deliberately absent —
// changing those needs its own audited flow.
type ProfileUpdate struct {
	FullName string `json:"full_name"`
	DOB      string `json:"dob,omitempty"` // ISO 8601 date string (optional)
}

// UpdateUserProfile applies a ProfileUpdate to one user document and
// returns the updated user.
//
// Only non-empty fields are written, so a partial PATCH leaves the
// other fields untouched.
func UpdateUserProfile(ctx context.Context, db *mongo.Database, id bson.ObjectID, update *ProfileUpdate) (*User, error) {
	// Steps to follow while updating a profile
	// ==========================================

	// 1. Build the set of fields to change, skipping empty ones
	set := bson.M{}
	if update.FullName != "" {
		set["full_name"] = update.FullName
	}
	if update.DOB != "" {
		parsed, err := time.Parse("2006-01-02", update.DOB)
		if err != nil {
			return nil, fmt.Errorf("invalid date of birth format, expected YYYY-MM-DD: %w", err)
		}
		set["dob"] = parsed
	}

	// 2. Nothing to change — return the user as-is rather than erroring
	if len(set) == 0 {
		return FindUserByID(ctx, db, id)
	}

	// 3. Apply the update
	collection := db.Collection("users")
	result, err := collection.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": set})
	if err != nil {
		return nil, fmt.Errorf("failed to update user: %w", err)
	}
	if result.MatchedCount == 0 {
		return nil, fmt.Errorf("user not found")
	}

	// 4. Return the fresh document
	return FindUserByID(ctx, db, id)
}

// FindUserByEmail looks up a user by their email address.
// Returns the full user document (including password hash)
// so the caller can verify credentials.
//
// Returns nil and an error if no user is found.
func FindUserByEmail(ctx context.Context, db *mongo.Database, email string) (*User, error) {
	// Steps to follow while finding a user by email
	// ===============================================

	// 1. Get a reference to the users collection
	collection := db.Collection("users")

	// 2. Query for a document with the matching email
	var user User
	err := collection.FindOne(ctx, bson.M{"email": email}).Decode(&user)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("user not found")
		}
		return nil, fmt.Errorf("failed to find user: %w", err)
	}

	// 3. Return the found user
	return &user, nil
}

// FindUserByID looks up a user by their MongoDB ObjectID.
// Returns the full user document. Used by the auth middleware
// to load the current user from the JWT claims.
//
// Returns nil and an error if no user is found.
func FindUserByID(ctx context.Context, db *mongo.Database, id bson.ObjectID) (*User, error) {
	// Steps to follow while finding a user by ID
	// =============================================

	// 1. Get a reference to the users collection
	collection := db.Collection("users")

	// 2. Query for a document with the matching _id
	var user User
	err := collection.FindOne(ctx, bson.M{"_id": id}).Decode(&user)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("user not found")
		}
		return nil, fmt.Errorf("failed to find user: %w", err)
	}

	// 3. Return the found user
	return &user, nil
}
