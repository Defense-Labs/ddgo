package main

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

func main() {
	a := app.NewWithID("org.defcad.fyne-capability-test")
	w := a.NewWindow("DDGO Fyne viewport capability test")
	w.Resize(fyne.NewSize(980, 720))

	path := syntheticToolpath()
	status := widget.NewLabel(fmt.Sprintf("Fyne input/shader diagnostic — generated %d segments", len(path)))
	viewport := NewViewport(path)
	viewport.OnInput = func(text string) {
		status.SetText(text)
		// This is a capability-test diagnostic, useful when running it from a terminal.
		fmt.Println(text)
	}

	reset := widget.NewButton("Reset View", func() {
		viewport.ResetView()
		status.SetText("Reset View clicked; custom viewport camera state reset")
	})
	var animate *widget.Button
	animate = widget.NewButton("Animate", func() {
		if viewport.ToggleShaderAnimation() {
			animate.SetText("Stop")
			status.SetText("Shader animation running (diagnostic only; not a toolpath animation)")
			return
		}
		animate.SetText("Animate")
		status.SetText("Shader animation stopped; normal Fyne control remained independent of viewport input")
	})

	toolbar := container.NewHBox(reset, animate, status)
	w.SetContent(container.NewBorder(nil, toolbar, nil, nil, viewport))
	w.ShowAndRun()
}
