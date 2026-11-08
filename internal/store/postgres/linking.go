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
	visible := strings.Replace(q.visible(access, scope), "select e.id, 0 as version", "select e.*", 1)
	visible = strings.Replace(visible, "visible as (", "v as not materialized (", 1)
	global := scope
	global.OccurredFrom = nil
	global.OccurredTo = nil
	global.Types = nil
	graph := strings.Replace(q.visible(access, global), "select e.id, 0 as version", "select e.*", 1)
	graph = strings.Replace(graph, "visible as (", "graph as not materialized (", 1)
	replace := strings.NewReplacer("@visible", visible, "@graph", graph, "@ws", q.arg(access.Workspace), "@read", q.arg(access.Read), "@anchors", q.arg(anchors), "@enabled", q.arg(approaches), "@asof", q.arg(scope.AsOf), "@minimum", q.arg(scope.CommonSubjectMin), "@cutoff", q.arg(scope.CommonSubjectShare))
	header := `with recursive @visible,@graph,
 a as (select * from v where id=any(@anchors::text[])),
 anchor_subjects as (select distinct i.entity_id from a join involvements i on i.workspace_id=@ws and i.record_type='event' and i.record_id=a.id and i.via in ('subject','involves')
 join entities x on x.workspace_id=@ws and x.id=i.entity_id and x.requires <@ @read),
 frequencies as (select t.entity_id,count(distinct i.record_id)::int as n from anchor_subjects t
 join involvements i on i.workspace_id=@ws and i.entity_id=t.entity_id and i.record_type='event' and i.via in ('subject','involves')
 join graph g on g.id=i.record_id group by t.entity_id),
 total as (select count(*)::int as n from graph),
 walk(src,dst,path,depth) as (
 select id,id,array[id],0 from a where 'thread'=any(@enabled::text[])
 union all select w.src,p.id,w.path||p.id,w.depth+1 from walk w
 join graph current on current.id=w.dst
 cross join lateral unnest(array[current.part_of,current.in_reply_to]) parent(id)
 join graph p on p.id=parent.id where w.depth<16 and not p.id=any(w.path)),
 candidates as (`
	parts := []string{}
	add := func(approach, body, order string) {
		parts = append(parts, `select a.id as src,c.dst,'`+approach+`'::text as approach,c.shared,c.path,c.value,c.frequency,c.type,c.link_id,c.link_from,c.link_to
 from a cross join lateral (select candidate.* from (`+body+`) candidate(dst,shared,path,value,frequency,type,link_id,link_from,link_to)
 join v endpoint on endpoint.id=candidate.dst
 where '`+approach+`'=any(@enabled::text[]) order by `+order+`,candidate.dst,candidate.shared,candidate.path,candidate.link_id limit 201) c`)
	}
	nearest := `abs(extract(epoch from (endpoint.occurred_at-a.occurred_at))) nulls last`
	add("thread", `select w.dst,array[]::text[],w.path,1::float8,0::int,''::text,''::text,''::text,''::text from walk w where w.src=a.id and w.depth>0
 union select c.id,array[]::text[],array[a.id,c.id],1::float8,0,'','','','' from graph c where c.part_of=a.id or c.in_reply_to=a.id
 union select c.id,array[]::text[],array[a.id,p.id,c.id],1::float8,0,'','','','' from graph p
 join graph c on c.part_of=p.id or c.in_reply_to=p.id
 where (p.id=a.part_of or p.id=a.in_reply_to) and c.id<>a.id`, nearest)
	add("subject", `select distinct b.id,array[i.entity_id],array[]::text[],0::float8,f.n,''::text,''::text,''::text,''::text
 from involvements i join frequencies f on f.entity_id=i.entity_id
 join involvements u on u.workspace_id=@ws and u.entity_id=i.entity_id and u.via in ('subject','involves') and u.record_type='event' and u.record_id<>a.id
 join v b on b.id=u.record_id cross join total
 where i.workspace_id=@ws and i.record_id=a.id and i.record_type='event' and i.via in ('subject','involves') and (f.n<@minimum or f.n::float8/greatest(total.n,1)<=@cutoff)`, `candidate.frequency,`+nearest)
	add("people", `select distinct b.id,array[i.entity_id],array[]::text[],abs(extract(epoch from (b.occurred_at-a.occurred_at)))::float8,0::int,''::text,''::text,''::text,''::text
 from involvements i join entities x on x.workspace_id=@ws and x.id=i.entity_id and x.requires <@ @read
 join involvements u on u.workspace_id=@ws and u.entity_id=i.entity_id and u.via='participant' and u.record_type='event' and u.record_id<>a.id
 join v b on b.id=u.record_id
 where i.workspace_id=@ws and i.record_id=a.id and i.record_type='event' and i.via='participant' and a.occurred_at is not null and b.occurred_at is not null`, nearest)
	add("time", `select b.id,array[]::text[],array[]::text[],abs(extract(epoch from (b.occurred_at-a.occurred_at)))::float8,0::int,''::text,''::text,''::text,''::text
 from v b join entities p on p.workspace_id=@ws and p.id=a.place_id and p.requires <@ @read
 where b.id<>a.id and b.place_id=a.place_id and b.occurred_at between a.occurred_at-interval '24 hours' and a.occurred_at+interval '24 hours'`, nearest)
	add("text", `select b.id,array[]::text[],array[]::text[],least(ts_rank_cd(b.said,search.query,32),ts_rank_cd(a.said,
 nullif(replace(plainto_tsquery('english',array_to_string(tsvector_to_array(b.said),' '))::text,'&','|'),'')::tsquery,32))::float8,0::int,''::text,''::text,''::text,''::text
 from v b cross join lateral (select nullif(replace(plainto_tsquery('english',array_to_string(tsvector_to_array(a.said),' '))::text,'&','|'),'')::tsquery as query) search
 where b.id<>a.id and b.said @@ search.query`, `candidate.value desc,`+nearest)
	add("explicit", `select case when l.from_id=a.id then l.to_id else l.from_id end,array[]::text[],array[]::text[],1::float8,0::int,l.type,l.id,l.from_id,l.to_id
 from links l where l.workspace_id=@ws and l.requires <@ @read and (l.from_id=a.id or l.to_id=a.id)
 and (@asof::timestamptz is null or l.recorded_at<=@asof) and l.from_id<>l.to_id`, nearest)
	tail := `), ranked as (select c.*,row_number() over(partition by src,approach order by
 case when approach='subject' then frequency else 0 end,
 case when approach='text' then -value else 0 end,
 abs(extract(epoch from (b.occurred_at-a.occurred_at))) nulls last,dst,shared,path,link_id) as position,
 count(*) over(partition by src,approach) as total_candidates
 from candidates c join a on a.id=c.src join v b on b.id=c.dst)
 select src,dst,approach,shared,path,value,frequency,type,link_id,link_from,link_to,total.n,total_candidates>200 from ranked cross join total where position<=200`
	sql := replace.Replace(header + strings.Join(parts, " union all ") + tail)
	rows, err := s.pool.Query(ctx, sql, q.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.LinkCandidate{}
	for rows.Next() {
		var c store.LinkCandidate
		if err := rows.Scan(&c.From, &c.To, &c.Approach, &c.Shared, &c.Path, &c.Value, &c.Frequency, &c.Type, &c.LinkID, &c.LinkFrom, &c.LinkTo, &c.Total, &c.Truncated); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
