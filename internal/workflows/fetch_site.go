package workflows

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"go.temporal.io/sdk/temporal"
	"golang.org/x/net/html"
)

const (
	fetchSiteTextLimit        = 50 * 1024
	fetchSitePageBodyLimit    = 1024 * 1024
	fetchSiteSitemapBodyLimit = 256 * 1024
	fetchSiteMaxRequests      = 20
	fetchSiteMaxRedirects     = 5
	fetchSiteTimeout          = 20 * time.Second
)

var (
	errUnsafeFetchURL  = errors.New("unsafe fetch url")
	errUnusablePage    = errors.New("unusable page")
	errNoUsableContent = errors.New("no usable site content")
)

var blockedFetchIPPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:2::/48"),
	netip.MustParsePrefix("2001:10::/28"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3ffe::/16"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("fec0::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

var fetchSiteHighValuePaths = []string{"/about", "/services", "/team", "/doctors", "/contact"}

var fetchSiteRelevantPathTerms = []string{
	"about",
	"contact",
	"doctor",
	"doctors",
	"location",
	"practitioner",
	"service",
	"services",
	"team",
}

type ipResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type netIPResolver struct{}

func (netIPResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

type dialContextFunc func(context.Context, string, string) (net.Conn, error)

// FetchSiteInput is the ONB-1 website fetch request.
type FetchSiteInput struct {
	Website string
}

// FetchSiteOutput is the stripped, capped site context used by later onboarding
// proposal activities.
type FetchSiteOutput struct {
	Text string   `json:"text"`
	URLs []string `json:"urls"`
}

type siteFetcher struct {
	client           *http.Client
	textLimit        int
	pageBodyLimit    int64
	sitemapBodyLimit int64
	maxRequests      int
}

type fetchedPage struct {
	URL      *url.URL
	Text     string
	NavHrefs []string
}

// FetchSite reads a clinic website with SSRF protections and returns stripped,
// bounded text for onboarding profile generation.
func (a *Activities) FetchSite(ctx context.Context, in FetchSiteInput) (FetchSiteOutput, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchSiteTimeout)
	defer cancel()

	fetcher := newSiteFetcher()
	defer fetcher.closeIdleConnections()

	out, err := fetcher.Fetch(ctx, in)
	if err != nil {
		if errors.Is(err, errUnsafeFetchURL) || errors.Is(err, errNoUsableContent) {
			return FetchSiteOutput{}, temporal.NewNonRetryableApplicationError(
				"fetch site", "BadWebsite", err)
		}
		return FetchSiteOutput{}, err
	}
	return out, nil
}

func newSiteFetcher() *siteFetcher {
	return &siteFetcher{
		client:           newSafeFetchHTTPClient(),
		textLimit:        fetchSiteTextLimit,
		pageBodyLimit:    fetchSitePageBodyLimit,
		sitemapBodyLimit: fetchSiteSitemapBodyLimit,
		maxRequests:      fetchSiteMaxRequests,
	}
}

func newSafeFetchHTTPClient() *http.Client {
	dialer := &net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	return &http.Client{
		Timeout: fetchSiteTimeout,
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           safeFetchDialContext(netIPResolver{}, dialer.DialContext),
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			IdleConnTimeout:       30 * time.Second,
		},
		CheckRedirect: safeFetchRedirect,
	}
}

func (f *siteFetcher) closeIdleConnections() {
	if f != nil && f.client != nil {
		f.client.CloseIdleConnections()
	}
}

