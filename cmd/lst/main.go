package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "lst:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a := &app{}
	defer a.close()

	return newRootCmd(a).ExecuteContext(ctx)
}

func newRootCmd(a *app) *cobra.Command {
	root := &cobra.Command{
		Use:           "lst",
		Short:         "Property Radar operator CLI",
		Long:          "Inspect the Property Radar corpus and provider health.",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(*cobra.Command, []string) error {
			return a.loadConfig()
		},
	}
	root.AddCommand(
		newListCmd(a),
		newShowCmd(a),
		newStatusCmd(a),
		newDoctorCmd(),
	)
	return root
}
