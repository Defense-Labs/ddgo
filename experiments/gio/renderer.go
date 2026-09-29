package main

/*
#cgo linux LDFLAGS: -lGLESv2
#include <stdlib.h>
#include <GLES3/gl3.h>
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// lineRenderer is intentionally only a shader plus three batched VBOs.
type lineRenderer struct {
	program, pathVBO, gridVBO, markerVBO C.GLuint
	transform                            C.GLint
	pathN, gridN                         int
}

func newLineRenderer(path []Segment) (*lineRenderer, error) {
	program, err := makeProgram()
	if err != nil {
		return nil, err
	}
	r := &lineRenderer{program: program, transform: C.glGetUniformLocation(program, C.CString("uTransform"))}
	C.glGenBuffers(1, &r.pathVBO)
	C.glGenBuffers(1, &r.gridVBO)
	C.glGenBuffers(1, &r.markerVBO)
	vertices := makePathVertices(path)
	r.pathN = len(vertices) / 6
	r.upload(r.pathVBO, vertices, C.GL_STATIC_DRAW)
	grid := makeGridVertices()
	r.gridN = len(grid) / 6
	r.upload(r.gridVBO, grid, C.GL_STATIC_DRAW)
	r.upload(r.markerVBO, makeMarkerVertices(Point{}), C.GL_DYNAMIC_DRAW)
	C.glEnable(C.GL_DEPTH_TEST)
	C.glDepthFunc(C.GL_LESS)
	return r, nil
}

func (r *lineRenderer) upload(buffer C.GLuint, data []float32, usage C.GLenum) {
	C.glBindBuffer(C.GL_ARRAY_BUFFER, buffer)
	C.glBufferData(C.GL_ARRAY_BUFFER, C.GLsizeiptr(len(data)*4), unsafe.Pointer(&data[0]), usage)
}
func (r *lineRenderer) Draw(width, height int, matrix [16]float32, tool Point) {
	if width <= 0 || height <= 0 {
		return
	}
	C.glEnable(C.GL_DEPTH_TEST)
	C.glDepthFunc(C.GL_LESS)
	C.glViewport(0, 0, C.GLsizei(width), C.GLsizei(height))
	C.glClearColor(.055, .08, .11, 1)
	C.glClear(C.GL_COLOR_BUFFER_BIT | C.GL_DEPTH_BUFFER_BIT)
	C.glUseProgram(r.program)
	C.glUniformMatrix4fv(r.transform, 1, C.GL_FALSE, (*C.GLfloat)(unsafe.Pointer(&matrix[0])))
	r.drawBuffer(r.gridVBO, r.gridN)
	r.drawBuffer(r.pathVBO, r.pathN)
	marker := makeMarkerVertices(tool)
	r.upload(r.markerVBO, marker, C.GL_DYNAMIC_DRAW)
	r.drawBuffer(r.markerVBO, len(marker)/6)
}
func (r *lineRenderer) drawBuffer(buffer C.GLuint, n int) {
	C.glBindBuffer(C.GL_ARRAY_BUFFER, buffer)
	C.glEnableVertexAttribArray(0)
	C.glEnableVertexAttribArray(1)
	C.glVertexAttribPointer(0, 3, C.GL_FLOAT, C.GL_FALSE, 24, nil)
	C.glVertexAttribPointer(1, 3, C.GL_FLOAT, C.GL_FALSE, 24, unsafe.Pointer(uintptr(12)))
	C.glDrawArrays(C.GL_LINES, 0, C.GLsizei(n))
}
func (r *lineRenderer) Release() {
	if r.program != 0 {
		C.glDeleteProgram(r.program)
	}
	if r.pathVBO != 0 {
		C.glDeleteBuffers(1, &r.pathVBO)
	}
	if r.gridVBO != 0 {
		C.glDeleteBuffers(1, &r.gridVBO)
	}
	if r.markerVBO != 0 {
		C.glDeleteBuffers(1, &r.markerVBO)
	}
	*r = lineRenderer{}
}

func makePathVertices(path []Segment) []float32 {
	v := make([]float32, 0, len(path)*12)
	for _, s := range path {
		color := [3]float32{.18, .9, .45}
		if s.Rapid {
			color = [3]float32{.95, .55, .12}
		}
		v = appendVertex(v, s.Start, color)
		v = appendVertex(v, s.End, color)
	}
	return v
}
func makeGridVertices() []float32 {
	var v []float32
	gray := [3]float32{.20, .28, .34}
	for i := -60; i <= 60; i += 10 {
		f := float32(i)
		v = appendVertex(v, Point{f, -60, 0}, gray)
		v = appendVertex(v, Point{f, 60, 0}, gray)
		v = appendVertex(v, Point{-60, f, 0}, gray)
		v = appendVertex(v, Point{60, f, 0}, gray)
	}
	v = appendVertex(v, Point{}, [3]float32{1, .1, .1})
	v = appendVertex(v, Point{70, 0, 0}, [3]float32{1, .1, .1})
	v = appendVertex(v, Point{}, [3]float32{.1, 1, .1})
	v = appendVertex(v, Point{0, 70, 0}, [3]float32{.1, 1, .1})
	v = appendVertex(v, Point{}, [3]float32{.2, .5, 1})
	v = appendVertex(v, Point{0, 0, 45}, [3]float32{.2, .5, 1})
	return v
}
func makeMarkerVertices(p Point) []float32 {
	const d float32 = 4
	var v []float32
	c := [3]float32{1, 1, .2}
	for _, a := range [][2]Point{{{-d, 0, 0}, {d, 0, 0}}, {{0, -d, 0}, {0, d, 0}}, {{0, 0, -d}, {0, 0, d}}} {
		v = appendVertex(v, Point{p.X + a[0].X, p.Y + a[0].Y, p.Z + a[0].Z}, c)
		v = appendVertex(v, Point{p.X + a[1].X, p.Y + a[1].Y, p.Z + a[1].Z}, c)
	}
	return v
}
func appendVertex(v []float32, p Point, c [3]float32) []float32 {
	return append(v, p.X, p.Y, p.Z, c[0], c[1], c[2])
}

func makeProgram() (C.GLuint, error) {
	vs := C.CString(`#version 300 es
layout(location=0) in vec3 position; layout(location=1) in vec3 color; uniform mat4 uTransform; out vec3 vColor; void main(){gl_Position=uTransform*vec4(position,1.0);vColor=color;}`)
	defer C.free(unsafe.Pointer(vs))
	fs := C.CString(`#version 300 es
precision mediump float; in vec3 vColor; out vec4 outColor; void main(){outColor=vec4(vColor,1.0);}`)
	defer C.free(unsafe.Pointer(fs))
	vert, err := compile(C.GL_VERTEX_SHADER, vs)
	if err != nil {
		return 0, err
	}
	defer C.glDeleteShader(vert)
	frag, err := compile(C.GL_FRAGMENT_SHADER, fs)
	if err != nil {
		return 0, err
	}
	defer C.glDeleteShader(frag)
	p := C.glCreateProgram()
	C.glAttachShader(p, vert)
	C.glAttachShader(p, frag)
	C.glLinkProgram(p)
	var ok C.GLint
	C.glGetProgramiv(p, C.GL_LINK_STATUS, &ok)
	if ok == C.GL_FALSE {
		C.glDeleteProgram(p)
		return 0, fmt.Errorf("link shader program failed")
	}
	return p, nil
}
func compile(kind C.GLenum, source *C.char) (C.GLuint, error) {
	s := C.glCreateShader(kind)
	C.glShaderSource(s, 1, &source, nil)
	C.glCompileShader(s)
	var ok C.GLint
	C.glGetShaderiv(s, C.GL_COMPILE_STATUS, &ok)
	if ok == C.GL_FALSE {
		C.glDeleteShader(s)
		return 0, fmt.Errorf("compile shader failed")
	}
	return s, nil
}
