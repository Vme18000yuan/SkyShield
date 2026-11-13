package subdomaindiscoveryruntime

import (
	"context"
	"github.com/yyhuni/lunafox/engine-go/stream"
	"github.com/yyhuni/lunafox/engines/subdomain_discovery/internal/validator"
	"log"

	subdomainspec "github.com/yyhuni/lunafox/engines/subdomain_discovery/contract"
)

// runResolveStage resolves the normalized union of the upstream discovery artifacts.
func (w *Runtime) runResolveStage(execCtx context.Context, run *discoveryRun, inputFiles []string) stageOutcome {
	const stageLabel = "3/3 resolve"
	if run.progress != nil {
		if err := run.progress.report("start stage 3/3 resolve: verify discovered subdomains"); err != nil {
			return stageOutcome{failed: []string{subdomainspec.StageResolve}}
		}
	}

	inputFile, err := deduplicateSubdomainArtifacts(execCtx, run.workspaceDir, inputFiles)
	if err != nil {
		log.Printf("failed to deduplicate subdomain input stage=%s error=%v", subdomainspec.StageResolve, err)
		reportStageFailure(run, stageLabel, "input: "+err.Error())
		return stageFailureOutcome(subdomainspec.StageResolve, "input: "+err.Error())
	}
	if countFileLines(inputFile) == 0 {
		reportStageSkip(run, stageLabel, "no previous outputs")
		return stageOutcome{}
	}

	outputPath, err := run.allocateWorkspaceFilePath("resolve_puredns")
	if err != nil {
		log.Printf("failed to allocate resolve output path error=%v", err)
		reportStageFailure(run, stageLabel, "workspace: "+err.Error())
		return stageFailureOutcome(subdomainspec.StageResolve, "workspace: "+err.Error())
	}
	tool, args := buildPurednsResolveInvocation(inputFile, outputPath, run.resolveResolversPath, run.typedConfig.Resolve)
	command := buildToolInvocation(tool, args, run.typedConfig.Resolve.Timeout)

	log.Printf("running DNS resolve stage input.file_count=%d timeout.seconds=%d", len(inputFiles), command.TimeoutSeconds)
	var result commandResult
	execute := func(toolCtx context.Context) error {
		result = executeStageCommand(toolCtx, run, subdomainspec.StageResolve, command, outputPath)
		return result.Error
	}
	if w.results != nil {
		err := stream.Submit(execCtx, w.results.Submit, func(liveCtx context.Context, emit func(subdomainspec.Subdomain) error) error {
			return stream.File(liveCtx, outputPath, 4*1024*1024, execute, func(line string, oversized bool) error {
				if oversized {
					return nil
				}
				name, ok := validator.NormalizeSubdomainLine(line)
				if !ok {
					return nil
				}
				return emit(subdomainspec.Subdomain{DNSName: name})
			})
		})
		if err != nil {
			result = failedCommandResult(subdomainspec.StageResolve, command, err)
		}
	} else {
		_ = execute(execCtx)
	}
	outcome := collectCommandOutcomes([]commandResult{result})
	if len(outcome.outputArtifactPaths) == 1 {
		outcome.resultArtifactPath = outcome.outputArtifactPaths[0]
	}
	reportStageComplete(run, stageLabel, outcome)
	return outcome
}
