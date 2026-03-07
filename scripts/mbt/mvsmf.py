"""mvsMF REST API client.

Communicates with the mvsMF server on MVS via z/OSMF-compatible
REST endpoints. Uses only Python stdlib (urllib).

Base URL: http://{host}:{port}/zosmf
Auth: HTTP Basic

All methods raise MvsMFError on communication failures.
"""

import http.client
import json
import re
import time
import urllib.request
import urllib.error
import base64
from dataclasses import dataclass

# Force HTTP/1.0 — mvsMF's HTTPD server speaks HTTP/1.0 and does not
# send Content-Length or chunked encoding. Python 3.13+ with HTTP/1.1
# waits for these headers and times out.
http.client.HTTPConnection._http_vsn = 10
http.client.HTTPConnection._http_vsn_str = "HTTP/1.0"


class MvsMFError(Exception):
    """Raised on mvsMF communication errors."""
    pass


@dataclass
class JobResult:
    """Result of a submitted JCL job."""
    jobid: str
    jobname: str
    rc: int           # 0, 4, 8, ... or 9999 for ABEND
    status: str       # "CC", "ABEND", "JCL ERROR", "TIMEOUT", "UNKNOWN"
    spool: str        # concatenated spool output

    @property
    def success(self) -> bool:
        return self.status == "CC"

    @property
    def abended(self) -> bool:
        return self.status == "ABEND"


