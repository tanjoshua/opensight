package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	opensightv1 "opensight/internal/gen/opensight/v1"
	"opensight/internal/gen/opensight/v1/opensightv1connect"
	"opensight/internal/store"

	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

// newRPCTest starts a full-stack httptest server for srv and returns a
// cookie-jar-backed Connect client pointed at its /rpc mount, so login/logout
// cookies naturally flow between calls like a real browser.
func newRPCTest(t *testing.T, srv *Server) (*httptest.Server, opensightv1connect.AuthServiceClient) {
	t.Helper()
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("new cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}
	rpcClient := opensightv1connect.NewAuthServiceClient(client, ts.URL+"/rpc")
	return ts, rpcClient
}

func TestRPCSessionLifecycle(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(map[bool]string{false: "insecure", true: "secure"}[secure], func(t *testing.T) {
			f := loginFixture(t, "s3cret-passphrase")
			srv := newTestServer(f)
			srv.secureCookies = secure
			srv.businesses = &fakeBusinessStore{businesses: []store.Business{{
				ID:     mustHashV7(t, businessIDForTest),
				Name:   "Acme Clinic",
				Status: store.BusinessStatusActive,
			}}}
			_, client := newRPCTest(t, srv)
			ctx := context.Background()

			// 1. GetMe unauthenticated.
			_, err := client.GetMe(ctx, connect.NewRequest(&opensightv1.GetMeRequest{}))
			if connect.CodeOf(err) != connect.CodeUnauthenticated {
				t.Fatalf("unauthenticated GetMe code = %v, want Unauthenticated", connect.CodeOf(err))
			}
			var cerr *connect.Error
			if !errors.As(err, &cerr) {
				t.Fatalf("expected *connect.Error, got %T", err)
			}
			setCookies := cerr.Meta().Values("Set-Cookie")
			if !anyContains(setCookies, "Max-Age=0") {
				t.Fatalf("unauthenticated GetMe Meta() Set-Cookie = %v, want a Max-Age=0 clearing cookie", setCookies)
			}

			// 2. Login.
			loginRes, err := client.Login(ctx, connect.NewRequest(&opensightv1.LoginRequest{Email: "user@example.com", Password: "s3cret-passphrase"}))
			if err != nil {
				t.Fatalf("login: %v", err)
			}
			setCookie := loginRes.Header().Get("Set-Cookie")
			if setCookie == "" {
				t.Fatal("login response missing Set-Cookie")
			}
			if !strings.Contains(setCookie, "HttpOnly") {
				t.Errorf("login Set-Cookie missing HttpOnly: %q", setCookie)
			}
			if !strings.Contains(setCookie, "SameSite=Lax") {
				t.Errorf("login Set-Cookie missing SameSite=Lax: %q", setCookie)
			}
			if secure && !strings.Contains(setCookie, "Secure") {
				t.Errorf("login Set-Cookie missing Secure when secureCookies=true: %q", setCookie)
			}
			if !secure && strings.Contains(setCookie, "Secure") {
				t.Errorf("login Set-Cookie has Secure when secureCookies=false: %q", setCookie)
			}
			if loginRes.Msg.GetUser().GetEmail() != "user@example.com" {
				t.Errorf("login user email = %q, want user@example.com", loginRes.Msg.GetUser().GetEmail())
			}
			if loginRes.Msg.GetTenant().GetName() != "Acme Clinic" {
				t.Errorf("login tenant name = %q, want Acme Clinic", loginRes.Msg.GetTenant().GetName())
			}

			// 3. GetMe again — jar replays the cookie.
			meRes, err := client.GetMe(ctx, connect.NewRequest(&opensightv1.GetMeRequest{}))
			if err != nil {
				t.Fatalf("authenticated GetMe: %v", err)
			}
			if len(meRes.Msg.GetBusinesses()) != 1 {
				t.Fatalf("GetMe businesses = %d, want 1", len(meRes.Msg.GetBusinesses()))
			}
			if meRes.Msg.GetPromptLimit() != 20 {
				t.Errorf("GetMe prompt limit = %d, want 20", meRes.Msg.GetPromptLimit())
			}

			// 4. Logout.
			logoutRes, err := client.Logout(ctx, connect.NewRequest(&opensightv1.LogoutRequest{}))
			if err != nil {
				t.Fatalf("logout: %v", err)
			}
			logoutCookie := logoutRes.Header().Get("Set-Cookie")
			if !strings.Contains(logoutCookie, "Max-Age=0") {
				t.Fatalf("logout Set-Cookie = %q, want Max-Age=0", logoutCookie)
			}

			// 5. GetMe again — session is gone.
			_, err = client.GetMe(ctx, connect.NewRequest(&opensightv1.GetMeRequest{}))
			if connect.CodeOf(err) != connect.CodeUnauthenticated {
				t.Fatalf("post-logout GetMe code = %v, want Unauthenticated", connect.CodeOf(err))
			}
		})
	}
}

