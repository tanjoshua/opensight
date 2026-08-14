package api

import (
	"context"
	"errors"
	"strings"

	"opensight/internal/billing"
	opensightv1 "opensight/internal/gen/opensight/v1"
	"opensight/internal/gen/opensight/v1/opensightv1connect"
	"opensight/internal/store"

	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ opensightv1connect.AccountServiceHandler = (*Server)(nil)

const compedAccountAdminEmail = "jtanjoshua@gmail.com"

func canCreateCompedAccount(email string) bool {
	return strings.EqualFold(strings.TrimSpace(email), compedAccountAdminEmail)
}

func accountRoleToProto(role store.AccountRole) opensightv1.AccountRole {
	switch role {
	case store.AccountRoleOwner:
		return opensightv1.AccountRole_ACCOUNT_ROLE_OWNER
	case store.AccountRoleAdmin:
		return opensightv1.AccountRole_ACCOUNT_ROLE_ADMIN
	case store.AccountRoleMember:
		return opensightv1.AccountRole_ACCOUNT_ROLE_MEMBER
	case store.AccountRoleViewer:
		return opensightv1.AccountRole_ACCOUNT_ROLE_VIEWER
	default:
		return opensightv1.AccountRole_ACCOUNT_ROLE_UNSPECIFIED
	}
}

func accountRoleFromProto(role opensightv1.AccountRole) (store.AccountRole, *connect.Error) {
	switch role {
	case opensightv1.AccountRole_ACCOUNT_ROLE_OWNER:
		return store.AccountRoleOwner, nil
	case opensightv1.AccountRole_ACCOUNT_ROLE_ADMIN:
		return store.AccountRoleAdmin, nil
	case opensightv1.AccountRole_ACCOUNT_ROLE_MEMBER:
		return store.AccountRoleMember, nil
	case opensightv1.AccountRole_ACCOUNT_ROLE_VIEWER:
		return store.AccountRoleViewer, nil
	default:
		return "", rpcInvalidArgument("role is required")
	}
}

func membershipToProto(m store.AccountMembership) *opensightv1.AccountMember {
	return &opensightv1.AccountMember{UserId: m.UserID.String(), Email: m.Email, Role: accountRoleToProto(m.Role), CreatedAt: timestamppb.New(m.CreatedAt), Pending: m.Pending}
}

func (s *Server) GetAccountContext(ctx context.Context, _ *connect.Request[opensightv1.GetAccountContextRequest]) (*connect.Response[opensightv1.GetAccountContextResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "account context")
	if cerr != nil {
		return nil, cerr
	}
	access, ok := accessFromContext(ctx)
	if !ok {
		return nil, s.rpcInternal("account context: missing access", errors.New("missing access context"))
	}
	businesses, err := s.store.ListBusinesses(ctx, su.AccountID)
	if err != nil {
		return nil, s.rpcError("account context: list businesses", err)
	}
	plan, err := billing.PlanFor(su.PlanCode)
	if err != nil {
		return nil, s.rpcInternal("account context: plan", err)
	}
	resp := &opensightv1.GetAccountContextResponse{
		Account: &opensightv1.Account{Id: su.AccountID.String(), Name: su.AccountName, Slug: su.AccountSlug},
		Role:    accountRoleToProto(su.Role), Access: accessToProto(access), Plan: planToProto(plan),
		Businesses: make([]*opensightv1.BusinessSummary, 0, len(businesses)),
	}
	for _, b := range businesses {
		resp.Businesses = append(resp.Businesses, &opensightv1.BusinessSummary{Id: b.ID.String(), Name: b.Name, Status: businessStatusToProto(b.Status)})
	}
	return connect.NewResponse(resp), nil
}

func (s *Server) CreateAccount(ctx context.Context, req *connect.Request[opensightv1.CreateAccountRequest]) (*connect.Response[opensightv1.CreateAccountResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "create account")
	if cerr != nil {
		return nil, cerr
	}
	name := strings.TrimSpace(req.Msg.GetName())
	if name == "" {
		return nil, rpcInvalidArgument("name is required")
	}
	m, err := s.store.CreateNamedAccount(ctx, store.CreateNamedAccountParams{
		UserID: su.UserID,
		Name:   name,
		Comped: canCreateCompedAccount(su.Email),
	})
	if err != nil {
		return nil, s.rpcError("create account", err)
	}
	return connect.NewResponse(&opensightv1.CreateAccountResponse{Membership: &opensightv1.AccountMembershipSummary{Account: &opensightv1.Account{Id: m.Account.ID.String(), Name: m.Account.Name, Slug: m.Account.Slug}, Role: accountRoleToProto(m.Role)}}), nil
}

func (s *Server) ListMembers(ctx context.Context, _ *connect.Request[opensightv1.ListMembersRequest]) (*connect.Response[opensightv1.ListMembersResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "list members")
	if cerr != nil {
		return nil, cerr
	}
	members, err := s.store.ListAccountMembers(ctx, su.AccountID)
	if err != nil {
		return nil, s.rpcError("list members", err)
	}
	resp := &opensightv1.ListMembersResponse{Members: make([]*opensightv1.AccountMember, 0, len(members))}
	for _, m := range members {
		resp.Members = append(resp.Members, membershipToProto(m))
	}
	return connect.NewResponse(resp), nil
}

func (s *Server) AddMember(ctx context.Context, req *connect.Request[opensightv1.AddMemberRequest]) (*connect.Response[opensightv1.AddMemberResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "add member")
	if cerr != nil {
		return nil, cerr
	}
	role, cerr := accountRoleFromProto(req.Msg.GetRole())
	if cerr != nil {
		return nil, cerr
	}
	if su.Role != store.AccountRoleOwner && role == store.AccountRoleOwner {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("only owners can add owners"))
	}
	email := strings.ToLower(strings.TrimSpace(req.Msg.GetEmail()))
	local, host, ok := strings.Cut(email, "@")
	if !ok || local == "" || host == "" {
		return nil, rpcInvalidArgument("a valid email is required")
	}
	m, err := s.store.AddAccountMember(ctx, store.AddAccountMemberParams{AccountID: su.AccountID, Email: email, Role: role})
	if err != nil {
		return nil, s.rpcError("add member", err)
	}
	return connect.NewResponse(&opensightv1.AddMemberResponse{Member: membershipToProto(m)}), nil
}

