#!/usr/bin/env python3
"""A stand-in for mvsMF: records every request, answers like a healthy
server.  usage: stub_mvsmf.py PORT LOGFILE [SPOOLFILE]
Datasets exist only while the stub remembers them (create/upload/delete).
"""
import hashlib, json, sys, threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlsplit, parse_qs
port, logfile = int(sys.argv[1]), sys.argv[2]
spool = open(sys.argv[3]).read() if len(sys.argv) > 3 else " COND CODE 0000\n"
lock = threading.Lock(); datasets = set(); jobs = [0]
def record(h, body):
    ct = h.headers.get("Content-Type", "")
    if body and "json" in ct:
        desc = json.dumps(json.loads(body), sort_keys=True)
    elif body and ct.startswith("text/"):
        desc = "text:" + hashlib.sha256(body).hexdigest()[:16]
    elif body:
        desc = "bin:" + hashlib.sha256(body).hexdigest()[:16]
    else:
        desc = "-"
    extra = h.headers.get("X-IBM-Data-Type", "")
    line = f"{h.command} {h.path} accept={h.headers.get('Accept')} ct={ct or '-'} {extra} {desc}"
    with lock:
        open(logfile, "a").write(line + "\n")
    if body and ct.startswith("text/plain") and h.path.endswith("/restjobs/jobs"):
        with lock:
            open(logfile + ".jcl", "a").write(body.decode() + "\n=====\n")
class H(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.0"
    def log_message(self, *a): pass
    def send(self, code, obj=None, text=None):
        data = (json.dumps(obj).encode() if obj is not None else (text or "").encode())
        self.send_response(code)
        self.send_header("Content-Type", "application/json" if obj is not None else "text/plain")
        self.send_header("Content-Length", str(len(data))); self.end_headers(); self.wfile.write(data)
    def body(self):
        n = int(self.headers.get("Content-Length", "0") or 0)
        return self.rfile.read(n) if n else b""
    def do_GET(self):
        record(self, b""); u = urlsplit(self.path); p = u.path
        if p == "/zosmf/restfiles/ds":
            lvl = parse_qs(u.query).get("dslevel", [""])[0]
            return self.send(200, {"items": [{"dsname": d} for d in sorted(datasets) if d.startswith(lvl)]})
        if p.startswith("/zosmf/restjobs/jobs/") and p.endswith("/files"):
            return self.send(200, [{"id": 1, "ddname": "JESMSGLG"}, {"id": 2, "ddname": "SYSTSPRT"}])
        if p.endswith("/records"):
            return self.send(200, text=spool)
        if p.startswith("/zosmf/restjobs/jobs/"):
            return self.send(200, {"status": "OUTPUT", "retcode": None})
        return self.send(200, {})
    def do_PUT(self):
        b = self.body(); record(self, b); p = urlsplit(self.path).path
        if p == "/zosmf/restjobs/jobs":
            jobs[0] += 1
            name = b.decode().split()[0][2:]
            return self.send(200, {"jobname": name, "jobid": f"JOB{jobs[0]:05d}"})
        datasets.add(p.split("/")[-1]); return self.send(204)
    def do_POST(self):
        b = self.body(); record(self, b); datasets.add(urlsplit(self.path).path.split("/")[-1]); self.send(201, {})
    def do_DELETE(self):
        record(self, b""); datasets.discard(urlsplit(self.path).path.split("/")[-1]); self.send(204)
ThreadingHTTPServer(("127.0.0.1", port), H).serve_forever()
