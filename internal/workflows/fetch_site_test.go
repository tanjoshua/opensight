package workflows

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"go.temporal.io/sdk/temporal"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type staticIPResolver struct {
	ips []net.IPAddr
	err error
}

func (r staticIPResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return r.ips, r.err
}

type closeIdleRoundTripper struct {
	closed bool
}

func (rt *closeIdleRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("not used")
}

func (rt *closeIdleRoundTripper) CloseIdleConnections() {
	rt.closed = true
}

func testFetchHTTPClient(rt http.RoundTripper) *http.Client {
	return &http.Client{
		Transport:     rt,
		CheckRedirect: safeFetchRedirect,
	}
}

func testFetchResponse(req *http.Request, status int, contentType, body string) *http.Response {
	resp := &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
	if contentType != "" {
		resp.Header.Set("Content-Type", contentType)
	}
	return resp
}

func TestSiteFetcherDiscoversHighValuePages(t *testing.T) {
	pages := map[string]struct {
		status      int
		contentType string
		body        string
	}{
		"http://example.com/": {
			status:      http.StatusOK,
			contentType: "text/html; charset=utf-8",
			body: `<html><head><style>.hidden{}</style><script>hidden script</script></head>
<body><nav>
<a href="/services?utm_source=newsletter">Services</a>
<a href="/team">Team</a>
<a href="https://elsewhere.example/contact">External</a>
</nav><main>Example Clinic homepage text</main></body></html>`,
		},
		"http://example.com/about": {
			status:      http.StatusOK,
			contentType: "text/html",
			body:        `<html><body><main>About Example Clinic and its approach.</main></body></html>`,
		},
		"http://example.com/services": {
			status:      http.StatusOK,
			contentType: "text/html",
			body:        `<html><body><main>Physiotherapy and sports injury services.</main></body></html>`,
		},
		"http://example.com/team": {
			status:      http.StatusOK,
			contentType: "text/html",
			body:        `<html><body><main>Dr Lee and the care team.</main></body></html>`,
		},
		"http://example.com/doctors": {
			status:      http.StatusNotFound,
			contentType: "text/html",
			body:        `missing`,
		},
		"http://example.com/contact": {
			status:      http.StatusOK,
			contentType: "text/html",
			body:        `<html><body><main>Contact us in Novena, Singapore.</main></body></html>`,
		},
		"http://example.com/sitemap.xml": {
			status:      http.StatusOK,
			contentType: "application/xml",
			body: `<urlset>
<url><loc>http://example.com/contact</loc></url>
<url><loc>http://example.com/blog/post</loc></url>
<url><loc>https://elsewhere.example/about</loc></url>
</urlset>`,
		},
	}

	var visited []string
	fetcher := &siteFetcher{
		client: testFetchHTTPClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
			visited = append(visited, req.URL.String())
			page, ok := pages[req.URL.String()]
			if !ok {
				return testFetchResponse(req, http.StatusNotFound, "text/html", ""), nil
			}
			return testFetchResponse(req, page.status, page.contentType, page.body), nil
		})),
		textLimit:        fetchSiteTextLimit,
		pageBodyLimit:    fetchSitePageBodyLimit,
		sitemapBodyLimit: fetchSiteSitemapBodyLimit,
		maxRequests:      fetchSiteMaxRequests,
	}

	out, err := fetcher.Fetch(context.Background(), FetchSiteInput{Website: "http://example.com"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	for _, want := range []string{
		"http://example.com/",
		"http://example.com/about",
		"http://example.com/services",
		"http://example.com/team",
		"http://example.com/contact",
	} {
		if !containsString(out.URLs, want) {
			t.Fatalf("URLs = %#v, missing %s", out.URLs, want)
		}
	}
	if containsString(visited, "https://elsewhere.example/contact") || containsString(visited, "https://elsewhere.example/about") {
		t.Fatalf("visited external URL: %#v", visited)
	}
	if !containsString(visited, "http://example.com/sitemap.xml") {
		t.Fatalf("visited = %#v, want sitemap.xml fetched", visited)
	}
	if strings.Contains(out.Text, "hidden script") || strings.Contains(out.Text, ".hidden") {
		t.Fatalf("extracted text includes script/style content: %q", out.Text)
	}
	for _, want := range []string{"Example Clinic homepage text", "Physiotherapy", "Dr Lee", "Novena"} {
		if !strings.Contains(out.Text, want) {
			t.Fatalf("text missing %q: %q", want, out.Text)
		}
	}
}

func TestSiteFetcherFallsBackToWWWWhenApexHasNoContent(t *testing.T) {
	var visited []string
	fetcher := &siteFetcher{
		client: testFetchHTTPClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
			visited = append(visited, req.URL.String())
			switch req.URL.String() {
			case "https://example.com/":
				return testFetchResponse(req, http.StatusOK, "text/html", "\n"), nil
			case "https://www.example.com/":
				return testFetchResponse(req, http.StatusOK, "text/html", "<main>Example Clinic on www</main>"), nil
			default:
				return testFetchResponse(req, http.StatusNotFound, "text/html", "missing"), nil
			}
		})),
		textLimit:        fetchSiteTextLimit,
		pageBodyLimit:    fetchSitePageBodyLimit,
		sitemapBodyLimit: fetchSiteSitemapBodyLimit,
		maxRequests:      fetchSiteMaxRequests,
	}

	out, err := fetcher.Fetch(context.Background(), FetchSiteInput{Website: "https://example.com"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(out.URLs) == 0 || out.URLs[0] != "https://www.example.com/" {
		t.Fatalf("URLs = %#v, want www homepage", out.URLs)
	}
	if len(visited) < 2 || visited[0] != "https://example.com/" || visited[1] != "https://www.example.com/" {
		t.Fatalf("visited = %#v, want apex then www", visited)
	}
}

func TestSiteFetcherReadsNavigationBeforeGuessedPaths(t *testing.T) {
	var visited []string
	fetcher := &siteFetcher{
		client: testFetchHTTPClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
			visited = append(visited, req.URL.Path)
			switch req.URL.Path {
			case "/":
				return testFetchResponse(req, http.StatusOK, "text/html", `<div id="topnavigation"><a href="/practice/our-team/">Our team</a></div><main>Clinic</main>`), nil
			case "/practice/our-team/":
				return testFetchResponse(req, http.StatusOK, "text/html", `<main>Dr Lee is a registered specialist.</main>`), nil
			default:
				return testFetchResponse(req, http.StatusOK, "text/html", `<main>Page not found</main>`), nil
			}
		})),
		textLimit: fetchSiteTextLimit, pageBodyLimit: fetchSitePageBodyLimit,
		sitemapBodyLimit: fetchSiteSitemapBodyLimit, maxRequests: 2,
	}

	out, err := fetcher.Fetch(context.Background(), FetchSiteInput{Website: "http://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	// The budget of 2 buys the homepage and the linked team page; the third visit
	// is the budget-exempt sitemap probe, not a guessed path.
	if len(visited) != 3 || visited[1] != "/practice/our-team/" || visited[2] != "/sitemap.xml" || !strings.Contains(out.Text, "registered specialist") {
		t.Fatalf("visited=%v text=%q, want linked team page before guessed paths", visited, out.Text)
	}
}

// The owned-site scan fails sitemap_published from SitemapFound alone, so a site
// whose pages spend the whole request budget must still have its sitemap probed:
// "we stopped looking" is not evidence that no sitemap exists.
func TestSiteFetcherProbesSitemapAfterRequestBudgetIsSpent(t *testing.T) {
	var visited []string
	fetcher := &siteFetcher{
		client: testFetchHTTPClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
			visited = append(visited, req.URL.Path)
			switch req.URL.Path {
			case "/":
				return testFetchResponse(req, http.StatusOK, "text/html", `<nav><a href="/about">About</a></nav><main>Clinic</main>`), nil
			case "/sitemap.xml":
				return testFetchResponse(req, http.StatusOK, "application/xml",
					`<urlset><url><loc>http://example.com/team</loc></url></urlset>`), nil
			default:
				return testFetchResponse(req, http.StatusOK, "text/html", `<main>About the clinic</main>`), nil
			}
		})),
		textLimit: fetchSiteTextLimit, pageBodyLimit: fetchSitePageBodyLimit,
		sitemapBodyLimit: fetchSiteSitemapBodyLimit, maxRequests: 2,
	}

	out, err := fetcher.Fetch(context.Background(), FetchSiteInput{Website: "http://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if !out.SitemapFound {
		t.Fatalf("SitemapFound = false, want the sitemap probed; visited=%v", visited)
	}
	// The exemption buys the probe itself and nothing else: the page it lists is
	// out of budget, so the crawl stops at three requests.
	if len(visited) != 3 {
		t.Fatalf("visited=%v, want exactly one request past the budget of 2", visited)
	}
}

