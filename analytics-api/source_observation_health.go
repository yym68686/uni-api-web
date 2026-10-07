package main

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"
)

const sourceObservationCooldown = 5 * time.Minute

// Preserve the public error message without discarding the transport cause.
// HTTP/authentication/JSON errors are not evidence that a source is offline.
type sourceTransportError struct {
	message string
	cause   error
}

func (e *sourceTransportError) Error() string { return e.message }
func (e *sourceTransportError) Unwrap() error { return e.cause }

func sourceTransportFailed(err error) bool {
	var transport *sourceTransportError
	return errors.As(err, &transport) && !errors.Is(err, context.Canceled)
}

type sourceObservationState struct {
	base    string
	retryAt time.Time
	probing bool
}

// This is transient runtime health, independent of console_sources.enabled.
// It only gates observation fanout; configuration and explicit operations
// continue to use their existing live validation paths.
type sourceObservationCircuit struct {
	mu      sync.Mutex
	entries map[string]*sourceObservationState
}

func (c *sourceObservationCircuit) failed(src controlSource, err error, now time.Time) {
	if !sourceTransportFailed(err) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]*sourceObservationState)
	}
	if state := c.entries[src.ID]; state != nil && state.base == src.Base {
		return
	}
	c.entries[src.ID] = &sourceObservationState{base: src.Base, retryAt: now.Add(sourceObservationCooldown)}
	log.Printf("source_observation source=%q status=unavailable reason=transport retry_after_seconds=%d", src.ID, int(sourceObservationCooldown.Seconds()))
}

// A known unavailable source never blocks a refresh, including when its
// cooldown expires. Only one background read probes recovery at a time.
func (c *sourceObservationCircuit) skip(src controlSource, now time.Time, probe func() error) bool {
	c.mu.Lock()
	state := c.entries[src.ID]
	if state == nil || state.base != src.Base {
		delete(c.entries, src.ID)
		c.mu.Unlock()
		return false
	}
	if now.Before(state.retryAt) || state.probing {
		c.mu.Unlock()
		return true
	}
	state.probing = true
	c.mu.Unlock()
	go func() {
		err := probe()
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.entries[src.ID] != state {
			return // The source was reconfigured or removed while probing.
		}
		if sourceTransportFailed(err) {
			state.probing = false
			state.retryAt = time.Now().Add(sourceObservationCooldown)
			log.Printf("source_observation source=%q status=unavailable reason=probe_transport retry_after_seconds=%d", src.ID, int(sourceObservationCooldown.Seconds()))
			return
		}
		// An HTTP or application error proves reachability too; leave those
		// errors visible on the normal read path instead of hiding them.
		delete(c.entries, src.ID)
		log.Printf("source_observation source=%q status=reachable", src.ID)
	}()
	return true
}

func (c *sourceObservationCircuit) reset(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, id)
}

func (c *sourceObservationCircuit) unavailable(src sourceView) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	state := c.entries[src.ID]
	return state != nil && state.base == src.Base
}

func (s *Service) skipUnavailableObservation(src controlSource) bool {
	return s.observationHealth.skip(src, time.Now(), func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 18*time.Second)
		defer cancel()
		_, _, err := fetchSource(ctx, src, "/v1/api-keys", nil)
		return err
	})
}

func (s *Service) observeSourceFailure(ctx context.Context, src controlSource, err error) {
	if ctx.Err() == nil {
		s.observationHealth.failed(src, err, time.Now())
	}
}
