package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	junit "github.com/joshdk/go-junit"
	"github.com/kelseyhightower/envconfig"
	"github.com/kubeshop/testkube/pkg/api/v1/testkube"
	"github.com/kubeshop/testkube/pkg/executor"
	"github.com/kubeshop/testkube/pkg/executor/content"
	"github.com/kubeshop/testkube/pkg/executor/output"
)

var ginkgoDefaultParams = InitializeGinkgoParams(make(map[string]string))
var ginkgoBin = "ginkgo"

type Params struct {
	GitUsername string `required:"true"` // RUNNER_GITUSERNAME
	GitToken    string `required:"true"` // RUNNER_GITTOKEN
}

func NewGinkgoRunner() (*GinkgoRunner, error) {
	var params Params
	err := envconfig.Process("runner", &params)
	if err != nil {
		return nil, err
	}

	runner := &GinkgoRunner{
		Fetcher: content.NewFetcher(""),
		Params:  params,
	}

	return runner, nil
}

// ExampleRunner for template - change me to some valid runner
type GinkgoRunner struct {
	Params  Params
	Fetcher content.ContentFetcher
}

func (r *GinkgoRunner) Run(execution testkube.Execution) (result testkube.ExecutionResult, err error) {
	err = r.Validate(execution)
	if err != nil {
		return result, err
	}

	// Set github user and token params in Content.Repository
	if r.Params.GitUsername != "" && r.Params.GitToken != "" {
		if execution.Content != nil && execution.Content.Repository != nil {
			execution.Content.Repository.Username = r.Params.GitUsername
			execution.Content.Repository.Token = r.Params.GitToken
		}
	}

	// use `execution.Variables` for variables passed from Test/Execution
	// variables of type "secret" will be automatically decoded
	path, err := r.Fetcher.Fetch(execution.Content)
	if err != nil {
		return result, err
	}
	output.PrintEvent("created content path", path)
	lsout, err := executor.Run(path, "ls")
	if err == nil {
		fmt.Println(lsout)
	}

	_, err = executor.Run(path, "ginkgo", "version")
	if err != nil {
		return result, fmt.Errorf("ginkgo binary not found?: %w", err)
	}

	// Set up ginkgo command and potential args
	ginkgoParams := FindGinkgoParams(&execution, ginkgoDefaultParams)
	ginkgoArgs := BuildGinkgoArgs(ginkgoParams)
	ginkgoPassThroughFlags := BuildGinkgoPassThroughFlags(execution.Variables)
	ginkgoArgsAndFlags := ginkgoArgs + " " + ginkgoPassThroughFlags
	_, err = os.Stat(filepath.Join(path, "vendor"))
	vendorParam := ""
	if err == nil {
		output.PrintEvent("found vendor dir, no need to install go modules")
		vendorParam = "--mod vendor"
		ginkgoArgs = vendorParam + " " + ginkgoArgs
	}

	fmt.Println("ginkgo bin:", ginkgoBin)
	fmt.Println("args and pass through flags:", ginkgoArgsAndFlags)

	// run executor here
	out, err := executor.Run(path, ginkgoBin, ginkgoArgsAndFlags)
	suites, serr := junit.IngestFile(ginkgoParams["GinkgoJunitReport"])
	result = MapJunitToExecutionResults(out, suites)

	return result.WithErrors(err, serr), nil
}

