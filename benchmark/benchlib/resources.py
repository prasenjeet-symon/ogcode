"""Resident-memory sampling for one agent's process tree.

Peak RSS is not something a finished process reports, so it has to be sampled
while the tree is alive.  On macOS the sampler reads every process in the tree
through libproc, one syscall each, and therefore spawns no child process of its
own: a child would be accounted in ``RUSAGE_CHILDREN``, the same counter the
runner reads for the run's CPU time, so polling with ``ps`` would charge the
agent for the sampler.  Other platforms fall back to a single ``ps`` sweep per
sample and the summary records which backend produced the numbers.

CPU time is deliberately *not* sampled here.  ``getrusage`` is exact for a
reaped child and covers subprocesses that start and exit between two polls,
which a 0.1 s sample can miss; the runner reads the delta there and stores it
beside the peak figures.

An agent is more than one process -- opencode runs a server, ogcode runs tool
subprocesses -- so the tree is walked from the root on every sample and both
the summed footprint and the largest single process are kept.
"""

from __future__ import annotations

import ctypes
import subprocess
import sys
import threading
import time
from typing import Any, Optional

#: Fast enough to catch a short peak, cheap enough to ignore: on macOS one
#: sample costs a handful of syscalls rather than a process launch.
DEFAULT_INTERVAL_S = 0.1

#: A run's time series is evidence, not the measurement; 120 points draw the
#: shape of a multi-minute run without bloating ``raw.jsonl``.
MAX_SERIES_POINTS = 120

_CHILD_BUFFER = 1024


class _LibprocTree:
    """macOS: one syscall per process, and no child process of our own."""

    name = "libproc"

    class _RusageInfoV2(ctypes.Structure):
        # libproc.h, struct rusage_info_v2.  The uuid comes first and every
        # following field is 64-bit, so the layout is fixed on both arm64 and
        # x86_64.
        _fields_ = [
            ("ri_uuid", ctypes.c_uint8 * 16),
            ("ri_user_time", ctypes.c_uint64),
            ("ri_system_time", ctypes.c_uint64),
            ("ri_pkg_idle_wkups", ctypes.c_uint64),
            ("ri_interrupt_wkups", ctypes.c_uint64),
            ("ri_pageins", ctypes.c_uint64),
            ("ri_wired_size", ctypes.c_uint64),
            ("ri_resident_size", ctypes.c_uint64),
            ("ri_phys_footprint", ctypes.c_uint64),
            ("ri_proc_start_abstime", ctypes.c_uint64),
            ("ri_proc_exit_abstime", ctypes.c_uint64),
            ("ri_child_user_time", ctypes.c_uint64),
            ("ri_child_system_time", ctypes.c_uint64),
            ("ri_child_pkg_idle_wkups", ctypes.c_uint64),
            ("ri_child_interrupt_wkups", ctypes.c_uint64),
            ("ri_child_pageins", ctypes.c_uint64),
            ("ri_child_elapsed_abstime", ctypes.c_uint64),
            ("ri_diskio_bytesread", ctypes.c_uint64),
            ("ri_diskio_byteswritten", ctypes.c_uint64),
        ]

    def __init__(self) -> None:
        self._lib = ctypes.CDLL("/usr/lib/libproc.dylib", use_errno=True)
        self._lib.proc_pid_rusage.argtypes = [
            ctypes.c_int,
            ctypes.c_int,
            ctypes.c_void_p,
        ]
        self._lib.proc_pid_rusage.restype = ctypes.c_int
        self._lib.proc_listchildpids.argtypes = [
            ctypes.c_int,
            ctypes.c_void_p,
            ctypes.c_int,
        ]
        self._lib.proc_listchildpids.restype = ctypes.c_int
        self._children = (ctypes.c_int * _CHILD_BUFFER)()
        self._info = self._RusageInfoV2()

    def _usage(self, pid: int) -> Optional[tuple[int, int]]:
        """(resident bytes, physical footprint bytes) or None if it has exited."""
        if self._lib.proc_pid_rusage(pid, 2, ctypes.byref(self._info)) != 0:
            return None
        return int(self._info.ri_resident_size), int(self._info.ri_phys_footprint)

    def _children_of(self, pid: int) -> list[int]:
        count = self._lib.proc_listchildpids(
            pid, ctypes.byref(self._children), ctypes.sizeof(self._children)
        )
        if count <= 0:
            return []
        return [child for child in self._children[:count] if child > 0]

    def sample(self, root_pid: int) -> list[tuple[int, int, int]]:
        found: list[tuple[int, int, int]] = []
        seen = {root_pid}
        frontier = [root_pid]
        while frontier:
            pid = frontier.pop()
            usage = self._usage(pid)
            if usage is not None:
                found.append((pid, usage[0], usage[1]))
            for child in self._children_of(pid):
                if child not in seen:
                    seen.add(child)
                    frontier.append(child)
        return found


