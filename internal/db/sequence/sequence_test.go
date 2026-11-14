package sequence

import (
	"database/sql"
	"math"
	"testing"
)

func TestNextValuePreservesUnusedAndReservedIDs(t *testing.T) {
	for _, tc := range []struct {
		name      string
		last      int64
		called    bool
		maximumID sql.NullInt64
		increment int64
		want      int64
	}{
		{"empty unused", 1, false, sql.NullInt64{}, 1, 1},
		{"dense restore", 1, false, sql.NullInt64{Int64: 3, Valid: true}, 1, 4},
		{"sparse restore", 1, false, sql.NullInt64{Int64: 100000, Valid: true}, 1, 100001},
		{"ahead unused", 400, false, sql.NullInt64{Int64: 3, Valid: true}, 1, 400},
		{"reserved cache", 500, true, sql.NullInt64{Int64: 3, Valid: true}, 1, 501},
		{"deleted highest row", 900, true, sql.NullInt64{}, 1, 901},
		{"custom increment", 3, true, sql.NullInt64{Int64: 20, Valid: true}, 7, 24},
		{"last available ID", math.MaxInt64 - 1, true, sql.NullInt64{}, 1, math.MaxInt64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := nextValue(sequenceState{tc.last, tc.called}, sequenceParameters{tc.increment, 1, math.MaxInt64, false}, tc.maximumID)
			if err != nil || got != tc.want {
				t.Fatalf("next ID = %d, err = %v, want %d", got, err, tc.want)
			}
		})
	}
}

func TestNextValueRejectsExhaustionAndUnsafeParameters(t *testing.T) {
	for _, tc := range []struct {
		state      sequenceState
		parameters sequenceParameters
		highest    sql.NullInt64
	}{
		{sequenceState{math.MaxInt64, true}, sequenceParameters{1, 1, math.MaxInt64, false}, sql.NullInt64{}},
		{sequenceState{1, false}, sequenceParameters{1, 1, math.MaxInt64, false}, sql.NullInt64{Int64: math.MaxInt64, Valid: true}},
		{sequenceState{8, true}, sequenceParameters{3, 1, 10, false}, sql.NullInt64{}},
		{sequenceState{1, false}, sequenceParameters{3, 1, 9, false}, sql.NullInt64{Int64: 8, Valid: true}},
		{sequenceState{1, false}, sequenceParameters{1, 1, 10, true}, sql.NullInt64{}},
		{sequenceState{1, false}, sequenceParameters{-1, 1, 10, false}, sql.NullInt64{}},
	} {
		if _, err := nextValue(tc.state, tc.parameters, tc.highest); err == nil {
			t.Fatalf("accepted exhausted or unsafe sequence: %+v", tc)
		}
	}
}
