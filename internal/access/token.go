package access

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/trailpad/memory/internal/store"
)

// Claims are what a person token must carry (SPEC §13.6).
type Claims struct {
	Issuer    string    `json:"iss"`
	Subject   string    `json:"sub"`
	Audience  audience  `json:"aud"`
	Workspace string    `json:"workspace"`
	Party     string    `json:"azp"`
	Expires   int64     `json:"exp"`
	Accounts  []Account `json:"accounts,omitempty"`
}

// audience accepts the JWT forms of aud: one string or a list.
type audience []string

func (a *audience) UnmarshalJSON(data []byte) error {
	var one string
	if err := json.Unmarshal(data, &one); err == nil {
		*a = audience{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return err
	}
	*a = many
	return nil
}

// skew allows for an issuer's clock running ahead of the engine's.
const skew = time.Minute

// verify checks a compact EdDSA JWT. Every failure is ErrUnauthenticated.
func (a *Authority) verify(ctx context.Context, service Service, token string) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, ErrUnauthenticated
	}
	header, err1 := base64.RawURLEncoding.DecodeString(parts[0])
	payload, err2 := base64.RawURLEncoding.DecodeString(parts[1])
	signature, err3 := base64.RawURLEncoding.DecodeString(parts[2])
	if err := errors.Join(err1, err2, err3); err != nil {
		return Claims{}, ErrUnauthenticated
	}
	var h struct {
		Alg string `json:"alg"`
	}
	if json.Unmarshal(header, &h) != nil || h.Alg != "EdDSA" {
		return Claims{}, ErrUnauthenticated
	}
	var c Claims
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if decoder.Decode(&c) != nil || c.Subject == "" {
		return Claims{}, ErrUnauthenticated
	}

	issuer, err := a.Directory.Issuer(ctx, service.Workspace, c.Issuer)
	if errors.Is(err, store.ErrNotFound) {
		return Claims{}, ErrUnauthenticated
	}
	if err != nil {
		return Claims{}, err
	}
	if len(issuer.PublicKey) != ed25519.PublicKeySize ||
		!ed25519.Verify(issuer.PublicKey, []byte(parts[0]+"."+parts[1]), signature) {
		return Claims{}, ErrUnauthenticated
	}
	// Without delegations, only the service the issuer is registered for may
	// act for its people.
	if c.Workspace != service.Workspace || c.Party != service.ID || issuer.Service != service.ID ||
		!slices.Contains(c.Audience, a.Audience) {
		return Claims{}, ErrUnauthenticated
	}
	ttl, err := a.Directory.TokenTTL(ctx, service.Workspace)
	if err != nil {
		return Claims{}, err
	}
	now := a.now()
	expires := time.Unix(c.Expires, 0)
	if !expires.After(now) || expires.After(now.Add(ttl+skew)) {
		return Claims{}, ErrUnauthenticated
	}
	// An issuer proves only the accounts of sources people sign in to it
	// with; any other account it names is left out.
	c.Accounts = slices.DeleteFunc(c.Accounts, func(acc Account) bool {
		return !slices.Contains(issuer.Attests, acc.System) || acc.Installation == "" || acc.Account == ""
	})
	return c, nil
}
