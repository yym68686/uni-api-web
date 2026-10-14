package main

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestSubDNSFallbackOnTransientFailure(t *testing.T) {
	for _, failure := range []error{&net.DNSError{IsTemporary: true}, &net.DNSError{IsTimeout: true}, context.DeadlineExceeded} {
		calls := 0
		resolvers := []subDNSResolver{
			{"system", time.Second, func(_ context.Context, host string) ([]net.IPAddr, error) {
				calls++
				if host != "fork.example" {
					t.Fatal("unexpected system name", host)
				}
				return nil, failure
			}},
			{"fallback", time.Second, func(_ context.Context, host string) ([]net.IPAddr, error) {
				calls++
				if host != "fork.example." {
					t.Fatal("public query must be absolute", host)
				}
				return []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}}, nil
			}},
		}
		ips, resolver, err := subResolvePublicIPs(context.Background(), "fork.example", resolvers)
		if err != nil || resolver != 1 || calls != 2 || len(ips) != 1 {
			t.Fatalf("fallback failed: ips=%v resolver=%d calls=%d err=%v", ips, resolver, calls, err)
		}
	}
}

func TestSubDNSDoesNotOverridePrivateOrPermanentAnswers(t *testing.T) {
	for _, tc := range []struct {
		name string
		ips  []net.IPAddr
		err  error
	}{
		{"private", []net.IPAddr{{IP: net.ParseIP("10.0.0.1")}}, nil},
		{"mixed", []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}, {IP: net.ParseIP("::1")}}, nil},
		{"nxdomain", nil, &net.DNSError{IsNotFound: true}},
		{"empty", nil, nil},
		{"permanent", nil, errors.New("resolver failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolvers := []subDNSResolver{
				{"system", time.Second, func(context.Context, string) ([]net.IPAddr, error) { return tc.ips, tc.err }},
				{"fallback", time.Second, func(context.Context, string) ([]net.IPAddr, error) {
					t.Fatal("must not override a private/permanent answer")
					return nil, nil
				}},
			}
			if _, _, err := subResolvePublicIPs(context.Background(), "fork.example", resolvers); err == nil {
				t.Fatal("unsafe answer accepted")
			}
		})
	}
}

func TestSubDNSFallbackIsBoundedAndValidatesEveryAnswer(t *testing.T) {
	resolvers := []subDNSResolver{
		{"system", 10 * time.Millisecond, func(ctx context.Context, _ string) ([]net.IPAddr, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}},
		{"fallback", time.Second, func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("169.254.169.254")}}, nil
		}},
	}
	if _, _, err := subResolvePublicIPs(context.Background(), "fork.example", resolvers); err == nil {
		t.Fatal("fallback bypassed metadata-address protection")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := subResolvePublicIPs(ctx, "fork.example", resolvers); !errors.Is(err, context.Canceled) {
		t.Fatal("lost cancellation", err)
	}
	resolvers[1].lookup = func(context.Context, string) ([]net.IPAddr, error) {
		t.Fatal("fallback queried a single-label internal name or ignored parent deadline")
		return nil, nil
	}
	if _, _, err := subResolvePublicIPs(context.Background(), "internal", resolvers); !errors.Is(err, errSubDNSLookup) {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, _, err := subResolvePublicIPs(ctx, "fork.example", resolvers); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("parent deadline was ignored", err)
	}
}

func TestSubDNSWorkingSystemResolverNeedsNoPublicQuery(t *testing.T) {
	calls := 0
	resolvers := []subDNSResolver{
		{"system", time.Second, func(context.Context, string) ([]net.IPAddr, error) {
			calls++
			return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
		}},
		{"fallback", time.Second, func(context.Context, string) ([]net.IPAddr, error) {
			t.Fatal("unnecessary fallback")
			return nil, nil
		}},
	}
	for _, host := range []string{"fork.example", "1.1.1.1"} {
		if _, resolver, err := subResolvePublicIPs(context.Background(), host, resolvers); err != nil || resolver != 0 {
			t.Fatal(host, resolver, err)
		}
	}
	if calls != 1 {
		t.Fatal("IP literal triggered DNS", calls)
	}
}
