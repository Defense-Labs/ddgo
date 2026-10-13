package app

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/ianbruene/ddgo/internal/grbl"
	"github.com/ianbruene/ddgo/internal/transport"
)

func defaultGG3TimingValues() map[int]float64 {
	return map[int]float64{
		13: 0,
		24: 30, 25: 2000, 26: 1, 27: 0.5,
		110: 2540, 111: 3100, 112: 3100,
		120: 500, 121: 500, 122: 500,
		130: 86.5, 131: 241.5, 132: 78.5,
	}
}

func durationNear(left, right, tolerance time.Duration) bool {
	delta := left - right
	if delta < 0 {
		delta = -delta
	}
	return delta <= tolerance
}

func TestGG3SystemMotionDuration(t *testing.T) {
	got, ok := gg3SystemMotionDuration(10, 600, 300, 100)
	// 300 mm/min = 5 mm/s: 10/5 + 2*5/100 = 2.1 seconds.
	if !ok || got != 2100*time.Millisecond {
		t.Fatalf("gg3SystemMotionDuration() = (%v, %t), want (2.1s, true)", got, ok)
	}
	floor, ok := gg3SystemMotionDuration(1, 0, 0, 10)
	if !ok || floor < time.Minute || floor > time.Minute+10*time.Millisecond {
		t.Fatalf("minimum-feed duration = (%v, %t), want approximately 1m0.003s", floor, ok)
	}
	for _, input := range []struct {
		distance, requested, maximum, acceleration float64
	}{
		{-1, 1, 1, 1}, {1, -1, 1, 1}, {1, 1, -1, 1}, {1, 1, 1, 0},
		{math.NaN(), 1, 1, 1}, {1, math.Inf(1), 1, 1}, {1, 1, 1, math.NaN()},
	} {
		if _, ok := gg3SystemMotionDuration(input.distance, input.requested, input.maximum, input.acceleration); ok {
			t.Fatalf("invalid motion input %+v unexpectedly accepted", input)
		}
	}
}

func TestGG3ExpectedSilenceDefaultSanity(t *testing.T) {
	values := defaultGG3TimingValues()
	tests := []struct {
		command grbl.SilentSystemCommand
		class   expectedSilenceClass
		minimum time.Duration
		maximum time.Duration
	}{
		{grbl.SilentSystemHomeX, expectedSilenceHomeX, 11 * time.Second, 13 * time.Second},
		{grbl.SilentSystemHomeY, expectedSilenceHomeY, 18 * time.Second, 20 * time.Second},
		{grbl.SilentSystemHomeZ, expectedSilenceHomeZ, 11 * time.Second, 13 * time.Second},
		{grbl.SilentSystemHomeAll, expectedSilenceHomeAll, 28 * time.Second, 30 * time.Second},
		{grbl.SilentSystemLevelSave, expectedSilenceLevelSave, 48 * time.Second, 52 * time.Second},
		{grbl.SilentSystemLevel, expectedSilenceLevel, 37 * time.Second, 41 * time.Second},
	}
	for _, tt := range tests {
		spec, ok := gg3ExpectedSilenceSpec(tt.command, values)
		if !ok || spec.class != tt.class || spec.inactivityLimit < tt.minimum || spec.inactivityLimit > tt.maximum {
			t.Errorf("command %d spec = %+v, ok=%t; want class %d in [%v,%v]", tt.command, spec, ok, tt.class, tt.minimum, tt.maximum)
		}
	}
}

func TestGG3ExpectedSilenceTimingInputs(t *testing.T) {
	base := defaultGG3TimingValues()
	baseSpec, ok := gg3ExpectedSilenceSpec(grbl.SilentSystemHomeX, base)
	if !ok {
		t.Fatal("default HomeX policy unavailable")
	}
	mutations := []struct {
		name   string
		number int
		value  float64
	}{
		{"slower feed", 24, 15},
		{"slower seek", 25, 1000},
		{"longer travel", 130, 173},
		{"larger pull-off", 27, 1},
		{"longer debounce", 26, 500},
		{"max-rate clamp", 110, 500},
		{"slower acceleration", 120, 100},
	}
	for _, tt := range mutations {
		t.Run(tt.name, func(t *testing.T) {
			values := cloneGRBLSettings(base)
			values[tt.number] = tt.value
			spec, ok := gg3ExpectedSilenceSpec(grbl.SilentSystemHomeX, values)
			if !ok || spec.inactivityLimit <= baseSpec.inactivityLimit {
				t.Fatalf("changed spec = %+v, ok=%t; want longer than %v", spec, ok, baseSpec.inactivityLimit)
			}
		})
	}

	for _, mutate := range []func(map[int]float64){
		func(values map[int]float64) { delete(values, 24) },
		func(values map[int]float64) { values[111] = math.NaN() },
		func(values map[int]float64) { values[122] = 0 },
	} {
		values := cloneGRBLSettings(base)
		mutate(values)
		if spec, ok := gg3ExpectedSilenceSpec(grbl.SilentSystemHomeX, values); ok {
			t.Fatalf("unusable settings produced spec %+v", spec)
		}
	}
}

