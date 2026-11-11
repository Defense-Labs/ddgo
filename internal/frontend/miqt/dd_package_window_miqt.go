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

// DDPackageWindow is the first-class native view for a .dd package. It can
// manually execute one selected G-code step, while the package itself remains
// the logical document and lifetime owner of every step session.
type DDPackageWindow struct {
	pkg               *ddpackage.Package
	documentID        app.DocumentID
	window            *qt.QMainWindow
	jobCombo          *qt.QComboBox
	steps             *qt.QListWidget
	detail            *qt.QTextEdit
	imageGallery      *qt.QScrollArea
	openGCode         *qt.QPushButton
	runStep           *qt.QPushButton
	selectedStatus    *qt.QLabel
	warningSection    *qt.QGroupBox
	warnings          *qt.QPlainTextEdit
	executionIdentity *qt.QLabel
	executionStatus   *qt.QLabel
	executionProgress *qt.QLabel
	executionError    *qt.QPlainTextEdit
	executionBar      *qt.QProgressBar
	pauseExecution    *qt.QPushButton
	resumeExecution   *qt.QPushButton
	stopExecution     *qt.QPushButton
	viewActive        *qt.QPushButton

	jobIndex            int
	stepIndex           int
	onOpenStep          func(int, ddpackage.Step)
	onSelectStep        func(int, ddpackage.Step)
	onRunStep           func(int, ddpackage.Step)
	onAction            func(frontend.ExecutionSessionID, string)
	onViewActive        func(frontend.ExecutionSessionID)
	executionID         frontend.ExecutionSessionID
	hasExecution        bool
	selectedSnapshot    frontend.ExecutionSessionSnapshot
	hasSelectedSnapshot bool
	onClose             func()
	onCloseRequest      func() bool
	closeNotified       bool
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
	w.imageGallery = qt.NewQScrollArea(nil)
	w.imageGallery.SetWidgetResizable(true)
	w.imageGallery.SetMaximumHeight(520)
	w.imageGallery.SetVisible(false)
	rightLayout.AddWidget(w.imageGallery.QWidget)
	w.openGCode = qt.NewQPushButton(nil)
	w.openGCode.SetText("Open G-code View")
	rightLayout.AddWidget(w.openGCode.QWidget)
	w.runStep = qt.NewQPushButton(nil)
	w.runStep.SetText("Run Selected Step")
	w.runStep.SetToolTip("Runs only this step's G-code; package workflow directives are not executed.")
	rightLayout.AddWidget(w.runStep.QWidget)
	w.selectedStatus = qt.NewQLabel(nil)
	w.selectedStatus.SetWordWrap(true)
	w.selectedStatus.SetStyleSheet("color: #555555;")
	rightLayout.AddWidget(w.selectedStatus.QWidget)
	split.AddWidget(right)
	split.SetStretchFactor(0, 2)
	split.SetStretchFactor(1, 5)
	split.QWidget.SetSizePolicy2(qt.QSizePolicy__Expanding, qt.QSizePolicy__Expanding)
	layout.AddWidget2(split.QWidget, 1)

	strip := qt.NewQGroupBox(nil)
	strip.SetTitle("Package Execution")
	strip.SetSizePolicy2(qt.QSizePolicy__Preferred, qt.QSizePolicy__Maximum)
	stripLayout := qt.NewQVBoxLayout(nil)
	strip.SetLayout(stripLayout.QLayout)
	w.executionIdentity = qt.NewQLabel(nil)
	w.executionIdentity.SetWordWrap(true)
	stripLayout.AddWidget(w.executionIdentity.QWidget)
	w.executionStatus = qt.NewQLabel(nil)
	w.executionStatus.SetWordWrap(true)
	stripLayout.AddWidget(w.executionStatus.QWidget)
	w.executionProgress = qt.NewQLabel(nil)
	stripLayout.AddWidget(w.executionProgress.QWidget)
	w.executionBar = qt.NewQProgressBar(nil)
	w.executionBar.SetMinimum(0)
	w.executionBar.SetMaximum(1)
	stripLayout.AddWidget(w.executionBar.QWidget)
	for _, item := range []struct {
		text string
		set  func(*qt.QPushButton)
	}{
		{"Pause", func(button *qt.QPushButton) { w.pauseExecution = button }},
		{"Resume", func(button *qt.QPushButton) { w.resumeExecution = button }},
		{"Stop", func(button *qt.QPushButton) { w.stopExecution = button }},
		{"View Active G-code", func(button *qt.QPushButton) { w.viewActive = button }},
	} {
		button := qt.NewQPushButton(nil)
		button.SetText(item.text)
		item.set(button)
		stripLayout.AddWidget(button.QWidget)
	}
	w.executionError = qt.NewQPlainTextEdit(nil)
	w.executionError.SetReadOnly(true)
	w.executionError.SetLineWrapMode(qt.QPlainTextEdit__WidgetWidth)
	w.executionError.SetMaximumHeight(84)
	w.executionError.SetVisible(false)
	stripLayout.AddWidget(w.executionError.QWidget)
	layout.AddWidget(strip.QWidget)
	w.warningSection = qt.NewQGroupBox(nil)
	w.warningSection.SetTitle("Package compatibility notes")
	w.warningSection.SetMaximumHeight(160)
	w.warningSection.SetSizePolicy2(qt.QSizePolicy__Preferred, qt.QSizePolicy__Maximum)
	warningLayout := qt.NewQVBoxLayout(nil)
	w.warningSection.SetLayout(warningLayout.QLayout)
	w.warnings = qt.NewQPlainTextEdit(nil)
	w.warnings.SetReadOnly(true)
	w.warnings.SetLineWrapMode(qt.QPlainTextEdit__WidgetWidth)
	w.warnings.SetMaximumHeight(130)
	warningLayout.AddWidget(w.warnings.QWidget)
	w.warningSection.SetVisible(false)
	layout.AddWidget(w.warningSection.QWidget)
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
	w.runStep.OnClicked(func() {
		if step, ok := w.selectedStep(); ok && w.onRunStep != nil {
			w.onRunStep(w.jobIndex, step)
		}
	})
	w.pauseExecution.OnClicked(func() { w.requestExecutionAction("pause") })
	w.resumeExecution.OnClicked(func() { w.requestExecutionAction("resume") })
	w.stopExecution.OnClicked(func() { w.requestExecutionAction("stop") })
	w.viewActive.OnClicked(func() {
		if w.hasExecution && w.onViewActive != nil {
			w.onViewActive(w.executionID)
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
		w.warnings.SetPlainText(frontend.PresentPackageDiagnostics(pkg.Warnings).Text())
		w.warningSection.SetVisible(true)
	}
	w.renderExecution()
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

func (w *DDPackageWindow) selectStep(index int) {
	w.stepIndex = index
	w.hasSelectedSnapshot = false
	w.renderStep()
	w.notifySelectedStep()
}

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
		w.clearImageGallery()
		w.openGCode.SetEnabled(false)
		w.applySelectedFeedback(frontend.PackageStepExecutionFeedback(false, nil, nil))
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
	w.renderSelectedControls()
	w.renderImageGallery(step.ImagePaths)
}

const (
	maxPackageImageBytes    = 16 << 20
	maxPackageImageWidth    = 4096
	maxPackageImageHeight   = 4096
	maxPackageImagePixels   = 16 << 20
	maxGalleryImageCount    = 16
	maxGalleryImageBytes    = 32 << 20
	maxGalleryImagePixels   = 32 << 20
	maxGalleryDisplayWidth  = 800
	maxGalleryDisplayHeight = 500
)

func (w *DDPackageWindow) clearImageGallery() {
	if old := w.imageGallery.TakeWidget(); old != nil {
		old.DeleteLater()
	}
	w.imageGallery.SetVisible(false)
}

func (w *DDPackageWindow) renderImageGallery(paths []string) {
	w.clearImageGallery()
	if len(paths) == 0 {
		return
	}
	w.imageGallery.SetVisible(true)
	content := qt.NewQWidget(nil)
	layout := qt.NewQVBoxLayout(nil)
	content.SetLayout(layout.QLayout)
	var displayedBytes, displayedPixels int64
	displayCount := len(paths)
	if displayCount > maxGalleryImageCount {
		displayCount = maxGalleryImageCount
	}
	for index, resourcePath := range paths[:displayCount] {
		caption := qt.NewQLabel(nil)
		caption.SetWordWrap(true)
		caption.SetText(fmt.Sprintf("Image %d of %d\n%s", index+1, len(paths), resourcePath))
		layout.AddWidget(caption.QWidget)
		imageLabel := qt.NewQLabel(nil)
		imageLabel.SetWordWrap(true)
		imageLabel.SetAlignment(qt.AlignHCenter)
		layout.AddWidget(imageLabel.QWidget)
		data, err := w.pkg.ReadResource(resourcePath)
		if err != nil {
			imageLabel.SetText("Image unavailable: " + err.Error())
			continue
		}
		if len(data) > maxPackageImageBytes || displayedBytes+int64(len(data)) > maxGalleryImageBytes {
			imageLabel.SetText("Image omitted: resource or gallery byte limit exceeded.")
			continue
		}
		config, _, err := image.DecodeConfig(bytes.NewReader(data))
		pixels := int64(config.Width) * int64(config.Height)
		if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width > maxPackageImageWidth || config.Height > maxPackageImageHeight || pixels > maxPackageImagePixels || displayedPixels+pixels > maxGalleryImagePixels {
			imageLabel.SetText("Image unavailable: unsupported, malformed, or too large to display safely.")
			continue
		}
		pixmap := qt.NewQPixmap()
		if !pixmap.LoadFromDataWithData(data) {
			imageLabel.SetText("Image unavailable: unsupported or malformed image.")
			continue
		}
		if pixmap.Width() != config.Width || pixmap.Height() != config.Height {
			imageLabel.SetText("Image unavailable: decoded image dimensions were inconsistent.")
			continue
		}
		displayedBytes += int64(len(data))
		displayedPixels += pixels
		imageLabel.SetPixmap(pixmap.Scaled2(maxGalleryDisplayWidth, maxGalleryDisplayHeight, qt.KeepAspectRatio))
	}
	if len(paths) > displayCount {
		omitted := qt.NewQLabel(nil)
		omitted.SetWordWrap(true)
		omitted.SetText(fmt.Sprintf("%d additional images omitted: gallery image-count limit reached.", len(paths)-displayCount))
		layout.AddWidget(omitted.QWidget)
	}
	layout.AddStretch()
	w.imageGallery.SetWidget(content)
}

// SetExecutionCallbacks is called only after the package document has been
// registered. This matters because selection during native construction must
// not create a session for a not-yet-owned document.
func (w *DDPackageWindow) SetExecutionCallbacks(selectStep, runStep func(int, ddpackage.Step), action func(frontend.ExecutionSessionID, string), viewActive func(frontend.ExecutionSessionID)) {
	w.onSelectStep, w.onRunStep, w.onAction, w.onViewActive = selectStep, runStep, action, viewActive
	w.notifySelectedStep()
}

func (w *DDPackageWindow) notifySelectedStep() {
	if step, ok := w.selectedStep(); ok && w.onSelectStep != nil {
		w.onSelectStep(w.jobIndex, step)
	}
}

// SetSelectedStepExecution applies only the selected step's own session
// snapshot. A sibling's matching package DocumentID is never enough to make
// this button control that sibling's RunID.
func (w *DDPackageWindow) SetSelectedStepExecution(snapshot frontend.ExecutionSessionSnapshot) {
	step, ok := w.selectedStep()
	if !ok || snapshot.ID != frontend.PackageStepExecutionSessionID(w.documentID, w.jobIndex+1, step.ID) {
		return
	}
	w.selectedSnapshot, w.hasSelectedSnapshot = snapshot, true
	w.renderSelectedControls()
}

func (w *DDPackageWindow) SetSelectedStepUnavailable(jobIndex int, stepID string, err error) {
	step, ok := w.selectedStep()
	if !ok || w.jobIndex != jobIndex || step.ID != stepID {
		return
	}
	w.hasSelectedSnapshot = false
	w.applySelectedFeedback(frontend.PackageStepExecutionFeedback(true, err, nil))
}

func (w *DDPackageWindow) renderSelectedControls() {
	step, ok := w.selectedStep()
	if !ok || step.GCodePath == "" {
		w.applySelectedFeedback(frontend.PackageStepExecutionFeedback(false, nil, nil))
		return
	}
	if !w.hasSelectedSnapshot {
		w.applySelectedFeedback(frontend.PackageStepExecutionFeedback(true, nil, nil))
		return
	}
	snapshot := w.selectedSnapshot
	w.applySelectedFeedback(frontend.PackageStepExecutionFeedback(true, nil, &snapshot))
}

func (w *DDPackageWindow) applySelectedFeedback(feedback frontend.PackageStepFeedback) {
	w.runStep.SetEnabled(feedback.RunEnabled)
	text := feedback.Message
	if feedback.Error != "" {
		text += "\n" + feedback.Error
	}
	w.selectedStatus.SetText(text)
}

// SetPackageExecution renders an authoritative ExecutionSessions projection.
// It does not infer execution from the currently selected job or step.
func (w *DDPackageWindow) SetPackageExecution(snapshot frontend.PackageExecutionPresentation, jobName, stepName string, present bool) {
	w.hasExecution = present
	if !present {
		w.executionID = frontend.ExecutionSessionID{}
		w.renderExecution()
		return
	}
	w.executionID = snapshot.ID
	display := frontend.PackageExecutionDisplayFor(snapshot)
	w.executionIdentity.SetText(fmt.Sprintf("%s: %s — %s", display.Prefix, jobName, stepName))
	w.executionStatus.SetText(display.Status)
	w.executionProgress.SetText(display.Progress)
	w.executionBar.SetMaximum(display.ProgressMaximum)
	w.executionBar.SetValue(display.ProgressValue)
	w.executionError.SetPlainText(display.Error)
	w.executionError.SetVisible(display.Error != "")
	w.pauseExecution.SetEnabled(display.PauseEnabled)
	w.resumeExecution.SetEnabled(display.ResumeEnabled)
	w.stopExecution.SetEnabled(display.StopEnabled)
	w.viewActive.SetEnabled(true)
}

func (w *DDPackageWindow) renderExecution() {
	if w.hasExecution {
		return
	}
	w.executionIdentity.SetText("No package step has run")
	w.executionStatus.SetText("Manual execution is available for individual G-code steps only.")
	w.executionProgress.SetText("Acknowledged commands: 0 / 0")
	w.executionBar.SetMaximum(1)
	w.executionBar.SetValue(0)
	w.executionError.Clear()
	w.executionError.SetVisible(false)
	w.pauseExecution.SetEnabled(false)
	w.resumeExecution.SetEnabled(false)
	w.stopExecution.SetEnabled(false)
	w.viewActive.SetEnabled(false)
}

func (w *DDPackageWindow) requestExecutionAction(action string) {
	if w.hasExecution && w.onAction != nil {
		w.onAction(w.executionID, action)
	}
}

func (w *DDPackageWindow) show()                            { w.window.Show() }
func (w *DDPackageWindow) SetOnClose(fn func())             { w.onClose = fn }
func (w *DDPackageWindow) SetOnCloseRequest(fn func() bool) { w.onCloseRequest = fn }
func (w *DDPackageWindow) Focus()                           { w.window.ShowNormal(); w.window.Raise(); w.window.ActivateWindow() }
func (w *DDPackageWindow) Close() bool                      { return w.window.Close() }
func (w *DDPackageWindow) ApplyState(app.State)             {}
func (w *DDPackageWindow) ApplyEvent(app.Event)             {}