func safeFetchDialContext(resolver ipResolver, dial dialContextFunc) dialContextFunc {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid dial address %q", errUnsafeFetchURL, address)
		}
		addrs, err := resolveFetchHost(ctx, resolver, host)
		if err != nil {
			return nil, err
		}
		var lastErr error
		for _, addr := range addrs {
			conn, err := dial(ctx, network, net.JoinHostPort(addr.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
}

func resolveFetchHost(ctx context.Context, resolver ipResolver, host string) ([]netip.Addr, error) {
	host = strings.TrimSpace(host)
	if host == "" || strings.Contains(host, "%") {
		return nil, fmt.Errorf("%w: invalid host", errUnsafeFetchURL)
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if !isPublicFetchAddr(addr) {
			return nil, fmt.Errorf("%w: refused private address %s", errUnsafeFetchURL, addr)
		}
		return []netip.Addr{normalizeFetchAddr(addr)}, nil
	}

	ips, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("resolve %s: no addresses", host)
	}

	addrs := make([]netip.Addr, 0, len(ips))
	for _, ip := range ips {
		addr, ok := netip.AddrFromSlice(ip.IP)
		if !ok {
			return nil, fmt.Errorf("%w: resolver returned invalid address for %s", errUnsafeFetchURL, host)
		}
		addr = normalizeFetchAddr(addr)
		if !isPublicFetchAddr(addr) {
			return nil, fmt.Errorf("%w: refused resolved address %s for %s", errUnsafeFetchURL, addr, host)
		}
		addrs = append(addrs, addr)
	}
	return addrs, nil
}

func normalizeFetchAddr(addr netip.Addr) netip.Addr {
	if addr.Is4In6() {
		return netip.AddrFrom4(addr.As4())
	}
	return addr.Unmap()
}

func isPublicFetchAddr(addr netip.Addr) bool {
	addr = normalizeFetchAddr(addr)
	if !addr.IsValid() || !addr.IsGlobalUnicast() {
		return false
	}
	for _, blocked := range blockedFetchIPPrefixes {
		if blocked.Contains(addr) {
			return false
		}
	}
	return true
}

func safeFetchRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= fetchSiteMaxRedirects {
		return fmt.Errorf("%w: too many redirects", errUnsafeFetchURL)
	}
	return validateFetchURL(req.URL)
}

func (f *siteFetcher) Fetch(ctx context.Context, in FetchSiteInput) (FetchSiteOutput, error) {
	f = f.withDefaults()

	startURL, err := parseFetchURL(in.Website)
	if err != nil {
		return FetchSiteOutput{}, err
	}

	requests := 0
	acc := textAccumulator{limit: f.textLimit}
	var urls []string
	seen := make(map[string]bool)
	queued := make(map[string]bool)
	var firstTransientErr error

	home, err := f.fetchHTMLPage(ctx, startURL, &requests)
	origin := originFor(startURL)
	if err == nil {
		origin = originFor(home.URL)
		seen[canonicalFetchURLKey(home.URL)] = true
		if acc.addPage(home.URL.String(), home.Text) {
			urls = append(urls, home.URL.String())
		}
	} else if errors.Is(err, errUnsafeFetchURL) {
		return FetchSiteOutput{}, err
	} else if !errors.Is(err, errUnusablePage) {
		return FetchSiteOutput{}, err
	}

	var candidates []*url.URL
	enqueue := func(candidate *url.URL) {
		if candidate == nil {
			return
		}
		key := canonicalFetchURLKey(candidate)
		if seen[key] || queued[key] {
			return
		}
		queued[key] = true
		candidates = append(candidates, candidate)
	}

	for _, p := range fetchSiteHighValuePaths {
		enqueue(originWithPath(origin, p))
	}
	if home != nil {
		for _, href := range home.NavHrefs {
			if candidate, ok := normalizeSameOriginFetchURL(home.URL, href, false); ok {
				enqueue(candidate)
			}
		}
	}

	processCandidates := func(candidates []*url.URL) error {
		for _, candidate := range candidates {
			if !acc.hasRoom() || requests >= f.maxRequests {
				break
			}
			key := canonicalFetchURLKey(candidate)
			seen[key] = true

			page, err := f.fetchHTMLPage(ctx, candidate, &requests)
			if errors.Is(err, errUnsafeFetchURL) {
				return err
			}
			if err != nil {
				if !errors.Is(err, errUnusablePage) && firstTransientErr == nil {
					firstTransientErr = err
				}
				continue
			}
			finalKey := canonicalFetchURLKey(page.URL)
			if finalKey != key && seen[finalKey] {
				continue
			}
			seen[finalKey] = true
			if acc.addPage(page.URL.String(), page.Text) {
				urls = append(urls, page.URL.String())
			}
		}
		return nil
	}

	if err := processCandidates(candidates); err != nil {
		return FetchSiteOutput{}, err
	}

	if acc.hasRoom() && requests < f.maxRequests {
		sitemapURL := originWithPath(origin, "/sitemap.xml")
		sitemapCandidates, err := f.fetchSitemap(ctx, origin, sitemapURL, &requests, 0, &firstTransientErr)
		if errors.Is(err, errUnsafeFetchURL) {
			return FetchSiteOutput{}, err
		}
		if err != nil && !errors.Is(err, errUnusablePage) && firstTransientErr == nil {
			firstTransientErr = err
		}
		if err == nil {
			candidates = candidates[:0]
			for _, candidate := range sitemapCandidates {
				enqueue(candidate)
			}
			if err := processCandidates(candidates); err != nil {
				return FetchSiteOutput{}, err
			}
		}
	}

	text := acc.String()
	if strings.TrimSpace(text) == "" {
		if firstTransientErr != nil {
			return FetchSiteOutput{}, firstTransientErr
		}
		return FetchSiteOutput{}, errNoUsableContent
	}
	return FetchSiteOutput{Text: text, URLs: urls}, nil
}

