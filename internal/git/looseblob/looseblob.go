package looseblob

import (
	"fmt"
	"path"
	"regexp"
)

var objectIdRegex = regexp.MustCompile(`\A[a-f0-9]{40}\z`)

type blobPath struct {
	repoPath string
	blobId   []byte
}

func (bp *blobPath) Path() string {
	return path.Join(bp.repoPath, "objects", string(bp.blobId[:2]), string(bp.blobId[2:]))
}

func (bp *blobPath) dir() string {
	return path.Dir(bp.Path())
}

func newBlobPath(repoPath, blobId string) (*blobPath, error) {
	if ok := objectIdRegex.MatchString(blobId); !ok {
		return nil, fmt.Errorf("invalid blobId %q", blobId)
	}
	return &blobPath{repoPath, []byte(blobId)}, nil
}
