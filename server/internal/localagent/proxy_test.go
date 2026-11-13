package localagent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yyhuni/lunafox/server/internal/httpoutbound"
)

func TestLocalEngineUsesCurrentProxyPolicy(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "ss-proxy-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	controller := httpoutbound.NewProxyController()
	socket, err := httpoutbound.StartLocalProxySocket(filepath.Join(root, "proxy.sock"), controller)
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	if err := controller.Set(true, "http://127.0.0.1:1234"); err != nil {
		t.Fatal(err)
	}
	values, force, err := localProxyEnvironment(filepath.Join(root, "proxy.sock"))
	if err != nil {
		t.Fatal(err)
	}
	if !force || len(values) != 4 || values[0] != "HTTP_PROXY=http://127.0.0.1:1234" {
		t.Fatalf("enabled proxy values = %v", values)
	}
	if err := controller.Set(false, ""); err != nil {
		t.Fatal(err)
	}
	values, force, err = localProxyEnvironment(filepath.Join(root, "proxy.sock"))
	if err != nil || force || len(values) != 0 {
		t.Fatalf("disabled proxy values=%v err=%v", values, err)
	}
	if _, _, err := localProxyEnvironment(filepath.Join(root, "missing.sock")); err == nil {
		t.Fatal("missing proxy policy was accepted")
	}
}
