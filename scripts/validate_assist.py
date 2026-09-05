#!/usr/bin/env python3
"""Validate the AI assistant against a real model, end to end.

Everything about the assistant up to this point has been tested with a
stub that returns whatever it is told to. That proves the transport, the
authentication, the caching, the filters and the status contract — and
proves nothing at all about whether a real model produces hints that are
useful, correctly scoped to their rung, and free of solutions.

This script closes that gap. It drives the same HTTP path a student's
browser uses, against a real provider, on real seeded problems, using a
real judged submission so that rung 3 and the verdict explanation are
grounded in an actual failure rather than a synthetic one.

    python3 scripts/validate_assist.py --api-url http://localhost:8080

It expects three things already running: an API with GROQ_API_KEY
configured, a judge worker (without one, submissions sit pending
forever and rungs 3 and 4 have no real failure to describe), and a
database seeded with the standard problem library.

It never prints the key. It does print every generated hint, because the
point of the exercise is a human reading them.
"""

import argparse
import json
import os
import re
import statistics
import sys
import time
import urllib.error
import urllib.request

# A submission that is wrong in a specific, useful way: it pairs the
# global minimum with the global maximum, so it passes the samples and
# fails the hidden case where the peak comes before the dip. That gives
# rung 3 something real to describe.
WRONG_BUY_SELL = """import sys
data = sys.stdin.read().split()
n = int(data[0])
prices = list(map(int, data[1:1+n]))
print(max(prices) - min(prices))
"""

# A genuine time-limit failure. Quadratic alone is not enough — the
# seeded inputs are small, so an O(n^2) solution is accepted — so this
# inflates the work deliberately. It is still the shape of the mistake a
# verdict explanation should diagnose: repeated recomputation of sums
# that could have been carried forward.
SLOW_MAX_SUBARRAY = """import sys
data = sys.stdin.read().split()
n = int(data[0])
a = list(map(int, data[1:1+n]))
big = a * 4000
best = big[0]
for i in range(len(big)):
    total = 0
    for j in range(i, len(big)):
        total += big[j]
        if total > best:
            best = total
print(best)
"""

RUNG_NAMES = {
    1: "constraint (restate a guarantee)",
    2: "shape (class of approach)",
    3: "failing case (a property of it)",
    4: "outline (steps in prose)",
}

# Shapes that mean the response handed over an implementation. This is a
# reviewer's aid, deliberately separate from the server's own filter: if
# these disagree, that is worth knowing.
CODE_SMELL = re.compile(
    r"```|\bdef\s+\w+\s*\(|\bfor\s+\w+\s+in\s+.*:|\bwhile\s+.*:|"
    r"=>|\bfunc\s+\w+\s*\(|#include|\bpublic\s+static\b"
)


class Client:
    def __init__(self, base):
        self.base = base.rstrip("/")
        self.cookie = None

    def call(self, method, path, body=None):
        url = f"{self.base}{path}"
        data = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(url, data=data, method=method)
        req.add_header("Content-Type", "application/json")
        if self.cookie:
            req.add_header("Cookie", self.cookie)

        started = time.monotonic()
        try:
            with urllib.request.urlopen(req, timeout=90) as resp:
                elapsed = time.monotonic() - started
                raw = resp.read().decode()
                setcookie = resp.headers.get("Set-Cookie")
                if setcookie:
                    self.cookie = setcookie.split(";")[0]
                return resp.status, json.loads(raw or "{}"), elapsed
        except urllib.error.HTTPError as e:
            elapsed = time.monotonic() - started
            raw = e.read().decode()
            try:
                return e.code, json.loads(raw or "{}"), elapsed
            except json.JSONDecodeError:
                return e.code, {"message": raw[:200]}, elapsed
        except Exception as e:  # noqa: BLE001 - reported, not handled
            return 0, {"message": f"{type(e).__name__}: {e}"}, time.monotonic() - started


def call_with_backoff(c, method, path, body=None, tries=4):
    """Retry past a rate limit.

    The per-user hint limit is ten a minute and this script makes more
    than that, so a 429 is an artefact of the harness rather than a
    result. Recording it as one would put a number in the report that
    says nothing about the model.
    """
    for attempt in range(tries):
        status, resp, elapsed = c.call(method, path, body)
        if status != 429:
            return status, resp, elapsed
        wait = 0
        msg = resp.get("message", "")
        digits = re.search(r"(\d+)\s*second", msg)
        if digits:
            wait = int(digits.group(1))
        wait = min(max(wait + 2, 5), 70)
        if attempt < tries - 1:
            print(f"      (rate limited; waiting {wait}s)")
            time.sleep(wait)
    return status, resp, elapsed