class _PsTree:
    """Fallback for platforms without libproc: one ``ps`` sweep per sample.

    The sweep is a child process, so on these platforms the sampler's own cost
    is billed to the machine rather than to the agent; that is why it is the
    fallback and not the default.
    """

    name = "ps"

    def sample(self, root_pid: int) -> list[tuple[int, int, int]]:
        proc = subprocess.run(
            ["ps", "-axo", "pid=,ppid=,rss="],
            capture_output=True,
            text=True,
            check=False,
        )
        parents: dict[int, int] = {}
        rss: dict[int, int] = {}
        for line in proc.stdout.splitlines():
            parts = line.split()
            if len(parts) < 3 or not parts[0].isdigit():
                continue
            pid, ppid, kilobytes = int(parts[0]), int(parts[1]), int(parts[2])
            parents[pid] = ppid
            rss[pid] = kilobytes * 1024
        children: dict[int, list[int]] = {}
        for pid, ppid in parents.items():
            children.setdefault(ppid, []).append(pid)
        found: list[tuple[int, int, int]] = []
        seen = {root_pid}
        frontier = [root_pid]
        while frontier:
            pid = frontier.pop()
            if pid in rss:
                # ``ps`` reports resident size only; footprint is left at zero.
                found.append((pid, rss[pid], 0))
            for child in children.get(pid, []):
                if child not in seen:
                    seen.add(child)
                    frontier.append(child)
        return found


def _backend() -> Any:
    if sys.platform == "darwin":
        try:
            return _LibprocTree()
        except OSError:
            pass
    return _PsTree()


def _median(values: list[int]) -> int:
    ordered = sorted(values)
    middle = len(ordered) // 2
    if len(ordered) % 2:
        return ordered[middle]
    return (ordered[middle - 1] + ordered[middle]) // 2


def _thin(samples: list[dict[str, Any]]) -> list[dict[str, Any]]:
    if len(samples) <= MAX_SERIES_POINTS:
        return samples
    stride = len(samples) / MAX_SERIES_POINTS
    return [samples[int(index * stride)] for index in range(MAX_SERIES_POINTS)]


class Sampler:
    """Poll a process tree's resident memory in a background thread."""

    def __init__(
        self,
        root_pid: int,
        interval_s: float = DEFAULT_INTERVAL_S,
        backend: Optional[Any] = None,
    ) -> None:
        self.root_pid = root_pid
        self.interval_s = interval_s
        self._backend = backend or _backend()
        self._samples: list[dict[str, Any]] = []
        self._stop = threading.Event()
        self._thread = threading.Thread(
            target=self._loop, name="resource-sampler", daemon=True
        )
        self._started_at = 0.0
        self.peak_process_rss_bytes = 0
        self.max_processes = 0
        self.error: Optional[str] = None

    def start(self) -> None:
        self._started_at = time.monotonic()
        self._thread.start()

    def _take(self) -> None:
        try:
            processes = self._backend.sample(self.root_pid)
        except Exception as exc:  # a sampler must never fail the run it measures
            self.error = f"{self._backend.name}: {exc}"
            self._stop.set()
            return
        if not processes:
            return
        elapsed = time.monotonic() - self._started_at
        self._samples.append(
            {
                "t": round(elapsed, 2),
                "rss_bytes": sum(entry[1] for entry in processes),
                "footprint_bytes": sum(entry[2] for entry in processes),
                "procs": len(processes),
            }
        )
        self.peak_process_rss_bytes = max(
            self.peak_process_rss_bytes, max(entry[1] for entry in processes)
        )
        self.max_processes = max(self.max_processes, len(processes))

    def _loop(self) -> None:
        while not self._stop.is_set():
            self._take()
            self._stop.wait(self.interval_s)

    def stop(self) -> dict[str, Any]:
        self._stop.set()
        if self._thread.is_alive():
            self._thread.join(timeout=5)
        return self.summary()

    def summary(self) -> dict[str, Any]:
        base: dict[str, Any] = {
            "backend": self._backend.name,
            "interval_s": self.interval_s,
            "samples": len(self._samples),
            "peak_tree_rss_bytes": None,
            "peak_tree_footprint_bytes": None,
            "peak_process_rss_bytes": self.peak_process_rss_bytes or None,
            "max_processes": self.max_processes or None,
            "median_tree_rss_bytes": None,
            "error": self.error,
        }
        if not self._samples:
            return base
        base.update(
            {
                "peak_tree_rss_bytes": max(s["rss_bytes"] for s in self._samples),
                "peak_tree_footprint_bytes": max(
                    s["footprint_bytes"] for s in self._samples
                ),
                "median_tree_rss_bytes": _median(
                    [s["rss_bytes"] for s in self._samples]
                ),
                "series": _thin(self._samples),
            }
        )
        return base
