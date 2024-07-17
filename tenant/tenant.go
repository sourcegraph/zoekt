package tenant

import (
	"log"
	"os"
	"path/filepath"
	"strings"

	proto "github.com/sourcegraph/zoekt/cmd/zoekt-sourcegraph-indexserver/protos/sourcegraph/zoekt/configuration/v1"
)

const IndexDirPrefix = "tenant_"

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

func ListTenantDirs(path string) []string {
	var dir []string

	files, err := os.ReadDir(path)
	if err != nil {
		log.Printf("listTenantDirs: error reading dir: %s", err)
		return nil
	}

	for _, file := range files {
		if file.IsDir() && strings.HasPrefix(file.Name(), IndexDirPrefix) {
			dir = append(dir, filepath.Join(path, file.Name()))
		}
	}
	return dir
}