func (f *siteFetcher) withDefaults() *siteFetcher {
	copy := *f
	if copy.client == nil {
		copy.client = newSafeFetchHTTPClient()
	}
	if copy.textLimit <= 0 {
		copy.textLimit = fetchSiteTextLimit
	}
	if copy.pageBodyLimit <= 0 {
		copy.pageBodyLimit = fetchSitePageBodyLimit
	}
	if copy.sitemapBodyLimit <= 0 {
		copy.sitemapBodyLimit = fetchSiteSitemapBodyLimit
	}
	if copy.maxRequests <= 0 {
		copy.maxRequests = fetchSiteMaxRequests
	}
	return &copy
}

func (f *siteFetcher) fetchHTMLPage(ctx context.Context, target *url.URL, requests *int) (*fetchedPage, error) {
	body, finalURL, contentType, err := f.fetchURL(ctx, target, "text/html,application/xhtml+xml", f.pageBodyLimit, requests)
	if err != nil {
		return nil, err
	}
	if !isHTMLContentType(contentType) {
		return nil, errUnusablePage
	}

	text, navHrefs, err := extractHTMLTextAndNav(body)
	if err != nil {
		return nil, errUnusablePage
	}
	if strings.TrimSpace(text) == "" {
		return nil, errUnusablePage
	}
	return &fetchedPage{URL: finalURL, Text: text, NavHrefs: navHrefs}, nil
}

func (f *siteFetcher) fetchSitemap(ctx context.Context, origin, target *url.URL, requests *int, depth int, firstTransientErr *error) ([]*url.URL, error) {
	if depth > 1 {
		return nil, nil
	}
	body, _, contentType, err := f.fetchURL(ctx, target, "application/xml,text/xml,*/*", f.sitemapBodyLimit, requests)
	if err != nil {
		recordFetchTransient(firstTransientErr, err)
		return nil, err
	}
	if contentType != "" && !isXMLLikeContentType(contentType) {
		return nil, nil
	}

	locs := parseSitemapLocs(body)
	var pageCandidates []*url.URL
	var childSitemaps []*url.URL
	for _, loc := range locs {
		candidate, ok := normalizeSameOriginFetchURL(origin, loc, true)
		if !ok {
			continue
		}
		if isLikelySitemapURL(candidate) {
			childSitemaps = append(childSitemaps, candidate)
			continue
		}
		if isBlockedFetchPath(candidate.Path) {
			continue
		}
		pageCandidates = append(pageCandidates, candidate)
	}

	sortFetchCandidates(pageCandidates)
	if depth == 0 && len(pageCandidates) == 0 {
		childCandidates, err := f.fetchSitemapChildCandidates(ctx, origin, childSitemaps, requests, firstTransientErr)
		if err != nil {
			return nil, err
		}
		pageCandidates = childCandidates
	}
	return pageCandidates, nil
}

