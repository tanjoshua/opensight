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

	"opensight/internal/visibility"

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
	fetchSiteUserAgent        = "OpenSight/0.1 (+https://opensight.local)"
)

var (
	errUnsafeFetchURL  = errors.New("unsafe fetch url")
	errUnusablePage    = errors.New("unusable page")
	errNoUsableContent = errors.New("no usable site content")
	// errAuthBarrier marks a page the site refused without credentials. It is
	// wrapped into the fetch error so the owned-site scan can report a confirmed
	// authentication barrier without re-requesting the page.
	errAuthBarrier = errors.New("authentication barrier")
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

// FetchSiteInput is the website fetch request.
type FetchSiteInput struct {
	Website string
}

// FetchSiteOutput is the stripped, capped site context used by later onboarding
// proposal activities.
type FetchSiteOutput struct {
	Text string   `json:"text"`
	URLs []string `json:"urls"`
	// NoIndexURLs is the subset of URLs whose page denied indexing through a
	// robots/oai-searchbot meta tag or an X-Robots-Tag header. Onboarding
	// ignores it; the owned-site scan reads it so the indexing verdict needs no
	// second request for markup the fetcher already had in hand.
	NoIndexURLs []string `json:"noindex_urls,omitempty"`
	// HomeNoIndex and HomeAuthBarrier report the same two barriers for the
	// homepage specifically, which is reported even when other pages were
	// retrievable: the entry page is the site's most linked and most cited
	// page, so a barrier there is never incidental.
	HomeNoIndex     bool `json:"home_noindex,omitempty"`
	HomeAuthBarrier bool `json:"home_auth_barrier,omitempty"`
	// Pages carries the structured facts read from each page in URLs order, so
	// the owned-site scan needs no second parse. Onboarding ignores it.
	Pages []visibility.PageFacts `json:"pages,omitempty"`
	// SitemapFound reports that /sitemap.xml answered with usable XML. It is
	// false when the crawl finished before the sitemap was needed, so only the
	// scan — which always reaches for it — should read it.
	SitemapFound bool `json:"sitemap_found,omitempty"`
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
	Facts    visibility.PageFacts
}

// fetchedBody is one retrieved response: the capped body plus the metadata the
// callers need from it.
type fetchedBody struct {
	Body        []byte
	FinalURL    *url.URL
	ContentType string
	Header      http.Header
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
	var urls, noIndexURLs []string
	var pages []visibility.PageFacts
	seen := make(map[string]bool)
	queued := make(map[string]bool)
	var firstTransientErr error

	record := func(page *fetchedPage) {
		if !acc.addPage(page.URL.String(), page.Text) {
			return
		}
		urls = append(urls, page.URL.String())
		pages = append(pages, page.Facts)
		if page.Facts.NoIndex {
			noIndexURLs = append(noIndexURLs, page.URL.String())
		}
	}

	home, err := f.fetchHTMLPage(ctx, startURL, &requests)
	// Some sites publish their real homepage on www but leave the apex host
	// answering 200 with an empty body instead of redirecting. Treat www as the
	// one conventional host alias we can discover without crawling arbitrary
	// subdomains. The alternate still passes the same DNS/IP and redirect SSRF
	// checks as every other request.
	if errors.Is(err, errUnusablePage) && !errors.Is(err, errAuthBarrier) {
		if alternate := wwwFetchURL(startURL); alternate != nil {
			alternateHome, alternateErr := f.fetchHTMLPage(ctx, alternate, &requests)
			switch {
			case alternateErr == nil:
				startURL, home, err = alternate, alternateHome, nil
			case errors.Is(alternateErr, errUnsafeFetchURL):
				return FetchSiteOutput{}, alternateErr
			case !errors.Is(alternateErr, errUnusablePage):
				firstTransientErr = alternateErr
			}
		}
	}
	origin := originFor(startURL)
	homeAuthBarrier := errors.Is(err, errAuthBarrier)
	if err == nil {
		origin = originFor(home.URL)
		seen[canonicalFetchURLKey(home.URL)] = true
		record(home)
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

	if home != nil {
		for _, href := range home.NavHrefs {
			if candidate, ok := normalizeSameOriginFetchURL(home.URL, href, false); ok {
				enqueue(candidate)
			}
		}
	}
	// Real navigation describes this site's actual information architecture and
	// therefore outranks guessed conventional paths. The fallbacks still help a
	// sparse homepage, but cannot spend the crawl budget on branded 404 pages
	// before a linked team or services page is read.
	for _, p := range fetchSiteHighValuePaths {
		enqueue(originWithPath(origin, p))
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
			record(page)
		}
		return nil
	}

	if err := processCandidates(candidates); err != nil {
		return FetchSiteOutput{}, err
	}

	sitemapFound := false
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
			sitemapFound = true
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
		if homeAuthBarrier {
			return FetchSiteOutput{}, fmt.Errorf("%w: %w", errNoUsableContent, errAuthBarrier)
		}
		return FetchSiteOutput{}, errNoUsableContent
	}
	out := FetchSiteOutput{Text: text, URLs: urls, NoIndexURLs: noIndexURLs, Pages: pages, SitemapFound: sitemapFound, HomeAuthBarrier: homeAuthBarrier}
	if home != nil {
		out.HomeNoIndex = home.Facts.NoIndex
	}
	return out, nil
}

