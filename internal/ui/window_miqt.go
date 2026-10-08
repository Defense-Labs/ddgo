//go:build miqt

package ui

import (
	"github.com/ianbruene/ddgo/internal/app"
	frontendmiqt "github.com/ianbruene/ddgo/internal/frontend/miqt"
)

func Run(controller *app.Controller) error {
	return frontendmiqt.Run(controller)
}
