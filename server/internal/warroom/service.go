package warroom

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"time"

	"github.com/toji339/online-judge/internal/problem"
)

// Room lifetimes. A room nobody joins, or a race nobody finishes, is
// swept up rather than lingering in the lobby forever.
const (
	WaitingRoomTTL  = 15 * time.Minute
	InProgressTTL   = 2 * time.Hour
	minParticipants = 2
	maxParticipants = 3
)

// roomCodeAlphabet omits characters that are easy to misread aloud or in
// a screenshot (0/O, 1/I) because room codes get shared between players.
const roomCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

const roomCodeLength = 6

// Service holds the war room business rules.
type Service struct {
	repo     Repository
	problems *problem.Service
}

// NewService creates a Service. The problem service is used to pick and
// validate the problem a room races on.
func NewService(repo Repository, problems *problem.Service) *Service {
	return &Service{repo: repo, problems: problems}
}

// CreateInput describes a new room.
type CreateInput struct {
	// ProblemSlug is optional — leaving it empty picks a random problem.
	ProblemSlug     string
	MaxParticipants int
	Creator         Participant
}

// Create opens a new room and puts its creator in it.
func (s *Service) Create(ctx context.Context, input CreateInput) (*Room, error) {
	// Steps to follow while creating a war room
	// ===========================================

	// 1. Validate the requested size
	if input.MaxParticipants < minParticipants || input.MaxParticipants > maxParticipants {
		return nil, ValidationError{
			Field:   "maxParticipants",
			Message: fmt.Sprintf("must be %d or %d", minParticipants, maxParticipants),
		}
	}
	if input.Creator.UserID == "" {
		return nil, ValidationError{Field: "creator", Message: "is required"}
	}

	// 2. Resolve the problem to race on
	prob, err := s.pickProblem(ctx, input.ProblemSlug)
	if err != nil {
		return nil, err
	}

	// 3. Generate a shareable code that is not already in use
	code, err := s.uniqueRoomCode(ctx)
	if err != nil {
		return nil, err
	}

	// 4. Store the room with its creator already inside
	creator := input.Creator
	creator.JoinedAt = time.Now().UTC()

	room := &Room{
		RoomCode:        code,
		ProblemID:       prob.ID,
		ProblemSlug:     prob.Slug,
		ProblemTitle:    prob.Title,
		Difficulty:      string(prob.Difficulty),
		Participants:    []Participant{creator},
		MaxParticipants: input.MaxParticipants,
		Status:          StatusWaiting,
		CreatedAt:       time.Now().UTC(),
	}
	if err := s.repo.Create(ctx, room); err != nil {
		return nil, err
	}
	return room, nil
}

// pickProblem returns the requested problem, or a random one when no
// slug was given.
func (s *Service) pickProblem(ctx context.Context, slug string) (*problem.Problem, error) {
	if slug != "" {
		prob, err := s.problems.GetBySlug(ctx, slug)
		if err != nil {
			return nil, err
		}
		return prob, nil
	}

	// A random pick keeps on-demand races varied without needing a
	// curated pool. Page size is capped so this stays a small read.
	candidates, err := s.problems.List(ctx, problem.ListFilter{Page: 1, PageSize: 50})
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, ValidationError{Field: "problemSlug", Message: "no problems are available to race on"}
	}

	index, err := randomInt(len(candidates))
	if err != nil {
		return nil, err
	}
	return &candidates[index], nil
}

// Join adds a player to a waiting room, starting the race once the room
// is full. It returns the updated room and whether this join started it.
func (s *Service) Join(ctx context.Context, code string, p Participant) (*Room, bool, error) {
	// Steps to follow while joining a war room
	// ==========================================

	// 1. Find the room
	room, err := s.repo.GetByCode(ctx, code)
	if err != nil {
		return nil, false, err
	}

	// 2. Rejoining is a no-op, so a refreshed page does not fail
	if room.HasParticipant(p.UserID) {
		return room, false, nil
	}

	// 3. Refuse rooms that are closed or already full
	if room.Status != StatusWaiting {
		return nil, false, ErrRoomClosed
	}
	if room.IsFull() {
		return nil, false, ErrRoomFull
	}

	// 4. Append conditionally — the database settles any race between two
	//    players joining the last seat at the same moment
	p.JoinedAt = time.Now().UTC()
	becameFull, err := s.repo.AddParticipant(ctx, room.ID, p)
	if err != nil {
		return nil, false, err
	}

	// 5. A full room starts immediately
	started := false
	if becameFull {
		if err := s.repo.Start(ctx, room.ID, time.Now().UTC()); err != nil {
			return nil, false, err
		}
		started = true
	}

	updated, err := s.repo.GetByID(ctx, room.ID)
	if err != nil {
		return nil, false, err
	}
	return updated, started, nil
}

