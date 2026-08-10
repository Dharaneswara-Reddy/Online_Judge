package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// OptionalAuth identifies the caller when they are signed in, and lets
// the request through untouched when they are not.
//
// It exists for endpoints that are public but render differently for a
// known user — a discussion thread showing which comments you upvoted,
// or a company tag widget that stops asking a question you answered.
// Anything that must reject anonymous callers uses AuthMiddleware
// instead; this one never rejects anybody.
func OptionalAuth(jwtSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Steps to follow while optionally identifying a caller
		// =======================================================

		// 1. No cookie simply means an anonymous reader
		tokenString, err := c.Cookie("token")
		if err != nil {
			c.Next()
			return
		}

		// 2. Parse the token. An invalid one is treated as anonymous
		//    rather than an error, since this route does not need auth.
		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return []byte(jwtSecret), nil
		})
		if err != nil || !token.Valid {
			c.Next()
			return
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			c.Next()
			return
		}

		// 3. Publish whichever claims are present and well-typed
		setStringClaim(c, "userID", claims["sub"])
		setStringClaim(c, "username", claims["username"])
		setStringClaim(c, "role", claims["role"])

		c.Next()
	}
}

// setStringClaim stores a claim only when it really is a string, so a
// malformed token cannot panic the request.
func setStringClaim(c *gin.Context, key string, value any) {
	if s, ok := value.(string); ok && s != "" {
		c.Set(key, s)
	}
}
