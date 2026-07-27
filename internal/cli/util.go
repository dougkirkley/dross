package cli

import (
	"fmt"
	"os"
)

// storePath is where session handoff briefs and artifacts live.
func storePath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	p := configDir + "/holocron/sessions"
	if err := os.MkdirAll(p, 0o755); err != nil {
		return "", err
	}
	return p, nil
}

func mustStorePath() (string, error) {
	p, err := storePath()
	if err != nil {
		return "", fmt.Errorf("storage dir: %w", err)
	}
	return p, nil
}