func anyContains(vals []string, sub string) bool {
	for _, v := range vals {
		if strings.Contains(v, sub) {
			return true
		}
	}
	return false
}

func TestRPCRequiresConnectProtocolHeader(t *testing.T) {
	f := loginFixture(t, "pw")
	srv := newTestServer(f)
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	loginURL := ts.URL + "/rpc" + opensightv1connect.AuthServiceLoginProcedure
	body := `{"email":"user@example.com","password":"pw"}`

	// No Connect-Protocol-Version header: rejected at the protocol layer,
	// before the handler (and thus the interceptor and session-creation) ever
	// runs. This is the load-bearing CSRF assertion.
	req, err := http.NewRequest(http.MethodPost, loginURL, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("no-protocol-header status = %d, want 400; body=%s", resp.StatusCode, b)
	}
	if resp.Header.Get("Set-Cookie") != "" {
		t.Errorf("no-protocol-header response set a cookie: %q", resp.Header.Get("Set-Cookie"))
	}
	if len(f.created) != 0 {
		t.Fatalf("no-protocol-header request created %d sessions, want 0 (handler must not have run)", len(f.created))
	}

	// With the header: succeeds.
	req2, err := http.NewRequest(http.MethodPost, loginURL, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Connect-Protocol-Version", "1")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp2.Body)
		t.Fatalf("with-protocol-header status = %d, want 200; body=%s", resp2.StatusCode, b)
	}
	if resp2.Header.Get("Set-Cookie") == "" {
		t.Fatal("with-protocol-header response did not set a cookie")
	}

	// Bare GET to the Login procedure: no RPC accepts the GET-bypass (none is
	// annotated NO_SIDE_EFFECTS), so it 405s.
	getResp, err := http.Get(loginURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = getResp.Body.Close() }()
	if getResp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET to Login status = %d, want 405", getResp.StatusCode)
	}
	if allow := getResp.Header.Get("Allow"); !strings.Contains(allow, "POST") {
		t.Fatalf("GET to Login Allow header = %q, want to contain POST", allow)
	}

	// Unknown service/procedure path: 404s from the mounted Connect handler,
	// and must not fall through to the SPA handler.
	unknownResp, err := http.Post(ts.URL+"/rpc/opensight.v1.NoSuchService/NoSuchMethod", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unknownResp.Body.Close() }()
	if unknownResp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown procedure status = %d, want 404", unknownResp.StatusCode)
	}
	b, _ := io.ReadAll(unknownResp.Body)
	if bytes.Contains(b, []byte("<title>OpenSight</title>")) {
		t.Fatalf("unknown procedure fell back to the SPA handler: %q", b)
	}
}

