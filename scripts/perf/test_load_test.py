"""压测统计与 Agent 协议回归；只访问本地模拟服务，不连接 B 站或用户 VM。"""

import contextlib
import io
import json
from pathlib import Path
import socket
import tempfile
import threading
import time
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlsplit

import load_test as perf


VIDEO = perf.Target("video", "BV17x411w7KC")
CREATOR = perf.Target("creator", "123")
HEADERS = {"content-type": "application/json"}


def state(target, tracking="active"):
    return {"kind": target.kind, "id": target.id, "recorded": True,
            "tracking": tracking == "active", "tracking_state": tracking, "status": "ok"}


def parse(endpoint, body, target=None, batch=None, status=200):
    return perf.parse_response(endpoint, status, HEADERS, json.dumps(body).encode(), target, batch)


@contextlib.contextmanager
def server(hook):
    class Handler(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def log_message(self, *args):
            pass

        def handle(self):
            try:
                super().handle()
            except ConnectionError:
                # 超时回归主动关闭连接，Windows 的下一次 keep-alive 读取可能收到 abort。
                pass

        def do_GET(self):
            self.respond("GET")

        def do_POST(self):
            self.respond("POST")

        def respond(self, method):
            payload = json.loads(self.rfile.read(int(self.headers["Content-Length"]))) if method == "POST" else None
            events.append((method, self.path, payload))
            result = hook(method, self.path, payload)
            if result is None:
                self.connection.shutdown(socket.SHUT_RDWR)
                self.connection.close()
                self.close_connection = True
                return
            status, body, delay = result
            time.sleep(delay)
            raw = json.dumps(body).encode()
            try:
                self.send_response(status)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)
            except (OSError, ConnectionError):
                pass

    events = []
    httpd = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=lambda: httpd.serve_forever(poll_interval=.01), daemon=True)
    thread.start()
    try:
        yield f"http://127.0.0.1:{httpd.server_port}", events
    finally:
        httpd.shutdown()
        httpd.server_close()
        thread.join()


def read_reply(method, path, payload):
    parsed = urlsplit(path)
    if parsed.path == "/healthz":
        return 200, {"status": "ok", "ok": True, "code": "OK"}, 0
    if parsed.path in ("/videos", "/creators"):
        target = VIDEO if parsed.path == "/videos" else CREATOR
        item = {**state(target), "seq": 1}
        return 200, {"items": [item], "page": 1, "limit": 18, "total": 1}, 0
    if parsed.path == "/api/targets/status":
        query = parse_qs(parsed.query)
        return 200, state(perf.Target(query["kind"][0], query["id"][0])), 0
    if parsed.path.startswith("/detail/"):
        kind, ident = parsed.path.split("/")[2:]
        target = perf.Target(kind, ident)
        return 200, {"item": {**state(target), "seq": 1}, "target": state(target),
                     "metrics": {"points": []}, "versions": []}, 0
    raise AssertionError(f"不应访问 {method} {path}")


def args(base, *extra):
    return perf.build_parser().parse_args(["--base-url", base, "--scenario", "mixed", "--duration", ".35", "--concurrency", "3", *extra])


