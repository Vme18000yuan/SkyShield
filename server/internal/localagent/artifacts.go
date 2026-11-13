package localagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/yyhuni/lunafox/contracts/executionartifact"
	data "github.com/yyhuni/lunafox/contracts/gen/lunafox/agent/data/v1"
	execution "github.com/yyhuni/lunafox/contracts/gen/lunafox/agent/execution/v1"
	engine "github.com/yyhuni/lunafox/engine-go/protocol"
	planauth "github.com/yyhuni/lunafox/server/internal/grpc/planeauth"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

const maxLocalArtifactBytes = 1 << 30

type artifactReceiver interface {
	Recv() (*data.ExecutionArtifactFrame, error)
}

func scopedContext(ctx context.Context, identity credentials, sessionID string, epoch int64) context.Context {
	return planauth.WithAgentSession(planauth.WithAgentAuthenticationToken(ctx, identity.AuthenticationToken), sessionID, epoch)
}

// receiveArtifact publishes only a complete stream whose header, trailer and
// content digest all agree. Incomplete transfers never become Engine inputs.
func receiveArtifact(stream artifactReceiver, destination, task, executionID, contentType string, binding func(*data.ExecutionArtifactHeader) bool) error {
	frame, err := stream.Recv()
	if err != nil || frame.GetHeader() == nil {
		return errors.New("artifact stream did not begin with a header")
	}
	header := frame.GetHeader()
	if header.GetTask() != task || header.GetExecution() != executionID || header.GetContentType() != contentType || !binding(header) {
		return errors.New("artifact header does not match the assigned plan")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(destination), ".artifact-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	digest := sha256.New()
	var size uint64
	var trailer *data.ExecutionArtifactIntegrity
	for {
		frame, err = stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if frame == nil || trailer != nil {
			return errors.New("invalid artifact stream framing")
		}
		switch payload := frame.GetPayload().(type) {
		case *data.ExecutionArtifactFrame_Chunk:
			if payload.Chunk == nil || len(payload.Chunk.GetData()) > 65536 || size+uint64(len(payload.Chunk.GetData())) > maxLocalArtifactBytes {
				return errors.New("artifact stream exceeds local limits")
			}
			chunk := payload.Chunk.GetData()
			if _, err := file.Write(chunk); err != nil {
				return err
			}
			_, _ = digest.Write(chunk)
			size += uint64(len(chunk))
		case *data.ExecutionArtifactFrame_Trailer:
			if payload.Trailer == nil || payload.Trailer.GetIntegrity() == nil {
				return errors.New("artifact trailer is missing")
			}
			trailer = payload.Trailer.GetIntegrity()
		default:
			return errors.New("unexpected artifact stream frame")
		}
	}
	if trailer == nil || trailer.GetSizeBytes() != size || trailer.GetSha256Digest() != "sha256:"+hex.EncodeToString(digest.Sum(nil)) || (header.ExpectedIntegrity != nil && !proto.Equal(header.ExpectedIntegrity, trailer)) {
		return errors.New("artifact integrity verification failed")
	}
	if err := file.Chmod(0o400); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), destination)
}

func safeComponent(value string) bool {
	return value != "" && value != "." && value != ".." && value == strings.TrimSpace(value) && filepath.Base(value) == value && !strings.ContainsAny(value, "\\/\x00\r\n")
}

