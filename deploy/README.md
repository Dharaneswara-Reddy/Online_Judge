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

Build the judge sandbox image (the worker starts containers from it):

    docker build -t codearena-sandbox:latest docker/judge-sandbox

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
