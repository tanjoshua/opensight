package api

import (
	"opensight/internal/billing"
	opensightv1 "opensight/internal/gen/opensight/v1"
)

// accessToProto maps a derived billing.Access onto the wire enum
// (common.proto's Access, shared with AuthService.GetMe's future access
// field — design 08 "Access").
func accessToProto(a billing.Access) opensightv1.Access {
	switch a {
	case billing.AccessFull:
		return opensightv1.Access_ACCESS_FULL
	case billing.AccessLapsed:
		return opensightv1.Access_ACCESS_LAPSED
	case billing.AccessNever:
		return opensightv1.Access_ACCESS_NEVER
	default:
		return opensightv1.Access_ACCESS_UNSPECIFIED
	}
}

// planToProto maps a catalog billing.Plan onto the proto Plan message
// (common.proto — moved here from business_conv.go, BILL-6: Plan now lives
// alongside Access on GetMeResponse, not on BusinessProfile).
// billing.Plan.Code fills the wire's code field (renamed from the old slug).
func planToProto(p billing.Plan) *opensightv1.Plan {
	return &opensightv1.Plan{
		Code:        p.Code,
		PromptLimit: int32(p.PromptLimit),
		RunInterval: p.RunInterval,
		Platforms:   p.Platforms,
	}
}
