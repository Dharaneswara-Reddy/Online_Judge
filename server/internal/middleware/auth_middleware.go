// Package middleware provides HTTP middleware functions for the
// Gin router. Middleware runs before the route handler and can
// modify the request context, reject unauthorized requests, or
// add logging/metrics.
package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// AuthMiddleware returns a Gin middleware function that validates
// the JWT token stored in the HTTP-only "token" cookie.
//
// If the cookie is missing or the token is invalid/expired, the
// middleware rejects the request with a 401 Unauthorized response.
//
// If the token is valid, the middleware extracts the user claims
// (userID, username, role) and sets them on the Gin context so
// downstream handlers can access them via c.Get("userID"), etc.
func AuthMiddleware(jwtSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Steps to follow while validating the auth token
		// =================================================

		// 1. Read the "token" cookie from the request
		tokenString, err := c.Cookie("token")
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Authentication required — no token provided",
			})
			c.Abort()
			return
		}

		// 2. Parse and validate the JWT token using the secret key
		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			// Ensure the signing method is HMAC (HS256)
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return []byte(jwtSecret), nil
		})

		if err != nil || !token.Valid {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Invalid or expired token",
			})
			c.Abort()
			return
		}

		// 3. Extract claims from the validated token
		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Invalid token claims",
			})
			c.Abort()
			return
		}

		// 4. Set user information on the Gin context for downstream handlers
		//    Controllers can access these with c.Get("userID"), c.Get("username"), etc.
		c.Set("userID", claims["sub"].(string))
		c.Set("username", claims["username"].(string))
		c.Set("role", claims["role"].(string))

		// 5. Continue to the next handler in the chain
		c.Next()
	}
}
