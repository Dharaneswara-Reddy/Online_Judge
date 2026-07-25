// Package routes mounts all API route groups on the Gin engine.
// It wires controllers and middleware together, keeping the
// main.go file clean and focused on startup logic only.
package routes

import (
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"github.com/toji339/online-judge/internal/config"
	"github.com/toji339/online-judge/internal/controllers"
	"github.com/toji339/online-judge/internal/middleware"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

// Setup creates the Gin engine, configures CORS and all route
// groups, and returns the engine ready to run.
//
// It accepts the MongoDB database reference and app config so
// it can inject them into controllers without using globals.
func Setup(db *mongo.Database, cfg *config.Config) *gin.Engine {
	// Steps to follow while setting up routes
	// =========================================

	// 1. Create a new Gin engine with default middleware (logger, recovery)
	router := gin.Default()

	// 2. Configure CORS to allow the React frontend to make requests
	//    - AllowOrigins: only the frontend URL (no wildcard)
	//    - AllowCredentials: true (so the browser sends cookies)
	//    - AllowMethods: the HTTP methods our API uses
	//    - AllowHeaders: Content-Type for JSON requests
	router.Use(cors.New(cors.Config{
		AllowOrigins:     []string{cfg.ClientURL},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Content-Type", "Authorization"},
		AllowCredentials: true,
	}))

	// 3. Create controller instances with their dependencies
	authController := controllers.NewAuthController(db, cfg.JWTSecret)

	// 4. Mount the auth routes
	//    Public routes: register, login, logout
	//    Protected routes: /me (requires valid JWT cookie)
	auth := router.Group("/api/auth")
	{
		auth.POST("/register", authController.Register)
		auth.POST("/login", authController.Login)
		auth.POST("/logout", authController.Logout)

		// Protected group — all routes below require authentication
		protected := auth.Group("")
		protected.Use(middleware.AuthMiddleware(cfg.JWTSecret))
		{
			protected.GET("/me", authController.GetMe)
		}
	}

	// 5. Return the configured router
	return router
}
