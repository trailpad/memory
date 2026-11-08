package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/trailpad/memory/internal/store"
)

type VectorIndex struct {
	store      *Store
	dimensions int
}

func (s *Store) NewVectorIndex(ctx context.Context, dimensions int) (*VectorIndex, error) {
	if dimensions < 1 || dimensions > 2000 {
		return nil, fmt.Errorf("embedding dimensions must be between 1 and 2000 for vector HNSW")
	}
	err := s.locked(ctx, func(conn *pgxpool.Conn) error {
		if _, err := conn.Exec(ctx, `create extension if not exists vector with schema public`); err != nil {
			return fmt.Errorf("enable pgvector: %w", err)
		}
		var supported bool
		if err := conn.QueryRow(ctx, `select string_to_array(extversion,'.')::int[] >= array[0,8,0] from pg_extension where extname='vector'`).Scan(&supported); err != nil {
			return err
		}
		if !supported {
			return fmt.Errorf("pgvector 0.8 or later is required")
		}
		_, err := conn.Exec(ctx, `create table if not exists event_vectors (
 workspace_id text not null,event_id text not null,bucket_id text not null,occurred_at timestamptz,
 type text not null,model text not null,dimensions integer not null,embedding vector not null,
 primary key(workspace_id,event_id,model))`)
		if err != nil {
			return err
		}
		_, err = conn.Exec(ctx, fmt.Sprintf(`create index if not exists event_vectors_hnsw_%d on event_vectors using hnsw ((embedding::vector(%d)) vector_cosine_ops) where dimensions=%d`, dimensions, dimensions, dimensions))
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &VectorIndex{s, dimensions}, nil
}
func (v *VectorIndex) Upsert(ctx context.Context, rows []store.EventVector) error {
	for _, r := range rows {
		if len(r.Vector) != v.dimensions {
			return fmt.Errorf("vector dimensions mismatch")
		}
		b, err := json.Marshal(r.Vector)
		if err != nil {
			return err
		}
		_, err = v.store.pool.Exec(ctx, `insert into event_vectors(workspace_id,event_id,bucket_id,occurred_at,type,model,dimensions,embedding) values($1,$2,$3,$4,$5,$6,$7,$8::vector)
 on conflict(workspace_id,event_id,model) do update set bucket_id=excluded.bucket_id,occurred_at=excluded.occurred_at,type=excluded.type,dimensions=excluded.dimensions,embedding=excluded.embedding`, r.Workspace, r.EventID, r.BucketID, r.OccurredAt, string(r.Type), r.Model, v.dimensions, string(b))
		if err != nil {
			return err
		}
	}
	return nil
}
func (v *VectorIndex) Delete(ctx context.Context, workspace string, ids []string) error {
	_, err := v.store.pool.Exec(ctx, `delete from event_vectors where workspace_id=$1 and event_id=any($2)`, workspace, ids)
	return err
}
func (v *VectorIndex) Nearest(ctx context.Context, workspace, model string, vector []float32, f store.VectorFilter) ([]store.VectorMatch, error) {
	rows, err := v.NearestBatch(ctx, workspace, model, [][]float32{vector}, f)
	if err != nil {
		return nil, err
	}
	return rows[0], nil
}
func (v *VectorIndex) NearestBatch(ctx context.Context, workspace, model string, vectors [][]float32, f store.VectorFilter) ([][]store.VectorMatch, error) {
	if f.K < 1 {
		return nil, fmt.Errorf("invalid vector query")
	}
	encoded := make([]string, len(vectors))
	for i, vector := range vectors {
		if len(vector) != v.dimensions {
			return nil, fmt.Errorf("invalid vector dimensions")
		}
		b, err := json.Marshal(vector)
		if err != nil {
			return nil, err
		}
		encoded[i] = string(b)
	}
	tx, err := v.store.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `set local hnsw.iterative_scan = relaxed_order; set local hnsw.max_scan_tuples = 1000000`); err != nil {
		return nil, err
	}
	requested := make([]string, len(f.Types))
	for i, t := range f.Types {
		requested[i] = string(t)
	}
	sql := fmt.Sprintf(`select input.position,nearest.event_id,1-nearest.distance
 from unnest($3::text[]) with ordinality input(vector,position)
 cross join lateral (with neighbours as materialized (
 select event_id,embedding::vector(%d) <=> input.vector::vector as distance
 from event_vectors where workspace_id=$1 and model=$2 and dimensions=%d and bucket_id=any($4::text[])
 and ($5::timestamptz is null or occurred_at >= $5) and ($6::timestamptz is null or occurred_at < $6)
 and (coalesce(cardinality($7::text[]),0)=0 or exists(select 1 from unnest($7::text[]) t where type=t or starts_with(type,t||'.')))
 order by embedding::vector(%d) <=> input.vector::vector limit $8)
 select * from neighbours order by distance+0,event_id) nearest
 order by input.position,nearest.distance+0,nearest.event_id`, v.dimensions, v.dimensions, v.dimensions)
	rows, err := tx.Query(ctx, sql, workspace, model, encoded, f.Buckets, f.From, f.To, requested, f.K)
	if err != nil {
		return nil, err
	}
	out := make([][]store.VectorMatch, len(vectors))
	for rows.Next() {
		var position int
		var r store.VectorMatch
		if err := rows.Scan(&position, &r.EventID, &r.Cosine); err != nil {
			rows.Close()
			return nil, err
		}
		out[position-1] = append(out[position-1], r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit(ctx)
}

var _ store.VectorIndex = (*VectorIndex)(nil)
var _ store.BatchVectorIndex = (*VectorIndex)(nil)