// Now that the probe runs on every crawl, an SPA catch-all answering 200 with
// the app shell at /sitemap.xml reaches it on every site rather than only the
// small ones. A non-XML answer is no sitemap, and must not pass the check.
func TestSiteFetcherRejectsNonXMLSitemap(t *testing.T) {
	fetcher := &siteFetcher{
		client: testFetchHTTPClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return testFetchResponse(req, http.StatusOK, "text/html", `<main>Clinic</main>`), nil
		})),
		textLimit: fetchSiteTextLimit, pageBodyLimit: fetchSitePageBodyLimit,
		sitemapBodyLimit: fetchSiteSitemapBodyLimit, maxRequests: fetchSiteMaxRequests,
	}

	out, err := fetcher.Fetch(context.Background(), FetchSiteInput{Website: "http://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if out.SitemapFound {
		t.Fatal("SitemapFound = true for an HTML response, want no sitemap")
	}
}

func TestFetchSiteRefusesPrivateAddresses(t *testing.T) {
	literals := []string{
		"http://127.0.0.1/",
		"http://10.0.0.1/",
		"http://169.254.169.254/latest/meta-data",
		"http://100.64.0.1/",
		"http://[::1]/",
		"http://[64:ff9b::a9fe:a9fe]/",
		"http://[fc00::1]/",
		"http://[fec0::1]/",
	}
	for _, raw := range literals {
		t.Run(raw, func(t *testing.T) {
			if _, err := parseFetchURL(raw); !errors.Is(err, errUnsafeFetchURL) {
				t.Fatalf("parseFetchURL(%q) error = %v, want unsafe URL", raw, err)
			}
		})
	}

	resolver := staticIPResolver{ips: []net.IPAddr{{IP: net.ParseIP("192.168.1.10")}}}
	if _, err := resolveFetchHost(context.Background(), resolver, "clinic.example"); !errors.Is(err, errUnsafeFetchURL) {
		t.Fatalf("resolve private host error = %v, want unsafe URL", err)
	}
	resolver = staticIPResolver{ips: []net.IPAddr{{IP: net.ParseIP("fec0::1")}}}
	if _, err := resolveFetchHost(context.Background(), resolver, "clinic.example"); !errors.Is(err, errUnsafeFetchURL) {
		t.Fatalf("resolve site-local host error = %v, want unsafe URL", err)
	}
}