def wait_for_no_pending(c, timeout=120):
    """Admission control allows one in-flight submission per user."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        status, body, _ = c.call("GET", "/api/users/me/submissions?pageSize=5")
        items = (body.get("data") or {}) if isinstance(body.get("data"), dict) else {}
        rows = items.get("submissions") or body.get("data") or []
        if not isinstance(rows, list):
            return
        if not any(r.get("status") in ("pending", "running") for r in rows if isinstance(r, dict)):
            return
        time.sleep(2)


def login_or_register(c, email, password, username, name):
    status, _, _ = c.call("POST", "/api/auth/register", {
        "full_name": name, "username": username, "email": email, "password": password,
    })
    if status not in (200, 201, 400, 409):
        print(f"  register returned {status}")
    status, _, _ = c.call("POST", "/api/auth/login", {"email": email, "password": password})
    return status == 200


def problems(c):
    status, body, _ = c.call("GET", "/api/problems?pageSize=50")
    if status != 200:
        return {}
    items = body.get("data") or body.get("problems") or []
    return {p["slug"]: p for p in items if "slug" in p}


def judge(c, slug, code, label):
    """Submit and poll to a terminal verdict. Returns the submission."""
    status, body, _ = c.call("POST", f"/api/problems/{slug}/submit",
                             {"language": "python", "code": code})
    if status not in (200, 202):
        print(f"  ! {label}: submit returned {status} {body.get('message','')}")
        return None
    sid = body.get("submissionId") or (body.get("data") or {}).get("id")
    if not sid:
        print(f"  ! {label}: no submission id in {body}")
        return None

    deadline = time.time() + 120
    while time.time() < deadline:
        status, body, _ = c.call("GET", f"/api/submissions/{sid}")
        sub = body.get("data") or body
        if sub.get("status") not in ("pending", "running"):
            print(f"  {label}: {sub.get('status')} "
                  f"(case {sub.get('failedCase')} of {sub.get('totalCases')}, "
                  f"{sub.get('runtimeMs')}ms)")
            return sub
        time.sleep(1)
    print(f"  ! {label}: never reached a verdict")
    return None


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--api-url", default="http://localhost:8080")
    ap.add_argument("--email", default="assistvalidation@local.test")
    ap.add_argument("--password", default="correct horse battery staple")
    ap.add_argument("--delay", type=float, default=7.0,
                    help="seconds between hint calls; the server allows 10/min")
    ap.add_argument("--json-out", default="")
    args = ap.parse_args()

    c = Client(args.api_url)
    records = []

    print("=" * 72)
    print("CodeArena assist — real-model validation")
    print("=" * 72)

    if not login_or_register(c, args.email, args.password, "assistvalidation", "Assist Validation"):
        sys.exit("could not authenticate against the API")

    catalogue = problems(c)
    if not catalogue:
        sys.exit("no problems found — run the seeder first")
    print(f"{len(catalogue)} problems in the library\n")

    # Is the assistant even on? A 200 with enabled:false means no key.
    slug0 = next(iter(catalogue))
    status, body, _ = c.call("GET", f"/api/problems/{slug0}/assist/state")
    state = body.get("data", {})
    if status != 200 or not state.get("enabled", False):
        sys.exit("assist reports itself disabled — set GROQ_API_KEY and restart the API")
    print("assist is enabled\n")

    # --- A real failure, so rung 3 and the explanation have something real
    print("── judging a deliberately wrong submission " + "─" * 30)
    wait_for_no_pending(c)
    wrong = None
    if "best-time-to-buy-and-sell-stock" in catalogue:
        wrong = judge(c, "best-time-to-buy-and-sell-stock", WRONG_BUY_SELL, "buy/sell (peak before dip)")
    slow = None
    wait_for_no_pending(c)
    if "maximum-subarray" in catalogue:
        slow = judge(c, "maximum-subarray", SLOW_MAX_SUBARRAY, "maximum subarray (quadratic)")
    print()

    # --- The hint ladder, on real problems
    targets = [s for s in [
        "best-time-to-buy-and-sell-stock", "maximum-subarray", "binary-search",
        "climbing-stairs", "longest-substring-without-repeating-characters", "two-sum",
    ] if s in catalogue]

    print("── hint ladder " + "─" * 58)
    for slug in targets:
        print(f"\n### {catalogue[slug]['title']}")
        for rung in (1, 2, 3, 4):
            status, body, elapsed = call_with_backoff(c, "POST", "/api/assist/hint", {
                "problemSlug": slug, "rung": rung,
                "language": "python", "code": WRONG_BUY_SELL,
            })
            data = body.get("data", {})
            text = data.get("text", "")
            rec = {
                "feature": "hint", "slug": slug, "rung": rung, "status": status,
                "latency": round(elapsed, 2), "cached": bool(data.get("cached")),
                "chars": len(text), "code_smell": bool(text and CODE_SMELL.search(text)),
                "message": body.get("message", ""),
            }
            records.append(rec)

            head = f"  rung {rung} [{RUNG_NAMES[rung]}] -> {status} {elapsed:.2f}s"
            if rec["cached"]:
                head += " (cached)"
            print(head)
            if text:
                for line in text.splitlines():
                    print(f"      {line}")
                if rec["code_smell"]:
                    print("      *** REVIEWER FLAG: looks like it contains code ***")
            elif status != 200:
                print(f"      withheld: {body.get('message','')}")
            time.sleep(args.delay)  # stay inside the 10/min per-user hint limit

    # --- Verdict explanations, grounded in a real verdict
    print("\n── verdict explanations " + "─" * 49)
    for sub, label in [(wrong, "wrong answer"), (slow, "time limit")]:
        if not sub:
            continue
        for attempt in (1, 2):  # second call proves the cache
            status, body, elapsed = call_with_backoff(
                c, "POST", "/api/assist/explain", {"submissionId": sub["id"]})
            data = body.get("data", {})
            text = data.get("text", "")
            records.append({
                "feature": "explain", "slug": sub.get("problemSlug"), "rung": 0,
                "status": status, "latency": round(elapsed, 2),
                "cached": bool(data.get("cached")), "chars": len(text),
                "code_smell": bool(text and CODE_SMELL.search(text)),
                "message": body.get("message", ""),
            })
            tag = " (cached)" if data.get("cached") else ""
            print(f"\n  {label}, call {attempt} -> {status} {elapsed:.2f}s{tag}")
            for line in text.splitlines():
                print(f"      {line}")
            if text and CODE_SMELL.search(text):
                print("      *** REVIEWER FLAG: looks like it contains code ***")
            time.sleep(5)

    # --- Numbers
    print("\n" + "=" * 72)
    print("METRICS")
    print("=" * 72)
    total = len(records)
    ok = [r for r in records if r["status"] == 200]
    withheld = [r for r in records if r["status"] == 502]
    cached = [r for r in records if r["cached"]]
    ratelimited = [r for r in records if r["status"] == 429]
    smelly = [r for r in ok if r["code_smell"]]
    live = [r["latency"] for r in records if not r["cached"] and r["status"] == 200]

    def pct(n):
        return f"{(100.0 * n / total):.0f}%" if total else "n/a"

    print(f"  requests                 {total}")
    print(f"  delivered (200)          {len(ok)}  {pct(len(ok))}")
    print(f"  withheld by filter (502) {len(withheld)}  {pct(len(withheld))}")
    print(f"  rate limited (429)       {len(ratelimited)}")
    print(f"  cache hits               {len(cached)}  {pct(len(cached))}")
    print(f"  delivered but code-like  {len(smelly)}   <- must be 0")
    if live:
        print(f"  latency mean/median/max  {statistics.mean(live):.2f}s / "
              f"{statistics.median(live):.2f}s / {max(live):.2f}s")
    print()
    print("  Useful student-safe hints / total real requests is a HUMAN")
    print("  judgement over the text printed above. These counts do not")
    print("  measure whether a hint was on-rung or worth reading.")

    if args.json_out:
        with open(args.json_out, "w") as f:
            json.dump(records, f, indent=2)
        print(f"\n  raw records -> {args.json_out}")

    return 1 if smelly else 0


if __name__ == "__main__":
    sys.exit(main())
