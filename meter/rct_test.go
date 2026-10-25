package meter

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/evcc-io/rct"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRCTTakeRSocStrategy verifies that a repeated BatteryNormal application
// (no intervening non-normal mode) is a no-op instead of a nil dereference (#31471).
func TestRCTTakeRSocStrategy(t *testing.T) {
	m := &RCT{}

	// nothing saved yet
	assert.Nil(t, m.takeRSocStrategy())

	m.setRSocStrategyIfAbsent(4)
	strategy := m.takeRSocStrategy()
	require.NotNil(t, strategy)
	assert.Equal(t, uint8(4), *strategy)

	// second take without an intervening set must not repeat the value or panic
	assert.Nil(t, m.takeRSocStrategy())
}

// TestRCTSetRSocStrategyIfAbsent verifies the saved value is never overwritten
// while still present, matching the original guard's intent.
func TestRCTSetRSocStrategyIfAbsent(t *testing.T) {
	m := &RCT{}

	m.setRSocStrategyIfAbsent(4)
	m.setRSocStrategyIfAbsent(7)

	strategy := m.takeRSocStrategy()
	require.NotNil(t, strategy)
	assert.Equal(t, uint8(4), *strategy)
}

// TestRCTWriteSkippable verifies the readback comparison that keeps a battery
// mode from rewriting values the inverter already holds (#34779).
func TestRCTWriteSkippable(t *testing.T) {
	errRead := errors.New("connection reset by peer")

	var (
		wrote = time.Now()
		after = wrote.Add(time.Second)
		befor = wrote.Add(-time.Second)
		never = time.Time{}
	)

	tc := []struct {
		name            string
		cur             float64
		err             error
		readAt, written time.Time
		val, epsilon    float64
		skip            bool
	}{
		{"identical soc, never written", 0.2, nil, after, never, 0.2, rctSocEpsilon, true},
		{"identical soc, read after our write", 0.2, nil, after, wrote, 0.2, rctSocEpsilon, true},
		// what a written value actually reads back as
		{"float32 round-trip", float64(float32(0.3)), nil, after, never, 0.3, rctSocEpsilon, true},
		// a manually raised reserve is still overwritten with the configured one
		{"soc differs", 0.3, nil, after, never, 0.2, rctSocEpsilon, false},
		// the tolerance must stay well below the one percent the app offers
		{"one percent apart", 0.21, nil, after, never, 0.2, rctSocEpsilon, false},
		{"power setpoint reached", 0.0, nil, after, never, 0, rctPowerEpsilon, true},
		{"power setpoint differs", -4200, nil, after, never, 0, rctPowerEpsilon, false},
		// a readback that failed says nothing, so the write must happen
		{"read failed", 0.2, errRead, after, never, 0.2, rctSocEpsilon, false},
		// the hold -> normal case inside the cache window: the cached entry was
		// read before the hold wrote the current soc, so it still reads as the
		// configured min soc. Skipping here would leave the hold in place.
		{"cached from before our write", 0.2, nil, befor, wrote, 0.2, rctSocEpsilon, false},
	}

	for _, tc := range tc {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.skip, writeSkippable(tc.cur, tc.err, tc.readAt, tc.written, tc.val, tc.epsilon))
		})
	}
}

// TestRCTNoteWrite verifies the per-register write timestamps the readback
// comparison relies on.
func TestRCTNoteWrite(t *testing.T) {
	m := &RCT{}

	// nothing written yet: any readback is newer than the zero time
	assert.True(t, m.lastWriteAt(rct.BatterySoCTargetMin).IsZero())

	before := time.Now()
	m.noteWrite(rct.BatterySoCTargetMin)

	assert.False(t, m.lastWriteAt(rct.BatterySoCTargetMin).Before(before))
	// registers are tracked separately
	assert.True(t, m.lastWriteAt(rct.PowerMngSocMax).IsZero())
}

// TestRCTSocEpsilonResolution guards the tolerance against being widened to
// where it would swallow a change the RCT app can make (one percent).
func TestRCTSocEpsilonResolution(t *testing.T) {
	assert.Less(t, rctSocEpsilon, 0.01)
	// and it must still absorb the float32 round-trip of any soc fraction
	for _, soc := range []float64{0.05, 0.07, 0.2, 0.3, 0.95, 1} {
		assert.LessOrEqual(t, math.Abs(float64(float32(soc))-soc), rctSocEpsilon)
	}
}