func (f *siteFetcher) fetchSitemapChildCandidates(ctx context.Context, origin *url.URL, childSitemaps []*url.URL, requests *int, firstTransientErr *error) ([]*url.URL, error) {
	sortFetchCandidates(childSitemaps)
	var pageCandidates []*url.URL
	var firstChildTransientErr error
	for _, child := range childSitemaps {
		if *requests >= f.maxRequests-1 {
			break
		}
		childCandidates, err := f.fetchSitemap(ctx, origin, child, requests, 1, firstTransientErr)
		if errors.Is(err, errUnsafeFetchURL) {
			return nil, err
		}
		if err != nil {
			if !errors.Is(err, errUnusablePage) && firstChildTransientErr == nil {
				firstChildTransientErr = err
			}
			continue
		}
		if len(childCandidates) == 0 {
			continue
		}
		pageCandidates = append(pageCandidates, childCandidates...)
		break
	}
	if len(pageCandidates) == 0 && firstChildTransientErr != nil {
		return nil, firstChildTransientErr
	}
	sortFetchCandidates(pageCandidates)
	return pageCandidates, nil
}

func recordFetchTransient(dest *error, err error) {
	if dest == nil || *dest != nil || err == nil {
		return
	}
	if errors.Is(err, errUnsafeFetchURL) || errors.Is(err, errUnusablePage) {
		return
	}
	*dest = err
}

func (f *siteFetcher) fetchURL(ctx context.Context, target *url.URL, accept string, bodyLimit int64, requests *int) ([]byte, *url.URL, string, error) {
	if requests != nil {
		if *requests >= f.maxRequests {
			return nil, nil, "", errUnusablePage
		}
		*requests++
	}
	if err := validateFetchURL(target); err != nil {
		return nil, nil, "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, nil, "", fmt.Errorf("%w: build request: %v", errUnsafeFetchURL, err)
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "OpenSight/0.1 (+https://opensight.local)")

	resp, err := f.client.Do(req)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, nil, "", fmt.Errorf("fetch %s: %w", target.Redacted(), err)
	}
	defer func() { _ = resp.Body.Close() }()

	finalURL := resp.Request.URL
	if err := validateFetchURL(finalURL); err != nil {
		return nil, nil, "", err
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		return nil, finalURL, "", errUnusablePage
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode >= 500 {
			return nil, finalURL, "", fmt.Errorf("fetch %s: status %d", finalURL.Redacted(), resp.StatusCode)
		}
		return nil, finalURL, "", errUnusablePage
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, bodyLimit))
	if err != nil {
		return nil, nil, "", fmt.Errorf("read %s: %w", finalURL.Redacted(), err)
	}
	return body, finalURL, resp.Header.Get("Content-Type"), nil
}

func parseFetchURL(raw string) (*url.URL, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, fmt.Errorf("%w: website is required", errUnsafeFetchURL)
	}
	if strings.HasPrefix(value, "//") {
		value = "https:" + value
	} else if !strings.Contains(value, "://") {
		value = "https://" + value
	}

	parsed, err := url.Parse(value)
	if err != nil {
		return nil, fmt.Errorf("%w: parse website: %v", errUnsafeFetchURL, err)
	}
	if err := validateFetchURL(parsed); err != nil {
		return nil, err
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Fragment = ""
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	return parsed, nil
}

func validateFetchURL(target *url.URL) error {
	if target == nil {
		return fmt.Errorf("%w: missing url", errUnsafeFetchURL)
	}
	scheme := strings.ToLower(target.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("%w: scheme %q is not allowed", errUnsafeFetchURL, target.Scheme)
	}
	if !target.IsAbs() || target.Host == "" {
		return fmt.Errorf("%w: url must be absolute with a host", errUnsafeFetchURL)
	}
	if target.User != nil {
		return fmt.Errorf("%w: credentials are not allowed", errUnsafeFetchURL)
	}
	if hasInvalidFetchPort(target.Host) {
		return fmt.Errorf("%w: invalid port", errUnsafeFetchURL)
	}

	host := target.Hostname()
	if host == "" || strings.ContainsAny(host, "\x00\r\n\t ") || strings.Contains(host, "%") {
		return fmt.Errorf("%w: invalid host", errUnsafeFetchURL)
	}
	if addr, err := netip.ParseAddr(host); err == nil && !isPublicFetchAddr(addr) {
		return fmt.Errorf("%w: refused private address %s", errUnsafeFetchURL, addr)
	}
	return nil
}

