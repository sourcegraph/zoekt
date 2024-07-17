package tenant

import proto "github.com/sourcegraph/zoekt/cmd/zoekt-sourcegraph-indexserver/protos/sourcegraph/zoekt/configuration/v1"

type Tenant struct {
	// never expose this otherwise impersonation outside of this package is possible.
	_id int
}

func (t Tenant) ID() int {
	return t._id
}

func FromProto(x *proto.ZoektIndexOptions) Tenant {
	return Tenant{
		_id: int(x.TenantId),
	}
}
