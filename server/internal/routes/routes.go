// Package routes mounts all API route groups on the Gin engine.
// It wires controllers and middleware together, keeping the
// main.go file clean and focused on startup logic only.
package routes

import (
	"context"
	"log"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"github.com/toji339/online-judge/internal/assist"
	"github.com/toji339/online-judge/internal/companytag"
	companytagmongo "github.com/toji339/online-judge/internal/companytag/mongorepo"
	"github.com/toji339/online-judge/internal/config"
	"github.com/toji339/online-judge/internal/controllers"
	"github.com/toji339/online-judge/internal/discussion"
	discussionmongo "github.com/toji339/online-judge/internal/discussion/mongorepo"
	"github.com/toji339/online-judge/internal/judge"
	"github.com/toji339/online-judge/internal/middleware"
	"github.com/toji339/online-judge/internal/playground"
	"github.com/toji339/online-judge/internal/problem"
	"github.com/toji339/online-judge/internal/problem/mongorepo"
	"github.com/toji339/online-judge/internal/queue"
	"github.com/toji339/online-judge/internal/ratelimit"
	"github.com/toji339/online-judge/internal/realtime"
	"github.com/toji339/online-judge/internal/session"
	"github.com/toji339/online-judge/internal/submission"
	submissionmongo "github.com/toji339/online-judge/internal/submission/mongorepo"
	"github.com/toji339/online-judge/internal/warroom"
	warroommongo "github.com/toji339/online-judge/internal/warroom/mongorepo"
	"github.com/toji339/online-judge/internal/worker"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

// maxRequestBodyBytes caps any single request body. The largest
// legitimate payload is a 64KB submission, so 1MB is generous.
const maxRequestBodyBytes = 1 << 20

// Deps holds the external services the API needs. Any of them may be nil,
// and the router degrades accordingly rather than refusing to start:
//
//   - no Publisher: submissions are judged inline by the API process
//   - no Sandbox:   code execution and inline judging are unavailable
//   - no Bus:       War Room live updates fall back to in-process only
//   - no Limiter:   rate limits are not enforced (this is the documented
//     "Redis is optional" mode, and is distinct from a configured
//     limiter that is failing — see internal/ratelimit)
type Deps struct {
	Publisher queue.Publisher
	Bus       realtime.Bus
	Limiter   ratelimit.Limiter

	// Caller runs playground code on a judge worker. It is only needed
	// when this process cannot reach Docker itself; with neither, the
	// playground is disabled and everything else still works.
	Caller queue.Caller

	// Sessions holds revocation state, so logout and "log out everywhere"
	// can end a token that is still signed and unexpired. Nil means
	// revocation is not configured and authentication behaves as before.
	Sessions session.Store

	// BrokerProbe reports whether the queue is reachable, for readiness.
	// It is separate from Publisher because readiness must never publish,
	// and nil simply means "no queue configured".
	BrokerProbe controllers.Pinger
}

// mongoPinger adapts a Mongo database to the health controller's Pinger.
//
// The ping goes to the database handle the API actually uses, so it fails
// when the API would fail rather than testing some other connection.
type mongoPinger struct{ db *mongo.Database }

func (p mongoPinger) Ping(ctx context.Context) error {
	return p.db.Client().Ping(ctx, nil)
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

	// 2. Apply global hardening before anything else runs.
	//
	//    First decide whose forwarding headers we believe, because
	//    everything that reads c.ClientIP() depends on it. Gin trusts
	//    every peer by default, which let any caller pick its own
	//    X-Forwarded-For and therefore its own rate-limit bucket. See
	//    middleware.ApplyTrustedProxies for the reasoning.
	if err := middleware.ApplyTrustedProxies(router); err != nil {
		log.Fatalf("FATAL: could not configure trusted proxies: %v", err)
	}

	//    Then a body cap so an oversized request is refused before it is
	//    buffered, and the response headers that govern how browsers
	//    treat our output.
	router.Use(middleware.MaxBodySize(maxRequestBodyBytes))
	router.Use(middleware.SecurityHeaders(cfg.SecureCookies))

	// 3. Configure CORS to allow the React frontend to make requests
	router.Use(cors.New(cors.Config{
		AllowOrigins:     []string{cfg.ClientURL},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Content-Type", "Authorization"},
		AllowCredentials: true,
	}))

	// 3a. Health probes.
	//
	//     Registered before everything else and outside every group, so
	//     they carry no auth, no rate limiting and no CORS-sensitive
	//     handling. They live at the root rather than under /api because
	//     they describe the process, not the product API — and because a
	//     container healthcheck should not have to travel through the
	//     application's routing conventions to ask whether the process is
	//     up. The Docker healthcheck used to hit /api/problems, which ran
	//     a Mongo query every few seconds forever.
	health := controllers.NewHealthController(mongoPinger{db}, deps.BrokerProbe)
	router.GET("/healthz", health.Live)
	router.GET("/readyz", health.Ready)

	// 3. Create shared services
	problemSvc := problem.NewService(mongorepo.New(db))
	submissionSvc := submission.NewService(submissionmongo.New(db))
	warRoomSvc := warroom.NewService(warroommongo.New(db), problemSvc)

	// 4. Mount the auth routes
	authController := controllers.NewAuthController(db, cfg.JWTSecret, cfg.SecureCookies, deps.Sessions)
	auth := router.Group("/api/auth")
	{
		// Keyed by address, not by user: there is no authenticated user
		// yet, so the per-user limiter would let every attempt through and
		// password guessing would be unthrottled.
		//
		// RateLimitAuthByIP rather than RateLimitByIP: here the limit is
		// the security control, so a broken counter refuses the request
		// instead of quietly waving it through. Running with no Redis at
		// all is still supported and still unthrottled — see the doc
		// comment on RateLimitAuthByIP.
		auth.POST("/register",
			middleware.RateLimitAuthByIP(deps.Limiter, "auth-register", 5, time.Hour),
			authController.Register)
		auth.POST("/login",
			middleware.RateLimitAuthByIP(deps.Limiter, "auth-login", 10, 15*time.Minute),
			authController.Login)
		auth.POST("/logout", authController.Logout)

		// Protected group — all routes below require authentication
		protected := auth.Group("")
		protected.Use(middleware.AuthMiddleware(cfg.JWTSecret, deps.Sessions))
		{
			protected.GET("/me", authController.GetMe)
		}
	}

	// 5. Mount the judge (code execution) routes.
	//
	//    The playground needs a sandbox, but not necessarily one on this
	//    host. In production the API container is given no access to the
	//    Docker daemon on purpose — it is the only internet-facing
	//    process, and the daemon socket is equivalent to host root — so it
	//    delegates the run to a judge worker over the broker. When this
	//    process *can* reach Docker (development, or a single-process
	//    deployment) it runs the code itself and no broker is involved.
	var sandbox judge.Sandbox
	var runner playground.Runner

	sandbox, err := judge.NewDockerSandbox()
	if err != nil {
		log.Printf("Docker sandbox unavailable in this process: %v", err)
		sandbox = nil
	} else {
		runner = playground.NewLocalRunner(sandbox)
		log.Println("Playground: running code in-process (Docker reachable)")
	}
	if runner == nil && deps.Caller != nil {
		runner = playground.NewRemoteRunner(deps.Caller)
		log.Println("Playground: delegating runs to a judge worker over the queue")
	}

	if runner == nil {
		log.Println("WARNING: no sandbox and no broker — playground disabled")
	} else {
		judgeController := controllers.NewJudgeController(runner)
		judgeGroup := router.Group("/api/judge")
		// Every request here starts a container somewhere, so it needs a
		// throttle of its own — submissions get theirs from per-user
		// admission control, but the playground bypasses that.
		judgeGroup.Use(
			middleware.AuthMiddleware(cfg.JWTSecret, deps.Sessions),
			middleware.RateLimit(deps.Limiter, "judge-run", 20, time.Minute),
		)
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
	adminProblems.Use(middleware.AuthMiddleware(cfg.JWTSecret, deps.Sessions), middleware.AdminOnly())
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
	submitGroup.Use(middleware.AuthMiddleware(cfg.JWTSecret, deps.Sessions))
	{
		submitGroup.POST("/:slug/submit", submissionController.Submit)
	}

	submissions := router.Group("/api/submissions")
	submissions.Use(middleware.AuthMiddleware(cfg.JWTSecret, deps.Sessions))
	{
		submissions.GET("/:id", submissionController.GetSubmission)
	}

	// 8. Mount profile routes
	userController := controllers.NewUserController(db, submissionSvc, problemSvc)
	users := router.Group("/api/users")
	users.Use(middleware.AuthMiddleware(cfg.JWTSecret, deps.Sessions))
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
	warRooms.Use(middleware.AuthMiddleware(cfg.JWTSecret, deps.Sessions))
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

	router.GET("/ws/warroom/:code", middleware.AuthMiddleware(cfg.JWTSecret, deps.Sessions), warRoomController.Live)

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
	discussionWrites.Use(middleware.AuthMiddleware(cfg.JWTSecret, deps.Sessions))
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
	tagWrites.Use(middleware.AuthMiddleware(cfg.JWTSecret, deps.Sessions))
	{
		tagWrites.POST("/:slug/company-tags",
			middleware.RateLimit(deps.Limiter, "company-tag", 10, time.Minute),
			companyController.TagProblem)
	}

	// 12. Mount the public landing-page endpoints.
	//
	//     The summary is unauthenticated and expensive (four counts, one
	//     of them a full collection scan). It is cached inside the
	//     controller and throttled per address here, so an anonymous
	//     caller cannot turn a refresh loop into unbounded database work.
	//     The limit is generous — this is the first request a real
	//     visitor makes — and degrades open, because a Redis blip should
	//     not take down the landing page.
	statsController := controllers.NewStatsController(db, problemSvc, submissionSvc, warRoomSvc,
		controllers.DefaultStatsCacheTTL)
	router.GET("/api/stats/summary",
		middleware.RateLimitByIP(deps.Limiter, "stats-summary", 60, time.Minute),
		statsController.Summary)
	publicProblems.GET("/recent", statsController.RecentProblems)

	// 13. Mount the AI assist routes.
	//
	//     Assist is an optional dependency in exactly the sense Redis and
	//     the broker are: with no key configured the service reports
	//     itself disabled, the endpoints answer 503, and the client hides
	//     the feature. Nothing else in the API changes, and no other
	//     feature may come to depend on it.
	//
	//     Every route here costs a model call, so each one is rate
	//     limited per user on top of the ownership checks in the
	//     controller. The limits differ because the calls do: hints are
	//     the interactive path, explanations are the cheapest and most
	//     cacheable, and a review is the most expensive generation the
	//     feature makes.
	assistSvc := newAssistService(cfg)
	assistController := controllers.NewAssistController(assistSvc, problemSvc, submissionSvc)

	publicProblems.GET("/:slug/assist/state",
		middleware.AuthMiddleware(cfg.JWTSecret, deps.Sessions), assistController.State)

	assistGroup := router.Group("/api/assist")
	assistGroup.Use(middleware.AuthMiddleware(cfg.JWTSecret, deps.Sessions))
	{
		assistGroup.POST("/hint",
			middleware.RateLimit(deps.Limiter, "assist-hint", 10, time.Minute), assistController.Hint)
		assistGroup.POST("/explain",
			middleware.RateLimit(deps.Limiter, "assist-explain", 15, time.Minute), assistController.Explain)
		assistGroup.POST("/review",
			middleware.RateLimit(deps.Limiter, "assist-review", 5, time.Minute), assistController.Review)
	}

	// 14. Return the configured router
	return router
}

// newAssistService builds the assist service from configuration.
//
// It always returns a usable *Service. Two separate settings can leave
// it disabled — a missing key and an explicit kill switch — and they are
// logged differently on purpose: "nobody configured this" and "somebody
// turned this off" call for opposite responses when a deployment is not
// behaving as expected.
func newAssistService(cfg *config.Config) *assist.Service {
	switch {
	case !cfg.AssistEnabled:
		log.Println("Assist: disabled by ASSIST_ENABLED")
		return assist.NewService(nil, assist.Options{})
	case cfg.AnthropicAPIKey == "":
		log.Println("Assist: no ANTHROPIC_API_KEY set — hints, explanations and reviews are off")
		return assist.NewService(nil, assist.Options{})
	}

	model := cfg.AssistModel
	if model == "" {
		model = assist.DefaultModel
	}
	log.Printf("Assist: enabled using model %s", model)

	return assist.NewService(
		assist.NewAnthropicProvider(cfg.AnthropicAPIKey, model, nil),
		assist.Options{Cache: assist.NewMemoryCache(assistCacheTTL, assistCacheEntries)},
	)
}

// Assist cache sizing. Fifteen minutes is long enough for a class
// working the same problem to share one generation, and short enough
// that an edited statement stops being described by a stale hint before
// anyone notices.
const (
	assistCacheTTL     = 15 * time.Minute
	assistCacheEntries = 1024
)
