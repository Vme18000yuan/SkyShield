package localagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	data "github.com/yyhuni/lunafox/contracts/gen/lunafox/agent/data/v1"
	execution "github.com/yyhuni/lunafox/contracts/gen/lunafox/agent/execution/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type templateExchangeFixture struct {
	data.UnimplementedExecutionArtifactServiceServer
	path string
	done chan error
}

func (fixture *templateExchangeFixture) ExchangeRuntimeArtifact(stream grpc.BidiStreamingServer[data.ExchangeRuntimeArtifactRequest, data.ExchangeRuntimeArtifactResponse]) error {
	begin, err := stream.Recv()
	if err != nil {
		return err
	}
	if begin.GetBegin() == nil || begin.GetBegin().GetArtifactId() != "nucleiTemplates" {
		return fmt.Errorf("unexpected begin")
	}
	content := []byte("id: fixture\ninfo:\n  name: fixture\n")
	sum := sha256.Sum256(content)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	entries := []templateEntry{{RelativePath: fixture.path, SHA256Digest: digest, SizeBytes: uint64(len(content))}}
	encoded, _ := json.Marshal(entries)
	snapshot := sha256.Sum256(encoded)
	snapshotDigest := "sha256:" + hex.EncodeToString(snapshot[:])
	if err := stream.Send(&data.ExchangeRuntimeArtifactResponse{Payload: &data.ExchangeRuntimeArtifactResponse_ManifestFrame{ManifestFrame: &data.RuntimeArtifactManifestFrame{Sequence: 1, Entries: []*data.RuntimeArtifactManifestEntry{{RelativePath: fixture.path, Sha256Digest: digest, SizeBytes: uint64(len(content))}}}}}); err != nil {
		return err
	}
	if err := stream.Send(&data.ExchangeRuntimeArtifactResponse{Payload: &data.ExchangeRuntimeArtifactResponse_ManifestEnd{ManifestEnd: &data.RuntimeArtifactManifestEnd{Sequence: 2, EntryCount: 1, TotalSizeBytes: uint64(len(content)), SnapshotDigest: snapshotDigest}}}); err != nil {
		return err
	}
	missing, err := stream.Recv()
	if err != nil {
		return err
	}
	end, err := stream.Recv()
	if err != nil {
		return err
	}
	if missing.GetMissingBlobDigests() == nil || missing.GetMissingBlobDigests().GetSequence() != 1 || len(missing.GetMissingBlobDigests().GetDigests()) != 1 || missing.GetMissingBlobDigests().GetDigests()[0] != digest || end.GetMissingEnd().GetSequence() != 2 {
		return fmt.Errorf("unexpected missing blobs")
	}
	if err := stream.Send(&data.ExchangeRuntimeArtifactResponse{Payload: &data.ExchangeRuntimeArtifactResponse_BlobGroup{BlobGroup: &data.RuntimeArtifactBlobGroup{Digest: digest, Sequence: 1, Data: content}}}); err != nil {
		return err
	}
	if err := stream.Send(&data.ExchangeRuntimeArtifactResponse{Payload: &data.ExchangeRuntimeArtifactResponse_BlobEnd{BlobEnd: &data.RuntimeArtifactBlobEnd{Digest: digest, Sequence: 2, SizeBytes: uint64(len(content)), Sha256Digest: digest}}}); err != nil {
		return err
	}
	if err := stream.Send(&data.ExchangeRuntimeArtifactResponse{Payload: &data.ExchangeRuntimeArtifactResponse_ExchangeEnd{ExchangeEnd: &data.RuntimeArtifactExchangeEnd{Sequence: 2}}}); err != nil {
		return err
	}
	ack, err := stream.Recv()
	if err != nil {
		return err
	}
	if ack.GetPublicationAck() == nil || ack.GetPublicationAck().GetSnapshotDigest() != snapshotDigest || ack.GetPublicationAck().GetEntryCount() != 1 {
		return fmt.Errorf("unexpected publication acknowledgement")
	}
	if err := stream.Send(&data.ExchangeRuntimeArtifactResponse{Payload: &data.ExchangeRuntimeArtifactResponse_ExchangeComplete{ExchangeComplete: &data.RuntimeArtifactExchangeComplete{SnapshotDigest: snapshotDigest, EntryCount: 1}}}); err != nil {
		return err
	}
	fixture.done <- nil
	return nil
}

func TestLocalTemplateExchange(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	fixture := &templateExchangeFixture{path: "http/example.yaml", done: make(chan error, 1)}
	data.RegisterExecutionArtifactServiceServer(server, fixture)
	go server.Serve(listener)
	defer server.Stop()
	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	root := t.TempDir()
	plan := &execution.ResolvedEngineExecutionPlan{Task: "scans/1/tasks/1", Execution: "executions/1", RuntimeArtifactBindings: []*execution.RuntimeArtifactBinding{{ArtifactId: "nucleiTemplates"}}}
	if err := materializeRuntimeArtifacts(ctx, data.NewExecutionArtifactServiceClient(conn), plan, root); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(root, "runtime/resources/runtime/nuclei/templates/http/example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "id: fixture\ninfo:\n  name: fixture\n" {
		t.Fatalf("template content = %q", content)
	}
	select {
	case err := <-fixture.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("exchange did not finish")
	}
}

func TestTemplatePathRejectsTraversal(t *testing.T) {
	for _, value := range []string{"../outside.yaml", "/tmp/outside.yaml", "a/../outside.yaml", "a\\outside.yaml", "a.txt"} {
		if validTemplatePath(value) {
			t.Fatalf("accepted unsafe template path %q", value)
		}
	}
}
