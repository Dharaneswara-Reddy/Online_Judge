FROM ubuntu:24.04

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    openjdk-17-jdk-headless \
    python3 \
    golang-go \
    && rm -rf /var/lib/apt/lists/*

RUN userdel -r ubuntu 2>/dev/null || true && \
    useradd -m -u 1000 -s /usr/sbin/nologin sandbox

# Pre-warm the Go build cache.
#
# Every submission runs in a fresh container, so GOCACHE started empty
# every single time and each Go submission recompiled the parts of the
# standard library it touched from scratch. Measured in this image with
# the judge's own limits (1 CPU, 256 MB), that cold compile takes about
# 12 seconds against a 15 second compile timeout — a three second margin
# that disappears entirely on a slower or busier host. It is also why the
# containment tests failed on GitHub's runners, on x86 as well as arm64.
#
# Compiling a file that imports the packages competitive submissions
# actually use populates the cache with those stdlib entries. The same
# compile afterwards takes about 0.9 seconds: the work was already done,
# once, at image build time.
#
# The cache is read-only at runtime, which is deliberate — the sandbox
# rootfs is read-only and nothing here weakens that. Go reads the entries
# it needs and writes the user's own build output to the writable tmpfs,
# so the untrusted process still cannot modify anything shared between
# submissions.
RUN mkdir -p /opt/gocache /tmp/warm && \
    printf 'package main\n\nimport (\n\t"bufio"\n\t"fmt"\n\t"io"\n\t"math"\n\t"os"\n\t"sort"\n\t"strconv"\n\t"strings"\n)\n\nfunc main() {\n\t_ = bufio.NewReader(os.Stdin)\n\t_, _ = io.ReadAll(os.Stdin)\n\t_ = sort.Ints\n\t_ = strings.TrimSpace\n\t_, _ = strconv.Atoi("1")\n\t_ = math.Abs\n\tfmt.Println()\n}\n' > /tmp/warm/main.go && \
    cd /tmp/warm && GOCACHE=/opt/gocache go build -buildvcs=false -o /dev/null main.go && \
    chmod -R a+rX /opt/gocache && rm -rf /tmp/warm

# Remove every setuid/setgid bit. Untrusted code runs as uid 1000 here,
# and a local privilege escalation in any setuid-root binary (su, passwd,
# mount and friends) would hand it root inside the container — the usual
# first step of a container escape. Nothing in a judge needs them.
RUN find / -xdev -perm /6000 -type f -exec chmod ug-s {} + 2>/dev/null || true
WORKDIR /home/sandbox
