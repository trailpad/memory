package httpapi_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/trailpad/memory/internal/store/postgres"
)

type candidateTrace struct {
	mu   sync.Mutex
	sql  string
	args []any
}

func (c *candidateTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, "total_candidates>200") {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.sql = data.SQL
		c.args = append([]any(nil), data.Args...)
	}
	return ctx
}
func (*candidateTrace) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
func TestLinkCandidatePlan(t *testing.T) {
	e := newEngine(t)
	bucket := e.repository("repo_plan", "ev_seed", "Setup")
	entities := []map[string]string{}
	events := []map[string]any{}
	for i := 0; i < 100; i++ {
		entities = append(entities, map[string]string{"id": fmt.Sprintf("sb_plan_%03d", i), "bucket": bucket, "class": "subject", "type": "code.path"}, map[string]string{"id": fmt.Sprintf("pl_plan_%03d", i), "bucket": bucket, "class": "place", "type": "room"})
	}
	events = append(events, map[string]any{"id": "ev_plan_parent", "bucket": bucket, "type": "observation", "occurred_at": "2026-10-21T00:00:00Z", "place": "pl_plan_000"})
	for i := 0; i < 1000; i++ {
		events = append(events, map[string]any{"id": fmt.Sprintf("ev_plan_%04d", i), "bucket": bucket, "type": "observation", "occurred_at": "2026-10-21T12:00:00Z", "in_reply_to": "ev_plan_parent", "place": fmt.Sprintf("pl_plan_%03d", i%100), "subjects": []map[string]string{{"subject": fmt.Sprintf("sb_plan_%03d", i%100), "role": "about"}}})
		if i >= 10 {
			delete(events[len(events)-1], "in_reply_to")
		}
	}
	e.must(200, "POST", "/v1/records", e.key, "", map[string]any{"entities": entities, "events": events})
	e.readers(bucket, "gh_ana")
	token := e.sign(e.claims("user_ana", "gh_ana"))
	if _, err := e.store.Pool().Exec(t.Context(), `analyze events; analyze involvements; analyze entities`); err != nil {
		t.Fatal(err)
	}
	trace := &candidateTrace{}
	config := e.store.Pool().Config()
	config.ConnConfig.Tracer = trace
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	e.recall.Store = postgres.New(pool)
	e.must(200, "POST", "/v1/events/links", e.key, token, map[string]any{"buckets": []string{bucket}, "from": "2026-10-21T00:00:00Z", "to": "2026-10-22T00:00:00Z", "anchors_limit": 2, "approaches": []string{"thread", "subject", "time"}})
	trace.mu.Lock()
	defer trace.mu.Unlock()
	if trace.sql == "" {
		t.Fatal("candidate query was not captured")
	}
	rows, err := e.store.Pool().Query(t.Context(), "explain (analyze,buffers,format text) "+trace.sql, trace.args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	plan := []string{}
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plan, "\n")
	for _, name := range []string{"involvements_entity_via", "events_place_time", "events_reply"} {
		if !strings.Contains(joined, name) {
			t.Errorf("query did not use %s", name)
		}
	}
	t.Log(joined)
}
