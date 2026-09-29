package main

import "math"

type vec3 struct{ x, y, z float32 }

func (a vec3) add(b vec3) vec3    { return vec3{a.x + b.x, a.y + b.y, a.z + b.z} }
func (a vec3) sub(b vec3) vec3    { return vec3{a.x - b.x, a.y - b.y, a.z - b.z} }
func (a vec3) mul(s float32) vec3 { return vec3{a.x * s, a.y * s, a.z * s} }
func dot(a, b vec3) float32       { return a.x*b.x + a.y*b.y + a.z*b.z }
func cross(a, b vec3) vec3        { return vec3{a.y*b.z - a.z*b.y, a.z*b.x - a.x*b.z, a.x*b.y - a.y*b.x} }
func normalize(v vec3) vec3 {
	if l := float32(math.Sqrt(float64(dot(v, v)))); l != 0 {
		return v.mul(1 / l)
	}
	return vec3{}
}

type Camera struct {
	target               vec3
	yaw, pitch, distance float32
	initial              cameraState
}
type cameraState struct {
	target               vec3
	yaw, pitch, distance float32
}

func newCamera() *Camera {
	c := &Camera{target: vec3{0, 0, -5}, yaw: .72, pitch: .58, distance: 175}
	c.initial = cameraState{c.target, c.yaw, c.pitch, c.distance}
	return c
}
func (c *Camera) Reset() {
	c.target, c.yaw, c.pitch, c.distance = c.initial.target, c.initial.yaw, c.initial.pitch, c.initial.distance
}
func (c *Camera) Orbit(dx, dy float32) {
	// Dragging the scene left should turn the camera left (and conversely),
	// matching the direct-manipulation convention used by the other spikes.
	c.yaw -= dx * .012
	c.pitch = maxf(-1.45, minf(1.45, c.pitch+dy*.012))
}
func (c *Camera) Pan(dx, dy float32) {
	forward := c.eye().sub(c.target)
	right := normalize(cross(vec3{0, 0, 1}, forward))
	up := normalize(cross(forward, right))
	scale := c.distance * .0015
	c.target = c.target.add(right.mul(-dx * scale)).add(up.mul(dy * scale))
}
func (c *Camera) Zoom(delta float32) {
	c.distance = maxf(25, minf(650, c.distance*float32(math.Exp(float64(-delta*.08)))))
}
func (c *Camera) eye() vec3 {
	cp := float32(math.Cos(float64(c.pitch)))
	return c.target.add(vec3{c.distance * cp * float32(math.Cos(float64(c.yaw))), c.distance * cp * float32(math.Sin(float64(c.yaw))), c.distance * float32(math.Sin(float64(c.pitch)))})
}
func (c *Camera) Matrix(aspect float32) [16]float32 {
	if aspect <= 0 {
		aspect = 1
	}
	return multiply(perspective(50*math.Pi/180, aspect, .1, 1000), lookAt(c.eye(), c.target, vec3{0, 0, 1}))
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
func minf(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}
func maxf(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}
