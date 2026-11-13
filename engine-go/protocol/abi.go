package protocol

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// EngineAPIMajor is the sole active Engine Container protocol major.
	EngineAPIMajor uint32 = 2
)

// Native single-machine execution gives every Engine process its own private
// root. Container execution retains the original, fixed ABI paths. The value
// is set only on the child Engine process by the local Agent.
const NativeExecutionRootEnv = "SKYSHIELD_NATIVE_EXECUTION_ROOT"

func executionRoots(value string) (string, string) {
	if value == "" {
		return "/run/lunafox", "/workspace"
	}
	if value != strings.TrimSpace(value) || !filepath.IsAbs(value) || filepath.Clean(value) != value || value == "/" {
		return "", ""
	}
	return filepath.Join(value, "runtime"), filepath.Join(value, "workspace")
}

var (
	RuntimeRootPath, WorkspacePath = executionRoots(os.Getenv(NativeExecutionRootEnv))
	ContextDirectoryPath           = filepath.Join(RuntimeRootPath, "context")
	ContextFilePath                = filepath.Join(ContextDirectoryPath, "execution.pb")
	CredentialDirectoryPath        = filepath.Join(RuntimeRootPath, "credential")
	CredentialFilePath             = filepath.Join(CredentialDirectoryPath, "token")
	SocketDirectoryPath            = filepath.Join(RuntimeRootPath, "socket")
	ExecutionEndpointPath          = filepath.Join(SocketDirectoryPath, "engine.sock")
	InputsDirectoryPath            = filepath.Join(RuntimeRootPath, "inputs")
	SubdomainsInputPath            = filepath.Join(InputsDirectoryPath, "subdomains.txt")
	HostPortsInputPath             = filepath.Join(InputsDirectoryPath, "host-ports.jsonl")
	WebsiteURLsInputPath           = filepath.Join(InputsDirectoryPath, "website-urls.txt")
	EndpointURLsInputPath          = filepath.Join(InputsDirectoryPath, "endpoint-urls.txt")
	ResourcesDirectoryPath         = filepath.Join(RuntimeRootPath, "resources")
	ConfigResourcesDirectoryPath   = filepath.Join(ResourcesDirectoryPath, "config")
	PlatformResourcesDirectoryPath = filepath.Join(ResourcesDirectoryPath, "platform")
	RuntimeResourcesDirectoryPath  = filepath.Join(ResourcesDirectoryPath, "runtime")
	// The Agent publishes the complete template directory before execution.
	NucleiTemplatesPath = filepath.Join(RuntimeResourcesDirectoryPath, "nuclei", "templates")
)

const (
	// ExecutionInputRoleSubdomains is the stable wire identity of the
	// non-sensitive subdomains product. Registry ownership of membership,
	// content, and paths remains in contracts/executionartifact.
	ExecutionInputRoleSubdomains = "subdomains"
	// ExecutionInputRoleHostPorts is the stable wire identity of the
	// non-sensitive hostPorts product.
	ExecutionInputRoleHostPorts = "hostPorts"
	// ExecutionInputRoleWebsiteURLs is the stable wire identity of the
	// non-sensitive websiteURLs product.
	ExecutionInputRoleWebsiteURLs = "websiteURLs"
	// ExecutionInputRoleEndpointURLs is the stable wire identity of the
	// non-sensitive Endpoint URL product.
	ExecutionInputRoleEndpointURLs = "endpointURLs"
)

const (
	// Protocol hard ceilings prevent a malformed or mixed-revision Context from
	// expanding the transport surface beyond what every implementation accepts.
	ProgressMessageMaxBytesCeiling uint32 = 64 * 1024
	ResultBatchMaxItemsCeiling     uint32 = 4096
	ResultBatchMaxBytesCeiling     uint32 = 4 * 1024 * 1024
)

// ContextBinding identifies the protocol message selected by a major.
type ContextBinding struct {
	EngineAPIMajor uint32
	ProtoPackage   string
	ProtoMessage   string
}

var contextBinding = ContextBinding{
	EngineAPIMajor: EngineAPIMajor,
	ProtoPackage:   "lunafox.engine.execution.v2",
	ProtoMessage:   "lunafox.engine.execution.v2.EngineExecutionContext",
}

// RequireContextBinding rejects unsupported protocol majors.
func RequireContextBinding(major uint32) (ContextBinding, error) {
	if major != EngineAPIMajor {
		return ContextBinding{}, fmt.Errorf("unsupported Engine API major %d", major)
	}
	return contextBinding, nil
}

// SupportedEngineAPIMajors returns the immutable supported-major set.
func SupportedEngineAPIMajors() []uint32 { return []uint32{EngineAPIMajor} }
