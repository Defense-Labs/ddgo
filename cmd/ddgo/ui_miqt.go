//go:build miqt

package main

import (
	"github.com/ianbruene/ddgo/internal/app"
	frontendmiqt "github.com/ianbruene/ddgo/internal/frontend/miqt"
)

func runUI(controller *app.Controller) error {
	return frontendmiqt.Run(controller)
}
