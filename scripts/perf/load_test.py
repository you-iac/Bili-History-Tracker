#!/usr/bin/env python3
"""多个 Agent 对 8181 的吞吐测试，口径与边界见 scripts/perf/README.md。

每个 worker 顺序执行一个业务操作内的 HTTP 请求。默认关闭客户端缓存和节流测容量；
pacing=agent 保留 150ms 节流和限流冷却。不自动重试、不隐式创建资料。
"""

from __future__ import annotations

import argparse
from collections import Counter, deque
from dataclasses import dataclass, field
from datetime import datetime, timezone
import http.client
import json
import math
from pathlib import Path
import random
import re
import sys
import threading
import time
from urllib.parse import quote, urlencode, urlsplit


MAX_BODY = 8 << 20  # 与 Agent remote 的响应上限一致。
READ_ACTIONS = ("overview",) * 2 + ("catalog",) * 3 + ("detail",) * 3 + ("refresh",) * 2
READ_TIMEOUT, STATUS_TIMEOUT, SUBMIT_TIMEOUT = 22.0, 8.0, 55.0


class StopWork(Exception):
    """窗口关闭；不伪造一次尚未发出的 HTTP 失败。"""


@dataclass(frozen=True)
class Target:
    kind: str
    id: str

    def query(self) -> str:
        return urlencode({"kind": self.kind, "id": self.id})


def target_from(value: dict) -> Target:
    if not isinstance(value, dict):
        raise ValueError("目标必须是包含 type/id 的对象")
    kind, ident = value.get("type", value.get("kind")), value.get("id")
    if not isinstance(ident, str):
        raise ValueError("目标 id 必须为字符串")
    valid = (kind == "video" and re.fullmatch(r"BV[0-9A-Za-z]{10}", ident)) or (
        kind == "creator" and re.fullmatch(r"[1-9][0-9]*", ident)
    )
    if not valid:
        raise ValueError(f"无效目标：{kind}/{ident}")
    return Target(kind, ident)


def load_targets(path: str) -> list[Target]:
    values = json.loads(Path(path).read_text(encoding="utf-8-sig"))
    if not isinstance(values, list) or not values:
        raise ValueError("目标文件必须是非空 JSON 数组")
    targets = [target_from(value) for value in values]
    if len(set(targets)) != len(targets):
        raise ValueError("目标文件含重复项；首次申请每项必须只使用一次")
    return targets


def tracking_state(value: dict) -> str:
    return value.get("tracking_state") or (
        "paused" if value.get("status") == "paused" else "active" if value.get("tracking") else "absent"
    )


def valid_status(value: object, target: Target) -> bool:
    if not isinstance(value, dict) or value.get("kind") != target.kind or value.get("id") != target.id:
        return False
    state = tracking_state(value)
    return (isinstance(value.get("tracking"), bool) and isinstance(value.get("recorded"), bool)
            and state in ("active", "absent", "paused") and value["tracking"] == (state == "active"))


@dataclass
class Reply:
    ok: bool
    code: str
    body: dict = field(default_factory=dict)
    status: int = 0
    size: int = 0
    elapsed: float = 0.0
    wait: float = 0.0
    finished: float = 0.0
    retry: float = 0.0
    units: int = 0
    units_ok: int = 0
    results: Counter = field(default_factory=Counter)
    target_codes: Counter = field(default_factory=Counter)
    diagnostic: str = ""


def business_code(body: dict) -> str:
    code, ok = body.get("code"), body.get("ok")
    if (ok is not None and not isinstance(ok, bool)) or (code is not None and not isinstance(code, str)):
        return "INVALID_RESPONSE"
    if code not in (None, "", "OK"):
        return code
    if ok is False or body.get("error") is not None:
        return "REMOTE_ERROR"
    return "OK"


