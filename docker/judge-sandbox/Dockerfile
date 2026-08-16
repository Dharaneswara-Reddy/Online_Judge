FROM ubuntu:24.04

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    openjdk-17-jdk-headless \
    python3 \
    golang-go \
    && rm -rf /var/lib/apt/lists/*

RUN userdel -r ubuntu 2>/dev/null || true && \
    useradd -m -u 1000 -s /usr/sbin/nologin sandbox

# Remove every setuid/setgid bit. Untrusted code runs as uid 1000 here,
# and a local privilege escalation in any setuid-root binary (su, passwd,
# mount and friends) would hand it root inside the container — the usual
# first step of a container escape. Nothing in a judge needs them.
RUN find / -xdev -perm /6000 -type f -exec chmod ug-s {} + 2>/dev/null || true
WORKDIR /home/sandbox
