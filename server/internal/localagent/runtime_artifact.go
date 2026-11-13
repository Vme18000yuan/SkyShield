package localagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	data "github.com/yyhuni/lunafox/contracts/gen/lunafox/agent/data/v1"
	execution "github.com/yyhuni/lunafox/contracts/gen/lunafox/agent/execution/v1"
	engine "github.com/yyhuni/lunafox/engine-go/protocol"
	"golang.org/x/text/unicode/norm"
)

type templateEntry struct {
	RelativePath string `json:"relativePath"`
	SHA256Digest string `json:"sha256Digest"`
	SizeBytes    uint64 `json:"sizeBytes"`
}

func validDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != 71 {
		return false
	}
	for _, c := range value[7:] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func validTemplatePath(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || strings.ContainsAny(value, "\\\x00\r\n") || path.IsAbs(value) || path.Clean(value) != value || value == ".." || strings.HasPrefix(value, "../") {
		return false
	}
	return path.Ext(value) == ".yaml" || path.Ext(value) == ".yml"
}

// materializeRuntimeArtifacts implements the Server's manifest/blob exchange
// without a shared cache; each task receives a verified, private template tree.
func materializeRuntimeArtifacts(ctx context.Context, client data.ExecutionArtifactServiceClient, plan *execution.ResolvedEngineExecutionPlan, root string) error {
	for _, binding := range plan.GetRuntimeArtifactBindings() {
		if binding == nil || binding.GetArtifactId() != "nucleiTemplates" {
			return errors.New("unsupported local runtime artifact")
		}
		stream, err := client.ExchangeRuntimeArtifact(ctx)
		if err != nil {
			return err
		}
		if err := stream.Send(&data.ExchangeRuntimeArtifactRequest{Payload: &data.ExchangeRuntimeArtifactRequest_Begin{Begin: &data.RuntimeArtifactExchangeBegin{
			Task: plan.GetTask(), Execution: plan.GetExecution(), ArtifactId: binding.GetArtifactId(), CompatibilityRevision: engine.EngineExecutionDiagnosticsCompatibilityRevision,
		}}}); err != nil {
			return err
		}
		var entries []templateEntry
		var total, sequence uint64 = 0, 1
		seenPaths := make(map[string]bool)
		var manifest *data.RuntimeArtifactManifestEnd
		for manifest == nil {
			response, err := stream.Recv()
			if err != nil {
				return err
			}
			switch item := response.GetPayload().(type) {
			case *data.ExchangeRuntimeArtifactResponse_ManifestFrame:
				if item.ManifestFrame == nil || item.ManifestFrame.GetSequence() != sequence || len(item.ManifestFrame.GetEntries()) == 0 {
					return errors.New("invalid template manifest sequence")
				}
				sequence++
				for _, entry := range item.ManifestFrame.GetEntries() {
					if entry == nil || !validTemplatePath(entry.GetRelativePath()) || !validDigest(entry.GetSha256Digest()) || entry.GetSizeBytes() == 0 || entry.GetSizeBytes() > 16<<20 || seenPaths[norm.NFC.String(strings.ToLower(entry.GetRelativePath()))] || len(entries) >= 100000 {
						return errors.New("invalid template manifest entry")
					}
					if len(entries) > 0 && entries[len(entries)-1].RelativePath >= entry.GetRelativePath() {
						return errors.New("template manifest is not ordered")
					}
					total += entry.GetSizeBytes()
					if total > 512<<20 {
						return errors.New("template manifest exceeds local limit")
					}
					seenPaths[norm.NFC.String(strings.ToLower(entry.GetRelativePath()))] = true
					entries = append(entries, templateEntry{entry.GetRelativePath(), entry.GetSha256Digest(), entry.GetSizeBytes()})
				}
			case *data.ExchangeRuntimeArtifactResponse_ManifestEnd:
				manifest = item.ManifestEnd
				if manifest == nil || manifest.GetSequence() != sequence || manifest.GetEntryCount() != uint64(len(entries)) || manifest.GetTotalSizeBytes() != total || len(entries) == 0 || !validDigest(manifest.GetSnapshotDigest()) {
					return errors.New("invalid template manifest trailer")
				}
			default:
				return errors.New("unexpected template manifest response")
			}
		}
		encoded, err := json.Marshal(entries)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(encoded)
		if "sha256:"+hex.EncodeToString(sum[:]) != manifest.GetSnapshotDigest() {
			return errors.New("template manifest digest mismatch")
		}
		unique, sizes := make([]string, 0, len(entries)), make(map[string]uint64)
		for _, entry := range entries {
			if size, exists := sizes[entry.SHA256Digest]; exists {
				if size != entry.SizeBytes {
					return errors.New("template digest has inconsistent sizes")
				}
				continue
			}
			sizes[entry.SHA256Digest] = entry.SizeBytes
			unique = append(unique, entry.SHA256Digest)
		}
		sequence = 1
		for index := 0; index < len(unique); index += 256 {
			end := index + 256
			if end > len(unique) {
				end = len(unique)
			}
			if err := stream.Send(&data.ExchangeRuntimeArtifactRequest{Payload: &data.ExchangeRuntimeArtifactRequest_MissingBlobDigests{MissingBlobDigests: &data.MissingBlobDigests{Sequence: sequence, Digests: unique[index:end]}}}); err != nil {
				return err
			}
			sequence++
		}
		if err := stream.Send(&data.ExchangeRuntimeArtifactRequest{Payload: &data.ExchangeRuntimeArtifactRequest_MissingEnd{MissingEnd: &data.MissingBlobDigestsEnd{Sequence: sequence, DigestCount: uint64(len(unique))}}}); err != nil {
			return err
		}
		blobRoot := filepath.Join(root, "runtime", "template-blobs")
		if err := os.MkdirAll(blobRoot, 0o700); err != nil {
			return err
		}
		for _, digest := range unique {
			file, err := os.OpenFile(filepath.Join(blobRoot, digest[7:]), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				return err
			}
			hash := sha256.New()
			var count, chunkSequence uint64 = 0, 1
			for {
				response, receiveErr := stream.Recv()
				if receiveErr != nil {
					file.Close()
					return receiveErr
				}
				switch item := response.GetPayload().(type) {
				case *data.ExchangeRuntimeArtifactResponse_BlobGroup:
					group := item.BlobGroup
					if group == nil || group.GetDigest() != digest || group.GetSequence() != chunkSequence || len(group.GetData()) == 0 || len(group.GetData()) > 65536 || count+uint64(len(group.GetData())) > sizes[digest] {
						file.Close()
						return errors.New("invalid template blob frame")
					}
					if _, err := file.Write(group.GetData()); err != nil {
						file.Close()
						return err
					}
					_, _ = hash.Write(group.GetData())
					count += uint64(len(group.GetData()))
					chunkSequence++
				case *data.ExchangeRuntimeArtifactResponse_BlobEnd:
					end := item.BlobEnd
					if end == nil || end.GetDigest() != digest || end.GetSequence() != chunkSequence || end.GetSha256Digest() != digest || end.GetSizeBytes() != count || count != sizes[digest] || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != digest {
						file.Close()
						return errors.New("template blob integrity mismatch")
					}
					if err := file.Close(); err != nil {
						return err
					}
					goto nextBlob
				default:
					file.Close()
					return errors.New("unexpected template blob response")
				}
			}
		nextBlob:
		}
		response, err := stream.Recv()
		if err != nil {
			return err
		}
		if response.GetExchangeEnd() == nil || response.GetExchangeEnd().GetSequence() != uint64(len(unique)+1) {
			return errors.New("invalid template exchange end")
		}
		templateRoot := filepath.Join(root, "runtime", "resources", "runtime", "nuclei", "templates")
		for _, entry := range entries {
			destination := filepath.Join(templateRoot, filepath.FromSlash(entry.RelativePath))
			if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
				return err
			}
			source, err := os.Open(filepath.Join(blobRoot, entry.SHA256Digest[7:]))
			if err != nil {
				return err
			}
			file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o400)
			if err != nil {
				source.Close()
				return err
			}
			_, copyErr := io.Copy(file, source)
			closeErr := file.Close()
			source.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		}
		if err := stream.Send(&data.ExchangeRuntimeArtifactRequest{Payload: &data.ExchangeRuntimeArtifactRequest_PublicationAck{PublicationAck: &data.RuntimeArtifactPublicationAck{SnapshotDigest: manifest.GetSnapshotDigest(), EntryCount: uint64(len(entries))}}}); err != nil {
			return err
		}
		response, err = stream.Recv()
		if err != nil {
			return err
		}
		complete := response.GetExchangeComplete()
		if complete == nil || complete.GetSnapshotDigest() != manifest.GetSnapshotDigest() || complete.GetEntryCount() != uint64(len(entries)) {
			return fmt.Errorf("template publication was not acknowledged")
		}
		if err := stream.CloseSend(); err != nil {
			return err
		}
	}
	return nil
}