def parse_response(endpoint: str, status: int, headers: dict, raw: bytes,
                   target: Target | None = None, batch: list[Target] | None = None) -> Reply:
    reply = Reply(False, f"HTTP_{status}", status=status, size=len(raw))
    try:
        body = json.loads(raw)
    except (ValueError, UnicodeError):
        reply.code = "INVALID_JSON" if 200 <= status < 300 else reply.code
        reply.diagnostic = raw[:300].decode("utf-8", errors="replace")
        return reply
    if not isinstance(body, dict):
        reply.code = "INVALID_RESPONSE"
        return reply
    reply.body = body
    reply.diagnostic = str(body.get("error") or "")[:300]
    try:
        retry = float(body.get("retry_after_seconds") or headers.get("retry-after") or 0)
        reply.retry = min(900.0, max(0.0, retry)) if math.isfinite(retry) else 0.0
    except (ValueError, TypeError):
        pass
    code = business_code(body)
    if not 200 <= status < 300 or code != "OK":
        reply.code = code if code != "OK" else f"HTTP_{status}"
        if reply.code == "RATE_LIMITED":
            reply.retry = max(1.0, reply.retry)
        return reply
    if headers.get("content-type", "").split(";", 1)[0].strip().lower() != "application/json":
        reply.code = "INVALID_CONTENT_TYPE"
        return reply
    valid = False
    if endpoint == "catalog":
        valid = (isinstance(body.get("items"), list) and type(body.get("page")) is int
                 and body["page"] >= 1 and type(body.get("limit")) is int and body["limit"] >= 1
                 and type(body.get("total")) is int and body["total"] >= 0)
    elif endpoint == "health":
        valid = body.get("status") == "ok"
    elif endpoint == "detail" and target:
        item = body.get("item", {})
        valid = (isinstance(item, dict) and item.get("kind") == target.kind and item.get("id") == target.id
                 and item.get("recorded") is True
                 and (target.kind != "creator" or type(item.get("seq")) is int and item["seq"] > 0)
                 and isinstance(body.get("metrics"), dict) and isinstance(body.get("versions"), list)
                 and (body.get("target") is None or valid_status(body["target"], target)))
    elif endpoint in ("status", "submit") and target:
        valid = valid_status(body, target) and (endpoint != "submit" or body.get("tracking") is True)
        if valid and endpoint == "submit":
            reply.units_ok = 1
            reply.results[str(body.get("result") or "unspecified")] += 1
    elif endpoint == "batch" and batch:
        reply.units = len(batch)
        items = body.get("results")
        if not isinstance(items, list):
            reply.code = "INVALID_RESPONSE"
            return reply
        indices = set()
        for item in items:
            if not isinstance(item, dict):
                reply.target_codes["INVALID_RESPONSE"] += 1
                continue
            index = item.get("index")
            if type(index) is not int or index < 0 or index >= len(batch) or index in indices:
                reply.target_codes["INVALID_RESPONSE"] += 1
                continue
            indices.add(index)
            expected = batch[index]
            item_code = business_code(item)
            nested = item.get("target")
            if item_code == "OK" and (item.get("ok") is not True or not valid_status(nested, expected)
                    or nested.get("tracking") is not True or item.get("kind") != expected.kind or item.get("id") != expected.id):
                item_code = "INVALID_RESPONSE"
            reply.target_codes[item_code] += 1
            if item_code == "OK":
                reply.units_ok += 1
                reply.results[str(item.get("result") or "unspecified")] += 1
        missing = len(batch) - len(indices)
        reply.target_codes["UNPROCESSED"] += missing
        complete = (body.get("received") == len(batch) and body.get("processed") == len(batch)
                    and body.get("ignored") == 0 and len(items) == len(batch) and missing == 0)
        reply.ok = complete and reply.units_ok == len(batch)
        reply.code = "OK" if reply.ok else "BATCH_TRUNCATED" if missing else "BATCH_FAILED"
        return reply
    reply.ok, reply.code = bool(valid), "OK" if valid else "INVALID_RESPONSE"
    return reply


def percentiles(values: list[float]) -> dict:
    values = sorted(values)
    return {name: round(values[max(0, math.ceil(len(values) * p) - 1)] * 1000, 2) if values else None
            for name, p in (("p50", .5), ("p95", .95), ("p99", .99), ("max", 1))}