func TestGG3LevelSaveRespondsToXAndZHomingSettings(t *testing.T) {
	base := defaultGG3TimingValues()
	baseSpec, ok := gg3ExpectedSilenceSpec(grbl.SilentSystemLevelSave, base)
	if !ok {
		t.Fatal("default LevelSave policy unavailable")
	}
	for _, number := range []int{24, 25, 27, 110, 112, 120, 122, 130, 132} {
		values := cloneGRBLSettings(base)
		values[number] /= 2
		if number == 27 || number == 130 || number == 132 {
			values[number] *= 4
		}
		spec, ok := gg3ExpectedSilenceSpec(grbl.SilentSystemLevelSave, values)
		if !ok || spec.inactivityLimit == baseSpec.inactivityLimit {
			t.Errorf("changing $%d did not alter LevelSave timing: got %+v, base %+v", number, spec, baseSpec)
		}
	}
}

func TestGG3ReportInchesUsesTruncatedSettingSemantics(t *testing.T) {
	for _, tt := range []struct {
		value      float64
		wantInches bool
		wantKnown  bool
	}{
		{0, false, true}, {0.9, false, true}, {1, true, true}, {1.9, true, true},
		{math.NaN(), false, false}, {math.Inf(1), false, false},
	} {
		inches, known := gg3ReportInchesFromSetting(tt.value)
		if inches != tt.wantInches || known != tt.wantKnown {
			t.Errorf("gg3ReportInchesFromSetting(%v) = (%t, %t), want (%t, %t)", tt.value, inches, known, tt.wantInches, tt.wantKnown)
		}
	}
}

func TestGG3LevelAdjustmentReportUnitConversion(t *testing.T) {
	mmValues := defaultGG3TimingValues()
	mmValues[13] = 0
	mmSpec, ok := gg3ExpectedSilenceSpec(grbl.SilentSystemLevel, mmValues)
	if !ok {
		t.Fatal("millimeter-report Level policy unavailable")
	}
	inchValues := defaultGG3TimingValues()
	inchValues[13] = 1
	inchSpec, ok := gg3ExpectedSilenceSpec(grbl.SilentSystemLevel, inchValues)
	if !ok {
		t.Fatal("inch-report Level policy unavailable")
	}

	mmAdjustment, parsedMM := grbl.ParseGG3LevelAdjustment("[25.400 mm]")
	inchAdjustment, parsedInch := grbl.ParseGG3LevelAdjustment("[1.0000 mm]")
	negativeInchAdjustment, parsedNegative := grbl.ParseGG3LevelAdjustment("[-1.0000 mm]")
	mmLimit, ok := gg3LevelNextInactivityLimit(mmSpec.levelPlan, mmAdjustment, 1)
	inchLimit, ok2 := gg3LevelNextInactivityLimit(inchSpec.levelPlan, inchAdjustment, 1)
	negativeInchLimit, ok3 := gg3LevelNextInactivityLimit(inchSpec.levelPlan, negativeInchAdjustment, 1)
	if !parsedMM || !parsedInch || !parsedNegative || !ok || !ok2 || !ok3 || !durationNear(mmLimit, inchLimit, time.Nanosecond) ||
		!durationNear(inchLimit, negativeInchLimit, time.Nanosecond) {
		t.Fatalf("unit conversion limits: mm=%v/%t inch=%v/%t negative-inch=%v/%t", mmLimit, ok, inchLimit, ok2, negativeInchLimit, ok3)
	}
}

