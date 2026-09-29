package main

import (
	"fmt"
	"unsafe"

	"github.com/gogpu/gputypes"
	"github.com/gogpu/wgpu"
)

const viewportWidth, viewportHeight = 960, 600

type vertex struct {
	Position [3]float32
	Color    [4]float32
}

type lineRenderer struct {
	device *wgpu.Device
	queue  *wgpu.Queue

	pipeline              *wgpu.RenderPipeline
	bind                  *wgpu.BindGroup
	uniform               *wgpu.Buffer
	path                  *wgpu.Buffer
	grid                  *wgpu.Buffer
	marker                *wgpu.Buffer
	pathN, gridN, markerN uint32

	depthView      *wgpu.TextureView
	depthTexture   *wgpu.Texture
	depthW, depthH uint32
}

func newLineRenderer(device *wgpu.Device, queue *wgpu.Queue, path []Segment) (*lineRenderer, error) {
	r := &lineRenderer{device: device, queue: queue}
	var err error
	if r.path, r.pathN, err = createStaticBuffer(device, "toolpath", pathVertices(path)); err != nil {
		return nil, err
	}
	if r.grid, r.gridN, err = createStaticBuffer(device, "grid and axes", gridVertices()); err != nil {
		r.Release()
		return nil, err
	}
	markerVertices := markerGeometry(Point{})
	if r.marker, err = device.CreateBuffer(&wgpu.BufferDescriptor{Label: "tool marker", Size: uint64(len(markerVertices)) * uint64(unsafe.Sizeof(vertex{})), Usage: gputypes.BufferUsageVertex | gputypes.BufferUsageCopyDst}); err != nil {
		r.Release()
		return nil, err
	}
	r.markerN = uint32(len(markerVertices))
	if err = queue.WriteBuffer(r.marker, 0, bytesOf(markerVertices)); err != nil {
		r.Release()
		return nil, err
	}
	if r.uniform, err = device.CreateBuffer(&wgpu.BufferDescriptor{Label: "camera matrix", Size: 64, Usage: gputypes.BufferUsageUniform | gputypes.BufferUsageCopyDst}); err != nil {
		r.Release()
		return nil, err
	}

	shader, err := device.CreateShaderModule(&wgpu.ShaderModuleDescriptor{WGSL: lineShader})
	if err != nil {
		r.Release()
		return nil, err
	}
	defer shader.Release()
	bgl, err := device.CreateBindGroupLayout(&wgpu.BindGroupLayoutDescriptor{Entries: []gputypes.BindGroupLayoutEntry{{Binding: 0, Visibility: wgpu.ShaderStageVertex, Buffer: &gputypes.BufferBindingLayout{Type: gputypes.BufferBindingTypeUniform, MinBindingSize: 64}}}})
	if err != nil {
		r.Release()
		return nil, err
	}
	defer bgl.Release()
	pl, err := device.CreatePipelineLayout(&wgpu.PipelineLayoutDescriptor{BindGroupLayouts: []*wgpu.BindGroupLayout{bgl}})
	if err != nil {
		r.Release()
		return nil, err
	}
	defer pl.Release()
	r.bind, err = device.CreateBindGroup(&wgpu.BindGroupDescriptor{Layout: bgl, Entries: []wgpu.BindGroupEntry{{Binding: 0, Buffer: r.uniform, Size: 64}}})
	if err != nil {
		r.Release()
		return nil, err
	}
	r.pipeline, err = device.CreateRenderPipeline(&wgpu.RenderPipelineDescriptor{
		Label: "CNC line-list pipeline", Layout: pl,
		Vertex: wgpu.VertexState{Module: shader, EntryPoint: "vs_main", Buffers: []gputypes.VertexBufferLayout{{
			ArrayStride: uint64(unsafe.Sizeof(vertex{})), StepMode: gputypes.VertexStepModeVertex,
			Attributes: []gputypes.VertexAttribute{{Format: gputypes.VertexFormatFloat32x3, Offset: 0, ShaderLocation: 0}, {Format: gputypes.VertexFormatFloat32x4, Offset: 12, ShaderLocation: 1}},
		}}},
		Primitive:    gputypes.PrimitiveState{Topology: gputypes.PrimitiveTopologyLineList},
		DepthStencil: &wgpu.DepthStencilState{Format: gputypes.TextureFormatDepth32Float, DepthWriteEnabled: true, DepthCompare: gputypes.CompareFunctionLess},
		Fragment:     &wgpu.FragmentState{Module: shader, EntryPoint: "fs_main", Targets: []gputypes.ColorTargetState{{Format: gputypes.TextureFormatBGRA8Unorm, WriteMask: gputypes.ColorWriteMaskAll}}},
	})
	if err != nil {
		r.Release()
		return nil, err
	}
	return r, nil
}

