package cloudflare

import (
	"context"
	"errors"
	"sort"
)

// AccountScope is what one token can do in one account.
type AccountScope struct {
	Account Account `json:"account"`
	Zones   []Zone  `json:"zones"`
	// Tunnels is true when the token can list the account's tunnels.
	Tunnels bool `json:"tunnels"`
	// DNS is true when the token can read DNS records in its zones.
	DNS bool `json:"dns"`
	// Problems explains missing permissions in plain words.
	Problems []string `json:"problems,omitempty"`
}

// Scope is everything a token was found to reach.
type Scope struct {
	Token    TokenStatus    `json:"token"`
	Accounts []AccountScope `json:"accounts"`
}

// Discover verifies the token and maps the accounts, zones and tunnel access
// it has. accountHint is required for account-owned tokens, which cannot
// list accounts or verify at the user endpoint.
func Discover(ctx context.Context, c *Client, accountHint string) (Scope, error) {
	var sc Scope
	st, err := c.VerifyToken(ctx, accountHint)
	if err != nil {
		return sc, err
	}
	sc.Token = st
	if st.Status != "" && st.Status != "active" {
		return sc, &APIError{Message: "the API token is " + st.Status, class: ErrAuth}
	}
	accounts, err := c.Accounts(ctx)
	if err != nil && !errors.Is(err, ErrPermission) {
		return sc, err
	}
	if accountHint != "" {
		found := false
		for _, a := range accounts {
			if a.ID == accountHint {
				found = true
			}
		}
		if !found {
			a, aerr := c.Account(ctx, accountHint)
			if aerr != nil {
				a = Account{ID: accountHint, Name: accountHint}
			}
			accounts = append(accounts, a)
		}
	}
	if len(accounts) == 0 {
		return sc, &APIError{Message: "the token cannot see any account; give it Account Settings Read, or enter the account ID", class: ErrPermission}
	}
	for _, a := range accounts {
		as := AccountScope{Account: a}
		if _, terr := c.Tunnels(ctx, a.ID); terr == nil {
			as.Tunnels = true
		} else if errors.Is(terr, ErrRateLimited) {
			return sc, terr
		} else {
			as.Problems = append(as.Problems, "cannot read tunnels (needs Cloudflare Tunnel Read or Edit)")
		}
		zones, zerr := c.Zones(ctx, a.ID)
		switch {
		case zerr == nil:
			as.Zones = zones
		case errors.Is(zerr, ErrRateLimited):
			return sc, zerr
		default:
			as.Problems = append(as.Problems, "cannot list zones (needs Zone Read)")
		}
		if len(as.Zones) > 0 {
			if _, derr := c.DNSRecords(ctx, as.Zones[0].ID, "ndl-permission-probe."+as.Zones[0].Name); derr == nil {
				as.DNS = true
			} else if errors.Is(derr, ErrRateLimited) {
				return sc, derr
			} else {
				as.Problems = append(as.Problems, "cannot read DNS records (needs DNS Read or Edit)")
			}
		}
		sort.Slice(as.Zones, func(i, j int) bool { return as.Zones[i].Name < as.Zones[j].Name })
		sc.Accounts = append(sc.Accounts, as)
	}
	sort.Slice(sc.Accounts, func(i, j int) bool { return sc.Accounts[i].Account.Name < sc.Accounts[j].Account.Name })
	return sc, nil
}
