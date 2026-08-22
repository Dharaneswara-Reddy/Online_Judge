package controllers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/toji339/online-judge/internal/models"
	"github.com/toji339/online-judge/internal/problem"
	"github.com/toji339/online-judge/internal/submission"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// UserController serves the profile page: the caller's own account
// details and their solved/submission statistics.
type UserController struct {
	db            *mongo.Database
	submissionSvc *submission.Service
	problemSvc    *problem.Service
}

// NewUserController creates a UserController with its dependencies.
func NewUserController(db *mongo.Database, submissionSvc *submission.Service, problemSvc *problem.Service) *UserController {
	return &UserController{db: db, submissionSvc: submissionSvc, problemSvc: problemSvc}
}

// GetProfile handles GET /api/users/me (authenticated).
// It returns the account details together with solved-problem stats.
func (uc *UserController) GetProfile(c *gin.Context) {
	// Steps to follow while building a profile
	// ==========================================

	// 1. Resolve the authenticated user
	userID, err := bson.ObjectIDFromHex(c.GetString("userID"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Invalid user ID in token"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	// 2. Load the account document
	user, err := models.FindUserByID(ctx, uc.db, userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "User not found"})
		return
	}

	// 3. Load the judging record and break solves down by difficulty
	stats, err := uc.buildStats(ctx, c.GetString("userID"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to load statistics"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Profile retrieved",
		"data":    gin.H{"user": user, "stats": stats},
	})
}

// UpdateProfile handles PATCH /api/users/me (authenticated), letting a
// user edit their own display name and date of birth.
func (uc *UserController) UpdateProfile(c *gin.Context) {
	userID, err := bson.ObjectIDFromHex(c.GetString("userID"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Invalid user ID in token"})
		return
	}

	var update models.ProfileUpdate
	if err := c.ShouldBindJSON(&update); err != nil {
		writeBindError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	user, err := models.UpdateUserProfile(ctx, uc.db, userID, &update)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Profile updated", "data": gin.H{"user": user}})
}

// GetStats handles GET /api/users/me/stats (authenticated).
func (uc *UserController) GetStats(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	stats, err := uc.buildStats(ctx, c.GetString("userID"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to load statistics"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": stats})
}

// profileStats is the shape the profile page consumes.
type profileStats struct {
	TotalSubmissions   int            `json:"totalSubmissions"`
	Accepted           int            `json:"accepted"`
	Solved             int            `json:"solved"`
	SolvedByDifficulty map[string]int `json:"solvedByDifficulty"`
	TotalByDifficulty  map[string]int `json:"totalByDifficulty"`
}

// buildStats combines the submission record with problem difficulties.
// The two domains stay decoupled: the submission service reports which
// problems were solved, and the problem service classifies them.
func (uc *UserController) buildStats(ctx context.Context, userID string) (profileStats, error) {
	raw, err := uc.submissionSvc.Stats(ctx, userID)
	if err != nil {
		return profileStats{}, err
	}

	byDifficulty, err := uc.problemSvc.SolvedCountsByDifficulty(ctx, raw.SolvedProblemIDs)
	if err != nil {
		return profileStats{}, err
	}

	solved := make(map[string]int, len(byDifficulty))
	for difficulty, count := range byDifficulty {
		solved[string(difficulty)] = count
	}

	// Totals let the UI render "12 / 40 easy solved" style progress.
	totals := make(map[string]int, len(byDifficulty))
	for _, difficulty := range []problem.Difficulty{problem.DifficultyEasy, problem.DifficultyMedium, problem.DifficultyHard} {
		count, err := uc.problemSvc.Count(ctx, problem.ListFilter{Difficulty: string(difficulty)})
		if err != nil {
			return profileStats{}, err
		}
		totals[string(difficulty)] = count
	}

	return profileStats{
		TotalSubmissions:   raw.TotalSubmissions,
		Accepted:           raw.Accepted,
		Solved:             len(raw.SolvedProblemIDs),
		SolvedByDifficulty: solved,
		TotalByDifficulty:  totals,
	}, nil
}