class ProtocolTests(unittest.TestCase):
    def test_http_200_business_failures_are_not_success(self):
        for code in ("RATE_LIMITED", "DETAIL_NOT_RECORDED", "TARGET_PAUSED", "REQUEST_TIMEOUT"):
            with self.subTest(code=code):
                reply = parse("status", {"ok": False, "code": code, "retry_after_seconds": 2}, VIDEO)
                self.assertFalse(reply.ok)
                self.assertEqual(reply.code, code)
                self.assertEqual(reply.retry, 2)

    def test_success_requires_shape_identity_and_recorded_profile(self):
        self.assertFalse(parse("catalog", {}).ok)
        self.assertFalse(parse("status", state(CREATOR), VIDEO).ok)
        self.assertFalse(parse("status", {**state(VIDEO), "tracking": False}, VIDEO).ok)
        self.assertFalse(parse("status", {**state(VIDEO), "ok": "false"}, VIDEO).ok)
        self.assertFalse(parse("status", {**state(VIDEO), "ok": False, "code": "OK"}, VIDEO).ok)
        self.assertFalse(parse("status", {**state(VIDEO), "error": "failed", "code": "OK"}, VIDEO).ok)
        detail = {"item": {**state(CREATOR), "seq": 0}, "metrics": {}, "versions": []}
        self.assertFalse(parse("detail", detail, CREATOR).ok)
        self.assertFalse(parse("submit", state(VIDEO, "paused"), VIDEO).ok)
        self.assertFalse(parse("status", state(VIDEO), VIDEO, status=500).ok)

    def test_invalid_json_and_media_type(self):
        reply = perf.parse_response("catalog", 200, HEADERS, b"<html>wrong port</html>")
        self.assertEqual(reply.code, "INVALID_JSON")
        reply = perf.parse_response("status", 200, {"content-type": "text/plain"}, json.dumps(state(VIDEO)).encode(), VIDEO)
        self.assertEqual(reply.code, "INVALID_CONTENT_TYPE")

    def test_batch_truncation_and_duplicate_indices(self):
        item = {"index": 0, "kind": VIDEO.kind, "id": VIDEO.id, "ok": True,
                "code": "OK", "target": state(VIDEO), "result": "existing"}
        body = {"received": 2, "processed": 1, "ignored": 1, "results": [item]}
        reply = parse("batch", body, batch=[VIDEO, CREATOR])
        self.assertFalse(reply.ok)
        self.assertEqual(reply.code, "BATCH_TRUNCATED")
        self.assertEqual(reply.units_ok, 1)
        self.assertEqual(reply.target_codes["UNPROCESSED"], 1)
        body.update(processed=2, ignored=0, results=[item, item])
        self.assertFalse(parse("batch", body, batch=[VIDEO, CREATOR]).ok)

    def test_batch_partial_failure_and_success(self):
        first = {"index": 0, "kind": VIDEO.kind, "id": VIDEO.id, "ok": True,
                 "code": "OK", "target": state(VIDEO), "result": "existing"}
        second = {"index": 1, "kind": CREATOR.kind, "id": CREATOR.id, "ok": False, "code": "TARGET_BUSY"}
        body = {"received": 2, "processed": 2, "ignored": 0, "results": [first, second]}
        reply = parse("batch", body, batch=[VIDEO, CREATOR])
        self.assertFalse(reply.ok)
        self.assertEqual(reply.units_ok, 1)
        self.assertEqual(reply.target_codes["TARGET_BUSY"], 1)
        second.update(ok=True, code="OK", target=state(CREATOR), result="existing")
        reply = parse("batch", body, batch=[VIDEO, CREATOR])
        self.assertTrue(reply.ok)
        self.assertEqual(reply.results["existing"], 2)

    def test_tail_is_not_counted_in_window_and_latency_keeps_failures(self):
        metric = perf.Metric()
        metric.add(perf.Reply(True, "OK", finished=9.9, elapsed=.1, size=100), 10, "test")
        metric.add(perf.Reply(False, "RATE_LIMITED", finished=9.95, elapsed=.01), 10, "test")
        metric.add(perf.Reply(True, "OK", finished=11, elapsed=2, size=100), 10, "test")
        report = metric.summary(10, 12)
        self.assertEqual(report["rps"], .2)
        self.assertEqual(report["goodput"], .1)
        self.assertEqual(report["tail_completed"], 1)
        self.assertEqual(report["drained_rps"], .25)
        self.assertEqual(report["success_latency_ms"]["max"], 2000)
        self.assertEqual(report["latency_ms"]["p50"], 100)


