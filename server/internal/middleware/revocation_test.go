package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/toji339/online-judge/internal/middleware"
	"github.com/toji339/online-judge/internal/session"
)

const revSecret = "a-test-signing-key-long-enough-to-pass-validation"

// issue mints a token the way the API does, with a jti so it can be revoked.
func issue(t *testing.T, userID, sessionID string, issuedAt time.Time) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":      userID,
		"username": "probe",
		"role":     "user",
		"jti":      sessionID,
		"iat":      issuedAt.Unix(),
		"exp":      issuedAt.Add(24 * time.Hour).Unix(),
	})
	s, err := tok.SignedString([]byte(revSecret))
	require.NoError(t, err)
	return s
}

func revRouter(t *testing.T, store session.Checker) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/me", middleware.AuthMiddleware(revSecret, store), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"userID": c.GetString("userID")})
	})
	return r
}

func do(t *testing.T, r *gin.Engine, token string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.AddCookie(&http.Cookie{Name: "token", Value: token})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

// A session that has not been revoked authenticates normally. Revocation
// must not become a way to break ordinary logins.
func TestAuth_NormalSessionIsAccepted(t *testing.T) {
	store := session.NewMemoryStore()
	r := revRouter(t, store)
	assert.Equal(t, http.StatusOK, do(t, r, issue(t, "user-1", "sess-1", time.Now())))
}

// The whole point: after logout the same token must stop working, even
// though its signature is still valid and it has not expired.
func TestAuth_RevokedSessionIsRejected(t *testing.T) {
	store := session.NewMemoryStore()
	r := revRouter(t, store)
	tok := issue(t, "user-1", "sess-1", time.Now())
	require.Equal(t, http.StatusOK, do(t, r, tok))

	require.NoError(t, store.RevokeSession(context.Background(), "sess-1", time.Now().Add(24*time.Hour)))

	assert.Equal(t, http.StatusUnauthorized, do(t, r, tok),
		"a revoked token must be rejected while it is still cryptographically valid")
}

// Revoking one device must not sign the user out everywhere.
func TestAuth_RevokingOneSessionSparesTheOthers(t *testing.T) {
	store := session.NewMemoryStore()
	r := revRouter(t, store)
	laptop := issue(t, "user-1", "sess-laptop", time.Now())
	phone := issue(t, "user-1", "sess-phone", time.Now())

	require.NoError(t, store.RevokeSession(context.Background(), "sess-laptop", time.Now().Add(24*time.Hour)))

	assert.Equal(t, http.StatusUnauthorized, do(t, r, laptop))
	assert.Equal(t, http.StatusOK, do(t, r, phone), "the other session must keep working")
}

// "Log out everywhere" — every session issued before the cutoff dies, and
// a login afterwards is unaffected.
func TestAuth_RevokeAllEndsExistingSessionsOnly(t *testing.T) {
	store := session.NewMemoryStore()
	r := revRouter(t, store)
	old := issue(t, "user-1", "sess-old", time.Now().Add(-time.Hour))

	require.NoError(t, store.RevokeUserSessions(context.Background(), "user-1", time.Now(), time.Now().Add(24*time.Hour)))
	assert.Equal(t, http.StatusUnauthorized, do(t, r, old))

	fresh := issue(t, "user-1", "sess-new", time.Now().Add(time.Second))
	assert.Equal(t, http.StatusOK, do(t, r, fresh), "a login after the revocation must still work")
}

// An expired token is rejected regardless of revocation state.
func TestAuth_ExpiredTokenIsRejected(t *testing.T) {
	store := session.NewMemoryStore()
	r := revRouter(t, store)
	assert.Equal(t, http.StatusUnauthorized, do(t, r, issue(t, "user-1", "sess-1", time.Now().Add(-48*time.Hour))))
}

// A nil store means revocation was not configured. Authentication must
// still work rather than failing shut and locking everyone out.
func TestAuth_NilStoreStillAuthenticates(t *testing.T) {
	r := revRouter(t, nil)
	assert.Equal(t, http.StatusOK, do(t, r, issue(t, "user-1", "sess-1", time.Now())))
}

func TestAuth_ConcurrentRequestsAreSafe(t *testing.T) {
	store := session.NewMemoryStore()
	r := revRouter(t, store)
	good := issue(t, "user-1", "sess-good", time.Now())
	bad := issue(t, "user-2", "sess-bad", time.Now())
	require.NoError(t, store.RevokeSession(context.Background(), "sess-bad", time.Now().Add(24*time.Hour)))

	done := make(chan [2]int, 40)
	for i := 0; i < 20; i++ {
		go func() { done <- [2]int{do(t, r, good), do(t, r, bad)} }()
	}
	for i := 0; i < 20; i++ {
		got := <-done
		assert.Equal(t, http.StatusOK, got[0])
		assert.Equal(t, http.StatusUnauthorized, got[1])
	}
}

// Logout is not behind AuthMiddleware, so it has to identify the session
// from the cookie itself. Without this the revocation code could never
// fire: the context is empty when no middleware ran, and logout appeared
// to succeed while leaving the token fully usable.
func TestSessionIDFromToken_ReadsAValidToken(t *testing.T) {
	tok := issue(t, "user-1", "sess-42", time.Now())
	got, ok := middleware.SessionIDFromToken(tok, revSecret)
	assert.True(t, ok)
	assert.Equal(t, "sess-42", got)
}

// Signing out with an expired token must still name the session, or a
// user whose token lapsed could never clear it.
func TestSessionIDFromToken_ToleratesAnExpiredToken(t *testing.T) {
	tok := issue(t, "user-1", "sess-old", time.Now().Add(-48*time.Hour))
	got, ok := middleware.SessionIDFromToken(tok, revSecret)
	assert.True(t, ok, "an expired token still identifies its own session")
	assert.Equal(t, "sess-old", got)
}

// A forged or foreign token must not let a caller revoke someone else's
// session — the signature is still checked.
func TestSessionIDFromToken_RejectsAForeignSignature(t *testing.T) {
	tok := issue(t, "user-1", "sess-1", time.Now())
	_, ok := middleware.SessionIDFromToken(tok, "a-completely-different-signing-key-value")
	assert.False(t, ok, "a token we did not sign must not name a revocable session")
}

func TestSessionIDFromToken_RejectsGarbage(t *testing.T) {
	_, ok := middleware.SessionIDFromToken("not-a-token", revSecret)
	assert.False(t, ok)
}
