package app

import (
	"math"
	"time"

	"github.com/ianbruene/ddgo/internal/grbl"
)

// These constants describe Defdist/grbl1v1g_GG3_GrBLDC3v0 at revision
// b42f8e461506e75b2d0b5d9014e64c323a49608d. They are GG3 firmware
// properties, not generic Grbl constants.
const (
	gg3HomingAxisSearchScalar = 1.5
	gg3HomingAxisLocateScalar = 5.0
	gg3HomingLocateCycles     = 2
	gg3DistanceFirstPullaway  = 5.0 // mm
	gg3MinimumFeedRate        = 1.0 // mm/min
)

type gg3TimingSettings struct {
	homingFeed     float64
	homingSeek     float64
	homingDebounce float64
	homingPullOff  float64
	maxRate        [3]float64
	acceleration   [3]float64
	travel         [3]float64
}

type expectedSilenceLevelPlan struct {
	valid               bool
	xHomeDuration       time.Duration
	measurementDuration time.Duration
	xTravel             float64
	xFeedRate           float64
	xMaxRate            float64
	xAcceleration       float64
}

func gg3TimingSettingsFromSnapshot(values map[int]float64) (gg3TimingSettings, bool) {
	required := [...]int{24, 25, 26, 27, 110, 111, 112, 120, 121, 122, 130, 131, 132}
	for _, number := range required {
		value, ok := values[number]
		if !ok || !isFinite(value) {
			return gg3TimingSettings{}, false
		}
	}
	settings := gg3TimingSettings{
		homingFeed:     values[24],
		homingSeek:     values[25],
		homingDebounce: values[26],
		homingPullOff:  values[27],
		maxRate:        [3]float64{values[110], values[111], values[112]},
		acceleration:   [3]float64{values[120], values[121], values[122]},
		travel:         [3]float64{values[130], values[131], values[132]},
	}
	if settings.homingFeed < 0 || settings.homingSeek < 0 || settings.homingDebounce < 0 || settings.homingPullOff < 0 {
		return gg3TimingSettings{}, false
	}
	for axis := range settings.maxRate {
		if settings.maxRate[axis] < 0 || settings.acceleration[axis] <= 0 || settings.travel[axis] < 0 {
			return gg3TimingSettings{}, false
		}
	}
	return settings, true
}

func gg3SystemMotionDuration(distanceMM, requestedRateMMMin, maxRateMMMin, accelerationMMS2 float64) (time.Duration, bool) {
	if !isFinite(distanceMM) || distanceMM < 0 ||
		!isFinite(requestedRateMMMin) || requestedRateMMMin < 0 ||
		!isFinite(maxRateMMMin) || maxRateMMMin < 0 ||
		!isFinite(accelerationMMS2) || accelerationMMS2 <= 0 {
		return 0, false
	}
	effectiveRate := math.Min(requestedRateMMMin, maxRateMMMin)
	effectiveRate = math.Max(gg3MinimumFeedRate, effectiveRate)
	velocity := effectiveRate / 60
	seconds := distanceMM/velocity + 2*velocity/accelerationMMS2
	return durationFromSeconds(seconds)
}

func gg3HomingCycleDuration(travel, maxRate, acceleration float64, settings gg3TimingSettings) (time.Duration, bool) {
	distances := [...]float64{
		gg3HomingAxisSearchScalar * travel,
		gg3DistanceFirstPullaway,
		gg3DistanceFirstPullaway + gg3HomingAxisLocateScalar*settings.homingPullOff,
		settings.homingPullOff,
		gg3HomingAxisLocateScalar * settings.homingPullOff,
		settings.homingPullOff,
	}
	rates := [...]float64{
		settings.homingSeek,
		settings.homingSeek,
		settings.homingSeek,
		settings.homingSeek,
		settings.homingFeed,
		settings.homingSeek,
	}
	var total time.Duration
	for index, distance := range distances {
		motion, ok := gg3SystemMotionDuration(distance, rates[index], maxRate, acceleration)
		if !ok || !addDuration(&total, motion) {
			return 0, false
		}
	}
	phaseCount := 2 + 2*gg3HomingLocateCycles
	debounce, ok := durationFromSeconds(float64(phaseCount) * settings.homingDebounce / 1000)
	if !ok || !addDuration(&total, debounce) {
		return 0, false
	}
	return total, total > 0
}

func gg3MeasurementDuration(settings gg3TimingSettings) (time.Duration, bool) {
	distances := [...]float64{
		gg3DistanceFirstPullaway,
		gg3DistanceFirstPullaway + gg3HomingAxisLocateScalar*settings.homingPullOff,
		gg3DistanceFirstPullaway,
	}
	rates := [...]float64{settings.homingSeek, settings.homingFeed, settings.homingSeek}
	var total time.Duration
	for index, distance := range distances {
		motion, ok := gg3SystemMotionDuration(distance, rates[index], settings.maxRate[0], settings.acceleration[0])
		if !ok || !addDuration(&total, motion) {
			return 0, false
		}
	}
	debounce, ok := durationFromSeconds(3 * settings.homingDebounce / 1000)
	if !ok || !addDuration(&total, debounce) {
		return 0, false
	}
	return total, total > 0
}