func materializeConfigResources(ctx context.Context, client data.ExecutionArtifactServiceClient, plan *execution.ResolvedEngineExecutionPlan, root string) ([]*engine.ConfigResource, error) {
	items := make([]*engine.ConfigResource, 0, len(plan.GetConfigResourceBindings()))
	for _, binding := range plan.GetConfigResourceBindings() {
		if binding == nil || binding.GetWordlist() == nil || !safeComponent(binding.GetSectionId()) || !safeComponent(binding.GetParamKey()) || !safeComponent(binding.GetWordlist().GetBasename()) {
			return nil, errors.New("invalid local configuration resource binding")
		}
		path := filepath.Join(root, "runtime", "resources", "config", binding.GetSectionId(), binding.GetParamKey(), binding.GetWordlist().GetBasename())
		stream, err := client.StreamConfigResource(ctx, &data.StreamConfigResourceRequest{Task: plan.GetTask(), Execution: plan.GetExecution(), SectionId: binding.GetSectionId(), ParamKey: binding.GetParamKey()})
		if err != nil {
			return nil, err
		}
		err = receiveArtifact(stream, path, plan.GetTask(), plan.GetExecution(), binding.GetContentType(), func(header *data.ExecutionArtifactHeader) bool {
			key := header.GetConfigResource()
			want := binding.GetWordlist()
			integrity := header.GetExpectedIntegrity()
			return key != nil && key.GetSectionId() == binding.GetSectionId() && key.GetParamKey() == binding.GetParamKey() && integrity != nil && integrity.GetSha256Digest() == want.GetSha256Digest() && integrity.GetSizeBytes() == want.GetSizeBytes() && integrity.RecordCount != nil && integrity.GetRecordCount() == want.GetLineCount()
		})
		if err != nil {
			return nil, fmt.Errorf("materialize configuration resource: %w", err)
		}
		items = append(items, &engine.ConfigResource{SectionId: binding.GetSectionId(), ParamKey: binding.GetParamKey(), ContentType: binding.GetContentType(), Path: path})
	}
	return items, nil
}

func materializePlatformResources(ctx context.Context, client data.ExecutionArtifactServiceClient, plan *execution.ResolvedEngineExecutionPlan, root string) ([]*engine.PlatformResource, error) {
	items := make([]*engine.PlatformResource, 0, len(plan.GetPlatformResourceBindings()))
	for _, binding := range plan.GetPlatformResourceBindings() {
		if binding == nil {
			return nil, errors.New("invalid local platform resource binding")
		}
		descriptor, ok := executionartifact.LookupPlatformResource(binding.GetResourceId())
		if !ok || !safeComponent(binding.GetResourceId()) || !safeComponent(descriptor.Filename) {
			return nil, errors.New("unsupported platform resource")
		}
		path := filepath.Join(root, "runtime", "resources", "platform", binding.GetResourceId(), descriptor.Filename)
		stream, err := client.StreamPlatformResourceContent(ctx, &data.StreamPlatformResourceContentRequest{Task: plan.GetTask(), Execution: plan.GetExecution(), ResourceId: binding.GetResourceId()})
		if err != nil {
			return nil, err
		}
		err = receiveArtifact(stream, path, plan.GetTask(), plan.GetExecution(), binding.GetContentType(), func(header *data.ExecutionArtifactHeader) bool {
			return header.GetPlatformResource() != nil && header.GetPlatformResource().GetResourceId() == binding.GetResourceId()
		})
		if err != nil {
			return nil, fmt.Errorf("materialize platform resource: %w", err)
		}
		items = append(items, &engine.PlatformResource{ResourceId: binding.GetResourceId(), ContentType: binding.GetContentType(), Path: path})
	}
	return items, nil
}

func inputStream(ctx context.Context, conn *grpc.ClientConn, plan *execution.ResolvedEngineExecutionPlan, role, root string) (string, error) {
	descriptor, ok := executionartifact.LookupRoleID(role)
	if !ok {
		return "", errors.New("unsupported input role")
	}
	path := filepath.Join(root, "runtime", "inputs", filepath.Base(descriptor.ContainerPath))
	stream, err := data.NewExecutionArtifactServiceClient(conn).StreamExecutionInput(ctx, &data.StreamExecutionInputRequest{Task: plan.GetTask(), Execution: plan.GetExecution(), Role: role})
	if err != nil {
		return "", err
	}
	err = receiveArtifact(stream, path, plan.GetTask(), plan.GetExecution(), descriptor.ContentType, func(header *data.ExecutionArtifactHeader) bool {
		return header.GetExecutionInput() != nil && header.GetExecutionInput().GetRole() == role
	})
	return path, err
}
