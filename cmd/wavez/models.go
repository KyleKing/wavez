package main

import (
	"context"
	"fmt"

	"github.com/kyleking/wavez/internal/runtime"
)

// modelsReport lists what ollama has pulled. Which models are on the disk
// decides what a tier can be pointed at, and answering it meant leaving
// wavez for a second terminal.
func modelsReport(ctx context.Context) error {
	models, err := runtime.ListModels(ctx)
	if err != nil {
		return fmt.Errorf("listing models: %w", err)
	}

	if len(models) == 0 {
		fmt.Println("ollama has no models pulled")

		return nil
	}

	for _, m := range models {
		if _, err := fmt.Printf("%-40s %10s  %s\n", m.Name, m.Size, m.Modified); err != nil {
			return fmt.Errorf("writing the model list: %w", err)
		}
	}

	return nil
}