func InitializeGinkgoParams(ginkgoParams map[string]string) map[string]string {
	ginkgoParams["GinkgoTestPackage"] = "."
	ginkgoParams["GinkgoRecursive"] = "-r"                          // -r
	ginkgoParams["GinkgoParallel"] = "-p"                           // -p
	ginkgoParams["GinkgoParallelProcs"] = ""                        // --procs=N
	ginkgoParams["GinkgoCompilers"] = ""                            // --compilers=N
	ginkgoParams["GinkgoRandomize"] = "--randomize-all"             // --randomize-all
	ginkgoParams["GinkgoRandomizeSuites"] = "--randomize-suites"    // --randomize-suites
	ginkgoParams["GinkgoLabelFilter"] = ""                          // --label-filter=QUERY
	ginkgoParams["GinkgoFocusFilter"] = ""                          // --focus=REGEXP
	ginkgoParams["GinkgoSkipFilter"] = ""                           // --skip=REGEXP
	ginkgoParams["GinkgoUntilItFails"] = ""                         // --until-it-fails
	ginkgoParams["GinkgoRepeat"] = ""                               // --repeat=N
	ginkgoParams["GinkgoFlakeAttempts"] = ""                        // --flake-attempts=N
	ginkgoParams["GinkgoTimeout"] = ""                              // --timeout=duration
	ginkgoParams["GinkgoSkipPackage"] = ""                          // --skip-package=list,of,packages
	ginkgoParams["GinkgoFailFast"] = ""                             // --fail-fast
	ginkgoParams["GinkgoKeepGoing"] = "--keep-going"                // --keep-going
	ginkgoParams["GinkgoFailOnPending"] = ""                        // --fail-on-pending
	ginkgoParams["GinkgoCover"] = ""                                // --cover
	ginkgoParams["GinkgoCoverProfile"] = ""                         // --coverprofile=cover.profile
	ginkgoParams["GinkgoRace"] = ""                                 // --race
	ginkgoParams["GinkgoTrace"] = "--trace"                         // --trace
	ginkgoParams["GinkgoJsonReport"] = "--json-report=report.json"  // --json-report=report.json
	ginkgoParams["GinkgoJunitReport"] = "--junit-report=report.xml" // --junit-report=report.xml
	ginkgoParams["GinkgoTeamCityReport"] = ""                       // --teamcity-report=report.teamcity
	output.PrintEvent("Initialized Ginkgo Parameters")
	return ginkgoParams
}

// Find any GinkgoParams in execution.Variables
func FindGinkgoParams(execution *testkube.Execution, defaultParams map[string]string) map[string]string {
	vars := execution.Variables
	var retVal = make(map[string]string)
	for k, p := range defaultParams {
		v, found := vars[k]
		if found {
			retVal[k] = v.Value
			delete(execution.Variables, k)
		} else {
			retVal[k] = p
		}
	}
	output.PrintEvent("matched up Ginkgo param defaults with those provided")
	output.PrintEvent("execution.Variables:", execution.Variables)
	return retVal
}

func BuildGinkgoArgs(params map[string]string) string {
	args := []string{}
	for k, p := range params {
		if k != "GinkgoTestPackage" {
			args = append(args, p)
		}
	}
	args = append(args, params["GinkgoTestPackage"])
	retVal := strings.Join(args, " ")
	pattern := regexp.MustCompile(`\s+`)
	retVal = pattern.ReplaceAllString(retVal, " ")
	output.PrintEvent("created ginkgo args string")
	return retVal
}

// This should always be called after FindGinkgoParams so that it only
// acts on the "left over" Variables that are to be treated as pass through
// flags to GInkgo
func BuildGinkgoPassThroughFlags(vars testkube.Variables) string {
	flags := []string{}
	for _, v := range vars {
		flag := "--" + v.Name + "=" + v.Value
		flags = append(flags, flag)
	}
	retVal := strings.Join(flags, " ")
	if retVal != "" {
		retVal = "-- " + retVal
	}
	output.PrintEvent("created ginkgo pass through flags string", retVal)
	return retVal
}

// Validate checks if Execution has valid data in context of Cypress executor
func (r *GinkgoRunner) Validate(execution testkube.Execution) error {

	if execution.Content == nil {
		return fmt.Errorf("can't find any content to run in execution data: %+v", execution)
	}

	if execution.Content.Repository == nil {
		return fmt.Errorf("ginkgo executor handles only repository based tests, but repository is nil")
	}

	if execution.Content.Repository.Branch == "" {
		return fmt.Errorf("can't find branch in params, repo:%+v", execution.Content.Repository)
	}

	if execution.Content.IsFile() {
		return fmt.Errorf("passing ginkgo test as single file not implemented yet")
	}
	return nil
}

func MapJunitToExecutionResults(out []byte, suites []junit.Suite) (result testkube.ExecutionResult) {
	status := testkube.PASSED_ExecutionStatus
	result.Status = &status
	result.Output = string(out)
	result.OutputType = "text/plain"

	for _, suite := range suites {
		for _, test := range suite.Tests {

			result.Steps = append(
				result.Steps,
				testkube.ExecutionStepResult{
					Name:     fmt.Sprintf("%s - %s", suite.Name, test.Name),
					Duration: test.Duration.String(),
					Status:   MapStatus(test.Status),
				})
		}

		// TODO parse sub suites recursively

	}

	return result
}

func MapStatus(in junit.Status) (out string) {
	switch string(in) {
	case "passed":
		return string(testkube.PASSED_ExecutionStatus)
	default:
		return string(testkube.FAILED_ExecutionStatus)
	}
}
