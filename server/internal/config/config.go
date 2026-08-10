// Package config loads environment variables and provides
// them to the rest of the application as a typed struct.
// It reads from a .env file using godotenv and panics if
// any required variable is missing — we want to fail fast
// on startup rather than discover a missing config at runtime.
package config

import (
	"log"
	"os"
	"strconv"

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
	WorkerCount int    // Concurrent judge slots per worker process
}

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
		RabbitMQURL: getEnvOrDefault("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"),
		RedisURL:    getEnvOrDefault("REDIS_URL", "redis://localhost:6379/0"),
		WorkerCount: getEnvAsIntOrDefault("WORKER_COUNT", 4),
	}

	// 3. Return the validated config
	return config
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