func TestSiteFetcherRefusesRedirectToPrivateAddress(t *testing.T) {
	for _, location := range []string{
		"http://169.254.169.254/latest/meta-data",
		"http://[64:ff9b::a9fe:a9fe]/latest/meta-data",
	} {
		t.Run(location, func(t *testing.T) {
			var visited []string
			fetcher := &siteFetcher{
				client: testFetchHTTPClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
					visited = append(visited, req.URL.String())
					if req.URL.Host != "example.com" {
						return nil, fmt.Errorf("private redirect target was requested: %s", req.URL.String())
					}
					resp := testFetchResponse(req, http.StatusFound, "text/html", "")
					resp.Header.Set("Location", location)
					return resp, nil
				})),
				textLimit:        fetchSiteTextLimit,
				pageBodyLimit:    fetchSitePageBodyLimit,
				sitemapBodyLimit: fetchSiteSitemapBodyLimit,
				maxRequests:      fetchSiteMaxRequests,
			}

			_, err := fetcher.Fetch(context.Background(), FetchSiteInput{Website: "http://example.com"})
			if !errors.Is(err, errUnsafeFetchURL) {
				t.Fatalf("Fetch error = %v, want unsafe URL", err)
			}
			if got := len(visited); got != 1 {
				t.Fatalf("visited %d URLs (%#v), want only the public redirect source", got, visited)
			}
		})
	}
}

func TestFetchSiteClassifiesBadWebsiteAsNonRetryable(t *testing.T) {
	_, err := (&Activities{}).FetchSite(context.Background(), FetchSiteInput{Website: "http://127.0.0.1"})
	if err == nil {
		t.Fatal("FetchSite returned nil, want non-retryable application error")
	}
	var appErr *temporal.ApplicationError
	if !errors.As(err, &appErr) {
		t.Fatalf("error type = %T, want *temporal.ApplicationError", err)
	}
	if !appErr.NonRetryable() {
		t.Fatalf("NonRetryable = false, want true")
	}
}

