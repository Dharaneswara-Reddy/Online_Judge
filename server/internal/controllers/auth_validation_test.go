package controllers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/toji339/online-judge/internal/models"
)

func validInput() models.RegisterInput {
	return models.RegisterInput{
		FullName: "Ada Lovelace",
		Username: "ada",
		Email:    "ada@example.com",
		Password: "correct horse battery",
	}
}

func TestValidateRegistration_AcceptsAGoodInput(t *testing.T) {
	assert.Empty(t, validateRegistration(validInput()))
}

// TestValidateRegistration_RejectsAShortPassword pins the raised
// minimum. Six characters is not a password.
func TestValidateRegistration_RejectsAShortPassword(t *testing.T) {
	input := validInput()
	input.Password = "abc1234" // seven

	errs := validateRegistration(input)

	require.NotEmpty(t, errs)
	assert.Contains(t, strings.Join(errs, " "), "8",
		"the message must state the rule the caller has to satisfy")
}

func TestValidateRegistration_AcceptsExactlyTheMinimum(t *testing.T) {
	input := validInput()
	input.Password = "abcd1234" // eight

	assert.Empty(t, validateRegistration(input))
}

// TestValidateRegistration_RejectsAPasswordBcryptCannotHash is the 500
// fix: bcrypt errors on anything over 72 bytes, and that surfaced as an
// internal server error instead of a validation failure.
func TestValidateRegistration_RejectsAPasswordBcryptCannotHash(t *testing.T) {
	input := validInput()
	input.Password = strings.Repeat("a", maxPasswordBytes+1)

	errs := validateRegistration(input)

	require.NotEmpty(t, errs)
	assert.Contains(t, strings.Join(errs, " "), "72")
}

func TestValidateRegistration_AcceptsExactlyTheMaximum(t *testing.T) {
	input := validInput()
	input.Password = strings.Repeat("a", maxPasswordBytes)

	assert.Empty(t, validateRegistration(input))
}

// TestValidateRegistration_CountsBytesNotRunes matters because the
// bcrypt limit is a byte limit: 30 three-byte characters is 90 bytes,
// which bcrypt refuses even though it is only 30 characters.
func TestValidateRegistration_CountsBytesNotRunes(t *testing.T) {
	input := validInput()
	input.Password = strings.Repeat("パ", 30)

	assert.NotEmpty(t, validateRegistration(input))
}

// TestValidateRegistration_ReportsEveryFormatProblemAtOnce keeps the
// caller from discovering the rules one submission at a time.
func TestValidateRegistration_ReportsEveryFormatProblemAtOnce(t *testing.T) {
	errs := validateRegistration(models.RegisterInput{
		FullName: "Ada Lovelace",
		Username: "ab",
		Email:    "not-an-email",
		Password: "short",
	})

	joined := strings.Join(errs, " | ")
	assert.Contains(t, joined, "Username")
	assert.Contains(t, joined, "Email")
	assert.Contains(t, joined, "Password")
}

// TestValidateRegistration_MissingFieldsShortCircuit documents the
// deliberate two-phase behaviour: format complaints about values the
// caller never sent would be noise.
func TestValidateRegistration_MissingFieldsShortCircuit(t *testing.T) {
	errs := validateRegistration(models.RegisterInput{})

	joined := strings.Join(errs, " | ")
	assert.Contains(t, joined, "Full name is required")
	assert.Contains(t, joined, "Username is required")
	assert.Contains(t, joined, "Email is required")
	assert.Contains(t, joined, "Password is required")
	assert.NotContains(t, joined, "at least 8")
}

// TestRegister_LongPasswordIsRejectedBeforeTheDatabase drives the real
// handler. The controller is built with a nil database on purpose: if
// the over-long password ever reached bcrypt or Mongo this would blow
// up as a 500, which is exactly the bug being fixed.
func TestRegister_LongPasswordIsRejectedBeforeTheDatabase(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(gin.Recovery())
	router.POST("/register", NewAuthController(nil, "test-secret", false, nil).Register)

	body, err := json.Marshal(map[string]string{
		"full_name": "Ada Lovelace",
		"username":  "ada",
		"email":     "ada@example.com",
		"password":  strings.Repeat("a", 200),
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())

	var resp struct {
		Success bool     `json:"success"`
		Message string   `json:"message"`
		Errors  []string `json:"errors"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp.Success)
	assert.NotEmpty(t, resp.Errors, "the contract is an errors array, not a details string")
}
