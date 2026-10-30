package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
)

// Keep only a server-produced representation tag and its existing current
// proof. These records cannot deliver a body and never authorize a selection.
// They survive large page eviction, but never process restart or deployment.
// The client must supply the exact tag/encoding of a complete prior response.
type managementPageValidator struct {
	key, owner, tag, encoding, revision string
	proof                               managementFreshProof
	bytes                               int
}

type managementPageValidators struct {
	mu      sync.Mutex
	entries []managementPageValidator
	bytes   int
}

const managementValidatorBudget = 256 << 10
const managementValidatorEntries = 32

func (c *managementPageValidators) get(key, encoding, condition string) (managementPageValidator, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.entries) - 1; i >= 0; i-- {
		entry := c.entries[i]
		if entry.key == key && entry.encoding == encoding && managementETagMatches(condition, entry.tag) {
			return entry, true
		}
	}
	return managementPageValidator{}, false
}

func (c *managementPageValidators) put(entry managementPageValidator) {
	if entry.bytes > managementValidatorBudget {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	remove := func(i int) {
		c.bytes -= c.entries[i].bytes
		copy(c.entries[i:], c.entries[i+1:])
		c.entries[len(c.entries)-1] = managementPageValidator{}
		c.entries = c.entries[:len(c.entries)-1]
	}
	for i := len(c.entries) - 1; i >= 0; i-- {
		if old := c.entries[i]; old.key == entry.key && old.encoding == entry.encoding {
			remove(i)
		}
	}
	for len(c.entries) > 0 && (len(c.entries) >= managementValidatorEntries || c.bytes+entry.bytes > managementValidatorBudget) {
		remove(0)
	}
	c.entries = append(c.entries, entry)
	c.bytes += entry.bytes
}

func managementETagMatches(condition, tag string) bool {
	if tag == "" {
		return false
	}
	for _, candidate := range strings.Split(condition, ",") {
		if strings.TrimSpace(candidate) == tag {
			return true
		}
	}
	return false
}

func (s *Service) rememberManagementValidator(w http.ResponseWriter, r *http.Request, page managementPageResponse, key string, proof *managementFreshProof) {
	if proof == nil || proof.Stamp == "" || page.Owner == "" || r.Context().Err() != nil || r.Header.Get("Accept") != managementPageV3MediaType || r.Header.Get("X-Console-Current-Validation") != "1" || w.Header().Get("X-Console-Validated-Request-ID") == "" || w.Header().Get("ETag") == "" {
		return
	}
	entry := managementPageValidator{key: strings.Clone(key), owner: strings.Clone(page.Owner), tag: strings.Clone(w.Header().Get("ETag")), encoding: strings.Clone(r.Header.Get("Accept-Encoding")), revision: strings.Clone(page.Revision), proof: managementRetainedProof(*proof)}
	// The byte cap includes retained source facts, including credentials. They
	// remain private server memory, just as in the existing full-page proof.
	raw, err := json.Marshal(entry.proof)
	if err != nil {
		return
	}
	entry.bytes = len(raw) + len(entry.key) + len(entry.owner) + len(entry.tag) + len(entry.encoding) + len(entry.revision) + 256
	// Source is intentionally excluded from the proof's JSON. Account for it
	// explicitly; never let that privacy annotation bypass the retention cap.
	for _, fact := range entry.proof.Facts {
		source, err := json.Marshal(fact.Source)
		if err != nil {
			return
		}
		entry.bytes += len(source) + 256
	}
	s.pageValidators.put(entry)
}

func managementValidatorProofEqual(current, retained managementFreshProof) bool {
	if len(current.Facts) != len(retained.Facts) || mustJSON(managementRetainedProof(current)) != mustJSON(retained) {
		return false
	}
	// These authorization fields are deliberately json:"-" in gateway facts;
	// compare them explicitly instead of relying on JSON equality alone.
	for i, fact := range current.Facts {
		other := retained.Facts[i]
		if fact.AtomicSnapshot != other.AtomicSnapshot || managementSourceIdentity(fact.Source) != managementSourceIdentity(other.Source) {
			return false
		}
	}
	return true
}

func (s *Service) writeAndRememberManagementValidator(w http.ResponseWriter, r *http.Request, page managementPageResponse, key string) error {
	if page.currentProof != nil && managementValidatorRequest(r) {
		entry, exists := s.pageValidators.get(key, r.Header.Get("Accept-Encoding"), r.Header.Get("If-None-Match"))
		if exists && page.Owner == entry.owner && page.Revision == entry.revision && managementValidatorProofEqual(*page.currentProof, entry.proof) {
			if err := r.Context().Err(); err != nil {
				return err
			}
			log.Printf("management_validator_response source=retained_page request_id=%s", consoleRequestID(r.Context()))
			return writeManagementValidatedNotModified(w, r, entry.tag, page.ReadAtMS)
		}
	}
	if err := writeManagementPage(w, r, page); err != nil {
		return err
	}
	s.rememberManagementValidator(w, r, page, key, page.currentProof)
	return nil
}

func (s *Service) currentManagementNotModified(ctx context.Context, w http.ResponseWriter, r *http.Request, owner, key, revision string, rebase bool) (bool, error) {
	if !managementValidatorRequest(r) {
		return false, nil
	}
	entry, exists := s.pageValidators.get(key, r.Header.Get("Accept-Encoding"), r.Header.Get("If-None-Match"))
	if !exists || owner != entry.owner || !rebase && revision != "" && revision != entry.revision {
		return false, nil
	}
	proof, now, valid, err := s.currentManagementProof(ctx, owner, entry.proof)
	if err != nil || !valid || proof.HeadersRevision != entry.proof.HeadersRevision {
		return false, err
	}
	if err = ctx.Err(); err != nil {
		return false, err
	}
	log.Printf("management_validator_response source=evicted_page request_id=%s", consoleRequestID(r.Context()))
	return true, writeManagementValidatedNotModified(w, r, entry.tag, now.UnixMilli())
}

func managementValidatorRequest(r *http.Request) bool {
	return r.Header.Get("Accept") == managementPageV3MediaType && r.Header.Get("X-Console-Current-Validation") == "1" && r.Header.Get("If-None-Match") != "" && consoleRequestID(r.Context()) != ""
}

func writeManagementValidatedNotModified(w http.ResponseWriter, r *http.Request, tag string, at int64) error {
	w.Header().Add("Vary", "Accept")
	w.Header().Add("Vary", "Accept-Encoding")
	w.Header().Add("Vary", "X-Console-Current-Validation")
	w.Header().Add("Vary", "Cookie")
	w.Header().Set("Cache-Control", "private, no-cache, must-revalidate")
	w.Header().Set("Content-Type", managementPageV3MediaType)
	w.Header().Set("ETag", tag)
	managementColumnReadHeaders(w, r, at)
	w.WriteHeader(http.StatusNotModified)
	return managementFlushNotModified(w, r)
}