func hasInvalidFetchPort(host string) bool {
	if strings.HasPrefix(host, "[") {
		end := strings.LastIndex(host, "]")
		if end < 0 {
			return true
		}
		tail := host[end+1:]
		if tail == "" {
			return false
		}
		if !strings.HasPrefix(tail, ":") {
			return true
		}
		return !isValidFetchPort(tail[1:])
	}

	colons := strings.Count(host, ":")
	if colons == 0 {
		return false
	}
	if colons > 1 {
		return true
	}
	_, port, _ := strings.Cut(host, ":")
	return !isValidFetchPort(port)
}

func isValidFetchPort(port string) bool {
	if port == "" {
		return false
	}
	n, err := strconv.Atoi(port)
	return err == nil && n > 0 && n <= 65535
}

func originFor(target *url.URL) *url.URL {
	return &url.URL{Scheme: strings.ToLower(target.Scheme), Host: strings.ToLower(target.Host), Path: "/"}
}

func originWithPath(origin *url.URL, p string) *url.URL {
	next := *origin
	next.Path = p
	next.RawQuery = ""
	next.Fragment = ""
	return &next
}

func normalizeSameOriginFetchURL(base *url.URL, raw string, allowSitemap bool) (*url.URL, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false
	}
	ref, err := url.Parse(raw)
	if err != nil {
		return nil, false
	}
	candidate := base.ResolveReference(ref)
	candidate.Fragment = ""
	candidate.Scheme = strings.ToLower(candidate.Scheme)
	candidate.Host = strings.ToLower(candidate.Host)
	if candidate.Path == "" {
		candidate.Path = "/"
	}
	if !sameFetchOrigin(base, candidate) {
		return nil, false
	}
	if err := validateFetchURL(candidate); err != nil {
		return nil, false
	}
	if !allowSitemap && isBlockedFetchPath(candidate.Path) {
		return nil, false
	}
	query := candidate.Query()
	for key := range query {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "utm_") || lower == "fbclid" || lower == "gclid" || lower == "msclkid" {
			query.Del(key)
		}
	}
	candidate.RawQuery = query.Encode()
	return candidate, true
}

func sameFetchOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}

func canonicalFetchURLKey(target *url.URL) string {
	clean := *target
	clean.Scheme = strings.ToLower(clean.Scheme)
	clean.Host = strings.ToLower(clean.Host)
	clean.Fragment = ""
	if clean.Path == "" {
		clean.Path = "/"
	}
	return clean.String()
}

func isBlockedFetchPath(p string) bool {
	switch strings.ToLower(pathExt(p)) {
	case ".7z", ".avi", ".css", ".csv", ".doc", ".docx", ".gif", ".ico", ".jpeg", ".jpg", ".js", ".json", ".mov", ".mp3", ".mp4", ".pdf", ".png", ".svg", ".webp", ".xls", ".xlsx", ".xml", ".zip":
		return true
	default:
		return false
	}
}

func pathExt(p string) string {
	if slash := strings.LastIndex(p, "/"); slash >= 0 {
		p = p[slash+1:]
	}
	if dot := strings.LastIndex(p, "."); dot >= 0 {
		return p[dot:]
	}
	return ""
}

func isLikelySitemapURL(target *url.URL) bool {
	path := strings.ToLower(target.Path)
	return strings.HasSuffix(path, ".xml") && strings.Contains(path, "sitemap")
}

func sortFetchCandidates(candidates []*url.URL) {
	sort.SliceStable(candidates, func(i, j int) bool {
		leftRank := fetchCandidateRank(candidates[i])
		rightRank := fetchCandidateRank(candidates[j])
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		return canonicalFetchURLKey(candidates[i]) < canonicalFetchURLKey(candidates[j])
	})
}