func TestSiteFetcherPreservesTransientFailureWhenNoPageSucceeds(t *testing.T) {
	fetcher := &siteFetcher{
		client: testFetchHTTPClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/", "/services", "/team", "/doctors", "/contact", "/sitemap.xml":
				return testFetchResponse(req, http.StatusNotFound, "text/html", "missing"), nil
			case "/about":
				return testFetchResponse(req, http.StatusServiceUnavailable, "text/html", "try later"), nil
			default:
				t.Fatalf("unexpected request: %s", req.URL.String())
				return nil, nil
			}
		})),
		textLimit:        fetchSiteTextLimit,
		pageBodyLimit:    fetchSitePageBodyLimit,
		sitemapBodyLimit: fetchSiteSitemapBodyLimit,
		maxRequests:      fetchSiteMaxRequests,
	}

	_, err := fetcher.Fetch(context.Background(), FetchSiteInput{Website: "http://example.com"})
	if err == nil {
		t.Fatal("Fetch returned nil, want transient 503 error")
	}
	if errors.Is(err, errNoUsableContent) {
		t.Fatalf("Fetch error = %v, want preserved transient error", err)
	}
	if !strings.Contains(err.Error(), "status 503") {
		t.Fatalf("Fetch error = %v, want status 503", err)
	}
}

func TestSiteFetcherPreservesChildSitemapTransientFailure(t *testing.T) {
	fetcher := &siteFetcher{
		client: testFetchHTTPClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/", "/about", "/services", "/team", "/doctors", "/contact":
				return testFetchResponse(req, http.StatusNotFound, "text/html", "missing"), nil
			case "/sitemap.xml":
				return testFetchResponse(req, http.StatusOK, "application/xml", `<sitemapindex>
<sitemap><loc>http://example.com/sitemap-a.xml</loc></sitemap>
</sitemapindex>`), nil
			case "/sitemap-a.xml":
				return testFetchResponse(req, http.StatusServiceUnavailable, "application/xml", "try later"), nil
			default:
				t.Fatalf("unexpected request: %s", req.URL.String())
				return nil, nil
			}
		})),
		textLimit:        fetchSiteTextLimit,
		pageBodyLimit:    fetchSitePageBodyLimit,
		sitemapBodyLimit: fetchSiteSitemapBodyLimit,
		maxRequests:      fetchSiteMaxRequests,
	}

	_, err := fetcher.Fetch(context.Background(), FetchSiteInput{Website: "http://example.com"})
	if err == nil {
		t.Fatal("Fetch returned nil, want child sitemap transient error")
	}
	if errors.Is(err, errNoUsableContent) {
		t.Fatalf("Fetch error = %v, want preserved child sitemap transient error", err)
	}
	if !strings.Contains(err.Error(), "status 503") {
		t.Fatalf("Fetch error = %v, want status 503", err)
	}
}

func TestSiteFetcherKeepsChildSitemapTransientUntilPageTextSucceeds(t *testing.T) {
	fetcher := &siteFetcher{
		client: testFetchHTTPClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/", "/about", "/services", "/team", "/doctors", "/contact":
				return testFetchResponse(req, http.StatusNotFound, "text/html", "missing"), nil
			case "/sitemap.xml":
				return testFetchResponse(req, http.StatusOK, "application/xml", `<sitemapindex>
<sitemap><loc>http://example.com/sitemap-a.xml</loc></sitemap>
<sitemap><loc>http://example.com/sitemap-b.xml</loc></sitemap>
</sitemapindex>`), nil
			case "/sitemap-a.xml":
				return testFetchResponse(req, http.StatusServiceUnavailable, "application/xml", "try later"), nil
			case "/sitemap-b.xml":
				return testFetchResponse(req, http.StatusOK, "application/xml", `<urlset>
<url><loc>http://example.com/dead-page</loc></url>
</urlset>`), nil
			case "/dead-page":
				return testFetchResponse(req, http.StatusNotFound, "text/html", "missing"), nil
			default:
				t.Fatalf("unexpected request: %s", req.URL.String())
				return nil, nil
			}
		})),
		textLimit:        fetchSiteTextLimit,
		pageBodyLimit:    fetchSitePageBodyLimit,
		sitemapBodyLimit: fetchSiteSitemapBodyLimit,
		maxRequests:      fetchSiteMaxRequests,
	}

	_, err := fetcher.Fetch(context.Background(), FetchSiteInput{Website: "http://example.com"})
	if err == nil {
		t.Fatal("Fetch returned nil, want preserved child sitemap transient error")
	}
	if errors.Is(err, errNoUsableContent) {
		t.Fatalf("Fetch error = %v, want child sitemap transient preserved through dead candidate page", err)
	}
	if !strings.Contains(err.Error(), "status 503") {
		t.Fatalf("Fetch error = %v, want status 503", err)
	}
}

