//go:build miqt

package ui

import (
	"fmt"
	"math"

	qt "github.com/mappu/miqt/qt"
)

// GCodeSourceView presents faithful source text and a physical-line gutter.
// The source remains in the QPlainTextEdit unchanged, allowing future use of
// SetExtraSelections for execution highlighting.
type GCodeSourceView struct {
	editor *qt.QPlainTextEdit
	gutter *qt.QWidget
}

func newGCodeSourceView(source string) *GCodeSourceView {
	v := &GCodeSourceView{editor: qt.NewQPlainTextEdit(nil)}
	v.editor.SetReadOnly(true)
	v.editor.SetLineWrapMode(qt.QPlainTextEdit__NoWrap)
	v.editor.SetFont(qt.QFontDatabase_SystemFont(qt.QFontDatabase__FixedFont))
	v.editor.SetPlainText(source)

	v.gutter = qt.NewQWidget(v.editor.QWidget)
	v.gutter.OnPaintEvent(func(_ func(*qt.QPaintEvent), event *qt.QPaintEvent) {
		v.paintLineNumbers(event)
	})
	v.editor.OnBlockCountChanged(func(int) { v.updateGutterWidth() })
	v.editor.OnUpdateRequest(func(rect *qt.QRect, dy int) {
		if dy != 0 {
			v.gutter.Scroll(0, dy)
		} else {
			v.gutter.Update2(0, rect.Y(), v.gutter.Width(), rect.Height())
		}
	})
	v.editor.OnResizeEvent(func(super func(*qt.QResizeEvent), event *qt.QResizeEvent) {
		super(event)
		v.layoutGutter()
	})
	v.updateGutterWidth()
	return v
}

func (v *GCodeSourceView) gutterWidth() int {
	digits := len(fmt.Sprintf("%d", max(1, v.editor.BlockCount())))
	return 10 + v.editor.FontMetrics().HorizontalAdvance("9")*digits
}

func (v *GCodeSourceView) updateGutterWidth() {
	v.editor.SetViewportMargins(v.gutterWidth(), 0, 0, 0)
	v.layoutGutter()
}

func (v *GCodeSourceView) layoutGutter() {
	contents := v.editor.ContentsRect()
	v.gutter.SetGeometry(contents.X(), contents.Y(), v.gutterWidth(), contents.Height())
}

func (v *GCodeSourceView) paintLineNumbers(event *qt.QPaintEvent) {
	painter := qt.NewQPainter2(v.gutter.QPaintDevice)
	painter.FillRect7(event.Rect().X(), event.Rect().Y(), event.Rect().Width(), event.Rect().Height(), qt.LightGray)
	painter.SetPen(qt.NewQColor2(qt.DarkGray))

	block := v.editor.FirstVisibleBlock()
	top := int(math.Round(v.editor.BlockBoundingGeometry(&block).TranslatedWithQPointF(v.editor.ContentOffset()).Top()))
	bottom := top + int(math.Round(v.editor.BlockBoundingRect(&block).Height()))
	for block.IsValid() && top <= event.Rect().Bottom() {
		if block.IsVisible() && bottom >= event.Rect().Top() {
			painter.DrawText7(0, top, v.gutter.Width()-5, v.editor.FontMetrics().Height(), int(qt.AlignRight), fmt.Sprintf("%d", block.BlockNumber()+1))
		}
		block = *block.Next()
		top = bottom
		if block.IsValid() {
			bottom = top + int(math.Round(v.editor.BlockBoundingRect(&block).Height()))
		}
	}
	painter.End()
}
