# Episodic memory

Memory is an open source Go engine that stores what happened, when, why, who was involved, and how it went. It recalls events using text, shared context and recency, while semantic facts stay in your application.

## Quick start

Start the service and PostgreSQL with Docker Compose:

```sh
docker compose up --build -d
```

Create a workspace and a service key with `memoryd`. The key is printed once; keep it in your shell:

```sh
docker compose exec memoryd /memoryd workspace -id demo
export MEMORY_KEY="$(docker compose exec -T memoryd /memoryd service \
  -workspace demo -id demo-client -actions read,write,sync \
  -installations manual/demo)"
```

Bind a source container. This grants the service access to the returned bucket:

```sh
export MEMORY_BUCKET="$(curl -fsS http://localhost:8090/v1/buckets \
  -X PUT -H "Authorization: Bearer $MEMORY_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"binding":{"system":"manual","installation":"demo","container":"journal"}}' \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')"
```

Write an event:

```sh
curl -fsS http://localhost:8090/v1/records \
  -H "Authorization: Bearer $MEMORY_KEY" \
  -H 'Content-Type: application/json' \
  -d "{\"events\":[{\"bucket\":\"$MEMORY_BUCKET\",\"id\":\"ev_demo\",\"type\":\"deployment.completed\",\"occurred_at\":\"2026-11-02T10:00:00Z\",\"why\":\"Resolve gateway timeouts.\",\"content\":[{\"text\":\"Deployed the retry fix successfully.\"}]}]}"
```

List events and recall relevant ones:

```sh
curl -fsS http://localhost:8090/v1/events \
  -H "Authorization: Bearer $MEMORY_KEY"

curl -fsS http://localhost:8090/v1/recall \
  -H "Authorization: Bearer $MEMORY_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"context":{"text":"gateway timeouts"},"explain":true}'
```

Recall strengthens returned events for the caller. To record an explicit use:

```sh
curl -fsS http://localhost:8090/v1/events/use \
  -H "Authorization: Bearer $MEMORY_KEY" \
  -H 'Content-Type: application/json' -d '{"events":["ev_demo"]}'
```

See [the record and HTTP specification](spec/SPEC.md), [architecture](spec/ARCHITECTURE.md), and [examples](spec/examples/). Licensed under [GNU AGPL-3.0](LICENSE).
