//go:build !miqt

package main

import (
	"errors"

	"github.com/ianbruene/ddgo/internal/app"
)

var errMIQTNotBuilt = errors.New("MIQT UI not built; rebuild with -tags miqt")

func runUI(_ *app.Controller) error {
	return errMIQTNotBuilt
}
