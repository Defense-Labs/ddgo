//go:build miqt

package miqt

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strings"

	"github.com/ianbruene/ddgo/internal/app"
	"github.com/ianbruene/ddgo/internal/ddpackage"
	"github.com/ianbruene/ddgo/internal/frontend"
	qt "github.com/mappu/miqt/qt6"
)

// DDPackageWindow is the first-class native view for a read-only .dd package.
// It deliberately contains no execution controls: package-step execution is a
// DD-B3 concern. Its auxiliary G-code views are owned by the package document.
type DDPackageWindow struct {
	pkg        *ddpackage.Package
	documentID app.DocumentID
	window     *qt.QMainWindow
	jobCombo   *qt.QComboBox
	steps      *qt.QListWidget
	detail     *qt.QTextEdit
	image      *qt.QLabel
	openGCode  *qt.QPushButton
	warnings   *qt.QLabel

	jobIndex       int
	stepIndex      int
	onOpenStep     func(int, ddpackage.Step)
	onClose        func()
	onCloseRequest func() bool
	closeNotified  bool
}

func newDDPackageWindow(id app.DocumentID, pkg *ddpackage.Package, openStep func(int, ddpackage.Step)) *DDPackageWindow {
	w := &DDPackageWindow{pkg: pkg, documentID: id, window: qt.NewQMainWindow(nil), onOpenStep: openStep, jobIndex: -1, stepIndex: -1}
	w.window.SetWindowTitle(pkg.DisplayName + " — DD package — DDGo")
	w.window.Resize(1120, 760)
	w.window.SetAttribute(qt.WA_DeleteOnClose)

	central := qt.NewQWidget(nil)
	layout := qt.NewQVBoxLayout(nil)
	central.SetLayout(layout.QLayout)
	jobRow := qt.NewQHBoxLayout(nil)
	jobLabel := qt.NewQLabel(nil)
	jobLabel.SetText("Job:")
	jobRow.AddWidget(jobLabel.QWidget)
	w.jobCombo = qt.NewQComboBox(nil)
	jobRow.AddWidget(w.jobCombo.QWidget)
	layout.AddLayout(jobRow.QLayout)

	split := qt.NewQSplitter3(qt.Horizontal)
	left := qt.NewQWidget(nil)
	leftLayout := qt.NewQVBoxLayout(nil)
	left.SetLayout(leftLayout.QLayout)
	stepLabel := qt.NewQLabel(nil)
	stepLabel.SetText("Steps")
	leftLayout.AddWidget(stepLabel.QWidget)
	w.steps = qt.NewQListWidget(nil)
	leftLayout.AddWidget(w.steps.QWidget)
	split.AddWidget(left)

	right := qt.NewQWidget(nil)
	rightLayout := qt.NewQVBoxLayout(nil)
	right.SetLayout(rightLayout.QLayout)
	w.detail = qt.NewQTextEdit(nil)
	w.detail.SetReadOnly(true)
	rightLayout.AddWidget(w.detail.QWidget)
	w.image = qt.NewQLabel(nil)
	w.image.SetWordWrap(true)
	rightLayout.AddWidget(w.image.QWidget)
	w.openGCode = qt.NewQPushButton(nil)
	w.openGCode.SetText("Open G-code View")
	rightLayout.AddWidget(w.openGCode.QWidget)
	split.AddWidget(right)
	split.SetStretchFactor(0, 2)
	split.SetStretchFactor(1, 5)
	layout.AddWidget(split.QWidget)

	strip := qt.NewQGroupBox(nil)
	strip.SetTitle("Package Execution")
	stripLayout := qt.NewQVBoxLayout(nil)
	strip.SetLayout(stripLayout.QLayout)
	stripText := qt.NewQLabel(nil)
	stripText.SetText("No package step running — package execution controls are available in a future release.")
	stripText.SetWordWrap(true)
	stripLayout.AddWidget(stripText.QWidget)
	layout.AddWidget(strip.QWidget)
	w.warnings = qt.NewQLabel(nil)
	w.warnings.SetWordWrap(true)
	w.warnings.SetStyleSheet("color: #8a5a00;")
	layout.AddWidget(w.warnings.QWidget)
	w.window.SetCentralWidget(central)

	for _, job := range pkg.Jobs {
		w.jobCombo.AddItem(job.Name)
	}
	if len(pkg.Jobs) == 0 {
		w.jobCombo.SetEnabled(false)
	}
	w.jobCombo.OnCurrentIndexChanged(func(index int) { w.selectJob(index) })
	w.steps.OnCurrentRowChanged(func(index int) { w.selectStep(index) })
	w.openGCode.OnClicked(func() {
		if step, ok := w.selectedStep(); ok && step.GCodePath != "" && w.onOpenStep != nil {
			w.onOpenStep(w.jobIndex, step)
		}
	})
	w.window.OnCloseEvent(func(super func(*qt.QCloseEvent), event *qt.QCloseEvent) {
		if w.onCloseRequest != nil && !w.onCloseRequest() {
			event.SetAccepted(false)
			return
		}
		super(event)
		if event.IsAccepted() && !w.closeNotified {
			w.closeNotified = true
			if w.onClose != nil {
				w.onClose()
			}
		}
	})
	if len(pkg.Jobs) > 0 {
		w.selectJob(0)
	}
	if len(pkg.Warnings) > 0 {
		messages := make([]string, 0, len(pkg.Warnings))
		for _, warning := range pkg.Warnings {
			messages = append(messages, fmt.Sprintf("%s: %s", warning.Path, warning.Message))
		}
		w.warnings.SetText("Package compatibility notes: " + strings.Join(messages, "\n"))
	}
	return w
}

