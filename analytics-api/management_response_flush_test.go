package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// Intentionally hide flushing: unsupported writers must keep the old buffered
// behavior rather than turning an otherwise valid 304 into an error.
type managementNonFlushingWriter struct{ http.ResponseWriter }

type managementCommitWriter struct {
	http.ResponseWriter
	committed chan int
}

func (w *managementCommitWriter) WriteHeader(status int) {
	w.ResponseWriter.WriteHeader(status)
	w.committed <- status
}
func (w *managementCommitWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func managementFlushRequest(url, encoding, tag string) *http.Request {
	r, _ := http.NewRequest(http.MethodGet, url, nil)
	r.Header.Set("Accept", managementPageV3MediaType)
	r.Header.Set("Accept-Encoding", encoding)
	r.Header.Set("X-Console-Current-Validation", "1")
	r.Header.Set("If-None-Match", tag)
	return r.WithContext(context.WithValue(r.Context(), consoleRequestIDKey{}, "flush-response-123"))
}

// A real HTTP client must receive the COMPLETE 304 while a separate barrier
// still holds retention. This is a local transport regression, not a production
// or end-to-end latency measurement; all current-data proof precedes writing.
func TestManagement304FlushCompletesBeforeSynchronousRetention(t *testing.T) {
	page := managementWireFixture()
	proof := &managementFreshProof{Stamp: "current"}
	for _, encoding := range []string{"identity", "gzip", "br"} {
		for _, flush := range []bool{false, true} {
			name := encoding + "/unsupported"
			if flush {
				name = encoding + "/flush"
			}
			t.Run(name, func(t *testing.T) {
				initial := httptest.NewRecorder()
				if err := (&Service{}).writeAndRememberManagementPage(initial, managementFlushRequest("http://fixture.test", encoding, ""), page, "page", proof); err != nil {
					t.Fatal(err)
				}
				s := &Service{}
				committed, handlerDone := make(chan int, 1), make(chan error, 1)
				s.managementComputed.mu.Lock()
				var unlockOnce sync.Once
				unlock := func() { unlockOnce.Do(func() { s.managementComputed.mu.Unlock() }) }
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					r = r.WithContext(context.WithValue(r.Context(), consoleRequestIDKey{}, "flush-response-123"))
					if !flush {
						w = managementNonFlushingWriter{w}
					}
					w = &managementCommitWriter{ResponseWriter: w, committed: committed}
					w = &observedResponse{ResponseWriter: w}
					handlerDone <- s.writeAndRememberManagementPage(w, r, page, "page", proof)
				}))
				defer func() { unlock(); server.Close() }()
				server.Client().Timeout = 3 * time.Second
				server.Client().Transport.(*http.Transport).DisableCompression = true
				clientDone := make(chan error, 1)
				r := managementFlushRequest(server.URL, encoding, initial.Header().Get("ETag"))
				go func() {
					response, err := server.Client().Do(r)
					if err == nil {
						body, readErr := io.ReadAll(response.Body)
						response.Body.Close()
						if readErr != nil {
							err = readErr
						} else if response.StatusCode != 304 || len(body) != 0 || response.Header.Get("ETag") != initial.Header().Get("ETag") || response.Header.Get("X-Console-Read-At-Ms") != "123" || response.Header.Get("X-Console-Validated-Request-ID") != "flush-response-123" {
							err = errors.New("complete validated response changed")
						}
					}
					clientDone <- err
				}()
				select {
				case status := <-committed:
					if status != http.StatusNotModified {
						t.Fatal("did not revalidate", status)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("response never reached WriteHeader")
				}
				if flush {
					select {
					case err := <-clientDone:
						if err != nil {
							t.Fatal(err)
						}
					case <-time.After(2 * time.Second):
						t.Fatal("complete response waited for retention")
					}
				} else {
					select {
					case <-clientDone:
						t.Fatal("unsupported writer unexpectedly bypassed retention")
					case <-time.After(50 * time.Millisecond):
					}
				}
				select {
				case <-handlerDone:
					t.Fatal("retention was skipped or moved to a goroutine")
				default:
				}
				unlock()
				select {
				case err := <-handlerDone:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("retention did not complete")
				}
				if !flush {
					if err := <-clientDone; err != nil {
						t.Fatal(err)
					}
				}
				if mustJSON(s.managementComputed.pages["page"]) != mustJSON(initialStoreForFlushTest(t, page, proof)["page"]) {
					t.Fatal("retention or proof changed")
				}
			})
		}
	}
}

func initialStoreForFlushTest(t *testing.T, page managementPageResponse, proof *managementFreshProof) map[string]managementComputedPage {
	t.Helper()
	s := &Service{}
	s.rememberManagementPage("page", proof, page)
	return s.managementComputed.pages
}

type managementFlushErrorWriter struct {
	*httptest.ResponseRecorder
	err     error
	flushes int
}

func (w *managementFlushErrorWriter) FlushError() error { w.flushes++; return w.err }

