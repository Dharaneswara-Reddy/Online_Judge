# CodeArena production deployment

One EC2 host running the whole stack under Docker Compose, with MongoDB
on Atlas. No ALB, no ECS, no NAT gateway — the instance sits in a public
subnet of the default VPC and only port 80 is open.

## Layout

    Internet :80
        │
        ▼
    nginx (frontend)  ── /api  ──▶  api ──┐
        │                                  ├─▶ RabbitMQ ──▶ worker ──▶ Docker judge
        └─ static bundle (Monaco local)    └─▶ Redis
                                                 │
                                                 ▼
                                          MongoDB Atlas

Only `frontend` publishes a port. RabbitMQ, Redis and the API are
reachable only on the compose network.

## First deploy

    ssh -i codearena-key.pem ec2-user@<PUBLIC_IP>
    git clone <repo> /opt/codearena && cd /opt/codearena

Create `deploy/.env` on the host — never commit it:

    MONGO_URI=<atlas connection string>
    DB_NAME=online_judge
    JWT_SECRET=<openssl rand -base64 48>
    CLIENT_URL=http://<PUBLIC_IP>
    RABBITMQ_USER=<user>
    RABBITMQ_PASSWORD=<password>
    WORKER_COUNT=2
    MAX_SANDBOXES=1

    # AI assistant (optional). Absent means the assist endpoints answer
    # 503, the client hides the feature, and judging is unaffected.
    GROQ_API_KEY=<groq key>
    ASSIST_MODEL=openai/gpt-oss-120b

Only the `api` service receives the assist variables — the worker judges
code and never calls a model, so it is not given the key. That separation
is in `docker-compose.prod.yml` and is worth preserving: the worker is the
process holding the Docker socket, and it has no business holding a
credential it cannot use.

After adding the key, restart just the API:

    docker compose --env-file .env -f docker-compose.prod.yml up -d api

The startup log says which provider and model it picked, and a background
check reports whether that model is actually available:

    Assist: enabled using Groq, model openai/gpt-oss-120b
    Assist: verified the configured model is available

A rejected credential is reported as such rather than as a missing model,
because the two have different fixes.

Get the judge sandbox image (the worker starts a container from it for
every submission). **Do not build it here** — it apt-installs a JDK and
build-essential, which is the heaviest build in the project and exactly
the kind of thing that has taken this instance down. Pull the published
one and give it the local tag the worker looks for:

    docker pull ghcr.io/dharaneswara-reddy/codearena-sandbox:latest
    docker tag  ghcr.io/dharaneswara-reddy/codearena-sandbox:latest codearena-sandbox:latest

The retag is needed because the worker refers to `codearena-sandbox:latest`
by a constant in the Go source rather than by configuration. Repeat both
lines any time the sandbox image changes, and after anything that prunes
images.

Then pull the application images and bring the stack up. There is no
`--build` here and no `build:` stanza in the compose file: see "Rolling
out a new build" below for why building on this host is forbidden.

    cd deploy
    docker compose --env-file .env -f docker-compose.prod.yml pull
    docker compose --env-file .env -f docker-compose.prod.yml up -d --no-build

## Operating

    # logs
    docker compose -f docker-compose.prod.yml logs -f api
    docker compose -f docker-compose.prod.yml logs -f worker

    # restart one service
    docker compose -f docker-compose.prod.yml restart api
    docker compose -f docker-compose.prod.yml restart worker
    docker compose -f docker-compose.prod.yml restart rabbitmq

    # restart everything
    docker compose -f docker-compose.prod.yml restart

    # redeploy after a code change — see "Rolling out a new build"
    git pull --ff-only
    docker compose -f docker-compose.prod.yml pull
    docker compose -f docker-compose.prod.yml up -d --no-build

`restart: unless-stopped` on every service means the stack comes back by
itself after a reboot, once the Docker daemon starts.

### WORKER_COUNT and MAX_SANDBOXES

These are different settings and the difference matters.

`WORKER_COUNT` is queue prefetch: how many deliveries each consumer
accepts at once. `MAX_SANDBOXES` is the hard ceiling on judge containers
alive on this host at any moment, enforced by a semaphore around
container creation and shared by all three consumers in the worker
process.

