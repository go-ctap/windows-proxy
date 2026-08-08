package main

import (
	"errors"
	"testing"

	ghid "github.com/telesma-app/hid"
)

func TestIsFIDOEvent(t *testing.T) {
	tests := []struct {
		name  string
		event ghid.DeviceEvent
		want  bool
	}{
		{
			name:  "unknown type",
			event: ghid.DeviceEvent{Type: ghid.DeviceEventType("changed")},
		},
		{
			name: "metadata error",
			event: ghid.DeviceEvent{
				Type:        ghid.DeviceEventConnected,
				MetadataErr: errors.New("metadata unavailable"),
			},
			want: true,
		},
		{
			name:  "missing metadata",
			event: ghid.DeviceEvent{Type: ghid.DeviceEventDisconnected},
			want:  true,
		},
		{
			name: "FIDO device",
			event: ghid.DeviceEvent{
				Type: ghid.DeviceEventConnected,
				DeviceInfo: &ghid.DeviceInfo{
					UsagePage: 0xf1d0,
					Usage:     0x01,
				},
			},
			want: true,
		},
		{
			name: "non-FIDO device",
			event: ghid.DeviceEvent{
				Type: ghid.DeviceEventConnected,
				DeviceInfo: &ghid.DeviceInfo{
					UsagePage: 0x01,
					Usage:     0x02,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isFIDOEvent(tt.event); got != tt.want {
				t.Fatalf("isFIDOEvent() = %v, want %v", got, tt.want)
			}
		})
	}
}
