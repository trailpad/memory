package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/trailpad/memory/internal/embed"
	"github.com/trailpad/memory/internal/record"
	"github.com/trailpad/memory/internal/store"
)

func (s *Store) PrepareEmbeddings(ctx context.Context, e embed.Embedder, reset bool) error {
	_, err := s.pool.Exec(ctx, `update embedding_jobs set model=$1,dimensions=$2,complete=false,attempts=0,retry_at=now() where model<>$1 or dimensions<>$2 or $3`, e.Model(), e.Dimensions(), reset)
	return err
}
func (s *Store) EmbeddingBacklog(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `select count(*) from embedding_jobs where not complete`).Scan(&n)
	return n, err
}
func (s *Store) EmbeddingFailing(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `select count(*) from embedding_jobs where not complete and attempts>0`).Scan(&n)
	return n, err
}
func (s *Store) logEmbeddingFailure(ctx context.Context, err error) {
	s.embeddingLogMu.Lock()
	defer s.embeddingLogMu.Unlock()
	if time.Since(s.embeddingLogAt) >= time.Minute {
		s.embeddingLogAt = time.Now()
		s.embeddingLogCount = 0
	}
	if s.embeddingLogCount >= 5 {
		return
	}
	s.embeddingLogCount++
	logger := s.EmbeddingLog
	if logger == nil {
		logger = slog.Default()
	}
	status, excerpt := 0, ""
	var response *embed.ResponseError
	if errors.As(err, &response) {
		status, excerpt = response.Status, response.Excerpt
	}
	logger.WarnContext(ctx, "embedding batch failed", "status", status, "provider_error", excerpt)
}
func (s *Store) EmbedBatch(ctx context.Context, e embed.Embedder, index store.VectorIndex) (int, error) {
	rows, err := s.pool.Query(ctx, `select j.workspace_id,e.body from embedding_jobs j join events e on e.workspace_id=j.workspace_id and e.id=j.event_id where not j.complete and j.retry_at<=now() order by j.retry_at,j.workspace_id,j.event_id limit 32`)
	if err != nil {
		return 0, err
	}
	var empty []store.EventVector
	var pending []store.EventVector
	var texts []string
	for rows.Next() {
		var ws string
		var body []byte
		if err := rows.Scan(&ws, &body); err != nil {
			rows.Close()
			return 0, err
		}
		var event record.Event
		if err := json.Unmarshal(body, &event); err != nil {
			rows.Close()
			return 0, err
		}
		var occurred *time.Time
		if event.OccurredAt != nil {
			occurred = &event.OccurredAt.At
		}
		text := embed.EventText(event)
		if strings.TrimSpace(text) == "" {
			empty = append(empty, store.EventVector{Workspace: ws, EventID: event.ID})
			continue
		}

		pending = append(pending, store.EventVector{Workspace: ws, EventID: event.ID, BucketID: event.Bucket, OccurredAt: occurred, Type: event.Type, Model: e.Model()})
		texts = append(texts, text)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, r := range empty {
		if err := index.Delete(ctx, r.Workspace, []string{r.EventID}); err != nil {
			return 0, err
		}
		if _, err := s.pool.Exec(ctx, `update embedding_jobs set complete=true,model=$3,dimensions=$4 where workspace_id=$1 and event_id=$2`, r.Workspace, r.EventID, e.Model(), e.Dimensions()); err != nil {
			return 0, err
		}
	}
	if len(pending) == 0 {
		return len(empty), nil
	}
	return len(pending) + len(empty), s.embedItems(ctx, e, index, pending, texts, true)
}
func (s *Store) embedItems(ctx context.Context, e embed.Embedder, index store.VectorIndex, pending []store.EventVector, texts []string, split bool) error {
	vectors, err := e.Embed(ctx, texts)
	if err != nil {
		s.logEmbeddingFailure(ctx, err)
		var response *embed.ResponseError
		if split && len(pending) > 1 && errors.As(err, &response) && response.Status >= 400 && response.Status < 500 {
			var failures []error
			for i := range pending {
				failures = append(failures, s.embedItems(ctx, e, index, pending[i:i+1], texts[i:i+1], false))
			}
			return errors.Join(failures...)
		}
	}
	if err == nil && len(vectors) != len(pending) {
		err = fmt.Errorf("embedding count mismatch")
		s.logEmbeddingFailure(ctx, err)
	}
	if err == nil {
		for i := range pending {
			pending[i].Vector = vectors[i]
		}
		err = index.Upsert(ctx, pending)
		if err != nil {
			s.logEmbeddingFailure(ctx, err)
		}
	}
	for _, r := range pending {
		var writeErr error
		if err != nil {
			_, writeErr = s.pool.Exec(ctx, `update embedding_jobs set attempts=attempts+1,retry_at=now()+make_interval(secs=>least(3600,power(2,least(attempts+1,12)))::int) where workspace_id=$1 and event_id=$2`, r.Workspace, r.EventID)
		} else {
			_, writeErr = s.pool.Exec(ctx, `update embedding_jobs set complete=true,model=$3,dimensions=$4 where workspace_id=$1 and event_id=$2`, r.Workspace, r.EventID, e.Model(), e.Dimensions())
		}
		if writeErr != nil {
			return errors.Join(err, writeErr)
		}
	}
	return err
}

func (s *Store) RunEmbeddings(ctx context.Context, e embed.Embedder, index store.VectorIndex) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		n, err := s.EmbedBatch(ctx, e, index)
		if err == nil && n > 0 {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
