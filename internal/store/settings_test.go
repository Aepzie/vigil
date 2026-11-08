package store

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSettingsValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Settings)
		field  string
	}{
		{name: "defaults", mutate: func(*Settings) {}},
		{name: "bounds low", mutate: func(s *Settings) {
			s.PollInterval, s.HistoryMinutes, s.OfflineGrace = time.Second, 1, 0
			s.CPUWarn, s.CPUCrit = 1, 2
		}},
		{name: "bounds high", mutate: func(s *Settings) {
			s.PollInterval, s.HistoryMinutes, s.OfflineGrace = time.Minute, 240, 10*time.Minute
			s.DiskWarn, s.DiskCrit = 99.5, 100
		}},
		{name: "compat ascii", mutate: func(s *Settings) { s.Compat = "ascii" }},

		{name: "poll too fast", mutate: func(s *Settings) { s.PollInterval = 500 * time.Millisecond }, field: "poll_interval"},
		{name: "poll too slow", mutate: func(s *Settings) { s.PollInterval = 61 * time.Second }, field: "poll_interval"},
		{name: "history zero", mutate: func(s *Settings) { s.HistoryMinutes = 0 }, field: "history_minutes"},
		{name: "history too long", mutate: func(s *Settings) { s.HistoryMinutes = 241 }, field: "history_minutes"},
		{name: "cpu warn zero", mutate: func(s *Settings) { s.CPUWarn = 0 }, field: "cpu_warn"},
		{name: "cpu crit over 100", mutate: func(s *Settings) { s.CPUCrit = 101 }, field: "cpu_crit"},
		{name: "mem warn equals crit", mutate: func(s *Settings) { s.MemWarn, s.MemCrit = 90, 90 }, field: "mem_warn"},
		{name: "disk warn above crit", mutate: func(s *Settings) { s.DiskWarn, s.DiskCrit = 96, 90 }, field: "disk_warn"},
		{name: "negative grace", mutate: func(s *Settings) { s.OfflineGrace = -time.Second }, field: "offline_grace"},
		{name: "grace too long", mutate: func(s *Settings) { s.OfflineGrace = 11 * time.Minute }, field: "offline_grace"},
		{name: "bad compat", mutate: func(s *Settings) { s.Compat = "fancy" }, field: "compat"},
		{name: "empty compat", mutate: func(s *Settings) { s.Compat = "" }, field: "compat"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			set := DefaultSettings()
			tt.mutate(&set)
			err := set.Validate()
			if tt.field == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			var fe *FieldError
			if !errors.As(err, &fe) || fe.Field != tt.field {
				t.Fatalf("Validate() = %v, want field %q", err, tt.field)
			}
			if !strings.HasPrefix(err.Error(), tt.field+": ") {
				t.Errorf("error %q lacks field prefix", err)
			}
		})
	}
}

func TestDefaultSettingsValid(t *testing.T) {
	if err := DefaultSettings().Validate(); err != nil {
		t.Fatal(err)
	}
}
