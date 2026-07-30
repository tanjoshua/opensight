package testdb

import (
	"fmt"
	"reflect"
	"testing"

	"opensight/internal/store/testsql"
)

func TestCatalogIDsHaveGeneratedMethods(t *testing.T) {
	queryType := reflect.TypeOf((*testsql.Queries)(nil))
	for query := Query001; query <= Query288; query++ {
		name := fmt.Sprintf("TestQuery%03d", query)
		if _, ok := queryType.MethodByName(name); !ok {
			t.Errorf("%s is not generated", name)
		}
	}
}