func TestGG3LevelAdjustmentConvertsBeforeTravelCap(t *testing.T) {
	values := defaultGG3TimingValues()
	values[13] = 1
	spec, ok := gg3ExpectedSilenceSpec(grbl.SilentSystemLevel, values)
	if !ok {
		t.Fatal("inch-report Level policy unavailable")
	}
	rawAdjustment, parsed := grbl.ParseGG3LevelAdjustment("[10.0000 mm]")
	convertedAndCapped, ok := gg3LevelNextInactivityLimit(spec.levelPlan, rawAdjustment, 1)
	fullTravel, ok2 := gg3LevelNextInactivityLimitMM(spec.levelPlan, spec.levelPlan.xTravel, 1)
	if !parsed || !ok || !ok2 || convertedAndCapped != fullTravel {
		t.Fatalf("converted cap = %v/%t, full travel = %v/%t", convertedAndCapped, ok, fullTravel, ok2)
	}
}

func TestGG3LevelMissingReportUnitsKeepsInitialLeaseAndFallsBack(t *testing.T) {
	values := defaultGG3TimingValues()
	knownSpec, ok := gg3ExpectedSilenceSpec(grbl.SilentSystemLevel, values)
	if !ok {
		t.Fatal("known-unit Level policy unavailable")
	}
	delete(values, 13)
	spec, ok := gg3ExpectedSilenceSpec(grbl.SilentSystemLevel, values)
	if !ok || spec.inactivityLimit != knownSpec.inactivityLimit || spec.levelPlan.reportUnitsKnown {
		t.Fatalf("missing-$13 Level spec = %+v, ok=%t; known spec = %+v", spec, ok, knownSpec)
	}

	c := NewController(transport.NewFakeTransport(), nil)
	started := time.Unix(75, 0)
	c.now = func() time.Time { return started }
	c.mu.Lock()
	c.connectionGeneration = 1
	c.statusMonitoringGeneration = 1
	c.state.ConnectionStatus = ConnectionConnected
	owner := responseOwner{kind: responseOwnerInteractiveMacro, session: newResponseSession(make(chan string, 1)), generation: 1}
	c.responseOwner = owner
	id, err := c.beginExpectedSilenceLocked(owner, spec)
	if err != nil {
		c.mu.Unlock()
		t.Fatal(err)
	}
	when := started.Add(time.Second)
	c.noteControllerActivityLocked(1, when)
	c.noteExpectedSilenceProgressLocked(1, when, "[1.000 mm]")
	lease := c.expectedSilence
	c.mu.Unlock()
	wantLimit, ok := gg3LevelFallbackInactivityLimit(spec.levelPlan, 1)
	if !ok || lease.id != id || lease.adjustmentCount != 1 || lease.lastActivity != when || lease.inactivityLimit != wantLimit {
		t.Fatalf("unknown-unit progress lease = %+v, want id=%d count=1 activity=%v limit=%v", lease, id, when, wantLimit)
	}

	values[13] = math.NaN()
	nonFiniteSpec, ok := gg3ExpectedSilenceSpec(grbl.SilentSystemLevel, values)
	if !ok || nonFiniteSpec.levelPlan.reportUnitsKnown {
		t.Fatalf("non-finite $13 spec = %+v, ok=%t", nonFiniteSpec, ok)
	}
}