func (r *lineRenderer) Render(target *wgpu.TextureView, camera *Camera, tool Point) error {
	if err := r.ensureDepth(viewportWidth, viewportHeight); err != nil {
		return err
	}
	mvp := camera.matrix(float32(viewportWidth) / float32(viewportHeight))
	if err := r.queue.WriteBuffer(r.uniform, 0, bytesOf(mvp[:])); err != nil {
		return err
	}
	marker := markerGeometry(tool)
	if err := r.queue.WriteBuffer(r.marker, 0, bytesOf(marker)); err != nil {
		return err
	}
	encoder, err := r.device.CreateCommandEncoder(nil)
	if err != nil {
		return err
	}
	pass, err := encoder.BeginRenderPass(&wgpu.RenderPassDescriptor{
		ColorAttachments:       []wgpu.RenderPassColorAttachment{{View: target, LoadOp: gputypes.LoadOpClear, StoreOp: gputypes.StoreOpStore, ClearValue: gputypes.Color{R: 0.035, G: 0.045, B: 0.07, A: 1}}},
		DepthStencilAttachment: &wgpu.RenderPassDepthStencilAttachment{View: r.depthView, DepthLoadOp: gputypes.LoadOpClear, DepthStoreOp: gputypes.StoreOpStore, DepthClearValue: 1},
	})
	if err != nil {
		return err
	}
	pass.SetPipeline(r.pipeline)
	pass.SetBindGroup(0, r.bind, nil)
	pass.SetVertexBuffer(0, r.grid, 0)
	pass.Draw(r.gridN, 1, 0, 0)
	pass.SetVertexBuffer(0, r.path, 0)
	pass.Draw(r.pathN, 1, 0, 0)
	pass.SetVertexBuffer(0, r.marker, 0)
	pass.Draw(r.markerN, 1, 0, 0)
	pass.End()
	commands, err := encoder.Finish()
	if err != nil {
		return err
	}
	_, err = r.queue.Submit(commands)
	return err
}