@dataclass
class Metric:
    count: int = 0
    success: int = 0
    window_count: int = 0
    window_success: int = 0
    size: int = 0
    window_size: int = 0
    units: int = 0
    units_ok: int = 0
    window_units_ok: int = 0
    wait: float = 0.0
    latencies: list[float] = field(default_factory=list)
    success_latencies: list[float] = field(default_factory=list)
    codes: Counter = field(default_factory=Counter)
    stages: Counter = field(default_factory=Counter)
    outcomes: Counter = field(default_factory=Counter)
    http: Counter = field(default_factory=Counter)
    results: Counter = field(default_factory=Counter)
    target_codes: Counter = field(default_factory=Counter)
    failures: list[dict] = field(default_factory=list)
    seconds: Counter = field(default_factory=Counter)
    success_seconds: Counter = field(default_factory=Counter)

    def add(self, reply: Reply, deadline: float, label: str, start: float | None = None) -> None:
        self.count += 1
        self.success += int(reply.ok)
        in_window = reply.finished <= deadline
        self.window_count += int(in_window)
        self.window_success += int(in_window and reply.ok)
        if in_window and start is not None:
            second = max(0, int(reply.finished - start))
            self.seconds[second] += 1
            self.success_seconds[second] += int(reply.ok)
        self.size += reply.size
        self.window_size += reply.size if in_window else 0
        self.units += reply.units
        self.units_ok += reply.units_ok
        self.window_units_ok += reply.units_ok if in_window else 0
        self.wait += reply.wait
        self.latencies.append(reply.elapsed)
        if reply.ok:
            self.success_latencies.append(reply.elapsed)
        self.codes[reply.code] += 1
        if reply.status:
            self.http[str(reply.status)] += 1
        for key, counter in (("stage", self.stages), ("outcome", self.outcomes)):
            if key in reply.body:
                counter[str(reply.body[key])] += 1
        self.results.update(reply.results)
        self.target_codes.update(reply.target_codes)
        if not reply.ok and len(self.failures) < 3:
            self.failures.append({"request": label, "http": reply.status, "code": reply.code,
                                  "stage": reply.body.get("stage"), "outcome": reply.body.get("outcome"),
                                  "error": reply.diagnostic, "elapsed_ms": round(reply.elapsed * 1000, 2)})

    def merge(self, other: Metric) -> None:
        for key in ("count", "success", "window_count", "window_success", "size", "window_size",
                    "units", "units_ok", "window_units_ok", "wait"):
            setattr(self, key, getattr(self, key) + getattr(other, key))
        self.latencies.extend(other.latencies)
        self.success_latencies.extend(other.success_latencies)
        for key in ("codes", "stages", "outcomes", "http", "results", "target_codes", "seconds", "success_seconds"):
            getattr(self, key).update(getattr(other, key))
        self.failures = (self.failures + other.failures)[:3]

    def summary(self, window: float, actual: float) -> dict:
        return {"completed": self.count, "successful": self.success,
                "window_completed": self.window_count, "window_successful": self.window_success,
                "rps": self.window_count / window, "goodput": self.window_success / window,
                "drained_rps": self.count / actual, "drained_goodput": self.success / actual,
                "tail_completed": self.count - self.window_count,
                "ok_rate": self.success / self.count if self.count else 0,
                "window_ok_rate": self.window_success / self.window_count if self.window_count else 0,
                "latency_ms": percentiles(self.latencies), "success_latency_ms": percentiles(self.success_latencies),
                "client_wait_s": self.wait, "response_mb_s": self.window_size / window / 1_000_000,
                "response_bytes": self.size, "window_response_bytes": self.window_size,
                "mean_response_bytes": self.size / self.count if self.count else 0,
                "submitted_targets": self.units, "confirmed_targets": self.units_ok,
                "confirmed_targets_s": self.window_units_ok / window,
                "codes": dict(self.codes), "http": dict(self.http), "stages": dict(self.stages),
                "outcomes": dict(self.outcomes), "submission_results": dict(self.results),
                "target_codes": dict(+self.target_codes), "failure_examples": self.failures,
                "per_second": [{"second": second, "completed": self.seconds[second],
                                "successful": self.success_seconds[second]}
                               for second in range(math.ceil(window))]}


