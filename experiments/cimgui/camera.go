package main

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

type camera struct {
	target   mgl32.Vec3
	yaw      float32
	pitch    float32
	distance float32
}

func newCamera() camera {
	var c camera
	c.reset()
	return c
}

func (c *camera) reset() {
	c.target = mgl32.Vec3{0, 0, -2}
	c.yaw = 0.72
	c.pitch = 0.55
	c.distance = 125
}

func (c *camera) eye() mgl32.Vec3 {
	cosPitch := float32(math.Cos(float64(c.pitch)))
	return c.target.Add(mgl32.Vec3{
		c.distance * cosPitch * float32(math.Cos(float64(c.yaw))),
		c.distance * cosPitch * float32(math.Sin(float64(c.yaw))),
		c.distance * float32(math.Sin(float64(c.pitch))),
	})
}

func (c *camera) orbit(dx, dy float32) {
	c.yaw += dx * 0.012
	c.pitch -= dy * 0.012
	if c.pitch > 1.45 {
		c.pitch = 1.45
	}
	if c.pitch < -1.45 {
		c.pitch = -1.45
	}
}

func (c *camera) pan(dx, dy float32) {
	eye := c.eye()
	forward := c.target.Sub(eye).Normalize()
	right := forward.Cross(mgl32.Vec3{0, 0, 1}).Normalize()
	up := right.Cross(forward).Normalize()
	scale := c.distance * 0.0016
	c.target = c.target.Sub(right.Mul(dx * scale)).Add(up.Mul(dy * scale))
}

func (c *camera) zoom(wheel float32) {
	c.distance *= float32(math.Pow(0.88, float64(wheel)))
	if c.distance < 18 {
		c.distance = 18
	}
	if c.distance > 450 {
		c.distance = 450
	}
}

func (c *camera) matrix(aspect float32) mgl32.Mat4 {
	return mgl32.Perspective(mgl32.DegToRad(45), aspect, 0.1, 1000).Mul4(
		mgl32.LookAtV(c.eye(), c.target, mgl32.Vec3{0, 0, 1}),
	)
}