func TestSiteFetcherFetchesHighValuePathsBeforeSitemapIndexes(t *testing.T) {
	var visited []string
	fetcher := &siteFetcher{
		client: testFetchHTTPClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
			visited = append(visited, req.URL.String())
			switch req.URL.Path {
			case "/":
				return testFetchResponse(req, http.StatusOK, "text/html", "<html><body>Home</body></html>"), nil
			case "/about", "/services", "/team", "/doctors", "/contact":
				return testFetchResponse(req, http.StatusOK, "text/html", "<html><body>"+req.URL.Path+" text</body></html>"), nil
			case "/sitemap.xml":
				return testFetchResponse(req, http.StatusOK, "application/xml", `<sitemapindex>
<sitemap><loc>http://example.com/sitemap-a.xml</loc></sitemap>
<sitemap><loc>http://example.com/sitemap-b.xml</loc></sitemap>
<sitemap><loc>http://example.com/sitemap-c.xml</loc></sitemap>
</sitemapindex>`), nil
			default:
				return testFetchResponse(req, http.StatusOK, "application/xml", `<urlset></urlset>`), nil
			}
		})),
		textLimit:        fetchSiteTextLimit,
		pageBodyLimit:    fetchSitePageBodyLimit,
		sitemapBodyLimit: fetchSiteSitemapBodyLimit,
		maxRequests:      7,
	}

	out, err := fetcher.Fetch(context.Background(), FetchSiteInput{Website: "http://example.com"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	for _, want := range []string{
		"http://example.com/about",
		"http://example.com/services",
		"http://example.com/team",
		"http://example.com/doctors",
		"http://example.com/contact",
	} {
		if !containsString(out.URLs, want) {
			t.Fatalf("URLs = %#v, missing required high-value page %s", out.URLs, want)
		}
	}
	if containsString(visited, "http://example.com/sitemap-a.xml") {
		t.Fatalf("visited child sitemap before required pages: %#v", visited)
	}
}

func TestSiteFetcherPreservesBudgetForIndexedSitemapPage(t *testing.T) {
	var childSitemaps strings.Builder
	for i := 0; i < 13; i++ {
		fmt.Fprintf(&childSitemaps, "<sitemap><loc>http://example.com/sitemap-%02d.xml</loc></sitemap>", i)
	}

	fetcher := &siteFetcher{
		client: testFetchHTTPClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/":
				return testFetchResponse(req, http.StatusOK, "text/html", "<html><body>Home</body></html>"), nil
			case "/about", "/services", "/team", "/doctors", "/contact":
				return testFetchResponse(req, http.StatusOK, "text/html", "<html><body>"+req.URL.Path+" text</body></html>"), nil
			case "/sitemap.xml":
				return testFetchResponse(req, http.StatusOK, "application/xml", "<sitemapindex>"+childSitemaps.String()+"</sitemapindex>"), nil
			case "/sitemap-00.xml":
				return testFetchResponse(req, http.StatusOK, "application/xml", `<urlset>
<url><loc>http://example.com/indexed-service</loc></url>
</urlset>`), nil
			case "/indexed-service":
				return testFetchResponse(req, http.StatusOK, "text/html", "<html><body>Indexed service page text</body></html>"), nil
			default:
				return testFetchResponse(req, http.StatusOK, "application/xml", `<urlset></urlset>`), nil
			}
		})),
		textLimit:        fetchSiteTextLimit,
		pageBodyLimit:    fetchSitePageBodyLimit,
		sitemapBodyLimit: fetchSiteSitemapBodyLimit,
		maxRequests:      fetchSiteMaxRequests,
	}

	out, err := fetcher.Fetch(context.Background(), FetchSiteInput{Website: "http://example.com"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !containsString(out.URLs, "http://example.com/indexed-service") {
		t.Fatalf("URLs = %#v, want indexed sitemap page fetched", out.URLs)
	}
	if !strings.Contains(out.Text, "Indexed service page text") {
		t.Fatalf("text missing indexed page content: %q", out.Text)
	}
}

func TestSiteFetcherClosesIdleConnections(t *testing.T) {
	transport := &closeIdleRoundTripper{}
	(&siteFetcher{client: &http.Client{Transport: transport}}).closeIdleConnections()
	if !transport.closed {
		t.Fatal("CloseIdleConnections was not forwarded to the transport")
	}
}

func TestSafeFetchDialContextFallsBackAcrossApprovedAddresses(t *testing.T) {
	resolver := staticIPResolver{ips: []net.IPAddr{
		{IP: net.ParseIP("93.184.216.34")},
		{IP: net.ParseIP("93.184.216.35")},
	}}
	var attempts []string
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})

	dial := safeFetchDialContext(resolver, func(_ context.Context, _ string, address string) (net.Conn, error) {
		attempts = append(attempts, address)
		if strings.Contains(address, "93.184.216.34") {
			return nil, errors.New("first address down")
		}
		return clientConn, nil
	})

	conn, err := dial(context.Background(), "tcp", net.JoinHostPort("clinic.example", "443"))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if conn != clientConn {
		t.Fatalf("conn = %v, want fallback connection", conn)
	}
	if len(attempts) != 2 {
		t.Fatalf("attempts = %#v, want first failed then fallback", attempts)
	}
}