class MvsMFClient:
    """mvsMF REST API client.

    Usage:
        client = MvsMFClient("localhost", 1080, "IBMUSER", "sys1")
        result = client.submit_jcl("//JOB ...")
    """

    def __init__(self, host: str, port: int,
                 user: str, password: str):
        self._base_url = f"http://{host}:{port}/zosmf"
        self._auth = base64.b64encode(
            f"{user}:{password}".encode()
        ).decode()

    # --- Internal HTTP ---

    def _request(self, method: str, path: str,
                 body: bytes | None = None,
                 content_type: str = "application/json",
                 accept: str = "application/json",
                 extra_headers: dict | None = None
                 ) -> bytes:
        """Execute HTTP request against mvsMF.

        Args:
            method: HTTP method
            path: URL path (appended to base URL)
            body: Request body
            content_type: Content-Type header
            accept: Accept header
            extra_headers: Additional headers to include

        Returns:
            Response body as bytes

        Raises:
            MvsMFError: On HTTP errors or connection failures
        """
        url = f"{self._base_url}{path}"
        req = urllib.request.Request(url, method=method)
        req.add_header("Authorization", f"Basic {self._auth}")
        req.add_header("Accept", accept)
        if body is not None:
            req.add_header("Content-Type", content_type)
            req.data = body
        if extra_headers:
            for key, val in extra_headers.items():
                req.add_header(key, val)
        try:
            with urllib.request.urlopen(req, timeout=30) as resp:
                return resp.read()
        except urllib.error.HTTPError as e:
            body_text = ""
            try:
                body_text = e.read().decode("utf-8", errors="replace")
            except Exception:
                pass
            raise MvsMFError(
                f"HTTP {e.code} {e.reason} for {method} {path}"
                + (f": {body_text}" if body_text else "")
            )
        except urllib.error.URLError as e:
            raise MvsMFError(
                f"Connection failed to {url}: {e.reason}"
            )

    def _json_request(self, method: str, path: str,
                      body: dict | None = None) -> dict:
        """JSON request/response convenience wrapper."""
        data = json.dumps(body).encode("utf-8") if body is not None else None
        raw = self._request(method, path, data,
                            "application/json", "application/json")
        if not raw:
            return {}
        return json.loads(raw)

    # --- Job Operations ---

    def submit_jcl(self, jcl_text: str,
                   wait: bool = True,
                   timeout: int = 120) -> JobResult:
        """Submit inline JCL and wait for completion.

        Endpoint: PUT /zosmf/restjobs/jobs
        Content-Type: text/plain

        Args:
            jcl_text: Complete JCL including JOB card
            wait: If True, poll until job completes
            timeout: Max seconds to wait

        Returns:
            JobResult with RC and spool output
        """
        body = jcl_text.encode("utf-8")
        raw = self._request("PUT", "/restjobs/jobs", body,
                            content_type="text/plain",
                            accept="application/json")
        resp = json.loads(raw)
        jobname = resp.get("jobname", "UNKNOWN")
        jobid = resp.get("jobid", "UNKNOWN")
        if not wait:
            return JobResult(
                jobid=jobid, jobname=jobname,
                rc=-1, status="ACTIVE", spool=""
            )
        return self._poll_job(jobname, jobid, timeout)

    def _poll_job(self, jobname: str, jobid: str,
                  timeout: int) -> JobResult:
        """Poll job status until OUTPUT or timeout.

        Endpoint: GET /zosmf/restjobs/jobs/{name}/{id}

        Uses progressive backoff to reduce REST load:
        1s, 2s, 3s, 5s, 5s, 5s, ...
        """
        backoff = [1, 2, 3, 5]
        backoff_idx = 0
        elapsed = 0.0

        while elapsed < timeout:
            delay = backoff[min(backoff_idx, len(backoff) - 1)]
            time.sleep(delay)
            elapsed += delay
            backoff_idx += 1

            try:
                data = self._json_request(
                    "GET", f"/restjobs/jobs/{jobname}/{jobid}"
                )
            except MvsMFError:
                continue

            if data.get("status") == "OUTPUT":
                retcode_str = data.get("retcode")
                spool = self._collect_spool(jobname, jobid)
                if retcode_str is not None:
                    rc, status = self._parse_retcode(retcode_str)
                else:
                    # MVS/CE mvsMF always returns null retcode;
                    # parse actual RC from spool output instead.
                    rc, status = self._parse_spool_rc(spool)
                return JobResult(
                    jobid=jobid, jobname=jobname,
                    rc=rc, status=status, spool=spool
                )

        # Timed out
        spool = self._collect_spool(jobname, jobid)
        return JobResult(
            jobid=jobid, jobname=jobname,
            rc=9999, status="TIMEOUT", spool=spool
        )

    def _collect_spool(self, jobname: str,
                       jobid: str) -> str:
        """Collect all spool files for a job.

        Endpoint: GET /zosmf/restjobs/jobs/{name}/{id}/files
        Endpoint: GET /zosmf/restjobs/jobs/{name}/{id}/files/{n}/records
        """
        try:
            data = self._json_request(
                "GET", f"/restjobs/jobs/{jobname}/{jobid}/files"
            )
        except MvsMFError:
            return ""

        files = data if isinstance(data, list) else data.get("items", [])
        parts = []
        for f in files:
            fid = f.get("id", "")
            ddname = f.get("ddname", "")
            if not fid:
                continue
            try:
                raw = self._request(
                    "GET",
                    f"/restjobs/jobs/{jobname}/{jobid}/files/{fid}/records",
                    accept="text/plain"
                )
                content = raw.decode("utf-8", errors="replace").replace("\r", "")
                parts.append(f"--- {ddname} ---\n{content}")
            except MvsMFError as e:
                parts.append(f"--- {ddname} --- [FAILED TO RETRIEVE: {e}]")

        return "\n".join(parts)

    @staticmethod
    def _parse_retcode(retcode_str: str | None) -> tuple[int, str]:
        """Parse z/OSMF retcode string.

        "CC 0000"    → (0, "CC")
        "CC 0004"    → (4, "CC")
        "ABEND S0C4" → (9999, "ABEND")
        "JCL ERROR"  → (9998, "JCL ERROR")
        None         → (-1, "UNKNOWN")
        """
        if retcode_str is None:
            return (-1, "UNKNOWN")
        s = retcode_str.strip()
        if s.startswith("CC "):
            try:
                rc = int(s[3:].strip())
                return (rc, "CC")
            except ValueError:
                return (9999, "CC")
        if "ABEND" in s:
            return (9999, "ABEND")
        if "JCL ERROR" in s:
            return (9998, "JCL ERROR")
        return (9999, "UNKNOWN")

    @staticmethod
    def _parse_spool_rc(spool: str) -> tuple[int, str]:
        """Parse RC from spool when retcode field is null.

        MVS/CE's mvsMF always returns null retcode in the REST API.
        This method reads the actual RC from JESYSMSG/JESJCL spool output.

        Patterns:
          IEF142I ... COND CODE NNNN → (NNNN, "CC")
          IEF452I / JCL ERROR        → (9998, "JCL ERROR")
          IEF472I / ABEND            → (9999, "ABEND")
          (none matched)             → (-1, "UNKNOWN")
        """
        m = re.search(r'COND CODE\s+(\d+)', spool)
        if m:
            return (int(m.group(1)), "CC")
        if 'IEF452I' in spool or 'JCL ERROR' in spool:
            return (9998, "JCL ERROR")
        if 'IEF472I' in spool or 'ABEND' in spool:
            return (9999, "ABEND")
        return (-1, "UNKNOWN")

    # --- Dataset Operations ---

    def dataset_exists(self, dsn: str) -> bool:
        """Check if dataset exists.

        Uses a prefix search (always shorter than the full DSN)
        so the dataset itself appears in dslevel results.
        Capped at 2 qualifiers for MVS/CE reliability.

        Endpoint: GET /zosmf/restfiles/ds?dslevel={prefix}
        """
        parts = dsn.split(".")
        # Search prefix must be shorter than the full DSN so the dataset
        # itself appears in dslevel results (the API matches prefix.*).
        # Cap at 2 qualifiers for MVS/CE reliability with long names.
        n = max(min(len(parts) - 1, 2), 1)
        search_level = ".".join(parts[:n])
        try:
            data = self._json_request(
                "GET", f"/restfiles/ds?dslevel={search_level}"
            )
            items = data.get("items", [])
            for item in items:
                if item.get("dsname", "").strip() == dsn:
                    return True
            return False
        except MvsMFError:
            return False

    def list_datasets(self, prefix: str) -> list[dict]:
        """List datasets matching prefix.

        Endpoint: GET /zosmf/restfiles/ds?dslevel={prefix}

        Returns:
            List of {"dsname": "...", "dsorg": "..."} dicts
        """
        try:
            data = self._json_request(
                "GET", f"/restfiles/ds?dslevel={prefix}"
            )
            return data.get("items", [])
        except MvsMFError:
            return []

    def create_dataset(self, dsn: str, dsorg: str,
                       recfm: str, lrecl: int, blksize: int,
                       space: list, unit: str = "SYSDA",
                       volume: str | None = None) -> None:
        """Create a new dataset.

        Endpoint: POST /zosmf/restfiles/ds/{dsn}
        Body: JSON with DCB attributes

        The space array is mapped to the JSON body:
          ["TRK", 10, 5, 10] → alcunit="TRK", primary=10,
                                secondary=5, dirblk=10
        """
        body: dict = {
            "dsorg": dsorg,
            "alcunit": space[0],
            "primary": int(space[1]),
            "secondary": int(space[2]),
            "recfm": recfm,
            "lrecl": lrecl,
            "blksize": blksize,
            "unit": unit,
        }
        if len(space) >= 4:
            body["dirblk"] = int(space[3])
        if volume:
            body["vol"] = volume
        self._json_request("POST", f"/restfiles/ds/{dsn}", body)

    def delete_dataset(self, dsn: str) -> None:
        """Delete a dataset.

        Endpoint: DELETE /zosmf/restfiles/ds/{dsn}
        """
        self._request("DELETE", f"/restfiles/ds/{dsn}",
                      accept="*/*")

    def list_members(self, dsn: str) -> list[str]:
        """List PDS members.

        Endpoint: GET /zosmf/restfiles/ds/{dsn}/member

        Returns:
            List of member names
        """
        data = self._json_request(
            "GET", f"/restfiles/ds/{dsn}/member"
        )
        return [m["member"].strip() for m in data.get("items", [])]

    def write_member(self, dsn: str, member: str,
                     content: str) -> None:
        """Write text content to PDS member.

        Endpoint: PUT /zosmf/restfiles/ds/{dsn}({member})
        Content-Type: text/plain
        """
        body = content.encode("utf-8")
        self._request(
            "PUT", f"/restfiles/ds/{dsn}({member})",
            body, content_type="text/plain", accept="*/*"
        )

    def read_member(self, dsn: str, member: str) -> str:
        """Read PDS member content.

        Endpoint: GET /zosmf/restfiles/ds/{dsn}({member})
        """
        raw = self._request(
            "GET", f"/restfiles/ds/{dsn}({member})",
            accept="text/plain"
        )
        return raw.decode("utf-8", errors="replace")

    def upload_binary(self, dsn: str,
                      data: bytes) -> None:
        """Upload binary data to sequential dataset.

        Used for XMIT file uploads.

        Endpoint: PUT /zosmf/restfiles/ds/{dsn}
        Content-Type: application/octet-stream
        X-IBM-Data-Type: binary
        """
        self._request(
            "PUT", f"/restfiles/ds/{dsn}",
            data,
            content_type="application/octet-stream",
            accept="*/*",
            extra_headers={"X-IBM-Data-Type": "binary"}
        )

    # --- Connectivity ---

    def ping(self) -> bool:
        """Test connectivity to mvsMF.

        Returns True if server responds (any HTTP response counts).
        """
        try:
            self._request("GET", "/info", accept="*/*")
            return True
        except MvsMFError as e:
            # Any HTTP response (including 404) means server is up
            if "HTTP " in str(e):
                return True
            return False
        except Exception:
            return False
