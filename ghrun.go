package main

// Running the GitHub CLI.
//
// This file is the same in every tool of the family that runs gh.

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// ghRun runs gh and returns its stdout. A failure is said the way gh said it
// (the first line of its stderr), and a missing gh in plain words instead of
// exec's. On a failure the stdout gh did print comes back with the error: a
// GraphQL response with errors is an exit 1 that still carries its data, and
// a caller may want it. The tests replace it.
var ghRun = func(ctx context.Context, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, "gh", args...).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return out, fmt.Errorf("gh: %s", firstLine(string(ee.Stderr)))
		}
		if errors.Is(err, exec.ErrNotFound) {
			return nil, errors.New("gh not found (install the GitHub CLI)")
		}
		return out, fmt.Errorf("gh: %w", err)
	}
	return out, nil
}
