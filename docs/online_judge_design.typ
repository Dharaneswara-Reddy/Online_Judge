= CodeArena Online Judge — Design Document

== Evaluation system (Docker sandboxing)

Each submission is judged through an `internal/judge` Go package built
around two interfaces — `Sandbox` and `SubmissionSandbox` — so the
judging logic (verdict decisions) can be unit tested independently of
Docker, and the Docker mechanics are verified separately by
integration tests that assert the security properties directly (no
network access, memory cap enforced, timeout enforced), not just the
happy path.

One container is provisioned per *submission*, not per test case: the
program is compiled once inside it, then each test case runs via a
separate `docker exec` against the same container, avoiding redundant
recompilation. The container is destroyed (`ContainerRemove`,
`Force: true`) once all test cases finish or a step fails.

All five supported languages (C, C++, Java, Python, Go) run inside one
shared image (`codearena/judge-sandbox`) with every toolchain
pre-installed, rather than five per-language images — each submission
still gets its own isolated, resource-capped container regardless, so
this is an operational simplification with no isolation trade-off.

Container-level guarantees: `NetworkMode: none`, read-only root
filesystem with a size-capped `tmpfs` for the source file, a memory
ceiling with no swap headroom, a `PidsLimit` against fork bombs, and
non-root execution (`uid 1000`). A wall-clock timeout is enforced from
the Go side via `context.WithTimeout`, independent of any in-container
limit.
