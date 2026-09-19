package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const subBindingSchema = `
CREATE TABLE IF NOT EXISTS console_configured_channels(
 source_id TEXT NOT NULL, provider TEXT NOT NULL, base TEXT NOT NULL, site TEXT NOT NULL,
 key_hashes JSONB NOT NULL, fingerprint TEXT NOT NULL, active BOOLEAN NOT NULL DEFAULT true,
 checked_at BIGINT NOT NULL, PRIMARY KEY(source_id,provider));
CREATE TABLE IF NOT EXISTS console_sub_key_scans(
 account_id TEXT PRIMARY KEY REFERENCES console_sub_accounts(id) ON DELETE CASCADE,
 revision TEXT NOT NULL DEFAULT '', lease_token TEXT NOT NULL DEFAULT '', lease_until TIMESTAMPTZ,
 checked_at BIGINT NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '', next_attempt TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS console_sub_key_index(
 account_id TEXT NOT NULL REFERENCES console_sub_accounts(id) ON DELETE CASCADE,
 key_hash TEXT NOT NULL, remote_key_id BIGINT NOT NULL, group_id BIGINT NOT NULL,
 key_created_at TIMESTAMPTZ, PRIMARY KEY(account_id,key_hash));`

// Retain a deployment path (multi-tenant origins may share a hostname) while
// removing known inference suffixes. Never send a credential to a guessed host.
func subBindingSite(base string) string {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	u.Host = strings.ToLower(u.Host)
	if (u.Scheme == "https" && u.Port() == "443") || (u.Scheme == "http" && u.Port() == "80") {
		u.Host = strings.ToLower(u.Hostname())
	}
	u.RawQuery = ""
	u.Fragment = ""
	u.RawPath = ""
	u.Path = strings.TrimRight(u.Path, "/")
	for _, suffix := range []string{"/v1/alpha/search", "/v1/chat/completions", "/v1/responses", "/v1/messages", "/v1beta/models", "/api/v1", "/v1beta", "/v1"} {
		if strings.HasSuffix(u.Path, suffix) {
			u.Path = strings.TrimSuffix(u.Path, suffix)
			break
		}
	}
	return strings.TrimRight(u.String(), "/")
}

