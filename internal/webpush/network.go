package webpush

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var errNonPublicEndpoint = errors.New("web push endpoint destination is not public")

type endpointResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type endpointDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

type safeDialer struct {
	resolver endpointResolver
	dialer   endpointDialer
}

func (d *safeDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errNonPublicEndpoint
	}
	addresses, err := resolvePublicHost(ctx, host, d.resolver)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, resolved := range addresses {
		connection, err := d.dialer.DialContext(ctx, network, net.JoinHostPort(resolved.IP.String(), port))
		if err == nil {
			return connection, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errNonPublicEndpoint
}

type safeRoundTripper struct {
	base     http.RoundTripper
	resolver endpointResolver
}

func (t *safeRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil || request.URL == nil {
		return nil, errNonPublicEndpoint
	}
	if err := validateEndpointURL(request.URL); err != nil {
		return nil, err
	}
	if _, err := resolvePublicHost(request.Context(), request.URL.Hostname(), t.resolver); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(request)
}

func newSafeHTTPClient(timeout time.Duration, resolver endpointResolver, dialer endpointDialer, base http.RoundTripper) *http.Client {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	if dialer == nil {
		dialer = &net.Dialer{Timeout: timeout}
	}
	if base == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.DialContext = (&safeDialer{resolver: resolver, dialer: dialer}).DialContext
		base = transport
	}
	return &http.Client{
		Transport: &safeRoundTripper{base: base, resolver: resolver},
		Timeout:   timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func validateEndpointURL(endpoint *url.URL) error {
	if endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.Fragment != "" {
		return errNonPublicEndpoint
	}
	return nil
}

func resolvePublicHost(ctx context.Context, host string, resolver endpointResolver) ([]net.IPAddr, error) {
	if strings.Contains(host, "%") {
		return nil, errNonPublicEndpoint
	}
	if ip := net.ParseIP(host); ip != nil {
		if !isPublicIP(ip) {
			return nil, errNonPublicEndpoint
		}
		return []net.IPAddr{{IP: ip}}, nil
	}
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addresses, err := resolver.LookupIPAddr(ctx, host)
	if err != nil || len(addresses) == 0 {
		return nil, errNonPublicEndpoint
	}
	for _, address := range addresses {
		if address.Zone != "" || !isPublicIP(address.IP) {
			return nil, errNonPublicEndpoint
		}
	}
	return addresses, nil
}

func isPublicIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	for _, network := range nonPublicEndpointCIDRs {
		if network.Contains(ip) {
			return false
		}
	}
	return true
}

var nonPublicEndpointCIDRs = mustParseEndpointCIDRs([]string{
	"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4",
	"2001:2::/48", "2001:10::/28", "2001:db8::/32",
})

func mustParseEndpointCIDRs(values []string) []*net.IPNet {
	result := make([]*net.IPNet, 0, len(values))
	for _, value := range values {
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			panic(err)
		}
		result = append(result, network)
	}
	return result
}
