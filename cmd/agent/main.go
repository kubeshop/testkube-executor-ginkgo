package main

import (
	"fmt"
	"os"

	"github.com/kubeshop/testkube-executor-ginkgo/pkg/runner"
	"github.com/kubeshop/testkube/pkg/executor/agent"
	"github.com/kubeshop/testkube/pkg/executor/output"
)

func main() {
	ginko, err := runner.NewGinkgoRunner()
	if err != nil {
		output.PrintError(fmt.Errorf("could not initialize runner: %w", err))
		os.Exit(1)
	}
	agent.Run(ginko, os.Args)
}
