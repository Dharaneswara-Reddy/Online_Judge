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

Then bring the stack up:

    cd deploy && docker compose --env-file .env -f docker-compose.prod.yml up -d --build

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

    # redeploy after a code change
    git pull && docker compose -f docker-compose.prod.yml up -d --build

`restart: unless-stopped` on every service means the stack comes back by
itself after a reboot, once the Docker daemon starts.

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
