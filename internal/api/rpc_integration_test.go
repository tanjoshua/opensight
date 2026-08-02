package api

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"opensight/internal/billing"
	"opensight/internal/domain"
	opensightv1 "opensight/internal/gen/opensight/v1"
	"opensight/internal/gen/opensight/v1/opensightv1connect"
	"opensight/internal/store"

	connect "connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func openAPITestDB(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	dbURL := os.Getenv("OPENSIGHT_STORE_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("set OPENSIGHT_STORE_TEST_DATABASE_URL to run API integration tests")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(db.Close)
	return db, ctx
}

func TestRPCSessionLifecycleAgainstPostgres(t *testing.T) {
	db, ctx := openAPITestDB(t)
	repository := store.New(db)
	srv := New(Deps{Store: repository})
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("new cookie jar: %v", err)
	}
	client := opensightv1connect.NewAuthServiceClient(&http.Client{Jar: jar}, ts.URL+"/rpc")
	email := "api-" + mustDomainID(t).String() + "@example.com"

	signup, err := client.Signup(ctx, connect.NewRequest(&opensightv1.SignupRequest{
		Email: email, Password: "correct-horse-battery",
	}))
	if err != nil {
		t.Fatalf("Signup: %v", err)
	}
	tenantID, err := uuid.Parse(signup.Msg.GetTenant().GetId())
	if err != nil {
		t.Fatalf("parse tenant id: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenantID) })

	me, err := client.GetMe(ctx, connect.NewRequest(&opensightv1.GetMeRequest{}))
	if err != nil {
		t.Fatalf("GetMe after signup: %v", err)
	}
	if me.Msg.GetUser().GetEmail() != email || me.Msg.GetPlan().GetCode() != billing.Starter.Code ||
		me.Msg.GetAccess() != opensightv1.Access_ACCESS_NEVER {
		t.Fatalf("GetMe = %+v, want signed-up unpaid starter account", me.Msg)
	}

	if _, err := client.Logout(ctx, connect.NewRequest(&opensightv1.LogoutRequest{})); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := client.GetMe(ctx, connect.NewRequest(&opensightv1.GetMeRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("GetMe after logout code = %v, want Unauthenticated", connect.CodeOf(err))
	}
}

func TestBusinessRPCUsesConcreteStore(t *testing.T) {
	db, ctx := openAPITestDB(t)
	tenantID := mustDomainID(t)
	otherTenantID := mustDomainID(t)
	t.Cleanup(func() {
		_, _ = db.Exec(ctx, "DELETE FROM tenants WHERE id = ANY($1)", []domain.ID{tenantID, otherTenantID})
	})
	for id, name := range map[domain.ID]string{tenantID: "API Tenant", otherTenantID: "Other Tenant"} {
		if _, err := db.Exec(ctx, "INSERT INTO tenants (id, name) VALUES ($1, $2)", id, name); err != nil {
			t.Fatalf("insert tenant: %v", err)
		}
		if _, err := db.Exec(ctx, "INSERT INTO subscriptions (tenant_id, plan_code, comped) VALUES ($1, $2, true)", id, billing.Starter.Code); err != nil {
			t.Fatalf("insert subscription: %v", err)
		}
	}

	temporal := &fakeTemporalClient{}
	repository := store.New(db)
	srv := New(Deps{Store: repository, Temporal: temporal, TemporalTaskQueue: "api-integration"})
	session := withSessionUser(ctx, store.SessionUser{
		UserID: mustDomainID(t), TenantID: tenantID, Email: "api@example.com",
		TenantName: "API Tenant", ExpiresAt: time.Now().Add(time.Hour),
		PlanCode: billing.Starter.Code, Billing: billing.State{Comped: true},
	})

	created, err := srv.CreateBusiness(session, connect.NewRequest(&opensightv1.CreateBusinessRequest{Name: "Atlas Dental"}))
	if err != nil {
		t.Fatalf("CreateBusiness: %v", err)
	}
	if len(temporal.started) != 1 || temporal.started[0].TaskQueue != "api-integration" {
		t.Fatalf("started workflows = %+v, want one on api-integration", temporal.started)
	}
	businessID, err := uuid.Parse(created.Msg.GetBusiness().GetId())
	if err != nil {
		t.Fatalf("parse business id: %v", err)
	}
	got, err := repository.GetBusiness(ctx, tenantID, businessID)
	if err != nil || got.Name != "Atlas Dental" || got.Status != store.BusinessStatusDraft {
		t.Fatalf("stored business = %+v, err=%v", got, err)
	}

	otherSession := withSessionUser(ctx, store.SessionUser{TenantID: otherTenantID, PlanCode: billing.Starter.Code})
	_, err = srv.GetBusiness(otherSession, connect.NewRequest(&opensightv1.GetBusinessRequest{BusinessId: businessID.String()}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("cross-tenant GetBusiness code = %v, want NotFound", connect.CodeOf(err))
	}
}
