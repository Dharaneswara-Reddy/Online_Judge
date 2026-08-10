// Package companytag records which companies users have seen a problem
// in, and aggregates those reports so problems can be browsed by company.
//
// Two representations exist on purpose. Each individual report is one
// document, with a unique index on (problem_id, user_id, company) so one
// user cannot inflate a single company's count. The per-problem
// company_tags array is a denormalised read-optimised summary kept in
// step with those documents, and it is what the problem list filter and
// the company explorer read.
package companytag

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// ErrAlreadyTagged is returned when a user tags the same problem with
// the same company twice.
var ErrAlreadyTagged = errors.New("you have already tagged this problem with that company")

// Tag is one user's report that a problem appeared at a company.
type Tag struct {
	ID        string    `bson:"_id,omitempty" json:"id"`
	ProblemID string    `bson:"problem_id" json:"problemId"`
	UserID    string    `bson:"user_id" json:"userId"`
	Company   string    `bson:"company" json:"company"`
	Round     string    `bson:"round,omitempty" json:"round,omitempty"`
	CreatedAt time.Time `bson:"created_at" json:"createdAt"`
}

// CompanyCount is one row of the aggregated company view.
type CompanyCount struct {
	Company      string `bson:"_id" json:"company"`
	ProblemCount int    `bson:"problem_count" json:"problemCount"`
	TagCount     int    `bson:"tag_count" json:"tagCount"`
}

// Interview rounds a user may attribute a tag to. An empty round is
// allowed — plenty of people remember the company but not the stage.
var validRounds = map[string]bool{
	"":             true,
	"oa":           true,
	"phone screen": true,
	"onsite":       true,
	"final":        true,
}

// ValidationError describes a rejected input field.
type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// NormalizeCompany canonicalises a company name so "Google", "google "
// and "GOOGLE" all aggregate into one bucket.
//
// It title-cases the result for display, since the stored value is what
// the company explorer shows.
func NormalizeCompany(raw string) string {
	trimmed := strings.Join(strings.Fields(strings.TrimSpace(raw)), " ")
	if trimmed == "" {
		return ""
	}

	words := strings.Split(strings.ToLower(trimmed), " ")
	for i, word := range words {
		runes := []rune(word)
		runes[0] = unicode.ToUpper(runes[0])
		words[i] = string(runes)
	}
	return strings.Join(words, " ")
}

// NormalizeRound lower-cases an interview round for comparison.
func NormalizeRound(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

// Validate checks a proposed tag and returns the normalised company and
// round to store.
func Validate(company, round string) (string, string, error) {
	normalized := NormalizeCompany(company)
	if normalized == "" {
		return "", "", ValidationError{Field: "company", Message: "is required"}
	}
	if len([]rune(normalized)) > 64 {
		return "", "", ValidationError{Field: "company", Message: "is too long"}
	}

	normalizedRound := NormalizeRound(round)
	if !validRounds[normalizedRound] {
		return "", "", ValidationError{Field: "round", Message: "must be OA, phone screen, onsite, or final"}
	}
	return normalized, normalizedRound, nil
}
