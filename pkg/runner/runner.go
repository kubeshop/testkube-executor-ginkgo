package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	junit "github.com/joshdk/go-junit"
	"github.com/kelseyhightower/envconfig"
	"github.com/kubeshop/testkube/pkg/api/v1/testkube"
	"github.com/kubeshop/testkube/pkg/executor"
	"github.com/kubeshop/testkube/pkg/executor/content"
	"github.com/kubeshop/testkube/pkg/executor/scraper"
	"github.com/kubeshop/testkube/pkg/executor/secret"
)

var ginkgoDefaultParams = InitializeGinkgoParams()
var ginkgoBin = "ginkgo"

type Params struct {
	// GitHub Params
	GitUsername string `required:"true"` // RUNNER_GITUSERNAME
	GitToken    string `required:"true"` // RUNNER_GITTOKEN

	// Scraper Params
	Endpoint        string // RUNNER_ENDPOINT
	AccessKeyID     string // RUNNER_ACCESSKEYID
	SecretAccessKey string // RUNNER_SECRETACCESSKEY
	Location        string // RUNNER_LOCATION
	Token           string // RUNNER_TOKEN
	Ssl             bool   // RUNNER_SSL
	ScrapperEnabled bool   // RUNNER_SCRAPPERENABLED
	DataDir         string // RUNNER_DATADIR
}

func NewGinkgoRunner() (*GinkgoRunner, error) {
	var params Params
	err := envconfig.Process("runner", &params)
	if err != nil {
		return nil, err
	}

	runner := &GinkgoRunner{
		Fetcher: content.NewFetcher(""),
		Scraper: scraper.NewMinioScraper(
			params.Endpoint,
			params.AccessKeyID,
			params.SecretAccessKey,
			params.Location,
			params.Token,
			params.Ssl,
		),
		Params: params,
	}

	return runner, nil
}

type GinkgoRunner struct {
	Params  Params
	Fetcher content.ContentFetcher
	Scraper scraper.Scraper
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

	// convert executor env variables to os env variables
	for key, value := range execution.Envs {
		if err = os.Setenv(key, value); err != nil {
			return result, fmt.Errorf("setting env var: %w", err)
		}
	}

	// use `execution.Variables` for variables passed from Test/Execution
	// variables of type "secret" will be automatically decoded
	envManager := secret.NewEnvManagerWithVars(execution.Variables)
	envManager.GetVars(execution.Variables)
	path, err := r.Fetcher.Fetch(execution.Content)
	if err != nil {
		return result, err
	}

	// Set up ginkgo params
	ginkgoParams := FindGinkgoParams(&execution, ginkgoDefaultParams)

	runPath := path
	if execution.Content.Repository != nil && execution.Content.Repository.WorkingDir != "" {
		runPath = filepath.Join(r.Params.DataDir, "repo", execution.Content.Repository.WorkingDir)
		path = filepath.Join(r.Params.DataDir, "repo", execution.Content.Repository.Path)
	}

	// Set up ginkgo potential args
	ginkgoArgs, err := BuildGinkgoArgs(ginkgoParams, path, runPath)
	if err != nil {
		return result, err
	}
	ginkgoPassThroughFlags := BuildGinkgoPassThroughFlags(execution)
	ginkgoArgsAndFlags := append(ginkgoArgs, ginkgoPassThroughFlags...)

	// set up reports directory
	reportsPath := filepath.Join(path, "reports")
	if _, err := os.Stat(reportsPath); os.IsNotExist(err) {
		mkdirErr := os.Mkdir(reportsPath, os.ModePerm)
		if mkdirErr != nil {
			return result, mkdirErr
		}
	}

	// run executor here
	out, err := executor.Run(runPath, ginkgoBin, envManager, ginkgoArgsAndFlags...)
	out = envManager.Obfuscate(out)

	// generate report/result
	if ginkgoParams["GinkgoJsonReport"] != "" {
		moveErr := MoveReport(runPath, reportsPath, strings.Split(ginkgoParams["GinkgoJsonReport"], " ")[1])
		if moveErr != nil {
			return result, moveErr
		}
	}
	if ginkgoParams["GinkgoJunitReport"] != "" {
		moveErr := MoveReport(runPath, reportsPath, strings.Split(ginkgoParams["GinkgoJunitReport"], " ")[1])
		if moveErr != nil {
			return result, moveErr
		}
	}
	if ginkgoParams["GinkgoTeamCityReport"] != "" {
		moveErr := MoveReport(runPath, reportsPath, strings.Split(ginkgoParams["GinkgoTeamCityReport"], " ")[1])
		if moveErr != nil {
			return result, moveErr
		}
	}
	suites, serr := junit.IngestFile(filepath.Join(reportsPath, strings.Split(ginkgoParams["GinkgoJunitReport"], " ")[1]))
	result = MapJunitToExecutionResults(out, suites)

	// scrape artifacts first even if there are errors above

	if r.Params.ScrapperEnabled {
		directories := []string{
			reportsPath,
		}
		err := r.Scraper.Scrape(execution.Id, directories)
		if err != nil {
			return result.WithErrors(fmt.Errorf("scrape artifacts error: %w", err)), nil
		}
	}

	return result.WithErrors(err, serr), nil
}

func MoveReport(path string, reportsPath string, reportFileName string) error {
	oldpath := filepath.Join(path, reportFileName)
	newpath := filepath.Join(reportsPath, reportFileName)
	err := os.Rename(oldpath, newpath)
	if err != nil {
		return err
	}
	return nil
}

