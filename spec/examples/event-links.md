# Event links

Page anchors and omit common subjects:

```json
{"from":"2026-10-21T00:00:00Z","to":"2026-10-24T00:00:00Z","anchors_limit":200,"after":"ev_previous","common_subject_share":0.2,"approaches":["thread","subject","meaning"],"meaning_floor":0.6}
```

Only events that appear in returned links are included. A candidate budget cut names the approach:

```json
{"events":[{"id":"ev_auth_a","bucket":"bk_shop","type":"observation"},{"id":"ev_auth_b","bucket":"bk_shop","type":"observation"}],"entities":[],"links":[{"from":"ev_auth_a","to":"ev_auth_b","score":1,"because":{"explicit":{"score":1,"links":[{"id":"ln_related","from":"ev_auth_b","to":"ev_auth_a","type":"related"}]},"meaning":{"score":0.5,"cosine":0.8,"model":"gemini-embedding-001"}}}],"next":"ev_last_anchor","truncated":["subject"]}
```

Recall continues when the provider is disabled or unavailable:

```json
{"results":[],"skipped":["meaning"]}
```

Subject filtering defaults to `common_subject_share: 0.2` and `common_subject_min: 50`. A file shared by 2 of 8 readable events contributes subject evidence. A subject shared by 60 of 100 readable events contributes none. Set `common_subject_min` to an integer of at least 1 to override the minimum.