// GetByCode returns one room by its shareable code.
func (s *Service) GetByCode(ctx context.Context, code string) (*Room, error) {
	return s.repo.GetByCode(ctx, code)
}

// GetByID returns one room by its identifier.
func (s *Service) GetByID(ctx context.Context, id string) (*Room, error) {
	return s.repo.GetByID(ctx, id)
}

// ListOpen returns rooms still waiting for players, sweeping away stale
// ones first so the lobby never advertises a dead room.
func (s *Service) ListOpen(ctx context.Context, limit int) ([]Room, error) {
	if _, err := s.ExpireStale(ctx); err != nil {
		// Sweeping is opportunistic; a failure must not break the lobby.
		return s.repo.ListByStatus(ctx, StatusWaiting, limit)
	}
	return s.repo.ListByStatus(ctx, StatusWaiting, limit)
}

// ListForUser returns the rooms a user has taken part in, newest first.
func (s *Service) ListForUser(ctx context.Context, userID string, limit int) ([]Room, error) {
	return s.repo.ListForUser(ctx, userID, limit)
}

// DeclareWinner records the first accepted submission as the winner and
// closes the room.
//
// It returns false when the room already had a winner, which is how a
// second finisher is distinguished from the first. Callers should only
// broadcast a result when it returns true.
func (s *Service) DeclareWinner(ctx context.Context, roomID, userID, username string, judgedAt time.Time) (bool, error) {
	if roomID == "" || userID == "" {
		return false, ValidationError{Field: "winner", Message: "room and user are required"}
	}
	// judgedAt comes from the judge worker's own stamp on the submission,
	// never from anything a client sent.
	return s.repo.DeclareWinner(ctx, roomID, userID, username, judgedAt)
}

// EnsureParticipant checks a user belongs to a room before letting them
// act on it, and that the race is actually running.
func (s *Service) EnsureParticipant(ctx context.Context, code, userID string) (*Room, error) {
	room, err := s.repo.GetByCode(ctx, code)
	if err != nil {
		return nil, err
	}
	if !room.HasParticipant(userID) {
		return nil, ErrNotParticipant
	}
	return room, nil
}

// ExpireStale sweeps abandoned rooms and reports how many were closed.
func (s *Service) ExpireStale(ctx context.Context) (int, error) {
	now := time.Now().UTC()
	return s.repo.ExpireStale(ctx, now.Add(-WaitingRoomTTL), now.Add(-InProgressTTL))
}

// uniqueRoomCode generates a code no live room is using.
func (s *Service) uniqueRoomCode(ctx context.Context) (string, error) {
	// A handful of attempts is plenty: the code space is 31^6, so a
	// collision is already unlikely on the first try.
	for range 5 {
		code, err := generateRoomCode()
		if err != nil {
			return "", err
		}
		_, err = s.repo.GetByCode(ctx, code)
		if err == ErrNotFound {
			return code, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("could not generate a unique room code")
}

// generateRoomCode builds a short, shareable, unambiguous code.
func generateRoomCode() (string, error) {
	code := make([]byte, roomCodeLength)
	for i := range code {
		index, err := randomInt(len(roomCodeAlphabet))
		if err != nil {
			return "", err
		}
		code[i] = roomCodeAlphabet[index]
	}
	return string(code), nil
}

// randomInt returns a uniform value in [0, n) from a cryptographic
// source, so room codes cannot be predicted and joined uninvited.
func randomInt(n int) (int, error) {
	value, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0, fmt.Errorf("generate random number: %w", err)
	}
	return int(value.Int64()), nil
}
