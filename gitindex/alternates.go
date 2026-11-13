package gitindex

import (
	"errors"
	"log"
	"os"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/storage/filesystem"
	"github.com/go-git/go-git/v5/storage/filesystem/dotgit"
)

// alternatesStorage also reads objects from objects/info/alternates, as
// written by git clone --shared or --reference. go-git can't reach those
// absolute paths from our chrooted fs, and with AlternatesFS set it reloads
// every alternate pack index on each miss, so we open them once here.
type alternatesStorage struct {
	*filesystem.Storage
	alternates []*filesystem.ObjectStorage
}

func newAlternatesStorage(s *filesystem.Storage, fs billy.Filesystem, c cache.Object) *alternatesStorage {
	as := &alternatesStorage{Storage: s}
	dirs, err := dotgit.NewWithOptions(fs, dotgit.Options{AlternatesFS: osfs.New("/")}).Alternates()
	if err != nil && !os.IsNotExist(err) {
		log.Printf("ignoring objects/info/alternates: %v", err)
	}
	for _, d := range dirs {
		as.alternates = append(as.alternates, filesystem.NewObjectStorageWithOptions(d, c, filesystem.Options{KeepDescriptors: true}))
	}
	return as
}

func (s *alternatesStorage) EncodedObject(t plumbing.ObjectType, h plumbing.Hash) (plumbing.EncodedObject, error) {
	obj, err := s.Storage.EncodedObject(t, h)
	if !errors.Is(err, plumbing.ErrObjectNotFound) {
		return obj, err
	}
	for _, a := range s.alternates {
		if obj, aerr := a.EncodedObject(t, h); aerr == nil {
			return obj, nil
		}
	}
	return nil, err
}

func (s *alternatesStorage) Close() error {
	var err error
	for _, a := range s.alternates {
		err = errors.Join(err, a.Close())
	}
	return errors.Join(err, s.Storage.Close())
}
