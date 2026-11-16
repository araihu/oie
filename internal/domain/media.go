package domain

import (
	"encoding/hex"
	"strings"
	"time"
)

// PlayableAudioType returns a MIME type safe to serve to a native audio player.
func PlayableAudioType(mediaType string) string {
	switch mediaType {
	case "audio/mpeg", "audio/ogg", "audio/mp4", "audio/wav", "audio/flac", "audio/aac", "audio/webm":
		return mediaType
	case "audio/x-wav", "audio/wave", "audio/vnd.wave":
		return "audio/wav"
	case "audio/x-m4a":
		return "audio/mp4"
	case "audio/x-flac":
		return "audio/flac"
	case "audio/opus":
		return "audio/ogg"
	}
	return ""
}

// FileDigest is sender metadata for the original at an exact URL.
// Value is the lowercase hexadecimal digest for Algorithm.
type FileDigest struct {
	URL, Algorithm, Value string
}

func (d FileDigest) Valid() bool {
	size := 0
	switch d.Algorithm {
	case "sha-256":
		size = 64
	case "sha-512":
		size = 128
	}
	if size == 0 || len(d.Value) != size || strings.ToLower(d.Value) != d.Value {
		return false
	}
	_, err := hex.DecodeString(d.Value)
	return err == nil
}

type Download struct {
	DeviceID, SourceID, SourceRevision      ID
	Kind, Hash, MimeType, VerifiedAlgorithm string
	Size                                    int64
	DownloadedAt                            time.Time
}

type DownloadPolicy struct {
	Days, Count int
	Generation  int64
}

type DownloadGarbage struct {
	Kind, Hash string
}
