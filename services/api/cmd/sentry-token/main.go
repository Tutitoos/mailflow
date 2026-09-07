package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	mailflowsentry "github.com/Tutitoos/mailflow/services/api/internal/modules/sentry"
)

func main() {
	if len(os.Args) != 2 {
		fail("usage: sentry-token <api|web|desktop|ios>")
	}
	component := strings.ToLower(strings.TrimSpace(os.Args[1]))
	secretFile := os.Getenv("MAILFLOW_MASTER_KEY_FILE")
	if secretFile == "" {
		fail("MAILFLOW_MASTER_KEY_FILE is required")
	}
	encoded, err := os.ReadFile(secretFile)
	if err != nil {
		fail("cannot read the installation master key")
	}
	masterKey, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil || len(masterKey) != 32 {
		fail("the installation master key is invalid")
	}
	projects, err := mailflowsentry.DerivedProjects(masterKey)
	if err != nil {
		fail("cannot derive Sentry artifact tokens")
	}
	for _, project := range projects {
		if project.Component == component {
			fmt.Println(project.ArtifactToken)
			return
		}
	}
	fail("component must be api, web, desktop, or ios")
}

func fail(message string) {
	_, _ = fmt.Fprintln(os.Stderr, message)
	os.Exit(2)
}
