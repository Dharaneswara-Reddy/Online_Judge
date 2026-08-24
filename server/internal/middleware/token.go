package middleware

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// signingAlgorithm is the only JWT algorithm this service issues, and
// therefore the only one it will verify.
//
// Pinning it is not optional. jwt.Parse decides what to verify with from
// the token's own "alg" header, so a parser that accepts a family of
// algorithms is trusting the attacker's choice among them. The classic
// results are "alg": "none", which asks to be believed with no signature
// at all, and RS256-to-HS256 confusion, where a public key is replayed
// as an HMAC key. Naming one algorithm closes both, and there is no
// legitimate token in this system signed with anything else.
const signingAlgorithm = "HS256"

// errIncompleteClaims means the signature checked out but the payload is
// not one this service would have issued.
var errIncompleteClaims = errors.New("token is missing a required claim")

// sessionClaims is the payload of a CodeArena session token.
//
// It is a struct rather than jwt.MapClaims on purpose. MapClaims hands
// back `any` for every field, and the old middleware type-asserted each
// one without checking — so a token whose "role" was a number panicked
// the request instead of failing it. Decoding into typed fields makes a
// wrongly-typed claim a parse error, which is the outcome we want, and
// leaves no unchecked assertion anywhere in the auth path.
type sessionClaims struct {
	Username string `json:"username"`
	Role     string `json:"role"`

	// RegisteredClaims carries sub, jti, iat and exp with real types.
	jwt.RegisteredClaims
}

// UserID is the subject claim under the name the rest of the app uses.
func (c sessionClaims) UserID() string { return c.Subject }

// SessionID is the jti — the per-session identifier that revocation is
// keyed by. Tokens issued before session revocation existed have none,
// which callers must handle rather than assume.
func (c sessionClaims) SessionID() string { return c.ID }

// IssuedAtTime is the iat claim as a time, or the zero time when the
// token carries none.
func (c sessionClaims) IssuedAtTime() time.Time {
	if c.IssuedAt == nil {
		return time.Time{}
	}
	return c.IssuedAt.Time
}

// ExpiresAtTime is the exp claim as a time. Parsing requires exp, so a
// successfully parsed token always has one.
func (c sessionClaims) ExpiresAtTime() time.Time {
	if c.ExpiresAt == nil {
		return time.Time{}
	}
	return c.ExpiresAt.Time
}

// parseSessionToken verifies a session token and returns its claims.
//
// Every rejection returns the same opaque error to the caller's caller:
// the middleware answers 401 either way, and distinguishing "bad
// signature" from "expired" in a response only helps someone probing.
//
// The parser is configured with three rules that the defaults do not
// give you:
//
//   - WithValidMethods pins the algorithm (see signingAlgorithm).
//   - WithExpirationRequired makes exp mandatory. jwt/v5 validates exp
//     when it is present and shrugs when it is not, so without this a
//     token that simply omits the claim is valid forever — which would
//     leave revocation as the only lever that ever ends a session.
//   - The claim completeness check below rejects a token missing sub,
//     username or role, since downstream handlers read all three.
//
// It never logs the token, or any part of it. A session token is a
// bearer credential: anything written about it belongs in a log only if
// it would be safe to hand a reader the account it names.
func parseSessionToken(tokenString, jwtSecret string) (sessionClaims, error) {
	var claims sessionClaims

	_, err := jwt.ParseWithClaims(tokenString, &claims,
		func(*jwt.Token) (any, error) { return []byte(jwtSecret), nil },
		jwt.WithValidMethods([]string{signingAlgorithm}),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return sessionClaims{}, err
	}

	if claims.Subject == "" || claims.Username == "" || claims.Role == "" {
		return sessionClaims{}, errIncompleteClaims
	}

	return claims, nil
}