func wwwFetchURL(source *url.URL) *url.URL {
	if source == nil {
		return nil
	}
	hostname := strings.ToLower(source.Hostname())
	if hostname == "" || strings.HasPrefix(hostname, "www.") || net.ParseIP(hostname) != nil {
		return nil
	}
	alternate := *source
	alternate.Host = "www." + hostname
	if port := source.Port(); port != "" {
		alternate.Host = net.JoinHostPort("www."+hostname, port)
	}
	return &alternate
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
	res, err := f.fetchURL(ctx, target, "text/html,application/xhtml+xml", f.pageBodyLimit, requests)
	if err != nil {
		return nil, err
	}
	if !isHTMLContentType(res.ContentType) {
		return nil, errUnusablePage
	}

	content, err := extractHTMLContent(res.Body)
	if err != nil {
		return nil, errUnusablePage
	}
	if strings.TrimSpace(content.Text) == "" {
		return nil, errUnusablePage
	}
	facts := content.PageFacts
	facts.URL = res.FinalURL.String()
	facts.NoIndex = facts.NoIndex || headerDeniesIndexing(res.Header)
	facts.JSONLD = boundJSONLD(facts.JSONLD)
	return &fetchedPage{
		URL:      res.FinalURL,
		Text:     content.Text,
		NavHrefs: content.NavHrefs,
		Facts:    facts,
	}, nil
}

// boundJSONLD caps the structured data kept per page. The checks only read the
// declared types and a handful of top-level fields, so an unbounded product
// catalogue would cost artifact size for nothing.
func boundJSONLD(blocks []string) []string {
	const maxBlocks, maxBlockBytes = 5, 16 * 1024
	if len(blocks) > maxBlocks {
		blocks = blocks[:maxBlocks]
	}
	for i, block := range blocks {
		if len(block) > maxBlockBytes {
			blocks[i] = validUTF8Prefix(block, maxBlockBytes)
		}
	}
	return blocks
}

// headerDeniesIndexing reports an X-Robots-Tag response header denying
// indexing.
func headerDeniesIndexing(header http.Header) bool {
	return strings.Contains(strings.ToLower(header.Get("X-Robots-Tag")), "noindex")
}

