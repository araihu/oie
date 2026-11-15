package desktop

import (
	"encoding/binary"
	"testing"
)

func TestValidNotificationWAV(t *testing.T) {
	const frames = 2 * 24000
	wav := make([]byte, 44+2*frames)
	copy(wav, "RIFF\x00\x00\x00\x00WAVEfmt \x10\x00\x00\x00\x01\x00\x01\x00\xc0\x5d\x00\x00\x80\xbb\x00\x00\x02\x00\x10\x00data")
	binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
	binary.LittleEndian.PutUint32(wav[40:], 2*frames)
	if !validNotificationWAV(wav) {
		t.Fatal("rejected a two-second mono PCM clip")
	}
	for _, bad := range [][]byte{wav[:44], wav[:len(wav)-1], append(append([]byte(nil), wav...), 0, 0)} {
		if validNotificationWAV(bad) {
			t.Fatal("accepted an invalid clip size")
		}
	}
	wav[22] = 2
	if validNotificationWAV(wav) {
		t.Fatal("accepted a non-mono header")
	}
}