They used to be one number, and that was a bug: the worker runs three
consumers — the standard lane, the War Room lane and the playground
responder — each sized from `WORKER_COUNT`, so `WORKER_COUNT=2` meant up
to **six** containers, not two. Each container may claim a full vCPU and
up to 512 MB on a 916 MB host.

The memory budget written out at the top of `docker-compose.prod.yml`
has room for exactly one sandbox. **Raising `MAX_SANDBOXES` without
redoing that arithmetic re-opens the out-of-memory failure that has
already taken this host down twice.** The worker logs its real ceiling
at startup; check it there rather than inferring it from settings.

## Health

    curl http://<PUBLIC_IP>/            # frontend
    curl http://<PUBLIC_IP>/api/problems  # API through the nginx proxy

## Rolling out a new build

Images are built by GitHub Actions (`.github/workflows/release.yml`) and
published to GHCR. **Do not build on the instance.** It is a t3.micro
with 916 MB of RAM; compiling the Go binaries and the Monaco frontend
bundle there has twice exhausted its memory badly enough to take the
site down, once so completely that `sshd` could no longer fork a session
and the host had to be rebooted.

A rollout is therefore a pull and a restart:

```bash
cd /opt/codearena && git pull --ff-only
cd deploy
docker compose -f docker-compose.prod.yml pull
docker compose -f docker-compose.prod.yml up -d --no-build
```

`git pull` is still needed because the compose file itself lives in the
repository; the application code arrives inside the images.

To roll out one exact build rather than whatever `latest` points at, set
the tag explicitly — this is also how to roll back:

```bash
SERVER_IMAGE=ghcr.io/dharaneswara-reddy/codearena-server:<sha> \
  docker compose -f docker-compose.prod.yml up -d --no-build api worker
```

### Verify afterwards

```bash
docker compose -f docker-compose.prod.yml ps
curl -sf http://127.0.0.1/ >/dev/null            && echo "frontend ok"
curl -sf http://127.0.0.1/api/problems >/dev/null && echo "api ok"
```

The API log should say which playground path it chose:

```
Playground: delegating runs to a judge worker over the queue
```

If it says `running code in-process` instead, the API believes it can
reach Docker — which in this deployment means something is wrong, since
the API container is deliberately given no daemon socket.

### If the API returns 502 after a rollout

Recreating the `api` container gives it a new IP. nginx resolves an
upstream name once at startup unless the address comes from a variable,
so an older frontend image keeps proxying to the dead address. The
current `client/nginx.conf` resolves per request through Docker's
embedded DNS and is not affected; an image built before that fix needs
`docker compose -f docker-compose.prod.yml restart frontend`.

## Migrating to t4g.small (arm64)

Production runs on a `t3.micro` (x86, 1 GiB). The target is a `t4g.small`
(arm64/Graviton2, 2 GiB): twice the memory, four times the sustained CPU
(20% baseline earning 24 credits/hour, against 10% and 6), and free under
the T4g trial — 750 hours per month for every AWS account until
2026-12-31, which covers one instance running continuously.

The images are published as multi-arch manifest lists, so nothing here
names an architecture: the daemon on each host pulls the right one, and
the x86 instance can still run the same tag. That is what makes the
rollback below cheap.

### What must change on the new host

`deploy/.env` gains one line:

    MAX_SANDBOXES=2

It stays at 1 on any 1 GiB host. Two is the ceiling on this instance and
raising it is not a tuning knob — see the memory and CPU arithmetic at the
top of `docker-compose.prod.yml`. The short version: two sandboxes at
1 vCPU each is exactly a 2 vCPU box, and a third would make sandboxes
share cores, which produces time-limit failures for programs that never
exceeded their limit.

### Migration

Do not terminate the old instance at any point in this list.

1. Confirm CI is green for the commit you intend to deploy, and that all
   three images exist for it: `docker manifest inspect` each
   `ghcr.io/.../codearena-{server,client,sandbox}:<sha>` and check
   `linux/arm64` is present.
2. Launch a `t4g.small` in the existing VPC (`vpc-0c69760d850a464ef`) and
   the existing security group. Create nothing else — no ALB, no NAT
   gateway, no extra Elastic IP, no extra volume.
3. Note the new public IP and add it to MongoDB Atlas network access as a
   `/32`. **Leave the old instance's entry in place** — it is the
   rollback. Never widen to `0.0.0.0/0`.