func configuredKeyHashes(p configuredProvider) []string {
	seen := map[string]bool{}
	for _, key := range providerKeys(p.API) {
		if key != "" {
			seen[tokenHash(key)] = true
		}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
func (s *Service) saveConfiguredInventory(ctx context.Context, src controlSource, providers []configuredProvider) error {
	tx, err := s.control.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE console_configured_channels SET active=false WHERE source_id=$1`, src.ID); err != nil {
		return err
	}
	for _, p := range providers {
		site := subBindingSite(p.Base)
		if p.Provider == "" || site == "" {
			continue
		}
		hashes := configuredKeyHashes(p)
		if len(hashes) == 0 {
			continue
		}
		raw, _ := json.Marshal(hashes)
		fingerprint := tokenHash(controlTarget(src) + "\n" + p.Provider + "\n" + site + "\n" + string(raw))
		_, err = tx.ExecContext(ctx, `INSERT INTO console_configured_channels(source_id,provider,base,site,key_hashes,fingerprint,checked_at) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(source_id,provider) DO UPDATE SET base=excluded.base,site=excluded.site,key_hashes=excluded.key_hashes,fingerprint=excluded.fingerprint,active=true,checked_at=excluded.checked_at`, src.ID, p.Provider, site, site, string(raw), fingerprint, time.Now().Unix())
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

type subBindingAccount struct {
	ID, Owner, Base, Name, Email string
	Synced                       int64
}

func (s *Service) bindingAccounts(ctx context.Context) ([]subBindingAccount, error) {
	rows, err := s.control.db.QueryContext(ctx, `SELECT id,owner,base,name,email,synced_at FROM console_sub_accounts ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var accounts []subBindingAccount
	for rows.Next() {
		var a subBindingAccount
		if err = rows.Scan(&a.ID, &a.Owner, &a.Base, &a.Name, &a.Email, &a.Synced); err != nil {
			return nil, err
		}
		accounts = append(accounts, a)
	}
	return accounts, rows.Err()
}
func (s *Service) subBindingLoop(ctx context.Context) {
	for ctx.Err() == nil {
		s.refreshConfiguredBindings(ctx)
		if !waitStartup(ctx, time.Minute) {
			return
		}
	}
}
func (s *Service) refreshConfiguredBindings(ctx context.Context) {
	sources, err := s.control.listSources(ctx)
	if err != nil {
		return
	}
	// Configuration reads only. Existing gateway credentials and routes never change.
	for _, v := range sources {
		scanCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		src, e := s.control.source(scanCtx, v.ID)
		if e == nil {
			var providers []configuredProvider
			providers, e = configuredProviders(scanCtx, src)
			if e == nil {
				e = s.saveConfiguredInventory(scanCtx, src, providers)
			}
		}
		if e != nil && ctx.Err() == nil {
			fmt.Printf("sub2api binding_inventory source=%s status=unavailable\n", v.ID)
		}
		cancel()
	}
	accounts, err := s.bindingAccounts(ctx)
	if err != nil {
		return
	}
	var wg sync.WaitGroup
	slots := make(chan struct{}, 2)
	for _, account := range accounts {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return
		}
		wg.Add(1)
		go func() { defer wg.Done(); defer func() { <-slots }(); s.refreshAccountKeyIndex(ctx, account) }()
	}
	wg.Wait()
}

// A durable revision includes only this site's configured credentials and the
// account's explicit synchronization revision. Reloads, restarts and token
// refreshes do not enumerate the account's keys again.
func (s *Service) refreshAccountKeyIndex(parent context.Context, account subBindingAccount) {
	site := subBindingSite(account.Base)
	if site == "" {
		return
	}
	rows, err := s.control.db.QueryContext(parent, `SELECT DISTINCT value FROM console_configured_channels c JOIN console_sources s ON s.id=c.source_id CROSS JOIN LATERAL jsonb_array_elements_text(c.key_hashes) AS value WHERE c.active AND s.enabled AND c.site=$1 ORDER BY value`, site)
	if err != nil {
		return
	}
	hashes := []string{}
	for rows.Next() {
		var hash string
		if err = rows.Scan(&hash); err != nil {
			break
		}
		hashes = append(hashes, hash)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil || len(hashes) == 0 {
		return
	}
	revision := tokenHash(site + "\n" + account.ID + "\n" + strconv.FormatInt(account.Synced, 10) + "\n" + strings.Join(hashes, "\n"))
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()
	if _, err = s.control.db.ExecContext(ctx, `INSERT INTO console_sub_key_scans(account_id) VALUES($1) ON CONFLICT DO NOTHING`, account.ID); err != nil {
		return
	}
	lease := randomID()
	var id string
	err = s.control.db.QueryRowContext(ctx, `UPDATE console_sub_key_scans SET lease_token=$3,lease_until=now()+interval '90 seconds' WHERE account_id=$1 AND revision<>$2 AND next_attempt<=now() AND (lease_until IS NULL OR lease_until<now()) RETURNING account_id`, account.ID, revision, lease).Scan(&id)
	if err != nil {
		return
	}
	type indexedKey struct {
		Hash      string
		ID, Group int64
		Created   *time.Time
	}
	keys := map[string]indexedKey{}
	complete := false
	for page := 1; page <= 100; page++ {
		var listing struct {
			Items    []subRemoteKey `json:"items"`
			Pages    int            `json:"pages"`
			PageSize int            `json:"page_size"`
			Page     int            `json:"page"`
		}
		err = s.subUsageGET(ctx, account.ID, account.Base, "/api/v1/keys?page_size=100&page="+strconv.Itoa(page), &listing)
		if err != nil {
			break
		}
		if listing.Items == nil || (listing.Page != 0 && listing.Page != page) {
			err = errors.New("invalid key pagination")
			break
		}
		for _, key := range listing.Items {
			if key.ID <= 0 || key.Key == "" || strings.ContainsAny(key.Key, "*•…") {
				continue
			}
			hash := tokenHash(key.Key)
			if previous, ok := keys[hash]; ok && previous.ID != key.ID {
				err = errors.New("ambiguous key")
				break
			}
			keys[hash] = indexedKey{hash, key.ID, key.GroupID, key.CreatedAt}
		}
		if err != nil {
			break
		}
		size := 100
		if listing.PageSize > 0 {
			size = listing.PageSize
		}
		if len(listing.Items) == 0 || (listing.Pages > 0 && page >= listing.Pages) || (listing.Pages == 0 && len(listing.Items) < size) {
			complete = true
			break
		}
	}
	if err == nil && !complete {
		err = errors.New("incomplete key listing")
	}
	if err == nil {
		tx, e := s.control.db.BeginTx(ctx, nil)
		if e == nil {
			defer tx.Rollback()
			var valid bool
			e = tx.QueryRowContext(ctx, `SELECT lease_token=$2 AND lease_until>now() FROM console_sub_key_scans WHERE account_id=$1 FOR UPDATE`, account.ID, lease).Scan(&valid)
			if e == nil && !valid {
				e = context.Canceled
			}
			if e == nil {
				_, e = tx.ExecContext(ctx, `DELETE FROM console_sub_key_index WHERE account_id=$1`, account.ID)
			}
			for _, key := range keys {
				if e != nil {
					break
				}
				_, e = tx.ExecContext(ctx, `INSERT INTO console_sub_key_index(account_id,key_hash,remote_key_id,group_id,key_created_at) VALUES($1,$2,$3,$4,$5)`, account.ID, key.Hash, key.ID, key.Group, key.Created)
			}
			if e == nil {
				_, e = tx.ExecContext(ctx, `UPDATE console_sub_key_scans SET revision=$2,checked_at=$3,error='',lease_token='',lease_until=NULL WHERE account_id=$1`, account.ID, revision, time.Now().Unix())
			}
			if e == nil {
				e = tx.Commit()
			}
		}
		err = e
	}
	if err != nil {
		cleanup, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_, _ = s.control.db.ExecContext(cleanup, `UPDATE console_sub_key_scans SET error='站点密钥读取暂不可用',lease_token='',lease_until=NULL,next_attempt=now()+interval '5 minutes' WHERE account_id=$1 AND lease_token=$2`, account.ID, lease)
		if parent.Err() == nil {
			fmt.Printf("sub2api binding_scan account=%s status=unavailable\n", account.ID)
		}
	} else {
		fmt.Printf("sub2api binding_scan account=%s indexed_keys=%d status=complete\n", account.ID, len(keys))
	}
}

type subBoundKey struct {
	AccountID   string `json:"account_id"`
	AccountName string `json:"account_name"`
	Base        string `json:"base"`
	GroupID     int64  `json:"group_id"`
	RemoteKeyID int64  `json:"remote_key_id"`
}

func (s *Service) configuredBindings(ctx context.Context, owner string) ([]subInstalledChannel, error) {
	accounts, err := s.bindingAccounts(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[string]subBindingAccount{}
	bySite := map[string][]subBindingAccount{}
	for _, a := range accounts {
		if a.Owner == owner {
			byID[a.ID] = a
			bySite[subBindingSite(a.Base)] = append(bySite[subBindingSite(a.Base)], a)
		}
	}
	rows, err := s.control.db.QueryContext(ctx, `SELECT i.account_id,i.key_hash,i.remote_key_id,i.group_id FROM console_sub_key_index i JOIN console_sub_accounts a ON a.id=i.account_id WHERE a.owner=$1`, owner)
	if err != nil {
		return nil, err
	}
	index := map[string][]subBoundKey{}
	for rows.Next() {
		var account, hash string
		var key, group int64
		if err = rows.Scan(&account, &hash, &key, &group); err != nil {
			break
		}
		a := byID[account]
		lookup := subBindingSite(a.Base) + "\n" + hash
		index[lookup] = append(index[lookup], subBoundKey{account, a.Name, a.Base, group, key})
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = s.control.db.QueryContext(ctx, `SELECT c.source_id,s.name,c.provider,c.base,c.site,c.key_hashes,c.checked_at FROM console_configured_channels c JOIN console_sources s ON s.id=c.source_id WHERE c.active AND s.enabled ORDER BY c.source_id,c.provider`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []subInstalledChannel{}
	for rows.Next() {
		item := subInstalledChannel{Kind: "configured", Models: []string{}, Positions: map[string]int{}, BoundKeys: []subBoundKey{}}
		var site string
		var raw []byte
		if err = rows.Scan(&item.SourceID, &item.SourceName, &item.Provider, &item.Base, &site, &raw, &item.BindingCheckedAt); err != nil {
			return nil, err
		}
		var hashes []string
		if err = json.Unmarshal(raw, &hashes); err != nil {
			return nil, err
		}
		item.Name = item.Provider
		item.BindingStatus = "matched"
		for _, hash := range hashes {
			matches := index[site+"\n"+hash]
			if len(matches) != 1 {
				if len(matches) > 1 {
					item.BindingStatus = "ambiguous"
				} else if item.BindingStatus != "ambiguous" {
					item.BindingStatus = "unmatched"
				}
				continue
			}
			item.BoundKeys = append(item.BoundKeys, matches[0])
		}
		if len(item.BoundKeys) > 0 && item.BindingStatus == "unmatched" {
			item.BindingStatus = "partial"
		}
		if len(bySite[site]) == 0 {
			item.BindingStatus = "no_account"
		}
		// Reuse group-level enrichment only when all credentials refer to one group.
		if item.BindingStatus == "matched" && len(item.BoundKeys) > 0 {
			first := item.BoundKeys[0]
			one := true
			for _, key := range item.BoundKeys {
				if key.AccountID != first.AccountID || key.GroupID != first.GroupID {
					one = false
				}
			}
			if one {
				item.AccountID = first.AccountID
				item.GroupID = first.GroupID
			}
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
