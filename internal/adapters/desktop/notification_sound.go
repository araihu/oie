package desktop

import "encoding/binary"

// The web view sends only the normalized, at most two-second mono PCM clip.
func validNotificationWAV(wav []byte) bool {
	const headerSize = 44
	const maxDataSize = 2 * 24000 * 2
	size := len(wav)
	if size <= headerSize || size > headerSize+maxDataSize || size%2 != 0 {
		return false
	}
	return string(wav[:4]) == "RIFF" &&
		binary.LittleEndian.Uint32(wav[4:8]) == uint32(size-8) &&
		string(wav[8:16]) == "WAVEfmt " &&
		binary.LittleEndian.Uint32(wav[16:20]) == 16 &&
		binary.LittleEndian.Uint16(wav[20:22]) == 1 &&
		binary.LittleEndian.Uint16(wav[22:24]) == 1 &&
		binary.LittleEndian.Uint32(wav[24:28]) == 24000 &&
		binary.LittleEndian.Uint32(wav[28:32]) == 48000 &&
		binary.LittleEndian.Uint16(wav[32:34]) == 2 &&
		binary.LittleEndian.Uint16(wav[34:36]) == 16 &&
		string(wav[36:40]) == "data" &&
		binary.LittleEndian.Uint32(wav[40:44]) == uint32(size-headerSize)
}
