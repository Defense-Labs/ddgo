// SPDX-License-Identifier: Unlicense OR MIT

//go:build linux

package main

import (
	"image"
	"unsafe"

	"gioui.org/app"
)

/*
#cgo linux pkg-config: egl wayland-egl
#cgo CFLAGS: -DEGL_NO_X11
#include <EGL/egl.h>
#include <wayland-client.h>
#include <wayland-egl.h>
*/
import "C"

// This is the Linux platform integration copied/adapted from gio-example/opengl.
func getDisplay(ve app.ViewEvent) C.EGLDisplay {
	switch v := ve.(type) {
	case app.X11ViewEvent:
		return C.eglGetDisplay(C.EGLNativeDisplayType(v.Display))
	case app.WaylandViewEvent:
		return C.eglGetDisplay(C.EGLNativeDisplayType(v.Display))
	}
	panic("no EGL display for Gio view")
}

func nativeViewFor(e app.ViewEvent, size image.Point) (C.EGLNativeWindowType, func()) {
	switch v := e.(type) {
	case app.X11ViewEvent:
		return C.EGLNativeWindowType(uintptr(v.Window)), func() {}
	case app.WaylandViewEvent:
		win := C.wl_egl_window_create((*C.struct_wl_surface)(v.Surface), C.int(size.X), C.int(size.Y))
		return C.EGLNativeWindowType(uintptr(unsafe.Pointer(win))), func() { C.wl_egl_window_destroy(win) }
	}
	panic("no EGL native view for Gio view")
}

func nativeViewResize(e app.ViewEvent, view C.EGLNativeWindowType, size image.Point) {
	if _, ok := e.(app.WaylandViewEvent); ok {
		C.wl_egl_window_resize(*(**C.struct_wl_egl_window)(unsafe.Pointer(&view)), C.int(size.X), C.int(size.Y), 0, 0)
	}
}
