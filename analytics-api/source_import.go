package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Sources have separate polling schedules: a large archive must not prevent
// another source from importing current traffic. Download concurrency stays
// globally bounded and DuckDB commits retain the engine's existing mutex.
func (s *Service) factSources(ctx context.Context) ([]*Service, error) {
	var sources []sourceView
	primaryEnabled := s.control == nil
	if s.control != nil {
		var err error
		sources, err = s.control.listSources(ctx)
		if err != nil {
			return nil, err
		}
		for _, v := range sources {
			if v.ID == s.cfg.SourceID {
				primaryEnabled = true
			}
		}
	}
	s.importMu.Lock()
	if s.factDownloadSlots == nil {
		s.factDownloadSlots = make(chan struct{}, 16)
	}
	slots := s.factDownloadSlots
	s.importMu.Unlock()
	var jobs []*Service
	if primaryEnabled && s.cfg.S3Bucket != "" {
		jobs = append(jobs, &Service{engine: s.engine, cfg: s.cfg, factClient: s.factClient, factDownloadSlots: slots})
	}
	for _, v := range sources {
		if v.ID == s.cfg.SourceID || !v.HasStorage {
			continue
		}
		src, err := s.control.source(ctx, v.ID)
		if err != nil {
			return nil, err
		}
		cfg := s.cfg
		cfg.SourceID, cfg.S3Endpoint, cfg.S3Bucket, cfg.S3Prefix = src.ID, src.Storage.Endpoint, src.Storage.Bucket, src.Storage.Prefix
		client, err := newObjectClient(ctx, src.Storage.Endpoint, src.Storage.AccessKey, src.Storage.SecretKey, 20*time.Second)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, &Service{engine: s.engine, cfg: cfg, factClient: client, factDownloadSlots: slots})
	}
	return jobs, nil
}

func (s *Service) runSourceImport(ctx context.Context, child *Service) error {
	child.scanStarted.Store(time.Now().UnixMilli())
	err := child.importSingleS3(ctx)
	if err != nil {
		if ctx.Err() == nil {
			child.importFailures.Add(1)
		}
		child.importError.Store(importErrorClass(err))
		fmt.Printf("analytics source_import source=%s class=%s pages=%d objects=%d duration_ms=%d remaining_objects=%d\n", child.cfg.SourceID, importErrorClass(err), child.listPages.Load(), child.listedObjects.Load(), time.Now().UnixMilli()-child.scanStarted.Load(), child.remaining.Load())
	} else {
		child.importError.Store("")
		child.lastCollect.Store(time.Now().UnixMilli())
	}
	child.active.Store(0)
	return err
}

// Initial readiness still waits for one complete pass across every source.
func (s *Service) importS3(ctx context.Context) error {
	jobs, err := s.factSources(ctx)
	if err != nil {
		return err
	}
	s.importMu.Lock()
	s.sourceImports = make(map[string]*Service, len(jobs))
	for _, child := range jobs {
		child.active.Store(1)
		s.sourceImports[child.cfg.SourceID] = child
	}
	s.importMu.Unlock()
	slots := make(chan struct{}, 4)
	errs := make(chan error, len(jobs))
	var wg sync.WaitGroup
	for _, child := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				child.active.Store(0)
				errs <- ctx.Err()
				return
			}
			errs <- s.runSourceImport(ctx, child)
		}()
	}
	wg.Wait()
	close(errs)
	var failures []error
	var remaining int64
	for e := range errs {
		if e != nil {
			failures = append(failures, e)
		}
	}
	for _, child := range jobs {
		remaining += child.remaining.Load()
	}
	s.remaining.Store(remaining)
	return errors.Join(failures...)
}

func (s *Service) startImportLoop(ctx context.Context) {
	s.pollSourceImports(ctx, 5*time.Second)
}