func TestGG3LevelProgressUpdatesSameLease(t *testing.T) {
	spec, ok := gg3ExpectedSilenceSpec(grbl.SilentSystemLevel, defaultGG3TimingValues())
	if !ok {
		t.Fatal("default Level policy unavailable")
	}
	c := NewController(transport.NewFakeTransport(), nil)
	started := time.Unix(100, 0)
	c.now = func() time.Time { return started }
	c.mu.Lock()
	c.connectionGeneration = 1
	c.statusMonitoringGeneration = 1
	c.state.ConnectionStatus = ConnectionConnected
	owner := responseOwner{kind: responseOwnerInteractiveMacro, session: newResponseSession(make(chan string, 1)), generation: 1}
	c.responseOwner = owner
	id, err := c.beginExpectedSilenceLocked(owner, spec)
	if err != nil {
		c.mu.Unlock()
		t.Fatalf("beginExpectedSilenceLocked() error = %v", err)
	}
	initial := c.expectedSilence.inactivityLimit
	lines := []string{"[1.000 mm]", "[-2.000 mm]", "[0.500 mm]"}
	for index, line := range lines {
		when := started.Add(time.Duration(index+1) * time.Second)
		c.noteControllerActivityLocked(1, when)
		c.noteExpectedSilenceProgressLocked(1, when, line)
		if c.expectedSilence.id != id || c.expectedSilence.adjustmentCount != index+1 || c.expectedSilence.lastActivity != when {
			c.mu.Unlock()
			t.Fatalf("progress %d lease = %+v", index+1, c.expectedSilence)
		}
	}
	final := c.expectedSilence.inactivityLimit
	c.noteControllerActivityLocked(1, started.Add(4*time.Second))
	c.noteExpectedSilenceProgressLocked(1, started.Add(4*time.Second), "[4.000 mm]")
	if c.expectedSilence.adjustmentCount != 3 {
		c.mu.Unlock()
		t.Fatalf("extra adjustment count = %d, want 3", c.expectedSilence.adjustmentCount)
	}
	c.mu.Unlock()
	if final >= initial {
		t.Fatalf("final phase limit = %v, want shorter than initial %v", final, initial)
	}
}

func TestGG3LevelProgressFallbackAndGenerationIsolation(t *testing.T) {
	spec, ok := gg3ExpectedSilenceSpec(grbl.SilentSystemLevel, defaultGG3TimingValues())
	if !ok {
		t.Fatal("default Level policy unavailable")
	}
	c := NewController(transport.NewFakeTransport(), nil)
	started := time.Unix(200, 0)
	c.now = func() time.Time { return started }
	c.mu.Lock()
	c.connectionGeneration = 2
	c.statusMonitoringGeneration = 2
	c.state.ConnectionStatus = ConnectionConnected
	owner := responseOwner{kind: responseOwnerInteractiveMacro, session: newResponseSession(make(chan string, 1)), generation: 2}
	c.responseOwner = owner
	_, err := c.beginExpectedSilenceLocked(owner, spec)
	if err != nil {
		c.mu.Unlock()
		t.Fatal(err)
	}
	c.noteExpectedSilenceProgressLocked(1, started.Add(time.Second), "[1.000 mm]")
	c.noteExpectedSilenceProgressLocked(2, started.Add(time.Second), "ordinary output")
	if c.expectedSilence.adjustmentCount != 0 {
		c.mu.Unlock()
		t.Fatalf("stale/unrelated RX changed count to %d", c.expectedSilence.adjustmentCount)
	}
	c.noteExpectedSilenceProgressLocked(2, started.Add(2*time.Second), "[NaN mm]")
	if c.expectedSilence.adjustmentCount != 1 || c.expectedSilence.inactivityLimit <= 0 {
		c.mu.Unlock()
		t.Fatalf("malformed progress fallback lease = %+v", c.expectedSilence)
	}
	plan := c.expectedSilence.levelPlan
	c.mu.Unlock()

	capped, ok := gg3LevelNextInactivityLimit(plan, plan.xTravel*10, 2)
	fullTravel, ok2 := gg3LevelNextInactivityLimit(plan, plan.xTravel, 2)
	if !ok || !ok2 || capped != fullTravel {
		t.Fatalf("adjustment cap: large=%v/%t full=%v/%t", capped, ok, fullTravel, ok2)
	}
}

func TestProductionExpectedSilenceClasses(t *testing.T) {
	tests := []struct {
		line  string
		class expectedSilenceClass
	}{
		{"$H", expectedSilenceHomeAll}, {"$HX", expectedSilenceHomeX}, {"$HY", expectedSilenceHomeY},
		{"$HZ", expectedSilenceHomeZ}, {"$L", expectedSilenceLevel}, {"$LS", expectedSilenceLevelSave},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			c, fake, observing := connectExpectedSilenceController(t, 0, nil)
			c.mu.Lock()
			c.expectedSilencePolicy = nil
			c.setGRBLSettingsLocked(c.connectionGeneration, defaultGG3TimingValues())
			c.mu.Unlock()
			fake.SetResponding(false)
			atWrite := make(chan expectedSilenceLease, 1)
			observing.setWriteObserver(func(msg transport.Message) {
				if msg.Display == tt.line {
					atWrite <- expectedSilenceSnapshot(c)
				}
			})
			if err := c.SendConsoleLine(context.Background(), tt.line); err != nil {
				t.Fatalf("SendConsoleLine(%q) error = %v", tt.line, err)
			}
			lease := <-atWrite
			if lease.id == 0 || lease.class != tt.class || lease.inactivityLimit <= 2*time.Second {
				t.Fatalf("lease at write = %+v", lease)
			}
			fake.InjectRX("ok")
			waitForExpectedSilence(t, c, false)
		})
	}
}