func (w *DDPackageWindow) selectJob(index int) {
	if index < 0 || index >= len(w.pkg.Jobs) {
		return
	}
	w.jobIndex, w.stepIndex = index, -1
	w.steps.Clear()
	for i, step := range w.pkg.Jobs[index].Steps {
		qt.NewQListWidgetItem7(fmt.Sprintf("%d. %s", i+1, step.Name), w.steps)
	}
	if len(w.pkg.Jobs[index].Steps) > 0 {
		w.steps.SetCurrentRow(0)
	} else {
		w.renderStep()
	}
}

func (w *DDPackageWindow) selectStep(index int) { w.stepIndex = index; w.renderStep() }

func (w *DDPackageWindow) selectedStep() (ddpackage.Step, bool) {
	if w.jobIndex < 0 || w.jobIndex >= len(w.pkg.Jobs) {
		return ddpackage.Step{}, false
	}
	steps := w.pkg.Jobs[w.jobIndex].Steps
	if w.stepIndex < 0 || w.stepIndex >= len(steps) {
		return ddpackage.Step{}, false
	}
	return steps[w.stepIndex], true
}

func (w *DDPackageWindow) renderStep() {
	jobText := ""
	if w.jobIndex >= 0 && w.jobIndex < len(w.pkg.Jobs) {
		jobText = w.pkg.Jobs[w.jobIndex].Text
	}
	step, ok := w.selectedStep()
	if !ok {
		w.detail.SetPlainText(jobText + "\n\nNo steps are declared for this job.")
		w.image.Clear()
		w.openGCode.SetEnabled(false)
		return
	}
	text := "Job: " + w.pkg.Jobs[w.jobIndex].Name + "\n\n" + jobText + "\n\nStep: " + step.Name
	if step.Text != "" {
		text += "\n\n" + step.Text
	}
	if step.Markdown != "" {
		text += "\n\nMarkdown:\n" + step.Markdown
	}
	if step.GCodePath != "" {
		text += "\n\nG-code: " + step.GCodePath
	}
	if len(w.pkg.Jobs[w.jobIndex].GuideFiles) > 0 {
		text += "\n\nGuides: " + strings.Join(w.pkg.Jobs[w.jobIndex].GuideFiles, ", ")
	}
	if len(w.pkg.Jobs[w.jobIndex].ModelFiles) > 0 {
		text += "\nModels: " + strings.Join(w.pkg.Jobs[w.jobIndex].ModelFiles, ", ")
	}
	if len(w.pkg.AdditionalFiles) > 0 {
		files := make([]string, 0, len(w.pkg.AdditionalFiles))
		for _, asset := range w.pkg.AdditionalFiles {
			files = append(files, fmt.Sprintf("%s (%d bytes)", asset.Path, asset.Size))
		}
		text += "\n\nAdditional files: " + strings.Join(files, ", ")
	}
	w.detail.SetPlainText(text) // Plain text deliberately avoids untrusted HTML/network resources.
	w.openGCode.SetEnabled(step.GCodePath != "")
	w.image.Clear()
	if step.ImagePath != "" {
		data, err := w.pkg.ReadResource(step.ImagePath)
		if err != nil {
			w.image.SetText("Image unavailable: " + err.Error())
			return
		}
		if len(data) > 16<<20 {
			w.image.SetText("Image omitted: resource is too large to display.")
			return
		}
		config, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width > 4096 || config.Height > 4096 || int64(config.Width)*int64(config.Height) > 16<<20 {
			w.image.SetText("Image unavailable: unsupported, malformed, or too large to display safely.")
			return
		}
		pixmap := qt.NewQPixmap()
		if !pixmap.LoadFromDataWithData(data) {
			w.image.SetText("Image unavailable: unsupported or malformed image.")
			return
		}
		if pixmap.Width() != config.Width || pixmap.Height() != config.Height {
			w.image.SetText("Image unavailable: decoded image dimensions were inconsistent.")
			return
		}
		w.image.SetPixmap(pixmap.Scaled2(800, 500, qt.KeepAspectRatio))
	}
}

func (w *DDPackageWindow) show()                            { w.window.Show() }
func (w *DDPackageWindow) SetOnClose(fn func())             { w.onClose = fn }
func (w *DDPackageWindow) SetOnCloseRequest(fn func() bool) { w.onCloseRequest = fn }
func (w *DDPackageWindow) Focus()                           { w.window.ShowNormal(); w.window.Raise(); w.window.ActivateWindow() }
func (w *DDPackageWindow) Close() bool                      { return w.window.Close() }
func (w *DDPackageWindow) ApplyState(app.State)             {}
func (w *DDPackageWindow) ApplyEvent(frontend.Event)        {}
