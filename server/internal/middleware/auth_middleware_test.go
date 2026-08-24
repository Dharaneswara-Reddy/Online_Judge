package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/toji339/online-judge/internal/middleware"
)

// testSecret is a throwaway signing key. It is only ever used by this
// file, and it is long enough to match the production floor so nothing
// here accidentally documents a weak key as acceptable.
const testSecret = "unit-test-signing-key-not-a-real-secret"

// signHS256 mints a token the way the login handler does.
func signHS256(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSecret))
	require.NoError(t, err)
	return signed
}

// validClaims is a well-formed claim set for a normal user.
func validClaims() jwt.MapClaims {
	return jwt.MapClaims{
		"sub":      "507f1f77bcf86cd799439011",
		"username": "alice",
		"role":     "user",
		"iat":      time.Now().Add(-time.Minute).Unix(),
		"exp":      time.Now().Add(time.Hour).Unix(),
	}
}

// runWithToken drives one request through AuthMiddleware carrying the
// given cookie value, and reports what the protected handler saw.
func runWithToken(t *testing.T, guard gin.HandlerFunc, token string) (*httptest.ResponseRecorder, map[string]string) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	seen := map[string]string{}
	router := gin.New()
	router.GET("/protected", guard, func(c *gin.Context) {
		for _, key := range []string{"userID", "username", "role"} {
			if v, ok := c.Get(key); ok {
				if s, ok := v.(string); ok {
					seen[key] = s
				}
			}
		}
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	if token != "" {
		req.AddCookie(&http.Cookie{Name: "token", Value: token})
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w, seen
}

// TestAuthMiddleware_AcceptsAWellFormedToken is the happy path.
func TestAuthMiddleware_AcceptsAWellFormedToken(t *testing.T) {
	guard := middleware.AuthMiddleware(testSecret)

	w, seen := runWithToken(t, guard, signHS256(t, validClaims()))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "507f1f77bcf86cd799439011", seen["userID"])
	assert.Equal(t, "alice", seen["username"])
	assert.Equal(t, "user", seen["role"])
}

// TestAuthMiddleware_RejectsAMissingCookie covers the anonymous caller.
func TestAuthMiddleware_RejectsAMissingCookie(t *testing.T) {
	w, _ := runWithToken(t, middleware.AuthMiddleware(testSecret), "")

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestAuthMiddleware_RejectsTheNoneAlgorithm is the classic JWT forgery:
// an attacker strips the signature and sets alg to "none". Nothing about
// the claims is wrong, so only algorithm pinning catches it.
func TestAuthMiddleware_RejectsTheNoneAlgorithm(t *testing.T) {
	forged, err := jwt.NewWithClaims(jwt.SigningMethodNone, validClaims()).
		SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	w, _ := runWithToken(t, middleware.AuthMiddleware(testSecret), forged)

	assert.Equal(t, http.StatusUnauthorized, w.Code,
		"an unsigned token must never authenticate anybody")
}

// TestAuthMiddleware_RejectsAnotherHMACAlgorithm pins the algorithm to
// exactly the one we issue. HS384 with our key is not a forgery on its
// own, but accepting a set of algorithms rather than one is how
// algorithm-confusion bugs get their foothold, and there is no reason to
// accept a token this server would never mint.
func TestAuthMiddleware_RejectsAnotherHMACAlgorithm(t *testing.T) {
	other, err := jwt.NewWithClaims(jwt.SigningMethodHS384, validClaims()).
		SignedString([]byte(testSecret))
	require.NoError(t, err)

	w, _ := runWithToken(t, middleware.AuthMiddleware(testSecret), other)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestAuthMiddleware_RejectsAForeignSignature covers the ordinary case
// of a token minted with a different key.
func TestAuthMiddleware_RejectsAForeignSignature(t *testing.T) {
	forged, err := jwt.NewWithClaims(jwt.SigningMethodHS256, validClaims()).
		SignedString([]byte("a-completely-different-signing-key-value"))
	require.NoError(t, err)

	w, _ := runWithToken(t, middleware.AuthMiddleware(testSecret), forged)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestAuthMiddleware_RejectsAnExpiredToken keeps the 24 hour lifetime
// meaningful.
func TestAuthMiddleware_RejectsAnExpiredToken(t *testing.T) {
	claims := validClaims()
	claims["iat"] = time.Now().Add(-48 * time.Hour).Unix()
	claims["exp"] = time.Now().Add(-time.Hour).Unix()

	w, _ := runWithToken(t, middleware.AuthMiddleware(testSecret), signHS256(t, claims))

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestAuthMiddleware_RejectsATokenWithNoExpiry is the quieter half of
// the same rule. jwt/v5 validates exp only when it is present, so a
// token that simply omits the claim is otherwise valid forever.
func TestAuthMiddleware_RejectsATokenWithNoExpiry(t *testing.T) {
	claims := validClaims()
	delete(claims, "exp")

	w, _ := runWithToken(t, middleware.AuthMiddleware(testSecret), signHS256(t, claims))

	assert.Equal(t, http.StatusUnauthorized, w.Code,
		"a token with no exp would outlive every incident response")
}

// TestAuthMiddleware_RejectsMissingClaimsWithoutPanicking is the crash
// this file was written for: the middleware type-asserted every claim
// without checking, so a validly signed token missing "username" took
// the request down with a panic rather than a 401.
func TestAuthMiddleware_RejectsMissingClaimsWithoutPanicking(t *testing.T) {
	for _, missing := range []string{"sub", "username", "role"} {
		t.Run("missing_"+missing, func(t *testing.T) {
			claims := validClaims()
			delete(claims, missing)

			var w *httptest.ResponseRecorder
			require.NotPanics(t, func() {
				w, _ = runWithToken(t, middleware.AuthMiddleware(testSecret), signHS256(t, claims))
			})
			assert.Equal(t, http.StatusUnauthorized, w.Code)
		})
	}
}

// TestAuthMiddleware_RejectsWronglyTypedClaims covers the same assertion
// bug from the other side — a claim that is present but is a number.
func TestAuthMiddleware_RejectsWronglyTypedClaims(t *testing.T) {
	claims := validClaims()
	claims["role"] = 42

	var w *httptest.ResponseRecorder
	require.NotPanics(t, func() {
		w, _ = runWithToken(t, middleware.AuthMiddleware(testSecret), signHS256(t, claims))
	})
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestAdminOnly_RejectsANonStringRole guards the same class of assertion
// in the admin gate.
func TestAdminOnly_RejectsANonStringRole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/admin",
		func(c *gin.Context) { c.Set("role", 7) },
		middleware.AdminOnly(),
		func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	require.NotPanics(t, func() {
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin", nil))
	})
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// TestOptionalAuth_IgnoresTheNoneAlgorithm holds the public read paths
// to the same pinning rule. A forged token here does not grant access,
// but it would let an attacker render a page as somebody else.
func TestOptionalAuth_IgnoresTheNoneAlgorithm(t *testing.T) {
	forged, err := jwt.NewWithClaims(jwt.SigningMethodNone, validClaims()).
		SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	w, seen := runWithToken(t, middleware.OptionalAuth(testSecret), forged)

	assert.Equal(t, http.StatusOK, w.Code, "this middleware never rejects anybody")
	assert.Empty(t, seen["userID"], "but it must not believe an unsigned token")
}

// TestOptionalAuth_IdentifiesAValidCaller is its happy path.
func TestOptionalAuth_IdentifiesAValidCaller(t *testing.T) {
	w, seen := runWithToken(t, middleware.OptionalAuth(testSecret), signHS256(t, validClaims()))

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "alice", seen["username"])
}
