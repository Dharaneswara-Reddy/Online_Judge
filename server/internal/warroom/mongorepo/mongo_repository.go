// Package mongorepo provides the production MongoDB-backed
// implementation of warroom.Repository.
package mongorepo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/toji339/online-judge/internal/warroom"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// MongoRepository implements warroom.Repository on the "war_rooms"
// collection.
type MongoRepository struct {
	rooms *mongo.Collection
}

// New creates a MongoRepository backed by the given database.
func New(db *mongo.Database) *MongoRepository {
	return &MongoRepository{rooms: db.Collection("war_rooms")}
}

func (r *MongoRepository) Create(ctx context.Context, room *warroom.Room) error {
	result, err := r.rooms.InsertOne(ctx, room)
	if err != nil {
		return fmt.Errorf("insert war room: %w", err)
	}
	room.ID = result.InsertedID.(bson.ObjectID).Hex()
	return nil
}

func (r *MongoRepository) GetByID(ctx context.Context, id string) (*warroom.Room, error) {
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return nil, warroom.ErrNotFound
	}
	return r.findOne(ctx, bson.M{"_id": oid})
}

func (r *MongoRepository) GetByCode(ctx context.Context, code string) (*warroom.Room, error) {
	return r.findOne(ctx, bson.M{"room_code": code})
}

func (r *MongoRepository) findOne(ctx context.Context, filter bson.M) (*warroom.Room, error) {
	var room warroom.Room
	err := r.rooms.FindOne(ctx, filter).Decode(&room)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, warroom.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find war room: %w", err)
	}
	return &room, nil
}

func (r *MongoRepository) ListByStatus(ctx context.Context, status warroom.Status, limit int) ([]warroom.Room, error) {
	return r.list(ctx, bson.M{"status": status}, limit)
}

func (r *MongoRepository) ListForUser(ctx context.Context, userID string, limit int) ([]warroom.Room, error) {
	return r.list(ctx, bson.M{"participants.user_id": userID}, limit)
}

func (r *MongoRepository) list(ctx context.Context, filter bson.M, limit int) ([]warroom.Room, error) {
	if limit <= 0 {
		limit = 20
	}
	opts := options.Find().
		SetSort(bson.D{{Key: "created_at", Value: -1}}).
		SetLimit(int64(limit))

	cursor, err := r.rooms.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("list war rooms: %w", err)
	}
	defer cursor.Close(ctx)

	var rooms []warroom.Room
	if err := cursor.All(ctx, &rooms); err != nil {
		return nil, fmt.Errorf("decode war rooms: %w", err)
	}
	if rooms == nil {
		rooms = []warroom.Room{}
	}
	return rooms, nil
}

// AddParticipant appends a player in a single conditional update.
//
// The filter carries the preconditions — the room is still waiting, the
// user is not already in it, and it is below capacity — so two players
// racing for the last seat are settled by the database rather than by a
// read-then-write in application code, where both could read "one seat
// free" before either wrote.
func (r *MongoRepository) AddParticipant(ctx context.Context, roomID string, p warroom.Participant) (bool, error) {
	oid, err := bson.ObjectIDFromHex(roomID)
	if err != nil {
		return false, warroom.ErrNotFound
	}

	// Read the cap first so the size precondition can be expressed. The
	// cap itself never changes after creation, so this is safe to reuse.
	room, err := r.GetByID(ctx, roomID)
	if err != nil {
		return false, err
	}

	filter := bson.M{
		"_id":                  oid,
		"status":               warroom.StatusWaiting,
		"participants.user_id": bson.M{"$ne": p.UserID},
		// "the participants array does not already have max entries"
		fmt.Sprintf("participants.%d", room.MaxParticipants-1): bson.M{"$exists": false},
	}

	// FindOneAndUpdate returning the post-image, rather than an update
	// followed by a read.
	//
	// becameFull used to come from a separate GetByID issued after the
	// push. Two players taking the last seats at the same moment both read
	// the final, full room, so both were told they had filled it and
	// room_started was broadcast once per racing joiner. Reading the
	// document the push itself produced makes exactly one caller observe
	// the transition, because only one push can be the one that reaches
	// the cap.
	var updated warroom.Room
	err = r.rooms.FindOneAndUpdate(ctx, filter,
		bson.M{"$push": bson.M{"participants": p}},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&updated)
	if errors.Is(err, mongo.ErrNoDocuments) {
		// Work out which precondition failed, for a useful error message.
		current, getErr := r.GetByID(ctx, roomID)
		if getErr != nil {
			return false, getErr
		}
		if current.HasParticipant(p.UserID) {
			return false, nil
		}
		if current.Status != warroom.StatusWaiting {
			return false, warroom.ErrRoomClosed
		}
		return false, warroom.ErrRoomFull
	}

	if err != nil {
		return false, fmt.Errorf("add participant: %w", err)
	}
	return updated.IsFull(), nil
}