func InitializeGinkgoParams() map[string]string {
	ginkgoParams := make(map[string]string)
	ginkgoParams["GinkgoTestPackage"] = ""
	ginkgoParams["GinkgoRecursive"] = "-r"                          // -r
	ginkgoParams["GinkgoParallel"] = "-p"                           // -p
	ginkgoParams["GinkgoParallelProcs"] = ""                        // --procs N
	ginkgoParams["GinkgoCompilers"] = ""                            // --compilers N
	ginkgoParams["GinkgoRandomize"] = "--randomize-all"             // --randomize-all
	ginkgoParams["GinkgoRandomizeSuites"] = "--randomize-suites"    // --randomize-suites
	ginkgoParams["GinkgoLabelFilter"] = ""                          // --label-filter QUERY
	ginkgoParams["GinkgoFocusFilter"] = ""                          // --focus REGEXP
	ginkgoParams["GinkgoSkipFilter"] = ""                           // --skip REGEXP
	ginkgoParams["GinkgoUntilItFails"] = ""                         // --until-it-fails
	ginkgoParams["GinkgoRepeat"] = ""                               // --repeat N
	ginkgoParams["GinkgoFlakeAttempts"] = ""                        // --flake-attempts N
	ginkgoParams["GinkgoTimeout"] = ""                              // --timeout=duration
	ginkgoParams["GinkgoSkipPackage"] = ""                          // --skip-package list,of,packages
	ginkgoParams["GinkgoFailFast"] = ""                             // --fail-fast
	ginkgoParams["GinkgoKeepGoing"] = "--keep-going"                // --keep-going
	ginkgoParams["GinkgoFailOnPending"] = ""                        // --fail-on-pending
	ginkgoParams["GinkgoCover"] = ""                                // --cover
	ginkgoParams["GinkgoCoverProfile"] = ""                         // --coverprofile cover.profile
	ginkgoParams["GinkgoRace"] = ""                                 // --race
	ginkgoParams["GinkgoTrace"] = "--trace"                         // --trace
	ginkgoParams["GinkgoJsonReport"] = ""                           // --json-report report.json [will be stored in reports/filename]
	ginkgoParams["GinkgoJunitReport"] = "--junit-report report.xml" // --junit-report report.xml [will be stored in reports/filename]
	ginkgoParams["GinkgoTeamCityReport"] = ""                       // --teamcity-report report.teamcity [will be stored in reports/filename]
	return ginkgoParams
}

// Find any GinkgoParams in execution.Variables
func FindGinkgoParams(execution *testkube.Execution, defaultParams map[string]string) map[string]string {
	var retVal = make(map[string]string)
	for k, p := range defaultParams {
		v, found := execution.Variables[k]
		if found {
			retVal[k] = v.Value
			delete(execution.Variables, k)
		} else {
			if p != "" {
				retVal[k] = p
			}
		}
	}
	return retVal
}

func BuildGinkgoArgs(params map[string]string, path, runPath string) ([]string, error) {
	args := []string{}
	for k, p := range params {
		if k != "GinkgoTestPackage" {
			args = append(args, strings.Split(p, " ")...)
		}
	}

	if params["GinkgoTestPackage"] != "" {
		if path != runPath {
			args = append(args, filepath.Join(path, params["GinkgoTestPackage"]))
		} else {
			args = append(args, params["GinkgoTestPackage"])
		}
	} else {
		if path != runPath {
			args = append(args, path)
		}
	}

	return args, nil
}

// This should always be called after FindGinkgoParams so that it only
// acts on the "left over" Variables that are to be treated as pass through
// flags to GInkgo
func BuildGinkgoPassThroughFlags(execution testkube.Execution) []string {
	vars := execution.Variables
	args := execution.Args
	flags := []string{}
	for _, v := range vars {
		flag := "--" + v.Name + "=" + v.Value
		flags = append(flags, flag)
	}

	if len(args) > 0 {
		flags = append(flags, args...)
	}

	if len(flags) > 0 {
		flags = append([]string{"--"}, flags...)
	}

	return flags
}

// Validate checks if Execution has valid data in context of Cypress executor
func (r *GinkgoRunner) Validate(execution testkube.Execution) error {

	if execution.Content == nil {
		return fmt.Errorf("can't find any content to run in execution data: %+v", execution)
	}

	if execution.Content.Repository == nil {
		return fmt.Errorf("ginkgo executor handles only repository based tests, but repository is nil")
	}

	if execution.Content.Repository.Branch == "" && execution.Content.Repository.Commit == "" {
		return fmt.Errorf("can't find branch or commit in params must use one or the other, repo:%+v", execution.Content.Repository)
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
	overallStatusFailed := false
	for _, suite := range suites {
		for _, test := range suite.Tests {
			result.Steps = append(
				result.Steps,
				testkube.ExecutionStepResult{
					Name:     fmt.Sprintf("%s - %s", suite.Name, test.Name),
					Duration: test.Duration.String(),
					Status:   MapStatus(test.Status),
				})
			if test.Status == junit.Status(testkube.FAILED_ExecutionStatus) {
				overallStatusFailed = true
			}
		}

		// TODO parse sub suites recursively

	}
	if overallStatusFailed {
		result.Status = testkube.ExecutionStatusFailed
	} else {
		result.Status = testkube.ExecutionStatusPassed
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