4. Install Docker and the compose plugin. Copy `deploy/.env` across by
   hand; it is git-ignored and holds the secrets. Add `MAX_SANDBOXES=2`.
5. Pull and start, exactly as a normal rollout — never build on the host:

       docker compose -f docker-compose.prod.yml pull
       docker compose -f docker-compose.prod.yml up -d --no-build

6. Verify on the new instance before sending it any traffic:

       uname -m                     # expect aarch64
       docker compose ps            # every service healthy
       curl -fsS localhost/healthz  # liveness
       curl -fsS localhost/readyz   # database up, queue up

   Then submit one solution of each verdict, run the playground once, and
   confirm `docker ps -aq --filter label=codearena.sandbox=judge` is empty
   afterwards.
7. Only once all of that passes, move traffic by pointing `CLIENT_URL`
   and whatever address users reach at the new instance.
8. Leave the old instance **stopped, not terminated**, for at least a few
   days. A stopped instance costs only its EBS volume and is the fastest
   possible rollback.

### Rollback and HSTS

**Read this before rolling back.** The site now sends
`Strict-Transport-Security: max-age=31536000` over HTTPS. Any browser
that has loaded the site since TLS went live will refuse to talk to
`codearena-live.duckdns.org` over plain HTTP for a year — it upgrades the
request itself and fails closed if TLS is unavailable. That is the header
doing its job, and it is exactly why HSTS must not be switched off to
make a rollback convenient: turning it off does nothing for browsers that
already hold the pin.

The consequence is concrete: **the old t3.micro serves HTTP only, so
pointing DNS back at it strands every returning visitor.** New visitors
would be fine; anyone who has been here since the switch would see a
connection failure, not a warning they can click through.

So a rollback that moves the hostname has one prerequisite:

1. **The rollback target must serve HTTPS for the same hostname first.**
   Obtain a certificate on the old instance *before* moving DNS. The
   instance is deliberately untouched, so this is a manual step and needs
   explicit authorisation:

       # on 100.62.87.249, only with authorisation
       sudo mkdir -p /etc/letsencrypt /var/www/certbot
       # DNS must already point here for HTTP-01 to validate, so do this
       # during a maintenance window, or use a DNS-01 challenge instead
       sudo docker run --rm -p 80:80 \
         -v /etc/letsencrypt:/etc/letsencrypt \
         -v /var/lib/letsencrypt:/var/lib/letsencrypt \
         certbot/certbot certonly --standalone \
         -d codearena-live.duckdns.org --non-interactive --agree-tos \
         --register-unsafely-without-email --key-type ecdsa

   Then deploy the current compose file and client image there, which
   already carry the TLS server block and the certificate mounts.

2. Only then move DNS back.

**The faster option, and the one to prefer:** roll back the *images*
rather than the *host*. Every build is published under its commit SHA, so
the current instance can be put on an older build without touching DNS,
HSTS or certificates at all:

    # in deploy/.env on 3.208.90.142
    SERVER_IMAGE=ghcr.io/dharaneswara-reddy/codearena-server:<sha>
    CLIENT_IMAGE=ghcr.io/dharaneswara-reddy/codearena-client:<sha>

    docker compose -f docker-compose.prod.yml pull
    docker compose -f docker-compose.prod.yml up -d --no-build

That covers every failure caused by a code change, which is the large
majority. Falling back to the old *instance* is only for losing the
machine itself, and that is the case the certificate prerequisite above
applies to.

### Rollback

The old instance keeps its Atlas allowlist entry and its EBS volume, and
the images are multi-arch, so rolling back is starting it again:

1. Start the `t3.micro`.
2. Confirm its public IP is still allowed in Atlas — a stop/start assigns
   a new one unless an Elastic IP is attached, in which case add the new
   `/32` before proceeding.
3. `docker compose -f docker-compose.prod.yml up -d --no-build` — the
   same tags resolve to amd64 there.
4. Point traffic back.
5. Leave the t4g.small running so the failure can be diagnosed rather
   than destroyed.

If the arm64 images themselves are the problem, the previous `:<sha>`
tags are all still in GHCR and any of them can be pinned through
`SERVER_IMAGE` / `CLIENT_IMAGE` in `deploy/.env`.
