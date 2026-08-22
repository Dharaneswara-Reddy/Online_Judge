package controllers

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/toji339/online-judge/internal/companytag"
	"github.com/toji339/online-judge/internal/problem"
)

// ProblemsForCompanyFunc resolves which problems carry a company tag.
// It is injected as a function so the controller depends on a capability
// rather than on a concrete storage type.
type ProblemsForCompanyFunc func(ctx context.Context, company string) ([]string, error)

// CompanyController serves the company tag widget on a problem page and
// the company explorer.
type CompanyController struct {
	tags      *companytag.Service
	problems  *problem.Service
	byCompany ProblemsForCompanyFunc
}

// NewCompanyController creates the controller with its dependencies.
func NewCompanyController(tags *companytag.Service, problems *problem.Service, problemsForCompany ProblemsForCompanyFunc) *CompanyController {
	return &CompanyController{tags: tags, problems: problems, byCompany: problemsForCompany}
}

// TagProblem handles POST /api/problems/:slug/company-tags (authenticated).
//
// This is the "have you seen this in an interview?" answer. A user may
// report several companies for one problem, but only once per company.
func (cc *CompanyController) TagProblem(c *gin.Context) {
	var body struct {
		Company string `json:"company" binding:"required"`
		Round   string `json:"round"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		writeBindError(c, err)
		return
	}

	prob, err := cc.problems.GetBySlug(c.Request.Context(), c.Param("slug"))
	if err != nil {
		writeProblemLookupError(c, err)
		return
	}

	tag, err := cc.tags.Tag(c.Request.Context(), companytag.TagInput{
		ProblemID: prob.ID,
		UserID:    c.GetString("userID"),
		Company:   body.Company,
		Round:     body.Round,
	})
	if err != nil {
		writeCompanyTagError(c, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{"success": true, "message": "Thanks for the tag", "data": tag})
}

// ListForProblem handles GET /api/problems/:slug/company-tags (public).
func (cc *CompanyController) ListForProblem(c *gin.Context) {
	prob, err := cc.problems.GetBySlug(c.Request.Context(), c.Param("slug"))
	if err != nil {
		writeProblemLookupError(c, err)
		return
	}

	companies, err := cc.tags.ListForProblem(c.Request.Context(), prob.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to load company tags"})
		return
	}

	// A signed-in user also sees which companies they already reported,
	// so the widget can stop asking them the same question.
	mine, err := cc.tags.ListUserTags(c.Request.Context(), prob.ID, c.GetString("userID"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to load your tags"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": companies, "myTags": mine})
}

// ListCompanies handles GET /api/companies (public), the explorer index.
func (cc *CompanyController) ListCompanies(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))

	companies, err := cc.tags.ListCompanies(c.Request.Context(), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to list companies"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": companies})
}

// ProblemsForCompany handles GET /api/companies/:name/problems (public).
func (cc *CompanyController) ProblemsForCompany(c *gin.Context) {
	company := companytag.NormalizeCompany(c.Param("name"))
	if company == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "A company name is required"})
		return
	}

	ids, err := cc.byCompany(c.Request.Context(), company)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to list problems"})
		return
	}

	problems, err := cc.problems.GetByIDs(c.Request.Context(), ids)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to load problems"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "company": company, "data": problems})
}

// writeCompanyTagError maps company tag errors to HTTP responses.
func writeCompanyTagError(c *gin.Context, err error) {
	var vErr companytag.ValidationError
	switch {
	case errors.As(err, &vErr):
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Validation failed", "errors": []string{vErr.Error()}})
	case errors.Is(err, companytag.ErrAlreadyTagged):
		c.JSON(http.StatusConflict, gin.H{"success": false, "message": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Company tag request failed"})
	}
}
