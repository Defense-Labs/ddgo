package main

import (
	"fmt"
	"strings"
	"unsafe"

	"github.com/go-gl/gl/v3.3-core/gl"
)

const lineVertexFloats = 6 // position.xyz, color.rgb

type renderer struct {
	program                    uint32
	pathVAO, pathVBO           uint32
	referenceVAO, referenceVBO uint32
	markerVAO, markerVBO       uint32
	pathVertexCount            int32
	referenceVertexCount       int32
	matrixUniform              int32
	depthBits                  int32
	glVersion, glRenderer      string
}

func (r *renderer) initialize(path []Segment) error {
	if err := gl.Init(); err != nil {
		return fmt.Errorf("load OpenGL functions: %w", err)
	}
	r.glVersion = gl.GoStr(gl.GetString(gl.VERSION))
	r.glRenderer = gl.GoStr(gl.GetString(gl.RENDERER))
	// GL_DEPTH_BITS is not exposed by go-gl's core-only binding. Query the
	// default framebuffer's depth attachment directly instead.
	gl.GetFramebufferAttachmentParameteriv(gl.FRAMEBUFFER, gl.DEPTH, gl.FRAMEBUFFER_ATTACHMENT_DEPTH_SIZE, &r.depthBits)

	program, err := newProgram(vertexShader, fragmentShader)
	if err != nil {
		return err
	}
	r.program = program
	r.matrixUniform = gl.GetUniformLocation(r.program, gl.Str("uMVP\x00"))

	pathData := make([]float32, 0, len(path)*2*lineVertexFloats)
	for _, s := range path {
		color := [3]float32{0.12, 0.82, 0.36} // cutting: green
		if s.Rapid {
			color = [3]float32{0.98, 0.60, 0.16} // rapid: orange
		}
		pathData = appendVertex(pathData, s.Start, color)
		pathData = appendVertex(pathData, s.End, color)
	}
	r.pathVAO, r.pathVBO = makeLineBuffer(pathData, gl.STATIC_DRAW)
	r.pathVertexCount = int32(len(pathData) / lineVertexFloats)

	refData := referenceGeometry()
	r.referenceVAO, r.referenceVBO = makeLineBuffer(refData, gl.STATIC_DRAW)
	r.referenceVertexCount = int32(len(refData) / lineVertexFloats)

	// The marker is the only dynamic GPU allocation. The static toolpath is
	// never regenerated or re-uploaded during animation.
	markerData := markerGeometry(Point{})
	r.markerVAO, r.markerVBO = makeLineBuffer(markerData, gl.DYNAMIC_DRAW)
	gl.Enable(gl.DEPTH_TEST)
	gl.DepthFunc(gl.LESS)
	return nil
}

func (r *renderer) destroy() {
	if r.markerVBO != 0 {
		gl.DeleteBuffers(1, &r.markerVBO)
	}
	if r.markerVAO != 0 {
		gl.DeleteVertexArrays(1, &r.markerVAO)
	}
	if r.referenceVBO != 0 {
		gl.DeleteBuffers(1, &r.referenceVBO)
	}
	if r.referenceVAO != 0 {
		gl.DeleteVertexArrays(1, &r.referenceVAO)
	}
	if r.pathVBO != 0 {
		gl.DeleteBuffers(1, &r.pathVBO)
	}
	if r.pathVAO != 0 {
		gl.DeleteVertexArrays(1, &r.pathVAO)
	}
	if r.program != 0 {
		gl.DeleteProgram(r.program)
	}
}

func (r *renderer) updateMarker(p Point) {
	data := markerGeometry(p)
	gl.BindBuffer(gl.ARRAY_BUFFER, r.markerVBO)
	gl.BufferSubData(gl.ARRAY_BUFFER, 0, len(data)*4, gl.Ptr(data))
}

// render draws only in pixelRect. ImGui positions are logical pixels with a
// top-left origin; OpenGL's viewport is framebuffer pixels with a bottom-left
// origin. main.go performs that conversion before calling this method.
func (r *renderer) render(pixelRect rect, c camera) {
	if pixelRect.w < 2 || pixelRect.h < 2 {
		return
	}
	gl.Enable(gl.SCISSOR_TEST)
	gl.Scissor(pixelRect.x, pixelRect.y, pixelRect.w, pixelRect.h)
	gl.Viewport(pixelRect.x, pixelRect.y, pixelRect.w, pixelRect.h)
	gl.Enable(gl.DEPTH_TEST)
	gl.DepthMask(true)
	gl.Disable(gl.BLEND)
	gl.ClearColor(0.035, 0.050, 0.075, 1)
	gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)

	gl.UseProgram(r.program)
	mvp := c.matrix(float32(pixelRect.w) / float32(pixelRect.h))
	gl.UniformMatrix4fv(r.matrixUniform, 1, false, &mvp[0])
	gl.LineWidth(1.0)
	gl.BindVertexArray(r.referenceVAO)
	gl.DrawArrays(gl.LINES, 0, r.referenceVertexCount)
	gl.LineWidth(1.8)
	gl.BindVertexArray(r.pathVAO)
	gl.DrawArrays(gl.LINES, 0, r.pathVertexCount)
	gl.LineWidth(2.5)
	gl.BindVertexArray(r.markerVAO)
	gl.DrawArrays(gl.LINES, 0, 6)
	gl.BindVertexArray(0)
	gl.UseProgram(0)
	gl.Disable(gl.SCISSOR_TEST)
}

