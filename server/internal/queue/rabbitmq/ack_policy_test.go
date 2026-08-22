package rabbitmq

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

var boom = errors.New("boom")

// A judging failure used to be discarded outright, so a submission whose
// record could not even be read stayed pending forever — and the partial
// unique index meant its owner could never submit again.
func TestDecideAck(t *testing.T) {
	cases := []struct {
		name         string
		err          error
		shuttingDown bool
		redelivered  bool
		want         ackAction
	}{
		{
			name: "success is acknowledged",
			want: ackDone,
		},
		{
			name:         "shutdown always hands the job back",
			err:          boom,
			shuttingDown: true,
			want:         ackRequeue,
		},
		{
			name: "a first failure is retried once",
			err:  boom,
			want: ackRequeue,
		},
		{
			name:        "a failure that already came back is dropped",
			err:         boom,
			redelivered: true,
			want:        ackDiscard,
		},
		{
			name:        "a redelivered job that succeeds is still acknowledged",
			redelivered: true,
			want:        ackDone,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, decideAck(tc.err, tc.shuttingDown, tc.redelivered))
		})
	}
}

// The bound is what keeps a poison message from looping forever: the
// broker sets Redelivered on the second delivery, so a job that always
// fails is attempted exactly twice.
func TestDecideAck_BoundsRetriesAtTwoAttempts(t *testing.T) {
	assert.Equal(t, ackRequeue, decideAck(boom, false, false), "attempt 1 goes back")
	assert.Equal(t, ackDiscard, decideAck(boom, false, true), "attempt 2 is the last")
}
