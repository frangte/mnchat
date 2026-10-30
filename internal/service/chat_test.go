package service

import (
	"errors"
	"testing"

	"github.com/frangte/mnchat/internal/domain"
)

func TestNormalizeChannelName(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "general", want: "general"},
		{in: "  #Team Sync ", want: "team-sync"},
		{in: "release_2026", want: "release_2026"},
		{in: "", wantErr: true},
		{in: "#", wantErr: true},
		{in: "-leading-dash", wantErr: true},
		{in: "bad!name", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := normalizeChannelName(tt.in)
			if tt.wantErr {
				if !errors.Is(err, domain.ErrInvalid) {
					t.Fatalf("normalizeChannelName(%q) err = %v, want ErrInvalid", tt.in, err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("normalizeChannelName(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
			}
		})
	}
}