func (r *lineRenderer) ensureDepth(w, h int) error {
	if r.depthView != nil && r.depthW == uint32(w) && r.depthH == uint32(h) {
		return nil
	}
	if r.depthView != nil {
		r.depthView.Release()
		r.depthTexture.Release()
	}
	tex, err := r.device.CreateTexture(&wgpu.TextureDescriptor{Label: "viewport depth", Size: wgpu.Extent3D{Width: uint32(w), Height: uint32(h), DepthOrArrayLayers: 1}, MipLevelCount: 1, SampleCount: 1, Dimension: gputypes.TextureDimension2D, Format: gputypes.TextureFormatDepth32Float, Usage: gputypes.TextureUsageRenderAttachment})
	if err != nil {
		return fmt.Errorf("create depth texture: %w", err)
	}
	view, err := r.device.CreateTextureView(tex, nil)
	if err != nil {
		tex.Release()
		return fmt.Errorf("create depth view: %w", err)
	}
	r.depthTexture, r.depthView, r.depthW, r.depthH = tex, view, uint32(w), uint32(h)
	return nil
}
func (r *lineRenderer) Release() {
	for _, x := range []interface{ Release() }{r.depthView, r.depthTexture, r.pipeline, r.bind, r.uniform, r.path, r.grid, r.marker} {
		if x != nil {
			x.Release()
		}
	}
}
func createStaticBuffer(device *wgpu.Device, label string, vertices []vertex) (*wgpu.Buffer, uint32, error) {
	b, err := device.CreateBuffer(&wgpu.BufferDescriptor{Label: label, Size: uint64(len(vertices)) * uint64(unsafe.Sizeof(vertex{})), Usage: gputypes.BufferUsageVertex | gputypes.BufferUsageCopyDst})
	if err != nil {
		return nil, 0, err
	}
	if err := device.Queue().WriteBuffer(b, 0, bytesOf(vertices)); err != nil {
		b.Release()
		return nil, 0, err
	}
	return b, uint32(len(vertices)), nil
}
func bytesOf[T any](v []T) []byte {
	if len(v) == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(&v[0])), len(v)*int(unsafe.Sizeof(v[0])))
}
func pathVertices(path []Segment) []vertex {
	v := make([]vertex, 0, len(path)*2)
	for _, s := range path {
		color := [4]float32{1, .55, .12, 1}
		if s.Rapid {
			color = [4]float32{.12, .72, 1, 1}
		}
		v = append(v, makeVertex(s.Start, color), makeVertex(s.End, color))
	}
	return v
}
func gridVertices() []vertex {
	var v []vertex
	grid := [4]float32{.22, .28, .36, 1}
	axisX := [4]float32{1, .2, .2, 1}
	axisY := [4]float32{.2, 1, .2, 1}
	axisZ := [4]float32{.3, .5, 1, 1}
	for i := -75; i <= 75; i += 5 {
		v = append(v, makeVertex(Point{float32(i), -75, 0}, grid), makeVertex(Point{float32(i), 75, 0}, grid), makeVertex(Point{-75, float32(i), 0}, grid), makeVertex(Point{75, float32(i), 0}, grid))
	}
	v = append(v, makeVertex(Point{-80, 0, 0}, axisX), makeVertex(Point{80, 0, 0}, axisX), makeVertex(Point{0, -80, 0}, axisY), makeVertex(Point{0, 80, 0}, axisY), makeVertex(Point{0, 0, -20}, axisZ), makeVertex(Point{0, 0, 45}, axisZ))
	return v
}
func markerGeometry(p Point) []vertex {
	c := [4]float32{1, 1, 1, 1}
	d := float32(3)
	return []vertex{makeVertex(Point{p.X - d, p.Y, p.Z}, c), makeVertex(Point{p.X + d, p.Y, p.Z}, c), makeVertex(Point{p.X, p.Y - d, p.Z}, c), makeVertex(Point{p.X, p.Y + d, p.Z}, c), makeVertex(Point{p.X, p.Y, p.Z - d}, c), makeVertex(Point{p.X, p.Y, p.Z + d}, c)}
}
func makeVertex(p Point, c [4]float32) vertex {
	return vertex{Position: [3]float32{p.X, p.Y, p.Z}, Color: c}
}

const lineShader = `
struct Camera { mvp: mat4x4<f32>, }
@group(0) @binding(0) var<uniform> camera: Camera;
struct Input { @location(0) position: vec3<f32>, @location(1) color: vec4<f32>, }
struct Output { @builtin(position) position: vec4<f32>, @location(0) color: vec4<f32>, }
@vertex fn vs_main(in: Input) -> Output { var out: Output; out.position = camera.mvp * vec4<f32>(in.position, 1.0); out.color = in.color; return out; }
@fragment fn fs_main(in: Output) -> @location(0) vec4<f32> { return in.color; }
`
