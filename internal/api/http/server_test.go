package httpapi_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trailpad/memory/internal/access"
	httpapi "github.com/trailpad/memory/internal/api/http"
	"github.com/trailpad/memory/internal/capture"
	"github.com/trailpad/memory/internal/profile"
	"github.com/trailpad/memory/internal/recall"
	"github.com/trailpad/memory/internal/store"
	"github.com/trailpad/memory/internal/store/postgres"
	"github.com/trailpad/memory/internal/store/postgres/pgtest"
)

const audience = "trailpad-memory"

var githubApp = access.Installation{System: "github", Installation: "gh_inst_1", Accounts: "github.com"}

// engine is a running engine with one workspace, a SourceAnt service that is
// also the issuer for its people, and a GitHub installation it connects.
type engine struct {
	t       *testing.T
	store   *postgres.Store
	server  *httptest.Server
	now     time.Time
	key     string
	signing ed25519.PrivateKey
}

func newEngine(t *testing.T) *engine {
	t.Helper()
	s := pgtest.ForTest(t)
	e := &engine{t: t, store: s, now: time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)}
	clock := func() time.Time { return e.now }
	profiles := profile.NewRegistry()
	api := &httpapi.Server{
		Auth:       &access.Authority{Directory: s, Audience: audience, Now: clock},
		Capture:    &capture.Capture{Store: s, Profiles: profiles},
		Recall:     &recall.Service{Store: s, Profiles: profiles, Now: clock},
		Store:      s,
		Connectors: s,
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:        clock,
	}
	e.server = httptest.NewServer(api.Handler())
	t.Cleanup(e.server.Close)

	ctx := context.Background()
	if err := s.CreateWorkspace(ctx, store.Workspace{ID: "ws_acme"}); err != nil {
		t.Fatal(err)
	}
	e.key = e.service("ws_acme", "sourceant", []string{"read", "write", "sync"}, githubApp)
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	e.signing = private
	if err := s.AddIssuer(ctx, "ws_acme", access.Issuer{Issuer: "https://sourceant.test", Service: "sourceant", PublicKey: public, Attests: []string{"github"}}); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *engine) service(workspace, id string, actions []string, installations ...access.Installation) string {
	e.t.Helper()
	key, hash := access.NewKey()
	if err := e.store.CreateService(context.Background(), access.Service{Workspace: workspace, ID: id, Actions: actions, Installations: installations}, hash); err != nil {
		e.t.Fatal(err)
	}
	return key
}

// claims are a valid person token's, which a test changes to break one.
func (e *engine) claims(sub string, accounts ...string) map[string]any {
	var linked []map[string]string
	for _, a := range accounts {
		linked = append(linked, map[string]string{"system": "github", "installation": "github.com", "account": a})
	}
	return map[string]any{
		"iss": "https://sourceant.test", "sub": sub, "aud": audience, "workspace": "ws_acme", "azp": "sourceant",
		"exp": e.now.Add(10 * time.Minute).Unix(), "accounts": linked,
	}
}

func (e *engine) sign(claims map[string]any) string {
	return signWith(e.signing, `{"alg":"EdDSA","typ":"JWT"}`, claims)
}

func signWith(key ed25519.PrivateKey, header string, claims map[string]any) string {
	payload, _ := json.Marshal(claims)
	unsigned := base64.RawURLEncoding.EncodeToString([]byte(header)) + "." + base64.RawURLEncoding.EncodeToString(payload)
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, []byte(unsigned)))
}

