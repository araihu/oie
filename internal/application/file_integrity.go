package application

import (
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"hash"
	"io"

	"github.com/araihu/oie/internal/domain"
)

func normalizeFileDigests(body string, audioURLs []string, candidates []domain.FileDigest) []domain.FileDigest {
	if len(candidates) == 0 {
		return nil
	}
	urls := map[string]bool{}
	for _, ref := range SharedReferences(body) {
		urls[ref.URL] = true
	}
	for _, url := range normalizeAudioURLs(audioURLs) {
		urls[url] = true
	}
	var digests []domain.FileDigest
	seen := map[domain.FileDigest]bool{}
	for _, digest := range candidates {
		if !urls[digest.URL] || seen[digest] || !digest.Valid() {
			continue
		}
		digests = append(digests, digest)
		seen[digest] = true
		if len(digests) == 64 {
			break
		}
	}
	return digests
}

func fileDigests(source domain.Message, address string) []domain.FileDigest {
	var expected []domain.FileDigest
	for _, digest := range normalizeFileDigests(domain.DisplayBody(source), source.AudioURLs, source.FileDigests) {
		if digest.URL == address {
			expected = append(expected, digest)
		}
	}
	return expected
}

type downloadHashes struct {
	sha256, sha512 hash.Hash
	io.Writer
}

func newDownloadHashes() downloadHashes {
	short, long := sha256.New(), sha512.New()
	return downloadHashes{sha256: short, sha512: long, Writer: io.MultiWriter(short, long)}
}

func (h downloadHashes) verify(expected []domain.FileDigest) (string, error) {
	algorithm := ""
	for _, digest := range expected {
		actual := h.sha256
		if digest.Algorithm == "sha-512" {
			actual = h.sha512
		}
		if digest.Value != hex.EncodeToString(actual.Sum(nil)) {
			return "", MediaFailure{Code: "media_integrity_mismatch"}
		}
		if algorithm == "" || digest.Algorithm == "sha-512" {
			algorithm = digest.Algorithm
		}
	}
	return algorithm, nil
}
