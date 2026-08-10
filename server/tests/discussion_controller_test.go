package tests

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/toji339/online-judge/internal/companytag"
	companytagmongo "github.com/toji339/online-judge/internal/companytag/mongorepo"
	"github.com/toji339/online-judge/internal/controllers"
	"github.com/toji339/online-judge/internal/discussion"
	discussionmongo "github.com/toji339/online-judge/internal/discussion/mongorepo"
	"github.com/toji339/online-judge/internal/problem"
	problemmongo "github.com/toji339/online-judge/internal/problem/mongorepo"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// communityHarness mounts the discussion and company tag routes with a
// switchable identity so one test can act as several users.
type communityHarness struct {
	router  *gin.Engine
	problem *problem.Problem

	userID   string
	username string
	role     string
}

func setupCommunityRouter(t *testing.T) *communityHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)

	problemSvc := problem.NewService(problemmongo.New(testDB))
	discussionSvc := discussion.NewService(discussionmongo.New(testDB))
	companyRepo := companytagmongo.New(testDB)
	companySvc := companytag.NewService(companyRepo)

	h := &communityHarness{role: "user"}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		if h.userID != "" {
			c.Set("userID", h.userID)
			c.Set("username", h.username)
			c.Set("role", h.role)
		}
		c.Next()
	})

	discussionController := controllers.NewDiscussionController(discussionSvc, problemSvc)
	companyController := controllers.NewCompanyController(companySvc, problemSvc, companyRepo.ProblemsForCompany)

	router.GET("/api/problems/:slug/discussions", discussionController.ListForProblem)
	router.POST("/api/problems/:slug/discussions", discussionController.Create)
	router.POST("/api/discussions/:id/upvote", discussionController.Upvote)
	router.DELETE("/api/discussions/:id/upvote", discussionController.RemoveUpvote)
	router.DELETE("/api/discussions/:id", discussionController.Delete)
	router.POST("/api/problems/:slug/company-tags", companyController.TagProblem)
	router.GET("/api/problems/:slug/company-tags", companyController.ListForProblem)
	router.GET("/api/companies", companyController.ListCompanies)
	router.GET("/api/companies/:name/problems", companyController.ProblemsForCompany)

	h.router = router
	h.problem = seedProblem(t, problemSvc)
	return h
}

func (h *communityHarness) as(username string) {
	h.userID = bson.NewObjectID().Hex()
	h.username = username
	h.role = "user"
}

func (h *communityHarness) anonymous() {
	h.userID = ""
	h.username = ""
}

func (h *communityHarness) do(method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	return w
}

func clearCommunity(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	for _, name := range []string{"discussions", "problem_company_tags"} {
		_, err := testDB.Collection(name).DeleteMany(ctx, bson.M{})
		require.NoError(t, err)
	}
	clearSubmissions(t)
}

