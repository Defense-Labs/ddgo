package frontend

import (
	"testing"

	"github.com/ianbruene/ddgo/internal/app"
)

func TestPlanJogToEndPlans(t *testing.T) {
	tests := []struct {
		name  string
		input JogToEndInput
		want  JogToEndPlan
	}{
		{
			name: "positive X target",
			input: JogToEndInput{
				Axis:        "X",
				Direction:   1,
				FeedText:    "500",
				XTravelText: "300",
				State: app.State{
					HasMachinePosition: true,
					MachinePosition:    [3]float64{-100, 0, 0},
				},
			},
			want: JogToEndPlan{Axis: "X", Target: 0, Feed: 500},
		},
		{
			name: "normalized selection preserves original axis",
			input: JogToEndInput{
				Axis:        " x ",
				Direction:   -1,
				FeedText:    "500",
				XTravelText: "300",
				State: app.State{
					HasMachinePosition: true,
					MachinePosition:    [3]float64{-100, 0, 0},
				},
			},
			want: JogToEndPlan{Axis: " x ", Target: -300, Feed: 500},
		},
		{
			name: "negative X target",
			input: JogToEndInput{
				Axis:        "X",
				Direction:   -1,
				FeedText:    "500",
				XTravelText: "300",
				State: app.State{
					HasMachinePosition: true,
					MachinePosition:    [3]float64{-100, 0, 0},
				},
			},
			want: JogToEndPlan{Axis: "X", Target: -300, Feed: 500},
		},
		{
			name: "Y travel selection",
			input: JogToEndInput{
				Axis:        "Y",
				Direction:   -1,
				FeedText:    "400",
				XTravelText: "invalid X",
				YTravelText: "200",
				ZTravelText: "invalid Z",
				State: app.State{
					HasMachinePosition: true,
					MachinePosition:    [3]float64{0, -50, 0},
				},
			},
			want: JogToEndPlan{Axis: "Y", Target: -200, Feed: 400},
		},
		{
			name: "Z travel selection",
			input: JogToEndInput{
				Axis:        "Z",
				Direction:   -1,
				FeedText:    "300",
				XTravelText: "invalid X",
				YTravelText: "invalid Y",
				ZTravelText: "75",
				State: app.State{
					HasMachinePosition: true,
					MachinePosition:    [3]float64{0, 0, -10},
				},
			},
			want: JogToEndPlan{Axis: "Z", Target: -75, Feed: 300},
		},
		{
			name: "already at positive target inclusive tolerance",
			input: JogToEndInput{
				Axis:        "X",
				Direction:   1,
				FeedText:    "500",
				XTravelText: "300",
				State: app.State{
					HasMachinePosition: true,
					MachinePosition:    [3]float64{0.001, 0, 0},
				},
			},
			want: JogToEndPlan{
				Axis:            "X",
				Target:          0,
				Feed:            500,
				AlreadyAtTarget: true,
				Message:         "X axis is already at 0.000 mm",
			},
		},
		{
			name: "already at negative target",
			input: JogToEndInput{
				Axis:        "X",
				Direction:   -1,
				FeedText:    "500",
				XTravelText: "300",
				State: app.State{
					HasMachinePosition: true,
					MachinePosition:    [3]float64{-299.9995, 0, 0},
				},
			},
			want: JogToEndPlan{
				Axis:            "X",
				Target:          -300,
				Feed:            500,
				AlreadyAtTarget: true,
				Message:         "X axis is already at -300.000 mm",
			},
		},
		{
			name: "outside tolerance",
			input: JogToEndInput{
				Axis:        "X",
				Direction:   1,
				FeedText:    "500",
				XTravelText: "300",
				State: app.State{
					HasMachinePosition: true,
					MachinePosition:    [3]float64{0.0011, 0, 0},
				},
			},
			want: JogToEndPlan{Axis: "X", Target: 0, Feed: 500},
		},
		{
			name: "zero direction selects negative target",
			input: JogToEndInput{
				Axis:        "X",
				Direction:   0,
				FeedText:    "500",
				XTravelText: "300",
				State: app.State{
					HasMachinePosition: true,
					MachinePosition:    [3]float64{-100, 0, 0},
				},
			},
			want: JogToEndPlan{Axis: "X", Target: -300, Feed: 500},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := PlanJogToEnd(tt.input)
			if err != nil {
				t.Fatalf("PlanJogToEnd() error = %v", err)
			}
			if plan != tt.want {
				t.Fatalf("PlanJogToEnd() = %+v, want %+v", plan, tt.want)
			}
		})
	}
}

func TestPlanJogToEndErrors(t *testing.T) {
	valid := JogToEndInput{
		Axis:        "X",
		Direction:   1,
		FeedText:    "500",
		XTravelText: "300",
		State: app.State{
			HasMachinePosition: true,
			MachinePosition:    [3]float64{-100, 0, 0},
		},
	}
	tests := []struct {
		name   string
		change func(*JogToEndInput)
		want   string
	}{
		{
			name: "invalid feed syntax precedes axis validation",
			change: func(input *JogToEndInput) {
				input.FeedText = "bad"
				input.Axis = "A"
				input.State.HasMachinePosition = false
			},
			want: `invalid feed: strconv.ParseFloat: parsing "bad": invalid syntax`,
		},
		{
			name:   "invalid selected travel syntax",
			change: func(input *JogToEndInput) { input.XTravelText = "bad" },
			want:   `invalid X travel: strconv.ParseFloat: parsing "bad": invalid syntax`,
		},
		{
			name:   "unsupported axis",
			change: func(input *JogToEndInput) { input.Axis = "A" },
			want:   `unsupported jog axis "A"`,
		},
		{
			name: "unknown machine position",
			change: func(input *JogToEndInput) {
				input.State.HasMachinePosition = false
			},
			want: "machine position is unknown; wait for a status report before jog-to-end",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := valid
			tt.change(&input)
			if _, err := PlanJogToEnd(input); err == nil || err.Error() != tt.want {
				t.Fatalf("PlanJogToEnd() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestPlanJogToEndRejectsNonPositiveOrNonFiniteValues(t *testing.T) {
	tests := []struct {
		name       string
		feedText   string
		travelText string
		want       string
	}{
		{name: "zero feed", feedText: "0", travelText: "300", want: "invalid feed: must be a finite value greater than zero"},
		{name: "negative feed", feedText: "-1", travelText: "300", want: "invalid feed: must be a finite value greater than zero"},
		{name: "NaN feed", feedText: "NaN", travelText: "300", want: "invalid feed: must be a finite value greater than zero"},
		{name: "infinite feed", feedText: "Inf", travelText: "300", want: "invalid feed: must be a finite value greater than zero"},
		{name: "zero travel", feedText: "500", travelText: "0", want: "invalid X travel: must be a finite value greater than zero"},
		{name: "negative travel", feedText: "500", travelText: "-1", want: "invalid X travel: must be a finite value greater than zero"},
		{name: "NaN travel", feedText: "500", travelText: "NaN", want: "invalid X travel: must be a finite value greater than zero"},
		{name: "infinite travel", feedText: "500", travelText: "Inf", want: "invalid X travel: must be a finite value greater than zero"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := JogToEndInput{
				Axis:        "X",
				Direction:   1,
				FeedText:    tt.feedText,
				XTravelText: tt.travelText,
				State: app.State{
					HasMachinePosition: true,
					MachinePosition:    [3]float64{-100, 0, 0},
				},
			}
			if _, err := PlanJogToEnd(input); err == nil || err.Error() != tt.want {
				t.Fatalf("PlanJogToEnd() error = %v, want %q", err, tt.want)
			}
		})
	}
}
