package api

import (
	"testing"

	_ "opensight/internal/gen/opensight/v1"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestRPCPaging(t *testing.T) {
	const defaultLimit, maxLimit = 50, 100
	cases := []struct {
		name          string
		limit, offset int32
		wantL, wantO  int
	}{
		{"limit zero defaults", 0, 0, defaultLimit, 0},
		{"negative limit defaults", -5, 0, defaultLimit, 0},
		{"over-max limit clamps", maxLimit + 1, 0, maxLimit, 0},
		{"in-range limit passes through", 7, 0, 7, 0},
		{"negative offset floors to zero", 0, -1, defaultLimit, 0},
		{"positive offset passes through", 0, 3, defaultLimit, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			limit, offset := rpcPaging(tc.limit, tc.offset, defaultLimit, maxLimit)
			if limit != tc.wantL || offset != tc.wantO {
				t.Fatalf("rpcPaging(%d, %d) = (%d, %d), want (%d, %d)", tc.limit, tc.offset, limit, offset, tc.wantL, tc.wantO)
			}
		})
	}
}

func TestCanCreateCompedAccount(t *testing.T) {
	for _, email := range []string{"jtanjoshua@gmail.com", " JTANJOSHUA@GMAIL.COM ", "liyicheng513@gmail.com"} {
		if !canCreateCompedAccount(email) {
			t.Errorf("canCreateCompedAccount(%q) = false, want true", email)
		}
	}
	if canCreateCompedAccount("someone@example.com") {
		t.Fatal("canCreateCompedAccount(non-admin) = true, want false")
	}
}

func TestNoRPCIsSideEffectFree(t *testing.T) {
	checked := 0
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if fd.Package() != "opensight.v1" {
			return true
		}
		services := fd.Services()
		for i := 0; i < services.Len(); i++ {
			methods := services.Get(i).Methods()
			for j := 0; j < methods.Len(); j++ {
				method := methods.Get(j)
				checked++
				opts, ok := method.Options().(*descriptorpb.MethodOptions)
				if ok && opts != nil && opts.GetIdempotencyLevel() == descriptorpb.MethodOptions_NO_SIDE_EFFECTS {
					t.Errorf("%s is annotated NO_SIDE_EFFECTS, which bypasses the CSRF guard", method.FullName())
				}
			}
		}
		return true
	})
	if checked == 0 {
		t.Fatal("no opensight.v1 RPC methods found")
	}
}

func TestEveryProcedureIsClassified(t *testing.T) {
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
				procedure := "/" + string(svc.FullName()) + "/" + string(method.Name())
				if _, ok := procedureAccess[procedure]; !ok {
					t.Errorf("%s is not classified in procedureAccess", procedure)
				}
			}
		}
		return true
	})
	if checked == 0 {
		t.Fatal("no opensight.v1 RPC methods found")
	}
	if len(procedureAccess) != checked {
		t.Errorf("procedureAccess has %d entries, want %d", len(procedureAccess), checked)
	}
}