func (f *siteFetcher) fetchSitemap(ctx context.Context, origin, target *url.URL, requests *int, depth int, firstTransientErr *error) ([]*url.URL, error) {
	if depth > 1 {
		return nil, nil
	}
	res, err := f.fetchURL(ctx, target, "application/xml,text/xml,*/*", f.sitemapBodyLimit, requests)
	if err != nil {
		recordFetchTransient(firstTransientErr, err)
		return nil, err
	}
	if res.ContentType != "" && !isXMLLikeContentType(res.ContentType) {
		return nil, nil
	}

	locs := parseSitemapLocs(res.Body)
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

func (f *siteFetcher) fetchURL(ctx context.Context, target *url.URL, accept string, bodyLimit int64, requests *int) (fetchedBody, error) {
	if requests != nil {
		if *requests >= f.maxRequests {
			return fetchedBody{}, errUnusablePage
		}
		*requests++
	}
	if err := validateFetchURL(target); err != nil {
		return fetchedBody{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return fetchedBody{}, fmt.Errorf("%w: build request: %v", errUnsafeFetchURL, err)
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", fetchSiteUserAgent)

	resp, err := f.client.Do(req)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return fetchedBody{}, fmt.Errorf("fetch %s: %w", target.Redacted(), err)
	}
	defer func() { _ = resp.Body.Close() }()

	finalURL := resp.Request.URL
	if err := validateFetchURL(finalURL); err != nil {
		return fetchedBody{}, err
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fetchedBody{}, fmt.Errorf("%w: %w", errUnusablePage, errAuthBarrier)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode >= 500 {
			return fetchedBody{}, fmt.Errorf("fetch %s: status %d", finalURL.Redacted(), resp.StatusCode)
		}
		return fetchedBody{}, errUnusablePage
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, bodyLimit))
	if err != nil {
		return fetchedBody{}, fmt.Errorf("read %s: %w", finalURL.Redacted(), err)
	}
	return fetchedBody{Body: body, FinalURL: finalURL, ContentType: resp.Header.Get("Content-Type"), Header: resp.Header}, nil
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

// htmlContent is everything one parse of a page yields: its stripped text, its
// navigation links, and the page facts later checks are derived from. Every
// field is read in the single walk below so no caller has to fetch the markup a
// second time.
type htmlContent struct {
	Text     string
	NavHrefs []string
	visibility.PageFacts
}

func extractHTMLContent(body []byte) (htmlContent, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return htmlContent{}, err
	}

	var text strings.Builder
	var title, h1 strings.Builder
	out := htmlContent{}
	var walk func(*html.Node, bool, bool)
	walk = func(n *html.Node, skip, inNav bool) {
		if n.Type == html.ElementNode {
			tag := strings.ToLower(n.Data)
			// The JSON-LD payload is the content of a script element, so it has to
			// be taken before skipHTMLTag stops the walk descending into it.
			if tag == "script" && isJSONLDType(attrValue(n, "type")) {
				if raw := nodeText(n); strings.TrimSpace(raw) != "" {
					out.JSONLD = append(out.JSONLD, raw)
				}
			}
			if skipHTMLTag(tag) {
				skip = true
			}
			if tag == "nav" || navigationRole(n) {
				inNav = true
			}
			if tag == "a" {
				href := attrValue(n, "href")
				if inNav && href != "" {
					out.NavHrefs = append(out.NavHrefs, href)
				}
				if strings.HasPrefix(strings.ToLower(strings.TrimSpace(href)), "tel:") {
					out.HasTelLink = true
				}
			}
			switch tag {
			case "title":
				appendNormalizedText(&title, nodeText(n))
			case "h1":
				if h1.Len() == 0 {
					appendNormalizedText(&h1, nodeText(n))
				}
			case "meta":
				if metaDeniesIndexing(n) {
					out.NoIndex = true
				}
				if strings.EqualFold(strings.TrimSpace(attrValue(n, "name")), "description") {
					out.MetaDescription = strings.TrimSpace(attrValue(n, "content"))
				}
			case "link":
				if hasLinkRel(n, "canonical") {
					out.Canonical = strings.TrimSpace(attrValue(n, "href"))
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
	out.Text = strings.TrimSpace(text.String())
	out.Title = strings.TrimSpace(title.String())
	out.H1 = strings.TrimSpace(h1.String())
	return out, nil
}

// nodeText concatenates the text directly under a node. Titles, headings and
// JSON-LD blocks are all shallow, so this needs no depth budget.
func nodeText(n *html.Node) string {
	var b strings.Builder
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		switch child.Type {
		case html.TextNode:
			b.WriteString(child.Data)
		case html.ElementNode:
			b.WriteString(nodeText(child))
		}
	}
	return b.String()
}

func isJSONLDType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(value))
	if err != nil {
		return false
	}
	return strings.EqualFold(mediaType, "application/ld+json")
}

// hasLinkRel reports a rel attribute containing the token, since rel is a
// space-separated list.
func hasLinkRel(n *html.Node, token string) bool {
	for _, field := range strings.Fields(strings.ToLower(attrValue(n, "rel"))) {
		if field == token {
			return true
		}
	}
	return false
}

// metaDeniesIndexing reports a robots or OAI-SearchBot meta tag carrying a
// noindex directive.
func metaDeniesIndexing(n *html.Node) bool {
	switch strings.ToLower(strings.TrimSpace(attrValue(n, "name"))) {
	case "robots", "oai-searchbot":
		return strings.Contains(strings.ToLower(attrValue(n, "content")), "noindex")
	default:
		return false
	}
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
	if strings.EqualFold(attrValue(n, "role"), "navigation") {
		return true
	}
	identity := strings.ToLower(attrValue(n, "id") + " " + attrValue(n, "class"))
	return strings.Contains(identity, "navigation") || strings.Contains(identity, "main-menu") || strings.Contains(identity, "main_menu")
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