func TestSiteFetcherCapsOutputText(t *testing.T) {
	hugeText := strings.Repeat("orthopaedic clinic singapore ", fetchSiteTextLimit)
	fetcher := &siteFetcher{
		client: testFetchHTTPClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path == "/sitemap.xml" {
				return testFetchResponse(req, http.StatusNotFound, "", ""), nil
			}
			if req.URL.Path != "/" {
				t.Fatalf("unexpected request after text cap was full: %s", req.URL.String())
			}
			return testFetchResponse(req, http.StatusOK, "text/html", "<html><body>"+hugeText+"</body></html>"), nil
		})),
		textLimit:        fetchSiteTextLimit,
		pageBodyLimit:    fetchSitePageBodyLimit,
		sitemapBodyLimit: fetchSiteSitemapBodyLimit,
		maxRequests:      fetchSiteMaxRequests,
	}

	out, err := fetcher.Fetch(context.Background(), FetchSiteInput{Website: "http://example.com"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(out.Text) > fetchSiteTextLimit {
		t.Fatalf("text length = %d, want <= %d", len(out.Text), fetchSiteTextLimit)
	}
	if len(out.URLs) != 1 || out.URLs[0] != "http://example.com/" {
		t.Fatalf("URLs = %#v, want only capped homepage", out.URLs)
	}
}

func TestSiteFetcherCapsResponseBody(t *testing.T) {
	body := "<html><body>visible text " + strings.Repeat("x", 2048) + " tail marker</body></html>"
	fetcher := &siteFetcher{
		client: testFetchHTTPClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path == "/sitemap.xml" {
				return testFetchResponse(req, http.StatusNotFound, "", ""), nil
			}
			if req.URL.Path != "/" {
				t.Fatalf("unexpected request: %s", req.URL.String())
			}
			return testFetchResponse(req, http.StatusOK, "text/html", body), nil
		})),
		textLimit:        fetchSiteTextLimit,
		pageBodyLimit:    int64(strings.Index(body, "tail marker")),
		sitemapBodyLimit: fetchSiteSitemapBodyLimit,
		maxRequests:      1,
	}

	out, err := fetcher.Fetch(context.Background(), FetchSiteInput{Website: "http://example.com"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if strings.Contains(out.Text, "tail marker") {
		t.Fatalf("text includes content beyond response body cap: %q", out.Text)
	}
}

func TestExtractHTMLContentReadsNoIndexDirective(t *testing.T) {
	tests := []struct {
		name, body string
		noIndex    bool
	}{
		{name: "robots noindex", body: `<html><head><meta content='noindex,follow' name='robots'></head><body>text</body></html>`, noIndex: true},
		{name: "oai-searchbot noindex", body: `<html><head><meta content='noindex' name='OAI-SearchBot'></head><body>text</body></html>`, noIndex: true},
		{name: "indexable", body: `<html><head><meta content='index,follow' name='robots'></head><body>text</body></html>`},
		{name: "unrelated meta", body: `<html><head><meta content='noindex' name='generator'></head><body>text</body></html>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content, err := extractHTMLContent([]byte(tt.body))
			if err != nil {
				t.Fatalf("extractHTMLContent: %v", err)
			}
			if content.NoIndex != tt.noIndex {
				t.Fatalf("NoIndex = %v, want %v", content.NoIndex, tt.noIndex)
			}
			if content.Text != "text" {
				t.Fatalf("Text = %q, want %q", content.Text, "text")
			}
		})
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