func gg3ExpectedSilenceSpec(command grbl.SilentSystemCommand, values map[int]float64) (expectedSilenceSpec, bool) {
	settings, ok := gg3TimingSettingsFromSnapshot(values)
	if !ok {
		return expectedSilenceSpec{}, false
	}
	homeX, ok := gg3HomingCycleDuration(settings.travel[0], settings.maxRate[0], settings.acceleration[0], settings)
	if !ok {
		return expectedSilenceSpec{}, false
	}
	homeY, ok := gg3HomingCycleDuration(settings.travel[1], settings.maxRate[1], settings.acceleration[1], settings)
	if !ok {
		return expectedSilenceSpec{}, false
	}
	homeZ, ok := gg3HomingCycleDuration(settings.travel[2], settings.maxRate[2], settings.acceleration[2], settings)
	if !ok {
		return expectedSilenceSpec{}, false
	}
	xyHome, ok := gg3HomingCycleDuration(
		math.Max(settings.travel[0], settings.travel[1]),
		math.Min(settings.maxRate[0], settings.maxRate[1]),
		math.Min(settings.acceleration[0], settings.acceleration[1]),
		settings,
	)
	if !ok {
		return expectedSilenceSpec{}, false
	}
	measurement, ok := gg3MeasurementDuration(settings)
	if !ok {
		return expectedSilenceSpec{}, false
	}

	var class expectedSilenceClass
	var base time.Duration
	var plan expectedSilenceLevelPlan
	switch command {
	case grbl.SilentSystemHomeAll:
		class = expectedSilenceHomeAll
		if !sumDurations(&base, homeZ, xyHome) {
			return expectedSilenceSpec{}, false
		}
	case grbl.SilentSystemHomeX:
		class, base = expectedSilenceHomeX, homeX
	case grbl.SilentSystemHomeY:
		class, base = expectedSilenceHomeY, homeY
	case grbl.SilentSystemHomeZ:
		class, base = expectedSilenceHomeZ, homeZ
	case grbl.SilentSystemLevelSave:
		class = expectedSilenceLevelSave
		if !sumDurations(&base, homeZ, homeX, measurement, homeX) {
			return expectedSilenceSpec{}, false
		}
	case grbl.SilentSystemLevel:
		class = expectedSilenceLevel
		if !sumDurations(&base, homeZ, homeX, measurement) {
			return expectedSilenceSpec{}, false
		}
		plan = expectedSilenceLevelPlan{
			valid:               true,
			xHomeDuration:       homeX,
			measurementDuration: measurement,
			xTravel:             settings.travel[0],
			xFeedRate:           settings.homingFeed,
			xMaxRate:            settings.maxRate[0],
			xAcceleration:       settings.acceleration[0],
		}
	default:
		return expectedSilenceSpec{}, false
	}
	limit, ok := gg3AddPolicyMargin(base)
	if !ok || limit <= 0 {
		return expectedSilenceSpec{}, false
	}
	return expectedSilenceSpec{class: class, inactivityLimit: limit, levelPlan: plan}, true
}

func gg3LevelNextInactivityLimit(plan expectedSilenceLevelPlan, adjustment float64, adjustmentCount int) (time.Duration, bool) {
	if !plan.valid || adjustmentCount < 1 || adjustmentCount > 3 || !isFinite(adjustment) {
		return 0, false
	}
	distance := math.Min(math.Abs(adjustment), plan.xTravel)
	square, ok := gg3SystemMotionDuration(distance, plan.xFeedRate, plan.xMaxRate, plan.xAcceleration)
	if !ok {
		return 0, false
	}
	var base time.Duration
	if adjustmentCount < 3 {
		if !sumDurations(&base, square, plan.xHomeDuration, plan.xHomeDuration, plan.measurementDuration) {
			return 0, false
		}
	} else if !sumDurations(&base, square, plan.xHomeDuration) {
		return 0, false
	}
	return gg3AddPolicyMargin(base)
}

func gg3LevelFallbackInactivityLimit(plan expectedSilenceLevelPlan, adjustmentCount int) (time.Duration, bool) {
	return gg3LevelNextInactivityLimit(plan, plan.xTravel, adjustmentCount)
}

func gg3AddPolicyMargin(base time.Duration) (time.Duration, bool) {
	if base <= 0 {
		return 0, false
	}
	margin := base / 10
	if margin < 2*time.Second {
		margin = 2 * time.Second
	}
	if base > time.Duration(math.MaxInt64)-margin {
		return 0, false
	}
	return base + margin, true
}

func durationFromSeconds(seconds float64) (time.Duration, bool) {
	if !isFinite(seconds) || seconds < 0 || seconds > float64(math.MaxInt64)/float64(time.Second) {
		return 0, false
	}
	return time.Duration(seconds * float64(time.Second)), true
}

func addDuration(total *time.Duration, value time.Duration) bool {
	if value < 0 || *total > time.Duration(math.MaxInt64)-value {
		return false
	}
	*total += value
	return true
}

func sumDurations(total *time.Duration, values ...time.Duration) bool {
	for _, value := range values {
		if !addDuration(total, value) {
			return false
		}
	}
	return true
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
