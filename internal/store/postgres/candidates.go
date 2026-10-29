package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/trailpad/memory/internal/record"
	"github.com/trailpad/memory/internal/store"
)

// maxDepth bounds walks along parent chains, so a cycle cannot run forever.
const maxDepth = 16

type query struct{ args []any }

func (q *query) arg(v any) string {
	q.args = append(q.args, v)
	return fmt.Sprintf("$%d", len(q.args))
}

func allows(kinds []string, kind string) bool {
	if len(kinds) == 0 {
		return true
	}
	for _, k := range kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// readable is the condition that a row of alias is in the access's
// workspace and that it may read every bucket the row requires.
func (q *query) readable(alias string, a store.Access) string {
	return alias + `.workspace_id = ` + q.arg(a.Workspace) + ` and ` + alias + `.requires <@ ` + q.arg(a.Read) + `::text[]`
}

// visible is a CTE of every record in scope that the access may read:
// (kind, id, version).
func (q *query) visible(a store.Access, s store.Scope) string {
	var parts []string
	inBuckets := func(alias string) []string {
		if len(s.Buckets) == 0 {
			return nil
		}
		return []string{alias + `.bucket_id = any(` + q.arg(s.Buckets) + `::text[])`}
	}
	if allows(s.Kinds, store.MemoryKind) {
		where := append([]string{q.readable("m", a)}, inBuckets("m")...)
		if s.AsOf == nil {
			where = append(where, `m.superseded_at is null`)
		} else {
			at := q.arg(*s.AsOf)
			where = append(where, `m.recorded_at <= `+at+` and (m.superseded_at is null or m.superseded_at > `+at+`)`)
		}
		if s.ValidAt != nil {
			at := q.arg(*s.ValidAt)
			where = append(where, `(m.valid_from is null or m.valid_from <= `+at+`) and (m.valid_until is null or m.valid_until > `+at+`)`)
		}
		if len(s.Types) > 0 {
			where = append(where, `m.type_lineage && `+q.arg(types(s.Types))+`::text[]`)
		}
		happened := `coalesce(m.valid_from, (select min(f.recorded_at) from memories f where f.workspace_id = m.workspace_id and f.id = m.id))`
		if s.OccurredFrom != nil {
			where = append(where, happened+` >= `+q.arg(*s.OccurredFrom))
		}
		if s.OccurredTo != nil {
			where = append(where, happened+` < `+q.arg(*s.OccurredTo))
		}
		parts = append(parts, `select 'memory'::text as kind, m.id, m.version from memories m where `+strings.Join(where, ` and `))
	}
	if allows(s.Kinds, store.EventKind) {
		where := append([]string{q.readable("e", a)}, inBuckets("e")...)
		if s.AsOf != nil {
			where = append(where, `e.recorded_at <= `+q.arg(*s.AsOf))
		}
		if len(s.Types) > 0 {
			where = append(where, `e.type_lineage && `+q.arg(types(s.Types))+`::text[]`)
		}
		happened := `coalesce(e.occurred_at, e.recorded_at)`
		if s.OccurredFrom != nil {
			where = append(where, happened+` >= `+q.arg(*s.OccurredFrom))
		}
		if s.OccurredTo != nil {
			where = append(where, happened+` < `+q.arg(*s.OccurredTo))
		}
		parts = append(parts, `select 'event'::text as kind, e.id, 0 as version from events e where `+strings.Join(where, ` and `))
	}
	if len(parts) == 0 {
		return `visible as (select null::text as kind, null::text as id, 0 as version where false)`
	}
	return `visible as (` + strings.Join(parts, ` union all `) + `)`
}

// under is a CTE of every entity below each cue that the access may read:
// (cue, entity, depth). The walk stops at an entity it may not read.
func (q *query) under(a store.Access, cues []string) string {
	return fmt.Sprintf(`under(cue, entity, depth) as (
		select x.id, x.id, 0 from entities x where %s and x.id = any(%s)
		union all
		select u.cue, c.id, u.depth + 1 from under u join entities c on c.parent_id = u.entity
		where %s and u.depth < %d
	)`, q.readable("x", a), q.arg(cues), q.readable("c", a), maxDepth)
}

func (s *Store) Text(ctx context.Context, access store.Access, scope store.Scope, text string, limit int) ([]store.Scored, error) {
	access, err := s.live(ctx, access)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	q := &query{}
	visible := q.visible(access, scope)
	// Any word may match; records matching more of them rank higher.
	sql := `with ` + visible + `, q as (select nullif(replace(plainto_tsquery('english', ` + q.arg(text) + `)::text, '&', '|'), '')::tsquery as q)
		select v.kind, v.id, ts_rank_cd(coalesce(m.said, e.said), q.q) from visible v
		left join memories m on v.kind = 'memory' and m.workspace_id = ` + q.arg(access.Workspace) + ` and m.id = v.id and m.version = v.version
		left join events e on v.kind = 'event' and e.workspace_id = ` + q.arg(access.Workspace) + ` and e.id = v.id
		cross join q
		where q.q is not null and coalesce(m.said, e.said) @@ q.q
		order by ts_rank_cd(coalesce(m.said, e.said), q.q) desc, v.kind, v.id
		limit ` + q.arg(limit)
	rows, err := s.pool.Query(ctx, sql, q.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Scored
	for rows.Next() {
		var r store.Scored
		if err := rows.Scan(&r.Ref.Kind, &r.Ref.ID, &r.Score); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Ancestors walks up only through entities the access may read: a parent it
// may not read ends the chain.
func (s *Store) Ancestors(ctx context.Context, access store.Access, ids []string) (map[string][]store.Hop, error) {
	access, err := s.live(ctx, access)
	if err != nil {
		return nil, err
	}
	out := map[string][]store.Hop{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		with recursive up(entity, ancestor, depth) as (
			select id, id, 0 from entities where workspace_id = $1 and requires <@ $2 and id = any($3)
			union all
			select u.entity, p.id, u.depth + 1 from up u
			join entities c on c.workspace_id = $1 and c.id = u.ancestor
			join entities p on p.workspace_id = $1 and p.id = c.parent_id and p.requires <@ $2
			where u.depth < %d
		)
		select entity, ancestor, min(depth) from up group by entity, ancestor order by entity, min(depth)`, maxDepth),
		access.Workspace, access.Read, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var h store.Hop
		if err := rows.Scan(&id, &h.Entity, &h.Depth); err != nil {
			return nil, err
		}
		out[id] = append(out[id], h)
	}
	return out, rows.Err()
}

func (s *Store) Frequency(ctx context.Context, access store.Access, scope store.Scope, cues []string) (map[string]int, int, error) {
	access, err := s.live(ctx, access)
	if err != nil {
		return nil, 0, err
	}
	q := &query{}
	visible := q.visible(access, scope)
	var total int
	if err := s.pool.QueryRow(ctx, `with `+visible+` select count(*) from visible`, q.args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	out := map[string]int{}
	if len(cues) == 0 {
		return out, total, nil
	}
	q = &query{}
	sql := `with recursive ` + q.visible(access, scope) + `, ` + q.under(access, cues) + `
		select u.cue, count(distinct (v.kind, v.id)) from under u
		join involvements i on i.workspace_id = ` + q.arg(access.Workspace) + ` and i.entity_id = u.entity
		join visible v on v.kind = i.record_kind and v.id = i.record_id and v.version = i.version
		group by u.cue`
	rows, err := s.pool.Query(ctx, sql, q.args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var cue string
		var n int
		if err := rows.Scan(&cue, &n); err != nil {
			return nil, 0, err
		}
		out[cue] = n
	}
	return out, total, rows.Err()
}

func (s *Store) Touches(ctx context.Context, access store.Access, scope store.Scope, cues []string, only []store.Ref, limit int) ([]store.Touch, error) {
	access, err := s.live(ctx, access)
	if err != nil {
		return nil, err
	}
	if len(cues) == 0 {
		return nil, nil
	}
	q := &query{}
	sql := `with recursive ` + q.visible(access, scope) + `, ` + q.under(access, cues) + `,
		touches as (
			select v.kind, v.id, i.entity_id, i.role, u.cue, u.depth from under u
			join involvements i on i.workspace_id = ` + q.arg(access.Workspace) + ` and i.entity_id = u.entity
			join visible v on v.kind = i.record_kind and v.id = i.record_id and v.version = i.version
		)`
	if only != nil {
		kinds := make([]string, len(only))
		ids := make([]string, len(only))
		for i, r := range only {
			kinds[i], ids[i] = r.Kind, r.ID
		}
		sql += ` select distinct t.* from touches t
			join unnest(` + q.arg(kinds) + `::text[], ` + q.arg(ids) + `::text[]) as o(kind, id) on o.kind = t.kind and o.id = t.id`
	} else {
		sql += `, chosen as (
			select kind, id from touches group by kind, id
			order by count(distinct cue) desc, kind, id limit ` + q.arg(limit) + `
		)
		select distinct t.* from touches t join chosen c on c.kind = t.kind and c.id = t.id`
	}
	rows, err := s.pool.Query(ctx, sql, q.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Touch
	for rows.Next() {
		var t store.Touch
		var role string
		if err := rows.Scan(&t.Ref.Kind, &t.Ref.ID, &t.Entity, &role, &t.Cue, &t.Depth); err != nil {
			return nil, err
		}
		t.Role = record.Role(role)
		out = append(out, t)
	}
	return out, rows.Err()
}

// Records reads candidates. A memory's uses are the access's principal's
// own: another's never show.
func (s *Store) Records(ctx context.Context, access store.Access, scope store.Scope, refs []store.Ref) ([]store.Found, error) {
	access, err := s.live(ctx, access)
	if err != nil {
		return nil, err
	}
	if len(refs) == 0 {
		return nil, nil
	}
	kinds := make([]string, len(refs))
	ids := make([]string, len(refs))
	for i, r := range refs {
		kinds[i], ids[i] = r.Kind, r.ID
	}
	q := &query{}
	visible := q.visible(access, scope)
	ws := q.arg(access.Workspace)
	used := ` and h.principal = ` + q.arg(access.Principal)
	if scope.AsOf != nil {
		used += ` and h.recorded_at <= ` + q.arg(*scope.AsOf)
	}
	if scope.UsedBy != nil {
		used += ` and h.at <= ` + q.arg(*scope.UsedBy)
	}
	sql := `with ` + visible + `
		select distinct v.kind, v.id, coalesce(m.body, e.body),
			case when v.kind = 'memory'
				then coalesce(m.valid_from, (select min(f.recorded_at) from memories f where f.workspace_id = ` + ws + ` and f.id = v.id))
				else coalesce(e.occurred_at, e.recorded_at) end,
			r.count, r.last
		from visible v
		join unnest(` + q.arg(kinds) + `::text[], ` + q.arg(ids) + `::text[]) as want(kind, id) on want.kind = v.kind and want.id = v.id
		left join memories m on v.kind = 'memory' and m.workspace_id = ` + ws + ` and m.id = v.id and m.version = v.version
		left join events e on v.kind = 'event' and e.workspace_id = ` + ws + ` and e.id = v.id
		left join lateral (
			select count(*) as count, max(h.at) as last from rehearsals h
			where v.kind = 'memory' and h.workspace_id = ` + ws + ` and h.memory_id = v.id` + used + `
		) r on true`
	rows, err := s.pool.Query(ctx, sql, q.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Found
	for rows.Next() {
		var f store.Found
		var body []byte
		var count int
		var last *time.Time
		if err := rows.Scan(&f.Ref.Kind, &f.Ref.ID, &body, &f.Happened, &count, &last); err != nil {
			return nil, err
		}
		if f.Ref.Kind == store.MemoryKind {
			var m record.Memory
			if err := json.Unmarshal(body, &m); err != nil {
				return nil, err
			}
			m.Recall = &record.Rehearsal{Count: count}
			if last != nil {
				at := record.Exact(*last)
				m.Recall.LastAt = &at
			}
			f.Memory = &m
		} else {
			var e record.Event
			if err := json.Unmarshal(body, &e); err != nil {
				return nil, err
			}
			f.Event = &e
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	var events []*record.Event
	var memories []*record.Memory
	for _, f := range out {
		if f.Event != nil {
			events = append(events, f.Event)
		}
		if f.Memory != nil {
			memories = append(memories, f.Memory)
		}
	}
	return out, s.strip(ctx, access, nil, events, memories)
}

func (s *Store) Entities(ctx context.Context, access store.Access, ids []string) (map[string]record.Entity, error) {
	access, err := s.live(ctx, access)
	if err != nil {
		return nil, err
	}
	out := map[string]record.Entity{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `select id, body from entities where workspace_id = $1 and requires <@ $2 and id = any($3)`,
		access.Workspace, access.Read, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var body []byte
		if err := rows.Scan(&id, &body); err != nil {
			return nil, err
		}
		var e record.Entity
		if err := json.Unmarshal(body, &e); err != nil {
			return nil, err
		}
		out[id] = e
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	read := make(map[string]*record.Entity, len(out))
	all := make([]*record.Entity, 0, len(out))
	for id, e := range out {
		read[id] = &e
		all = append(all, &e)
	}
	if err := s.strip(ctx, access, all, nil, nil); err != nil {
		return nil, err
	}
	for id, e := range read {
		out[id] = *e
	}
	return out, nil
}

func (s *Store) Rehearse(ctx context.Context, access store.Access, memories []string, at time.Time) error {
	access, err := s.live(ctx, access)
	if err != nil {
		return err
	}
	if len(memories) == 0 {
		return nil
	}
	_, err = s.pool.Exec(ctx, `
		insert into rehearsals (workspace_id, memory_id, principal, at)
		select distinct m.workspace_id, m.id, $3, $4::timestamptz from memories m
		where m.workspace_id = $1 and m.requires <@ $2 and m.id = any($5) and m.superseded_at is null`,
		access.Workspace, access.Read, access.Principal, at, memories)
	return err
}