func TestRPCLoginFailuresAreUniform(t *testing.T) {
	f := &fakeAuthStore{
		credsByEmail: map[string]store.UserCredentials{
			"real@example.com": {
				UserID:       mustHashV7(t, userID),
				TenantID:     mustHashV7(t, tenantID),
				Email:        "real@example.com",
				TenantName:   "Acme",
				PasswordHash: ptrString(realHash(t, "the-right-password")),
			},
			"nopass@example.com": {
				UserID:       mustHashV7(t, userID),
				TenantID:     mustHashV7(t, tenantID),
				Email:        "nopass@example.com",
				TenantName:   "Acme",
				PasswordHash: nil,
			},
			"bad-hash@example.com": {
				UserID:       mustHashV7(t, userID),
				TenantID:     mustHashV7(t, tenantID),
				Email:        "bad-hash@example.com",
				TenantName:   "Acme",
				PasswordHash: ptrString("not-a-phc-hash"),
			},
		},
	}
	srv := newTestServer(f)
	ctx := context.Background()

	cases := []struct {
		name  string
		email string
		pass  string
	}{
		{"unknown email", "ghost@example.com", "whatever"},
		{"nil password hash", "nopass@example.com", "whatever"},
		{"malformed stored hash", "bad-hash@example.com", "whatever"},
		{"wrong password", "real@example.com", "wrong-password"},
	}

	var errs []error
	for _, tc := range cases {
		_, err := srv.Login(ctx, connect.NewRequest(&opensightv1.LoginRequest{Email: tc.email, Password: tc.pass}))
		if err == nil {
			t.Fatalf("%s: expected error, got nil", tc.name)
		}
		errs = append(errs, err)
	}

	first := errs[0]
	firstCerr := asConnectError(t, first)
	for i, err := range errs[1:] {
		cerr := asConnectError(t, err)
		if connect.CodeOf(err) != connect.CodeOf(first) {
			t.Fatalf("%s: code = %v, want %v", cases[i+1].name, connect.CodeOf(err), connect.CodeOf(first))
		}
		if cerr.Message() != firstCerr.Message() {
			t.Fatalf("%s: message = %q, want %q", cases[i+1].name, cerr.Message(), firstCerr.Message())
		}
		if len(cerr.Meta()) != 0 {
			t.Fatalf("%s: Meta() = %v, want empty", cases[i+1].name, cerr.Meta())
		}
		if len(cerr.Details()) != 0 {
			t.Fatalf("%s: Details() = %v, want empty", cases[i+1].name, cerr.Details())
		}
	}
	if len(firstCerr.Meta()) != 0 {
		t.Fatalf("first error Meta() = %v, want empty", firstCerr.Meta())
	}
	if len(f.created) != 0 {
		t.Fatalf("failed logins created %d sessions, want 0", len(f.created))
	}

	// Raw-HTTP round trip: byte-identical response bodies for unknown-email vs
	// wrong-password, the direct analogue of the REST test's byte-identity
	// assertion.
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)
	unknownBody := doRawLogin(t, ts.URL, "ghost@example.com", "whatever")
	wrongPwBody := doRawLogin(t, ts.URL, "real@example.com", "wrong-password")
	if unknownBody != wrongPwBody {
		t.Fatalf("unknown-email body %q != wrong-password body %q", unknownBody, wrongPwBody)
	}
}

func doRawLogin(t *testing.T, baseURL, email, password string) string {
	t.Helper()
	url := baseURL + "/rpc" + opensightv1connect.AuthServiceLoginProcedure
	body := `{"email":"` + email + `","password":"` + password + `"}`
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func asConnectError(t *testing.T, err error) *connect.Error {
	t.Helper()
	var cerr *connect.Error
	if !errors.As(err, &cerr) {
		t.Fatalf("expected *connect.Error, got %T (%v)", err, err)
	}
	return cerr
}

// TestNoRPCIsSideEffectFree is a cheap, high-value regression guard: no RPC
// in the opensight.v1 schema may ever be annotated
// idempotency_level = NO_SIDE_EFFECTS, because that would let Connect accept
// it as a header-less GET, bypassing WithRequireConnectProtocolHeader's CSRF
// guarantee (design 06 common.proto conventions).
func TestNoRPCIsSideEffectFree(t *testing.T) {
	checked := 0
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if fd.Package() != "opensight.v1" {
			return true
		}
		services := fd.Services()
		for i := 0; i < services.Len(); i++ {
			svc := services.Get(i)
			methods := svc.Methods()
			for j := 0; j < methods.Len(); j++ {
				method := methods.Get(j)
				checked++
				opts, ok := method.Options().(*descriptorpb.MethodOptions)
				if !ok || opts == nil {
					continue
				}
				if opts.GetIdempotencyLevel() == descriptorpb.MethodOptions_NO_SIDE_EFFECTS {
					t.Errorf("%s.%s is annotated NO_SIDE_EFFECTS, which would let Connect accept it as a header-less GET (bypasses the CSRF guard)", svc.FullName(), method.Name())
				}
			}
		}
		return true
	})
	if checked == 0 {
		t.Fatal("no opensight.v1 RPC methods found in protoregistry.GlobalFiles — did the import registering generated types get dropped?")
	}
}
