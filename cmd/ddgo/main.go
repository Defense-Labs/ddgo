package main

import (
	"fmt"
	"os"

	"github.com/ianbruene/ddgo/internal/app"
	"github.com/ianbruene/ddgo/internal/ports"
	"github.com/ianbruene/ddgo/internal/transport"
)

func run(uiRunner func(*app.Controller) error) error {
	controller := app.NewController(transport.NewSerialTransport(), ports.ListPorts)
	return uiRunner(controller)
}

func main() {
	if err := run(runUI); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
