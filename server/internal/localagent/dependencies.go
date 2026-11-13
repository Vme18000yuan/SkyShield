package localagent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// CheckDependencies prevents queued scans being claimed and failed merely
// because the local Engine or one of its tools has not been installed yet.
func CheckDependencies(config Config) error {
	var missing []string
	for _, name := range supportedEngines {
		if !executable(filepath.Join(config.EngineBinDir, name)) {
			missing = append(missing, "engine:"+name)
		}
	}
	for _, name := range []string{"subfinder", "puredns", "massdns", "naabu", "httpx", "katana", "waymore", "uro", "observer-ward", "ffuf", "nuclei", "cwebp"} {
		if !executable(filepath.Join(config.ToolsDir, name)) {
			missing = append(missing, "tool:"+name)
		}
	}
	if !executable(filepath.Join(config.ToolsDir, "website_discovery", "httpx")) {
		missing = append(missing, "tool:website_discovery/httpx")
	}
	if !executable(filepath.Join(config.ToolsDir, "website_discovery", "httpx.real")) {
		missing = append(missing, "tool:website_discovery/httpx.real")
	}
	for _, binding := range []string{"subdomain_discovery/subfinder", "url_collection/katana", "nuclei_vulnerability/nuclei", "fingerprint_detection/observer-ward", "directory_scan/ffuf"} {
		if !executable(filepath.Join(config.ToolsDir, filepath.FromSlash(binding))) {
			missing = append(missing, "proxy-wrapper:"+binding)
		}
	}
	if _, err := exec.LookPath("google-chrome"); err != nil {
		if _, err := exec.LookPath("chromium"); err != nil {
			missing = append(missing, "tool:chromium-or-google-chrome")
		}
	}
	if len(missing) != 0 {
		sort.Strings(missing)
		return fmt.Errorf("local scanner dependencies missing: %s", strings.Join(missing, ", "))
	}
	return nil
}

func executable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}