func (s *Server) UpdateMemberRole(ctx context.Context, req *connect.Request[opensightv1.UpdateMemberRoleRequest]) (*connect.Response[opensightv1.UpdateMemberRoleResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "update member role")
	if cerr != nil {
		return nil, cerr
	}
	userID, cerr := rpcID("user_id", req.Msg.GetUserId())
	if cerr != nil {
		return nil, cerr
	}
	role, cerr := accountRoleFromProto(req.Msg.GetRole())
	if cerr != nil {
		return nil, cerr
	}
	current, err := s.store.GetAccountMember(ctx, su.AccountID, userID)
	if err != nil {
		return nil, s.rpcError("update member role", err)
	}
	if su.Role != store.AccountRoleOwner && (current.Role == store.AccountRoleOwner || role == store.AccountRoleOwner) {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("only owners can manage owners"))
	}
	if err := s.store.UpdateAccountMemberRole(ctx, su.AccountID, userID, role); err != nil {
		return nil, s.rpcError("update member role", err)
	}
	members, err := s.store.ListAccountMembers(ctx, su.AccountID)
	if err != nil {
		return nil, s.rpcError("update member role: reload", err)
	}
	for _, m := range members {
		if m.UserID == userID {
			return connect.NewResponse(&opensightv1.UpdateMemberRoleResponse{Member: membershipToProto(m)}), nil
		}
	}
	return nil, s.rpcError("update member role", store.ErrNotFound)
}

func (s *Server) RemoveMember(ctx context.Context, req *connect.Request[opensightv1.RemoveMemberRequest]) (*connect.Response[opensightv1.RemoveMemberResponse], error) {
	su, cerr := s.rpcSessionUser(ctx, "remove member")
	if cerr != nil {
		return nil, cerr
	}
	userID, cerr := rpcID("user_id", req.Msg.GetUserId())
	if cerr != nil {
		return nil, cerr
	}
	current, err := s.store.GetAccountMember(ctx, su.AccountID, userID)
	if err != nil {
		return nil, s.rpcError("remove member", err)
	}
	if su.Role != store.AccountRoleOwner && current.Role == store.AccountRoleOwner {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("only owners can remove owners"))
	}
	if err := s.store.RemoveAccountMember(ctx, su.AccountID, userID); err != nil {
		return nil, s.rpcError("remove member", err)
	}
	return connect.NewResponse(&opensightv1.RemoveMemberResponse{}), nil
}