class Recorder:
    """每个 Agent 独立记录，结束后合并，不在所有请求之间添加统计互斥锁。"""

    def __init__(self):
        self.requests: dict[str, Metric] = {}
        self.operations: dict[str, Metric] = {}
        self.started = 0
        self.incomplete = 0
        self.start: float | None = None

    def request(self, label: str, reply: Reply, deadline: float) -> None:
        self.requests.setdefault(label, Metric()).add(reply, deadline, label, self.start)


class Client:
    def __init__(self, base: str, recorder: Recorder | None = None, pacing: str = "capacity",
                 timeout: float | None = None):
        parsed = urlsplit(base)
        if (parsed.scheme not in ("http", "https") or not parsed.hostname or parsed.username is not None
                or parsed.query or parsed.fragment):
            raise ValueError("base URL 必须是无账号、查询串、片段的 HTTP(S) 地址")
        cls = http.client.HTTPSConnection if parsed.scheme == "https" else http.client.HTTPConnection
        self.conn = cls(parsed.hostname, parsed.port, timeout=READ_TIMEOUT)
        self.prefix = parsed.path.rstrip("/")
        self.recorder, self.pacing, self.timeout = recorder, pacing, timeout
        self.next_start = 0.0

    def close(self) -> None:
        self.conn.close()

    def call(self, endpoint: str, path: str, target: Target | None = None,
             batch: list[Target] | None = None, deadline: float = math.inf) -> Reply:
        queued = time.perf_counter()
        delay = max(0.0, self.next_start - queued) if self.pacing == "agent" else 0.0
        if queued + delay >= deadline:
            # 冷却可能覆盖整个剩余窗口；不能缩短分母抬高吞吐。
            time.sleep(max(0.0, deadline - queued))
            raise StopWork()
        if delay:
            time.sleep(delay)
        started = time.perf_counter()
        if started >= deadline:
            raise StopWork()
        self.next_start = started + .150
        method = "POST" if endpoint in ("submit", "batch") else "GET"
        payload = None
        if target and endpoint == "submit":
            payload = {"type": target.kind, "id": target.id}
        if batch is not None:
            payload = {"targets": [{"type": t.kind, "id": t.id} for t in batch]}
        headers = {"Accept": "application/json"}
        if payload is not None:
            headers["Content-Type"] = "application/json"
        timeout = self.timeout or (SUBMIT_TIMEOUT if method == "POST" else STATUS_TIMEOUT if endpoint == "status" else READ_TIMEOUT)
        self.conn.timeout = timeout
        if self.conn.sock is not None:
            self.conn.sock.settimeout(timeout)
        raw = b""
        http_status = 0
        try:
            self.conn.request(method, self.prefix + path, body=json.dumps(payload).encode() if payload is not None else None, headers=headers)
            response = self.conn.getresponse()
            http_status = response.status
            raw = response.read(MAX_BODY + 1)
            if len(raw) > MAX_BODY:
                self.close()
                reply = Reply(False, "RESPONSE_TOO_LARGE", status=response.status, size=len(raw))
            else:
                reply = parse_response(endpoint, response.status,
                                       {k.lower(): v for k, v in response.getheaders()}, raw, target, batch)
        except TimeoutError as exc:
            self.close()
            reply = Reply(False, "REQUEST_TIMEOUT", status=http_status, diagnostic=str(exc))
        except (OSError, http.client.HTTPException) as exc:
            self.close()
            reply = Reply(False, "CONNECTION_FAILED", status=http_status, diagnostic=str(exc))
        except (ValueError, TypeError, KeyError) as exc:
            self.close()
            reply = Reply(False, "INVALID_RESPONSE", status=http_status, size=len(raw), diagnostic=str(exc))
        reply.units = len(batch) if batch is not None else int(endpoint == "submit")
        if not reply.body and reply.code in ("REQUEST_TIMEOUT", "CONNECTION_FAILED", "INVALID_JSON",
                                             "INVALID_RESPONSE", "RESPONSE_TOO_LARGE"):
            reply.body = {"stage": "transport", "outcome": "unknown" if method == "POST" else "not_applied"}
        reply.finished = time.perf_counter()
        reply.elapsed, reply.wait = reply.finished - started, started - queued
        if reply.code == "RATE_LIMITED":
            self.next_start = max(self.next_start, reply.finished + max(1.0, reply.retry))
        label = (f"{method} /targets" + (" [batch]" if batch else "") if method == "POST" else
                 f"GET /detail/{target.kind}" if endpoint == "detail" and target else
                 "GET /api/targets/status" if endpoint == "status" else f"GET {path.split('?')[0]}")
        if self.recorder:
            self.recorder.request(label, reply, deadline)
        return reply


