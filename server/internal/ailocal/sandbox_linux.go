//go:build linux

package ailocal

import (
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"
)

func createMCPProcessSession() error {
	_, err := unix.Setsid()
	return err
}

// Restrict the locked launcher thread, then exec a fresh runtime. This is
// essential on kernels before Landlock ABI 8: sibling Go threads otherwise
// retain their original access. All worker threads inherit the exec's policy.
func enterSandbox(root, executable string, args, environment, readPaths []string) error {
	runtime.LockOSThread()
	abi, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 || abi < 3 {
		return errors.New("本地工具执行需要启用 Landlock ABI 3 或以上的 Linux 内核")
	}
	handled := uint64((1 << 15) - 1)
	attribute := unix.LandlockRulesetAttr{Access_fs: handled}
	fd, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&attribute)), 8, 0)
	if errno != 0 {
		return fmt.Errorf("create local tool sandbox: %w", errno)
	}
	defer unix.Close(int(fd))
	read := uint64(unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_READ_DIR | unix.LANDLOCK_ACCESS_FS_EXECUTE)
	add := func(path string, access uint64, required bool) error {
		descriptor, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
		if err != nil {
			if !required && errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("sandbox path %s: %w", path, err)
		}
		defer unix.Close(descriptor)
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			access &= unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE | unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_TRUNCATE
		}
		rule := unix.LandlockPathBeneathAttr{Allowed_access: access & handled, Parent_fd: int32(descriptor)}
		_, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, fd, unix.LANDLOCK_RULE_PATH_BENEATH, uintptr(unsafe.Pointer(&rule)), 0, 0, 0)
		if errno != 0 {
			return fmt.Errorf("sandbox rule %s: %w", path, errno)
		}
		return nil
	}
	if err := add(root, handled, true); err != nil {
		return err
	}
	if err := add(executable, read, true); err != nil {
		return err
	}
	for _, path := range readPaths {
		if !filepath.IsAbs(path) || filepath.Clean(path) == "/" {
			return errors.New("invalid sandbox resource")
		}
		if err := add(path, read, true); err != nil {
			return err
		}
	}
	for _, path := range []string{"/usr/bin", "/bin", "/usr/lib", "/lib", "/lib64", "/usr/lib64", "/usr/local/bin", "/usr/local/lib", "/usr/share", "/etc/ssl", "/etc/ca-certificates", "/etc/resolv.conf", "/etc/hosts", "/etc/nsswitch.conf", "/etc/ld.so.cache", "/etc/passwd", "/etc/group", "/etc/localtime", "/proc/self/fd"} {
		if err := add(path, read, false); err != nil {
			return err
		}
	}
	for _, path := range []string{"/dev/null", "/dev/zero", "/dev/random", "/dev/urandom", "/dev/ptmx", "/dev/pts"} {
		if err := add(path, read|unix.LANDLOCK_ACCESS_FS_WRITE_FILE, false); err != nil {
			return err
		}
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return err
	}
	_, _, errno = unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, fd, 0, 0)
	if errno != 0 {
		return fmt.Errorf("enforce local tool sandbox: %w", errno)
	}
	_ = unix.Setrlimit(unix.RLIMIT_CORE, &unix.Rlimit{Cur: 0, Max: 0})
	_ = unix.Setrlimit(unix.RLIMIT_NOFILE, &unix.Rlimit{Cur: 512, Max: 512})
	// Per-file backstop even on development hosts without delegated cgroups.
	if err := unix.Setrlimit(unix.RLIMIT_FSIZE, &unix.Rlimit{Cur: 256 << 20, Max: 256 << 20}); err != nil {
		return err
	}
	return syscall.Exec(executable, append([]string{filepath.Base(executable)}, args...), environment)
}
