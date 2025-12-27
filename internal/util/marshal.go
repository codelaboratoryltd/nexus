package util

import (
	"encoding/binary"
	"time"
)

// UnmarshalTime deserializes a time.Time from bytes.
func UnmarshalTime(v []byte) time.Time {
	return time.Unix(0, int64(binary.BigEndian.Uint64(v)))
}

// MarshalTime serializes a time.Time to bytes.
func MarshalTime(t time.Time) []byte {
	const timestampBytesCount = 8
	timestampBytes := make([]byte, timestampBytesCount)
	binary.BigEndian.PutUint64(timestampBytes, uint64(t.UnixNano()))
	return timestampBytes
}
