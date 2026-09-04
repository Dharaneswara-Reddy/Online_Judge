// Package config loads environment variables and provides
// them to the rest of the application as a typed struct.
// It reads from a .env file using godotenv and panics if
// any required variable is missing — we want to fail fast
// on startup rather than discover a missing config at runtime.
package config

import (
	"fmt"
	"log"
	neturl "net/url"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds all the environment variables the application needs.
// Each field maps to one variable in the .env file.
type Config struct {
	MongoURI    string // MongoDB Atlas connection string
	DBName      string // Database name on the Atlas cluster
	JWTSecret   string // Secret key used to sign and verify JWT tokens
	Port        string // Port the HTTP server listens on
	ClientURL   string // Frontend origin for CORS (e.g. http://localhost:5173)
	RabbitMQURL string // AMQP URL for the submission queue
	RedisURL    string // Redis URL for caching, rate limits and pub/sub
	WorkerCount int    // Queue prefetch: deliveries a worker takes at once

	// MaxSandboxes caps how many judge containers may exist on the host
	// at the same time, across every consumer in the process.
	//
	// It is deliberately a separate setting from WorkerCount. A worker
	// runs three independent consumers — the standard lane, the War Room
	// lane and the playground responder — and each used to size itself
	// from WorkerCount alone, so the real container ceiling was three
	// times the number anyone configured. A container may claim a full
	// vCPU and up to 512MB; on the 916MB production host that arithmetic
	// ended at the OOM killer, twice.
	//
	// Prefetch and container count answer different questions, so they
	// get different knobs. The default is one because the memory budget
	// in deploy/docker-compose.prod.yml has room for exactly one.
	MaxSandboxes int

	// SecureCookies marks the session cookie Secure, so the browser only
	// ever sends it over HTTPS. It defaults to true: the insecure setting
	// is the one that has to be asked for, and only local development
	// over plain HTTP should ask.
	SecureCookies bool

	// AssistEnabled is the kill switch for the AI assist features — the
	// hint ladder, verdict explanations and post-accept reviews. It is
	// separate from the API key on purpose: turning the feature off
	// during an incident, or for a cohort sitting an assessment, should
	// not mean deleting a credential and restarting with it missing.
	AssistEnabled bool

	// AnthropicAPIKey authenticates the assist provider. Empty is the
	// default and a supported mode: with no key the assist service
	// reports itself disabled, the endpoints answer 503, and the client
	// hides the feature. Nothing else in the API changes.
	AnthropicAPIKey string

	// AssistModel names the model used for assist completions. Empty
	// means the assist package's own default.
	AssistModel string
}

// minJWTSecretLength is the shortest signing key we accept. HS256 with a
// low-entropy key can be brute-forced offline from a single captured
// token, and recovering it means an attacker can mint admin tokens.
const minJWTSecretLength = 32

// Load reads the .env file and returns a populated Config struct.
// It panics if any required variable is missing — this ensures
// the server never starts in a half-configured state.
func Load() *Config {
	// Steps to follow while loading the config
	// ==========================================

	// 1. Load the .env file into the environment
	err := godotenv.Load()
	if err != nil {
		log.Println("Warning: .env file not found, using system environment variables")
	}

	// 2. Read each required variable and fail fast if missing
	config := &Config{
		MongoURI:  getEnvOrPanic("MONGO_URI"),
		DBName:    getEnvOrDefault("DB_NAME", "online_judge"),
		JWTSecret: getEnvOrPanic("JWT_SECRET"),
		Port:      getEnvOrDefault("PORT", "8080"),
		ClientURL: getEnvOrDefault("CLIENT_URL", "http://localhost:5173"),
		// Queue and cache default to the local docker-compose services.
		// They are not required: the API degrades to synchronous judging
		// and an uncached read path when they are unreachable.
		RabbitMQURL:  rabbitMQURL(),
		RedisURL:     getEnvOrDefault("REDIS_URL", "redis://localhost:6379/0"),
		WorkerCount:  getEnvAsIntOrDefault("WORKER_COUNT", 4),
		MaxSandboxes: getEnvAsIntOrDefault("MAX_SANDBOXES", 1),

		// Default from the frontend's scheme rather than a flat true: a
		// Secure cookie is dropped by the browser over plain HTTP, which
		// would silently break local development. Deployments serve the
		// client over HTTPS and so get the secure setting automatically,
		// and SECURE_COOKIES overrides either way.
		SecureCookies: getEnvAsBoolOrDefault("SECURE_COOKIES",
			strings.HasPrefix(getEnvOrDefault("CLIENT_URL", "http://localhost:5173"), "https://")),

		// The assist feature defaults to on so that setting a key is the
		// only step needed to enable it, and defaults to keyless so that
		// doing nothing leaves the API exactly as it was.
		AssistEnabled:   getEnvAsBoolOrDefault("ASSIST_ENABLED", true),
		AnthropicAPIKey: getEnvOrDefault("ANTHROPIC_API_KEY", ""),
		AssistModel:     getEnvOrDefault("ASSIST_MODEL", ""),
	}

	// 3. Refuse to start on a signing key weak enough to brute-force
	if len(config.JWTSecret) < minJWTSecretLength {
		log.Fatalf("FATAL: JWT_SECRET must be at least %d characters (got %d). "+
			"Generate one with: openssl rand -base64 48", minJWTSecretLength, len(config.JWTSecret))
	}

	// 4. Return the validated config
	return config
}

// rabbitMQURL resolves the broker address.
//
// RABBITMQ_URL stays the primary setting — it is what a managed broker
// hands you. The component variables exist for environments that supply
// host, credentials and vhost separately (Compose, Kubernetes secrets),
// and are only consulted when no full URL is given, so the two can never
// disagree.
func rabbitMQURL() string {
	if url := os.Getenv("RABBITMQ_URL"); url != "" {
		return url
	}

	user := getEnvOrDefault("RABBITMQ_USER", "guest")
	password := getEnvOrDefault("RABBITMQ_PASSWORD", "guest")
	host := getEnvOrDefault("RABBITMQ_HOST", "localhost")
	port := getEnvOrDefault("RABBITMQ_PORT", "5672")
	vhost := getEnvOrDefault("RABBITMQ_VHOST", "/")

	// Credentials and the vhost are escaped: a password containing "@"
	// or "/" would otherwise silently produce a different address.
	return fmt.Sprintf("amqp://%s:%s@%s:%s/%s",
		neturl.QueryEscape(user),
		neturl.QueryEscape(password),
		host, port,
		neturl.PathEscape(strings.TrimPrefix(vhost, "/")),
	)
}

// getEnvAsBoolOrDefault reads a boolean environment variable. Anything
// unparseable falls back to the default rather than silently reading as
// false, which for a security flag would be the dangerous direction.
func getEnvAsBoolOrDefault(key string, defaultValue bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		log.Printf("Warning: %s=%q is not a boolean, using %t", key, value, defaultValue)
		return defaultValue
	}
	return parsed
}

// getEnvOrPanic reads an environment variable by key.
// If the variable is not set or is empty, it panics
// with a descriptive message so the developer knows
// exactly which variable is missing.
func getEnvOrPanic(key string) string {
	value := os.Getenv(key)
	if value == "" {
		log.Fatalf("FATAL: Required environment variable %s is not set", key)
	}
	return value
}

// getEnvAsIntOrDefault reads an environment variable and parses it as an
// integer, falling back to the default when unset or unparseable.
func getEnvAsIntOrDefault(key string, defaultValue int) int {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		log.Printf("Warning: %s=%q is not a positive integer, using %d", key, value, defaultValue)
		return defaultValue
	}
	return parsed
}

// getEnvOrDefault reads an environment variable by key.
// If the variable is not set, it returns the provided
// default value instead of failing.
func getEnvOrDefault(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}
