package directoryscanruntime

import (
	"context"
	"github.com/yyhuni/lunafox/engine-go/stream"
	contract "github.com/yyhuni/lunafox/engines/directory_scan/contract"
	"strings"
)

// Provisional observations are persisted while FFUF runs. The existing final
// deterministic winner pass remains authoritative when all candidates finish.
type liveFFUFRunner struct {
	inner     ffufProcessRunner
	submitter contract.DirectorySubmitter
}

func (runner liveFFUFRunner) Run(ctx context.Context, invocation ffufInvocation) error {
	path, err := rawFFUFArtifactPath(invocation.Workspace, invocation.Candidate.Ordinal)
	if err != nil {
		return err
	}
	return stream.Submit(ctx, runner.submitter.Submit, func(liveCtx context.Context, emit func(contract.Directory) error) error {
		return stream.File(liveCtx, path, maximumFFUFPhysicalRecordBytes, func(runCtx context.Context) error { return runner.inner.Run(runCtx, invocation) }, func(line string, oversized bool) error {
			if oversized {
				return nil
			}
			_, err := parseFFUFStreamWithOptions(liveCtx, strings.NewReader(line+"\n"), invocation.Candidate.Ordinal, directoryDenoiseOptions{authRedirect: invocation.Config.DenoiseAuthRedirect, backupHTML: invocation.Config.DenoiseBackupHtml}, func(observation DirectoryObservation) error { return emit(observation.Item) })
			return err
		})
	})
}
