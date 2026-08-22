package discussion

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Page size bounds. A request may ask for fewer, never more: the point of
// pagination is that one request cannot pull an unbounded thread.
const (
	DefaultPageSize = 20
	MaxPageSize     = 100

	// MaxRepliesPerComment caps how many replies one comment contributes
	// to a page. Roots are paginated but replies are not, so without a cap
	// a single popular comment decides how much memory the request costs:
	// a thread with 50k replies would be read whole into a process capped
	// at 112 MB. The cap is per comment rather than per page so one busy
	// thread cannot use up the whole budget and leave its neighbours
	// showing nothing.
	MaxRepliesPerComment = 20
)

// ErrInvalidCursor is returned when a cursor cannot be decoded. It is a
// client error, not a server one — a malformed cursor is almost always a
// hand-edited URL.
var ErrInvalidCursor = errors.New("invalid pagination cursor")

// Cursor marks the position of the last item on a page.
//
// It carries both the timestamp and the id because timestamps are not
// unique: two comments posted in the same millisecond would otherwise
// make the boundary ambiguous, and a page could repeat or skip one. The
// pair (created_at, _id) is unique and totally ordered, which is what
// makes paging stable even while new comments arrive.
type Cursor struct {
	CreatedAt time.Time
	ID        string
}

// Encode renders a cursor as an opaque string for the client.
//
// It is deliberately opaque rather than, say, a raw ObjectID: clients
// should treat it as a token to hand back, not as a document identifier
// they can construct or reason about.
func (c Cursor) Encode() string {
	raw := fmt.Sprintf("%d|%s", c.CreatedAt.UTC().UnixNano(), c.ID)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// DecodeCursor parses a cursor produced by Encode. An empty string means
// "start at the beginning" and is not an error.
func DecodeCursor(encoded string) (*Cursor, error) {
	if encoded == "" {
		return nil, nil
	}

	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, ErrInvalidCursor
	}

	nanos, id, found := strings.Cut(string(decoded), "|")
	if !found || id == "" {
		return nil, ErrInvalidCursor
	}

	parsed, err := strconv.ParseInt(nanos, 10, 64)
	if err != nil {
		return nil, ErrInvalidCursor
	}

	return &Cursor{CreatedAt: time.Unix(0, parsed).UTC(), ID: id}, nil
}

// ClampPageSize keeps a requested page size inside the allowed range.
// Zero or negative means "unspecified", which takes the default.
func ClampPageSize(requested int) int {
	if requested <= 0 {
		return DefaultPageSize
	}
	if requested > MaxPageSize {
		return MaxPageSize
	}
	return requested
}

// Page is one page of threads plus what the client needs to ask for the
// next one.
type Page struct {
	Threads    []Comment `json:"data"`
	NextCursor string    `json:"nextCursor,omitempty"`
	HasMore    bool      `json:"hasMore"`
}
