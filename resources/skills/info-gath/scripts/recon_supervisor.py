#!/usr/bin/env python3
"""Keep a command's process group bounded even when /proc is unavailable."""
from __future__ import annotations

import ctypes
import os
import signal
import subprocess
import sys


def terminate_group(_signum, _frame):
    try:
        os.killpg(os.getpgrp(), signal.SIGKILL)
    except ProcessLookupError:
        os._exit(1)


def main():
    if sys.platform != 'linux' or len(sys.argv) < 3 or os.getpgrp() != os.getpid():
        print('无法建立独立的采集进程组', file=sys.stderr)
        return 126
    try:
        parent_pid = int(sys.argv[1])
    except ValueError:
        return 126

    # The parent-death signal reaches this supervisor even when Landlock denies
    # /proc. The supervisor then kills its entire command group; nested
    # supervised commands receive the same signal when their parent dies.
    child = None
    cancelled = False

    def forward_cancel(_signum, _frame):
        nonlocal cancelled
        cancelled = True
        if child and child.poll() is None:
            try:
                child.send_signal(signal.SIGTERM)
            except ProcessLookupError:
                pass

    signal.signal(signal.SIGTERM, terminate_group)
    signal.signal(signal.SIGINT, forward_cancel)
    libc = ctypes.CDLL(None, use_errno=True)
    if libc.prctl(1, signal.SIGTERM, 0, 0, 0) != 0:  # PR_SET_PDEATHSIG
        print('无法设置采集进程的退出保护', file=sys.stderr)
        return 126
    if os.getppid() != parent_pid:
        terminate_group(signal.SIGTERM, None)
    try:
        child = subprocess.Popen(sys.argv[2:])
        if cancelled and child.poll() is None:
            child.send_signal(signal.SIGTERM)
        result = child.wait()
    except OSError as exc:
        print(str(exc), file=sys.stderr)
        return 127
    if result < 0:
        signum = -result
        if signum not in (signal.SIGKILL, signal.SIGSTOP):
            signal.signal(signum, signal.SIG_DFL)
        os.kill(os.getpid(), signum)
    return result


if __name__ == '__main__':
    raise SystemExit(main())
