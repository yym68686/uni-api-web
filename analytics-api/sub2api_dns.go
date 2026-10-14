package main

import (
	"context"
	"errors"
	"log"
	"net"
	"strings"
	"time"
)

var errSubDNSLookup = errors.New("站点域名解析失败，请稍后重试或检查站点 DNS 配置")

type subIPLookup func(context.Context, string) ([]net.IPAddr, error)

type subDNSResolver struct {
	name    string
	timeout time.Duration
	lookup  subIPLookup
}

var subSiteResolvers = func() []subDNSResolver {
	resolvers := []subDNSResolver{{"system", 3 * time.Second, net.DefaultResolver.LookupIPAddr}}
	for _, server := range []string{"1.1.1.1:53", "8.8.8.8:53"} {
		resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, network, server)
		}}
		resolvers = append(resolvers, subDNSResolver{server, 2 * time.Second, resolver.LookupIPAddr})
	}
	return resolvers
}()

func subLookupPublicIPs(ctx context.Context, host string) ([]net.IPAddr, error) {
	ips, resolver, err := subResolvePublicIPs(ctx, host, subSiteResolvers)
	if err == nil && resolver > 0 {
		// No URLs, query strings, credentials or upstream response text in logs.
		log.Printf("event=site_dns_fallback host=%q resolver=%s", host, subSiteResolvers[resolver].name)
	}
	return ips, err
}

// Retry only transient DNS failures. NXDOMAIN and private-address answers are
// final: a public resolver must never override a local deny/private answer.
// Every answer is validated before dialing the numeric address; TLS still uses
// the original host. This does not change cluster DNS or follow HTTP redirects.
func subResolvePublicIPs(ctx context.Context, host string, resolvers []subDNSResolver) ([]net.IPAddr, int, error) {
	if ip := net.ParseIP(host); ip != nil {
		if !subPublicIP(ip) {
			return nil, 0, errors.New("不允许访问内部地址")
		}
		return []net.IPAddr{{IP: ip}}, 0, nil
	}
	for i, resolver := range resolvers {
		if err := ctx.Err(); err != nil {
			return nil, i, err
		}
		name := host
		if i > 0 {
			// Public fallback is only for fully qualified site names. The trailing
			// dot avoids leaking Kubernetes search suffixes to public DNS.
			if !strings.Contains(strings.TrimSuffix(host, "."), ".") {
				break
			}
			name = strings.TrimSuffix(host, ".") + "."
		}
		lookupCtx, cancel := context.WithTimeout(ctx, resolver.timeout)
		ips, err := resolver.lookup(lookupCtx, name)
		cancel()
		if ctx.Err() != nil {
			return nil, i, ctx.Err()
		}
		if err == nil {
			if len(ips) == 0 {
				break
			}
			for _, ip := range ips {
				if !subPublicIP(ip.IP) {
					return nil, i, errors.New("不允许访问内部地址")
				}
			}
			return ips, i, nil
		}
		var dnsErr *net.DNSError
		if !errors.Is(err, context.DeadlineExceeded) && !(errors.As(err, &dnsErr) && !dnsErr.IsNotFound && (dnsErr.IsTimeout || dnsErr.IsTemporary)) {
			break
		}
	}
	return nil, 0, errSubDNSLookup
}