// postComment posts a top-level comment and returns its ID.
func (h *communityHarness) postComment(t *testing.T, content string) string {
	t.Helper()
	w := h.do(http.MethodPost, "/api/problems/"+h.problem.Slug+"/discussions",
		`{"content":"`+content+`"}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	var resp struct {
		Data discussion.Comment `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp.Data.ID
}

// =============================================================
// Discussions
// =============================================================

func TestDiscussion_PostReplyAndRead(t *testing.T) {
	clearCommunity(t)
	h := setupCommunityRouter(t)

	h.as("alice")
	parentID := h.postComment(t, "Why is this O(n)?")

	h.as("bob")
	w := h.do(http.MethodPost, "/api/problems/"+h.problem.Slug+"/discussions",
		`{"content":"Each element is visited once.","parentId":"`+parentID+`"}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	w = h.do(http.MethodGet, "/api/problems/"+h.problem.Slug+"/discussions", "")
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Data []discussion.Comment `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Data, 1, "the reply is nested, not a second thread")
	assert.Equal(t, "alice", resp.Data[0].Username)
	require.Len(t, resp.Data[0].Replies, 1)
	assert.Equal(t, "bob", resp.Data[0].Replies[0].Username)
}

func TestDiscussion_RejectsEmptyContent(t *testing.T) {
	clearCommunity(t)
	h := setupCommunityRouter(t)
	h.as("alice")

	w := h.do(http.MethodPost, "/api/problems/"+h.problem.Slug+"/discussions", `{"content":"   "}`)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDiscussion_UnknownProblemReturns404(t *testing.T) {
	clearCommunity(t)
	h := setupCommunityRouter(t)
	h.as("alice")

	w := h.do(http.MethodPost, "/api/problems/no-such-problem/discussions", `{"content":"Hello there."}`)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestDiscussion_UpvoteIsIdempotentAcrossRequests(t *testing.T) {
	clearCommunity(t)
	h := setupCommunityRouter(t)

	h.as("alice")
	id := h.postComment(t, "A genuinely useful hint.")

	h.as("bob")
	first := h.do(http.MethodPost, "/api/discussions/"+id+"/upvote", "")
	second := h.do(http.MethodPost, "/api/discussions/"+id+"/upvote", "")

	require.Equal(t, http.StatusOK, first.Code)
	require.Equal(t, http.StatusOK, second.Code)

	var resp struct {
		Data struct {
			Upvotes int `json:"upvotes"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(second.Body.Bytes(), &resp))
	assert.Equal(t, 1, resp.Data.Upvotes, "one user cannot vote twice")

	// And withdrawing takes it back to zero.
	w := h.do(http.MethodDelete, "/api/discussions/"+id+"/upvote", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 0, resp.Data.Upvotes)
}

func TestDiscussion_DeletingSomeoneElsesCommentIsForbidden(t *testing.T) {
	clearCommunity(t)
	h := setupCommunityRouter(t)

	h.as("alice")
	id := h.postComment(t, "My own comment.")

	h.as("mallory")
	w := h.do(http.MethodDelete, "/api/discussions/"+id, "")

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestDiscussion_AnonymousReadersSeeTheThread(t *testing.T) {
	clearCommunity(t)
	h := setupCommunityRouter(t)

	h.as("alice")
	id := h.postComment(t, "A public hint.")
	h.as("bob")
	require.Equal(t, http.StatusOK, h.do(http.MethodPost, "/api/discussions/"+id+"/upvote", "").Code)

	h.anonymous()
	w := h.do(http.MethodGet, "/api/problems/"+h.problem.Slug+"/discussions", "")

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Data []discussion.Comment `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Data, 1)
	assert.Equal(t, 1, resp.Data[0].Upvotes)
	assert.False(t, resp.Data[0].UpvotedByMe, "an anonymous reader has voted on nothing")
}

// =============================================================
// Company tags
// =============================================================

func TestCompanyTag_AggregatesAcrossUsersAndSpellings(t *testing.T) {
	clearCommunity(t)
	h := setupCommunityRouter(t)
	path := "/api/problems/" + h.problem.Slug + "/company-tags"

	h.as("alice")
	require.Equal(t, http.StatusCreated, h.do(http.MethodPost, path, `{"company":"  google ","round":"OA"}`).Code)

	h.as("bob")
	require.Equal(t, http.StatusCreated, h.do(http.MethodPost, path, `{"company":"GOOGLE","round":"onsite"}`).Code)

	w := h.do(http.MethodGet, path, "")
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Data []companytag.CompanyCount `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Data, 1, "different spellings collapse into one company")
	assert.Equal(t, "Google", resp.Data[0].Company)
	assert.Equal(t, 2, resp.Data[0].TagCount)
}

// TestCompanyTag_SameUserCannotTagTwice is the vote-stuffing guard.
func TestCompanyTag_SameUserCannotTagTwice(t *testing.T) {
	clearCommunity(t)
	h := setupCommunityRouter(t)
	path := "/api/problems/" + h.problem.Slug + "/company-tags"

	h.as("alice")
	require.Equal(t, http.StatusCreated, h.do(http.MethodPost, path, `{"company":"Google"}`).Code)

	w := h.do(http.MethodPost, path, `{"company":"google"}`)

	assert.Equal(t, http.StatusConflict, w.Code, "a normalised duplicate is rejected")
}

func TestCompanyTag_RejectsUnknownRound(t *testing.T) {
	clearCommunity(t)
	h := setupCommunityRouter(t)
	h.as("alice")

	w := h.do(http.MethodPost, "/api/problems/"+h.problem.Slug+"/company-tags",
		`{"company":"Google","round":"coffee chat"}`)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCompanyExplorer_ListsCompaniesAndTheirProblems(t *testing.T) {
	clearCommunity(t)
	h := setupCommunityRouter(t)

	h.as("alice")
	require.Equal(t, http.StatusCreated,
		h.do(http.MethodPost, "/api/problems/"+h.problem.Slug+"/company-tags", `{"company":"Google"}`).Code)

	w := h.do(http.MethodGet, "/api/companies", "")
	require.Equal(t, http.StatusOK, w.Code)
	var list struct {
		Data []companytag.CompanyCount `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &list))
	require.NotEmpty(t, list.Data)
	assert.Equal(t, "Google", list.Data[0].Company)
	assert.Equal(t, 1, list.Data[0].ProblemCount)

	// The explorer resolves a company to real problems, case-insensitively.
	w = h.do(http.MethodGet, "/api/companies/google/problems", "")
	require.Equal(t, http.StatusOK, w.Code)
	var problems struct {
		Company string            `json:"company"`
		Data    []problem.Problem `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &problems))
	assert.Equal(t, "Google", problems.Company)
	require.Len(t, problems.Data, 1)
	assert.Equal(t, h.problem.Slug, problems.Data[0].Slug)
}
