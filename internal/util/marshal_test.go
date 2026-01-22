package util

import (
	"testing"
	"time"
)

func TestMarshalTime(t *testing.T) {
	tests := []struct {
		name string
		time time.Time
	}{
		{
			name: "zero time",
			time: time.Time{},
		},
		{
			name: "unix epoch",
			time: time.Unix(0, 0),
		},
		{
			name: "specific time",
			time: time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
		},
		{
			name: "time with nanoseconds",
			time: time.Date(2024, 1, 15, 10, 30, 0, 123456789, time.UTC),
		},
		{
			name: "current time",
			time: time.Now(),
		},
		{
			name: "far future",
			time: time.Date(2099, 12, 31, 23, 59, 59, 999999999, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bytes := MarshalTime(tt.time)

			// Check length
			if len(bytes) != 8 {
				t.Errorf("MarshalTime() returned %d bytes, want 8", len(bytes))
			}
		})
	}
}

func TestUnmarshalTime(t *testing.T) {
	tests := []struct {
		name  string
		bytes []byte
		want  int64 // UnixNano
	}{
		{
			name:  "zero",
			bytes: []byte{0, 0, 0, 0, 0, 0, 0, 0},
			want:  0,
		},
		{
			name:  "one nanosecond",
			bytes: []byte{0, 0, 0, 0, 0, 0, 0, 1},
			want:  1,
		},
		{
			name:  "one second",
			bytes: []byte{0, 0, 0, 0, 0x3B, 0x9A, 0xCA, 0x00}, // 1_000_000_000 in big-endian
			want:  1_000_000_000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := UnmarshalTime(tt.bytes)
			if result.UnixNano() != tt.want {
				t.Errorf("UnmarshalTime() = %d, want %d", result.UnixNano(), tt.want)
			}
		})
	}
}

func TestMarshalUnmarshalTime_Roundtrip(t *testing.T) {
	// Test that marshal -> unmarshal produces the same time (at nanosecond precision)
	testTimes := []time.Time{
		time.Unix(0, 0),
		time.Unix(1, 0),
		time.Unix(1000000000, 0),
		time.Unix(1705315800, 123456789),
		time.Now(),
		time.Date(2024, 1, 15, 10, 30, 0, 123456789, time.UTC),
	}

	for _, original := range testTimes {
		t.Run(original.String(), func(t *testing.T) {
			bytes := MarshalTime(original)
			result := UnmarshalTime(bytes)

			// Compare at nanosecond precision (ignore location)
			if original.UnixNano() != result.UnixNano() {
				t.Errorf("Roundtrip failed: %d -> %d", original.UnixNano(), result.UnixNano())
			}
		})
	}
}

func TestMarshalTime_ByteOrder(t *testing.T) {
	// Test that bytes are in big-endian order
	tm := time.Unix(0, 0x0102030405060708)
	bytes := MarshalTime(tm)

	expected := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	for i, b := range bytes {
		if b != expected[i] {
			t.Errorf("Byte %d: got 0x%02X, want 0x%02X", i, b, expected[i])
		}
	}
}

func TestUnmarshalTime_ByteOrder(t *testing.T) {
	// Test that bytes are read in big-endian order
	bytes := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	result := UnmarshalTime(bytes)

	expected := int64(0x0102030405060708)
	if result.UnixNano() != expected {
		t.Errorf("UnmarshalTime() = %d (0x%X), want %d (0x%X)",
			result.UnixNano(), result.UnixNano(), expected, expected)
	}
}

func TestMarshalTime_NegativeTime(t *testing.T) {
	// Time before Unix epoch (negative UnixNano)
	// This tests edge case handling
	tm := time.Unix(-1, 0) // 1 second before epoch
	bytes := MarshalTime(tm)

	// Should still produce 8 bytes
	if len(bytes) != 8 {
		t.Errorf("MarshalTime() returned %d bytes for negative time, want 8", len(bytes))
	}

	// Roundtrip should work
	result := UnmarshalTime(bytes)
	if tm.UnixNano() != result.UnixNano() {
		t.Errorf("Roundtrip for negative time failed: %d != %d", tm.UnixNano(), result.UnixNano())
	}
}

func BenchmarkMarshalTime(b *testing.B) {
	tm := time.Now()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = MarshalTime(tm)
	}
}

func BenchmarkUnmarshalTime(b *testing.B) {
	bytes := MarshalTime(time.Now())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = UnmarshalTime(bytes)
	}
}

func BenchmarkMarshalUnmarshalRoundtrip(b *testing.B) {
	tm := time.Now()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bytes := MarshalTime(tm)
		_ = UnmarshalTime(bytes)
	}
}
