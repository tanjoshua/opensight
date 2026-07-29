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
