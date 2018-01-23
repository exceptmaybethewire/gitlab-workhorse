package file_storage

import (
	"bytes"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"hash"
	"io"
)

var supportedHashes = []string{"md5", "sha1", "sha256", "sha512"}

type multiHash struct {
	io.Writer
	hashes map[string]hash.Hash
	buffer bytes.Buffer
}

func newHash(hashName string) hash.Hash {
	switch hashName {
	case "md5":
		return md5.New()
	case "sha1":
		return sha1.New()
	case "sha256":
		return sha256.New()
	case "sha512":
		return sha512.New()
	default:
		return nil
	}
}

func newMultiHash() (m *multiHash) {
	m = &multiHash{}
	m.hashes = map[string]hash.Hash {
		"md5": md5.New(),
		"sha1": sha1.New(),
		"sha256": sha256.New(),
		"sha512": sha512.New(),
	}

	var hashes []io.Writer
	for _, hash := range m.hashes {
		hashes = append(hashes, hash)
	}
	m.Writer = io.MultiWriter(hashes...)
	return m
}

func (m *multiHash) hash(hashName string) []byte {
	hash := m.hashes[hashName]
	return hash.Sum(nil)
}

func (m *multiHash) finish() map[string]string {
	h := make(map[string]string)
	for hashName, hash := range m.hashes {
		checksum := hash.Sum(nil)
		h[hashName] = hex.EncodeToString(checksum)
	}
	return h
}
