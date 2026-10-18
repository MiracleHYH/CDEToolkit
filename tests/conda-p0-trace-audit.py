#!/usr/bin/env python3
"""只读归类 P0 VM 原生系统调用日志，不改写原始验收输入或结果。

日志过滤器须包含 %file,%network。可写 open 仅代表意图，不能证明写入
内容；原始 trace、完整运行时摘要以及配置 metadata 必须结合判断。
"""
import argparse
import collections
import json
import hashlib
import re
from pathlib import Path

LINE = re.compile(r"^(\d+)\s+(.*)$")
CALL = re.compile(r"^([A-Za-z_][A-Za-z_0-9]*)\(")
RESUMED = re.compile(r"^<\.\.\. ([A-Za-z_0-9]+) resumed>(.*)$")
QUOTED = re.compile(r'"(?:[^"\\]|\\.)*"')
WRITE_FLAGS = re.compile(r"\bO_(?:WRONLY|RDWR|CREAT|TRUNC|APPEND)\b")
MUTATIONS = {
    "mkdir", "mkdirat", "rmdir", "unlink", "unlinkat", "rename", "renameat", "renameat2",
    "symlink", "symlinkat", "link", "linkat", "chmod", "fchmod", "fchmodat", "fchmodat2",
    "chown", "lchown", "fchownat", "utime", "utimes", "utimensat", "truncate", "ftruncate",
    "mknod", "mknodat", "creat", "mount", "umount", "umount2", "setxattr", "lsetxattr",
    "removexattr", "lremovexattr",
}
NETWORK = {"connect", "sendto", "sendmsg", "sendmmsg", "bind", "listen", "socket", "socketpair"}

def strings(body):
    values = []
    for match in QUOTED.findall(body):
        try:
            values.append(json.loads(match))
        except json.JSONDecodeError:
            values.append(match[1:-1])
    return values

def area(path):
    if path == "/runtime/miniconda/conda-meta/history":
        return "runtime-base-history"
    for prefix, value in (("/work", "private-work"), ("/tmp", "private-tmp"), ("/evidence", "test-evidence"), ("/runtime", "other-runtime")):
        if path == prefix or path.startswith(prefix + "/"):
            return value
    if path.startswith("/"):
        return "outside-test-write-roots"
    return "relative-or-no-path"