func fetchCandidateRank(target *url.URL) int {
	path := strings.ToLower(target.Path)
	for i, highValue := range fetchSiteHighValuePaths {
		if path == highValue || strings.TrimSuffix(path, "/") == highValue {
			return i
		}
	}
	for _, term := range fetchSiteRelevantPathTerms {
		if strings.Contains(path, term) {
			return len(fetchSiteHighValuePaths)
		}
	}
	return len(fetchSiteHighValuePaths) + 1
}

func isHTMLContentType(contentType string) bool {
	if strings.TrimSpace(contentType) == "" {
		return true
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return strings.Contains(strings.ToLower(contentType), "html")
	}
	switch strings.ToLower(mediaType) {
	case "text/html", "application/xhtml+xml":
		return true
	default:
		return false
	}
}

func isXMLLikeContentType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		lower := strings.ToLower(contentType)
		return strings.Contains(lower, "xml") || strings.Contains(lower, "text/plain")
	}
	mediaType = strings.ToLower(mediaType)
	return strings.Contains(mediaType, "xml") || mediaType == "text/plain"
}

func extractHTMLTextAndNav(body []byte) (string, []string, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return "", nil, err
	}

	var text strings.Builder
	var navHrefs []string
	var walk func(*html.Node, bool, bool)
	walk = func(n *html.Node, skip, inNav bool) {
		if n.Type == html.ElementNode {
			tag := strings.ToLower(n.Data)
			if skipHTMLTag(tag) {
				skip = true
			}
			if tag == "nav" || navigationRole(n) {
				inNav = true
			}
			if inNav && tag == "a" {
				if href := attrValue(n, "href"); href != "" {
					navHrefs = append(navHrefs, href)
				}
			}
		}
		if n.Type == html.TextNode && !skip {
			appendNormalizedText(&text, n.Data)
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child, skip, inNav)
		}
	}
	walk(doc, false, false)
	return strings.TrimSpace(text.String()), navHrefs, nil
}

func skipHTMLTag(tag string) bool {
	switch tag {
	case "script", "style", "svg", "template", "noscript":
		return true
	default:
		return false
	}
}

func navigationRole(n *html.Node) bool {
	return strings.EqualFold(attrValue(n, "role"), "navigation")
}

func attrValue(n *html.Node, key string) string {
	for _, attr := range n.Attr {
		if strings.EqualFold(attr.Key, key) {
			return attr.Val
		}
	}
	return ""
}

func appendNormalizedText(b *strings.Builder, value string) {
	for _, field := range strings.Fields(value) {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(field)
	}
}

func parseSitemapLocs(body []byte) []string {
	decoder := xml.NewDecoder(bytes.NewReader(body))
	var locs []string
	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}
		start, ok := token.(xml.StartElement)
		if !ok || !strings.EqualFold(start.Name.Local, "loc") {
			continue
		}
		var loc string
		if err := decoder.DecodeElement(&loc, &start); err == nil {
			if loc = strings.TrimSpace(loc); loc != "" {
				locs = append(locs, loc)
			}
		}
	}
	return locs
}

type textAccumulator struct {
	b     strings.Builder
	limit int
}

func (a *textAccumulator) addPage(pageURL, text string) bool {
	text = strings.TrimSpace(text)
	if text == "" || !a.hasRoom() {
		return false
	}
	prefix := ""
	if a.b.Len() > 0 {
		prefix = "\n\n"
	}
	header := prefix + "URL: " + pageURL + "\n"
	if a.remaining() <= len(header) {
		return false
	}
	a.writeLimited(header)
	a.writeLimited(text)
	return true
}

func (a *textAccumulator) hasRoom() bool {
	return a.remaining() > 0
}

func (a *textAccumulator) remaining() int {
	return a.limit - a.b.Len()
}

func (a *textAccumulator) writeLimited(value string) {
	if value == "" || a.remaining() <= 0 {
		return
	}
	if len(value) > a.remaining() {
		value = validUTF8Prefix(value, a.remaining())
	}
	a.b.WriteString(value)
}

func (a *textAccumulator) String() string {
	return strings.TrimSpace(a.b.String())
}

func validUTF8Prefix(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