func TestProductionHomeAndInteractiveMacroLeaseBeforeWrite(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		class expectedSilenceClass
		send  func(context.Context, *Controller) error
	}{
		{"home action", "$H", expectedSilenceHomeAll, func(ctx context.Context, c *Controller) error { return c.Home(ctx) }},
		{"interactive macro", "$HY", expectedSilenceHomeY, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, fake, observing := connectExpectedSilenceController(t, 0, nil)
			c.mu.Lock()
			c.expectedSilencePolicy = nil
			c.setGRBLSettingsLocked(c.connectionGeneration, defaultGG3TimingValues())
			c.mu.Unlock()
			fake.SetResponding(false)
			atWrite := make(chan expectedSilenceLease, 1)
			observing.setWriteObserver(func(msg transport.Message) {
				if msg.Display == tt.line {
					atWrite <- expectedSilenceSnapshot(c)
				}
			})
			if tt.send != nil {
				if err := tt.send(context.Background(), c); err != nil {
					t.Fatal(err)
				}
				if lease := <-atWrite; lease.id == 0 || lease.class != tt.class {
					t.Fatalf("lease at write = %+v", lease)
				}
				fake.InjectRX("ok")
				return
			}
			session, err := c.beginInteractiveSession(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer c.endInteractiveSession(session, nil)
			done := make(chan error, 1)
			go func() { done <- c.runtimeForSession(session).sendLineAndWaitOK(context.Background(), tt.line, 0) }()
			if lease := <-atWrite; lease.id == 0 || lease.class != tt.class {
				t.Fatalf("lease at write = %+v", lease)
			}
			fake.InjectRX("ok")
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProductionExpectedSilenceRequiresCompleteCurrentSettings(t *testing.T) {
	c := NewController(transport.NewFakeTransport(), nil)
	c.mu.Lock()
	c.connectionGeneration = 3
	c.grblSettings = grblSettingsSnapshot{generation: 3, values: map[int]float64{24: 30}}
	if spec, ok := c.expectedSilenceSpecForLineLocked("$H"); ok {
		c.mu.Unlock()
		t.Fatalf("partial settings produced spec %+v", spec)
	}
	c.setGRBLSettingsLocked(2, defaultGG3TimingValues())
	if spec, ok := c.expectedSilenceSpecForLineLocked("$H"); ok {
		c.mu.Unlock()
		t.Fatalf("stale settings produced spec %+v", spec)
	}
	c.mu.Unlock()
}

func TestProductionLevelSaveSilenceExpiresUnresponsive(t *testing.T) {
	spec, ok := gg3ExpectedSilenceSpec(grbl.SilentSystemLevelSave, defaultGG3TimingValues())
	if !ok {
		t.Fatal("default LevelSave policy unavailable")
	}
	c := NewController(transport.NewFakeTransport(), nil)
	started := time.Unix(300, 0)
	c.now = func() time.Time { return started }
	c.mu.Lock()
	c.connectionGeneration = 1
	c.statusMonitoringGeneration = 1
	c.state.ConnectionStatus = ConnectionConnected
	owner := responseOwner{kind: responseOwnerInteractiveMacro, session: newResponseSession(make(chan string, 1)), generation: 1}
	c.responseOwner = owner
	if _, err := c.beginExpectedSilenceLocked(owner, spec); err != nil {
		c.mu.Unlock()
		t.Fatal(err)
	}
	c.mu.Unlock()
	c.checkStatusWatchdog(1, started.Add(spec.inactivityLimit))
	state := c.Snapshot()
	if state.EStopStatus != EStopActive || state.EStopSource != EStopSourceUnresponsive {
		t.Fatalf("state after LevelSave timeout = %+v", state)
	}
	if lease := expectedSilenceSnapshot(c); lease.id != 0 {
		t.Fatalf("expired LevelSave lease remains active: %+v", lease)
	}
}
