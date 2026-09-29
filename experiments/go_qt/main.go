package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/qml"
)

//go:embed main.qml
var qmlSource []byte

type Point struct {
	X float32 `json:"x"`
	Y float32 `json:"y"`
	Z float32 `json:"z"`
}

type Segment struct {
	Start Point `json:"start"`
	End   Point `json:"end"`
	Rapid bool  `json:"rapid"`
}

type toolpathPayload struct {
	SegmentCount int       `json:"segmentCount"`
	Segments     []Segment `json:"segments"`
}

func main() {
	segments := generateToolpath()
	payload, err := json.Marshal(toolpathPayload{
		SegmentCount: len(segments),
		Segments:     segments,
	})
	if err != nil {
		log.Fatalf("encode synthetic toolpath: %v", err)
	}

	qt.NewQGuiApplication(os.Args)
	qt.QCoreApplication_SetApplicationName("DDGO Go + Qt 6 3D Experiment")

	engine := qml.NewQQmlApplicationEngine()
	engine.RootContext().SetContextProperty2("toolpathJSON", qt.NewQVariant14(string(payload)))
	engine.LoadData(qmlSource)
	if len(engine.RootObjects()) == 0 {
		log.Fatal("QML engine did not create a root window; inspect the QML diagnostics above")
	}

	fmt.Printf("loaded %d Go-generated toolpath segments\n", len(segments))
	os.Exit(qt.QGuiApplication_Exec())
}

func generateToolpath() []Segment {
	segments := make([]Segment, 0, 3300)
	current := Point{X: -85, Y: -65, Z: 20}
	move := func(end Point, rapid bool) {
		segments = append(segments, Segment{Start: current, End: end, Rapid: rapid})
		current = end
	}

	// Four flower-shaped circular passes provide curved XY motion at visibly
	// different depths. Small Z undulations make depth obvious during orbiting.
	for pass := 0; pass < 4; pass++ {
		depth := float32(-3 - pass*3)
		centerX := float32(-35 + pass*23)
		centerY := float32(12 - pass*7)
		radius := float32(24 - pass*2)
		start := Point{X: centerX + radius, Y: centerY, Z: 20}
		move(start, true)
		move(Point{X: start.X, Y: start.Y, Z: depth}, false)

		for i := 1; i <= 480; i++ {
			theta := float64(i) * 2 * math.Pi / 480
			wave := float32(1 + 0.12*math.Sin(5*theta))
			next := Point{
				X: centerX + radius*wave*float32(math.Cos(theta)),
				Y: centerY + radius*wave*float32(math.Sin(theta)),
				Z: depth - float32(1.4*math.Pow(math.Sin(3*theta), 2)),
			}
			move(next, false)
		}
		move(Point{X: current.X, Y: current.Y, Z: 20}, true)
	}

	// A serpentine pocketing pattern adds many straight cutting moves. Each row
	// changes depth and each reposition occurs above the synthetic work surface.
	const rows = 24
	const columns = 50
	for row := 0; row < rows; row++ {
		y := float32(-62) + float32(row)*5.2
		leftToRight := row%2 == 0
		startX := float32(-82)
		if !leftToRight {
			startX = 82
		}
		depth := float32(-4 - (row%4)*2)
		move(Point{X: startX, Y: y, Z: 20}, true)
		move(Point{X: startX, Y: y, Z: depth}, false)
		for column := 1; column <= columns; column++ {
			fraction := float32(column) / columns
			x := float32(-82) + 164*fraction
			if !leftToRight {
				x = 82 - 164*fraction
			}
			z := depth - float32(1.2*math.Sin(float64(fraction)*math.Pi))
			move(Point{X: x, Y: y, Z: z}, false)
		}
		move(Point{X: current.X, Y: current.Y, Z: 20}, true)
	}

	move(Point{X: 0, Y: 0, Z: 28}, true)
	return segments
}
