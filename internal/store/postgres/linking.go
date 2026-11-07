package postgres

import (
	"context"
	"strings"

	"github.com/trailpad/memory/internal/store"
)

func (s *Store) LinkCandidates(ctx context.Context, access store.Access, scope store.Scope, anchors, approaches []string) ([]store.LinkCandidate, error) {
	access, err := s.live(ctx, access)
	if err != nil {
		return nil, err
	}
	if len(anchors) == 0 {
		return []store.LinkCandidate{}, nil
	}
	q := &query{}
	visible := q.visible(access, scope)
	graphScope := scope
	graphScope.OccurredFrom, graphScope.OccurredTo = nil, nil
	graphVisible := q.visible(access, graphScope)
	graphVisible = strings.Replace(graphVisible, "visible as", "graph_visible as", 1)
	ws := q.arg(access.Workspace)
	read := q.arg(access.Read)
	ids := q.arg(anchors)
	enabled := q.arg(approaches)
	asOf := q.arg(scope.AsOf)
	sql := `with recursive ` + visible + `,` + graphVisible + `,
 graph as (select e.* from graph_visible g join events e on e.workspace_id = ` + ws + ` and e.id=g.id),
 v as (select e.* from visible join events e on e.workspace_id = ` + ws + ` and e.id = visible.id),
 a as (select * from v where id = any(` + ids + `::text[])),
 ties as (
 select distinct i.record_id, i.entity_id, i.via from involvements i
 join v on v.id = i.record_id
 join entities x on x.workspace_id = i.workspace_id and x.id = i.entity_id and x.requires <@ ` + read + `
 where i.workspace_id = ` + ws + ` and i.record_type = 'event'),
 freq as (select entity_id, count(distinct record_id)::int as n from ties where via in ('subject','involves') group by entity_id),
 edges as (
 select e.id as src, p.id as dst from graph e
 cross join lateral unnest(array[e.part_of,e.in_reply_to]) parent(id)
 join graph p on p.id = parent.id
 union select p.id, e.id from graph e
 cross join lateral unnest(array[e.part_of,e.in_reply_to]) parent(id)
 join graph p on p.id = parent.id),
 walk(src,dst,depth) as (
 select a.id,a.id,0 from a where 'thread' = any(` + enabled + `::text[])
 union select w.src,e.dst,w.depth+1 from walk w join edges e on e.src=w.dst where w.depth<16),
 distances as (select src,dst,min(depth) as depth from walk group by src,dst),
 predecessors as (
 select distinct on (d.src,d.dst) d.src,d.dst,p.dst as parent
 from distances d join edges e on e.dst=d.dst
 join distances p on p.src=d.src and p.dst=e.src and p.depth=d.depth-1
 order by d.src,d.dst,p.dst),
 paths(src,dst,path) as (
 select a.id,a.id,array[a.id] from a where 'thread' = any(` + enabled + `::text[])
 union all select p.src,e.dst,p.path || e.dst from paths p
 join predecessors e on e.src=p.src and e.parent=p.dst),
 candidates as (
 select src,dst,'thread'::text as approach,array[]::text[] as shared,path,
 1::float8 as value,0::int as frequency,''::text as type from paths join v on v.id=paths.dst where src <> dst
 ),
 other as (
 select a.id as src,b.id as dst,'subject'::text as approach,array[t.entity_id] as shared,array[]::text[] as path,
 0::float8 as value,f.n as frequency,''::text as type
 from a join ties t on t.record_id = a.id and t.via in ('subject','involves')
 join freq f on f.entity_id = t.entity_id
 cross join lateral (
 select distinct u.record_id as id from ties u where u.entity_id = t.entity_id and u.via in ('subject','involves') and u.record_id <> a.id
 order by u.record_id limit 200
 ) b where 'subject' = any(` + enabled + `::text[])
 union all
 select a.id,b.id,'people',array[t.entity_id],array[]::text[],
 abs(extract(epoch from (b.occurred_at-a.occurred_at)))::float8,0,''
 from a join ties t on t.record_id=a.id and t.via='participant'
 cross join lateral (select v.* from ties u join v on v.id=u.record_id
 where u.entity_id=t.entity_id and u.via='participant' and v.id<>a.id
 order by abs(extract(epoch from (v.occurred_at-a.occurred_at))) nulls last,v.id limit 200) b
 where 'people' = any(` + enabled + `::text[]) and a.occurred_at is not null and b.occurred_at is not null
 union all
 select a.id,b.id,'time',array[]::text[],array[]::text[],abs(extract(epoch from (b.occurred_at-a.occurred_at)))::float8,0,''
 from a join entities place on place.workspace_id = ` + ws + ` and place.id=a.place_id and place.requires <@ ` + read + `
 cross join lateral (select * from v b where b.id<>a.id and b.place_id=a.place_id
 and b.occurred_at between a.occurred_at-interval '24 hours' and a.occurred_at+interval '24 hours'
 order by abs(extract(epoch from (b.occurred_at-a.occurred_at))),b.id limit 200) b
 where 'time' = any(` + enabled + `::text[])
 union all
 select a.id,b.id,'text',array[]::text[],array[]::text[],b.rank,0,''
 from a cross join lateral (select nullif(replace(plainto_tsquery('english',array_to_string(tsvector_to_array(a.said),' '))::text,'&','|'),'')::tsquery as query) search
 cross join lateral (select b.id,ts_rank_cd(b.said,search.query,32)::float8 as rank from events b join visible vb on vb.id=b.id
 where b.workspace_id = ` + ws + ` and b.id<>a.id and b.said @@ search.query order by rank desc,b.id limit 100) b
 where 'text' = any(` + enabled + `::text[])
 union all
 select a.id,b.id,'explicit',array[]::text[],array[]::text[],1::float8,0,l.type
 from a join links l on l.workspace_id=` + ws + ` and l.requires <@ ` + read + `
 and (l.from_id=a.id or l.to_id=a.id) and (` + asOf + `::timestamptz is null or l.recorded_at <= ` + asOf + `)
 join v b on b.id=case when l.from_id=a.id then l.to_id else l.from_id end and b.id<>a.id
 where 'explicit' = any(` + enabled + `::text[])
 )
 select *, (select count(*)::int from v) from candidates
 union all select *, (select count(*)::int from v) from other`
	rows, err := s.pool.Query(ctx, sql, q.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.LinkCandidate{}
	for rows.Next() {
		var c store.LinkCandidate
		if err := rows.Scan(&c.From, &c.To, &c.Approach, &c.Shared, &c.Path, &c.Value, &c.Frequency, &c.Type, &c.Total); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
