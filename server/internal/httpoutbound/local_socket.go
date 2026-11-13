package httpoutbound

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// LocalProxySnapshot is returned only to processes owned by the Server user.
// It contains credentials when the configured proxy uses authentication.
type LocalProxySnapshot struct {
	Override bool              `json:"override"`
	Values   map[string]string `json:"values"`
}

type LocalProxySocket struct {
	listener *net.UnixListener
	path     string
	identity os.FileInfo
}

func StartLocalProxySocket(path string, controller *ProxyController) (*LocalProxySocket, error) {
	if controller == nil || !filepath.IsAbs(path) || filepath.Clean(path) != path || len(path) >= 104 {
		return nil, errors.New("invalid local proxy socket configuration")
	}
	dir := filepath.Dir(path)
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("local proxy socket directory must be private")
	}
	if owner, ok := info.Sys().(*syscall.Stat_t); !ok || owner.Uid != uint32(os.Getuid()) {
		return nil, errors.New("local proxy socket directory must belong to the current user")
	}
	if old, err := os.Lstat(path); err == nil {
		if old.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("local proxy socket path is occupied")
		}
		connection, dialErr := net.DialTimeout("unix", path, 200*time.Millisecond)
		if dialErr == nil {
			connection.Close()
			return nil, errors.New("local proxy socket is already in use")
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	listener.SetUnlinkOnClose(false)
	if err := os.Chmod(path, 0o600); err != nil {
		listener.Close()
		os.Remove(path)
		return nil, err
	}
	identity, err := os.Lstat(path)
	if err != nil {
		listener.Close()
		return nil, err
	}
	socket := &LocalProxySocket{listener: listener, path: path, identity: identity}
	go func() {
		for {
			connection, err := listener.AcceptUnix()
			if err != nil {
				return
			}
			go serveLocalProxyConnection(connection, controller)
		}
	}()
	return socket, nil
}

func serveLocalProxyConnection(connection *net.UnixConn, controller *ProxyController) {
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
	raw, err := connection.SyscallConn()
	if err != nil {
		return
	}
	var credential *unix.Ucred
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		credential, socketErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil || socketErr != nil || credential == nil || credential.Uid != uint32(os.Getuid()) {
		return
	}
	override, values := controller.ChildEnvironment()
	_ = json.NewEncoder(connection).Encode(LocalProxySnapshot{Override: override, Values: values})
}

func (socket *LocalProxySocket) Close() error {
	if socket == nil || socket.listener == nil {
		return nil
	}
	err := socket.listener.Close()
	if current, statErr := os.Lstat(socket.path); statErr == nil && os.SameFile(socket.identity, current) {
		if removeErr := os.Remove(socket.path); removeErr != nil && err == nil {
			err = fmt.Errorf("remove local proxy socket: %w", removeErr)
		}
	}
	return err
}
