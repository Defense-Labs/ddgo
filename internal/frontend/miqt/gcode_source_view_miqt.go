//go:build miqt

package miqt

import (
	"fmt"
	"math"

	"github.com/ianbruene/ddgo/internal/frontend"
	qt "github.com/mappu/miqt/qt6"
)

// GCodeSourceView presents faithful source text and a physical-line gutter.
// The source remains in the QPlainTextEdit unchanged. MIQT 0.14 exposes an
// extra-selection setter but not a default ExtraSelection constructor, so the
// execution marker is rendered in the existing gutter rather than changing
// the editor's user cursor or source document formatting.
type GCodeSourceView struct {
	editor           *qt.QPlainTextEdit
	gutter           *qt.QWidget
	highlightedLine  int
	followWasEnabled bool
}

// SetExecutionHighlight renders a non-destructive marker for one physical
// source line. The editor cursor remains under user control. A zero line
// explicitly clears a prior marker.
func (v *GCodeSourceView) SetExecutionHighlight(line int, follow bool) {
	if line > 0 {
		block := v.editor.Document().FindBlockByNumber(line - 1)
		if !block.IsValid() {
			line = 0
		}
	}
	action := frontend.SourceHighlightUpdate(v.highlightedLine, line, follow, v.followWasEnabled)
	v.followWasEnabled = follow
	if !action.Apply {
		return
	}
	v.highlightedLine = line
	v.gutter.Update()
	if !action.Reveal {
		return
	}
	block := v.editor.Document().FindBlockByNumber(line - 1)
	geometry := v.editor.BlockBoundingGeometry(block)
	offset := v.editor.ContentOffset()
	top := int(math.Round(geometry.TranslatedWithQPointF(&offset).Top()))
	blockRect := v.editor.BlockBoundingRect(block)
	bottom := top + int(math.Round(blockRect.Height()))
	viewportHeight := v.editor.Viewport().Height()
	if top < 0 || bottom > viewportHeight {
		// QPlainTextEdit's embedded QAbstractScrollArea exposes its vertical
		// scrollbar through MIQT. Moving it reveals the block without changing
		// the user's QTextCursor or selection.
		v.editor.VerticalScrollBar().SetValue(block.BlockNumber())
	}
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
	geometry := v.editor.BlockBoundingGeometry(&block)
	offset := v.editor.ContentOffset()
	translated := geometry.TranslatedWithQPointF(&offset)
	top := int(math.Round(translated.Top()))
	blockRect := v.editor.BlockBoundingRect(&block)
	bottom := top + int(math.Round(blockRect.Height()))
	for block.IsValid() && top <= event.Rect().Bottom() {
		if block.IsVisible() && bottom >= event.Rect().Top() {
			if block.BlockNumber()+1 == v.highlightedLine {
				painter.FillRect5(0, top, v.gutter.Width(), int(math.Round(blockRect.Height())), qt.NewQColor2(qt.Yellow))
			}
			painter.DrawText7(0, top, v.gutter.Width()-5, v.editor.FontMetrics().Height(), int(qt.AlignRight), fmt.Sprintf("%d", block.BlockNumber()+1))
		}
		block = *block.Next()
		top = bottom
		if block.IsValid() {
			blockRect = v.editor.BlockBoundingRect(&block)
			bottom = top + int(math.Round(blockRect.Height()))
		}
	}
	painter.End()
}
