package localagent

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"time"

	"github.com/yyhuni/lunafox/server/internal/httpoutbound"
)

func localProxyEnvironment(path string) ([]string, bool, error) {
	connection, err := net.DialTimeout("unix", path, 3*time.Second)
	if err != nil {
		return nil, false, errors.New("local proxy policy is unavailable")
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
	var snapshot httpoutbound.LocalProxySnapshot
	decoder := json.NewDecoder(io.LimitReader(connection, 16384))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return nil, false, errors.New("local proxy policy is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, false, errors.New("local proxy policy has trailing data")
	}
	if !snapshot.Override {
		if len(snapshot.Values) != 0 {
			return nil, false, errors.New("local proxy policy is invalid")
		}
		var inherited []string
		for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy"} {
			if value, ok := os.LookupEnv(name); ok {
				inherited = append(inherited, name+"="+value)
			}
		}
		return inherited, false, nil
	}
	if len(snapshot.Values) == 0 {
		return nil, false, nil
	}
	if len(snapshot.Values) != 4 {
		return nil, false, errors.New("local proxy policy is invalid")
	}
	value := snapshot.Values["HTTP_PROXY"]
	if _, err := httpoutbound.ParseProxyURL(value); err != nil {
		return nil, false, errors.New("local proxy policy is invalid")
	}
	var result []string
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		if snapshot.Values[name] != value {
			return nil, false, errors.New("local proxy policy is inconsistent")
		}
		result = append(result, name+"="+value)
	}
	return result, true, nil
}