def catalog(client: Client, kind: str, page: int = 1, deadline: float = math.inf) -> Reply:
    path = "/videos" if kind == "video" else "/creators"
    return client.call("catalog", path + "?" + urlencode({"page": page}), deadline=deadline)


def detail(client: Client, target: Target, deadline: float) -> Reply:
    return client.call("detail", f"/detail/{target.kind}/{quote(target.id, safe='')}?page=1&days=30", target, deadline=deadline)


def status(client: Client, target: Target, deadline: float = math.inf) -> Reply:
    return client.call("status", "/api/targets/status?" + target.query(), target, deadline=deadline)


def discover(client: Client, max_ids: int, kinds: tuple[str, ...] = ("video", "creator")) -> tuple[list[Target], dict[str, int]]:
    targets, pages = [], {}
    for kind in kinds:
        found, page = set(), 1
        while len(found) < max_ids:
            reply = catalog(client, kind, page)
            if not reply.ok:
                raise ValueError(f"发现资料失败：{kind} {reply.code} {reply.diagnostic}")
            body = reply.body
            pages[kind] = max(1, math.ceil(body["total"] / body["limit"]))
            for item in body["items"]:
                if not isinstance(item, dict) or item.get("recorded") is not True:
                    continue
                if kind == "creator" and (type(item.get("seq")) is not int or item["seq"] < 1):
                    continue
                target = target_from({"type": kind, "id": item.get("id")})
                if target not in found and len(found) < max_ids:
                    targets.append(target)
                    found.add(target)
            # 防止充满占位资料的大库使预检变成无界扫描。
            if page >= pages[kind] or page >= 20 or not body["items"]:
                break
            page += 1
    if not targets:
        raise ValueError("未发现完整资料；请先准备测试库，脚本不会自动请求 B 站补齐")
    return targets, pages


def run_operation(action: str, client: Client, target: Target | None, deadline: float,
                  rng: random.Random, pages: dict[str, int], batch: list[Target] | None = None) -> Reply:
    if action == "health":
        return client.call("health", "/healthz", deadline=deadline)
    if action == "overview":
        reply = catalog(client, "creator", deadline=deadline)
        return catalog(client, "video", deadline=deadline) if reply.ok else reply
    if action == "catalog":
        kind = rng.choice(("video", "creator"))
        return catalog(client, kind, rng.randint(1, min(3, pages.get(kind, 1))), deadline)
    if action == "detail":
        return detail(client, target, deadline)
    if action in ("refresh", "reconcile"):
        reply = status(client, target, deadline)
        if not reply.ok:
            return reply
        if action == "refresh":
            return detail(client, target, deadline) if reply.body["recorded"] else reply
        state = tracking_state(reply.body)
        if state in ("active", "paused"):
            return Reply(True, "ALREADY_ACTIVE" if state == "active" else "PAUSED_RESPECTED")
        reply = client.call("submit", "/targets", target, deadline=deadline)
        if reply.ok:
            return reply
        # 不重发 POST。未知结果仅做一次只读确认；原 HTTP 失败仍保留。
        if reply.body.get("outcome") == "unknown" or reply.code in ("REQUEST_TIMEOUT", "TARGET_BUSY"):
            confirmed = status(client, target, deadline)
            if confirmed.ok and tracking_state(confirmed.body) == "active":
                return Reply(True, "RECOVERED_BY_STATUS")
        return reply
    if action == "write":
        return client.call("submit", "/targets", target, deadline=deadline)
    if action == "batch":
        return client.call("batch", "/targets", batch=batch, deadline=deadline)
    raise ValueError(f"未知操作：{action}")