type rect struct{ x, y, w, h int32 }

func appendVertex(dst []float32, p Point, color [3]float32) []float32 {
	return append(dst, p.X, p.Y, p.Z, color[0], color[1], color[2])
}

func makeLineBuffer(data []float32, usage uint32) (uint32, uint32) {
	var vao, vbo uint32
	gl.GenVertexArrays(1, &vao)
	gl.GenBuffers(1, &vbo)
	gl.BindVertexArray(vao)
	gl.BindBuffer(gl.ARRAY_BUFFER, vbo)
	gl.BufferData(gl.ARRAY_BUFFER, len(data)*4, gl.Ptr(data), usage)
	gl.EnableVertexAttribArray(0)
	gl.VertexAttribPointer(0, 3, gl.FLOAT, false, lineVertexFloats*4, unsafe.Pointer(uintptr(0)))
	gl.EnableVertexAttribArray(1)
	gl.VertexAttribPointer(1, 3, gl.FLOAT, false, lineVertexFloats*4, unsafe.Pointer(uintptr(3*4)))
	gl.BindVertexArray(0)
	return vao, vbo
}

func referenceGeometry() []float32 {
	var data []float32
	grid := [3]float32{0.18, 0.25, 0.33}
	for i := -50; i <= 50; i += 5 {
		data = appendVertex(data, Point{float32(i), -50, 0}, grid)
		data = appendVertex(data, Point{float32(i), 50, 0}, grid)
		data = appendVertex(data, Point{-50, float32(i), 0}, grid)
		data = appendVertex(data, Point{50, float32(i), 0}, grid)
	}
	data = appendVertex(data, Point{}, [3]float32{0.95, 0.18, 0.18})
	data = appendVertex(data, Point{55, 0, 0}, [3]float32{0.95, 0.18, 0.18})
	data = appendVertex(data, Point{}, [3]float32{0.18, 0.95, 0.25})
	data = appendVertex(data, Point{0, 55, 0}, [3]float32{0.18, 0.95, 0.25})
	data = appendVertex(data, Point{}, [3]float32{0.20, 0.50, 1.00})
	data = appendVertex(data, Point{0, 0, 30}, [3]float32{0.20, 0.50, 1.00})
	return data
}

func markerGeometry(p Point) []float32 {
	var data []float32
	for _, line := range [][2]Point{
		{{p.X - 2, p.Y, p.Z}, {p.X + 2, p.Y, p.Z}},
		{{p.X, p.Y - 2, p.Z}, {p.X, p.Y + 2, p.Z}},
		{{p.X, p.Y, p.Z - 2}, {p.X, p.Y, p.Z + 2}},
	} {
		data = appendVertex(data, line[0], [3]float32{1, 1, 0.15})
		data = appendVertex(data, line[1], [3]float32{1, 1, 0.15})
	}
	return data
}

func newProgram(vertex, fragment string) (uint32, error) {
	vs, err := compileShader(vertex, gl.VERTEX_SHADER)
	if err != nil {
		return 0, err
	}
	defer gl.DeleteShader(vs)
	fs, err := compileShader(fragment, gl.FRAGMENT_SHADER)
	if err != nil {
		return 0, err
	}
	defer gl.DeleteShader(fs)
	program := gl.CreateProgram()
	gl.AttachShader(program, vs)
	gl.AttachShader(program, fs)
	gl.BindAttribLocation(program, 0, gl.Str("position\x00"))
	gl.BindAttribLocation(program, 1, gl.Str("color\x00"))
	gl.LinkProgram(program)
	var status int32
	gl.GetProgramiv(program, gl.LINK_STATUS, &status)
	if status == gl.FALSE {
		return 0, fmt.Errorf("link shader: %s", programLog(program))
	}
	return program, nil
}

func compileShader(source string, kind uint32) (uint32, error) {
	shader := gl.CreateShader(kind)
	cs, free := gl.Strs(source + "\x00")
	gl.ShaderSource(shader, 1, cs, nil)
	free()
	gl.CompileShader(shader)
	var status int32
	gl.GetShaderiv(shader, gl.COMPILE_STATUS, &status)
	if status == gl.FALSE {
		return 0, fmt.Errorf("compile shader: %s", shaderLog(shader))
	}
	return shader, nil
}

func shaderLog(shader uint32) string {
	var length int32
	gl.GetShaderiv(shader, gl.INFO_LOG_LENGTH, &length)
	log := make([]byte, length+1)
	gl.GetShaderInfoLog(shader, length, nil, &log[0])
	return strings.TrimRight(string(log), "\x00")
}

func programLog(program uint32) string {
	var length int32
	gl.GetProgramiv(program, gl.INFO_LOG_LENGTH, &length)
	log := make([]byte, length+1)
	gl.GetProgramInfoLog(program, length, nil, &log[0])
	return strings.TrimRight(string(log), "\x00")
}

const vertexShader = `#version 130
in vec3 position;
in vec3 color;
uniform mat4 uMVP;
out vec3 vColor;
void main() { gl_Position = uMVP * vec4(position, 1.0); vColor = color; }
`

const fragmentShader = `#version 130
in vec3 vColor;
out vec4 outputColor;
void main() { outputColor = vec4(vColor, 1.0); }
`
