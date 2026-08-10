// Package routes mounts all API route groups on the Gin engine.
// It wires controllers and middleware together, keeping the
// main.go file clean and focused on startup logic only.
package routes

import (
	"log"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"github.com/toji339/online-judge/internal/companytag"
	companytagmongo "github.com/toji339/online-judge/internal/companytag/mongorepo"
	"github.com/toji339/online-judge/internal/config"
	"github.com/toji339/online-judge/internal/controllers"
	"github.com/toji339/online-judge/internal/discussion"
	discussionmongo "github.com/toji339/online-judge/internal/discussion/mongorepo"
	"github.com/toji339/online-judge/internal/judge"
	"github.com/toji339/online-judge/internal/middleware"
	"github.com/toji339/online-judge/internal/problem"
	"github.com/toji339/online-judge/internal/problem/mongorepo"
	"github.com/toji339/online-judge/internal/queue"
	"github.com/toji339/online-judge/internal/ratelimit"
	"github.com/toji339/online-judge/internal/realtime"
	"github.com/toji339/online-judge/internal/submission"
	submissionmongo "github.com/toji339/online-judge/internal/submission/mongorepo"
	"github.com/toji339/online-judge/internal/warroom"
	warroommongo "github.com/toji339/online-judge/internal/warroom/mongorepo"
	"github.com/toji339/online-judge/internal/worker"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

// Deps holds the external services the API needs. Any of them may be nil,
// and the router degrades accordingly rather than refusing to start:
//
//   - no Publisher: submissions are judged inline by the API process
//   - no Sandbox:   code execution and inline judging are unavailable
//   - no Bus:       War Room live updates fall back to in-process only
//   - no Limiter:   rate limits are not enforced
type Deps struct {
	Publisher queue.Publisher
	Bus       realtime.Bus
	Limiter   ratelimit.Limiter
}

// withDefaults fills in safe no-op stand-ins so the rest of Setup never
// has to nil-check an optional dependency.
func (d Deps) withDefaults() Deps {
	if d.Bus == nil {
		// In-process fan-out still works for a single API instance.
		d.Bus = realtime.NewMemoryBus()
	}
	if d.Limiter == nil {
		d.Limiter = ratelimit.AllowAll{}
	}
	return d
}

// Setup creates the Gin engine, configures CORS and all route
// groups, and returns the engine ready to run.
//
// It accepts the MongoDB database reference, app config, and external
// dependencies so it can inject them into controllers without globals.
func Setup(db *mongo.Database, cfg *config.Config, deps Deps) *gin.Engine {
	deps = deps.withDefaults()

	// 1. Create a new Gin engine with default middleware (logger, recovery)
	router := gin.Default()

	// 2. Configure CORS to allow the React frontend to make requests
	router.Use(cors.New(cors.Config{
		AllowOrigins:     []string{cfg.ClientURL},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Content-Type", "Authorization"},
		AllowCredentials: true,
	}))

	// 3. Create shared services
	problemSvc := problem.NewService(mongorepo.New(db))
	submissionSvc := submission.NewService(submissionmongo.New(db))
	warRoomSvc := warroom.NewService(warroommongo.New(db), problemSvc)

	// 4. Mount the auth routes
	authController := controllers.NewAuthController(db, cfg.JWTSecret)
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

	// 5. Mount the judge (code execution) routes. The playground runs code
	//    in-process, so it needs a sandbox on the API host; queued
	//    submissions do not, since judge workers own their own sandbox.
	var sandbox judge.Sandbox
	sandbox, err := judge.NewDockerSandbox()
	if err != nil {
		log.Printf("WARNING: Docker sandbox unavailable: %v (playground disabled)", err)
		sandbox = nil
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
	problemController := controllers.NewProblemController(problemSvc)

	// Public problem routes — no auth required
	publicProblems := router.Group("/api/problems")
	{
		publicProblems.GET("", problemController.ListProblems)
		publicProblems.GET("/:slug", problemController.GetProblem)
	}

	// Admin-only problem routes — require auth + admin role.
	//
	// These live under /api/admin rather than /api/problems for two
	// reasons: it is the layout the design document specifies, and Gin
	// cannot register both /api/problems/:slug and /api/problems/:id
	// because two different wildcard names cannot share a position.
	adminProblems := router.Group("/api/admin/problems")
	adminProblems.Use(middleware.AuthMiddleware(cfg.JWTSecret), middleware.AdminOnly())
	{
		adminProblems.POST("", problemController.CreateProblem)
		adminProblems.PUT("/:id", problemController.UpdateProblem)
		adminProblems.POST("/:id/testcases", problemController.AddTestCase)
		adminProblems.GET("/:id/testcases", problemController.ListTestCases)
	}

	// 7. Mount submission routes. Submissions are queued for judge workers
	//    when a broker is available, and judged inline otherwise.
	var inlineProcessor *worker.Processor
	if sandbox != nil {
		inlineProcessor = worker.NewProcessor(submissionSvc, problemSvc, sandbox, nil)
	}
	if deps.Publisher == nil && inlineProcessor == nil {
		log.Println("WARNING: no queue and no sandbox — submissions cannot be judged")
	}

	submissionController := controllers.NewSubmissionController(problemSvc, submissionSvc, deps.Publisher, inlineProcessor, warRoomSvc)

	submitGroup := router.Group("/api/problems")
	submitGroup.Use(middleware.AuthMiddleware(cfg.JWTSecret))
	{
		submitGroup.POST("/:slug/submit", submissionController.Submit)
	}

	submissions := router.Group("/api/submissions")
	submissions.Use(middleware.AuthMiddleware(cfg.JWTSecret))
	{
		submissions.GET("/:id", submissionController.GetSubmission)
	}

	// 8. Mount profile routes
	userController := controllers.NewUserController(db, submissionSvc, problemSvc)
	users := router.Group("/api/users")
	users.Use(middleware.AuthMiddleware(cfg.JWTSecret))
	{
		users.GET("/me", userController.GetProfile)
		users.PATCH("/me", userController.UpdateProfile)
		users.GET("/me/stats", userController.GetStats)
		users.GET("/me/submissions", submissionController.ListMySubmissions)
	}

	// 9. Mount War Room routes. The WebSocket sits outside /api because it
	//    is not a REST endpoint, matching the design document's /ws prefix.
	warRoomController := controllers.NewWarRoomController(warRoomSvc, deps.Bus, cfg.ClientURL)

	warRooms := router.Group("/api/warrooms")
	warRooms.Use(middleware.AuthMiddleware(cfg.JWTSecret))
	{
		warRooms.GET("", warRoomController.ListRooms)
		warRooms.GET("/mine", warRoomController.ListMyRooms)
		warRooms.GET("/:code", warRoomController.GetRoom)
		// Creating a room is rate limited: rooms are cheap to make and a
		// flood of them would clutter the lobby for everyone.
		warRooms.POST("", middleware.RateLimit(deps.Limiter, "warroom-create", 10, time.Minute), warRoomController.CreateRoom)
		warRooms.POST("/:code/join", warRoomController.JoinRoom)
		warRooms.POST("/:code/submit", submissionController.SubmitToWarRoom)
	}

	router.GET("/ws/warroom/:code", middleware.AuthMiddleware(cfg.JWTSecret), warRoomController.Live)

	// 10. Mount discussion routes. Reading is public; writing needs an
	//     account and is rate limited, which is the spam control the
	//     design document calls for.
	discussionSvc := discussion.NewService(discussionmongo.New(db))
	discussionController := controllers.NewDiscussionController(discussionSvc, problemSvc)

	// OptionalAuth lets a signed-in reader see their own votes marked
	// while still serving anonymous readers.
	publicProblems.GET("/:slug/discussions",
		middleware.OptionalAuth(cfg.JWTSecret), discussionController.ListForProblem)

	discussionWrites := router.Group("/api")
	discussionWrites.Use(middleware.AuthMiddleware(cfg.JWTSecret))
	{
		discussionWrites.POST("/problems/:slug/discussions",
			middleware.RateLimit(deps.Limiter, "discussion-post", 5, time.Minute),
			discussionController.Create)
		discussionWrites.POST("/discussions/:id/upvote", discussionController.Upvote)
		discussionWrites.DELETE("/discussions/:id/upvote", discussionController.RemoveUpvote)
		discussionWrites.DELETE("/discussions/:id", discussionController.Delete)
	}

	// 11. Mount company tag routes and the company explorer
	companyRepo := companytagmongo.New(db)
	companyController := controllers.NewCompanyController(
		companytag.NewService(companyRepo), problemSvc, companyRepo.ProblemsForCompany)

	publicProblems.GET("/:slug/company-tags",
		middleware.OptionalAuth(cfg.JWTSecret), companyController.ListForProblem)

	companies := router.Group("/api/companies")
	{
		companies.GET("", companyController.ListCompanies)
		companies.GET("/:name/problems", companyController.ProblemsForCompany)
	}

	tagWrites := router.Group("/api/problems")
	tagWrites.Use(middleware.AuthMiddleware(cfg.JWTSecret))
	{
		tagWrites.POST("/:slug/company-tags",
			middleware.RateLimit(deps.Limiter, "company-tag", 10, time.Minute),
			companyController.TagProblem)
	}

	// 12. Mount the public landing-page endpoints
	statsController := controllers.NewStatsController(db, problemSvc, submissionSvc, warRoomSvc)
	router.GET("/api/stats/summary", statsController.Summary)
	publicProblems.GET("/recent", statsController.RecentProblems)

	// 13. Return the configured router
	return router
}