def run_load(args: argparse.Namespace, targets: list[Target], pages: dict[str, int]) -> dict:
    records = [Recorder() for _ in range(args.concurrency)]
    ready = threading.Barrier(args.concurrency + 1)
    begin = threading.Event()
    clock = {}
    pending = deque(targets)
    lock = threading.Lock()
    errors = []

    def worker(index: int) -> None:
        recorder = records[index]
        client = Client(args.base_url, recorder, args.pacing, args.timeout)
        rng = random.Random(args.seed + index)
        ready.wait()
        begin.wait()
        recorder.start = clock["start"]
        try:
            while time.perf_counter() < clock["deadline"]:
                if args.mode == "reconcile":
                    with lock:
                        if not pending:
                            break
                        target = pending.popleft()
                    action = "reconcile"
                else:
                    target = None if args.mode == "read" and args.scenario == "health" else rng.choice(targets)
                    action = rng.choice(READ_ACTIONS) if args.mode == "read" and args.scenario == "mixed" else (
                        args.scenario if args.mode == "read" else args.mode)
                batch = rng.sample(targets, args.batch_size) if action == "batch" else None
                started = time.perf_counter()
                recorder.started += 1
                try:
                    reply = run_operation(action, client, target, clock["deadline"], rng, pages, batch)
                except StopWork:
                    recorder.incomplete += 1
                    break
                finished = time.perf_counter()
                # 业务延迟包含节流和多步调用；HTTP 延迟不包含客户端等待。
                operation = Reply(reply.ok, reply.code, body=reply.body,
                                  elapsed=finished - started, finished=finished, diagnostic=reply.diagnostic)
                label = f"detail/{target.kind}" if action == "detail" else action
                recorder.operations.setdefault(label, Metric()).add(operation, clock["deadline"], label, clock["start"])
        except Exception as exc:
            with lock:
                errors.append(f"Agent {index}: {type(exc).__name__}: {exc}")
        finally:
            client.close()

    threads = [threading.Thread(target=worker, args=(i,), daemon=True) for i in range(args.concurrency)]
    for thread in threads:
        thread.start()
    ready.wait()
    clock["start"] = time.perf_counter()
    clock["started_at"] = datetime.now(timezone.utc).isoformat()
    clock["deadline"] = clock["start"] + args.duration
    begin.set()
    for thread in threads:
        thread.join()
    actual = max(time.perf_counter() - clock["start"], .000001)
    window = min(actual, args.duration) if args.mode == "reconcile" else args.duration
    merged = {"requests": {}, "operations": {}}
    for recorder in records:
        for group in merged:
            for label, metric in getattr(recorder, group).items():
                merged[group].setdefault(label, Metric()).merge(metric)
    report = {"measurement_s": window, "actual_s": actual, "drain_s": max(0, actual - args.duration),
              "measurement_started_at": clock["started_at"],
              "started_operations": sum(r.started for r in records),
              "incomplete_operations": sum(r.incomplete for r in records),
              "unconsumed_targets": len(pending) if args.mode == "reconcile" else None,
              "finite_workload": args.mode == "reconcile", "worker_errors": errors}
    for group, metrics in merged.items():
        total = Metric()
        for metric in metrics.values():
            total.merge(metric)
        report[group] = {label: metric.summary(window, actual) for label, metric in metrics.items()}
        report[group + "_total"] = total.summary(window, actual)
    return report


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--base-url", default="http://192.168.61.128:8181")
    parser.add_argument("--mode", choices=("read", "reconcile", "write", "batch"), default="read",
                        help="read=读取（默认详情）；reconcile=一次性关注核对；write/batch=已有任务申请诊断")
    parser.add_argument("--scenario", choices=("mixed", "overview", "catalog", "detail", "refresh", "health"),
                        help="read 默认 detail；mixed=混合业务，health=独立无数据库基线")
    parser.add_argument("--kind", choices=("all", "creator", "video"), default="all", help="detail 场景的目标类型")
    parser.add_argument("--pacing", choices=("capacity", "agent"), default="capacity")
    parser.add_argument("--duration", type=float, default=30.0, help="整个场景的测量窗口秒数")
    parser.add_argument("--concurrency", type=int, default=32, help="同时活跃的模拟 Agent 数")
    parser.add_argument("--timeout", type=float, help="覆盖 HTTP socket 超时；默认读取22s/状态8s/申请55s")
    parser.add_argument("--max-ids", type=int, default=100, help="自动发现时每种类型最多取多少完整资料")
    selection = parser.add_mutually_exclusive_group()
    selection.add_argument("--targets-file", help="JSON 目标数组；reconcile/write/batch 必填")
    selection.add_argument("--id", help="detail 场景固定一个已有资料 ID，必须同时指定 --kind")
    parser.add_argument("--batch-size", type=int, default=10)
    parser.add_argument("--seed", type=int, default=1)
    parser.add_argument("--json-out")
    return parser


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    args.scenario = args.scenario or ("detail" if args.mode == "read" else "mixed")
    if (not math.isfinite(args.duration) or args.duration <= 0 or args.concurrency < 1
            or args.max_ids < 1 or not 1 <= args.batch_size <= 100
            or args.timeout is not None and (not math.isfinite(args.timeout) or args.timeout <= 0)):
        parser.error("duration/concurrency/max-ids/timeout 必须为正，batch-size 必须为1～100")
    if args.mode != "read" and not args.targets_file:
        parser.error("申请场景必须显式指定 --targets-file；不会自动 warmup 或添加目标")
    if args.mode != "read" and args.scenario != "mixed":
        parser.error("--scenario 仅用于 read 模式")
    if (args.kind != "all" or args.id is not None) and (args.mode != "read" or args.scenario != "detail"):
        parser.error("--kind/--id 仅用于 read 的 detail 场景")
    if args.id is not None and args.kind == "all":
        parser.error("--id 必须同时指定 --kind creator 或 video")
    if args.scenario == "health" and args.targets_file:
        parser.error("health 基线不使用目标文件")
    try:
        client = Client(args.base_url, pacing="agent", timeout=args.timeout)
        try:
            targets = ([target_from({"type": args.kind, "id": args.id})] if args.id is not None else
                       load_targets(args.targets_file) if args.targets_file else None)
            pages = {"video": 1, "creator": 1}
            if args.mode == "read" and args.scenario == "health":
                reply = client.call("health", "/healthz")
                if not reply.ok:
                    raise ValueError(f"健康检查预检失败：{reply.code} {reply.diagnostic}")
                targets = []
            elif targets is None:
                kinds = ("video", "creator") if args.kind == "all" else (args.kind,)
                targets, pages = discover(client, args.max_ids, kinds)
            elif args.mode in ("read", "write", "batch"):
                if args.kind != "all":
                    targets = [target for target in targets if target.kind == args.kind]
                if not targets:
                    raise ValueError(f"目标文件中没有 {args.kind} 类型的资料")
                for target in targets:
                    if args.mode == "read" and args.scenario == "detail":
                        reply = detail(client, target, math.inf)
                        if not reply.ok:
                            raise ValueError(f"详情预检失败：{target.kind}/{target.id} {reply.code} {reply.diagnostic}")
                        continue
                    reply = status(client, target)
                    if not reply.ok or not reply.body.get("recorded"):
                        raise ValueError(f"预检失败：{target.kind}/{target.id} 没有完整资料或 {reply.code}")
                    if args.mode in ("write", "batch") and tracking_state(reply.body) != "active":
                        raise ValueError(f"{target.kind}/{target.id} 不是 active；已有任务诊断禁止隐式首次申请")
            if args.mode == "batch" and len(targets) < args.batch_size:
                raise ValueError("资料池小于 batch-size；请增加目标或显式缩小批次")
        finally:
            client.close()
    except (ValueError, OSError, TypeError) as exc:
        print(f"预检失败：{exc}")
        return 2
    print(f"target={args.base_url} mode={args.mode} scenario={args.scenario} pacing={args.pacing} "
          f"kind={args.kind} agents={args.concurrency} window={args.duration}s targets={len(targets)}", flush=True)
    if args.mode == "read" and args.scenario == "detail":
        print("详情读取：page=1、days=30；固定ID=" + (args.id or "无，从完整资料池随机选择"), flush=True)
    elif args.mode == "read" and args.scenario == "health":
        print("独立 HTTP 基线：仅 /healthz，不读数据库；不代表相同响应体大小的详情传输成本。", flush=True)
    if args.pacing == "capacity":
        print("容量模式：不使用客户端缓存、150ms节流或限流冷却；RATE_LIMITED 不计成功。", flush=True)
    if args.mode == "reconcile":
        print("一次性关注核对：每个目标至多消费一次；active/paused 不发 POST，absent 才申请。", flush=True)
    elif args.mode in ("write", "batch"):
        print("已有任务申请诊断：确认吞吐不等于首次采集或新增落库吞吐。", flush=True)
    report = run_load(args, targets, pages)
    report["schema_version"] = 2
    report["config"] = vars(args)
    report["target_pool"] = len(targets)
    report["target_kinds"] = dict(Counter(target.kind for target in targets))
    report["transport"] = {"protocol": "HTTP/1.1", "connection_reuse": True}
    report["finished_at"] = datetime.now(timezone.utc).isoformat()
    print(f"\n测量窗口 {report['measurement_s']:.3f}s；实际 {report['actual_s']:.3f}s；收尾 {report['drain_s']:.3f}s")
    for group, title in (("requests", "HTTP请求"), ("operations", "Agent业务操作")):
        print(f"\n{title}（RPS/成功吞吐仅计窗口内完成；延迟含收尾请求）：")
        for label, item in [("TOTAL", report[group + "_total"]), *report[group].items()]:
            latency = item["latency_ms"]
            print(f"{label:28} done={item['completed']:6} rps={item['rps']:8.2f} "
                  f"goodput={item['goodput']:8.2f} ok={item['ok_rate']:7.2%} "
                  f"p50/p95/p99={latency['p50']}/{latency['p95']}/{latency['p99']}ms "
                  f"ok_p95={item['success_latency_ms']['p95']}ms tail={item['tail_completed']} codes={item['codes']}")
    total = report["requests_total"]
    print(f"\nHTTP/1.1 按 Agent 复用连接；响应体={total['response_mb_s']:.3f} MB/s，平均={total['mean_response_bytes']:.0f}字节（不含头部和TCP开销）")
    print(f"\n收尾全程 HTTP成功吞吐={total['drained_goodput']:.2f}/s；未完成业务操作={report['incomplete_operations']}")
    if total["submitted_targets"]:
        print(f"申请目标={total['submitted_targets']} 确认成功={total['confirmed_targets']} "
              f"窗口确认吞吐={total['confirmed_targets_s']:.2f}目标/s result={total['submission_results']}")
    if report["finite_workload"]:
        print(f"有限资料集：未消费目标={report['unconsumed_targets']}；结果不代表持续新增容量。")
    if total["codes"].get("RATE_LIMITED"):
        print("存在业务限流；当前结果受服务端接纳配置约束。测处理容量时核对实际 BHT_API_RPS。")
    for item in report["requests"].values():
        for failure in item["failure_examples"]:
            print("失败样本：" + json.dumps(failure, ensure_ascii=False))
    if report["worker_errors"]:
        print("压测器错误：" + str(report["worker_errors"]))
    if args.json_out:
        Path(args.json_out).write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        print(f"结果已写入 {args.json_out}")
    return 2 if report["worker_errors"] or not total["completed"] else 0


if __name__ == "__main__":
    for stream in (sys.stdout, sys.stderr):
        if hasattr(stream, "reconfigure"):
            stream.reconfigure(encoding="utf-8", errors="replace")
    raise SystemExit(main())
