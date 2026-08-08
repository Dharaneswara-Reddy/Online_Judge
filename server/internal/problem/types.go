package problem

import "time"

type Difficulty string

const (
	DifficultyEasy   Difficulty = "easy"
	DifficultyMedium Difficulty = "medium"
	DifficultyHard   Difficulty = "hard"
)

type Problem struct {
	ID            string              `bson:"_id,omitempty" json:"id"`
	Title         string              `bson:"title" json:"title"`
	Slug          string              `bson:"slug" json:"slug"`
	Statement     string              `bson:"statement" json:"statement"`
	Difficulty    Difficulty          `bson:"difficulty" json:"difficulty"`
	Tags          []string            `bson:"tags" json:"tags"`
	CompanyTags   []CompanyTagSummary `bson:"company_tags" json:"companyTags"`
	TimeLimitMS   int                 `bson:"time_limit_ms" json:"timeLimitMs"`
	MemoryLimitMB int                 `bson:"memory_limit_mb" json:"memoryLimitMb"`
	StarterCode   map[string]string   `bson:"starter_code,omitempty" json:"starterCode,omitempty"`
	CreatedAt     time.Time           `bson:"created_at" json:"createdAt"`
}

type CompanyTagSummary struct {
	Company string `bson:"company" json:"company"`
	Count   int    `bson:"count" json:"count"`
}

type TestCase struct {
	ID             string `bson:"_id,omitempty" json:"id"`
	ProblemID      string `bson:"problem_id" json:"problemId"`
	Input          string `bson:"input" json:"input"`
	ExpectedOutput string `bson:"expected_output" json:"expectedOutput"`
	IsSample       bool   `bson:"is_sample" json:"isSample"`
}
