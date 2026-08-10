package companytag_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/toji339/online-judge/internal/companytag"
	"github.com/toji339/online-judge/internal/companytag/companytagtest"
)

func newService() (*companytag.Service, *companytagtest.FakeRepository) {
	repo := companytagtest.New()
	return companytag.NewService(repo), repo
}

// --- Normalisation ---

func TestNormalizeCompany_CollapsesEquivalentSpellings(t *testing.T) {
	cases := map[string]string{
		"google":         "Google",
		"  GOOGLE  ":     "Google",
		"gOoGlE":         "Google",
		"jane  street":   "Jane Street",
		"JANE STREET":    "Jane Street",
		"  Amazon\t":     "Amazon",
		"":               "",
		"   ":            "",
		"goldman  sachs": "Goldman Sachs",
	}

	for input, want := range cases {
		assert.Equal(t, want, companytag.NormalizeCompany(input), "input %q", input)
	}
}

func TestValidate_RejectsBadInput(t *testing.T) {
	_, _, err := companytag.Validate("", "")
	var vErr companytag.ValidationError
	assert.ErrorAs(t, err, &vErr, "an empty company is rejected")

	_, _, err = companytag.Validate("Google", "coffee chat")
	assert.ErrorAs(t, err, &vErr, "an unknown interview round is rejected")

	company, round, err := companytag.Validate("google", "ONSITE")
	require.NoError(t, err)
	assert.Equal(t, "Google", company)
	assert.Equal(t, "onsite", round, "rounds are stored lower-case")
}

func TestValidate_AllowsAnUnspecifiedRound(t *testing.T) {
	_, round, err := companytag.Validate("Google", "")

	require.NoError(t, err)
	assert.Empty(t, round, "remembering the company but not the stage is fine")
}

// --- Tagging ---

func TestTag_RecordsTheReportAndBumpsTheSummary(t *testing.T) {
	svc, repo := newService()

	tag, err := svc.Tag(context.Background(), companytag.TagInput{
		ProblemID: "problem-1", UserID: "user-1", Company: "  google ", Round: "OA",
	})

	require.NoError(t, err)
	assert.Equal(t, "Google", tag.Company, "the stored company is normalised")
	assert.Equal(t, "oa", tag.Round)

	summary, err := repo.ListForProblem(context.Background(), "problem-1")
	require.NoError(t, err)
	require.Len(t, summary, 1)
	assert.Equal(t, "Google", summary[0].Company)
	assert.Equal(t, 1, summary[0].TagCount)
}

// TestTag_SameUserCannotInflateOneCompanysCount is the whole point of
// the unique (problem, user, company) constraint.
func TestTag_SameUserCannotInflateOneCompanysCount(t *testing.T) {
	svc, repo := newService()
	ctx := context.Background()
	_, err := svc.Tag(ctx, companytag.TagInput{ProblemID: "problem-1", UserID: "user-1", Company: "Google"})
	require.NoError(t, err)

	// A second report of the same company — including a differently
	// spelled one, since names are normalised first — is rejected.
	_, err = svc.Tag(ctx, companytag.TagInput{ProblemID: "problem-1", UserID: "user-1", Company: "google"})

	assert.ErrorIs(t, err, companytag.ErrAlreadyTagged)

	summary, err := repo.ListForProblem(ctx, "problem-1")
	require.NoError(t, err)
	require.Len(t, summary, 1)
	assert.Equal(t, 1, summary[0].TagCount, "the count did not move")
}

func TestTag_DifferentUsersAccumulate(t *testing.T) {
	svc, repo := newService()
	ctx := context.Background()

	for _, userID := range []string{"user-1", "user-2", "user-3"} {
		_, err := svc.Tag(ctx, companytag.TagInput{ProblemID: "problem-1", UserID: userID, Company: "Google"})
		require.NoError(t, err)
	}

	summary, err := repo.ListForProblem(ctx, "problem-1")
	require.NoError(t, err)
	require.Len(t, summary, 1)
	assert.Equal(t, 3, summary[0].TagCount)
}

func TestTag_OneUserMayReportSeveralCompanies(t *testing.T) {
	svc, _ := newService()
	ctx := context.Background()

	_, err := svc.Tag(ctx, companytag.TagInput{ProblemID: "problem-1", UserID: "user-1", Company: "Google"})
	require.NoError(t, err)
	_, err = svc.Tag(ctx, companytag.TagInput{ProblemID: "problem-1", UserID: "user-1", Company: "Amazon"})

	require.NoError(t, err, "seeing one problem at two companies is legitimate")

	tags, err := svc.ListUserTags(ctx, "problem-1", "user-1")
	require.NoError(t, err)
	assert.Len(t, tags, 2)
}

func TestTag_RejectsMissingIdentifiers(t *testing.T) {
	svc, _ := newService()
	var vErr companytag.ValidationError

	_, err := svc.Tag(context.Background(), companytag.TagInput{UserID: "user-1", Company: "Google"})
	assert.ErrorAs(t, err, &vErr)

	_, err = svc.Tag(context.Background(), companytag.TagInput{ProblemID: "problem-1", Company: "Google"})
	assert.ErrorAs(t, err, &vErr)
}

// --- Explorer ---

func TestListCompanies_RanksByPopularity(t *testing.T) {
	svc, _ := newService()
	ctx := context.Background()

	for _, userID := range []string{"user-1", "user-2", "user-3"} {
		_, err := svc.Tag(ctx, companytag.TagInput{ProblemID: "problem-1", UserID: userID, Company: "Google"})
		require.NoError(t, err)
	}
	_, err := svc.Tag(ctx, companytag.TagInput{ProblemID: "problem-2", UserID: "user-1", Company: "Amazon"})
	require.NoError(t, err)

	companies, err := svc.ListCompanies(ctx, 10)

	require.NoError(t, err)
	require.Len(t, companies, 2)
	assert.Equal(t, "Google", companies[0].Company, "the most-tagged company leads")
	assert.Equal(t, 3, companies[0].TagCount)
	assert.Equal(t, "Amazon", companies[1].Company)
}

func TestListUserTags_AnonymousViewerHasNone(t *testing.T) {
	svc, _ := newService()

	tags, err := svc.ListUserTags(context.Background(), "problem-1", "")

	require.NoError(t, err)
	assert.Empty(t, tags)
}
