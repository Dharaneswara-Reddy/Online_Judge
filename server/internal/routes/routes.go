// Package routes mounts all API route groups on the Gin engine.
// It wires controllers and middleware together, keeping the
// main.go file clean and focused on startup logic only.
package routes

import (
	"log"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"github.com/toji339/online-judge/internal/config"
	"github.com/toji339/online-judge/internal/controllers"
	"github.com/toji339/online-judge/internal/judge"
	"github.com/toji339/online-judge/internal/middleware"
	"github.com/toji339/online-judge/internal/problem"
	"github.com/toji339/online-judge/internal/problem/mongorepo"
	"github.com/toji339/online-judge/internal/submission"
	submissionmongo "github.com/toji339/online-judge/internal/submission/mongorepo"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

// Setup creates the Gin engine, configures CORS and all route
// groups, and returns the engine ready to run.
//
// It accepts the MongoDB database reference and app config so
// it can inject them into controllers without using globals.
func Setup(db *mongo.Database, cfg *config.Config) *gin.Engine {
	// 1. Create a new Gin engine with default middleware (logger, recovery)
	router := gin.Default()

	// 2. Configure CORS to allow the React frontend to make requests
	router.Use(cors.New(cors.Config{
		AllowOrigins:     []string{cfg.ClientURL},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Content-Type", "Authorization"},
		AllowCredentials: true,
	}))

	// 3. Create controller instances with their dependencies
	authController := controllers.NewAuthController(db, cfg.JWTSecret)

	// 4. Mount the auth routes
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

	// 5. Mount the judge (code execution) routes
	var sandbox judge.Sandbox
	sandbox, err := judge.NewDockerSandbox()
	if err != nil {
		log.Printf("WARNING: Docker sandbox unavailable: %v (code execution disabled)", err)
	} else {
		judgeController := controllers.NewJudgeController(sandbox)
		judgeGroup := router.Group("/api/judge")
		judgeGroup.Use(middleware.AuthMiddleware(cfg.JWTSecret))
		{
			judgeGroup.POST("/run", judgeController.RunCode)
			judgeGroup.POST("/run-raw", judgeController.RunRaw)
		}
	}

	// 6. Mount problem routes
	problemRepo := mongorepo.New(db)
	problemSvc := problem.NewService(problemRepo)
	problemController := controllers.NewProblemController(problemSvc)

	// Public problem routes — no auth required
	publicProblems := router.Group("/api/problems")
	{
		publicProblems.GET("", problemController.ListProblems)
		publicProblems.GET("/:slug", problemController.GetProblem)
	}

	// Admin-only problem routes — require auth + admin role
	adminProblems := router.Group("/api/problems")
	adminProblems.Use(middleware.AuthMiddleware(cfg.JWTSecret), middleware.AdminOnly())
	{
		adminProblems.POST("", problemController.CreateProblem)
		adminProblems.PUT("/:id", problemController.UpdateProblem)
		adminProblems.POST("/:id/testcases", problemController.AddTestCase)
		adminProblems.GET("/:id/testcases", problemController.ListTestCases)
	}

	// 7. Mount submission routes (authenticated, synchronous for now)
	submissionRepo := submissionmongo.New(db)
	submissionSvc := submission.NewService(submissionRepo)

	if sandbox != nil {
		submissionController := controllers.NewSubmissionController(problemSvc, submissionSvc, sandbox)

		submitGroup := router.Group("/api/problems")
		submitGroup.Use(middleware.AuthMiddleware(cfg.JWTSecret))
		{
			submitGroup.POST("/:slug/submit", submissionController.Submit)
		}

		submissionsGroup := router.Group("/api/submissions")
		submissionsGroup.Use(middleware.AuthMiddleware(cfg.JWTSecret))
		{
			submissionsGroup.GET("/:id", submissionController.GetSubmission)
		}

		historyGroup := router.Group("/api/users/me")
		historyGroup.Use(middleware.AuthMiddleware(cfg.JWTSecret))
		{
			historyGroup.GET("/submissions", submissionController.ListMySubmissions)
		}
	}

	// 8. Mount profile routes
	userController := controllers.NewUserController(db, submissionSvc, problemSvc)
	users := router.Group("/api/users")
	users.Use(middleware.AuthMiddleware(cfg.JWTSecret))
	{
		users.GET("/me", userController.GetProfile)
		users.PATCH("/me", userController.UpdateProfile)
		users.GET("/me/stats", userController.GetStats)
	}

	// 9. Return the configured router
	return router
}
