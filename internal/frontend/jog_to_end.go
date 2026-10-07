package frontend

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/ianbruene/ddgo/internal/app"
)

const jogToEndTargetTolerance = 0.001

type JogToEndInput struct {
	Axis      string
	Direction float64

	FeedText    string
	XTravelText string
	YTravelText string
	ZTravelText string

	State app.State
}

type JogToEndPlan struct {
	Axis   string
	Target float64
	Feed   float64

	AlreadyAtTarget bool
	Message         string
}

func PlanJogToEnd(input JogToEndInput) (JogToEndPlan, error) {
	feed, err := parsePositiveFloat(input.FeedText, "feed")
	if err != nil {
		return JogToEndPlan{}, err
	}

	travelText, travelName, index, err := jogToEndAxis(input)
	if err != nil {
		return JogToEndPlan{}, err
	}
	travel, err := parsePositiveFloat(travelText, travelName)
	if err != nil {
		return JogToEndPlan{}, err
	}

	if !input.State.HasMachinePosition {
		return JogToEndPlan{}, fmt.Errorf("machine position is unknown; wait for a status report before jog-to-end")
	}

	target := -travel
	if input.Direction > 0 {
		target = 0
	}
	plan := JogToEndPlan{
		Axis:   input.Axis,
		Target: target,
		Feed:   feed,
	}
	if math.Abs(input.State.MachinePosition[index]-target) <= jogToEndTargetTolerance {
		plan.AlreadyAtTarget = true
		plan.Message = fmt.Sprintf("%s axis is already at %.3f mm", strings.ToUpper(input.Axis), target)
	}
	return plan, nil
}

func parsePositiveFloat(text, name string) (float64, error) {
	value, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %v", name, err)
	}
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("invalid %s: must be a finite value greater than zero", name)
	}
	return value, nil
}

func jogToEndAxis(input JogToEndInput) (travelText string, travelName string, index int, err error) {
	switch strings.ToUpper(strings.TrimSpace(input.Axis)) {
	case "X":
		return input.XTravelText, "X travel", 0, nil
	case "Y":
		return input.YTravelText, "Y travel", 1, nil
	case "Z":
		return input.ZTravelText, "Z travel", 2, nil
	default:
		return "", "", 0, fmt.Errorf("unsupported jog axis %q", input.Axis)
	}
}