func (r *MongoRepository) Start(ctx context.Context, roomID string, at time.Time) error {
	oid, err := bson.ObjectIDFromHex(roomID)
	if err != nil {
		return warroom.ErrNotFound
	}

	// Only a waiting room may start, so a retry cannot restart a race and
	// reset its clock.
	result, err := r.rooms.UpdateOne(ctx,
		bson.M{"_id": oid, "status": warroom.StatusWaiting},
		bson.M{"$set": bson.M{"status": warroom.StatusInProgress, "started_at": at}},
	)
	if err != nil {
		return fmt.Errorf("start war room: %w", err)
	}
	if result.MatchedCount == 0 {
		return nil // already started — nothing to do
	}
	return nil
}

// DeclareWinner records the winner only when the room has none yet.
//
// The "winner_id does not exist" precondition makes this a compare-and-set:
// whichever accepted submission reaches the database first wins, and every
// later one matches nothing and reports false.
func (r *MongoRepository) DeclareWinner(ctx context.Context, roomID, winnerID, winnerUsername string, solvedAt time.Time) (bool, error) {
	oid, err := bson.ObjectIDFromHex(roomID)
	if err != nil {
		return false, warroom.ErrNotFound
	}

	// The win goes to the earliest solve, not the earliest write.
	//
	// This used to filter only on "no winner yet", so two verdicts landing
	// close together raced on Mongo write scheduling — and the entrant who
	// genuinely finished first could lose because their worker's update
	// arrived a few milliseconds later. The timestamp was accepted as an
	// argument and then stored as ended_at without ever being compared.
	//
	// Now a later write carrying an earlier solvedAt corrects a
	// provisional winner, and a later solve can never displace an earlier
	// one. Both cases are decided by this single conditional update, so
	// concurrent declarations settle without a read-then-write.
	//
	// The status precondition is the other half. Without it a verdict
	// arriving after ExpireStale had closed a room flipped it back to
	// finished and gave the room a winner. Only a room that is running —
	// or already finished, so an earlier solve can still correct it — may
	// be won; waiting and expired rooms may not.
	filter := bson.M{
		"_id":    oid,
		"status": bson.M{"$in": []warroom.Status{warroom.StatusInProgress, warroom.StatusFinished}},
		"$or": []bson.M{
			{"winner_id": bson.M{"$exists": false}},
			{"winner_id": ""},
			{"winner_solved_at": bson.M{"$gt": solvedAt}},
		},
	}
	update := bson.M{"$set": bson.M{
		"winner_id":        winnerID,
		"winner_username":  winnerUsername,
		"winner_solved_at": solvedAt,
		"status":           warroom.StatusFinished,
		"ended_at":         solvedAt,
	}}

	result, err := r.rooms.UpdateOne(ctx, filter, update)
	if err != nil {
		return false, fmt.Errorf("declare winner: %w", err)
	}
	return result.MatchedCount > 0, nil
}

func (r *MongoRepository) ExpireStale(ctx context.Context, waitingBefore, startedBefore time.Time) (int, error) {
	filter := bson.M{"$or": []bson.M{
		{"status": warroom.StatusWaiting, "created_at": bson.M{"$lt": waitingBefore}},
		{"status": warroom.StatusInProgress, "started_at": bson.M{"$lt": startedBefore}},
	}}

	result, err := r.rooms.UpdateMany(ctx, filter, bson.M{"$set": bson.M{"status": warroom.StatusExpired}})
	if err != nil {
		return 0, fmt.Errorf("expire stale war rooms: %w", err)
	}
	return int(result.ModifiedCount), nil
}