func TestManagement304FlushPreservesErrorsAndAllOtherResponses(t *testing.T) {
	page, proof := managementWireFixture(), &managementFreshProof{Stamp: "current"}
	initial := httptest.NewRecorder()
	if err := writeManagementPage(initial, managementFlushRequest("http://fixture.test", "identity", ""), page); err != nil {
		t.Fatal(err)
	}
	for _, failure := range []error{nil, http.ErrNotSupported, fmt.Errorf("wrapped: %w", http.ErrNotSupported), io.ErrClosedPipe, context.Canceled} {
		s := &Service{}
		w := &managementFlushErrorWriter{ResponseRecorder: httptest.NewRecorder(), err: failure}
		observed := &observedResponse{ResponseWriter: w}
		err := s.writeAndRememberManagementPage(observed, managementFlushRequest("http://fixture.test", "identity", initial.Header().Get("ETag")), page, "page", proof)
		want := failure
		if errors.Is(want, http.ErrNotSupported) {
			want = nil
		}
		_, stored := s.managementComputed.pages["page"]
		if !errors.Is(err, want) || observed.status != 304 || observed.bytes != 0 || w.flushes != 1 || stored != (want == nil) {
			t.Fatal("flush failure was swallowed or failed transport was retained", failure, err, w.flushes, stored)
		}
	}
	for _, media := range []string{"application/json", managementPageV2MediaType, managementPageV3MediaType} {
		for _, validation := range []string{"", "1"} {
			for _, tag := range []string{"", `"different"`, initial.Header().Get("ETag")} {
				if media == managementPageV3MediaType && validation == "1" && tag == initial.Header().Get("ETag") {
					continue // The conditional branch is checked above.
				}
				for _, encoding := range []string{"identity", "gzip", "br"} {
					r := managementFlushRequest("http://fixture.test", encoding, tag)
					r.Header.Set("Accept", media)
					r.Header.Set("X-Console-Current-Validation", validation)
					old, recorder := httptest.NewRecorder(), httptest.NewRecorder()
					w := &managementFlushErrorWriter{ResponseRecorder: recorder, err: io.ErrClosedPipe}
					err := writeManagementPage(old, r, page)
					newErr := (&Service{}).writeAndRememberManagementPage(&observedResponse{ResponseWriter: w}, r, page, "page", proof)
					if err != nil || newErr != nil || w.flushes != 0 || old.Code != recorder.Code || !reflect.DeepEqual(old.Header(), recorder.Header()) || !bytes.Equal(old.Body.Bytes(), recorder.Body.Bytes()) {
						t.Fatal("non-304 response changed", media, validation, tag, encoding)
					}
				}
			}
		}
	}
	for _, tag := range []string{"", initial.Header().Get("ETag")} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		r := managementFlushRequest("http://fixture.test", "identity", tag).WithContext(ctx)
		w := &managementFlushErrorWriter{ResponseRecorder: httptest.NewRecorder()}
		s := &Service{}
		if err := s.writeAndRememberManagementPage(&observedResponse{ResponseWriter: w}, r, page, "page", proof); !errors.Is(err, context.Canceled) || w.flushes != 0 || w.Body.Len() != 0 || len(s.managementComputed.pages) != 0 {
			t.Fatal("canceled request was flushed or retained", err)
		}
	}
}

func TestObservedResponseFlushPreservesStatusAndTransportCapabilities(t *testing.T) {
	for _, status := range []int{0, 200, 304} {
		for _, failure := range []error{nil, io.ErrClosedPipe} {
			underlying := &managementFlushErrorWriter{ResponseRecorder: httptest.NewRecorder(), err: failure}
			w := &observedResponse{ResponseWriter: underlying}
			if status != 0 {
				w.WriteHeader(status)
			}
			if err := http.NewResponseController(w).Flush(); !errors.Is(err, failure) {
				t.Fatal("controller lost transport error", err)
			}
			w.Flush() // The existing no-error interface keeps its old behavior.
			if w.status != max(status, http.StatusOK) || underlying.Code != w.status || w.bytes != 0 || underlying.flushes != 2 || w.Unwrap() != underlying {
				t.Fatal("flush changed status, byte accounting, or legacy interface")
			}
		}
	}
	underlying := httptest.NewRecorder()
	w := &observedResponse{ResponseWriter: managementNonFlushingWriter{underlying}}
	if err := http.NewResponseController(w).Flush(); !errors.Is(err, http.ErrNotSupported) || w.status != http.StatusOK {
		t.Fatal("unsupported transport was hidden", err)
	}
}

func TestManagement304FlushDiagnosticsDoNotExposeRequestOrTransport(t *testing.T) {
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)
	r := managementFlushRequest("http://fixture.test/private-source?key=private-key", "identity", "")
	r = r.WithContext(managementTraceContext(r.Context()))
	initial := httptest.NewRecorder()
	if err := writeManagementPage(initial, r, managementWireFixture()); err != nil {
		t.Fatal(err)
	}
	r.Header.Set("If-None-Match", initial.Header().Get("ETag"))
	w := &managementFlushErrorWriter{ResponseRecorder: httptest.NewRecorder(), err: errors.New("private-transport-error")}
	if err := writeManagementPage(&observedResponse{ResponseWriter: w}, r, managementWireFixture()); err == nil {
		t.Fatal("failed flush was hidden")
	}
	for _, want := range []string{"stage=response_flush", "management_response_flush status=304 flushed=false unsupported=false request_id=flush-response-123"} {
		if !strings.Contains(logs.String(), want) {
			t.Fatal("missing safe diagnostic", want)
		}
	}
	if strings.Contains(logs.String(), "private-") {
		t.Fatal("request or raw transport cause escaped")
	}
}
