package main

import "math"

type vec3 struct{ x, y, z float32 }

func (a vec3) add(b vec3) vec3    { return vec3{a.x + b.x, a.y + b.y, a.z + b.z} }
func (a vec3) sub(b vec3) vec3    { return vec3{a.x - b.x, a.y - b.y, a.z - b.z} }
func (a vec3) mul(s float32) vec3 { return vec3{a.x * s, a.y * s, a.z * s} }
func dot(a, b vec3) float32       { return a.x*b.x + a.y*b.y + a.z*b.z }
func cross(a, b vec3) vec3 {
	return vec3{a.y*b.z - a.z*b.y, a.z*b.x - a.x*b.z, a.x*b.y - a.y*b.x}
}
func normalize(v vec3) vec3 {
	l := float32(math.Sqrt(float64(dot(v, v))))
	if l == 0 {
		return vec3{}
	}
	return v.mul(1 / l)
}

// Camera is deliberately small: orbit angles, a target for panning, and a
// distance for zooming. Matrices are column-major for WGSL mat4x4.
type Camera struct {
	target       vec3
	yaw, pitch   float32
	distance     float32
	initialState cameraState
}
type cameraState struct {
	target               vec3
	yaw, pitch, distance float32
}

func newCamera() *Camera {
	c := &Camera{target: vec3{0, 0, -3}, yaw: 0.72, pitch: 0.55, distance: 175}
	c.initialState = cameraState{c.target, c.yaw, c.pitch, c.distance}
	return c
}
func (c *Camera) Reset() {
	c.target, c.yaw, c.pitch, c.distance = c.initialState.target, c.initialState.yaw, c.initialState.pitch, c.initialState.distance
}
func (c *Camera) Orbit(dx, dy float32) {
	c.yaw += dx * 0.012
	c.pitch += dy * 0.012
	if c.pitch > 1.45 {
		c.pitch = 1.45
	}
	if c.pitch < -1.45 {
		c.pitch = -1.45
	}
}
func (c *Camera) Pan(dx, dy float32) {
	forward := c.eye().sub(c.target)
	right := normalize(cross(vec3{0, 0, 1}, forward))
	up := normalize(cross(forward, right))
	scale := c.distance * 0.0015
	c.target = c.target.add(right.mul(-dx * scale)).add(up.mul(dy * scale))
}
func (c *Camera) Zoom(delta float32) {
	c.distance *= float32(math.Exp(float64(delta * 0.08)))
	if c.distance < 20 {
		c.distance = 20
	}
	if c.distance > 600 {
		c.distance = 600
	}
}
func (c *Camera) eye() vec3 {
	cp := float32(math.Cos(float64(c.pitch)))
	return c.target.add(vec3{
		x: c.distance * cp * float32(math.Cos(float64(c.yaw))),
		y: c.distance * cp * float32(math.Sin(float64(c.yaw))),
		z: c.distance * float32(math.Sin(float64(c.pitch))),
	})
}
func (c *Camera) matrix(aspect float32) [16]float32 {
	if aspect <= 0 {
		aspect = 1
	}
	view := lookAt(c.eye(), c.target, vec3{0, 0, 1})
	proj := perspective(50*math.Pi/180, aspect, 0.1, 1000)
	return multiply(proj, view)
}
func lookAt(eye, target, up vec3) [16]float32 {
	f := normalize(target.sub(eye))
	s := normalize(cross(f, up))
	u := cross(s, f)
	return [16]float32{s.x, u.x, -f.x, 0, s.y, u.y, -f.y, 0, s.z, u.z, -f.z, 0, -dot(s, eye), -dot(u, eye), dot(f, eye), 1}
}
func perspective(fov float64, aspect, near, far float32) [16]float32 {
	f := float32(1 / math.Tan(fov/2))
	return [16]float32{f / aspect, 0, 0, 0, 0, f, 0, 0, 0, 0, far / (near - far), -1, 0, 0, (far * near) / (near - far), 0}
}
func multiply(a, b [16]float32) (r [16]float32) {
	for col := 0; col < 4; col++ {
		for row := 0; row < 4; row++ {
			for k := 0; k < 4; k++ {
				r[col*4+row] += a[k*4+row] * b[col*4+k]
			}
		}
	}
	return
}