class HTTPTests(unittest.TestCase):
    def test_read_mix_uses_agent_routes_and_no_posts(self):
        with server(read_reply) as (base, events):
            report = perf.run_load(args(base), [VIDEO, CREATOR], {"video": 1, "creator": 1})
        self.assertFalse(report["worker_errors"])
        self.assertGreater(report["requests_total"]["successful"], 0)
        self.assertEqual(report["requests_total"]["ok_rate"], 1)
        self.assertTrue(all(method == "GET" for method, _, _ in events))
        paths = {urlsplit(path).path for _, path, _ in events}
        self.assertTrue({"/videos", "/creators", "/api/targets/status"}.issubset(paths))
        self.assertNotIn("/", paths)
        self.assertNotIn("/healthz", paths)
        self.assertGreater(report["requests_total"]["completed"], report["operations_total"]["completed"])

    def test_connection_failure_is_not_retried(self):
        with server(lambda *_: None) as (base, events):
            client = perf.Client(base)
            try:
                reply = perf.status(client, VIDEO)
            finally:
                client.close()
        self.assertEqual(reply.code, "CONNECTION_FAILED")
        self.assertEqual(len(events), 1)

    def test_agent_pacing_respects_rate_limit_cooldown(self):
        with server(lambda *_: (200, {"ok": False, "code": "RATE_LIMITED"}, 0)) as (base, events):
            client = perf.Client(base, pacing="agent")
            try:
                reply = perf.status(client, VIDEO)
                self.assertEqual(reply.code, "RATE_LIMITED")
                self.assertGreaterEqual(client.next_start - reply.finished, 1)
                with self.assertRaises(perf.StopWork):
                    perf.status(client, VIDEO, time.perf_counter() + .1)
            finally:
                client.close()
        self.assertEqual(len(events), 1)

    def test_late_completion_is_reported_as_tail(self):
        def slow(method, path, payload):
            status, body, _ = read_reply(method, path, payload)
            return status, body, .2

        with server(slow) as (base, _):
            report = perf.run_load(args(base, "--duration", ".08", "--concurrency", "1", "--scenario", "detail"),
                                   [VIDEO], {"video": 1})
        total = report["requests_total"]
        self.assertEqual(total["completed"], 1)
        self.assertEqual(total["window_completed"], 0)
        self.assertEqual(total["goodput"], 0)
        self.assertEqual(total["tail_completed"], 1)
        self.assertGreater(report["actual_s"], .15)

    def test_rate_limit_cooldown_does_not_shorten_measurement_window(self):
        with server(lambda *_: (200, {"ok": False, "code": "RATE_LIMITED"}, 0)) as (base, events):
            report = perf.run_load(args(base, "--duration", ".2", "--concurrency", "1", "--pacing", "agent"), [VIDEO], {})
        self.assertEqual(len(events), 1)
        self.assertEqual(report["measurement_s"], .2)
        self.assertGreaterEqual(report["actual_s"], .19)
        self.assertEqual(report["requests_total"]["goodput"], 0)
        self.assertEqual(report["requests_total"]["rps"], 5)
        self.assertEqual(report["requests_total"]["per_second"], [{"second": 0, "completed": 1, "successful": 0}])

    def test_socket_timeout_is_reported_without_retry(self):
        with server(lambda *_: (200, state(VIDEO), .15)) as (base, events):
            client = perf.Client(base, timeout=.04)
            try:
                reply = perf.status(client, VIDEO)
            finally:
                client.close()
        self.assertEqual(reply.code, "REQUEST_TIMEOUT")
        self.assertEqual(reply.body, {"stage": "transport", "outcome": "not_applied"})
        self.assertEqual(len(events), 1)

    def test_window_close_does_not_start_next_step(self):
        with server(lambda *_: (200, {"items": [], "total": 0, "page": 1, "limit": 18}, .2)) as (base, events):
            report = perf.run_load(args(base, "--duration", ".08", "--concurrency", "1", "--scenario", "overview"), [VIDEO], {})
        self.assertEqual(len(events), 1)
        self.assertEqual(report["incomplete_operations"], 1)
        self.assertEqual(report["operations_total"]["completed"], 0)

    def test_reconcile_uses_each_target_once_and_respects_active_paused(self):
        states = {"123": "active", "456": "paused", "789": "absent"}

        def hook(method, path, payload):
            if method == "POST":
                states[payload["id"]] = "active"
                return 200, {**state(perf.Target("creator", payload["id"])), "result": "created"}, 0
            ident = parse_qs(urlsplit(path).query)["id"][0]
            return 200, state(perf.Target("creator", ident), states[ident]), 0

        with server(hook) as (base, events):
            report = perf.run_load(args(base, "--mode", "reconcile"),
                                   [perf.Target("creator", ident) for ident in states], {})
        posts = [payload for method, _, payload in events if method == "POST"]
        self.assertEqual(posts, [{"type": "creator", "id": "789"}])
        self.assertEqual(report["operations_total"]["completed"], 3)
        self.assertEqual(report["requests_total"]["submission_results"], {"created": 1})
        self.assertEqual(report["unconsumed_targets"], 0)

    def test_unknown_submission_is_confirmed_without_repost_or_hiding_failure(self):
        active = False

        def hook(method, path, payload):
            nonlocal active
            if method == "POST":
                active = True
                return 200, {"ok": False, "code": "REQUEST_TIMEOUT", "stage": "confirm", "outcome": "unknown"}, 0
            return 200, state(VIDEO, "active" if active else "absent"), 0

        with server(hook) as (base, events):
            report = perf.run_load(args(base, "--mode", "reconcile", "--concurrency", "1"), [VIDEO], {})
        self.assertEqual(sum(method == "POST" for method, _, _ in events), 1)
        self.assertEqual(report["requests_total"]["codes"]["REQUEST_TIMEOUT"], 1)
        self.assertEqual(report["requests_total"]["outcomes"], {"unknown": 1})
        self.assertEqual(report["operations_total"]["codes"], {"RECOVERED_BY_STATUS": 1})

    def test_existing_diagnostic_preflight_never_warms_up_missing_target(self):
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / "targets.json"
            path.write_text(json.dumps([{"type": VIDEO.kind, "id": VIDEO.id}]))
            with server(lambda *_: (200, state(VIDEO, "absent"), 0)) as (base, events):
                with contextlib.redirect_stdout(io.StringIO()):
                    result = perf.main(["--base-url", base, "--mode", "write", "--targets-file", str(path)])
            self.assertEqual(result, 2)
            self.assertTrue(all(method == "GET" for method, _, _ in events))

    def test_cli_writes_report_and_discovery_excludes_placeholder(self):
        def hook(method, path, payload):
            status, body, delay = read_reply(method, path, payload)
            if urlsplit(path).path == "/creators":
                body["items"].append({"kind": "creator", "id": "456", "recorded": True, "seq": 0})
            return status, body, delay

        with tempfile.TemporaryDirectory() as folder:
            output = Path(folder) / "report.json"
            with server(hook) as (base, _):
                with contextlib.redirect_stdout(io.StringIO()):
                    result = perf.main(["--base-url", base, "--duration", ".1", "--concurrency", "1", "--json-out", str(output)])
            report = json.loads(output.read_text(encoding="utf-8"))
            self.assertEqual(result, 0)
            self.assertEqual(report["target_pool"], 2)
            self.assertEqual(report["schema_version"], 2)
            self.assertFalse(report["worker_errors"])
            self.assertEqual(report["config"]["scenario"], "detail")
            self.assertTrue(report["requests"])
            self.assertTrue(all(label.startswith("GET /detail/") for label in report["requests"]))
            self.assertTrue(all(label.startswith("detail/") for label in report["operations"]))

    def test_detail_kind_discovers_only_selected_type(self):
        for kind, catalog_path in (("creator", "/creators"), ("video", "/videos")):
            with self.subTest(kind=kind), tempfile.TemporaryDirectory() as folder:
                output = Path(folder) / "report.json"
                with server(read_reply) as (base, events), contextlib.redirect_stdout(io.StringIO()):
                    result = perf.main(["--base-url", base, "--kind", kind, "--duration", ".1",
                                        "--concurrency", "1", "--json-out", str(output)])
                report = json.loads(output.read_text(encoding="utf-8"))
                self.assertEqual(result, 0)
                self.assertEqual(report["target_kinds"], {kind: 1})
                self.assertEqual(set(report["requests"]), {f"GET /detail/{kind}"})
                self.assertEqual(set(report["operations"]), {f"detail/{kind}"})
                self.assertTrue(all(urlsplit(path).path in (catalog_path, f"/detail/{kind}/" + (CREATOR.id if kind == "creator" else VIDEO.id))
                                    for _, path, _ in events))

    def test_fixed_id_uses_only_detail_without_catalog_or_status(self):
        with tempfile.TemporaryDirectory() as folder:
            output = Path(folder) / "report.json"
            with server(read_reply) as (base, events), contextlib.redirect_stdout(io.StringIO()):
                result = perf.main(["--base-url", base, "--kind", "creator", "--id", CREATOR.id,
                                    "--duration", ".1", "--concurrency", "1", "--json-out", str(output)])
            report = json.loads(output.read_text(encoding="utf-8"))
            self.assertEqual(result, 0)
            self.assertEqual(report["target_pool"], 1)
            self.assertEqual(report["target_kinds"], {"creator": 1})
            self.assertTrue(all(method == "GET" and urlsplit(path).path == f"/detail/creator/{CREATOR.id}"
                                for method, path, _ in events))
            self.assertTrue(all(parse_qs(urlsplit(path).query) == {"page": ["1"], "days": ["30"]} for _, path, _ in events))

    def test_fixed_missing_id_fails_preflight_without_collection(self):
        with server(lambda *_: (200, {"ok": False, "code": "DETAIL_NOT_RECORDED"}, 0)) as (base, events):
            with contextlib.redirect_stdout(io.StringIO()):
                result = perf.main(["--base-url", base, "--kind", "creator", "--id", CREATOR.id])
        self.assertEqual(result, 2)
        self.assertEqual(len(events), 1)
        self.assertEqual(urlsplit(events[0][1]).path, f"/detail/creator/{CREATOR.id}")

    def test_detail_file_filters_kind_before_preflight(self):
        with tempfile.TemporaryDirectory() as folder:
            targets = Path(folder) / "targets.json"
            targets.write_text(json.dumps([{"type": target.kind, "id": target.id} for target in (VIDEO, CREATOR)]))
            with server(read_reply) as (base, events), contextlib.redirect_stdout(io.StringIO()):
                result = perf.main(["--base-url", base, "--kind", "video", "--targets-file", str(targets),
                                    "--duration", ".1", "--concurrency", "1"])
            self.assertEqual(result, 0)
            self.assertTrue(all(urlsplit(path).path == f"/detail/video/{VIDEO.id}" for _, path, _ in events))

    def test_health_baseline_needs_no_dataset(self):
        with tempfile.TemporaryDirectory() as folder:
            output = Path(folder) / "health.json"
            with server(read_reply) as (base, events), contextlib.redirect_stdout(io.StringIO()):
                result = perf.main(["--base-url", base, "--scenario", "health", "--duration", ".1",
                                    "--concurrency", "1", "--json-out", str(output)])
            report = json.loads(output.read_text(encoding="utf-8"))
            self.assertEqual(result, 0)
            self.assertEqual(report["target_pool"], 0)
            self.assertEqual(set(report["requests"]), {"GET /healthz"})
            self.assertEqual(set(report["operations"]), {"health"})
            self.assertGreater(report["requests_total"]["mean_response_bytes"], 0)
            self.assertTrue(all(method == "GET" and path == "/healthz" for method, path, _ in events))

    def test_http_connection_is_reused(self):
        with server(read_reply) as (base, events):
            client = perf.Client(base)
            try:
                self.assertTrue(perf.detail(client, VIDEO, float("inf")).ok)
                connection = client.conn.sock
                self.assertTrue(perf.detail(client, CREATOR, float("inf")).ok)
                self.assertIs(client.conn.sock, connection)
                self.assertIsNotNone(connection)
            finally:
                client.close()
        self.assertEqual(len(events), 2)

    def test_detail_selection_rejects_incompatible_flags(self):
        for flags in (["--id", "123"], ["--kind", "creator", "--scenario", "mixed"],
                      ["--kind", "video", "--id", VIDEO.id, "--targets-file", "unused.json"],
                      ["--scenario", "health", "--targets-file", "unused.json"]):
            with self.subTest(flags=flags), contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
                perf.main(flags)

    def test_target_file_rejects_duplicates_and_invalid_inputs(self):
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / "targets.json"
            path.write_text(json.dumps([{"type": "creator", "id": "123"}] * 2))
            with self.assertRaises(ValueError):
                perf.load_targets(str(path))
        with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
            perf.main(["--mode", "reconcile"])
        with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
            perf.main(["--duration", "nan"])


if __name__ == "__main__":
    unittest.main()
