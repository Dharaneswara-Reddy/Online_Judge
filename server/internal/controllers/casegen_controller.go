package controllers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/toji339/online-judge/internal/assist"
	"github.com/toji339/online-judge/internal/casegen"
	"github.com/toji339/online-judge/internal/judge"
	"github.com/toji339/online-judge/internal/problem"
)

// CaseGenController is the admin edge of the adversarial test-case
// generator.
//
// It exists because of one shipped defect: a Two Sum problem whose
// expected output admitted two correct answers, so the canonical
// solution was judged wrong. A human had written that expected output by
// hand and nothing checked it.
//
// Two properties make this endpoint safe to hand an admin, and both are
// established below rather than in the model:
//
//   - It writes nothing. The response is a list of proposals; a human
//     reads them and uses the existing AddTestCase route for the ones
//     they accept. There is no path from here to the database.
//   - The expected outputs are not the model's. They come from executing
//     the admin's own reference solution, which is the whole point —
//     see internal/casegen.
type CaseGenController struct {
	generator *casegen.Generator
	problems  *problem.Service
}

// NewCaseGenController wires the controller. A nil or disabled generator
// is legal: the endpoint then answers 503 and the admin UI hides the
// tool, the same degradation the rest of the assist surface gets.
func NewCaseGenController(g *casegen.Generator, problems *problem.Service) *CaseGenController {
	return &CaseGenController{generator: g, problems: problems}
}

// maxReferenceSolutionBytes mirrors the judge's own submission cap. A
// reference solution larger than any submission the judge would accept
// is not a reference solution.
const maxReferenceSolutionBytes = 64 * 1024

// GenerateTestCases handles POST /api/admin/problems/:id/assist/testcases
// (admin only).
func (cc *CaseGenController) GenerateTestCases(c *gin.Context) {
	// Steps to follow while generating candidate test cases
	// =====================================================

	// 1. Get the reference solution and the language from the body

	// 2. Refuse early if the feature is not configured on this deployment

	// 3. Validate the input: a solution is required, and it must be in a
	//    language this judge can actually execute

	// 4. Load the problem named in the path, and the cases it already has

	// 5. Ask the generator, which proposes inputs and then produces every
	//    expected output by running the reference solution

	// 6. Send the proposals back for a human to review — nothing is saved

	// 1. Get the reference solution and the language from the body.
	var body struct {
		ReferenceSolution string `json:"referenceSolution"`
		Language          string `json:"language"`
		Count             int    `json:"count"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request body"})
		return
	}

	// 2. Refuse early if the feature is not configured. This is checked
	//    before the problem is loaded so a deployment with no key answers
	//    identically for every problem id.
	if !cc.generator.Enabled() {
		respondToCaseGenFailure(c, assist.ErrDisabled)
		return
	}

	// 3. Validate the input.
	solution := strings.TrimSpace(body.ReferenceSolution)
	if solution == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "A reference solution is required — it is what produces the expected outputs.",
		})
		return
	}
	if len(body.ReferenceSolution) > maxReferenceSolutionBytes {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "That reference solution is too large to run.",
		})
		return
	}
	// Checked here rather than left to the sandbox: an unknown language
	// is a mistake in the request, and reporting it as a failed run per
	// proposal would bury it.
	if _, err := judge.GetLanguage(body.Language); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "That language is not supported by this judge.",
		})
		return
	}

	// 4. Load the problem and its existing cases. All of them, hidden
	//    ones included: this route is admin-only and an admin can
	//    already read them, and the model needs to see what exists in
	//    order not to propose it again.
	prob, err := cc.problems.GetByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Problem not found"})
		return
	}

	existing, err := cc.problems.ListAllTestCases(c.Request.Context(), prob.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to read the problem's test cases",
		})
		return
	}

	// 5. Ask the generator.
	result, err := cc.generator.Generate(c.Request.Context(), casegen.Request{
		Problem:           problemContext(prob),
		ExistingCases:     toCaseGenCases(existing),
		ReferenceSolution: body.ReferenceSolution,
		Language:          body.Language,
		Count:             body.Count,
	})
	if err != nil {
		respondToCaseGenFailure(c, err)
		return
	}

	// 6. Send the proposals back. Nothing has been written; the admin
	//    accepts what they want through the existing test-case route.
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Review these before adding any of them to the problem.",
		"data":    result,
	})
}

// toCaseGenCases reduces stored test cases to the pair the generator
// needs. The reduction is not decoration: casegen.Case has no id and no
// problem id, so a prompt built from this cannot carry database
// identifiers to a model.
func toCaseGenCases(cases []problem.TestCase) []casegen.Case {
	out := make([]casegen.Case, 0, len(cases))
	for _, tc := range cases {
		out = append(out, casegen.Case{Input: tc.Input, ExpectedOutput: tc.ExpectedOutput})
	}
	return out
}

// respondToCaseGenFailure maps a generator error onto a status.
//
// The distinction that matters is the same one the assist controller
// draws: the admin UI hides the tool on a 503 and only on a 503, so a
// generation that merely failed must not use it. A model that replied
// with prose is a bad gateway, not an absent feature.
func respondToCaseGenFailure(c *gin.Context, err error) {
	switch {
	case errors.Is(err, assist.ErrDisabled):
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "Test-case generation is not available on this deployment.",
		})
	case errors.Is(err, casegen.ErrBadResponse):
		c.JSON(http.StatusBadGateway, gin.H{
			"success": false,
			"message": "The model's reply could not be read as test cases, so it was discarded. Try again.",
		})
	default:
		c.JSON(http.StatusBadGateway, gin.H{
			"success": false,
			"message": "The generator could not be reached. Please try again in a moment.",
		})
	}
}