def analyze(source, destination):
    source_before = source.stat()
    digest = hashlib.sha256()
    destination.mkdir(parents=True, exist_ok=True)
    pending = {}
    executables = {}
    total_lines = 0
    write_counts = collections.Counter()
    write_area_counts = collections.Counter()
    network_counts = collections.Counter()
    upstream_commands = collections.Counter()
    history = []
    network_calls = []
    outside = []
    paths = collections.Counter()
    events_total = 0
    unmatched_resumed = 0
    with source.open("rb") as stream, (destination / "write-intents.jsonl").open("w") as write_output:
        for line_number, raw_line in enumerate(stream, 1):
            digest.update(raw_line)
            line = raw_line.decode("utf-8", errors="replace")
            total_lines = line_number
            match = LINE.match(line.rstrip("\n"))
            if not match:
                continue
            pid, body = match.groups()
            if "<unfinished ...>" in body:
                pending[pid] = (line_number, body.replace(" <unfinished ...>", ""))
                continue
            resumed = RESUMED.match(body)
            if resumed:
                previous = pending.pop(pid, None)
                if previous:
                    line_number, initial = previous
                    body = initial + resumed.group(2)
                else:
                    unmatched_resumed += 1
                    continue
            call = CALL.match(body)
            if not call:
                continue
            call = call.group(1)
            success = not bool(re.search(r"\)\s+=\s+-1\b", body))
            values = strings(body)
            result_match = re.search(r"\)\s+=\s+(.+)$", body)
            result = result_match.group(1) if result_match else "unknown"
            if call == "execve" and success:
                executables[pid] = {"path": values[0] if values else "unknown", "argv": values[1:]}
                if values and values[0] == "/runtime/miniconda/bin/conda":
                    upstream_commands[tuple(values[1:])] += 1
            is_write = call in MUTATIONS or call in {"open", "openat", "openat2"} and bool(WRITE_FLAGS.search(body))
            context = executables.get(pid, {"path": "unknown-thread-or-parent", "argv": []})
            if is_write:
                # %file contains path-taking operations. File descriptor-only calls may have no path.
                path = (values[-1] if call in {"symlink", "symlinkat"} else values[0]) if values else "<no-path>"
                mutation_paths = values[:2] if call in {"rename", "renameat", "renameat2", "link", "linkat"} else [path]
                event = {"line": line_number, "pid": int(pid), "syscall": call, "path": path,
                         "mutation_paths": mutation_paths,
                         "area": area(path), "success": success, "result": result,
                         "exec": context, "raw": body}
                write_output.write(json.dumps(event, ensure_ascii=False) + "\n")
                events_total += 1
                write_counts[(call, "success" if success else "failed")] += 1
                write_area_counts[(event["area"], "success" if success else "failed")] += 1
                paths[(path, call, "success" if success else "failed")] += 1
                if event["area"] == "runtime-base-history":
                    history.append(event)
                if any(area(value) in {"outside-test-write-roots", "other-runtime"} for value in mutation_paths):
                    outside.append(event)
            if call in NETWORK:
                event = {"line": line_number, "pid": int(pid), "syscall": call, "success": success,
                         "result": result, "exec": context, "raw": body}
                network_calls.append(event)
                family = "AF_UNIX" if "AF_UNIX" in body else "AF_INET6" if "AF_INET6" in body else "AF_INET" if "AF_INET" in body else "AF_NETLINK" if "AF_NETLINK" in body else "other-or-not-visible"
                network_counts[(call, family, "success" if success else "failed")] += 1
    source_after = source.stat()
    if (source_before.st_ino, source_before.st_size, source_before.st_mtime_ns) != (source_after.st_ino, source_after.st_size, source_after.st_mtime_ns):
        raise RuntimeError(f"日志在归类期间发生变化，请等待验收结束后重试：{source}")
    result_path = source.parent / "result.json"
    acceptance = json.loads(result_path.read_text()) if result_path.is_file() else None
    summary = {"source": str(source), "source_sha256": digest.hexdigest(),
               "acceptance_result": None if acceptance is None else {
                   "path": str(result_path), "passed": acceptance.get("passed"),
                   "binary_sha256": acceptance.get("binary_sha256"),
                   "runtime_manifest_sha256": acceptance.get("runtime_manifest_sha256"),
                   "groups": [{"name": group.get("name"), "passed": group.get("passed")}
                              for group in acceptance.get("groups", [])]},
               "source_bytes": source_before.st_size, "total_lines": total_lines, "write_intent_events": events_total,
               "unfinished_calls_at_end": len(pending), "unmatched_resumed": unmatched_resumed,
               "write_syscalls": [{"syscall": k[0], "outcome": k[1], "count": v} for k, v in sorted(write_counts.items())],
               "write_areas": [{"area": k[0], "outcome": k[1], "count": v} for k, v in sorted(write_area_counts.items())],
               "network_counts": [{"syscall": k[0], "family": k[1], "outcome": k[2], "count": v} for k, v in sorted(network_counts.items())],
               "upstream_conda_commands": [{"argv": list(argv), "count": count}
                                           for argv, count in sorted(upstream_commands.items())],
               "runtime_history": history, "outside_write_roots": outside,
               "limits": ["Native VM syscalls issued by amd64 emulation, not x86 guest ptrace.",
                          "A writable open is an intent; this trace filter does not record write(fd) content or prove content changes.",
                          "Some Go thread PIDs have no execve in %file/%network trace; context then remains unknown.",
                          "Relative paths and descriptor-only calls may lack full path context in this trace filter."]}
    (destination / "summary.json").write_text(json.dumps(summary, indent=2, ensure_ascii=False) + "\n")
    (destination / "network.json").write_text(json.dumps(network_calls, indent=2, ensure_ascii=False) + "\n")
    with (destination / "write-path-counts.tsv").open("w") as output:
        output.write("path\tsyscall\toutcome\tcount\n")
        for key, count in sorted(paths.items()):
            output.write("\t".join(map(str, (*key, count))) + "\n")
    print(json.dumps({"source": str(source), "lines": total_lines, "write_intents": events_total,
                      "runtime_history": len(history), "outside": len(outside), "network_calls": len(network_calls),
                      "destination": str(destination)}, ensure_ascii=False))

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--output", type=Path, required=True)
parser.add_argument("sources", nargs="+", type=Path)
args = parser.parse_args()
output_root = args.output.resolve()
seen = set()
for source in args.sources:
    if source.resolve().is_relative_to(output_root):
        parser.error("输出目录必须与原始日志所在目录分离")
    if not source.is_file():
        parser.error(f"日志文件不存在：{source}")
    key = (source.parent.parent.name, source.parent.name)
    if key in seen:
        parser.error(f"存在重名验收目录/模式：{key}")
    seen.add(key)
for source in args.sources:
    # Parent/parent identify receipt + mode without rewriting the original evidence.
    destination = args.output / source.parent.parent.name / source.parent.name
    analyze(source, destination)