// call makes a request with a service key and, when given, a person token.
func (e *engine) call(method, path, key, token string, body any) (int, []byte) {
	e.t.Helper()
	var reader io.Reader
	if body != nil {
		b, ok := body.(string)
		if !ok {
			raw, _ := json.Marshal(body)
			b = string(raw)
		}
		reader = strings.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.server.URL+path, reader)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if token != "" {
		req.Header.Set(httpapi.PersonHeader, token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	return res.StatusCode, out
}

func (e *engine) must(want int, method, path, key, token string, body any) []byte {
	e.t.Helper()
	status, out := e.call(method, path, key, token, body)
	if status != want {
		e.t.Fatalf("%s %s: want %d, got %d: %s", method, path, want, status, out)
	}
	return out
}

// repository binds a bucket to a repository, writes a review comment in it
// and returns the bucket's id.
func (e *engine) repository(container, event, text string) string {
	e.t.Helper()
	out := e.must(200, "PUT", "/v1/buckets", e.key, "", map[string]any{
		"binding": map[string]string{"system": "github", "installation": "gh_inst_1", "container": container},
	})
	var bound struct{ ID string }
	_ = json.Unmarshal(out, &bound)
	if !strings.HasPrefix(bound.ID, "bk_") {
		e.t.Fatalf("bound bucket has id %q", bound.ID)
	}
	bucket := bound.ID
	e.must(200, "POST", "/v1/records", e.key, "", `{
	  "entities": [{"bucket": "`+bucket+`", "id": "pl_`+container+`", "class": "place", "type": "github.repository", "name": "`+container+`"},
	               {"bucket": "`+bucket+`", "id": "ac_`+event+`", "class": "actor", "type": "person", "name": "Ana"}],
	  "events": [{"bucket": "`+bucket+`", "id": "`+event+`", "type": "github.pull_request.review_comment",
	              "occurred_at": "2026-10-20T10:00:00Z", "place": "pl_`+container+`",
	              "participants": [{"actor": "ac_`+event+`", "role": "author"}],
	              "content": [{"speaker": "ac_`+event+`", "text": "`+text+`"}]}]
	}`)
	return bucket
}

func (e *engine) readers(bucket string, accounts ...string) {
	e.t.Helper()
	var readers []access.Account
	for _, a := range accounts {
		readers = append(readers, access.Account{System: "github", Installation: "github.com", Account: a})
	}
	e.must(204, "PUT", "/v1/buckets/"+bucket+"/readers", e.key, "", map[string]any{"readers": readers})
}

func (e *engine) recalled(key, token, text string) []string {
	e.t.Helper()
	out := e.must(200, "POST", "/v1/recall", key, token, map[string]any{"context": map[string]string{"text": text}})
	var res recall.Response
	if err := json.Unmarshal(out, &res); err != nil {
		e.t.Fatal(err)
	}
	var ids []string
	for _, r := range res.Results {
		ids = append(ids, r.ID)
	}
	slices.Sort(ids)
	return ids
}

func TestARequestWithoutAProvenServiceIsRefused(t *testing.T) {
	e := newEngine(t)
	for _, key := range []string{"", "mk_not_a_key", e.key + "x"} {
		if status, _ := e.call("POST", "/v1/recall", key, "", `{"context": {"text": "retry"}}`); status != 401 {
			t.Fatalf("key %q: want 401, got %d", key, status)
		}
	}
	if status, _ := e.call("GET", "/healthz", "", "", nil); status != 204 {
		t.Fatalf("health needs no key, got %d", status)
	}
}

func TestAServiceActsAsItselfWithTheBucketsItBound(t *testing.T) {
	e := newEngine(t)
	e.repository("repo_shop", "ev_retry", "Retries stop after three attempts.")

	e.must(200, "GET", "/v1/events/ev_retry", e.key, "", nil)
	if got := e.recalled(e.key, "", "retries attempts"); !slices.Contains(got, "ev_retry") {
		t.Fatalf("the service recalls what it wrote, got %v", got)
	}
	other := e.service("ws_acme", "assistant", []string{"read"})
	e.must(404, "GET", "/v1/events/ev_retry", other, "", nil)
	if got := e.recalled(other, "", "retries attempts"); len(got) != 0 {
		t.Fatalf("a service with no grants recalls nothing, got %v", got)
	}
}

func TestAPersonReadsWhatTheirLinkedAccountMaySeeAtTheSource(t *testing.T) {
	e := newEngine(t)
	shop := e.repository("repo_shop", "ev_retry", "Retries stop after three attempts.")
	payroll := e.repository("repo_payroll", "ev_salary", "Retries of salary runs need approval.")
	e.readers(shop, "gh_ana", "gh_ben")
	e.readers(payroll, "gh_hana")

	ana := e.sign(e.claims("user_ana", "gh_ana"))
	e.must(200, "GET", "/v1/events/ev_retry", e.key, ana, nil)
	e.must(404, "GET", "/v1/events/ev_salary", e.key, ana, nil)
	if got := e.recalled(e.key, ana, "retries"); !slices.Equal(got, []string{"ev_retry"}) {
		t.Fatalf("Ana recalls only the shop repository, got %v", got)
	}

	hana := e.sign(e.claims("user_hana", "gh_hana"))
	if got := e.recalled(e.key, hana, "retries"); !slices.Equal(got, []string{"ev_salary"}) {
		t.Fatalf("Hana recalls only payroll, got %v", got)
	}

	nobody := e.sign(e.claims("user_zed"))
	if got := e.recalled(e.key, nobody, "retries"); len(got) != 0 {
		t.Fatalf("a person with no linked account reads nothing, got %v", got)
	}
}

func TestASyncReplacesReadersFromTheNextRequest(t *testing.T) {
	e := newEngine(t)
	shop := e.repository("repo_shop", "ev_retry", "Retries stop after three attempts.")
	e.readers(shop, "gh_ana")
	ana := e.sign(e.claims("user_ana", "gh_ana"))
	e.must(200, "GET", "/v1/events/ev_retry", e.key, ana, nil)

	e.readers(shop, "gh_ben")
	e.must(404, "GET", "/v1/events/ev_retry", e.key, ana, nil)
}

func TestSyncedReadersLapseWithoutAFreshSync(t *testing.T) {
	e := newEngine(t)
	shop := e.repository("repo_shop", "ev_retry", "Retries stop after three attempts.")
	e.readers(shop, "gh_ana")
	e.now = e.now.Add(25 * time.Hour)
	ana := e.sign(e.claims("user_ana", "gh_ana"))
	e.must(404, "GET", "/v1/events/ev_retry", e.key, ana, nil)

	e.readers(shop, "gh_ana")
	e.must(200, "GET", "/v1/events/ev_retry", e.key, ana, nil)
}

func TestAnAccountLinksToOnePerson(t *testing.T) {
	e := newEngine(t)
	shop := e.repository("repo_shop", "ev_retry", "Retries stop after three attempts.")
	e.readers(shop, "gh_ana")
	e.must(200, "GET", "/v1/events/ev_retry", e.key, e.sign(e.claims("user_ana", "gh_ana")), nil)

	mallory := e.sign(e.claims("user_mallory", "gh_ana"))
	e.must(404, "GET", "/v1/events/ev_retry", e.key, mallory, nil)
}

func TestARepeatSignInRecordsNothingNew(t *testing.T) {
	e := newEngine(t)
	shop := e.repository("repo_shop", "ev_retry", "Retries stop after three attempts.")
	e.readers(shop, "gh_ana")
	count := func() (log, principals int) {
		t.Helper()
		if err := e.store.Pool().QueryRow(context.Background(),
			`select (select count(*) from access_log), (select count(*) from principals)`).Scan(&log, &principals); err != nil {
			t.Fatal(err)
		}
		return log, principals
	}
	ana := e.sign(e.claims("user_ana", "gh_ana"))
	e.must(200, "GET", "/v1/events/ev_retry", e.key, ana, nil)
	log, principals := count()

	// A lock that lets links be read but not written: a sign-in with nothing
	// new to record must not wait on it.
	ctx := context.Background()
	lock, err := e.store.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock.Exec(ctx, `lock table account_links in share mode`); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", e.server.URL+"/v1/events/ev_retry", nil)
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set(httpapi.PersonHeader, ana)
	res, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	_ = lock.Rollback(ctx)
	if err != nil {
		t.Fatalf("a repeat sign-in waited to write: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("want 200, got %d", res.StatusCode)
	}
	if l, p := count(); l != log || p != principals {
		t.Fatalf("signing in again changes nothing, got %d log rows and %d principals, want %d and %d", l, p, log, principals)
	}

	both := e.sign(e.claims("user_ana", "gh_ana", "gh_ana_2"))
	e.must(200, "GET", "/v1/events/ev_retry", e.key, both, nil)
	var links int
	if err := e.store.Pool().QueryRow(context.Background(), `select count(*) from account_links`).Scan(&links); err != nil {
		t.Fatal(err)
	}
	if links != 2 {
		t.Fatalf("a newly proven account is still linked, got %d links", links)
	}
}

func TestAnIssuerProvesOnlyTheSourcesItSignsPeopleInWith(t *testing.T) {
	e := newEngine(t)
	shop := e.repository("repo_shop", "ev_retry", "Retries stop after three attempts.")
	if err := e.store.Grant(context.Background(), "ws_acme", shop, "sourceant", []string{"read", "write"}, "operator"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.BindBucket(context.Background(), access.Service{Workspace: "ws_acme", ID: "sourceant", Installations: []access.Installation{{System: "slack", Installation: "slack_T1", Accounts: "slack_T1"}}},
		store.Bucket{ID: "bk_chan", Binding: &store.Binding{System: "slack", Installation: "slack_T1", Container: "C1"}}); err != nil {
		t.Fatal(err)
	}
	claims := e.claims("user_ana")
	claims["accounts"] = []map[string]string{{"system": "slack", "installation": "slack_T1", "account": "U1"}}
	e.must(404, "GET", "/v1/events/ev_retry", e.key, e.sign(claims), nil)
	var linked int
	if err := e.store.Pool().QueryRow(context.Background(), `select count(*) from account_links`).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if linked != 0 {
		t.Fatalf("a Slack account named by an issuer that signs people in with GitHub is not linked, got %d links", linked)
	}
}

func TestAPersonTokenIsRefusedUnlessEveryClaimHolds(t *testing.T) {
	e := newEngine(t)
	_, stranger, _ := ed25519.GenerateKey(rand.Reader)
	breaks := map[string]string{
		"another issuer":         e.sign(with(e.claims("user_ana"), "iss", "https://evil.test")),
		"another audience":       e.sign(with(e.claims("user_ana"), "aud", "someone-else")),
		"another workspace":      e.sign(with(e.claims("user_ana"), "workspace", "ws_other")),
		"another service":        e.sign(with(e.claims("user_ana"), "azp", "assistant")),
		"expired":                e.sign(with(e.claims("user_ana"), "exp", e.now.Add(-time.Second).Unix())),
		"living too long":        e.sign(with(e.claims("user_ana"), "exp", e.now.Add(time.Hour).Unix())),
		"no subject":             e.sign(with(e.claims(""), "sub", "")),
		"signed by someone else": signWith(stranger, `{"alg":"EdDSA"}`, e.claims("user_ana")),
		"not signed":             strings.TrimSuffix(e.sign(e.claims("user_ana")), "x") + "x",
		"another algorithm":      signWith(e.signing, `{"alg":"none"}`, e.claims("user_ana")),
		"garbage":                "a.b.c",
	}
	for name, token := range breaks {
		if status, _ := e.call("POST", "/v1/recall", e.key, token, `{"context": {"text": "retry"}}`); status != 401 {
			t.Errorf("%s: want 401, got %d", name, status)
		}
	}
	e.must(200, "POST", "/v1/recall", e.key, e.sign(e.claims("user_ana")), `{"context": {"text": "retry"}}`)
}

func TestOnlyTheIssuersOwnServiceActsForItsPeople(t *testing.T) {
	e := newEngine(t)
	assistant := e.service("ws_acme", "assistant", []string{"read"})
	token := e.sign(with(e.claims("user_ana"), "azp", "assistant"))
	if status, _ := e.call("POST", "/v1/recall", assistant, token, `{"context": {"text": "retry"}}`); status != 401 {
		t.Fatalf("a service other than the issuer's acts for nobody without a delegation, got %d", status)
	}
}

func TestACredentialLimitsWhatAServiceMayDo(t *testing.T) {
	e := newEngine(t)
	reader := e.service("ws_acme", "reader", []string{"read"}, access.Installation{System: "github", Installation: "gh_inst_2", Accounts: "github.com"})
	e.must(403, "POST", "/v1/records", reader, "", `{}`)
	e.must(403, "PUT", "/v1/buckets", reader, "", map[string]any{
		"binding": map[string]string{"system": "github", "installation": "gh_inst_2", "container": "repo_x"},
	})
	e.must(403, "PUT", "/v1/buckets", e.key, "", map[string]any{
		"binding": map[string]string{"system": "github", "installation": "gh_inst_other", "container": "repo_x"},
	})
}

func TestAWriteOnlyCredentialCannotReachRecordsThroughReferences(t *testing.T) {
	e := newEngine(t)
	shop := e.repository("repo_shop", "ev_retry", "Retries stop after three attempts.")
	writer := e.service("ws_acme", "writer", []string{"write"})
	if err := e.store.Grant(context.Background(), "ws_acme", shop, "writer", []string{"read", "write"}, "operator"); err != nil {
		t.Fatal(err)
	}
	e.must(422, "POST", "/v1/records", writer, "", `{"events": [{"bucket": "`+shop+`", "id": "ev_reply",
	  "type": "github.pull_request.review_comment", "occurred_at": "2026-10-21T10:00:00Z", "place": "pl_repo_shop",
	  "content": [{"text": "Agreed."}]}]}`)
}

func TestAConnectorReachesOnlyItsOwnInstallations(t *testing.T) {
	e := newEngine(t)
	e.must(403, "PUT", "/v1/buckets", e.key, "", map[string]any{
		"binding": map[string]string{"system": "slack", "installation": "gh_inst_1", "container": "C1"},
	})
	shop := e.repository("repo_shop", "ev_retry", "Retries stop after three attempts.")
	for _, reader := range []access.Account{
		{System: "github", Installation: "ghe.acme.test", Account: "gh_ana"},
		{System: "slack", Installation: "github.com", Account: "gh_ana"},
	} {
		e.must(403, "PUT", "/v1/buckets/"+shop+"/readers", e.key, "", map[string]any{"readers": []access.Account{reader}})
	}

	slack := e.service("ws_acme", "slack", []string{"sync"}, access.Installation{System: "slack", Installation: "T1", Accounts: "T1"})
	out := e.must(200, "PUT", "/v1/buckets", slack, "", map[string]any{
		"binding": map[string]string{"system": "slack", "installation": "T1", "container": "C1"},
	})
	var channel struct{ ID string }
	_ = json.Unmarshal(out, &channel)
	e.must(404, "PUT", "/v1/buckets/"+channel.ID+"/readers", e.key, "", map[string]any{"readers": []access.Account{}})
	e.must(404, "PUT", "/v1/buckets/bk_missing/readers", e.key, "", map[string]any{"readers": []access.Account{}})
}

func TestAPersonWritesOnlyWhereGranted(t *testing.T) {
	e := newEngine(t)
	shop := e.repository("repo_shop", "ev_retry", "Retries stop after three attempts.")
	e.readers(shop, "gh_ana")
	ana := e.sign(e.claims("user_ana", "gh_ana"))
	e.must(403, "POST", "/v1/records", e.key, ana, `{"entities": [{"bucket": "`+shop+`", "id": "ac_x", "class": "actor", "type": "person", "name": "X"}]}`)
}

func TestWorkspacesNeverSeeEachOther(t *testing.T) {
	e := newEngine(t)
	shop := e.repository("repo_shop", "ev_retry", "Retries stop after three attempts.")
	if err := e.store.CreateWorkspace(context.Background(), store.Workspace{ID: "ws_other"}); err != nil {
		t.Fatal(err)
	}
	other := e.service("ws_other", "sourceant", []string{"read", "write", "sync"}, githubApp)
	e.must(404, "GET", "/v1/events/ev_retry", other, "", nil)
	e.must(404, "PUT", "/v1/buckets/"+shop+"/readers", other, "", map[string]any{"readers": []access.Account{}})
	if got := e.recalled(other, "", "retries"); len(got) != 0 {
		t.Fatalf("another workspace recalls nothing, got %v", got)
	}
}

func TestInvalidRecordsAreRefusedWithEveryProblem(t *testing.T) {
	e := newEngine(t)
	shop := e.repository("repo_shop", "ev_retry", "Retries stop after three attempts.")
	out := e.must(422, "POST", "/v1/records", e.key, "", `{"events": [{"bucket": "`+shop+`", "id": "ev_bad"}]}`)
	if !bytes.Contains(out, []byte(`"problems"`)) {
		t.Fatalf("a refused write names its problems, got %s", out)
	}
	e.must(422, "POST", "/v1/records", e.key, "", `not json`)
}

func with(claims map[string]any, key string, value any) map[string]any {
	claims[key] = value
	return claims
}