func (s *Service) pollSourceImports(ctx context.Context, interval time.Duration) {
	var wg sync.WaitGroup
	defer wg.Wait()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		jobs, err := s.factSources(ctx)
		if err != nil {
			s.importFailures.Add(1)
			s.importError.Store(importErrorClass(err))
		} else {
			s.importMu.Lock()
			if s.sourceImports == nil {
				s.sourceImports = map[string]*Service{}
			}
			present := map[string]bool{}
			for _, child := range jobs {
				present[child.cfg.SourceID] = true
			}
			for id, child := range s.sourceImports {
				if !present[id] && child.active.Load() == 0 {
					delete(s.sourceImports, id)
				}
			}
			// Give never-scanned/older sources first access to the four scan slots.
			sort.SliceStable(jobs, func(i, j int) bool {
				a, b := s.sourceImports[jobs[i].cfg.SourceID], s.sourceImports[jobs[j].cfg.SourceID]
				if a == nil {
					return b != nil
				}
				if b == nil {
					return false
				}
				return a.scanStarted.Load() < b.scanStarted.Load()
			})
			active := 0
			for _, child := range s.sourceImports {
				active += int(child.active.Load())
			}
			for _, child := range jobs {
				old := s.sourceImports[child.cfg.SourceID]
				if active >= 4 || (old != nil && old.active.Load() != 0) {
					continue
				}
				if old != nil {
					child.lastCollect.Store(old.lastCollect.Load())
					child.importFailures.Store(old.importFailures.Load())
					if e := old.importError.Load(); e != nil {
						child.importError.Store(e)
					}
				}
				child.active.Store(1)
				child.scanStarted.Store(time.Now().UnixMilli())
				s.sourceImports[child.cfg.SourceID] = child
				active++
				wg.Add(1)
				go func() {
					defer wg.Done()
					if s.runSourceImport(ctx, child) == nil {
						s.maybeCheckpoint(ctx)
					}
				}()
			}
			var last, remaining int64
			var failures uint64
			errorClass := ""
			first := true
			for _, child := range s.sourceImports {
				scanned := child.lastCollect.Load()
				if first || scanned < last {
					last = scanned
				}
				first = false
				remaining += child.remaining.Load()
				failures += child.importFailures.Load()
				if e := child.importError.Load(); e != nil && e != "" {
					errorClass = e.(string)
				}
			}
			s.active.Store(int64(active))
			s.remaining.Store(remaining)
			s.importFailures.Store(failures)
			s.importError.Store(errorClass)
			if last > 0 {
				s.lastCollect.Store(last)
			}
			s.importMu.Unlock()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Report only sources that belong to the authenticated query, with scan state
// separate from known pending objects. Zero discovered objects is not caught up
// while pagination is still in progress.
func (s *Service) importStatus(allowed []string) map[string]any {
	s.importMu.Lock()
	defer s.importMu.Unlock()
	var remaining, last int64
	var failures uint64
	scanning, complete := false, true
	errorClass := ""
	sources := []map[string]any{}
	for _, id := range allowed {
		child := s.sourceImports[id]
		if child == nil {
			continue
		}
		active := child.active.Load() != 0
		at := child.lastCollect.Load()
		if len(sources) == 0 || at < last {
			last = at
		}
		if at == 0 {
			complete = false
		}
		scanning = scanning || active
		remaining += child.remaining.Load()
		failures += child.importFailures.Load()
		sourceError := ""
		if e := child.importError.Load(); e != nil {
			sourceError = e.(string)
		}
		if sourceError != "" {
			errorClass = sourceError
		}
		sources = append(sources, map[string]any{"source_id": id, "scanning": active, "scan_started_ms": child.scanStarted.Load(), "last_scan_ms": at, "pages": child.listPages.Load(), "listed_objects": child.listedObjects.Load(), "remaining_objects": child.remaining.Load(), "error_class": sourceError})
	}
	if len(sources) == 0 {
		last = s.lastCollect.Load()
		remaining = s.remaining.Load()
		failures = s.importFailures.Load()
		scanning = s.active.Load() != 0
		if e := s.importError.Load(); e != nil {
			errorClass = e.(string)
		}
		complete = last > 0
	}
	return map[string]any{"last_scan_ms": last, "remaining_objects": remaining, "errors": failures, "error_class": errorClass, "scanning": scanning, "caught_up": complete && !scanning && remaining == 0 && errorClass == "", "sources": sources}
}
